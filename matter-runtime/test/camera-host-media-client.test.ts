import assert from "node:assert/strict";
import test from "node:test";
import {
  HostRpcCameraMediaBackend,
  RpcCameraHostMediaClient,
  type CameraHostRpcTransport,
} from "../src/camera/host-media-client.js";
import { cameraRpcTimeoutMs } from "../src/runtime/server.js";

const media = {
  deviceId: "front-door",
  streamId: "camera-front-door-main",
};

test("slow media setup gets a dedicated timeout without widening control RPCs", () => {
  assert.equal(cameraRpcTimeoutMs("camera.webrtc", "open"), 12_000);
  assert.equal(cameraRpcTimeoutMs("camera.webrtc", "reoffer"), 12_000);
  assert.equal(cameraRpcTimeoutMs("camera.snapshot"), 12_000);
  assert.equal(cameraRpcTimeoutMs("camera.webrtc", "addIce"), 5_000);
  assert.equal(cameraRpcTimeoutMs("camera.webrtc", "close"), 5_000);
});

test("camera reverse RPC uses the Target-bound Core contract without media identity", async () => {
  const calls: Array<{ method: string; params: Record<string, unknown> }> = [];
  const transport: CameraHostRpcTransport = {
    async request<T>(method: "camera.webrtc" | "camera.snapshot", params: Record<string, unknown>) {
      calls.push({ method, params });
      if (method === "camera.snapshot") {
        return { jpegBase64: "anBlZw==" } as T;
      }
      if (params.operation === "close") {
        return { closed: true } as T;
      }
      return {
        sessionId: "pion-session-1",
        sdp: `answer-${String(params.operation)}`,
      } as T;
    },
  };
  const host = new RpcCameraHostMediaClient(media, transport);

  const snapshot = await host.captureSnapshot({
    media,
    owner: { fabricIndex: 1, peerNodeId: "123" },
    snapshotStreamId: 7,
    resolution: { width: 640, height: 360 },
  });
  assert.equal(snapshot.jpegBase64, "anBlZw==");

  const opened = await host.openWebRtc({
    media,
    offer: {
      fabricIndex: 1,
      peerNodeId: "123",
      peerEndpointId: 4,
      sessionId: 9,
      offerSdp: "offer",
      streamUsage: 3,
      videoStreamIds: [1],
    },
  });
  assert.equal(opened.sessionId, "pion-session-1");
  await host.reofferWebRtc({
    sessionId: opened.sessionId,
    offer: {
      fabricIndex: 1,
      peerNodeId: "123",
      peerEndpointId: 4,
      sessionId: 9,
      offerSdp: "reoffer",
      streamUsage: 3,
      videoStreamIds: [1],
    },
  });
  await host.addWebRtcIce({
    sessionId: opened.sessionId,
    candidates: [
      { candidate: "candidate-a", sdpMid: "0", sdpMLineIndex: 0 },
      { candidate: "candidate-b", sdpMid: null, sdpMLineIndex: null },
    ],
  });
  await host.closeWebRtc({ sessionId: opened.sessionId, reason: "user-hangup" });

  assert.deepEqual(calls, [
    {
      method: "camera.snapshot",
      params: { width: 640, height: 360 },
    },
    {
      method: "camera.webrtc",
      params: { operation: "open", sdp: "offer" },
    },
    {
      method: "camera.webrtc",
      params: {
        operation: "reoffer",
        sessionId: "pion-session-1",
        sdp: "reoffer",
      },
    },
    {
      method: "camera.webrtc",
      params: {
        operation: "addIce",
        sessionId: "pion-session-1",
        candidate: {
          candidate: "candidate-a",
          sdpMid: "0",
          sdpMLineIndex: 0,
        },
      },
    },
    {
      method: "camera.webrtc",
      params: {
        operation: "addIce",
        sessionId: "pion-session-1",
        candidate: {
          candidate: "candidate-b",
          sdpMid: null,
          sdpMLineIndex: null,
        },
      },
    },
    {
      method: "camera.webrtc",
      params: { operation: "close", sessionId: "pion-session-1" },
    },
  ]);
  for (const call of calls) {
    assert.equal("streamId" in call.params, false);
    assert.equal("deviceId" in call.params, false);
    assert.equal("owner" in call.params, false);
    assert.equal("reason" in call.params, false);
  }
});

test("host media backend keeps Matter and Pion session identifiers separate", async () => {
  const calls: Record<string, unknown>[] = [];
  const host = new RpcCameraHostMediaClient(media, {
    async request<T>(
      _method: "camera.webrtc" | "camera.snapshot",
      params: Record<string, unknown>,
    ) {
      calls.push(params);
      return {
        sessionId: "host-session-42",
        sdp: params.operation === "reoffer" ? "answer-2" : "answer-1",
        closed: params.operation === "close",
      } as T;
    },
  });
  const backend = new HostRpcCameraMediaBackend(media, host);
  const opened = await backend.openSession(
    {
      fabricIndex: 2,
      peerNodeId: "44",
      peerEndpointId: 3,
      sessionId: 17,
      offerSdp: "offer-1",
      streamUsage: 3,
      videoStreamIds: [1],
    },
    { onLocalIceCandidates: () => assert.fail("Pion is non-trickle") },
  );
  assert.equal(opened.answerSdp, "answer-1");
  assert.equal(await opened.session.renegotiate({
    fabricIndex: 2,
    peerNodeId: "44",
    peerEndpointId: 3,
    sessionId: 17,
    offerSdp: "offer-2",
    streamUsage: 3,
    videoStreamIds: [1],
  }), "answer-2");
  await opened.session.close("user-hangup");

  assert.deepEqual(calls, [
    { operation: "open", sdp: "offer-1" },
    { operation: "reoffer", sessionId: "host-session-42", sdp: "offer-2" },
    { operation: "close", sessionId: "host-session-42" },
  ]);
});

test("camera reverse RPC rejects mismatched local media identity before transport", async () => {
  let called = false;
  const host = new RpcCameraHostMediaClient(media, {
    async request<T>() {
      called = true;
      return {} as T;
    },
  });
  await assert.rejects(
    host.openWebRtc({
      media: { ...media, streamId: "other-stream" },
      offer: {
        fabricIndex: 1,
        peerNodeId: "1",
        peerEndpointId: 1,
        sessionId: 1,
        offerSdp: "offer",
        streamUsage: 3,
      },
    }),
    /does not match/,
  );
  assert.equal(called, false);
});
