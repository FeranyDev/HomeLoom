import assert from "node:assert/strict";
import test from "node:test";
import {
  CameraAvAllocationError,
  CameraAvAllocationManager,
  type CameraAvAllocation,
  type CameraAvHostClient,
  type CameraAvHostLease,
  type CameraAvOwner,
  type CameraAvResourceKind,
  type VideoModification,
} from "../src/camera/av-allocation-manager.js";

const owner: CameraAvOwner = {
  fabricIndex: 1,
  peerNodeId: "0x1234",
};

class FakeLease implements CameraAvHostLease {
  readonly modifications: VideoModification[] = [];
  closeCount = 0;
  modifyError: Error | undefined;
  closeError: Error | undefined;

  async modifyVideo(modification: VideoModification): Promise<void> {
    this.modifications.push({ ...modification });
    if (this.modifyError !== undefined) throw this.modifyError;
  }

  async close(): Promise<void> {
    this.closeCount += 1;
    if (this.closeError !== undefined) throw this.closeError;
  }
}

function fixture() {
  const calls: Array<{
    media: { deviceId: string; streamId: string };
    owner: CameraAvOwner;
    kind: CameraAvResourceKind;
    allocationId: number;
    allocation: CameraAvAllocation;
  }> = [];
  const leases: FakeLease[] = [];
  const host: CameraAvHostClient = {
    allocate: async (request) => {
      calls.push(structuredClone(request));
      const lease = new FakeLease();
      leases.push(lease);
      return lease;
    },
  };
  const changes: number[] = [];
  const manager = new CameraAvAllocationManager(
    { deviceId: "front-door", streamId: "camera-abc123" },
    host,
    (state) => changes.push(state.video.length + state.audio.length + state.snapshot.length),
  );
  return { manager, calls, leases, changes };
}

test("allocates supported H.264 profiles and passes only opaque media identity to host", async () => {
  const { manager, calls } = fixture();
  const allocation = await manager.allocateVideo(owner, videoRequest({
    minResolution: { width: 600, height: 300 },
    maxResolution: { width: 1_280, height: 720 },
  }));

  assert.deepEqual(allocation.maxResolution, { width: 1_280, height: 720 });
  assert.equal(allocation.videoCodec, 0);
  assert.equal(allocation.maxFrameRate, 30);
  assert.deepEqual(calls[0]?.media, {
    deviceId: "front-door",
    streamId: "camera-abc123",
  });
  assert.deepEqual(Object.keys(calls[0]?.media ?? {}).sort(), ["deviceId", "streamId"]);
});

test("selects 640x360 when the request range excludes 1280x720", async () => {
  const { manager } = fixture();
  const allocation = await manager.allocateVideo(owner, videoRequest({
    maxResolution: { width: 640, height: 360 },
  }));

  assert.deepEqual(allocation.maxResolution, { width: 640, height: 360 });
});

test("never raises the allocated frame rate above the Controller request", async () => {
  const { manager } = fixture();
  const allocation = await manager.allocateVideo(owner, videoRequest({
    minFrameRate: 10,
    maxFrameRate: 20,
  }));

  assert.equal(allocation.minFrameRate, 10);
  assert.equal(allocation.maxFrameRate, 20);
});

test("strictly rejects unsupported video, audio and snapshot requests before host RPC", async () => {
  const { manager, calls } = fixture();
  await rejectsWithCode(
    manager.allocateVideo(owner, videoRequest({ videoCodec: 1 })),
    "invalid-request",
  );
  await rejectsWithCode(
    manager.allocateVideo(owner, videoRequest({
      minResolution: { width: 1_920, height: 1_080 },
      maxResolution: { width: 1_920, height: 1_080 },
    })),
    "invalid-request",
  );
  await rejectsWithCode(
    manager.allocateAudio(owner, audioRequest({ sampleRate: 16_000 })),
    "invalid-request",
  );
  await rejectsWithCode(
    manager.allocateSnapshot(owner, snapshotRequest({ imageCodec: 1 })),
    "invalid-request",
  );
  assert.equal(calls.length, 0);
});

test("allocates one Opus 48k stream and one JPEG snapshot with independent limits", async () => {
  const { manager } = fixture();
  const audio = await manager.allocateAudio(owner, audioRequest());
  const snapshot = await manager.allocateSnapshot(owner, snapshotRequest());

  assert.equal(audio.audioCodec, 0);
  assert.equal(audio.sampleRate, 48_000);
  assert.equal(audio.channelCount, 1);
  assert.equal(snapshot.imageCodec, 0);
  assert.equal(snapshot.frameRate, 1);
  assert.notEqual(audio.audioStreamId, snapshot.snapshotStreamId);
  await rejectsWithCode(manager.allocateAudio(owner, audioRequest()), "resource-exhausted");
  await rejectsWithCode(
    manager.allocateSnapshot(owner, snapshotRequest()),
    "resource-exhausted",
  );
});

test("pending allocation counts toward the per-kind limit", async () => {
  let release: ((lease: CameraAvHostLease) => void) | undefined;
  const pending = new Promise<CameraAvHostLease>((resolve) => {
    release = resolve;
  });
  const manager = new CameraAvAllocationManager(
    { deviceId: "front-door", streamId: "camera-abc123" },
    { allocate: async () => pending },
  );

  const first = manager.allocateVideo(owner, videoRequest());
  await tick();
  await rejectsWithCode(manager.allocateVideo(owner, videoRequest()), "resource-exhausted");
  release?.(new FakeLease());
  await first;
});

