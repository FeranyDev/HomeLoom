export const IPC_PROTOCOL_VERSION = "1.1";

export const RpcMethod = {
  handshake: "runtime.handshake",
  ping: "runtime.ping",
  replayState: "state.replay",
  configureBridge: "bridge.configure",
  replaceDevices: "devices.replace",
  updateAttributes: "attribute.update",
  updateReachability: "availability.update",
  openCommissioningWindow: "commissioning.open",
  closeCommissioningWindow: "commissioning.close",
  removeFabric: "fabric.remove",
  factoryReset: "identity.factoryReset",
  getStatus: "runtime.status",
  hostAttributeWrite: "attribute.write",
  hostCommand: "cluster.command",
  commissioningChanged: "commissioning.changed",
  fabricChanged: "fabric.changed",
  runtimeDiagnostics: "runtime.diagnostics",
  storageGet: "storage.get",
  storagePut: "storage.put",
  storageDelete: "storage.delete",
  storageList: "storage.list",
  storageClear: "storage.clear",
  cameraWebRtc: "camera.webrtc",
  cameraSnapshot: "camera.snapshot",
} as const;

export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue };
export type NodeKind = "bridge" | "camera";

export interface HandshakeParams {
  protocolVersion: string;
  targetId: string;
  clientName: string;
}

export interface HandshakeResult {
  protocolVersion: string;
  targetId: string;
  identityNamespace: string;
  runtimeInstanceId: string;
  replayRequired: true;
  capabilities: string[];
}

export interface BridgeConfiguration {
  nodeKind: NodeKind;
  targetId: string;
  identityNamespace: string;
  networkInterface?: string;
  listenPort: number;
  discriminator: number;
  commissioningPasscode: number;
  vendorId: number;
  productId: number;
  productName: string;
  serialNumber: string;
  commissioningWindowSeconds: number;
}

export interface CameraMediaBinding {
  streamId: string;
  deviceId: string;
}

export interface DeviceSnapshot {
  id: string;
  endpointId: number;
  deviceType: string;
  name: string;
  reachable: boolean;
  attributes: Record<string, JsonValue>;
}

export interface AttributeUpdate {
  deviceId: string;
  path: string;
  value: JsonValue;
  version?: number;
}

export interface ReachabilityUpdate {
  deviceId: string;
  reachable: boolean;
}

export interface RuntimeReplayState {
  revision: number;
  bridge: BridgeConfiguration | null;
  devices: DeviceSnapshot[];
  /**
   * Opaque host-side media identity only. Transport URIs and credentials must
   * never cross this IPC boundary.
   */
  media?: CameraMediaBinding;
}

export interface OpenCommissioningWindowParams {
  durationSeconds: number;
}

export interface CommissioningWindowResult {
  open: boolean;
  expiresAt: string | null;
}

export interface RemoveFabricParams {
  fabricId: string;
}

export interface AttributeWriteEvent {
  targetId: string;
  deviceId: string;
  endpointId: number;
  path: string;
  value: JsonValue;
  interactionId: string;
}

export interface AttributeWriteResult {
  accepted: boolean;
  message?: string;
}

export interface CommandEvent {
  targetId: string;
  deviceId: string;
  endpointId: number;
  path: string;
  fields: Record<string, JsonValue>;
  interactionId: string;
}

export interface CommandResult {
  accepted: boolean;
  fields?: Record<string, JsonValue>;
  message?: string;
}

export interface CommissioningChangedEvent {
  targetId: string;
  timestamp: string;
  state: "uncommissioned" | "commissioned";
  windowOpen: boolean;
  fabricCount: number;
  reason: "startup" | "window-opened" | "window-closed" | "fabric-added" | "fabric-removed" | "factory-reset";
}

export interface FabricChangedEvent {
  targetId: string;
  timestamp: string;
  change: "added" | "removed" | "reset";
  fabricId?: string;
  label?: string;
  fabricCount: number;
}

