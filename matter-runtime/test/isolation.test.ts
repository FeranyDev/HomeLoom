import assert from "node:assert/strict";
import { access } from "node:fs/promises";
import { afterEach, test } from "node:test";
import { MemoryHostStorage, ReconnectingRuntimeClient } from "../src/ipc/reconnecting-client.js";
import { FakeProtocolAdapter } from "../src/runtime/fake-adapter.js";
import { RuntimeServer } from "../src/runtime/server.js";
import { bridgeConfiguration, switchDevice, temporarySocket } from "./helpers.js";

const cleanups: Array<() => Promise<void>> = [];

afterEach(async () => {
  for (const cleanup of cleanups.splice(0).reverse()) {
    await cleanup();
  }
});

test("two targets isolate socket, identity, port, discriminator and host storage", async () => {
  const firstTemporary = await temporarySocket("isolation-a");
  const secondTemporary = await temporarySocket("isolation-b");
  const firstAdapter = new FakeProtocolAdapter();
  const secondAdapter = new FakeProtocolAdapter();
  const firstServer = new RuntimeServer(firstAdapter, {
    socketPath: firstTemporary.socketPath,
    targetId: "matter-a",
    identityNamespace: "identity-a",
  });
  const secondServer = new RuntimeServer(secondAdapter, {
    socketPath: secondTemporary.socketPath,
    targetId: "matter-b",
    identityNamespace: "identity-b",
  });
  const firstStorage = new MemoryHostStorage();
  const secondStorage = new MemoryHostStorage();
  const firstClient = new ReconnectingRuntimeClient({
    socketPath: firstTemporary.socketPath,
    targetId: "matter-a",
    identityNamespace: "identity-a",
    initialState: {
      revision: 1,
      bridge: bridgeConfiguration("matter-a", {
        identityNamespace: "identity-a",
        listenPort: 5_540,
        discriminator: 101,
      }),
      devices: [switchDevice("device-a", 2)],
    },
    host: { storage: firstStorage },
  });
  const secondClient = new ReconnectingRuntimeClient({
    socketPath: secondTemporary.socketPath,
    targetId: "matter-b",
    identityNamespace: "identity-b",
    initialState: {
      revision: 1,
      bridge: bridgeConfiguration("matter-b", {
        identityNamespace: "identity-b",
        listenPort: 5_541,
        discriminator: 202,
      }),
      devices: [switchDevice("device-b", 2)],
    },
    host: { storage: secondStorage },
  });

  await Promise.all([firstServer.start(), secondServer.start()]);
  firstClient.start();
  secondClient.start();
  cleanups.push(async () => {
    await Promise.all([firstClient.stop(), secondClient.stop()]);
    await Promise.all([firstServer.stop(), secondServer.stop()]);
    await Promise.all([firstTemporary.cleanup(), secondTemporary.cleanup()]);
  });
  await Promise.all([firstClient.waitForReady(), secondClient.waitForReady()]);

  assert.equal(firstAdapter.configuration?.identityNamespace, "identity-a");
  assert.equal(secondAdapter.configuration?.identityNamespace, "identity-b");
  assert.equal(firstAdapter.configuration?.listenPort, 5_540);
  assert.equal(secondAdapter.configuration?.listenPort, 5_541);
  assert.equal(firstAdapter.configuration?.discriminator, 101);
  assert.equal(secondAdapter.configuration?.discriminator, 202);
  assert.deepEqual(firstAdapter.devices.map((device) => device.id), ["device-a"]);
  assert.deepEqual(secondAdapter.devices.map((device) => device.id), ["device-b"]);

  await firstClient.factoryReset();
  assert.equal(
    Buffer.from((await firstStorage.get("runtime/identity-generation")) ?? []).toString("utf8"),
    "1",
  );
  assert.equal(
    Buffer.from((await secondStorage.get("runtime/identity-generation")) ?? []).toString("utf8"),
    "0",
  );

  await assert.rejects(
    firstClient.configureBridge(bridgeConfiguration("matter-b", {
      identityNamespace: "identity-b",
    })),
    /identity does not match/,
  );
});

test("a second runtime cannot steal or unlink an active socket", async () => {
  const temporary = await temporarySocket("socket-owner");
  const firstServer = new RuntimeServer(new FakeProtocolAdapter(), {
    socketPath: temporary.socketPath,
    targetId: "matter-owner",
  });
  const secondServer = new RuntimeServer(new FakeProtocolAdapter(), {
    socketPath: temporary.socketPath,
    targetId: "matter-intruder",
  });
  await firstServer.start();
  cleanups.push(async () => {
    await firstServer.stop();
    await temporary.cleanup();
  });

  await assert.rejects(secondServer.start(), (error: unknown) => {
    return error instanceof Error && "code" in error && error.code === "EADDRINUSE";
  });
  await access(temporary.socketPath);
  assert.equal(firstServer.started, true);
});