test("owner protects modify/deallocate and successful modify updates state", async () => {
  const { manager, leases } = fixture();
  const allocation = await manager.allocateVideo(owner, videoRequest());
  const otherPeer = { ...owner, peerNodeId: "0x9999" };

  await rejectsWithCode(
    manager.modifyVideo(otherPeer, {
      videoStreamId: allocation.videoStreamId,
      watermarkEnabled: true,
    }),
    "unknown-allocation",
  );
  await rejectsWithCode(
    manager.deallocateVideo(otherPeer, allocation.videoStreamId),
    "unknown-allocation",
  );
  const modified = await manager.modifyVideo(owner, {
    videoStreamId: allocation.videoStreamId,
    watermarkEnabled: true,
  });
  assert.equal(modified.watermarkEnabled, true);
  assert.equal(manager.state.video[0]?.watermarkEnabled, true);
  assert.equal(leases[0]?.modifications.length, 1);

  await manager.deallocateVideo(owner, allocation.videoStreamId);
  assert.equal(manager.state.video.length, 0);
  assert.equal(leases[0]?.closeCount, 1);
});

test("host allocate and modify failures roll back local state", async () => {
  const rejecting = new CameraAvAllocationManager(
    { deviceId: "front-door", streamId: "camera-abc123" },
    { allocate: async () => { throw new Error("worker unavailable"); } },
  );
  await rejectsWithCode(
    rejecting.allocateVideo(owner, videoRequest()),
    "host-failure",
  );
  assert.equal(rejecting.state.video.length, 0);

  const { manager, leases } = fixture();
  const allocation = await manager.allocateVideo(owner, videoRequest());
  leases[0]!.modifyError = new Error("RPC timeout");
  await rejectsWithCode(
    manager.modifyVideo(owner, {
      videoStreamId: allocation.videoStreamId,
      osdEnabled: true,
    }),
    "host-failure",
  );
  assert.equal(manager.state.video[0]?.osdEnabled, false);
});

test("modify rejects empty changes and leases without host modification support", async () => {
  const manager = new CameraAvAllocationManager(
    { deviceId: "front-door", streamId: "camera-abc123" },
    { allocate: async () => ({ async close() {} }) },
  );
  const allocation = await manager.allocateVideo(owner, videoRequest());
  await rejectsWithCode(
    manager.modifyVideo(owner, { videoStreamId: allocation.videoStreamId }),
    "invalid-request",
  );
  await rejectsWithCode(
    manager.modifyVideo(owner, {
      videoStreamId: allocation.videoStreamId,
      watermarkEnabled: true,
    }),
    "host-failure",
  );
  assert.equal(manager.state.video[0]?.watermarkEnabled, false);
});

test("close releases every allocation once, clears state and is idempotent", async () => {
  const { manager, leases, changes } = fixture();
  await manager.allocateVideo(owner, videoRequest());
  await manager.allocateAudio(owner, audioRequest());
  await manager.allocateSnapshot(owner, snapshotRequest());

  await manager.close();
  await manager.close();

  assert.deepEqual(manager.state, { video: [], audio: [], snapshot: [] });
  assert.deepEqual(leases.map(({ closeCount }) => closeCount), [1, 1, 1]);
  assert.deepEqual(changes, [1, 2, 3, 0]);
  await rejectsWithCode(manager.allocateAudio(owner, audioRequest()), "manager-closed");
});

test("close waits for an in-flight host allocation and rolls its lease back", async () => {
  let release: ((lease: CameraAvHostLease) => void) | undefined;
  const pending = new Promise<CameraAvHostLease>((resolve) => {
    release = resolve;
  });
  const lease = new FakeLease();
  const manager = new CameraAvAllocationManager(
    { deviceId: "front-door", streamId: "camera-abc123" },
    { allocate: async () => pending },
  );
  const allocating = manager.allocateVideo(owner, videoRequest());
  await tick();

  const closing = manager.close();
  release?.(lease);
  await closing;
  await rejectsWithCode(allocating, "manager-closed");

  assert.equal(lease.closeCount, 1);
  assert.deepEqual(manager.state, { video: [], audio: [], snapshot: [] });
});

function videoRequest(
  overrides: Partial<Parameters<CameraAvAllocationManager["allocateVideo"]>[1]> = {},
): Parameters<CameraAvAllocationManager["allocateVideo"]>[1] {
  return {
    streamUsage: 3,
    videoCodec: 0,
    minFrameRate: 15,
    maxFrameRate: 30,
    minResolution: { width: 320, height: 180 },
    maxResolution: { width: 1_280, height: 720 },
    minBitRate: 128_000,
    maxBitRate: 2_000_000,
    keyFrameInterval: 2_000,
    ...overrides,
  };
}

function audioRequest(
  overrides: Partial<Parameters<CameraAvAllocationManager["allocateAudio"]>[1]> = {},
): Parameters<CameraAvAllocationManager["allocateAudio"]>[1] {
  return {
    streamUsage: 3,
    audioCodec: 0,
    channelCount: 1,
    sampleRate: 48_000,
    bitRate: 64_000,
    bitDepth: 16,
    ...overrides,
  };
}

function snapshotRequest(
  overrides: Partial<Parameters<CameraAvAllocationManager["allocateSnapshot"]>[1]> = {},
): Parameters<CameraAvAllocationManager["allocateSnapshot"]>[1] {
  return {
    imageCodec: 0,
    maxFrameRate: 1,
    minResolution: { width: 320, height: 180 },
    maxResolution: { width: 1_280, height: 720 },
    quality: 80,
    ...overrides,
  };
}

async function rejectsWithCode(
  promise: Promise<unknown>,
  code: CameraAvAllocationError["code"],
): Promise<void> {
  await assert.rejects(
    promise,
    (error: unknown) => error instanceof CameraAvAllocationError && error.code === code,
  );
}

async function tick(): Promise<void> {
  await new Promise<void>((resolve) => setImmediate(resolve));
}