export interface RuntimeDiagnosticsEvent {
  targetId: string;
  timestamp: string;
  level: "info" | "warning" | "error";
  code: string;
  message?: string;
  metrics: Record<string, number>;
}

export interface StorageEntry {
  key: string;
  valueBase64: string;
}

export interface StorageGetResult {
  found: boolean;
  valueBase64?: string;
}

export interface StorageListResult {
  entries: StorageEntry[];
}

export interface FabricSummary {
  id: string;
  label?: string;
}

export interface RuntimeStatus {
  targetId: string;
  identityNamespace: string;
  configured: boolean;
  deviceCount: number;
  fabricCount: number;
  fabrics: FabricSummary[];
  commissioningWindowOpen: boolean;
  commissioningWindowExpiresAt: string | null;
  lastReplayRevision: number | null;
  identityGeneration: number;
  manualPairingCode?: string;
  qrPairingCode?: string;
  queue: {
    inboundDepth: number;
    inboundRejected: number;
    outboundDepth: number;
    outboundRejected: number;
  };
}

export function expectHandshakeParams(value: unknown): HandshakeParams {
  const object = expectObject(value, "handshake params");
  return {
    protocolVersion: expectNonEmptyString(object.protocolVersion, "protocolVersion"),
    targetId: expectNonEmptyString(object.targetId, "targetId"),
    clientName: expectNonEmptyString(object.clientName, "clientName"),
  };
}

export function expectBridgeConfiguration(value: unknown): BridgeConfiguration {
  const object = expectObject(value, "bridge configuration");
  const result: BridgeConfiguration = {
    nodeKind: expectNodeKind(object.nodeKind),
    targetId: expectNonEmptyString(object.targetId, "targetId"),
    identityNamespace: expectNonEmptyString(object.identityNamespace, "identityNamespace"),
    listenPort: expectIntegerInRange(object.listenPort, "listenPort", 1, 65_535),
    discriminator: expectIntegerInRange(object.discriminator, "discriminator", 0, 4_095),
    commissioningPasscode: expectIntegerInRange(
      object.commissioningPasscode,
      "commissioningPasscode",
      1,
      99_999_998,
    ),
    vendorId: expectIntegerInRange(object.vendorId, "vendorId", 0, 65_535),
    productId: expectIntegerInRange(object.productId, "productId", 0, 65_535),
    productName: expectNonEmptyString(object.productName, "productName"),
    serialNumber: expectNonEmptyString(object.serialNumber, "serialNumber"),
    commissioningWindowSeconds: expectIntegerInRange(
      object.commissioningWindowSeconds,
      "commissioningWindowSeconds",
      1,
      86_400,
    ),
  };
  if (object.networkInterface !== undefined) {
    result.networkInterface = expectNonEmptyString(object.networkInterface, "networkInterface");
  }
  return result;
}

export function expectDeviceSnapshots(
  value: unknown,
  nodeKind: NodeKind = "bridge",
): DeviceSnapshot[] {
  if (!Array.isArray(value)) {
    throw new ContractValidationError("devices must be an array");
  }
  const endpointIds = new Set<number>();
  const deviceIds = new Set<string>();
  const devices = value.map((entry, index) => {
    const object = expectObject(entry, `devices[${index}]`);
    const endpointId = expectIntegerInRange(
      object.endpointId,
      `devices[${index}].endpointId`,
      nodeKind === "camera" ? 1 : 2,
      65_534,
    );
    const id = expectNonEmptyString(object.id, `devices[${index}].id`);
    if (endpointIds.has(endpointId)) {
      throw new ContractValidationError(`duplicate endpointId ${endpointId}`);
    }
    if (deviceIds.has(id)) {
      throw new ContractValidationError(`duplicate device id ${id}`);
    }
    endpointIds.add(endpointId);
    deviceIds.add(id);
    return {
      id,
      endpointId,
      deviceType: expectNonEmptyString(object.deviceType, `devices[${index}].deviceType`),
      name: expectNonEmptyString(object.name, `devices[${index}].name`),
      reachable: expectBoolean(object.reachable, `devices[${index}].reachable`),
      attributes: expectJsonRecord(object.attributes, `devices[${index}].attributes`),
    };
  });
  if (
    nodeKind === "camera"
    && (
      devices.length !== 1
      || devices[0]?.endpointId !== 1
      || devices[0].deviceType.trim().toLowerCase() !== "camera"
    )
  ) {
    throw new ContractValidationError(
      "camera node requires exactly one camera device at endpointId 1",
    );
  }
  return devices;
}

