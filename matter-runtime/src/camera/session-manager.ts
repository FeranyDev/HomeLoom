/**
 * Protocol-neutral session lifecycle for a Matter 1.5 WebRTC Transport Provider.
 *
 * The Matter cluster adapter is intentionally kept outside this module. It maps
 * ProvideOffer/ProvideIceCandidates/EndSession commands to this manager and maps
 * the callbacks below to ProvideAnswer/ProvideIceCandidates commands sent to the
 * requestor. A real WebRTC implementation is supplied through CameraMediaBackend.
 */

export type MatterWebRtcSessionId = number;

export interface IceCandidate {
  candidate: string;
  sdpMid: string | null;
  sdpMLineIndex: number | null;
}

interface IceServer {
  urls: readonly string[];
  username?: string;
  credential?: string;
}

export interface CameraSessionOwner {
  fabricIndex: number;
  peerNodeId: string;
  peerEndpointId: number;
}

export interface CameraOffer extends CameraSessionOwner {
  /**
   * Null corresponds to ProvideOffer.WebRTCSessionID == null. In that case the
   * provider allocates an ID and returns it in ProvideOfferResponse. A non-null
   * ID is exclusively a re-offer for an existing session owned by the same
   * Fabric, peer node and peer endpoint; it never creates a caller-chosen ID.
   */
  sessionId: MatterWebRtcSessionId | null;
  offerSdp: string;
  /**
   * Matter FabricIndex owns the session. Passing it through to the backend
   * prevents a peer node ID on one fabric being confused with the same node ID
   * on another fabric.
   */
  streamUsage: number;
  videoStreamIds?: readonly number[];
  audioStreamIds?: readonly number[];
  iceServers?: readonly IceServer[];
  iceTransportPolicy?: string;
}

export interface CameraOfferResponse {
  sessionId: MatterWebRtcSessionId;
}

export interface CameraSessionDescription extends CameraSessionOwner {
  sessionId: MatterWebRtcSessionId;
  sdp: string;
}

export interface CameraIceCandidates extends CameraSessionOwner {
  sessionId: MatterWebRtcSessionId;
  candidates: readonly IceCandidate[];
}

export type CameraSessionEndReason =
  | "ice-failed"
  | "ice-timeout"
  | "user-hangup"
  | "user-busy"
  | "replaced"
  | "no-user-media"
  | "invite-timeout"
  | "answered-elsewhere"
  | "out-of-resources"
  | "media-timeout"
  | "low-power"
  | "privacy-mode"
  | "unknown"
  | "shutdown"
  | "backend-failure";

export interface CameraMediaSession {
  renegotiate(
    offer: Readonly<CameraOffer> & { sessionId: MatterWebRtcSessionId },
  ): Promise<string>;
  addRemoteIceCandidates(candidates: readonly IceCandidate[]): Promise<void>;
  close(reason: CameraSessionEndReason): Promise<void>;
}

interface CameraMediaSessionResult {
  answerSdp: string;
  session: CameraMediaSession;
}

interface CameraMediaSessionEvents {
  /**
   * The backend may emit candidates before openSession resolves. They are
   * buffered until the answer callback completes so the Matter peer always
   * observes the answer first.
   */
  onLocalIceCandidates(candidates: readonly IceCandidate[]): void;
}

export interface CameraMediaBackend {
  openSession(
    offer: Readonly<CameraOffer> & { sessionId: MatterWebRtcSessionId },
    events: CameraMediaSessionEvents,
  ): Promise<CameraMediaSessionResult>;
}

export interface CameraSessionCallbacks {
  provideAnswer(answer: CameraSessionDescription): Promise<void>;
  provideIceCandidates(update: CameraIceCandidates): Promise<void>;
  sessionFailed?(
    sessionId: MatterWebRtcSessionId,
    error: CameraSessionError,
  ): void | Promise<void>;
}

export interface CameraSessionManagerOptions {
  maxConcurrentSessions: number;
}

export type CameraSessionErrorCode =
  | "invalid-argument"
  | "duplicate-session"
  | "unknown-session"
  | "resource-exhausted"
  | "backend-failure"
  | "manager-closed";

export class CameraSessionError extends Error {
  constructor(
    readonly code: CameraSessionErrorCode,
    message: string,
    options?: ErrorOptions,
  ) {
    super(message, options);
    this.name = "CameraSessionError";
  }
}

type OpeningSession = {
  state: "opening";
  owner: CameraSessionOwner;
  pendingLocalCandidates: IceCandidate[][];
};

type ActiveSession = {
  state: "active";
  owner: CameraSessionOwner;
  renegotiating: boolean;
  media: CameraMediaSession;
  localCandidateDelivery: Promise<void>;
};

type ManagedSession = OpeningSession | ActiveSession;

