export interface CameraAvOwner {
  fabricIndex: number;
  peerNodeId: string;
}

export type CameraAvResourceKind = "video" | "audio" | "snapshot";

export interface CameraMediaIdentity {
  deviceId: string;
  streamId: string;
}

export interface VideoResolution {
  width: number;
  height: number;
}

export interface VideoAllocationRequest {
  streamUsage: number;
  videoCodec: number;
  minFrameRate: number;
  maxFrameRate: number;
  minResolution: VideoResolution;
  maxResolution: VideoResolution;
  minBitRate: number;
  maxBitRate: number;
  keyFrameInterval: number;
  watermarkEnabled?: boolean;
  osdEnabled?: boolean;
}

export interface VideoAllocation {
  videoStreamId: number;
  streamUsage: number;
  videoCodec: 0;
  minFrameRate: number;
  maxFrameRate: number;
  minResolution: VideoResolution;
  maxResolution: VideoResolution;
  minBitRate: number;
  maxBitRate: number;
  keyFrameInterval: number;
  watermarkEnabled: boolean;
  osdEnabled: boolean;
  referenceCount: 1;
}

export interface AudioAllocationRequest {
  streamUsage: number;
  audioCodec: number;
  channelCount: number;
  sampleRate: number;
  bitRate: number;
  bitDepth: number;
}

export interface AudioAllocation {
  audioStreamId: number;
  streamUsage: number;
  audioCodec: 0;
  channelCount: 1;
  sampleRate: 48_000;
  bitRate: number;
  bitDepth: 16;
  referenceCount: 1;
}

export interface SnapshotAllocationRequest {
  imageCodec: number;
  maxFrameRate: number;
  minResolution: VideoResolution;
  maxResolution: VideoResolution;
  quality: number;
  watermarkEnabled?: boolean;
  osdEnabled?: boolean;
}

export interface SnapshotAllocation {
  snapshotStreamId: number;
  imageCodec: 0;
  frameRate: number;
  minResolution: VideoResolution;
  maxResolution: VideoResolution;
  quality: number;
  referenceCount: 1;
  encodedPixels: false;
  hardwareEncoder: false;
  watermarkEnabled: boolean;
  osdEnabled: boolean;
}

export interface VideoModification {
  videoStreamId: number;
  watermarkEnabled?: boolean;
  osdEnabled?: boolean;
}

export type CameraAvAllocation = VideoAllocation | AudioAllocation | SnapshotAllocation;

/**
 * Host RPC boundary. Implementations may control ffmpeg/WebRTC elsewhere, but
 * this request contains only opaque media identity and negotiated parameters.
 * Source URIs and credentials are intentionally absent.
 */
export interface CameraAvHostClient {
  allocate(request: {
    media: CameraMediaIdentity;
    owner: CameraAvOwner;
    kind: CameraAvResourceKind;
    allocationId: number;
    allocation: CameraAvAllocation;
  }): Promise<CameraAvHostLease>;
}

export interface CameraAvHostLease {
  modifyVideo?(modification: VideoModification): Promise<void>;
  close(): Promise<void>;
}

export interface CameraAvAllocationSnapshot {
  video: readonly VideoAllocation[];
  audio: readonly AudioAllocation[];
  snapshot: readonly SnapshotAllocation[];
}

export type CameraAvAllocationErrorCode =
  | "invalid-request"
  | "resource-exhausted"
  | "unknown-allocation"
  | "host-failure"
  | "manager-closed";

export class CameraAvAllocationError extends Error {
  constructor(
    readonly code: CameraAvAllocationErrorCode,
    message: string,
    options?: ErrorOptions,
  ) {
    super(message, options);
    this.name = "CameraAvAllocationError";
  }
}

type Stored<T extends CameraAvAllocation> = {
  owner: CameraAvOwner;
  allocation: T;
  lease: CameraAvHostLease;
};

const VIDEO_RESOLUTIONS: readonly VideoResolution[] = [
  { width: 1_280, height: 720 },
  { width: 640, height: 360 },
];

