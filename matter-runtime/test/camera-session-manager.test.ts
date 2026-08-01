import assert from "node:assert/strict";
import test from "node:test";
import {
  CameraSessionError,
  CameraSessionManager,
  type CameraIceCandidates,
  type CameraMediaBackend,
  type CameraMediaSession,
  type CameraOffer,
  type CameraSessionDescription,
  type CameraSessionEndReason,
  type CameraSessionOwner,
  type IceCandidate,
} from "../src/camera/session-manager.js";

const candidate: IceCandidate = {
  candidate: "candidate:1 1 UDP 2130706431 192.0.2.10 5000 typ host",
  sdpMid: "0",
  sdpMLineIndex: 0,
};

function owner(overrides: Partial<CameraSessionOwner> = {}): CameraSessionOwner {
  return {
    fabricIndex: 1,
    peerNodeId: "0x1234",
    peerEndpointId: 2,
    ...overrides,
  };
}

function offer(sessionId: number | null = null): CameraOffer {
  return {
    ...owner(),
    sessionId,
    offerSdp: "v=0\r\no=requestor 1 1 IN IP4 192.0.2.1",
    streamUsage: 0,
    videoStreamIds: [1],
  };
}

class FakeMediaSession implements CameraMediaSession {
  readonly renegotiationOffers: CameraOffer[] = [];
  readonly remoteCandidates: IceCandidate[][] = [];
  readonly closeReasons: CameraSessionEndReason[] = [];
  closeError: Error | undefined;
  renegotiateImpl:
    | ((offer: Readonly<CameraOffer> & { sessionId: number }) => Promise<string>)
    | undefined;

  async renegotiate(
    offer: Readonly<CameraOffer> & { sessionId: number },
  ): Promise<string> {
    this.renegotiationOffers.push(offer);
    return this.renegotiateImpl?.(offer) ?? "v=0\r\no=provider-renegotiated";
  }

  async addRemoteIceCandidates(candidates: readonly IceCandidate[]): Promise<void> {
    this.remoteCandidates.push([...candidates]);
  }

  async close(reason: CameraSessionEndReason): Promise<void> {
    this.closeReasons.push(reason);
    if (this.closeError !== undefined) {
      throw this.closeError;
    }
  }
}

function managerFixture(options: {
  maxConcurrentSessions?: number;
  beforeOpen?: (events: { onLocalIceCandidates(candidates: readonly IceCandidate[]): void }) => void;
  openError?: Error;
} = {}) {
  const mediaSessions: FakeMediaSession[] = [];
  const answers: CameraSessionDescription[] = [];
  const localCandidates: CameraIceCandidates[] = [];
  const events: string[] = [];
  const failures: CameraSessionError[] = [];
  const backend: CameraMediaBackend = {
    openSession: async (_request, sessionEvents) => {
      options.beforeOpen?.(sessionEvents);
      if (options.openError !== undefined) {
        throw options.openError;
      }
      const media = new FakeMediaSession();
      mediaSessions.push(media);
      return {
        answerSdp: "v=0\r\no=provider 2 2 IN IP4 192.0.2.2",
        session: media,
      };
    },
  };
  const manager = new CameraSessionManager(
    backend,
    {
      provideAnswer: async (answer) => {
        events.push("answer");
        answers.push(answer);
      },
      provideIceCandidates: async (update) => {
        events.push("candidate");
        localCandidates.push(update);
      },
      sessionFailed: async (_sessionId, error) => {
        failures.push(error);
      },
    },
    { maxConcurrentSessions: options.maxConcurrentSessions ?? 2 },
  );
  return { manager, mediaSessions, answers, localCandidates, events, failures };
}

