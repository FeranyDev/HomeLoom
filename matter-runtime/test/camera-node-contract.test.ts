import assert from "node:assert/strict";
import test from "node:test";
import {
  ContractValidationError,
  expectReplayState,
  IPC_PROTOCOL_VERSION,
  type DeviceSnapshot,
  type RuntimeReplayState,
} from "../src/contract.js";
import { MemoryHostStorage } from "../src/ipc/reconnecting-client.js";
import type { AdapterContext } from "../src/runtime/adapter.js";
import { loadPinnedMatterJs } from "../src/runtime/matter-js-adapter.js";
import {
  MatterJsBridgeDriver,
  removeCameraCurrentSession,
  upsertCameraCurrentSession,
  validateCameraStreamPriorities,
} from "../src/runtime/matter-js-driver.js";
import { bridgeConfiguration } from "./helpers.js";

test("camera replay contract describes one independent endpoint and opaque media identity", () => {
  assert.equal(IPC_PROTOCOL_VERSION, "1.1");
  const state = expectReplayState(cameraReplay());

  assert.equal(state.bridge?.nodeKind, "camera");
  assert.deepEqual(state.devices.map(({ id, endpointId, deviceType }) => ({
    id,
    endpointId,
    deviceType,
  })), [{
    id: "front-door",
    endpointId: 1,
    deviceType: "camera",
  }]);
  assert.deepEqual(state.media, {
    streamId: "camera-front-door-main",
    deviceId: "front-door",
  });
});

test("camera stream priorities accept each advertised usage exactly once", () => {
  assert.doesNotThrow(() => validateCameraStreamPriorities([3], [3]));
  assert.throws(
    () => validateCameraStreamPriorities([], [3]),
    /each supported stream usage exactly once/,
  );
  assert.throws(
    () => validateCameraStreamPriorities([3, 3], [3]),
    /each supported stream usage exactly once/,
  );
  assert.throws(
    () => validateCameraStreamPriorities([2], [3]),
    /each supported stream usage exactly once/,
  );
});

test("camera current sessions are inserted, updated and removed by Matter session ID", () => {
  const initial = [{ id: 1, sdp: "old" }];
  const added = upsertCameraCurrentSession(initial, { id: 2, sdp: "new" });
  assert.deepEqual(added, [
    { id: 1, sdp: "old" },
    { id: 2, sdp: "new" },
  ]);
  assert.deepEqual(initial, [{ id: 1, sdp: "old" }]);
  const updated = upsertCameraCurrentSession(added, { id: 1, sdp: "reoffer" });
  assert.deepEqual(updated, [
    { id: 1, sdp: "reoffer" },
    { id: 2, sdp: "new" },
  ]);
  assert.deepEqual(removeCameraCurrentSession(updated, 1), [{ id: 2, sdp: "new" }]);
});

test("camera replay rejects bridge topology, mismatched media and transport secrets", () => {
  const withoutMedia = cameraReplay();
  delete withoutMedia.media;
  assertInvalid(withoutMedia, "camera node requires a media binding");
  assertInvalid({
    ...cameraReplay(),
    devices: [{ ...cameraDevice(), endpointId: 2 }],
  }, "exactly one camera device at endpointId 1");
  assertInvalid({
    ...cameraReplay(),
    devices: [cameraDevice(), { ...cameraDevice(), id: "second", endpointId: 2 }],
  }, "exactly one camera device at endpointId 1");
  assertInvalid({
    ...cameraReplay(),
    devices: [{ ...cameraDevice(), deviceType: "OnOffLight" }],
  }, "exactly one camera device at endpointId 1");
  assertInvalid({
    ...cameraReplay(),
    media: { streamId: "main", deviceId: "other" },
  }, "must match the camera device id");
  assertInvalid({
    ...cameraReplay(),
    media: {
      streamId: "main",
      deviceId: "front-door",
      uri: "rtsp://user:password@example.invalid/live",
    },
  }, "unsupported field uri");
  assertInvalid({
    ...cameraReplay(),
    bridge: bridgeConfiguration("bridge"),
    devices: [cameraDevice()],
  }, "endpointId must be an integer between 2");
});

test("official camera endpoint 0x0142 is a direct Node child with mandatory skeleton behaviors", async (context) => {
  // MC-2 deliberately verifies topology and mandatory cluster presence only.
  // Video+ImageControl is the matter.js 0.17.6 constructible skeleton; MC-3
  // must replace it with Video+Audio+Snapshot and real media command handlers.
  const sdk = await loadPinnedMatterJs();
  const driver = new MatterJsBridgeDriver(sdk, { startNetwork: false });
  context.after(async () => {
    await driver.stop();
    await sdk.main.Environment.default
      .maybeGet(sdk.protocol.MdnsService)
      ?.close();
  });
  const adapterContext: AdapterContext = {
    targetId: "matter-camera",
    identityNamespace: "matter-camera",
    storage: new MemoryHostStorage(),
    async emitCommand() {
      return { accepted: false, message: "camera media commands are not wired in MC-2" };
    },
    async emitAttributeWrite() {
      return { accepted: false, message: "camera media attributes are not wired in MC-2" };
    },
    async emitCommissioningChanged() {},
    async emitFabricChanged() {},
    async emitDiagnostics() {},
  };

  await driver.start(adapterContext);
  await driver.replayState(cameraReplay());

  assert.equal(driver.endpointPlacementForTest("front-door"), "node");
  const deviceTypes = await driver.readAttributeForTest(
    "front-door",
    "descriptor",
    "deviceTypeList",
  ) as Array<{ deviceType?: number }>;
  assert.ok(deviceTypes.some(({ deviceType }) => deviceType === 0x0142));
  assert.deepEqual(
    await driver.readAttributeForTest(
      "front-door",
      "webRtcTransportProvider",
      "currentSessions",
    ),
    [],
  );
  assert.deepEqual(
    await driver.readAttributeForTest(
      "front-door",
      "cameraAvStreamManagement",
      "allocatedVideoStreams",
    ),
    [],
  );
  assert.deepEqual(driver.mediaBindingForTest(), {
    streamId: "camera-front-door-main",
    deviceId: "front-door",
  });
});

function cameraReplay(): RuntimeReplayState {
  return {
    revision: 1,
    bridge: bridgeConfiguration("matter-camera", { nodeKind: "camera" }),
    devices: [cameraDevice()],
    media: {
      streamId: "camera-front-door-main",
      deviceId: "front-door",
    },
  };
}

function cameraDevice(): DeviceSnapshot {
  return {
    id: "front-door",
    endpointId: 1,
    deviceType: "camera",
    name: "Front door camera",
    reachable: true,
    attributes: {},
  };
}

function assertInvalid(state: unknown, message: string): void {
  assert.throws(
    () => expectReplayState(state),
    (error: unknown) => error instanceof ContractValidationError
      && error.message.includes(message),
  );
}
