import {
  ContractValidationError,
  expectAttributeUpdates,
  expectAttributeWriteEvent,
  expectBridgeConfiguration,
  expectCommandEvent,
  expectCommissioningChangedEvent,
  expectDeviceSnapshots,
  expectFabricChangedEvent,
  expectNonEmptyString,
  expectObject,
  expectReachabilityUpdates,
  expectReplayState,
  expectRuntimeDiagnostics,
  expectStorageKey,
  expectStorageValueBase64,
  IPC_PROTOCOL_VERSION,
  RpcMethod,
  type AttributeUpdate,
  type AttributeWriteEvent,
  type AttributeWriteResult,
  type BridgeConfiguration,
  type CommandEvent,
  type CommandResult,
  type CommissioningChangedEvent,
  type CommissioningWindowResult,
  type DeviceSnapshot,
  type FabricChangedEvent,
  type ReachabilityUpdate,
  type RuntimeDiagnosticsEvent,
  type RuntimeReplayState,
  type RuntimeStatus,
  type StorageEntry,
} from "../contract.js";
import type { HostStorage } from "../runtime/adapter.js";
import {
  connectJsonRpcPeer,
  JsonRpcPeer,
  RpcDisconnectedError,
} from "./json-rpc.js";

export interface RuntimeHostHandlers {
  storage?: HostStorage;
  onAttributeWrite?(event: AttributeWriteEvent): AttributeWriteResult | Promise<AttributeWriteResult>;
  onCommand?(event: CommandEvent): CommandResult | Promise<CommandResult>;
  onCommissioningChanged?(event: CommissioningChangedEvent): void | Promise<void>;
  onFabricChanged?(event: FabricChangedEvent): void | Promise<void>;
  onDiagnostics?(event: RuntimeDiagnosticsEvent): void | Promise<void>;
}

export interface ReconnectingRuntimeClientOptions {
  socketPath: string;
  targetId: string;
  identityNamespace?: string;
  clientName?: string;
  initialState?: RuntimeReplayState;
  requestTimeoutMs?: number;
  maxInboundQueue?: number;
  maxOutboundQueue?: number;
  reconnectMinimumMs?: number;
  reconnectMaximumMs?: number;
  host?: RuntimeHostHandlers;
}

interface ReadyWaiter {
  resolve(): void;
  reject(error: Error): void;
  timer: NodeJS.Timeout;
}

/**
 * Reference host implementation for contract tests and Go-client parity.
 * Desired bridge/device state is retained across transport loss and replayed in
 * full after every successful version handshake.
 */
export class ReconnectingRuntimeClient {
  readonly socketPath: string;
  readonly targetId: string;
  readonly identityNamespace: string;
  readonly #options: ReconnectingRuntimeClientOptions;
  readonly #host: RuntimeHostHandlers;
  readonly #storage: HostStorage;
  readonly #readyWaiters = new Set<ReadyWaiter>();
  readonly #disconnectWaiters = new Set<() => void>();
  #desiredState: RuntimeReplayState;
  #peer: JsonRpcPeer | undefined;
  #running = false;
  #ready = false;
  #connecting = false;
  #reconnectTimer: NodeJS.Timeout | undefined;
  #reconnectDelayMs: number;
  #runtimeInstanceId: string | undefined;