export class CameraAvAllocationManager {
  readonly #media: CameraMediaIdentity;
  readonly #host: CameraAvHostClient;
  readonly #onChanged: (snapshot: CameraAvAllocationSnapshot) => void;
  readonly #video = new Map<number, Stored<VideoAllocation>>();
  readonly #audio = new Map<number, Stored<AudioAllocation>>();
  readonly #snapshot = new Map<number, Stored<SnapshotAllocation>>();
  readonly #pending = new Set<CameraAvResourceKind>();
  readonly #pendingHostAllocations = new Set<Promise<CameraAvHostLease>>();
  #nextId = 1;
  #closed = false;

  constructor(
    media: CameraMediaIdentity,
    host: CameraAvHostClient,
    onChanged: (snapshot: CameraAvAllocationSnapshot) => void = () => undefined,
  ) {
    if (media.deviceId.trim() === "" || media.streamId.trim() === "") {
      throw new CameraAvAllocationError("invalid-request", "media identity must be non-empty");
    }
    this.#media = { ...media };
    this.#host = host;
    this.#onChanged = onChanged;
  }

  get state(): CameraAvAllocationSnapshot {
    return {
      video: [...this.#video.values()].map(({ allocation }) => structuredClone(allocation)),
      audio: [...this.#audio.values()].map(({ allocation }) => structuredClone(allocation)),
      snapshot: [...this.#snapshot.values()].map(({ allocation }) => structuredClone(allocation)),
    };
  }

  async allocateVideo(
    owner: CameraAvOwner,
    request: VideoAllocationRequest,
  ): Promise<VideoAllocation> {
    validateOwner(owner);
    const resolution = selectResolution(request.minResolution, request.maxResolution);
    if (
      request.videoCodec !== 0
      || !integerBetween(request.minFrameRate, 1, 30)
      || !integerBetween(request.maxFrameRate, request.minFrameRate, 30)
      || !integerBetween(request.minBitRate, 1, 4_000_000)
      || !integerBetween(request.maxBitRate, request.minBitRate, 4_000_000)
      || !integerBetween(request.keyFrameInterval, 0, 65_535)
    ) {
      invalid("unsupported H.264 video allocation parameters");
    }
    const allocation: VideoAllocation = {
      videoStreamId: this.#allocateId(),
      streamUsage: validateStreamUsage(request.streamUsage),
      videoCodec: 0,
      minFrameRate: request.minFrameRate,
      maxFrameRate: request.maxFrameRate,
      minResolution: { ...resolution },
      maxResolution: { ...resolution },
      minBitRate: request.minBitRate,
      maxBitRate: request.maxBitRate,
      keyFrameInterval: request.keyFrameInterval,
      watermarkEnabled: request.watermarkEnabled ?? false,
      osdEnabled: request.osdEnabled ?? false,
      referenceCount: 1,
    };
    return this.#allocate("video", owner, allocation, this.#video);
  }

  async allocateAudio(
    owner: CameraAvOwner,
    request: AudioAllocationRequest,
  ): Promise<AudioAllocation> {
    validateOwner(owner);
    if (
      request.audioCodec !== 0
      || request.channelCount !== 1
      || request.sampleRate !== 48_000
      || request.bitDepth !== 16
      || !integerBetween(request.bitRate, 6_000, 510_000)
    ) {
      invalid("unsupported Opus 48 kHz mono audio allocation parameters");
    }
    const allocation: AudioAllocation = {
      audioStreamId: this.#allocateId(),
      streamUsage: validateStreamUsage(request.streamUsage),
      audioCodec: 0,
      channelCount: 1,
      sampleRate: 48_000,
      bitRate: request.bitRate,
      bitDepth: 16,
      referenceCount: 1,
    };
    return this.#allocate("audio", owner, allocation, this.#audio);
  }

  async allocateSnapshot(
    owner: CameraAvOwner,
    request: SnapshotAllocationRequest,
  ): Promise<SnapshotAllocation> {
    validateOwner(owner);
    const resolution = selectResolution(request.minResolution, request.maxResolution);
    if (
      request.imageCodec !== 0
      || !integerBetween(request.maxFrameRate, 1, 1)
      || !integerBetween(request.quality, 1, 100)
    ) {
      invalid("unsupported JPEG snapshot allocation parameters");
    }
    const allocation: SnapshotAllocation = {
      snapshotStreamId: this.#allocateId(),
      imageCodec: 0,
      frameRate: request.maxFrameRate,
      minResolution: { ...resolution },
      maxResolution: { ...resolution },
      quality: request.quality,
      referenceCount: 1,
      encodedPixels: false,
      hardwareEncoder: false,
      watermarkEnabled: request.watermarkEnabled ?? false,
      osdEnabled: request.osdEnabled ?? false,
    };
    return this.#allocate("snapshot", owner, allocation, this.#snapshot);
  }

  async modifyVideo(owner: CameraAvOwner, change: VideoModification): Promise<VideoAllocation> {
    this.#assertOpen();
    validateOwner(owner);
    const stored = this.#owned(this.#video, change.videoStreamId, owner);
    if (change.watermarkEnabled === undefined && change.osdEnabled === undefined) {
      invalid("video modification must contain watermarkEnabled or osdEnabled");
    }
    if (stored.lease.modifyVideo === undefined) {
      throw new CameraAvAllocationError(
        "host-failure",
        "host media lease does not support video modification",
      );
    }
    const previous = structuredClone(stored.allocation);
    const updated: VideoAllocation = {
      ...stored.allocation,
      watermarkEnabled: change.watermarkEnabled ?? stored.allocation.watermarkEnabled,
      osdEnabled: change.osdEnabled ?? stored.allocation.osdEnabled,
    };
    try {
      await stored.lease.modifyVideo(change);
      stored.allocation = updated;
      this.#emitChanged();
      return structuredClone(updated);
    } catch (error) {
      stored.allocation = previous;
      throw hostFailure("failed to modify video allocation", error);
    }
  }

  deallocateVideo(owner: CameraAvOwner, id: number): Promise<void> {
    return this.#deallocate(this.#video, id, owner);
  }

  deallocateAudio(owner: CameraAvOwner, id: number): Promise<void> {
    return this.#deallocate(this.#audio, id, owner);
  }

  deallocateSnapshot(owner: CameraAvOwner, id: number): Promise<void> {
    return this.#deallocate(this.#snapshot, id, owner);
  }

  assertOwned(kind: CameraAvResourceKind, id: number, owner: CameraAvOwner): void {
    this.#owned(this.#map(kind), id, owner);
  }

  ownedSnapshot(id: number, owner: CameraAvOwner): SnapshotAllocation {
    return structuredClone(this.#owned(this.#snapshot, id, owner).allocation);
  }

  async close(): Promise<void> {
    if (this.#closed) {
      return;
    }
    this.#closed = true;
    const leases = [
      ...this.#video.values(),
      ...this.#audio.values(),
      ...this.#snapshot.values(),
    ].map(({ lease }) => lease);
    this.#video.clear();
    this.#audio.clear();
    this.#snapshot.clear();
    this.#emitChanged();
    await Promise.allSettled([
      ...leases.map((lease) => lease.close()),
      ...this.#pendingHostAllocations,
    ]);
  }

  async #allocate<T extends CameraAvAllocation>(
    kind: CameraAvResourceKind,
    owner: CameraAvOwner,
    allocation: T,
    allocations: Map<number, Stored<T>>,
  ): Promise<T> {
    this.#assertOpen();
    if (allocations.size !== 0 || this.#pending.has(kind)) {
      throw new CameraAvAllocationError(
        "resource-exhausted",
        `${kind} allocation limit 1 reached`,
      );
    }
    this.#pending.add(kind);
    let lease: CameraAvHostLease | undefined;
    try {
      const hostAllocation = this.#host.allocate({
        media: { ...this.#media },
        owner: copyOwner(owner),
        kind,
        allocationId: allocationId(allocation),
        allocation: structuredClone(allocation),
      });
      this.#pendingHostAllocations.add(hostAllocation);
      try {
        lease = await hostAllocation;
      } finally {
        this.#pendingHostAllocations.delete(hostAllocation);
      }
      if (this.#closed) {
        await bestEffortClose(lease);
        throw new CameraAvAllocationError("manager-closed", "AV allocation manager is closed");
      }
      allocations.set(allocationId(allocation), {
        owner: copyOwner(owner),
        allocation,
        lease,
      });
      this.#emitChanged();
      return structuredClone(allocation);
    } catch (error) {
      if (error instanceof CameraAvAllocationError) {
        throw error;
      }
      if (lease !== undefined) {
        await bestEffortClose(lease);
      }
      throw hostFailure(`failed to allocate ${kind} stream`, error);
    } finally {
      this.#pending.delete(kind);
    }
  }

  async #deallocate<T extends CameraAvAllocation>(
    allocations: Map<number, Stored<T>>,
    id: number,
    owner: CameraAvOwner,
  ): Promise<void> {
    this.#assertOpen();
    validateOwner(owner);
    const stored = this.#owned(allocations, id, owner);
    allocations.delete(id);
    this.#emitChanged();
    try {
      await stored.lease.close();
    } catch (error) {
      throw hostFailure(`failed to close allocation ${id}`, error);
    }
  }

  #owned<T extends CameraAvAllocation>(
    allocations: Map<number, Stored<T>>,
    id: number,
    owner: CameraAvOwner,
  ): Stored<T> {
    this.#assertOpen();
    validateId(id);
    validateOwner(owner);
    const stored = allocations.get(id);
    if (stored === undefined || !sameOwner(stored.owner, owner)) {
      throw new CameraAvAllocationError(
        "unknown-allocation",
        `allocation ${id} is not available for this owner`,
      );
    }
    return stored;
  }

