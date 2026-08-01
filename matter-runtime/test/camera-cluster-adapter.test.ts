import assert from "node:assert/strict";
import test from "node:test";
import {
  CameraAvAllocationManager,
  type CameraAvOwner,
  type CameraAvHostLease,
} from "../src/camera/av-allocation-manager.js";
import { MatterCameraClusterAdapter } from "../src/camera/camera-cluster-adapter.js";
import type { CameraHostMediaClient } from "../src/camera/host-media-client.js";
import {
  CameraSessionManager,
  type CameraMediaSession,
  type CameraSessionOwner,
  type IceCandidate,
} from "../src/camera/session-manager.js";

const owner: CameraSessionOwner = {
  fabricIndex: 1,
  peerNodeId: "0x1234",
  peerEndpointId: 2,
};
const avOwner: CameraAvOwner = {
  fabricIndex: owner.fabricIndex,
  peerNodeId: owner.peerNodeId,
};

test("cluster seam gates WebRTC offers on owner-scoped AV allocations", async () => {
  const mediaSession = fakeMediaSession();
  const sessions = new CameraSessionManager(
    {
      openSession: async () => ({
        answerSdp: "v=0\r\no=provider",
        session: mediaSession,
      }),
    },
    {
      provideAnswer: async () => undefined,
      provideIceCandidates: async () => undefined,
    },
    { maxConcurrentSessions: 1 },
  );
  const allocations = new CameraAvAllocationManager(
    { deviceId: "front-door", streamId: "camera-abc123" },
    { allocate: async () => fakeLease() },
  );
  const adapter = new MatterCameraClusterAdapter(allocations, sessions);
  const video = await adapter.allocateVideo(avOwner, {
    streamUsage: 3,
    videoCodec: 0,
    minFrameRate: 15,
    maxFrameRate: 30,
    minResolution: { width: 640, height: 360 },
    maxResolution: { width: 1_280, height: 720 },
    minBitRate: 128_000,
    maxBitRate: 2_000_000,
    keyFrameInterval: 2_000,
  });

  const created = await adapter.provideOffer({
    ...owner,
    sessionId: null,
    offerSdp: "v=0\r\no=requestor",
    streamUsage: 3,
    videoStreamIds: [video.videoStreamId],
  });
  assert.equal(created.sessionId, 0);

  await assert.rejects(
    adapter.provideOffer({
      ...owner,
      peerNodeId: "0x9999",
      sessionId: null,
      offerSdp: "v=0\r\no=attacker",
      streamUsage: 3,
      videoStreamIds: [video.videoStreamId],
    }),
    (error: unknown) => error instanceof Error
      && "code" in error
      && error.code === "unknown-allocation",
  );
});

test("cluster seam rejects offers without an allocated video stream", async () => {
  const adapter = new MatterCameraClusterAdapter(
    new CameraAvAllocationManager(
      { deviceId: "front-door", streamId: "camera-abc123" },
      { allocate: async () => fakeLease() },
    ),
    new CameraSessionManager(
      {
        openSession: async () => ({
          answerSdp: "v=0\r\no=provider",
          session: fakeMediaSession(),
        }),
      },
      {
        provideAnswer: async () => undefined,
        provideIceCandidates: async () => undefined,
      },
      { maxConcurrentSessions: 1 },
    ),
  );

  await assert.rejects(
    adapter.provideOffer({
      ...owner,
      sessionId: null,
      offerSdp: "v=0\r\no=requestor",
      streamUsage: 3,
    }),
    TypeError,
  );
});

test("snapshot capture is limited to the owner allocation and advertised resolution", async () => {
  const captured: Array<{ width: number; height: number }> = [];
  const allocations = new CameraAvAllocationManager(
    { deviceId: "front-door", streamId: "camera-abc123" },
    { allocate: async () => fakeLease() },
  );
  const sessions = new CameraSessionManager(
    {
      openSession: async () => ({
        answerSdp: "unused",
        session: fakeMediaSession(),
      }),
    },
    {
      provideAnswer: async () => undefined,
      provideIceCandidates: async () => undefined,
    },
    { maxConcurrentSessions: 1 },
  );
  const host: CameraHostMediaClient = {
    async captureSnapshot(request) {
      captured.push(request.resolution);
      return { jpegBase64: "anBlZw==", resolution: request.resolution };
    },
    async openWebRtc() {
      throw new Error("unused");
    },
    async reofferWebRtc() {
      throw new Error("unused");
    },
    async addWebRtcIce() {},
    async closeWebRtc() {},
    onLocalIceCandidates() {
      return () => {};
    },
  };
  const adapter = new MatterCameraClusterAdapter(
    allocations,
    sessions,
    {
      host,
      media: { deviceId: "front-door", streamId: "camera-abc123" },
    },
  );
  const snapshot = await adapter.allocateSnapshot(avOwner, {
    imageCodec: 0,
    maxFrameRate: 1,
    minResolution: { width: 640, height: 360 },
    maxResolution: { width: 640, height: 360 },
    quality: 80,
  });

  await adapter.captureSnapshot(
    avOwner,
    snapshot.snapshotStreamId,
    { width: 640, height: 360 },
  );
  await assert.rejects(
    adapter.captureSnapshot(
      avOwner,
      snapshot.snapshotStreamId,
      { width: 4_096, height: 4_096 },
    ),
    /outside the allocated capability/,
  );
  assert.deepEqual(captured, [{ width: 640, height: 360 }]);
});

function fakeLease(): CameraAvHostLease {
  return {
    async modifyVideo() {},
    async close() {},
  };
}

function fakeMediaSession(): CameraMediaSession {
  return {
    async renegotiate() {
      return "v=0\r\no=provider-renegotiated";
    },
    async addRemoteIceCandidates(_candidates: readonly IceCandidate[]) {},
    async close() {},
  };
}