  constructor(options: ReconnectingRuntimeClientOptions) {
    if (options.socketPath.trim() === "" || options.targetId.trim() === "") {
      throw new TypeError("socketPath and targetId must be non-empty");
    }
    this.#options = options;
    this.socketPath = options.socketPath;
    this.targetId = options.targetId;
    this.identityNamespace = options.identityNamespace ?? options.targetId;
    this.#host = options.host ?? {};
    this.#storage = this.#host.storage ?? new MemoryHostStorage();
    this.#reconnectDelayMs = options.reconnectMinimumMs ?? 25;
    this.#desiredState = expectReplayState(
      options.initialState ?? { revision: 0, bridge: null, devices: [] },
    );
    this.#assertDesiredIdentity(this.#desiredState.bridge);
  }

  get running(): boolean {
    return this.#running;
  }

  get ready(): boolean {
    return this.#ready;
  }

  get runtimeInstanceId(): string | undefined {
    return this.#runtimeInstanceId;
  }

  get desiredState(): RuntimeReplayState {
    return structuredClone(this.#desiredState);
  }

  start(): void {
    if (this.#running) {
      return;
    }
    this.#running = true;
    void this.#connectOnce();
  }

  async stop(): Promise<void> {
    if (!this.#running) {
      return;
    }
    this.#running = false;
    this.#ready = false;
    if (this.#reconnectTimer !== undefined) {
      clearTimeout(this.#reconnectTimer);
      this.#reconnectTimer = undefined;
    }
    this.#peer?.close(new RpcDisconnectedError("runtime client stopped"));
    this.#peer = undefined;
    const stopped = new RpcDisconnectedError("runtime client stopped");
    for (const waiter of this.#readyWaiters) {
      clearTimeout(waiter.timer);
      waiter.reject(stopped);
    }
    this.#readyWaiters.clear();
  }

  waitForReady(timeoutMs = 5_000): Promise<void> {
    if (this.#ready) {
      return Promise.resolve();
    }
    if (!this.#running) {
      return Promise.reject(new RpcDisconnectedError("runtime client is not started"));
    }
    return new Promise<void>((resolve, reject) => {
      const waiter: ReadyWaiter = {
        resolve,
        reject,
        timer: setTimeout(() => {
          this.#readyWaiters.delete(waiter);
          reject(new Error(`runtime client did not become ready within ${timeoutMs}ms`));
        }, timeoutMs),
      };
      this.#readyWaiters.add(waiter);
    });
  }

  waitForDisconnected(timeoutMs = 5_000): Promise<void> {
    if (!this.#ready && this.#peer === undefined) {
      return Promise.resolve();
    }
    return new Promise<void>((resolve, reject) => {
      const onDisconnect = (): void => {
        clearTimeout(timer);
        this.#disconnectWaiters.delete(onDisconnect);
        resolve();
      };
      const timer = setTimeout(() => {
        this.#disconnectWaiters.delete(onDisconnect);
        reject(new Error(`runtime client stayed connected for ${timeoutMs}ms`));
      }, timeoutMs);
      this.#disconnectWaiters.add(onDisconnect);
    });
  }

  setDesiredState(state: RuntimeReplayState): void {
    const validated = expectReplayState(state);
    this.#assertDesiredIdentity(validated.bridge);
    this.#desiredState = structuredClone(validated);
  }

  async configureBridge(configuration: BridgeConfiguration): Promise<void> {
    const validated = expectBridgeConfiguration(configuration);
    this.#assertDesiredIdentity(validated);
    this.#desiredState.bridge = structuredClone(validated);
    this.#bumpRevision();
    await this.#requestWhenReady(RpcMethod.configureBridge, validated);
  }

  async replaceDevices(devices: DeviceSnapshot[]): Promise<void> {
    const validated = expectDeviceSnapshots(
      devices,
      this.#desiredState.bridge?.nodeKind ?? "bridge",
    );
    this.#desiredState.devices = structuredClone(validated);
    this.#bumpRevision();
    await this.#requestWhenReady(RpcMethod.replaceDevices, { devices: validated });
  }

  async updateAttributes(updates: AttributeUpdate[]): Promise<void> {
    const validated = expectAttributeUpdates(updates);
    for (const update of validated) {
      const device = this.#desiredState.devices.find((candidate) => candidate.id === update.deviceId);
      if (device === undefined) {
        throw new ContractValidationError(`unknown desired device ${update.deviceId}`);
      }
      device.attributes[update.path] = structuredClone(update.value);
    }
    this.#bumpRevision();
    await this.#requestWhenReady(RpcMethod.updateAttributes, { updates: validated });
  }

  async updateReachability(updates: ReachabilityUpdate[]): Promise<void> {
    const validated = expectReachabilityUpdates(updates);
    for (const update of validated) {
      const device = this.#desiredState.devices.find((candidate) => candidate.id === update.deviceId);
      if (device === undefined) {
        throw new ContractValidationError(`unknown desired device ${update.deviceId}`);
      }
      device.reachable = update.reachable;
    }
    this.#bumpRevision();
    await this.#requestWhenReady(RpcMethod.updateReachability, { updates: validated });
  }

  requestCommissioningWindow(durationSeconds: number): Promise<CommissioningWindowResult> {
    return this.#requestWhenReady(RpcMethod.openCommissioningWindow, { durationSeconds });
  }

  closeCommissioningWindow(): Promise<CommissioningWindowResult> {
    return this.#requestWhenReady(RpcMethod.closeCommissioningWindow, {});
  }

  async removeFabric(fabricId: string): Promise<void> {
    await this.#requestWhenReady(RpcMethod.removeFabric, { fabricId });
  }

  async factoryReset(): Promise<void> {
    await this.#requestWhenReady(RpcMethod.factoryReset, {});
  }

  getStatus(): Promise<RuntimeStatus> {
    return this.#requestWhenReady(RpcMethod.getStatus, {});
  }

  async #connectOnce(): Promise<void> {
    if (!this.#running || this.#connecting || this.#ready) {
      return;
    }
    this.#connecting = true;
    let peer: JsonRpcPeer | undefined;
    try {
      const connectedPeer = await connectJsonRpcPeer(
        this.socketPath,
        {
          requestTimeoutMs: this.#options.requestTimeoutMs ?? 5_000,
          maxInboundQueue: this.#options.maxInboundQueue ?? 128,
          maxOutboundQueue: this.#options.maxOutboundQueue ?? 128,
          idPrefix: `host-${this.targetId}`,
        },
      );
      peer = connectedPeer;
      if (!this.#running) {
        connectedPeer.close();
        return;
      }
      this.#peer = connectedPeer;
      this.#registerHostHandlers(connectedPeer);
      connectedPeer.onClose(() => this.#onPeerClosed(connectedPeer));

      const handshake = expectObject(
        await connectedPeer.request(RpcMethod.handshake, {
          protocolVersion: IPC_PROTOCOL_VERSION,
          targetId: this.targetId,
          clientName: this.#options.clientName ?? "homeloom-contract-client",
        }),
        "handshake result",
      );
      if (handshake.protocolVersion !== IPC_PROTOCOL_VERSION ||
          handshake.targetId !== this.targetId ||
          handshake.identityNamespace !== this.identityNamespace ||
          handshake.replayRequired !== true) {
        throw new Error("runtime handshake identity or protocol mismatch");
      }
      this.#runtimeInstanceId = expectNonEmptyString(handshake.runtimeInstanceId, "runtimeInstanceId");

      while (this.#running && this.#peer === connectedPeer) {
        const snapshot = structuredClone(this.#desiredState);
        await connectedPeer.request(RpcMethod.replayState, snapshot);
        if (snapshot.revision === this.#desiredState.revision) {
          break;
        }
      }
      if (!this.#running || this.#peer !== connectedPeer) {
        return;
      }
      this.#ready = true;
      this.#reconnectDelayMs = this.#options.reconnectMinimumMs ?? 25;
      for (const waiter of this.#readyWaiters) {
        clearTimeout(waiter.timer);
        waiter.resolve();
      }
      this.#readyWaiters.clear();
    } catch (error) {
      peer?.close(error instanceof Error ? error : new Error(String(error)));
    } finally {
      this.#connecting = false;
      if (this.#running && !this.#ready && this.#reconnectTimer === undefined) {
        this.#scheduleReconnect();
      }
    }
  }

  #registerHostHandlers(peer: JsonRpcPeer): void {
    peer.register(RpcMethod.hostAttributeWrite, async (params) => {
      const event = expectAttributeWriteEvent(params);
      this.#assertEventTarget(event.targetId);
      return this.#host.onAttributeWrite?.(event) ?? {
        accepted: false,
        message: "no host attribute write handler",
      };
    });
    peer.register(RpcMethod.hostCommand, async (params) => {
      const event = expectCommandEvent(params);
      this.#assertEventTarget(event.targetId);
      return this.#host.onCommand?.(event) ?? {
        accepted: false,
        message: "no host command handler",
      };
    });
    peer.register(RpcMethod.commissioningChanged, async (params) => {
      const event = expectCommissioningChangedEvent(params);
      this.#assertEventTarget(event.targetId);
      await this.#host.onCommissioningChanged?.(event);
    });
    peer.register(RpcMethod.fabricChanged, async (params) => {
      const event = expectFabricChangedEvent(params);
      this.#assertEventTarget(event.targetId);
      await this.#host.onFabricChanged?.(event);
    });
    peer.register(RpcMethod.runtimeDiagnostics, async (params) => {
      const event = expectRuntimeDiagnostics(params);
      this.#assertEventTarget(event.targetId);
      await this.#host.onDiagnostics?.(event);
    });
    peer.register(RpcMethod.storageGet, async (params) => {
      const object = expectObject(params, "storage.get params");
      const value = await this.#storage.get(expectStorageKey(object.key));
      return value === undefined
        ? { found: false }
        : { found: true, valueBase64: Buffer.from(value).toString("base64") };
    });
    peer.register(RpcMethod.storagePut, async (params) => {
      const object = expectObject(params, "storage.put params");
      await this.#storage.put(
        expectStorageKey(object.key),
        Buffer.from(expectStorageValueBase64(object.valueBase64), "base64"),
      );
      return { stored: true };
    });
    peer.register(RpcMethod.storageDelete, async (params) => {
      const object = expectObject(params, "storage.delete params");
      await this.#storage.delete(expectStorageKey(object.key));
      return { deleted: true };
    });
    peer.register(RpcMethod.storageList, async (params) => {
      expectObject(params, "storage.list params");
      return { entries: await this.#storage.list() };
    });
    peer.register(RpcMethod.storageClear, async (params) => {
      expectObject(params, "storage.clear params");
      await this.#storage.clear();
      return { cleared: true };
    });
  }

  async #requestWhenReady<T>(method: string, params: unknown): Promise<T> {
    await this.waitForReady(this.#options.requestTimeoutMs ?? 5_000);
    const peer = this.#peer;
    if (!this.#ready || peer === undefined) {
      throw new RpcDisconnectedError();
    }
    return peer.request<T>(method, params);
  }

  #onPeerClosed(peer: JsonRpcPeer): void {
    if (this.#peer !== peer) {
      return;
    }
    this.#peer = undefined;
    this.#ready = false;
    for (const resolve of this.#disconnectWaiters) {
      resolve();
    }
    this.#disconnectWaiters.clear();
    if (!this.#connecting) {
      this.#scheduleReconnect();
    }
  }

  #scheduleReconnect(): void {
    if (!this.#running || this.#reconnectTimer !== undefined) {
      return;
    }
    const delay = this.#reconnectDelayMs;
    const maximum = this.#options.reconnectMaximumMs ?? 1_000;
    this.#reconnectDelayMs = Math.min(maximum, Math.max(delay + 1, delay * 2));
    this.#reconnectTimer = setTimeout(() => {
      this.#reconnectTimer = undefined;
      void this.#connectOnce();
    }, delay);
    this.#reconnectTimer.unref();
  }

  #bumpRevision(): void {
    if (this.#desiredState.revision === Number.MAX_SAFE_INTEGER) {
      this.#desiredState.revision = 0;
      return;
    }
    this.#desiredState.revision += 1;
  }

  #assertDesiredIdentity(configuration: BridgeConfiguration | null): void {
    if (configuration === null) {
      return;
    }
    if (configuration.targetId !== this.targetId ||
        configuration.identityNamespace !== this.identityNamespace) {
      throw new ContractValidationError("desired bridge identity does not match runtime client");
    }
  }

  #assertEventTarget(targetId: string): void {
    if (targetId !== this.targetId) {
      throw new ContractValidationError("runtime event targetId does not match client");
    }
  }
}

export class MemoryHostStorage implements HostStorage {
  readonly #entries = new Map<string, Uint8Array>();

  async get(key: string): Promise<Uint8Array | undefined> {
    const value = this.#entries.get(key);
    return value === undefined ? undefined : Uint8Array.from(value);
  }

  async put(key: string, value: Uint8Array): Promise<void> {
    this.#entries.set(key, Uint8Array.from(value));
  }

  async delete(key: string): Promise<void> {
    this.#entries.delete(key);
  }

  async list(): Promise<StorageEntry[]> {
    return [...this.#entries.entries()]
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, value]) => ({ key, valueBase64: Buffer.from(value).toString("base64") }));
  }

  async clear(): Promise<void> {
    this.#entries.clear();
  }
}