  #map(kind: CameraAvResourceKind): Map<number, Stored<CameraAvAllocation>> {
    if (kind === "video") return this.#video as Map<number, Stored<CameraAvAllocation>>;
    if (kind === "audio") return this.#audio as Map<number, Stored<CameraAvAllocation>>;
    return this.#snapshot as Map<number, Stored<CameraAvAllocation>>;
  }

  #allocateId(): number {
    const id = this.#nextId;
    this.#nextId = this.#nextId === 65_535 ? 1 : this.#nextId + 1;
    return id;
  }

  #emitChanged(): void {
    this.#onChanged(this.state);
  }

  #assertOpen(): void {
    if (this.#closed) {
      throw new CameraAvAllocationError("manager-closed", "AV allocation manager is closed");
    }
  }
}

function selectResolution(minimum: VideoResolution, maximum: VideoResolution): VideoResolution {
  validateResolution(minimum);
  validateResolution(maximum);
  const selected = VIDEO_RESOLUTIONS.find(
    ({ width, height }) => width >= minimum.width
      && height >= minimum.height
      && width <= maximum.width
      && height <= maximum.height,
  );
  if (selected === undefined) {
    invalid("resolution range does not include 1280x720 or 640x360");
  }
  return selected;
}

function validateResolution(value: VideoResolution): void {
  if (!integerBetween(value.width, 1, 65_535) || !integerBetween(value.height, 1, 65_535)) {
    invalid("resolution must contain positive uint16 dimensions");
  }
}

