import assert from "node:assert/strict";
import { afterEach, test } from "node:test";
import {
  IPC_PROTOCOL_VERSION,
  RpcMethod,
  type CommissioningChangedEvent,
  type FabricChangedEvent,
  type RuntimeDiagnosticsEvent,
} from "../src/contract.js";
import {
  MemoryHostStorage,
  ReconnectingRuntimeClient,
} from "../src/ipc/reconnecting-client.js";
import {
  connectJsonRpcPeer,
  JsonRpcErrorCode,
  RpcRemoteError,
} from "../src/ipc/json-rpc.js";
import { FakeProtocolAdapter } from "../src/runtime/fake-adapter.js";
import { RuntimeServer } from "../src/runtime/server.js";
import { bridgeConfiguration, eventually, switchDevice, temporarySocket } from "./helpers.js";

const cleanups: Array<() => Promise<void>> = [];

afterEach(async () => {
  for (const cleanup of cleanups.splice(0).reverse()) {
    await cleanup();
  }
});

test("sidecar contract covers replay, deltas, reverse control, commissioning and storage", async () => {
  const temporary = await temporarySocket("runtime-contract");
  const adapter = new FakeProtocolAdapter();
  let factoryResetCompleted = 0;
  const server = new RuntimeServer(adapter, {
    socketPath: temporary.socketPath,
    targetId: "matter-a",
    requestTimeoutMs: 1_000,
    onFactoryResetComplete: () => {
      factoryResetCompleted += 1;
    },
  });
  const storage = new MemoryHostStorage();
  const commissioningEvents: CommissioningChangedEvent[] = [];
  const fabricEvents: FabricChangedEvent[] = [];
  const diagnosticsEvents: RuntimeDiagnosticsEvent[] = [];
  const attributeWrites: string[] = [];
  const commands: string[] = [];
  const configuration = bridgeConfiguration("matter-a");
  const client = new ReconnectingRuntimeClient({
    socketPath: temporary.socketPath,
    targetId: "matter-a",
    requestTimeoutMs: 1_000,
    initialState: {
      revision: 7,
      bridge: configuration,
      devices: [switchDevice("lamp-a")],
    },
    host: {
      storage,
      onAttributeWrite: (event) => {
        attributeWrites.push(event.interactionId);
        return { accepted: true, message: "queued by Go" };
      },
      onCommand: (event) => {
        commands.push(event.interactionId);
        return { accepted: true, fields: { status: "ok" } };
      },
      onCommissioningChanged: (event) => {
        commissioningEvents.push(event);
      },
      onFabricChanged: (event) => {
        fabricEvents.push(event);
      },
      onDiagnostics: (event) => {
        diagnosticsEvents.push(event);
      },
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
  assert.equal(adapter.replayCount, 1);
  assert.equal(adapter.lastReplayRevision, 7);
  assert.equal(adapter.configuration?.listenPort, 5_540);
  assert.deepEqual(adapter.devices.map((device) => device.id), ["lamp-a"]);

  await client.updateAttributes([{
    deviceId: "lamp-a",
    path: "OnOff.OnOff",
    value: true,
    version: 1,
  }]);
  await client.updateReachability([{ deviceId: "lamp-a", reachable: false }]);
  assert.equal(adapter.devices[0]?.attributes["OnOff.OnOff"], true);
  assert.equal(adapter.devices[0]?.reachable, false);

  const window = await client.requestCommissioningWindow(120);
  assert.equal(window.open, true);
  assert.ok(window.expiresAt?.endsWith("Z"));
  await eventually(() => commissioningEvents.some((event) => event.reason === "window-opened"));
  assert.deepEqual(await client.closeCommissioningWindow(), { open: false, expiresAt: null });

  await adapter.addFabricForTest("fabric-1", "Primary home");
  await eventually(() => fabricEvents.some((event) => event.change === "added"));
  assert.deepEqual((await client.getStatus()).fabrics, [{ id: "fabric-1", label: "Primary home" }]);
  await client.removeFabric("fabric-1");
  await eventually(() => fabricEvents.some((event) => event.change === "removed"));

  const writeResult = await adapter.emitAttributeWriteForTest({
    deviceId: "lamp-a",
    endpointId: 2,
    path: "OnOff.OnOff",
    value: false,
    interactionId: "write-1",
  });
  assert.deepEqual(writeResult, { accepted: true, message: "queued by Go" });
  const commandResult = await adapter.emitCommandForTest({
    deviceId: "lamp-a",
    endpointId: 2,
    path: "OnOff.Toggle",
    fields: {},
    interactionId: "command-1",
  });
  assert.deepEqual(commandResult, { accepted: true, fields: { status: "ok" } });
  assert.deepEqual(attributeWrites, ["write-1"]);
  assert.deepEqual(commands, ["command-1"]);

  await adapter.emitDiagnosticsForTest({
    level: "warning",
    code: "test.metric",
    message: "contract diagnostic",
    metrics: { queueDepth: 2 },
  });
  await eventually(() => diagnosticsEvents.length === 1);
  assert.equal(diagnosticsEvents[0]?.metrics.queueDepth, 2);

  const statusBeforeReset = await client.getStatus();
  assert.equal(statusBeforeReset.deviceCount, 1);
  assert.equal(statusBeforeReset.fabricCount, 0);
  assert.deepEqual(statusBeforeReset.fabrics, []);
  assert.ok(statusBeforeReset.manualPairingCode?.startsWith("FAKE-"));
  assert.ok(statusBeforeReset.qrPairingCode?.startsWith("MT:FAKE-"));
  assert.notEqual(statusBeforeReset.manualPairingCode, String(configuration.commissioningPasscode));

  assert.equal(
    Buffer.from((await storage.get("runtime/identity-generation")) ?? []).toString("utf8"),
    "0",
  );
  await client.factoryReset();
  await eventually(() => factoryResetCompleted === 1);
  assert.equal(
    Buffer.from((await storage.get("runtime/identity-generation")) ?? []).toString("utf8"),
    "1",
  );
  assert.equal((await storage.list()).length, 1);
  assert.equal((await client.getStatus()).identityGeneration, 1);
});

test("handshake rejects incompatible clients and state replay is mandatory", async () => {
  const temporary = await temporarySocket("handshake");
  const server = new RuntimeServer(new FakeProtocolAdapter(), {
    socketPath: temporary.socketPath,
    targetId: "matter-a",
  });
  await server.start();
  const peer = await connectJsonRpcPeer(temporary.socketPath);
  cleanups.push(async () => {
    peer.close();
    await server.stop();
    await temporary.cleanup();
  });

  await rejectsWithCode(
    peer.request(RpcMethod.getStatus, {}),
    JsonRpcErrorCode.handshakeRequired,
  );
  await rejectsWithCode(
    peer.request(RpcMethod.handshake, {
      protocolVersion: "99.0",
      targetId: "matter-a",
      clientName: "bad-version",
    }),
    JsonRpcErrorCode.protocolVersionMismatch,
  );
  await rejectsWithCode(
    peer.request(RpcMethod.handshake, {
      protocolVersion: IPC_PROTOCOL_VERSION,
      targetId: "matter-b",
      clientName: "wrong-target",
    }),
    JsonRpcErrorCode.targetMismatch,
  );

  const handshake = await peer.request<Record<string, unknown>>(RpcMethod.handshake, {
    protocolVersion: IPC_PROTOCOL_VERSION,
    targetId: "matter-a",
    clientName: "contract-test",
  });
  assert.equal(handshake.protocolVersion, IPC_PROTOCOL_VERSION);
  assert.equal(handshake.replayRequired, true);
  await rejectsWithCode(
    peer.request(RpcMethod.configureBridge, bridgeConfiguration("matter-a")),
    JsonRpcErrorCode.replayRequired,
  );
});

async function rejectsWithCode(promise: Promise<unknown>, code: number): Promise<void> {
  await assert.rejects(
    promise,
    (error: unknown) => error instanceof RpcRemoteError && error.code === code,
  );
}