const MIN_SESSION_ID = 0;
const MAX_SESSION_ID = 65_535;

export class CameraSessionManager {
  readonly #sessions = new Map<MatterWebRtcSessionId, ManagedSession>();
  readonly #backend: CameraMediaBackend;
  readonly #callbacks: CameraSessionCallbacks;
  readonly #maxConcurrentSessions: number;
  #nextSessionId = MIN_SESSION_ID;
  #closed = false;

  constructor(
    backend: CameraMediaBackend,
    callbacks: CameraSessionCallbacks,
    options: CameraSessionManagerOptions,
  ) {
    if (!Number.isSafeInteger(options.maxConcurrentSessions) || options.maxConcurrentSessions < 1) {
      throw new CameraSessionError(
        "invalid-argument",
        "maxConcurrentSessions must be a positive integer",
      );
    }
    this.#backend = backend;
    this.#callbacks = callbacks;
    this.#maxConcurrentSessions = options.maxConcurrentSessions;
  }

  get size(): number {
    return this.#sessions.size;
  }

  get sessionIds(): readonly MatterWebRtcSessionId[] {
    return [...this.#sessions.keys()];
  }

  ownerForSession(
    sessionId: MatterWebRtcSessionId,
    peer: Pick<CameraSessionOwner, "fabricIndex" | "peerNodeId">,
  ): CameraSessionOwner {
    const managed = this.#sessions.get(sessionId);
    if (
      managed === undefined
      || managed.state !== "active"
      || managed.owner.fabricIndex !== peer.fabricIndex
      || managed.owner.peerNodeId !== peer.peerNodeId
    ) {
      throw new CameraSessionError("unknown-session", `camera session ${sessionId} is not active`);
    }
    return { ...managed.owner };
  }

  async provideOffer(offer: CameraOffer): Promise<CameraOfferResponse> {
    this.#assertOpen();
    validateOffer(offer);

    if (offer.sessionId !== null) {
      return this.#provideReoffer(offer as CameraOffer & { sessionId: MatterWebRtcSessionId });
    }
    if (this.#sessions.size >= this.#maxConcurrentSessions) {
      throw new CameraSessionError(
        "resource-exhausted",
        `camera session limit ${this.#maxConcurrentSessions} reached`,
      );
    }
    const sessionId = this.#allocateSessionId();

    const opening: OpeningSession = {
      state: "opening",
      owner: ownerOf(offer),
      pendingLocalCandidates: [],
    };
    this.#sessions.set(sessionId, opening);
    setImmediate(() => {
      void this.#finishOpen(offer, sessionId, opening);
    });
    return { sessionId };
  }

  async #finishOpen(
    offer: CameraOffer,
    sessionId: MatterWebRtcSessionId,
    opening: OpeningSession,
  ): Promise<void> {
    let media: CameraMediaSession | undefined;
    try {
      const result = await this.#backend.openSession(
        { ...offer, sessionId },
        {
          onLocalIceCandidates: (candidates) => {
            if (candidates.length === 0) {
              return;
            }
            const managed = this.#sessions.get(sessionId);
            if (managed === opening) {
              opening.pendingLocalCandidates.push([...candidates]);
            } else if (managed?.state === "active") {
              this.#queueLocalCandidates(sessionId, managed, candidates);
            }
          },
        },
      );
      media = result.session;
      if (this.#sessions.get(sessionId) !== opening) {
        throw new CameraSessionError("manager-closed", "camera session manager is closed");
      }
      if (result.answerSdp.trim().length === 0) {
        throw new CameraSessionError("backend-failure", "media backend returned an empty answer SDP");
      }

      await this.#callbacks.provideAnswer({
        sessionId,
        ...ownerOf(offer),
        sdp: result.answerSdp,
      });
      if (this.#sessions.get(sessionId) !== opening) {
        throw new CameraSessionError("manager-closed", "camera session manager is closed");
      }

      const active: ActiveSession = {
        state: "active",
        owner: ownerOf(offer),
        renegotiating: false,
        media,
        localCandidateDelivery: Promise.resolve(),
      };
      this.#sessions.set(sessionId, active);
      for (const candidates of opening.pendingLocalCandidates) {
        this.#queueLocalCandidates(sessionId, active, candidates);
      }
    } catch (error) {
      if (this.#sessions.get(sessionId) === opening) {
        this.#sessions.delete(sessionId);
      }
      if (media !== undefined) {
        await bestEffortClose(
          media,
          error instanceof CameraSessionError && error.code === "manager-closed"
            ? "shutdown"
            : "backend-failure",
        );
      }
      await this.#notifyFailure(
        sessionId,
        error instanceof CameraSessionError
          ? error
          : new CameraSessionError(
            "backend-failure",
            `failed to open camera session ${sessionId}`,
            { cause: error },
          ),
      );
    }
  }

  async #provideReoffer(
    offer: CameraOffer & { sessionId: MatterWebRtcSessionId },
  ): Promise<CameraOfferResponse> {
    const managed = this.#sessions.get(offer.sessionId);
    if (
      managed === undefined
      || managed.state !== "active"
      || !sameOwner(managed.owner, offer)
    ) {
      // Deliberately do not distinguish unknown IDs from cross-owner access.
      throw new CameraSessionError(
        "unknown-session",
        `camera session ${offer.sessionId} is not active for this owner`,
      );
    }
    if (managed.renegotiating) {
      throw new CameraSessionError(
        "duplicate-session",
        `camera session ${offer.sessionId} is already renegotiating`,
      );
    }

    managed.renegotiating = true;
    setImmediate(() => {
      void this.#finishReoffer(offer, managed);
    });
    return { sessionId: offer.sessionId };
  }

  async #finishReoffer(
    offer: CameraOffer & { sessionId: MatterWebRtcSessionId },
    managed: ActiveSession,
  ): Promise<void> {
    try {
      const answerSdp = await managed.media.renegotiate(offer);
      if (this.#sessions.get(offer.sessionId) !== managed) {
        throw new CameraSessionError(
          this.#closed ? "manager-closed" : "unknown-session",
          `camera session ${offer.sessionId} ended during renegotiation`,
        );
      }
      if (answerSdp.trim().length === 0) {
        throw new CameraSessionError(
          "backend-failure",
          "media backend returned an empty re-offer answer SDP",
        );
      }
      await this.#callbacks.provideAnswer({
        sessionId: offer.sessionId,
        ...managed.owner,
        sdp: answerSdp,
      });
      if (this.#sessions.get(offer.sessionId) !== managed) {
        throw new CameraSessionError(
          this.#closed ? "manager-closed" : "unknown-session",
          `camera session ${offer.sessionId} ended during answer delivery`,
        );
      }
      managed.renegotiating = false;
    } catch (error) {
      // A failed SDP exchange can leave the backend negotiation state
      // indeterminate. Tear down only if this operation still owns the active
      // instance; EndSession/close may already have removed and closed it.
      if (this.#sessions.get(offer.sessionId) === managed) {
        this.#sessions.delete(offer.sessionId);
        await bestEffortClose(managed.media, "backend-failure");
      }
      await this.#notifyFailure(
        offer.sessionId,
        error instanceof CameraSessionError
          ? error
          : new CameraSessionError(
            "backend-failure",
            `failed to renegotiate camera session ${offer.sessionId}`,
            { cause: error },
          ),
      );
    }
  }

  async provideIceCandidates(update: CameraIceCandidates): Promise<void> {
    this.#assertOpen();
    validateSessionId(update.sessionId);
    validateOwner(update);
    validateCandidates(update.candidates);
    if (update.candidates.length === 0) {
      throw new CameraSessionError("invalid-argument", "at least one ICE candidate is required");
    }

    const managed = this.#sessions.get(update.sessionId);
    if (
      managed === undefined
      || managed.state !== "active"
      || !sameOwner(managed.owner, update)
    ) {
      throw new CameraSessionError(
        "unknown-session",
        `camera session ${update.sessionId} is not active`,
      );
    }
    try {
      await managed.media.addRemoteIceCandidates(update.candidates);
    } catch (error) {
      throw new CameraSessionError(
        "backend-failure",
        `failed to add ICE candidates to camera session ${update.sessionId}`,
        { cause: error },
      );
    }
  }

  async endSession(
    sessionId: MatterWebRtcSessionId,
    owner: CameraSessionOwner,
    reason: CameraSessionEndReason,
  ): Promise<void> {
    this.#assertOpen();
    validateSessionId(sessionId);
    validateOwner(owner);
    const managed = this.#sessions.get(sessionId);
    if (
      managed === undefined
      || managed.state !== "active"
      || !sameOwner(managed.owner, owner)
    ) {
      throw new CameraSessionError("unknown-session", `camera session ${sessionId} is not active`);
    }

    // Remove first so duplicate EndSession commands are deterministically rejected
    // and the slot becomes available even if backend cleanup fails.
    this.#sessions.delete(sessionId);
    await managed.localCandidateDelivery.catch(() => undefined);
    try {
      await managed.media.close(reason);
    } catch (error) {
      throw new CameraSessionError(
        "backend-failure",
        `failed to close camera session ${sessionId}`,
        { cause: error },
      );
    }
  }

  async close(reason: CameraSessionEndReason = "shutdown"): Promise<void> {
    if (this.#closed) {
      return;
    }
    this.#closed = true;
    const activeSessions = [...this.#sessions.values()]
      .filter((session): session is ActiveSession => session.state === "active");
    this.#sessions.clear();
    await Promise.allSettled(
      activeSessions.map(async (session) => {
        await session.localCandidateDelivery.catch(() => undefined);
        await session.media.close(reason);
      }),
    );
  }

  #queueLocalCandidates(
    sessionId: MatterWebRtcSessionId,
    managed: ActiveSession,
    candidates: readonly IceCandidate[],
  ): void {
    managed.localCandidateDelivery = managed.localCandidateDelivery
      .then(async () => {
        if (this.#sessions.get(sessionId) !== managed) {
          return;
        }
        await this.#callbacks.provideIceCandidates({
          sessionId,
          ...managed.owner,
          candidates,
        });
      })
      .catch(async () => {
        // Whichever path removes the exact active instance owns media cleanup.
        // EndSession/close remove it first, so an in-flight callback rejection
        // cannot close the same backend session twice.
        if (this.#sessions.get(sessionId) !== managed) {
          return;
        }
        this.#sessions.delete(sessionId);
        await bestEffortClose(managed.media, "backend-failure");
      });
  }

  #allocateSessionId(): MatterWebRtcSessionId {
    for (let offset = MIN_SESSION_ID; offset <= MAX_SESSION_ID; offset += 1) {
      const candidate = (this.#nextSessionId + offset) & MAX_SESSION_ID;
      if (!this.#sessions.has(candidate)) {
        this.#nextSessionId = (candidate + 1) & MAX_SESSION_ID;
        return candidate;
      }
    }
    throw new CameraSessionError("resource-exhausted", "no Matter WebRTC session IDs available");
  }

  #assertOpen(): void {
    if (this.#closed) {
      throw new CameraSessionError("manager-closed", "camera session manager is closed");
    }
  }

  async #notifyFailure(
    sessionId: MatterWebRtcSessionId,
    error: CameraSessionError,
  ): Promise<void> {
    try {
      await this.#callbacks.sessionFailed?.(sessionId, error);
    } catch {
      // The session is already cleaned up; diagnostics must not resurrect it.
    }
  }
}