function validateStreamUsage(value: number): number {
  if (!integerBetween(value, 0, 3)) {
    invalid("streamUsage must be a known Matter stream usage");
  }
  return value;
}

function validateOwner(owner: CameraAvOwner): void {
  if (
    !integerBetween(owner.fabricIndex, 1, 254)
    || owner.peerNodeId.trim() === ""
  ) {
    invalid("invalid Matter allocation owner");
  }
}

function validateId(id: number): void {
  if (!integerBetween(id, 1, 65_535)) {
    invalid("allocation ID must be a positive uint16");
  }
}

function integerBetween(value: number, minimum: number, maximum: number): boolean {
  return Number.isSafeInteger(value) && value >= minimum && value <= maximum;
}

function allocationId(allocation: CameraAvAllocation): number {
  if ("videoStreamId" in allocation) return allocation.videoStreamId;
  if ("audioStreamId" in allocation) return allocation.audioStreamId;
  return allocation.snapshotStreamId;
}

function copyOwner(owner: CameraAvOwner): CameraAvOwner {
  return { ...owner };
}

function sameOwner(left: CameraAvOwner, right: CameraAvOwner): boolean {
  return left.fabricIndex === right.fabricIndex
    && left.peerNodeId === right.peerNodeId;
}

function invalid(message: string): never {
  throw new CameraAvAllocationError("invalid-request", message);
}

function hostFailure(message: string, cause: unknown): CameraAvAllocationError {
  return new CameraAvAllocationError("host-failure", message, { cause });
}

async function bestEffortClose(lease: CameraAvHostLease): Promise<void> {
  try {
    await lease.close();
  } catch {
    // Preserve the originating failure.
  }
}