test("ProvideOffer creates media and sends answer before buffered trickle ICE", async () => {
  let mediaEvents:
    | { onLocalIceCandidates(candidates: readonly IceCandidate[]): void }
    | undefined;
  const fixture = managerFixture({
    beforeOpen: (events) => {
      mediaEvents = events;
      events.onLocalIceCandidates([candidate]);
    },
  });

  assert.deepEqual(await fixture.manager.provideOffer(offer()), { sessionId: 0 });
  await tick();
  mediaEvents?.onLocalIceCandidates([{
    ...candidate,
    candidate: "candidate:2 1 UDP 2130706430 192.0.2.10 5001 typ host",
  }]);
  await tick();

  assert.equal(fixture.manager.size, 1);
  assert.deepEqual(fixture.manager.sessionIds, [0]);
  assert.equal(fixture.answers[0]?.sessionId, 0);
  assert.equal(fixture.answers[0]?.fabricIndex, 1);
  assert.equal(fixture.answers[0]?.peerNodeId, "0x1234");
  assert.equal(fixture.answers[0]?.peerEndpointId, 2);
  assert.equal(fixture.localCandidates.length, 2);
  assert.ok(
    fixture.localCandidates.every(
      (update) => update.sessionId === 0
        && update.fabricIndex === 1
        && update.peerNodeId === "0x1234"
        && update.peerEndpointId === 2,
    ),
  );
  assert.deepEqual(fixture.events, ["answer", "candidate", "candidate"]);
});

test("null session ID is allocated and returned to the cluster adapter", async () => {
  const fixture = managerFixture();

  const first = await fixture.manager.provideOffer(offer(null));
  const second = await fixture.manager.provideOffer(offer(null));

  assert.notEqual(first.sessionId, second.sessionId);
  assert.deepEqual(fixture.manager.sessionIds, [first.sessionId, second.sessionId]);
});

test("ProvideOfferResponse does not wait for media open or asynchronous Answer", async () => {
  let releaseOpen:
    | ((result: { answerSdp: string; session: CameraMediaSession }) => void)
    | undefined;
  const mediaOpen = new Promise<{ answerSdp: string; session: CameraMediaSession }>((resolve) => {
    releaseOpen = resolve;
  });
  const answers: CameraSessionDescription[] = [];
  const manager = new CameraSessionManager(
    { openSession: async () => mediaOpen },
    {
      provideAnswer: async (answer) => {
        answers.push(answer);
      },
      provideIceCandidates: async () => undefined,
    },
    { maxConcurrentSessions: 1 },
  );

  assert.deepEqual(await manager.provideOffer(offer()), { sessionId: 0 });
  assert.equal(answers.length, 0);
  releaseOpen?.({
    answerSdp: "v=0\r\no=provider",
    session: new FakeMediaSession(),
  });
  await tick();
  assert.equal(answers.length, 1);
});

test("Offer requires a valid operational FabricIndex before allocating resources", async () => {
  const fixture = managerFixture();

  await rejectsWithCode(
    fixture.manager.provideOffer({ ...offer(), fabricIndex: 0 }),
    "invalid-argument",
  );
  await rejectsWithCode(
    fixture.manager.provideOffer({ ...offer(), fabricIndex: 255 }),
    "invalid-argument",
  );

  assert.equal(fixture.manager.size, 0);
  assert.equal(fixture.mediaSessions.length, 0);
});

test("remote trickle ICE is routed only to an active known session", async () => {
  const fixture = managerFixture();
  const { sessionId } = await fixture.manager.provideOffer(offer());
  await tick();

  await fixture.manager.provideIceCandidates({
    ...owner(),
    sessionId,
    candidates: [candidate],
  });
  assert.deepEqual(fixture.mediaSessions[0]?.remoteCandidates, [[candidate]]);

  await rejectsWithCode(
    fixture.manager.provideIceCandidates({
      ...owner(),
      sessionId: 45,
      candidates: [candidate],
    }),
    "unknown-session",
  );
  await rejectsWithCode(
    fixture.manager.provideIceCandidates({ ...owner(), sessionId, candidates: [] }),
    "invalid-argument",
  );
  await rejectsWithCode(
    fixture.manager.provideIceCandidates({
      ...owner({ fabricIndex: 2 }),
      sessionId,
      candidates: [candidate],
    }),
    "unknown-session",
  );
  await rejectsWithCode(
    fixture.manager.provideIceCandidates({
      ...owner({ peerNodeId: "0x9999" }),
      sessionId,
      candidates: [candidate],
    }),
    "unknown-session",
  );
  await rejectsWithCode(
    fixture.manager.provideIceCandidates({
      ...owner({ peerEndpointId: 3 }),
      sessionId,
      candidates: [candidate],
    }),
    "unknown-session",
  );
});