async function bestEffortClose(
  media: CameraMediaSession,
  reason: CameraSessionEndReason,
): Promise<void> {
  try {
    await media.close(reason);
  } catch {
    // Preserve the original setup failure.
  }
}

function validateOffer(offer: CameraOffer): void {
  if (offer.sessionId !== null) {
    validateSessionId(offer.sessionId);
  }
  if (offer.offerSdp.trim().length === 0) {
    throw new CameraSessionError("invalid-argument", "offer SDP must not be empty");
  }
  validateOwner(offer);
  validateUnsignedInteger(offer.streamUsage, "streamUsage", 255);
}

function validateOwner(owner: CameraSessionOwner): void {
  validatePositiveInteger(owner.fabricIndex, "fabricIndex", 254);
  if (owner.peerNodeId.trim().length === 0) {
    throw new CameraSessionError("invalid-argument", "peerNodeId must not be empty");
  }
  validateUnsignedInteger(owner.peerEndpointId, "peerEndpointId", 65_535);
}

function ownerOf(owner: CameraSessionOwner): CameraSessionOwner {
  return {
    fabricIndex: owner.fabricIndex,
    peerNodeId: owner.peerNodeId,
    peerEndpointId: owner.peerEndpointId,
  };
}

function sameOwner(left: CameraSessionOwner, right: CameraSessionOwner): boolean {
  return left.fabricIndex === right.fabricIndex
    && left.peerNodeId === right.peerNodeId
    && left.peerEndpointId === right.peerEndpointId;
}