export function expectAttributeUpdates(value: unknown): AttributeUpdate[] {
  if (!Array.isArray(value)) {
    throw new ContractValidationError("attribute updates must be an array");
  }
  return value.map((entry, index) => {
    const object = expectObject(entry, `updates[${index}]`);
    const result: AttributeUpdate = {
      deviceId: expectNonEmptyString(object.deviceId, `updates[${index}].deviceId`),
      path: expectNonEmptyString(object.path, `updates[${index}].path`),
      value: expectJsonValue(object.value, `updates[${index}].value`),
    };
    if (object.version !== undefined) {
      result.version = expectIntegerInRange(object.version, `updates[${index}].version`, 0, Number.MAX_SAFE_INTEGER);
    }
    return result;
  });
}

export function expectReachabilityUpdates(value: unknown): ReachabilityUpdate[] {
  if (!Array.isArray(value)) {
    throw new ContractValidationError("reachability updates must be an array");
  }
  return value.map((entry, index) => {
    const object = expectObject(entry, `updates[${index}]`);
    return {
      deviceId: expectNonEmptyString(object.deviceId, `updates[${index}].deviceId`),
      reachable: expectBoolean(object.reachable, `updates[${index}].reachable`),
    };
  });
}

export function expectReplayState(value: unknown): RuntimeReplayState {
  const object = expectObject(value, "replay state");
  const bridge = object.bridge === null ? null : expectBridgeConfiguration(object.bridge);
  const nodeKind = bridge?.nodeKind ?? "bridge";
  const devices = expectDeviceSnapshots(object.devices, nodeKind);
  const result: RuntimeReplayState = {
    revision: expectIntegerInRange(object.revision, "revision", 0, Number.MAX_SAFE_INTEGER),
    bridge,
    devices,
  };
  if (nodeKind === "camera" && object.media === undefined) {
    throw new ContractValidationError("camera node requires a media binding");
  }
  if (object.media !== undefined) {
    if (nodeKind !== "camera") {
      throw new ContractValidationError("media is only valid for a camera node");
    }
    const media = expectCameraMediaBinding(object.media);
    if (media.deviceId !== devices[0]?.id) {
      throw new ContractValidationError("media.deviceId must match the camera device id");
    }
    result.media = media;
  }
  return result;
}

export function expectCameraMediaBinding(value: unknown): CameraMediaBinding {
  const object = expectObject(value, "camera media binding");
  const allowed = new Set(["streamId", "deviceId"]);
  const unexpected = Object.keys(object).find((key) => !allowed.has(key));
  if (unexpected !== undefined) {
    throw new ContractValidationError(`camera media binding contains unsupported field ${unexpected}`);
  }
  return {
    streamId: expectNonEmptyString(object.streamId, "media.streamId"),
    deviceId: expectNonEmptyString(object.deviceId, "media.deviceId"),
  };
}

function expectNodeKind(value: unknown): NodeKind {
  if (value !== "bridge" && value !== "camera") {
    throw new ContractValidationError("nodeKind must be bridge or camera");
  }
  return value;
}

export function expectOpenCommissioningWindowParams(value: unknown): OpenCommissioningWindowParams {
  const object = expectObject(value, "commissioning window params");
  return {
    durationSeconds: expectIntegerInRange(object.durationSeconds, "durationSeconds", 1, 86_400),
  };
}

