import type {
  AttributeUpdate,
  AttributeWriteEvent,
  AttributeWriteResult,
  BridgeConfiguration,
  CommandEvent,
  CommandResult,
  CommissioningChangedEvent,
  CommissioningWindowResult,
  DeviceSnapshot,
  FabricSummary,
  FabricChangedEvent,
  ReachabilityUpdate,
  RuntimeDiagnosticsEvent,
  RuntimeReplayState,
  StorageEntry,
} from "../contract.js";

export interface HostStorage {
  get(key: string): Promise<Uint8Array | undefined>;
  put(key: string, value: Uint8Array): Promise<void>;
  delete(key: string): Promise<void>;
  list(): Promise<StorageEntry[]>;
  clear(): Promise<void>;
}

export interface AdapterContext {
  readonly targetId: string;
  readonly identityNamespace: string;
  emitAttributeWrite(event: AttributeWriteEvent): Promise<AttributeWriteResult>;
  emitCommand(event: CommandEvent): Promise<CommandResult>;
  emitCommissioningChanged(event: CommissioningChangedEvent): Promise<void>;
  emitFabricChanged(event: FabricChangedEvent): Promise<void>;
  emitDiagnostics(event: RuntimeDiagnosticsEvent): Promise<void>;
  readonly storage: HostStorage;
}

export interface AdapterDiagnostics {
  configured: boolean;
  deviceCount: number;
  fabricCount: number;
  fabrics: FabricSummary[];
  commissioningWindowOpen: boolean;
  commissioningWindowExpiresAt: string | null;
  identityGeneration: number;
  manualPairingCode?: string;
  qrPairingCode?: string;
}

/**
 * The only layer allowed to know the concrete Matter SDK API.
 *
 * IPC and lifecycle code depend on this interface so a deterministic adapter can
 * exercise contracts, recovery and load behavior without multicast networking.
 */
export interface MatterProtocolAdapter {
  start(context: AdapterContext): Promise<void>;
  stop(): Promise<void>;
  replayState(state: RuntimeReplayState): Promise<void>;
  configureBridge(configuration: BridgeConfiguration): Promise<void>;
  replaceDevices(devices: DeviceSnapshot[]): Promise<void>;
  updateAttributes(updates: AttributeUpdate[]): Promise<void>;
  updateReachability(updates: ReachabilityUpdate[]): Promise<void>;
  openCommissioningWindow(durationSeconds: number): Promise<CommissioningWindowResult>;
  closeCommissioningWindow(): Promise<CommissioningWindowResult>;
  removeFabric(fabricId: string): Promise<void>;
  factoryReset(): Promise<void>;
  diagnostics(): AdapterDiagnostics;
}

export class AdapterOperationError extends Error {
  constructor(
    readonly operationCode: string,
    message: string,
    readonly details?: Record<string, unknown>,
  ) {
    super(message);
    this.name = "AdapterOperationError";
  }
}
