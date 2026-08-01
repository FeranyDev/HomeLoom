import {
  CameraAvAllocationManager,
  type CameraAvOwner,
  type AudioAllocation,
  type AudioAllocationRequest,
  type CameraAvAllocationSnapshot,
  type SnapshotAllocation,
  type SnapshotAllocationRequest,
  type VideoAllocation,
  type VideoAllocationRequest,
  type VideoModification,
  type CameraMediaIdentity,
  type VideoResolution,
} from "./av-allocation-manager.js";
import type {
  CameraHostMediaClient,
  CameraSnapshotResult,
} from "./host-media-client.js";
import {
  CameraSessionManager,
  type CameraIceCandidates,
  type CameraOffer,
  type CameraOfferResponse,
  type CameraSessionEndReason,
  type CameraSessionOwner,
} from "./session-manager.js";

/**
 * Narrow seam consumed by future matter.js custom behaviors. It contains no
 * SDK types, making command semantics independently testable and keeping all
 * media access behind the injected host clients of the two managers.
 */
export class MatterCameraClusterAdapter {
  readonly #allocations: CameraAvAllocationManager;
  readonly #sessions: CameraSessionManager;
  readonly #snapshotHost: CameraHostMediaClient | undefined;
  readonly #media: CameraMediaIdentity | undefined;

  constructor(
    allocations: CameraAvAllocationManager,
    sessions: CameraSessionManager,
    snapshot?: {
      host: CameraHostMediaClient;
      media: CameraMediaIdentity;
    },
  ) {
    this.#allocations = allocations;
    this.#sessions = sessions;
    this.#snapshotHost = snapshot?.host;
    this.#media = snapshot === undefined ? undefined : { ...snapshot.media };
  }

  get allocationState(): CameraAvAllocationSnapshot {
    return this.#allocations.state;
  }

  allocateVideo(
    owner: CameraAvOwner,
    request: VideoAllocationRequest,
  ): Promise<VideoAllocation> {
    return this.#allocations.allocateVideo(owner, request);
  }

  allocateAudio(
    owner: CameraAvOwner,
    request: AudioAllocationRequest,
  ): Promise<AudioAllocation> {
    return this.#allocations.allocateAudio(owner, request);
  }

  allocateSnapshot(
    owner: CameraAvOwner,
    request: SnapshotAllocationRequest,
  ): Promise<SnapshotAllocation> {
    return this.#allocations.allocateSnapshot(owner, request);
  }

  modifyVideo(owner: CameraAvOwner, request: VideoModification): Promise<VideoAllocation> {
    return this.#allocations.modifyVideo(owner, request);
  }

  deallocateVideo(owner: CameraAvOwner, id: number): Promise<void> {
    return this.#allocations.deallocateVideo(owner, id);
  }

  deallocateAudio(owner: CameraAvOwner, id: number): Promise<void> {
    return this.#allocations.deallocateAudio(owner, id);
  }

  deallocateSnapshot(owner: CameraAvOwner, id: number): Promise<void> {
    return this.#allocations.deallocateSnapshot(owner, id);
  }

  async captureSnapshot(
    owner: CameraAvOwner,
    snapshotStreamId: number,
    resolution: VideoResolution,
  ): Promise<CameraSnapshotResult> {
    const allocation = this.#allocations.ownedSnapshot(snapshotStreamId, owner);
    if (
      resolution.width !== allocation.minResolution.width
      || resolution.height !== allocation.minResolution.height
      || resolution.width !== allocation.maxResolution.width
      || resolution.height !== allocation.maxResolution.height
    ) {
      throw new TypeError("requested snapshot resolution is outside the allocated capability");
    }
    if (this.#snapshotHost === undefined || this.#media === undefined) {
      throw new Error("snapshot reverse RPC is not configured");
    }
    return this.#snapshotHost.captureSnapshot({
      media: { ...this.#media },
      owner: { ...owner },
      snapshotStreamId,
      resolution: { ...resolution },
    });
  }

  async provideOffer(offer: CameraOffer): Promise<CameraOfferResponse> {
    const videoIds = offer.videoStreamIds ?? [];
    const audioIds = offer.audioStreamIds ?? [];
    if (videoIds.length !== 1 || audioIds.length > 1) {
      throw new TypeError("camera offer requires one video stream and at most one audio stream");
    }
    const avOwner: CameraAvOwner = {
      fabricIndex: offer.fabricIndex,
      peerNodeId: offer.peerNodeId,
    };
    this.#allocations.assertOwned("video", videoIds[0]!, avOwner);
    for (const id of audioIds) {
      this.#allocations.assertOwned("audio", id, avOwner);
    }
    return this.#sessions.provideOffer(offer);
  }

  provideIceCandidates(update: CameraIceCandidates): Promise<void> {
    return this.#sessions.provideIceCandidates(update);
  }

  endSession(
    sessionId: number,
    owner: CameraSessionOwner,
    reason: CameraSessionEndReason,
  ): Promise<void> {
    return this.#sessions.endSession(sessionId, owner, reason);
  }

  sessionOwner(
    sessionId: number,
    peer: CameraAvOwner,
  ): CameraSessionOwner {
    return this.#sessions.ownerForSession(sessionId, peer);
  }

  async close(): Promise<void> {
    await Promise.allSettled([
      this.#sessions.close(),
      this.#allocations.close(),
    ]);
  }
}