test("only null session ID creates a session and the concurrent-session limit is enforced", async () => {
  const fixture = managerFixture({ maxConcurrentSessions: 1 });
  const created = await fixture.manager.provideOffer(offer());
  await tick();

  await rejectsWithCode(fixture.manager.provideOffer(offer()), "resource-exhausted");
  await rejectsWithCode(fixture.manager.provideOffer(offer(65_000)), "unknown-session");
  assert.deepEqual(fixture.manager.sessionIds, [created.sessionId]);
  assert.equal(fixture.mediaSessions.length, 1);
});

test("non-null session ID renegotiates an existing session and delivers a new answer", async () => {
  const fixture = managerFixture();
  const created = await fixture.manager.provideOffer(offer());
  await tick();
  const reoffer = {
    ...offer(created.sessionId),
    offerSdp: "v=0\r\no=requestor-renegotiated",
  };

  assert.deepEqual(await fixture.manager.provideOffer(reoffer), created);
  await tick();
  assert.deepEqual(fixture.mediaSessions[0]?.renegotiationOffers, [reoffer]);
  assert.equal(fixture.answers.length, 2);
  assert.equal(fixture.answers[1]?.sdp, "v=0\r\no=provider-renegotiated");
  assert.equal(fixture.manager.size, 1);
});

test("re-offer rejects unknown and cross-owner sessions without revealing ownership", async () => {
  const fixture = managerFixture();
  const { sessionId } = await fixture.manager.provideOffer(offer());
  await tick();

  await rejectsWithCode(fixture.manager.provideOffer(offer(999)), "unknown-session");
  await rejectsWithCode(
    fixture.manager.provideOffer({ ...offer(sessionId), fabricIndex: 2 }),
    "unknown-session",
  );
  await rejectsWithCode(
    fixture.manager.provideOffer({ ...offer(sessionId), peerNodeId: "0x9999" }),
    "unknown-session",
  );
  await rejectsWithCode(
    fixture.manager.provideOffer({ ...offer(sessionId), peerEndpointId: 3 }),
    "unknown-session",
  );

  assert.equal(fixture.mediaSessions[0]?.renegotiationOffers.length, 0);
  assert.equal(fixture.manager.size, 1);
});

test("concurrent re-offer of the same session is rejected as busy", async () => {
  const fixture = managerFixture();
  const { sessionId } = await fixture.manager.provideOffer(offer());
  await tick();
  let finishRenegotiation: ((answer: string) => void) | undefined;
  fixture.mediaSessions[0]!.renegotiateImpl = async () => new Promise<string>((resolve) => {
    finishRenegotiation = resolve;
  });

  const first = await fixture.manager.provideOffer(offer(sessionId));
  await rejectsWithCode(
    fixture.manager.provideOffer(offer(sessionId)),
    "duplicate-session",
  );
  finishRenegotiation?.("v=0\r\no=provider-renegotiated");
  await tick();

  assert.deepEqual(first, { sessionId });
  assert.equal(fixture.manager.size, 1);
});

test("failed re-offer deterministically closes and removes the old session", async () => {
  const fixture = managerFixture({ maxConcurrentSessions: 1 });
  const { sessionId } = await fixture.manager.provideOffer(offer());
  await tick();
  fixture.mediaSessions[0]!.renegotiateImpl = async () => {
    throw new Error("setRemoteDescription failed");
  };

  assert.deepEqual(await fixture.manager.provideOffer(offer(sessionId)), { sessionId });
  await tick();

  assert.equal(fixture.manager.size, 0);
  assert.equal(fixture.failures[0]?.code, "backend-failure");
  assert.deepEqual(fixture.mediaSessions[0]?.closeReasons, ["backend-failure"]);
  assert.deepEqual(await fixture.manager.provideOffer(offer()), { sessionId: 1 });
});

