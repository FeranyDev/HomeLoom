import assert from "node:assert/strict";
import { createServer, type Server } from "node:net";
import { afterEach, test } from "node:test";
import {
  connectJsonRpcPeer,
  JsonRpcErrorCode,
  JsonRpcPeer,
  RpcRemoteError,
  RpcRequestTimeoutError,
} from "../src/ipc/json-rpc.js";
import { temporarySocket } from "./helpers.js";

const cleanups: Array<() => Promise<void>> = [];

afterEach(async () => {
  for (const cleanup of cleanups.splice(0).reverse()) {
    await cleanup();
  }
});

test("JSON-RPC peer supports requests in both directions", async () => {
  const pair = await createPeerPair();
  pair.serverPeer.register("math.add", (params) => {
    const values = params as { left: number; right: number };
    return values.left + values.right;
  });
  pair.clientPeer.register("host.describe", () => ({ name: "go-host" }));

  assert.equal(await pair.clientPeer.request("math.add", { left: 2, right: 3 }), 5);
  assert.deepEqual(await pair.serverPeer.request("host.describe", {}), { name: "go-host" });
});

test("request IDs time out and late responses are ignored", async () => {
  const pair = await createPeerPair();
  pair.serverPeer.register("slow", async () => {
    await new Promise<void>((resolve) => setTimeout(resolve, 80));
    return "late";
  });

  await assert.rejects(
    pair.clientPeer.request("slow", {}, 15),
    RpcRequestTimeoutError,
  );
  await new Promise<void>((resolve) => setTimeout(resolve, 100));
  assert.equal(pair.clientPeer.closed, false);
});

test("burst traffic is rejected once the bounded inbound queue is full", async () => {
  let release: (() => void) | undefined;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const pair = await createPeerPair({ serverInboundCapacity: 3 });
  pair.serverPeer.register("blocked", async () => {
    await gate;
    return { ok: true };
  });

  const requests = Array.from({ length: 20 }, (_, index) =>
    pair.clientPeer.request("blocked", { index }, 2_000));
  const settled = Promise.allSettled(requests);
  await new Promise<void>((resolve) => setTimeout(resolve, 20));
  release?.();
  const results = await settled;
  const overloaded = results.filter(
    (result) => result.status === "rejected" &&
      result.reason instanceof RpcRemoteError &&
      result.reason.code === JsonRpcErrorCode.overloaded,
  );

  assert.ok(overloaded.length > 0);
  assert.ok(pair.serverPeer.queueStats.inboundMaxDepth <= 3);
  assert.equal(pair.serverPeer.queueStats.inboundRejected, overloaded.length);
});

async function createPeerPair(options: { serverInboundCapacity?: number } = {}): Promise<{
  serverPeer: JsonRpcPeer;
  clientPeer: JsonRpcPeer;
}> {
  const temporary = await temporarySocket("json-rpc");
  const server: Server = createServer();
  const accepted = new Promise<JsonRpcPeer>((resolve) => {
    server.on("connection", (socket) => resolve(new JsonRpcPeer(socket, {
      maxInboundQueue: options.serverInboundCapacity ?? 32,
      maxOutboundQueue: 32,
    })));
  });
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(temporary.socketPath, resolve);
  });
  const clientPeer = await connectJsonRpcPeer(temporary.socketPath, {
    maxInboundQueue: 32,
    maxOutboundQueue: 64,
  });
  const serverPeer = await accepted;
  cleanups.push(async () => {
    clientPeer.close();
    serverPeer.close();
    await new Promise<void>((resolve) => server.close(() => resolve()));
    await temporary.cleanup();
  });
  return { serverPeer, clientPeer };
}
