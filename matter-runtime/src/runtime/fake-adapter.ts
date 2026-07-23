import {
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
} from "../contract.js";
import {
  AdapterOperationError,
  type AdapterContext,
  type AdapterDiagnostics,
  type MatterProtocolAdapter,
} from "./adapter.js";

export interface FakeProtocolAdapterOptions {
  operationDelayMs?: number;
}

/**
 * Deterministic protocol adapter used by IPC contract and supervisor tests.
 * It intentionally models state transitions but never opens Matter UDP/mDNS.
 */
export class FakeProtocolAdapter implements MatterProtocolAdapter {
  readonly #devices = new Map<string, DeviceSnapshot>();
  readonly #fabrics = new Map<string, string | undefined>();
  readonly #operationDelayMs: number;
  #context: AdapterContext | undefined;
  #configuration: BridgeConfiguration | undefined;
  #commissioningWindowExpiresAt: string | null = null;
  #identityGeneration = 0;
  #lastReplayRevision: number | null = null;
  #replayCount = 0;

  constructor(options: FakeProtocolAdapterOptions = {}) {
    this.#operationDelayMs = options.operationDelayMs ?? 0;
  }

  get replayCount(): number {
    return this.#replayCount;
  }

  get lastReplayRevision(): number | null {
    return this.#lastReplayRevision;
  }

  get configuration(): BridgeConfiguration | undefined {
    return this.#configuration === undefined ? undefined : structuredClone(this.#configuration);
  }

