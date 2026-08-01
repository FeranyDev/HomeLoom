import type {
  CameraMediaBackend,
  CameraMediaSession,
  CameraOffer,
  CameraSessionEndReason,
  IceCandidate,
} from "./session-manager.js";
import type {
  CameraAvOwner,
  CameraMediaIdentity,
  VideoResolution,
} from "./av-allocation-manager.js";

export interface CameraSnapshotRequest {
  media: CameraMediaIdentity;
  owner: CameraAvOwner;
  snapshotStreamId: number;
  resolution: VideoResolution;
}

export interface CameraSnapshotResult {
  jpegBase64: string;
  resolution: VideoResolution;
}

export interface CameraWebRtcOpenResult {
  sessionId: string;
  answerSdp: string;
  localIceCandidates?: readonly IceCandidate[];
}

export interface CameraHostRpcTransport {
  request<T>(method: "camera.webrtc" | "camera.snapshot", params: Record<string, unknown>): Promise<T>;
}

/**
 * Reverse RPC implemented by the HomeLoom host. Every media source remains
 * host-owned; the runtime receives only an opaque host session ID.
 */
export interface CameraHostMediaClient {
  captureSnapshot(request: CameraSnapshotRequest): Promise<CameraSnapshotResult>;
  openWebRtc(request: {
    media: CameraMediaIdentity;
    offer: Readonly<CameraOffer> & { sessionId: number };
  }): Promise<CameraWebRtcOpenResult>;
  reofferWebRtc(request: {
    sessionId: string;
    offer: Readonly<CameraOffer> & { sessionId: number };
  }): Promise<{ answerSdp: string }>;
  addWebRtcIce(request: {
    sessionId: string;
    candidates: readonly IceCandidate[];
  }): Promise<void>;
  closeWebRtc(request: {
    sessionId: string;
    reason: CameraSessionEndReason;
  }): Promise<void>;
  onLocalIceCandidates(
    sessionId: string,
    listener: (candidates: readonly IceCandidate[]) => void,
  ): () => void;
}

/**
 * Exact Core reverse-RPC mapping. The expected media identity is checked
 * locally only; streamId/deviceId and Matter ownership never cross the IPC
 * seam because the authenticated Target already selects its bound stream.
 */
export class RpcCameraHostMediaClient implements CameraHostMediaClient {
  readonly #media: CameraMediaIdentity;
  readonly #transport: CameraHostRpcTransport;

  constructor(media: CameraMediaIdentity, transport: CameraHostRpcTransport) {
    this.#media = { ...media };
    this.#transport = transport;
  }

  async captureSnapshot(request: CameraSnapshotRequest): Promise<CameraSnapshotResult> {
    this.#assertMedia(request.media);
    const response = await this.#transport.request<{ jpegBase64: string }>(
      "camera.snapshot",
      {
        width: request.resolution.width,
        height: request.resolution.height,
      },
    );
    if (typeof response.jpegBase64 !== "string" || response.jpegBase64.trim() === "") {
      throw new Error("host returned an invalid camera snapshot");
    }
    return {
      jpegBase64: response.jpegBase64,
      resolution: { ...request.resolution },
    };
  }

  async openWebRtc(request: {
    media: CameraMediaIdentity;
    offer: Readonly<CameraOffer> & { sessionId: number };
  }): Promise<CameraWebRtcOpenResult> {
    this.#assertMedia(request.media);
    const response = await this.#webRtc({
      operation: "open",
      sdp: request.offer.offerSdp,
    });
    return {
      sessionId: response.sessionId,
      answerSdp: response.sdp,
      localIceCandidates: [],
    };
  }

  async reofferWebRtc(request: {
    sessionId: string;
    offer: Readonly<CameraOffer> & { sessionId: number };
  }): Promise<{ answerSdp: string }> {
    const response = await this.#webRtc({
      operation: "reoffer",
      sessionId: request.sessionId,
      sdp: request.offer.offerSdp,
    });
    return { answerSdp: response.sdp };
  }

  async addWebRtcIce(request: {
    sessionId: string;
    candidates: readonly IceCandidate[];
  }): Promise<void> {
    for (const candidate of request.candidates) {
      await this.#webRtc({
        operation: "addIce",
        sessionId: request.sessionId,
        candidate: {
          candidate: candidate.candidate,
          sdpMid: candidate.sdpMid,
          sdpMLineIndex: candidate.sdpMLineIndex,
        },
      }, false);
    }
  }

  async closeWebRtc(request: {
    sessionId: string;
    reason: CameraSessionEndReason;
  }): Promise<void> {
    await this.#webRtc({
      operation: "close",
      sessionId: request.sessionId,
    }, false);
  }

  onLocalIceCandidates(
    _sessionId: string,
    _listener: (candidates: readonly IceCandidate[]) => void,
  ): () => void {
    // Core's Pion implementation currently returns a complete, non-trickle
    // answer, so there is no local candidate event channel to subscribe to.
    return () => {};
  }

  async #webRtc(
    params: Record<string, unknown>,
    requireSdp = true,
  ): Promise<{ sessionId: string; sdp: string; closed?: boolean }> {
    const response = await this.#transport.request<{
      sessionId?: unknown;
      sdp?: unknown;
      closed?: unknown;
    }>("camera.webrtc", params);
    const sessionId = typeof response.sessionId === "string" ? response.sessionId : "";
    const sdp = typeof response.sdp === "string" ? response.sdp : "";
    if (
      (params.operation !== "close" && sessionId.trim() === "")
      || (requireSdp && sdp.trim() === "")
    ) {
      throw new Error("host returned an incomplete camera WebRTC response");
    }
    return { sessionId, sdp, closed: response.closed === true };
  }

  #assertMedia(media: CameraMediaIdentity): void {
    if (media.deviceId !== this.#media.deviceId || media.streamId !== this.#media.streamId) {
      throw new Error("camera media identity does not match the Target binding");
    }
  }
}