function validateSessionId(sessionId: number): void {
  validateUnsignedInteger(sessionId, "sessionId", MAX_SESSION_ID);
}

function validateCandidates(candidates: readonly IceCandidate[]): void {
  for (const candidate of candidates) {
    if (candidate.candidate.trim().length === 0) {
      throw new CameraSessionError("invalid-argument", "ICE candidate must not be empty");
    }
    if (candidate.sdpMid !== null && candidate.sdpMid.length === 0) {
      throw new CameraSessionError("invalid-argument", "sdpMid must be null or non-empty");
    }
    if (candidate.sdpMLineIndex !== null) {
      validateUnsignedInteger(candidate.sdpMLineIndex, "sdpMLineIndex", 65_535);
    }
  }
}

function validateUnsignedInteger(value: number, name: string, maximum: number): void {
  if (!Number.isSafeInteger(value) || value < 0 || value > maximum) {
    throw new CameraSessionError(
      "invalid-argument",
      `${name} must be an integer between 0 and ${maximum}`,
    );
  }
}

function validatePositiveInteger(value: number, name: string, maximum: number): void {
  if (!Number.isSafeInteger(value) || value < 1 || value > maximum) {
    throw new CameraSessionError(
      "invalid-argument",
      `${name} must be an integer between 1 and ${maximum}`,
    );
  }
}