export function expectRemoveFabricParams(value: unknown): RemoveFabricParams {
  const object = expectObject(value, "remove fabric params");
  return { fabricId: expectNonEmptyString(object.fabricId, "fabricId") };
}

export function expectAttributeWriteEvent(value: unknown): AttributeWriteEvent {
  const object = expectObject(value, "attribute write event");
  return {
    targetId: expectNonEmptyString(object.targetId, "targetId"),
    deviceId: expectNonEmptyString(object.deviceId, "deviceId"),
    endpointId: expectIntegerInRange(object.endpointId, "endpointId", 1, 65_534),
    path: expectNonEmptyString(object.path, "path"),
    value: expectJsonValue(object.value, "value"),
    interactionId: expectNonEmptyString(object.interactionId, "interactionId"),
  };
}

export function expectAttributeWriteResult(value: unknown): AttributeWriteResult {
  const object = expectObject(value, "attribute write result");
  const result: AttributeWriteResult = {
    accepted: expectBoolean(object.accepted, "accepted"),
  };
  if (object.message !== undefined) {
    result.message = expectNonEmptyString(object.message, "message");
  }
  return result;
}

export function expectCommandEvent(value: unknown): CommandEvent {
  const object = expectObject(value, "command event");
  return {
    targetId: expectNonEmptyString(object.targetId, "targetId"),
    deviceId: expectNonEmptyString(object.deviceId, "deviceId"),
    endpointId: expectIntegerInRange(object.endpointId, "endpointId", 1, 65_534),
    path: expectNonEmptyString(object.path, "path"),
    fields: expectJsonRecord(object.fields, "fields"),
    interactionId: expectNonEmptyString(object.interactionId, "interactionId"),
  };
}

export function expectCommandResult(value: unknown): CommandResult {
  const object = expectObject(value, "command result");
  const result: CommandResult = {
    accepted: expectBoolean(object.accepted, "accepted"),
  };
  if (object.fields !== undefined) {
    result.fields = expectJsonRecord(object.fields, "fields");
  }
  if (object.message !== undefined) {
    result.message = expectNonEmptyString(object.message, "message");
  }
  return result;
}

export function expectRuntimeDiagnostics(value: unknown): RuntimeDiagnosticsEvent {
  const object = expectObject(value, "runtime diagnostics");
  const rawMetrics = expectObject(object.metrics, "metrics");
  const metrics: Record<string, number> = {};
  const level = expectNonEmptyString(object.level, "level");
  if (!["info", "warning", "error"].includes(level)) {
    throw new ContractValidationError(`unsupported diagnostics level ${level}`);
  }
  for (const [name, metric] of Object.entries(rawMetrics)) {
    if (typeof metric !== "number" || !Number.isFinite(metric)) {
      throw new ContractValidationError(`metric ${name} must be a finite number`);
    }
    metrics[name] = metric;
  }
  const result: RuntimeDiagnosticsEvent = {
    targetId: expectNonEmptyString(object.targetId, "targetId"),
    timestamp: expectNonEmptyString(object.timestamp, "timestamp"),
    level: level as RuntimeDiagnosticsEvent["level"],
    code: expectNonEmptyString(object.code, "code"),
    metrics,
  };
  if (object.message !== undefined) {
    result.message = expectNonEmptyString(object.message, "message");
  }
  return result;
}

