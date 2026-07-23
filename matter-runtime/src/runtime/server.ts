import { randomUUID } from "node:crypto";
import { mkdir, lstat, unlink } from "node:fs/promises";
import { createServer, type Server, type Socket } from "node:net";
import { dirname } from "node:path";
import {
  ContractValidationError,
  expectAttributeUpdates,
  expectAttributeWriteResult,
  expectBridgeConfiguration,
  expectCommandResult,
  expectDeviceSnapshots,
  expectHandshakeParams,
  expectObject,
  expectOpenCommissioningWindowParams,
  expectReachabilityUpdates,
  expectRemoveFabricParams,
  expectReplayState,
  expectStorageKey,
  expectStorageValueBase64,
  IPC_PROTOCOL_VERSION,
  RpcMethod,
  type AttributeWriteEvent,
  type BridgeConfiguration,
  type CommandEvent,
  type CommissioningChangedEvent,
  type FabricChangedEvent,
  type HandshakeResult,
  type RuntimeDiagnosticsEvent,
  type RuntimeStatus,
  type StorageGetResult,
  type StorageListResult,
} from "../contract.js";
import {
  JsonRpcErrorCode,
  JsonRpcPeer,
  RpcDisconnectedError,
  RpcFault,
} from "../ipc/json-rpc.js";
import {
  AdapterOperationError,
  type AdapterContext,
  type HostStorage,
  type MatterProtocolAdapter,
} from "./adapter.js";

interface RuntimeServerOptions {
  socketPath: string;
  targetId: string;
  identityNamespace?: string;
  requestTimeoutMs?: number;
  maxInboundQueue?: number;
  maxOutboundQueue?: number;
  maxFrameBytes?: number;
  onFactoryResetComplete?: () => void;
}

interface Session {
  readonly peer: JsonRpcPeer;
  authenticated: boolean;
  replayed: boolean;
}

interface SocketIdentity {
  dev: number;
  ino: number;
}

const CAPABILITIES = [
  RpcMethod.replayState,
  RpcMethod.configureBridge,
  RpcMethod.replaceDevices,
  RpcMethod.updateAttributes,
  RpcMethod.updateReachability,
  RpcMethod.openCommissioningWindow,
  RpcMethod.closeCommissioningWindow,
  RpcMethod.removeFabric,
  RpcMethod.factoryReset,
  RpcMethod.hostAttributeWrite,
  RpcMethod.hostCommand,
  RpcMethod.commissioningChanged,
  RpcMethod.fabricChanged,
  RpcMethod.runtimeDiagnostics,
  RpcMethod.storageGet,
  RpcMethod.storagePut,
  RpcMethod.storageDelete,
  RpcMethod.storageList,
  RpcMethod.storageClear,
] as const;

/**
 * One RuntimeServer represents exactly one Target and one Unix socket.
 * It never accepts a namespace in storage RPC calls; the authenticated session
 * already fixes the target and the Go host owns namespace selection.
 */
export class RuntimeServer {
  readonly socketPath: string;
  readonly targetId: string;
  readonly identityNamespace: string;
  readonly runtimeInstanceId = randomUUID();
  readonly #adapter: MatterProtocolAdapter;
  readonly #options: RuntimeServerOptions;
  readonly #sessions = new Set<Session>();
  #server: Server | undefined;
  #activeSession: Session | undefined;
  #socketIdentity: SocketIdentity | undefined;
  #lastReplayRevision: number | null = null;

  constructor(adapter: MatterProtocolAdapter, options: RuntimeServerOptions) {
    if (options.socketPath.trim() === "") {
      throw new TypeError("socketPath must be non-empty");
    }
    if (options.targetId.trim() === "") {
      throw new TypeError("targetId must be non-empty");
    }
    this.#adapter = adapter;
    this.#options = options;
    this.socketPath = options.socketPath;
    this.targetId = options.targetId;
    this.identityNamespace = options.identityNamespace ?? options.targetId;
    if (this.identityNamespace.trim() === "") {
      throw new TypeError("identityNamespace must be non-empty");
    }
  }

  get started(): boolean {
    return this.#server !== undefined;
  }