/**
 * Adapts the reverse-RPC contract to CameraSessionManager's injectable media
 * backend. No transport address, source URI, token or credential is accepted.
 */
export class HostRpcCameraMediaBackend implements CameraMediaBackend {
  readonly #media: CameraMediaIdentity;
  readonly #host: CameraHostMediaClient;

  constructor(media: CameraMediaIdentity, host: CameraHostMediaClient) {
    this.#media = { ...media };
    this.#host = host;
  }

  async openSession(
    offer: Readonly<CameraOffer> & { sessionId: number },
    events: { onLocalIceCandidates(candidates: readonly IceCandidate[]): void },
  ): Promise<{ answerSdp: string; session: CameraMediaSession }> {
    const opened = await this.#host.openWebRtc({
      media: { ...this.#media },
      offer,
    });
    if (opened.sessionId.trim() === "" || opened.answerSdp.trim() === "") {
      throw new Error("host returned an invalid WebRTC session or answer");
    }
    const unsubscribe = this.#host.onLocalIceCandidates(
      opened.sessionId,
      events.onLocalIceCandidates,
    );
    if (opened.localIceCandidates !== undefined && opened.localIceCandidates.length > 0) {
      events.onLocalIceCandidates(opened.localIceCandidates);
    }
    return {
      answerSdp: opened.answerSdp,
      session: new HostRpcCameraMediaSession(opened.sessionId, this.#host, unsubscribe),
    };
  }
}

class HostRpcCameraMediaSession implements CameraMediaSession {
  readonly #sessionId: string;
  readonly #host: CameraHostMediaClient;
  readonly #unsubscribe: () => void;
  #closed = false;

  constructor(
    sessionId: string,
    host: CameraHostMediaClient,
    unsubscribe: () => void,
  ) {
    this.#sessionId = sessionId;
    this.#host = host;
    this.#unsubscribe = unsubscribe;
  }

  async renegotiate(
    offer: Readonly<CameraOffer> & { sessionId: number },
  ): Promise<string> {
    if (this.#closed) throw new Error("WebRTC host handle is closed");
    return (await this.#host.reofferWebRtc({ sessionId: this.#sessionId, offer })).answerSdp;
  }

  async addRemoteIceCandidates(candidates: readonly IceCandidate[]): Promise<void> {
    if (this.#closed) throw new Error("WebRTC host handle is closed");
    await this.#host.addWebRtcIce({ sessionId: this.#sessionId, candidates });
  }

  async close(reason: CameraSessionEndReason): Promise<void> {
    if (this.#closed) return;
    this.#closed = true;
    this.#unsubscribe();
    await this.#host.closeWebRtc({ sessionId: this.#sessionId, reason });
  }
}