  get devices(): DeviceSnapshot[] {
    return [...this.#devices.values()].map((device) => structuredClone(device));
  }

  async start(context: AdapterContext): Promise<void> {
    if (this.#context !== undefined) {
      throw new AdapterOperationError("already_started", "fake adapter is already started");
    }
    this.#context = context;
  }

  async stop(): Promise<void> {
    this.#context = undefined;
  }

  async replayState(state: RuntimeReplayState): Promise<void> {
    await this.#delay();
    const context = this.#assertContext();
    this.#lastReplayRevision = state.revision;
    this.#replayCount += 1;
    this.#configuration = state.bridge === null ? undefined : structuredClone(state.bridge);
    this.#replaceDevices(state.devices);

    const storedGeneration = await context.storage.get("runtime/identity-generation");
    if (storedGeneration === undefined) {
      await context.storage.put(
        "runtime/identity-generation",
        Buffer.from(String(this.#identityGeneration), "utf8"),
      );
    } else {
      const parsed = Number.parseInt(Buffer.from(storedGeneration).toString("utf8"), 10);
      if (Number.isSafeInteger(parsed) && parsed >= 0) {
        this.#identityGeneration = parsed;
      }
    }
  }

  async configureBridge(configuration: BridgeConfiguration): Promise<void> {
    await this.#delay();
    this.#assertContext();
    this.#configuration = structuredClone(configuration);
  }

  async replaceDevices(devices: DeviceSnapshot[]): Promise<void> {
    await this.#delay();
    this.#assertContext();
    this.#replaceDevices(devices);
  }

  async updateAttributes(updates: AttributeUpdate[]): Promise<void> {
    await this.#delay();
    this.#assertContext();
    for (const update of updates) {
      const device = this.#devices.get(update.deviceId);
      if (device === undefined) {
        throw new AdapterOperationError("device_not_found", `unknown device ${update.deviceId}`);
      }
      device.attributes[update.path] = structuredClone(update.value);
    }
  }

  async updateReachability(updates: ReachabilityUpdate[]): Promise<void> {
    await this.#delay();
    this.#assertContext();
    for (const update of updates) {
      const device = this.#devices.get(update.deviceId);
      if (device === undefined) {
        throw new AdapterOperationError("device_not_found", `unknown device ${update.deviceId}`);
      }
      device.reachable = update.reachable;
    }
  }

  async openCommissioningWindow(durationSeconds: number): Promise<CommissioningWindowResult> {
    await this.#delay();
    const context = this.#assertContext();
    const expiresAt = new Date(Date.now() + durationSeconds * 1_000).toISOString();
    this.#commissioningWindowExpiresAt = expiresAt;
    await context.emitCommissioningChanged(this.#commissioningEvent("window-opened"));
    return { open: true, expiresAt };
  }

  async closeCommissioningWindow(): Promise<CommissioningWindowResult> {
    await this.#delay();
    const context = this.#assertContext();
    this.#commissioningWindowExpiresAt = null;
    await context.emitCommissioningChanged(this.#commissioningEvent("window-closed"));
    return { open: false, expiresAt: null };
  }

  async removeFabric(fabricId: string): Promise<void> {
    await this.#delay();
    const context = this.#assertContext();
    if (!this.#fabrics.delete(fabricId)) {
      throw new AdapterOperationError("fabric_not_found", `unknown fabric ${fabricId}`);
    }
    await context.emitFabricChanged(this.#fabricEvent("removed", fabricId));
    await context.emitCommissioningChanged(this.#commissioningEvent("fabric-removed"));
  }

  async factoryReset(): Promise<void> {
    await this.#delay();
    const context = this.#assertContext();
    this.#fabrics.clear();
    this.#commissioningWindowExpiresAt = null;
    await context.storage.clear();
    this.#identityGeneration += 1;
    await context.storage.put(
      "runtime/identity-generation",
      Buffer.from(String(this.#identityGeneration), "utf8"),
    );
    await context.emitFabricChanged(this.#fabricEvent("reset"));
    await context.emitCommissioningChanged(this.#commissioningEvent("factory-reset"));
  }

  diagnostics(): AdapterDiagnostics {
    const diagnostics: AdapterDiagnostics = {
      configured: this.#configuration !== undefined,
      deviceCount: this.#devices.size,
      fabricCount: this.#fabrics.size,
      fabrics: Array.from(this.#fabrics, ([id, label]) => label === undefined ? { id } : { id, label }),
      commissioningWindowOpen: this.#commissioningWindowExpiresAt !== null,
      commissioningWindowExpiresAt: this.#commissioningWindowExpiresAt,
      identityGeneration: this.#identityGeneration,
    };
    if (this.#configuration !== undefined) {
      const discriminator = String(this.#configuration.discriminator).padStart(4, "0");
      const passcode = String(this.#configuration.commissioningPasscode).padStart(8, "0");
      diagnostics.manualPairingCode = `FAKE-${discriminator}-${passcode}`;
      diagnostics.qrPairingCode = `MT:FAKE-${this.#configuration.targetId}-${discriminator}`;
    }
    return diagnostics;
  }

  async addFabricForTest(fabricId: string, label?: string): Promise<void> {
    const context = this.#assertContext();
    this.#fabrics.set(fabricId, label);
    await context.emitFabricChanged(this.#fabricEvent("added", fabricId, label));
    await context.emitCommissioningChanged(this.#commissioningEvent("fabric-added"));
  }

  emitAttributeWriteForTest(event: Omit<AttributeWriteEvent, "targetId">): Promise<AttributeWriteResult> {
    const context = this.#assertContext();
    return context.emitAttributeWrite({ ...event, targetId: context.targetId });
  }

  emitCommandForTest(event: Omit<CommandEvent, "targetId">): Promise<CommandResult> {
    const context = this.#assertContext();
    return context.emitCommand({ ...event, targetId: context.targetId });
  }

  emitDiagnosticsForTest(
    event: Omit<RuntimeDiagnosticsEvent, "targetId" | "timestamp">,
  ): Promise<void> {
    const context = this.#assertContext();
    return context.emitDiagnostics({
      ...event,
      targetId: context.targetId,
      timestamp: new Date().toISOString(),
    });
  }

  #replaceDevices(devices: DeviceSnapshot[]): void {
    this.#devices.clear();
    for (const device of devices) {
      this.#devices.set(device.id, structuredClone(device));
    }
  }

  #commissioningEvent(reason: CommissioningChangedEvent["reason"]): CommissioningChangedEvent {
    const context = this.#assertContext();
    return {
      targetId: context.targetId,
      timestamp: new Date().toISOString(),
      state: this.#fabrics.size === 0 ? "uncommissioned" : "commissioned",
      windowOpen: this.#commissioningWindowExpiresAt !== null,
      fabricCount: this.#fabrics.size,
      reason,
    };
  }

  #fabricEvent(
    change: FabricChangedEvent["change"],
    fabricId?: string,
    label?: string,
  ): FabricChangedEvent {
    const context = this.#assertContext();
    const event: FabricChangedEvent = {
      targetId: context.targetId,
      timestamp: new Date().toISOString(),
      change,
      fabricCount: this.#fabrics.size,
    };
    if (fabricId !== undefined) {
      event.fabricId = fabricId;
    }
    if (label !== undefined) {
      event.label = label;
    }
    return event;
  }

  #assertContext(): AdapterContext {
    if (this.#context === undefined) {
      throw new AdapterOperationError("not_started", "fake adapter is not started");
    }
    return this.#context;
  }

  async #delay(): Promise<void> {
    if (this.#operationDelayMs === 0) {
      return;
    }
    await new Promise<void>((resolve) => setTimeout(resolve, this.#operationDelayMs));
  }
}