export function expectCommissioningChangedEvent(value: unknown): CommissioningChangedEvent {
  const object = expectObject(value, "commissioning changed event");
  const state = expectNonEmptyString(object.state, "state");
  const reason = expectNonEmptyString(object.reason, "reason");
  if (!["uncommissioned", "commissioned"].includes(state)) {
    throw new ContractValidationError(`unsupported commissioning state ${state}`);
  }
  if (!["startup", "window-opened", "window-closed", "fabric-added", "fabric-removed", "factory-reset"].includes(reason)) {
    throw new ContractValidationError(`unsupported commissioning reason ${reason}`);
  }
  return {
    targetId: expectNonEmptyString(object.targetId, "targetId"),
    timestamp: expectNonEmptyString(object.timestamp, "timestamp"),
    state: state as CommissioningChangedEvent["state"],
    windowOpen: expectBoolean(object.windowOpen, "windowOpen"),
    fabricCount: expectIntegerInRange(object.fabricCount, "fabricCount", 0, Number.MAX_SAFE_INTEGER),
    reason: reason as CommissioningChangedEvent["reason"],
  };
}

export function expectFabricChangedEvent(value: unknown): FabricChangedEvent {
  const object = expectObject(value, "fabric changed event");
  const change = expectNonEmptyString(object.change, "change");
  if (!["added", "removed", "reset"].includes(change)) {
    throw new ContractValidationError(`unsupported fabric change ${change}`);
  }
  const result: FabricChangedEvent = {
    targetId: expectNonEmptyString(object.targetId, "targetId"),
    timestamp: expectNonEmptyString(object.timestamp, "timestamp"),
    change: change as FabricChangedEvent["change"],
    fabricCount: expectIntegerInRange(object.fabricCount, "fabricCount", 0, Number.MAX_SAFE_INTEGER),
  };
  if (object.fabricId !== undefined) {
    result.fabricId = expectNonEmptyString(object.fabricId, "fabricId");
  }
  if (object.label !== undefined) {
    result.label = expectNonEmptyString(object.label, "label");
  }
  return result;
}

export function expectStorageKey(value: unknown): string {
  const key = expectNonEmptyString(value, "key");
  if (key.length > 512 || key.includes("\0")) {
    throw new ContractValidationError("key must be at most 512 characters and contain no NUL");
  }
  return key;
}

export function expectStorageValueBase64(value: unknown): string {
  if (typeof value !== "string" || value.length > 1_350_000 ||
      (value.length > 0 && !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value))) {
    throw new ContractValidationError("valueBase64 must be valid base64 no larger than the IPC frame limit");
  }
  return value;
}

export function expectObject(value: unknown, name: string): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new ContractValidationError(`${name} must be an object`);
  }
  return value as Record<string, unknown>;
}

export function expectNonEmptyString(value: unknown, name: string): string {
  if (typeof value !== "string" || value.trim() === "") {
    throw new ContractValidationError(`${name} must be a non-empty string`);
  }
  return value;
}

export function expectBoolean(value: unknown, name: string): boolean {
  if (typeof value !== "boolean") {
    throw new ContractValidationError(`${name} must be a boolean`);
  }
  return value;
}

export function expectIntegerInRange(value: unknown, name: string, minimum: number, maximum: number): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum || (value as number) > maximum) {
    throw new ContractValidationError(`${name} must be an integer between ${minimum} and ${maximum}`);
  }
  return value as number;
}

export function expectJsonRecord(value: unknown, name: string): Record<string, JsonValue> {
  const object = expectObject(value, name);
  const result: Record<string, JsonValue> = {};
  for (const [key, entry] of Object.entries(object)) {
    result[key] = expectJsonValue(entry, `${name}.${key}`);
  }
  return result;
}

export function expectJsonValue(value: unknown, name: string): JsonValue {
  if (value === null || typeof value === "string" || typeof value === "boolean") {
    return value;
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      throw new ContractValidationError(`${name} must not contain a non-finite number`);
    }
    return value;
  }
  if (Array.isArray(value)) {
    return value.map((entry, index) => expectJsonValue(entry, `${name}[${index}]`));
  }
  if (typeof value === "object") {
    return expectJsonRecord(value, name);
  }
  throw new ContractValidationError(`${name} is not a JSON value`);
}

export class ContractValidationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ContractValidationError";
  }
}