test("EndSession cleans the slot and duplicate or unknown end is rejected", async () => {
  const fixture = managerFixture({ maxConcurrentSessions: 1 });
  const { sessionId } = await fixture.manager.provideOffer(offer());
  await tick();

  await rejectsWithCode(
    fixture.manager.endSession(sessionId, owner({ fabricIndex: 2 }), "user-hangup"),
    "unknown-session",
  );
  await rejectsWithCode(
    fixture.manager.endSession(
      sessionId,
      owner({ peerNodeId: "0x9999" }),
      "user-hangup",
    ),
    "unknown-session",
  );
  await rejectsWithCode(
    fixture.manager.endSession(
      sessionId,
      owner({ peerEndpointId: 3 }),
      "user-hangup",
    ),
    "unknown-session",
  );
  await fixture.manager.endSession(sessionId, owner(), "user-hangup");
  assert.equal(fixture.manager.size, 0);
  assert.deepEqual(fixture.mediaSessions[0]?.closeReasons, ["user-hangup"]);
  await rejectsWithCode(
    fixture.manager.endSession(sessionId, owner(), "user-hangup"),
    "unknown-session",
  );

  assert.deepEqual(await fixture.manager.provideOffer(offer()), { sessionId: 1 });
});

test("setup failures clean reservations and close partially-created media", async () => {
  const fixture = managerFixture({ openError: new Error("encoder unavailable") });

  assert.deepEqual(await fixture.manager.provideOffer(offer()), { sessionId: 0 });
  await tick();
  assert.equal(fixture.manager.size, 0);
  assert.equal(fixture.failures[0]?.code, "backend-failure");

  const answers: CameraSessionDescription[] = [];
  const failures: CameraSessionError[] = [];
  const media = new FakeMediaSession();
  const callbackFailure = new CameraSessionManager(
    {
      openSession: async () => ({
        answerSdp: "v=0\r\no=provider",
        session: media,
      }),
    },
    {
      provideAnswer: async (answer) => {
        answers.push(answer);
        throw new Error("Matter command failed");
      },
      provideIceCandidates: async () => undefined,
      sessionFailed: async (_sessionId, error) => {
        failures.push(error);
      },
    },
    { maxConcurrentSessions: 1 },
  );

  assert.deepEqual(await callbackFailure.provideOffer(offer()), { sessionId: 0 });
  await tick();
  assert.equal(callbackFailure.size, 0);
  assert.deepEqual(media.closeReasons, ["backend-failure"]);
  assert.equal(answers.length, 1);
  assert.equal(failures[0]?.code, "backend-failure");
});

test("manager close releases all active sessions and rejects later commands", async () => {
  const fixture = managerFixture();
  await fixture.manager.provideOffer(offer());
  await fixture.manager.provideOffer(offer());
  await tick();

  await fixture.manager.close();
  await fixture.manager.close();

  assert.equal(fixture.manager.size, 0);
  assert.deepEqual(
    fixture.mediaSessions.map((session) => session.closeReasons),
    [["shutdown"], ["shutdown"]],
  );
  await rejectsWithCode(fixture.manager.provideOffer(offer()), "manager-closed");
  await rejectsWithCode(
    fixture.manager.provideIceCandidates({
      ...owner(),
      sessionId: 1,
      candidates: [candidate],
    }),
    "manager-closed",
  );
});

test("closing while answer delivery is in flight cannot resurrect the session", async () => {
  const media = new FakeMediaSession();
  let releaseAnswer: (() => void) | undefined;
  const answerBlocked = new Promise<void>((resolve) => {
    releaseAnswer = resolve;
  });
  const manager = new CameraSessionManager(
    {
      openSession: async () => ({
        answerSdp: "v=0\r\no=provider",
        session: media,
      }),
    },
    {
      provideAnswer: async () => answerBlocked,
      provideIceCandidates: async () => undefined,
    },
    { maxConcurrentSessions: 1 },
  );

  const opening = manager.provideOffer(offer());
  await tick();
  await manager.close();
  releaseAnswer?.();

  assert.deepEqual(await opening, { sessionId: 0 });
  await tick();
  assert.equal(manager.size, 0);
  assert.deepEqual(media.closeReasons, ["shutdown"]);
});

