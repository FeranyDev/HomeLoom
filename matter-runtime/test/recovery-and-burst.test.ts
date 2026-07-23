import assert from "node:assert/strict";
import { afterEach, test } from "node:test";
import { ReconnectingRuntimeClient } from "../src/ipc/reconnecting-client.js";
import { JsonRpcErrorCode, RpcRemoteError } from "../src/ipc/json-rpc.js";
import { FakeProtocolAdapter } from "../src/runtime/fake-adapter.js";
import { RuntimeServer } from "../src/runtime/server.js";
import { bridgeConfiguration, switchDevice, temporarySocket } from "./helpers.js";

const cleanups: Array<() => Promise<void>> = [];

afterEach(async () => {
  for (const cleanup of cleanups.splice(0).reverse()) {
    await cleanup();
  }
});

test("a new sidecar receives the complete desired snapshot after disconnect", async () => {
  const temporary = await temporarySocket("recovery");
  const firstAdapter = new FakeProtocolAdapter();
  const firstServer = new RuntimeServer(firstAdapter, {
    socketPath: temporary.socketPath,
    targetId: "matter-recovery",
  });
  const client = new ReconnectingRuntimeClient({
    socketPath: temporary.socketPath,
    targetId: "matter-recovery",
    requestTimeoutMs: 3_000,
    reconnectMinimumMs: 10,
    reconnectMaximumMs: 50,
    initialState: {
      revision: 1,
      bridge: bridgeConfiguration("matter-recovery", {
        listenPort: 5_550,
        discriminator: 2_001,
      }),
      devices: [switchDevice("old-device")],
    },
  });
  await firstServer.start();
  client.start();
  await client.waitForReady();
  const firstInstanceId = client.runtimeInstanceId;
  assert.deepEqual(firstAdapter.devices.map((device) => device.id), ["old-device"]);

  await firstServer.stop();
  await client.waitForDisconnected();
  const replaceWhileDisconnected = client.replaceDevices([
    switchDevice("new-device", 7, true),
  ]);

  const secondAdapter = new FakeProtocolAdapter();
  const secondServer = new RuntimeServer(secondAdapter, {
    socketPath: temporary.socketPath,
    targetId: "matter-recovery",
  });
  await secondServer.start();
  cleanups.push(async () => {
    await client.stop();
    await secondServer.stop();
    await temporary.cleanup();
  });

  await replaceWhileDisconnected;
  await client.waitForReady();
  assert.notEqual(client.runtimeInstanceId, firstInstanceId);
  assert.ok(secondAdapter.replayCount >= 1);
  assert.equal(secondAdapter.configuration?.listenPort, 5_550);
  assert.deepEqual(secondAdapter.devices.map((device) => device.id), ["new-device"]);
  assert.equal(secondAdapter.devices[0]?.attributes["OnOff.OnOff"], true);
});

test("attribute bursts stay bounded and report overload instead of growing forever", async () => {
  const temporary = await temporarySocket("burst");
  const adapter = new FakeProtocolAdapter({ operationDelayMs: 25 });
  const server = new RuntimeServer(adapter, {
    socketPath: temporary.socketPath,
    targetId: "matter-burst",
    maxInboundQueue: 4,
    maxOutboundQueue: 64,
    requestTimeoutMs: 2_000,
  });
  const client = new ReconnectingRuntimeClient({
    socketPath: temporary.socketPath,
    targetId: "matter-burst",
    requestTimeoutMs: 2_000,
    maxOutboundQueue: 64,
    initialState: {
      revision: 1,
      bridge: bridgeConfiguration("matter-burst"),
      devices: [switchDevice("burst-device")],
    },
  });
  await server.start();
  client.start();
  cleanups.push(async () => {
    await client.stop();
    await server.stop();
    await temporary.cleanup();
  });
  await client.waitForReady();

  const results = await Promise.allSettled(
    Array.from({ length: 40 }, (_, index) => client.updateAttributes([{
      deviceId: "burst-device",
      path: "LevelControl.CurrentLevel",
      value: index,
      version: index,
    }])),
  );
  const rejected = results.filter(
    (result) => result.status === "rejected" &&
      result.reason instanceof RpcRemoteError &&
      result.reason.code === JsonRpcErrorCode.overloaded,
  );
  const status = await client.getStatus();

  assert.ok(rejected.length > 0);
  assert.equal(status.queue.inboundRejected, rejected.length);
  assert.ok(status.queue.inboundDepth <= 4);
});