  async start(): Promise<void> {
    if (this.#server !== undefined) {
      throw new Error("runtime server is already started");
    }
    await mkdir(dirname(this.socketPath), { recursive: true });
    await this.#adapter.start(this.#adapterContext());

    const server = createServer((socket) => this.#accept(socket));
    this.#server = server;
    try {
      await new Promise<void>((resolve, reject) => {
        const onError = (error: Error): void => {
          server.off("listening", onListening);
          reject(error);
        };
        const onListening = (): void => {
          server.off("error", onError);
          resolve();
        };
        server.once("error", onError);
        server.once("listening", onListening);
        server.listen(this.socketPath);
      });
      const stat = await lstat(this.socketPath);
      this.#socketIdentity = { dev: stat.dev, ino: stat.ino };
    } catch (error) {
      this.#server = undefined;
      server.close();
      await this.#adapter.stop();
      throw error;
    }
  }

  async stop(): Promise<void> {
    const server = this.#server;
    if (server === undefined) {
      return;
    }
    this.#server = undefined;
    for (const session of this.#sessions) {
      session.peer.close(new RpcDisconnectedError("Matter runtime is stopping"));
    }
    this.#sessions.clear();
    this.#activeSession = undefined;
    await new Promise<void>((resolve) => server.close(() => resolve()));
    await this.#adapter.stop();
    await this.#unlinkOwnedSocket();
  }

  dropConnectionsForTest(): void {
    for (const session of this.#sessions) {
      session.peer.close(new RpcDisconnectedError("test connection drop"));
    }
  }

  status(): RuntimeStatus {
    const diagnostics = this.#adapter.diagnostics();
    const queue = this.#activeSession?.peer.queueStats;
    const status: RuntimeStatus = {
      targetId: this.targetId,
      identityNamespace: this.identityNamespace,
      configured: diagnostics.configured,
      deviceCount: diagnostics.deviceCount,
      fabricCount: diagnostics.fabricCount,
      fabrics: diagnostics.fabrics,
      commissioningWindowOpen: diagnostics.commissioningWindowOpen,
      commissioningWindowExpiresAt: diagnostics.commissioningWindowExpiresAt,
      lastReplayRevision: this.#lastReplayRevision,
      identityGeneration: diagnostics.identityGeneration,
      queue: {
        inboundDepth: queue?.inboundDepth ?? 0,
        inboundRejected: queue?.inboundRejected ?? 0,
        outboundDepth: queue?.outboundDepth ?? 0,
        outboundRejected: queue?.outboundRejected ?? 0,
      },
    };
    if (diagnostics.manualPairingCode !== undefined) {
      status.manualPairingCode = diagnostics.manualPairingCode;
    }
    if (diagnostics.qrPairingCode !== undefined) {
      status.qrPairingCode = diagnostics.qrPairingCode;
    }
    return status;
  }

  #accept(socket: Socket): void {
    socket.setNoDelay(true);
    const peerOptions = {
      requestTimeoutMs: this.#options.requestTimeoutMs,
      maxInboundQueue: this.#options.maxInboundQueue,
      maxOutboundQueue: this.#options.maxOutboundQueue,
      maxFrameBytes: this.#options.maxFrameBytes,
      idPrefix: `runtime-${this.runtimeInstanceId}`,
    };
    const peer = new JsonRpcPeer(
      socket,
      Object.fromEntries(Object.entries(peerOptions).filter(([, value]) => value !== undefined)),
    );
    const session: Session = { peer, authenticated: false, replayed: false };
    this.#sessions.add(session);
    peer.onClose(() => {
      this.#sessions.delete(session);
      if (this.#activeSession === session) {
        this.#activeSession = undefined;
      }
    });

    peer.register(RpcMethod.handshake, (params) => this.#handshake(session, params));
    peer.register(RpcMethod.ping, () => this.#authenticated(session, () => ({
      targetId: this.targetId,
      timestamp: new Date().toISOString(),
    })));
    peer.register(RpcMethod.replayState, (params) => this.#authenticated(session, async () => {
      const state = expectReplayState(params);
      this.#assertConfigurationIdentity(state.bridge);
      await this.#adapter.replayState(state);
      this.#lastReplayRevision = state.revision;
      session.replayed = true;
      return { appliedRevision: state.revision };
    }));
    peer.register(RpcMethod.configureBridge, (params) => this.#ready(session, async () => {
      const configuration = expectBridgeConfiguration(params);
      this.#assertConfigurationIdentity(configuration);
      await this.#adapter.configureBridge(configuration);
      return { applied: true };
    }));
    peer.register(RpcMethod.replaceDevices, (params) => this.#ready(session, async () => {
      const object = expectObject(params, "devices.replace params");
      const devices = expectDeviceSnapshots(object.devices);
      await this.#adapter.replaceDevices(devices);
      return { applied: devices.length };
    }));
    peer.register(RpcMethod.updateAttributes, (params) => this.#ready(session, async () => {
      const object = expectObject(params, "attribute.update params");
      const updates = expectAttributeUpdates(object.updates);
      await this.#adapter.updateAttributes(updates);
      return { applied: updates.length };
    }));
    peer.register(RpcMethod.updateReachability, (params) => this.#ready(session, async () => {
      const object = expectObject(params, "availability.update params");
      const updates = expectReachabilityUpdates(object.updates);
      await this.#adapter.updateReachability(updates);
      return { applied: updates.length };
    }));
    peer.register(RpcMethod.openCommissioningWindow, (params) => this.#ready(session, async () => {
      const request = expectOpenCommissioningWindowParams(params);
      return this.#adapter.openCommissioningWindow(request.durationSeconds);
    }));
    peer.register(RpcMethod.closeCommissioningWindow, (params) => this.#ready(session, async () => {
      expectObject(params, "commissioning.close params");
      return this.#adapter.closeCommissioningWindow();
    }));
    peer.register(RpcMethod.removeFabric, (params) => this.#ready(session, async () => {
      const request = expectRemoveFabricParams(params);
      await this.#adapter.removeFabric(request.fabricId);
      return { removed: true };
    }));
    peer.register(RpcMethod.factoryReset, (params) => this.#ready(session, async () => {
      expectObject(params, "identity.factoryReset params");
      await this.#adapter.factoryReset();
      if (this.#options.onFactoryResetComplete !== undefined) {
        const restart = setTimeout(this.#options.onFactoryResetComplete, 100);
        restart.unref();
      }
      return { reset: true };
    }));
    peer.register(RpcMethod.getStatus, () => this.#authenticated(session, () => this.status()));
  }

  #handshake(session: Session, params: unknown): HandshakeResult {
    const request = expectHandshakeParams(params);
    if (request.protocolVersion !== IPC_PROTOCOL_VERSION) {
      throw new RpcFault(
        JsonRpcErrorCode.protocolVersionMismatch,
        `unsupported protocol version ${request.protocolVersion}`,
        { supported: [IPC_PROTOCOL_VERSION] },
      );
    }
    if (request.targetId !== this.targetId) {
      throw new RpcFault(JsonRpcErrorCode.targetMismatch, "targetId does not match this runtime");
    }
    const previous = this.#activeSession;
    session.authenticated = true;
    session.replayed = false;
    this.#activeSession = session;
    if (previous !== undefined && previous !== session) {
      previous.peer.close(new RpcDisconnectedError("superseded by a new authenticated host session"));
    }
    return {
      protocolVersion: IPC_PROTOCOL_VERSION,
      targetId: this.targetId,
      identityNamespace: this.identityNamespace,
      runtimeInstanceId: this.runtimeInstanceId,
      replayRequired: true,
      capabilities: [...CAPABILITIES],
    };
  }

  async #authenticated<T>(session: Session, operation: () => T | Promise<T>): Promise<T> {
    if (!session.authenticated || this.#activeSession !== session) {
      throw new RpcFault(JsonRpcErrorCode.handshakeRequired, "runtime.handshake is required");
    }
    try {
      return await operation();
    } catch (error) {
      throw this.#translateError(error);
    }
  }

  #ready<T>(session: Session, operation: () => T | Promise<T>): Promise<T> {
    return this.#authenticated(session, async () => {
      if (!session.replayed) {
        throw new RpcFault(JsonRpcErrorCode.replayRequired, "state.replay is required after every handshake");
      }
      return operation();
    });
  }

  #translateError(error: unknown): Error {
    if (error instanceof RpcFault) {
      return error;
    }
    if (error instanceof ContractValidationError) {
      return new RpcFault(JsonRpcErrorCode.invalidParams, error.message);
    }
    if (error instanceof AdapterOperationError) {
      return new RpcFault(
        JsonRpcErrorCode.adapterOperationFailed,
        error.message,
        { operationCode: error.operationCode, details: error.details ?? null },
      );
    }
    return error instanceof Error ? error : new Error(String(error));
  }

  #assertConfigurationIdentity(configuration: BridgeConfiguration | null): void {
    if (configuration === null) {
      return;
    }
    if (configuration.targetId !== this.targetId) {
      throw new RpcFault(JsonRpcErrorCode.targetMismatch, "configuration targetId does not match this runtime");
    }
    if (configuration.identityNamespace !== this.identityNamespace) {
      throw new RpcFault(
        JsonRpcErrorCode.identityMismatch,
        "configuration identityNamespace does not match this runtime",
      );
    }
  }

  #adapterContext(): AdapterContext {
    return {
      targetId: this.targetId,
      identityNamespace: this.identityNamespace,
      emitAttributeWrite: (event) => this.#emitAttributeWrite(event),
      emitCommand: (event) => this.#emitCommand(event),
      emitCommissioningChanged: (event) => this.#notifyCommissioningChanged(event),
      emitFabricChanged: (event) => this.#notifyFabricChanged(event),
      emitDiagnostics: (event) => this.#notifyDiagnostics(event),
      storage: this.#hostStorage(),
    };
  }

  async #emitAttributeWrite(event: AttributeWriteEvent) {
    this.#assertOutboundTarget(event.targetId);
    const result = await this.#activePeer().request(RpcMethod.hostAttributeWrite, event);
    return expectAttributeWriteResult(result);
  }

  async #emitCommand(event: CommandEvent) {
    this.#assertOutboundTarget(event.targetId);
    const result = await this.#activePeer().request(RpcMethod.hostCommand, event);
    return expectCommandResult(result);
  }

  async #notifyCommissioningChanged(event: CommissioningChangedEvent): Promise<void> {
    this.#assertOutboundTarget(event.targetId);
    await this.#activePeer().notify(RpcMethod.commissioningChanged, event);
  }

  async #notifyFabricChanged(event: FabricChangedEvent): Promise<void> {
    this.#assertOutboundTarget(event.targetId);
    await this.#activePeer().notify(RpcMethod.fabricChanged, event);
  }

  async #notifyDiagnostics(event: RuntimeDiagnosticsEvent): Promise<void> {
    this.#assertOutboundTarget(event.targetId);
    await this.#activePeer().notify(RpcMethod.runtimeDiagnostics, event);
  }

  #hostStorage(): HostStorage {
    return {
      get: async (key) => {
        const result = await this.#activePeer().request<StorageGetResult>(
          RpcMethod.storageGet,
          { key: expectStorageKey(key) },
        );
        const object = expectObject(result, "storage.get result");
        if (typeof object.found !== "boolean") {
          throw new ContractValidationError("storage.get result found must be a boolean");
        }
        if (!object.found) {
          return undefined;
        }
        return Buffer.from(expectStorageValueBase64(object.valueBase64), "base64");
      },
      put: async (key, value) => {
        await this.#activePeer().request(RpcMethod.storagePut, {
          key: expectStorageKey(key),
          valueBase64: Buffer.from(value).toString("base64"),
        });
      },
      delete: async (key) => {
        await this.#activePeer().request(RpcMethod.storageDelete, { key: expectStorageKey(key) });
      },
      list: async () => {
        const result = await this.#activePeer().request<StorageListResult>(RpcMethod.storageList, {});
        const object = expectObject(result, "storage.list result");
        if (!Array.isArray(object.entries)) {
          throw new ContractValidationError("storage.list result entries must be an array");
        }
        return object.entries.map((entry, index) => {
          const item = expectObject(entry, `storage.list entries[${index}]`);
          return {
            key: expectStorageKey(item.key),
            valueBase64: expectStorageValueBase64(item.valueBase64),
          };
        });
      },
      clear: async () => {
        await this.#activePeer().request(RpcMethod.storageClear, {});
      },
    };
  }

  #activePeer(): JsonRpcPeer {
    const session = this.#activeSession;
    if (session === undefined || !session.authenticated || session.peer.closed) {
      throw new RpcDisconnectedError("no authenticated Go host session");
    }
    return session.peer;
  }

  #assertOutboundTarget(targetId: string): void {
    if (targetId !== this.targetId) {
      throw new AdapterOperationError("target_mismatch", "adapter emitted an event for another target");
    }
  }

  async #unlinkOwnedSocket(): Promise<void> {
    const owned = this.#socketIdentity;
    this.#socketIdentity = undefined;
    if (owned === undefined) {
      return;
    }
    try {
      const current = await lstat(this.socketPath);
      if (current.dev === owned.dev && current.ino === owned.ino) {
        await unlink(this.socketPath);
      }
    } catch (error) {
      if (!isErrno(error, "ENOENT")) {
        throw error;
      }
    }
  }
}

function isErrno(error: unknown, code: string): error is NodeJS.ErrnoException {
  return error instanceof Error && "code" in error && error.code === code;
}