test("failed local ICE delivery removes and closes only the owning active session", async () => {
  const mediaSessions: FakeMediaSession[] = [];
  let emitLocalCandidate:
    | ((candidates: readonly IceCandidate[]) => void)
    | undefined;
  const manager = new CameraSessionManager(
    {
      openSession: async (_offer, events) => {
        emitLocalCandidate = events.onLocalIceCandidates;
        const media = new FakeMediaSession();
        mediaSessions.push(media);
        return {
          answerSdp: "v=0\r\no=provider",
          session: media,
        };
      },
    },
    {
      provideAnswer: async () => undefined,
      provideIceCandidates: async () => {
        throw new Error("requestor disconnected");
      },
    },
    { maxConcurrentSessions: 1 },
  );
  const { sessionId } = await manager.provideOffer(offer());
  await tick();

  emitLocalCandidate?.([candidate]);
  await tick();

  assert.equal(manager.size, 0);
  assert.deepEqual(mediaSessions[0]?.closeReasons, ["backend-failure"]);
  await rejectsWithCode(
    manager.provideIceCandidates({
      ...owner(),
      sessionId,
      candidates: [candidate],
    }),
    "unknown-session",
  );
  assert.deepEqual(await manager.provideOffer(offer()), { sessionId: 1 });
});

test("EndSession owns cleanup when local ICE delivery fails concurrently", async () => {
  const media = new FakeMediaSession();
  let emitLocalCandidate:
    | ((candidates: readonly IceCandidate[]) => void)
    | undefined;
  let rejectCandidate: ((error: Error) => void) | undefined;
  const candidateDelivery = new Promise<void>((_resolve, reject) => {
    rejectCandidate = reject;
  });
  const manager = new CameraSessionManager(
    {
      openSession: async (_offer, events) => {
        emitLocalCandidate = events.onLocalIceCandidates;
        return {
          answerSdp: "v=0\r\no=provider",
          session: media,
        };
      },
    },
    {
      provideAnswer: async () => undefined,
      provideIceCandidates: async () => candidateDelivery,
    },
    { maxConcurrentSessions: 1 },
  );
  const { sessionId } = await manager.provideOffer(offer());
  await tick();
  emitLocalCandidate?.([candidate]);
  await tick();

  const ending = manager.endSession(sessionId, owner(), "user-hangup");
  rejectCandidate?.(new Error("requestor disconnected"));
  await ending;

  assert.equal(manager.size, 0);
  assert.deepEqual(media.closeReasons, ["user-hangup"]);
});

test("manager close owns cleanup when local ICE delivery fails concurrently", async () => {
  const media = new FakeMediaSession();
  let emitLocalCandidate:
    | ((candidates: readonly IceCandidate[]) => void)
    | undefined;
  let rejectCandidate: ((error: Error) => void) | undefined;
  const candidateDelivery = new Promise<void>((_resolve, reject) => {
    rejectCandidate = reject;
  });
  const manager = new CameraSessionManager(
    {
      openSession: async (_offer, events) => {
        emitLocalCandidate = events.onLocalIceCandidates;
        return {
          answerSdp: "v=0\r\no=provider",
          session: media,
        };
      },
    },
    {
      provideAnswer: async () => undefined,
      provideIceCandidates: async () => candidateDelivery,
    },
    { maxConcurrentSessions: 1 },
  );
  await manager.provideOffer(offer());
  await tick();
  emitLocalCandidate?.([candidate]);
  await tick();

  const closing = manager.close();
  rejectCandidate?.(new Error("requestor disconnected"));
  await closing;

  assert.equal(manager.size, 0);
  assert.deepEqual(media.closeReasons, ["shutdown"]);
});

test("backend close failure still removes a session and frees capacity", async () => {
  const fixture = managerFixture({ maxConcurrentSessions: 1 });
  const { sessionId } = await fixture.manager.provideOffer(offer());
  await tick();
  const media = fixture.mediaSessions[0];
  assert.ok(media);
  media.closeError = new Error("close failed");

  await rejectsWithCode(
    fixture.manager.endSession(sessionId, owner(), "media-timeout"),
    "backend-failure",
  );
  assert.equal(fixture.manager.size, 0);
  assert.deepEqual(await fixture.manager.provideOffer(offer()), { sessionId: 1 });
});

async function rejectsWithCode(
  promise: Promise<unknown>,
  code: CameraSessionError["code"],
): Promise<void> {
  await assert.rejects(
    promise,
    (error: unknown) => error instanceof CameraSessionError && error.code === code,
  );
}

async function tick(): Promise<void> {
  await new Promise<void>((resolve) => setImmediate(resolve));
}
