import { createHash, randomUUID } from "node:crypto";
import type { SupportedStorageTypes } from "@matter/main";
import type { CommissioningOptions } from "@matter/main/types";
import type {
  AttributeUpdate,
  AttributeWriteResult,
  BridgeConfiguration,
  CommandResult,
  CommissioningChangedEvent,
  CommissioningWindowResult,
  DeviceSnapshot,
  FabricChangedEvent,
  JsonValue,
  ReachabilityUpdate,
  RuntimeReplayState,
} from "../contract.js";
import {
  AdapterOperationError,
  type AdapterContext,
  type AdapterDiagnostics,
  type HostStorage,
  type MatterProtocolAdapter,
} from "./adapter.js";
import type { LoadedMatterJs } from "./matter-js-adapter.js";

type MatterMain = LoadedMatterJs["main"];
type MatterProtocol = LoadedMatterJs["protocol"];

interface MatterJsDriverOptions {
  /**
   * Used by focused integration tests that exercise the official endpoint
   * implementation without binding UDP/mDNS. Production never disables it.
   */
  startNetwork?: boolean;
}

interface InstalledDevice {
  readonly snapshot: DeviceSnapshot;
  readonly endpoint: InstanceType<MatterMain["Endpoint"]>;
}

/**
 * Runtime service consumed by the custom cluster servers below.
 *
 * The service is installed in the node environment, so Matter command
 * execution remains within matter.js' transaction/ACL machinery while the
 * actual write is delegated to the authenticated Go host.
 */
class BridgeRuntimeService {
  readonly #hostUpdates = new Set<string>();

  constructor(
    readonly context: AdapterContext,
    readonly devicesByEndpoint: Map<number, DeviceSnapshot>,
  ) {}

  device(endpointNumber: number): DeviceSnapshot {
    const device = this.devicesByEndpoint.get(endpointNumber);
    if (device === undefined) {
      throw new AdapterOperationError(
        "device_not_found",
        `no HomeLoom device is registered for endpoint ${endpointNumber}`,
      );
    }
    return device;
  }

  async command(
    endpointNumber: number,
    path: string,
    fields: Record<string, JsonValue>,
  ): Promise<CommandResult> {
    const device = this.device(endpointNumber);
    return this.context.emitCommand({
      targetId: this.context.targetId,
      deviceId: device.id,
      endpointId: endpointNumber,
      path,
      fields,
      interactionId: randomUUID(),
    });
  }

  async attribute(
    endpointNumber: number,
    path: string,
    value: JsonValue,
  ): Promise<AttributeWriteResult> {
    const device = this.device(endpointNumber);
    return this.context.emitAttributeWrite({
      targetId: this.context.targetId,
      deviceId: device.id,
      endpointId: endpointNumber,
      path,
      value,
      interactionId: randomUUID(),
    });
  }

  isHostUpdate(endpointNumber: number, path: string): boolean {
    return this.#hostUpdates.has(`${endpointNumber}:${path}`);
  }

  async applyHostUpdate<T>(
    endpointNumber: number,
    path: string,
    operation: () => Promise<T>,
  ): Promise<T> {
    const key = `${endpointNumber}:${path}`;
    this.#hostUpdates.add(key);
    try {
      return await operation();
    } finally {
      this.#hostUpdates.delete(key);
    }
  }
}

/**
 * matter.js stores structured values in hierarchical contexts. The Go RPC is
 * deliberately a flat byte KV API, so this driver performs the only encoding
 * between those two boundaries. No filesystem-backed identity storage is ever
 * registered in the runtime environment.
 */
class HostStorageState {
  readonly #storage: HostStorage;
  readonly #namespace: string;
  readonly #toJson: MatterMain["toJson"];
  readonly #fromJson: MatterMain["fromJson"];
  readonly #entries = new Map<string, SupportedStorageTypes>();
  #initialized = false;

  constructor(
    storage: HostStorage,
    namespace: string,
    toJson: MatterMain["toJson"],
    fromJson: MatterMain["fromJson"],
  ) {
    this.#storage = storage;
    this.#namespace = namespace;
    this.#toJson = toJson;
    this.#fromJson = fromJson;
  }

  get initialized(): boolean {
    return this.#initialized;
  }

  async initialize(): Promise<void> {
    if (this.#initialized) {
      throw new AdapterOperationError("storage_already_initialized", "matter.js host storage is already initialized");
    }
    const prefix = this.#prefix;
    for (const entry of await this.#storage.list()) {
      if (!entry.key.startsWith(prefix)) {
        continue;
      }
      const encoded = Buffer.from(entry.valueBase64, "base64").toString("utf8");
      this.#entries.set(entry.key, this.#fromJson(encoded));
    }
    this.#initialized = true;
  }

  close(): void {
    this.#initialized = false;
    this.#entries.clear();
  }

  get(contexts: readonly string[], key: string): SupportedStorageTypes | undefined {
    this.#assertInitialized();
    return this.#entries.get(this.#key(contexts, key));
  }

  async set(
    contexts: readonly string[],
    keyOrValues: string | Record<string, SupportedStorageTypes>,
    value?: SupportedStorageTypes,
  ): Promise<void> {
    this.#assertInitialized();
    const values = typeof keyOrValues === "string"
      ? [[keyOrValues, value] as const]
      : Object.entries(keyOrValues);
    for (const [key, current] of values) {
      const storageKey = this.#key(contexts, key);
      const bytes = Buffer.from(this.#toJson(current), "utf8");
      await this.#storage.put(storageKey, bytes);
      this.#entries.set(storageKey, current);
    }
  }

  async delete(contexts: readonly string[], key: string): Promise<void> {
    this.#assertInitialized();
    const storageKey = this.#key(contexts, key);
    await this.#storage.delete(storageKey);
    this.#entries.delete(storageKey);
  }

  keys(contexts: readonly string[]): string[] {
    this.#assertInitialized();
    const prefix = this.#contextPrefix(contexts);
    const keys = new Set<string>();
    for (const storageKey of this.#entries.keys()) {
      if (!storageKey.startsWith(prefix)) {
        continue;
      }
      const suffix = storageKey.slice(prefix.length);
      if (suffix !== "" && !suffix.includes("/")) {
        keys.add(decodeSegment(suffix));
      }
    }
    return [...keys];
  }

  values(contexts: readonly string[]): Record<string, SupportedStorageTypes> {
    return Object.fromEntries(
      this.keys(contexts).map((key) => [key, this.get(contexts, key)]),
    );
  }

  contexts(contexts: readonly string[]): string[] {
    this.#assertInitialized();
    const prefix = this.#contextPrefix(contexts);
    const children = new Set<string>();
    for (const storageKey of this.#entries.keys()) {
      if (!storageKey.startsWith(prefix)) {
        continue;
      }
      const suffix = storageKey.slice(prefix.length);
      const separator = suffix.indexOf("/");
      if (separator > 0) {
        children.add(decodeSegment(suffix.slice(0, separator)));
      }
    }
    return [...children];
  }

  async clearAll(contexts: readonly string[]): Promise<void> {
    this.#assertInitialized();
    const prefix = this.#contextPrefix(contexts);
    const keys = [...this.#entries.keys()].filter((key) => key.startsWith(prefix));
    await Promise.all(keys.map((key) => this.#storage.delete(key)));
    for (const key of keys) {
      this.#entries.delete(key);
    }
  }

  async has(contexts: readonly string[], key: string): Promise<boolean> {
    return this.get(contexts, key) !== undefined;
  }

  #assertInitialized(): void {
    if (!this.#initialized) {
      throw new AdapterOperationError("storage_not_initialized", "matter.js host storage is not initialized");
    }
  }

  get #prefix(): string {
    return `matter-js/${encodeSegment(this.#namespace)}/`;
  }

  #contextPrefix(contexts: readonly string[]): string {
    return `${this.#prefix}${contexts.map(encodeSegment).join("/")}${contexts.length === 0 ? "" : "/"}`;
  }

  #key(contexts: readonly string[], key: string): string {
    return `${this.#contextPrefix(contexts)}${encodeSegment(key)}`;
  }
}

function encodeSegment(value: string): string {
  return Buffer.from(value, "utf8").toString("base64url");
}

function decodeSegment(value: string): string {
  return Buffer.from(value, "base64url").toString("utf8");
}

/**
 * Concrete @matter/main 0.17.6 bridge implementation.
 */
export class MatterJsBridgeDriver implements MatterProtocolAdapter {
  readonly #sdk: LoadedMatterJs;
  readonly #options: Required<MatterJsDriverOptions>;
  readonly #installed = new Map<string, InstalledDevice>();
  readonly #devicesByEndpoint = new Map<number, DeviceSnapshot>();
  #context: AdapterContext | undefined;
  #configuration: BridgeConfiguration | undefined;
  #environment: InstanceType<MatterMain["Environment"]> | undefined;
  #node: InstanceType<MatterMain["ServerNode"]> | undefined;
  #aggregator: InstanceType<MatterMain["Endpoint"]> | undefined;
  #commissioningWindowExpiresAt: string | null = null;
  #commissioningTimer: NodeJS.Timeout | undefined;
  #commissioningDurationSeconds = 900;
  #identityGeneration = 0;

  constructor(sdk: LoadedMatterJs, options: MatterJsDriverOptions = {}) {
    this.#sdk = sdk;
    this.#options = {
      startNetwork: options.startNetwork ?? true,
    };
  }

  async start(context: AdapterContext): Promise<void> {
    if (this.#context !== undefined) {
      throw new AdapterOperationError("already_started", "matter.js bridge driver is already started");
    }
    this.#context = context;
  }

  async stop(): Promise<void> {
    this.#clearCommissioningTimer();
    const node = this.#node;
    this.#node = undefined;
    this.#aggregator = undefined;
    this.#environment = undefined;
    this.#installed.clear();
    this.#devicesByEndpoint.clear();
    this.#configuration = undefined;
    this.#commissioningWindowExpiresAt = null;
    try {
      await node?.close();
    } finally {
      this.#context = undefined;
    }
  }

  async replayState(state: RuntimeReplayState): Promise<void> {
    this.#assertContext();
    if (state.bridge === null) {
      await this.#closeNode();
      this.#configuration = undefined;
      return;
    }
    await this.#applyConfiguration(state.bridge, state.devices);
  }

  async configureBridge(configuration: BridgeConfiguration): Promise<void> {
    this.#assertContext();
    const devices = [...this.#installed.values()].map(({ snapshot }) => structuredClone(snapshot));
    await this.#applyConfiguration(configuration, devices);
  }

  async replaceDevices(devices: DeviceSnapshot[]): Promise<void> {
    this.#assertReady();
    const wanted = new Set(devices.map((device) => device.id));
    for (const [deviceId, installed] of [...this.#installed]) {
      if (wanted.has(deviceId)) {
        continue;
      }
      await installed.endpoint.delete();
      installed.endpoint.owner = undefined;
      this.#installed.delete(deviceId);
      this.#devicesByEndpoint.delete(installed.snapshot.endpointId);
    }

    for (const device of devices) {
      const current = this.#installed.get(device.id);
      if (
        current !== undefined &&
        (current.snapshot.endpointId !== device.endpointId ||
          normalizeDeviceType(current.snapshot.deviceType) !== normalizeDeviceType(device.deviceType))
      ) {
        await current.endpoint.delete();
        current.endpoint.owner = undefined;
        this.#installed.delete(device.id);
        this.#devicesByEndpoint.delete(current.snapshot.endpointId);
      }
      const installed = this.#installed.get(device.id);
      if (installed === undefined) {
        await this.#addDevice(device);
      } else {
        Object.assign(installed.snapshot, structuredClone(device));
        this.#devicesByEndpoint.set(device.endpointId, installed.snapshot);
        await this.#applySnapshot(installed.endpoint, device);
      }
    }
  }

  async updateAttributes(updates: AttributeUpdate[]): Promise<void> {
    this.#assertReady();
    for (const update of updates) {
      const installed = this.#installed.get(update.deviceId);
      if (installed === undefined) {
        throw new AdapterOperationError("device_not_found", `unknown device ${update.deviceId}`);
      }
      installed.snapshot.attributes[update.path] = structuredClone(update.value);
      await this.#setAttribute(installed.endpoint, update.path, update.value);
    }
  }

  async updateReachability(updates: ReachabilityUpdate[]): Promise<void> {
    this.#assertReady();
    for (const update of updates) {
      const installed = this.#installed.get(update.deviceId);
      if (installed === undefined) {
        throw new AdapterOperationError("device_not_found", `unknown device ${update.deviceId}`);
      }
      installed.snapshot.reachable = update.reachable;
      await installed.endpoint.setStateOf("bridgedDeviceBasicInformation", {
        reachable: update.reachable,
      });
    }
  }

  async openCommissioningWindow(durationSeconds: number): Promise<CommissioningWindowResult> {
    const node = this.#assertReady();
    this.#commissioningDurationSeconds = durationSeconds;
    if (this.#options.startNetwork) {
      const commissioner = node.env.get(this.#sdk.protocol.DeviceCommissioner);
      await commissioner.allowBasicCommissioning();
    }
    this.#setCommissioningWindow(durationSeconds);
    await this.#emitCommissioningChanged("window-opened");
    return {
      open: true,
      expiresAt: this.#commissioningWindowExpiresAt,
    };
  }

  async closeCommissioningWindow(): Promise<CommissioningWindowResult> {
    const node = this.#assertReady();
    if (this.#options.startNetwork) {
      await node.env.maybeGet(this.#sdk.protocol.DeviceCommissioner)?.endCommissioning();
    }
    this.#commissioningWindowExpiresAt = null;
    this.#clearCommissioningTimer();
    await this.#emitCommissioningChanged("window-closed");
    return { open: false, expiresAt: null };
  }

  async removeFabric(fabricId: string): Promise<void> {
    const node = this.#assertReady();
    const manager = node.env.get(this.#sdk.protocol.FabricManager);
    const fabric = manager.fabrics.find((candidate) => {
      const identifiers = [
        String(candidate.fabricIndex),
        String(candidate.fabricId),
        String(candidate.globalId),
        `0x${candidate.fabricId.toString(16)}`,
        `0x${candidate.globalId.toString(16)}`,
      ];
      return identifiers.includes(fabricId.toLowerCase());
    });
    if (fabric === undefined) {
      throw new AdapterOperationError("fabric_not_found", `unknown fabric ${fabricId}`);
    }
    const eventId = String(fabric.fabricId);
    const label = fabric.label;
    await fabric.leave();
    await this.#emitFabricChanged("removed", eventId, label);
    await this.#emitCommissioningChanged("fabric-removed");
  }

  async factoryReset(): Promise<void> {
    const node = this.#assertReady();
    this.#clearCommissioningTimer();
    const duration = this.#configuration?.commissioningWindowSeconds ?? 900;
    this.#commissioningDurationSeconds = duration;
    await node.erase();
    await node.env.close(this.#sdk.protocol.DeviceCommissioner);
    this.#installCommissioningConfigProvider(node);
    for (const installed of this.#installed.values()) {
      await this.#applySnapshot(installed.endpoint, installed.snapshot);
    }
    if (this.#options.startNetwork) {
      await node.env.get(this.#sdk.protocol.DeviceCommissioner).allowBasicCommissioning();
    }
    this.#identityGeneration += 1;
    await this.#assertContext().storage.put(
      "runtime/identity-generation",
      Buffer.from(String(this.#identityGeneration), "utf8"),
    );
    this.#setCommissioningWindow(duration);
    await this.#emitFabricChanged("reset");
    await this.#emitCommissioningChanged("factory-reset");
  }

  diagnostics(): AdapterDiagnostics {
    const node = this.#node;
    const fabricManager = node?.env.get(this.#sdk.protocol.FabricManager);
    const fabrics = fabricManager?.fabrics.map((fabric) => {
      const summary: { id: string; label?: string } = {
        id: String(fabric.fabricId),
      };
      if (fabric.label !== "") {
        summary.label = fabric.label;
      }
      return summary;
    }) ?? [];
    const fabricCount = fabricManager?.length ?? 0;
    const result: AdapterDiagnostics = {
      configured: this.#configuration !== undefined,
      deviceCount: this.#installed.size,
      fabricCount,
      fabrics,
      commissioningWindowOpen: this.#commissioningWindowExpiresAt !== null,
      commissioningWindowExpiresAt: this.#commissioningWindowExpiresAt,
      identityGeneration: this.#identityGeneration,
    };
    if (node !== undefined && (fabricCount === 0 || this.#commissioningWindowExpiresAt !== null)) {
      const pairingCodes = node.stateOf(this.#sdk.main.CommissioningServer).pairingCodes;
      result.manualPairingCode = pairingCodes.manualPairingCode;
      result.qrPairingCode = pairingCodes.qrPairingCode;
    }
    return result;
  }

  /**
   * A narrow official-endpoint test hook. It invokes the actual installed
   * matter.js command behavior rather than bypassing cluster logic.
   */
  async invokeCommandForTest(
    deviceId: string,
    behaviorId: string,
    command: string,
    fields: Record<string, unknown> = {},
  ): Promise<void> {
    const installed = this.#installed.get(deviceId);
    if (installed === undefined) {
      throw new AdapterOperationError("device_not_found", `unknown device ${deviceId}`);
    }
    const commands = installed.endpoint.commandsOf(behaviorId) as Record<
      string,
      (fields: Record<string, unknown>) => Promise<unknown>
    >;
    const handler = commands[command];
    if (handler === undefined) {
      throw new AdapterOperationError("command_not_found", `unknown ${behaviorId}.${command}`);
    }
    await handler(fields);
  }

  async readAttributeForTest(deviceId: string, behaviorId: string, attribute: string): Promise<unknown> {
    const installed = this.#installed.get(deviceId);
    if (installed === undefined) {
      throw new AdapterOperationError("device_not_found", `unknown device ${deviceId}`);
    }
    const state = await installed.endpoint.getStateOf(behaviorId);
    return state[attribute];
  }

  /**
   * Test-only interaction seam for writable Matter attributes. Values here are
   * already in Matter wire representation; unlike updateAttributes(), this is
   * intentionally not marked as a host replay and therefore exercises the
   * behavior's Go callback reactor.
   */
  async writeAttributeForTest(
    deviceId: string,
    path: string,
    value: unknown,
  ): Promise<void> {
    const installed = this.#installed.get(deviceId);
    if (installed === undefined) {
      throw new AdapterOperationError("device_not_found", `unknown device ${deviceId}`);
    }
    switch (path) {
      case "FanControl.FanMode":
        await installed.endpoint.setStateOf("fanControl", { fanMode: value } as never);
        return;
      case "FanControl.PercentSetting":
        await installed.endpoint.setStateOf("fanControl", { percentSetting: value } as never);
        return;
      case "FanControl.RockSetting":
        await installed.endpoint.setStateOf("fanControl", { rockSetting: value } as never);
        return;
      case "Thermostat.SystemMode":
        await installed.endpoint.setStateOf("thermostat", { systemMode: value } as never);
        return;
      case "Thermostat.OccupiedHeatingSetpoint":
        await installed.endpoint.setStateOf(
          "thermostat",
          { occupiedHeatingSetpoint: value } as never,
        );
        return;
      case "Thermostat.OccupiedCoolingSetpoint":
        await installed.endpoint.setStateOf(
          "thermostat",
          { occupiedCoolingSetpoint: value } as never,
        );
        return;
      case "ThermostatUserInterfaceConfiguration.TemperatureDisplayMode":
        await installed.endpoint.setStateOf(
          "thermostatUserInterfaceConfiguration",
          { temperatureDisplayMode: value } as never,
        );
        return;
      default:
        throw new AdapterOperationError(
          "attribute_unsupported",
          `Matter attribute ${path} is not writable by the first-batch driver`,
        );
    }
  }

  async #applyConfiguration(
    configuration: BridgeConfiguration,
    devices: DeviceSnapshot[],
  ): Promise<void> {
    if (
      this.#configuration !== undefined &&
      JSON.stringify(this.#configuration) === JSON.stringify(configuration) &&
      this.#node !== undefined
    ) {
      await this.replaceDevices(devices);
      return;
    }

    await this.#closeNode();
    this.#configuration = structuredClone(configuration);
    const context = this.#assertContext();
    const generation = await context.storage.get("runtime/identity-generation");
    if (generation !== undefined) {
      const parsed = Number.parseInt(Buffer.from(generation).toString("utf8"), 10);
      if (Number.isSafeInteger(parsed) && parsed >= 0) {
        this.#identityGeneration = parsed;
      }
    } else {
      await context.storage.put("runtime/identity-generation", Buffer.from("0", "utf8"));
    }

    const environment = new this.#sdk.main.Environment(
      `homeloom-${context.targetId}`,
      this.#sdk.main.Environment.default,
    );
    const storageService = new this.#sdk.main.StorageService(environment);
    // StorageService installation creates the local service map. Block the
    // inherited Node.js filesystem afterwards so ServerNodeStore cannot create
    // a DatafileRoot or driver metadata on disk.
    environment.delete(this.#sdk.main.Filesystem);
    const namespace = context.identityNamespace;
    const sdk = this.#sdk;
    const DriverBase = this.#sdk.main.StorageDriver;
    const hostStorage = context.storage;
    class HostStorageDriver extends DriverBase {
      static readonly id = "homeloom-host";
      readonly #state: HostStorageState;

      constructor(storageNamespace: string) {
        super();
        this.#state = new HostStorageState(hostStorage, `${namespace}:${storageNamespace}`, sdk.main.toJson, sdk.main.fromJson);
      }

      static async create(dataNamespace: { namespace: string }): Promise<HostStorageDriver> {
        const driver = new HostStorageDriver(dataNamespace.namespace);
        await driver.initialize();
        return driver;
      }

      override get initialized(): boolean {
        return this.#state.initialized;
      }

      override initialize(): Promise<void> {
        return this.#state.initialize();
      }

      override close(): void {
        this.#state.close();
      }

      override get(contexts: string[], key: string): SupportedStorageTypes | undefined {
        return this.#state.get(contexts, key);
      }

      override set(
        contexts: string[],
        keyOrValues: string | Record<string, SupportedStorageTypes>,
        value?: SupportedStorageTypes,
      ): Promise<void> {
        return this.#state.set(contexts, keyOrValues, value);
      }

      override delete(contexts: string[], key: string): Promise<void> {
        return this.#state.delete(contexts, key);
      }

      override has(contexts: string[], key: string): Promise<boolean> {
        return this.#state.has(contexts, key);
      }

      override keys(contexts: string[]): string[] {
        return this.#state.keys(contexts);
      }

      override values(contexts: string[]): Record<string, SupportedStorageTypes> {
        return this.#state.values(contexts);
      }

      override contexts(contexts: string[]): string[] {
        return this.#state.contexts(contexts);
      }

      override clearAll(contexts: string[]): Promise<void> {
        return this.#state.clearAll(contexts);
      }
    }
    storageService.registerDriver(HostStorageDriver);
    storageService.defaultDriver = HostStorageDriver.id;
    storageService.configuredDriver = HostStorageDriver.id;

    if (configuration.networkInterface !== undefined) {
      // One HomeLoom runtime process owns exactly one target, so constraining the
      // shared Node.js mDNS service here cannot affect another bridge.
      this.#sdk.main.Environment.default.vars.set(
        "mdns.networkInterface",
        configuration.networkInterface,
      );
    }

    const runtimeService = new BridgeRuntimeService(context, this.#devicesByEndpoint);
    environment.set(BridgeRuntimeService, runtimeService);

    const node = await this.#sdk.main.ServerNode.create({
      id: `homeloom-${safeId(context.targetId)}`,
      environment,
      network: {
        port: configuration.listenPort,
        ipv4: true,
        ble: false,
        discoveryCapabilities: { onIpNetwork: true },
      },
      commissioning: {
        passcode: configuration.commissioningPasscode,
        discriminator: configuration.discriminator,
      },
      productDescription: {
        name: configuration.productName,
        vendorId: this.#sdk.main.VendorId(configuration.vendorId),
        productId: configuration.productId,
      },
      basicInformation: {
        vendorName: "HomeLoom",
        vendorId: this.#sdk.main.VendorId(configuration.vendorId),
        productName: configuration.productName,
        productId: configuration.productId,
        nodeLabel: configuration.productName.slice(0, 32),
        serialNumber: configuration.serialNumber,
        hardwareVersion: 1,
        hardwareVersionString: "1",
        softwareVersion: 1,
        softwareVersionString: "0.1.0",
      },
    });

    this.#environment = environment;
    this.#node = node;
    this.#commissioningDurationSeconds = configuration.commissioningWindowSeconds;
    this.#installCommissioningConfigProvider(node);
    this.#attachNodeEvents(node);
    const aggregator = await node.add(this.#sdk.aggregator.AggregatorEndpoint, {
      id: "aggregator",
      number: this.#sdk.main.EndpointNumber(1),
    });
    this.#aggregator = aggregator;
    for (const device of devices) {
      await this.#addDevice(device);
    }

    if (this.#options.startNetwork) {
      await node.start();
    }

    const fabricCount = node.env.get(this.#sdk.protocol.FabricManager).length;
    if (fabricCount === 0) {
      this.#setCommissioningWindow(configuration.commissioningWindowSeconds);
    } else {
      this.#commissioningWindowExpiresAt = null;
    }
    await context.emitCommissioningChanged(this.#commissioningEvent("startup"));
  }

  async #closeNode(): Promise<void> {
    this.#clearCommissioningTimer();
    const node = this.#node;
    this.#node = undefined;
    this.#aggregator = undefined;
    this.#environment = undefined;
    this.#installed.clear();
    this.#devicesByEndpoint.clear();
    this.#commissioningWindowExpiresAt = null;
    await node?.close();
  }

  async #addDevice(device: DeviceSnapshot): Promise<void> {
    const aggregator = this.#aggregator;
    if (aggregator === undefined) {
      throw new AdapterOperationError("not_configured", "Matter aggregator is not configured");
    }
    if (this.#devicesByEndpoint.has(device.endpointId)) {
      throw new AdapterOperationError(
        "endpoint_conflict",
        `endpoint ${device.endpointId} is already assigned`,
      );
    }

    const type = this.#deviceType(device);
    const endpoint = await aggregator.add(type, {
      id: `device-${safeId(device.id)}`,
      number: this.#sdk.main.EndpointNumber(device.endpointId),
      bridgedDeviceBasicInformation: {
        nodeLabel: device.name.slice(0, 32),
        productName: device.name.slice(0, 32),
        serialNumber: `HL-${stableUniqueId(this.#assertContext().targetId, device.id).slice(0, 29)}`,
        uniqueId: stableUniqueId(this.#assertContext().targetId, device.id),
        reachable: device.reachable,
      },
      ...this.#initialClusterState(device),
    } as never);
    const snapshot = structuredClone(device);
    this.#installed.set(device.id, { snapshot, endpoint });
    this.#devicesByEndpoint.set(device.endpointId, snapshot);
    await this.#applySnapshot(endpoint, device);
  }

  #deviceType(device: DeviceSnapshot): ReturnType<LoadedMatterJs["extendedColorLight"]["ExtendedColorLightDevice"]["with"]> {
    const sdk = this.#sdk;
    const BridgeInfo = sdk.bridgedDeviceBasicInformation.BridgedDeviceBasicInformationServer;
    const Runtime = BridgeRuntimeService;
    const failure = (message?: string): never => {
      throw new sdk.types.StatusResponseError(message ?? "HomeLoom rejected the command", sdk.types.Status.Failure);
    };

    const OnOffBase = sdk.onOff.OnOffServer.with("Lighting");
    class HostOnOffServer extends OnOffBase {
      override async on(): Promise<void> {
        const result = await this.env.get(Runtime).command(this.endpoint.number, "OnOff.On", {});
        if (!result.accepted) {
          failure(result.message);
        }
        this.state.onOff = true;
      }

      override async off(): Promise<void> {
        const result = await this.env.get(Runtime).command(this.endpoint.number, "OnOff.Off", {});
        if (!result.accepted) {
          failure(result.message);
        }
        this.state.onOff = false;
      }

      override async toggle(): Promise<void> {
        if (this.state.onOff) {
          await this.off();
        } else {
          await this.on();
        }
      }
    }

    const GenericOnOffBase = sdk.onOff.OnOffServer;
    class HostGenericOnOffServer extends GenericOnOffBase {
      override async on(): Promise<void> {
        const result = await this.env.get(Runtime).command(this.endpoint.number, "OnOff.On", {});
        if (!result.accepted) {
          failure(result.message);
        }
        this.state.onOff = true;
      }

      override async off(): Promise<void> {
        const result = await this.env.get(Runtime).command(this.endpoint.number, "OnOff.Off", {});
        if (!result.accepted) {
          failure(result.message);
        }
        this.state.onOff = false;
      }

      override async toggle(): Promise<void> {
        if (this.state.onOff) {
          await this.off();
        } else {
          await this.on();
        }
      }
    }

    const LevelBase = sdk.levelControl.LevelControlServer.with("OnOff", "Lighting");
    class HostLevelControlServer extends LevelBase {
      override async moveToLevel(request: {
        level: number;
        transitionTime: number | null;
        optionsMask: Record<string, boolean>;
        optionsOverride: Record<string, boolean>;
      }): Promise<void> {
        const result = await this.env.get(Runtime).command(
          this.endpoint.number,
          "LevelControl.MoveToLevel",
          {
            level: matterLevelToPercent(request.level),
            transitionTime: request.transitionTime,
          },
        );
        if (!result.accepted) {
          failure(result.message);
        }
        this.state.currentLevel = request.level;
      }

      override async moveToLevelWithOnOff(request: {
        level: number;
        transitionTime: number | null;
      }): Promise<void> {
        await this.moveToLevel({
          ...request,
          optionsMask: {},
          optionsOverride: {},
        });
        const onOff = this.agent.maybeGet(HostOnOffServer);
        if (onOff !== undefined) {
          onOff.state.onOff = request.level > 0;
        }
      }
    }

    const ColorBase = sdk.colorControl.ColorControlServer.with(
      "HueSaturation",
      "Xy",
      "ColorTemperature",
    );
    class HostColorControlServer extends ColorBase {
      override async moveToHue(request: {
        hue: number;
        direction: number;
        transitionTime: number;
        optionsMask: Record<string, boolean>;
        optionsOverride: Record<string, boolean>;
      }): Promise<void> {
        const result = await this.env.get(Runtime).attribute(
          this.endpoint.number,
          "ColorControl.CurrentHue",
          matterHueToDegrees(request.hue),
        );
        if (!result.accepted) {
          failure(result.message);
        }
        this.state.currentHue = request.hue;
      }

      override async moveToSaturation(request: {
        saturation: number;
        transitionTime: number;
        optionsMask: Record<string, boolean>;
        optionsOverride: Record<string, boolean>;
      }): Promise<void> {
        const result = await this.env.get(Runtime).attribute(
          this.endpoint.number,
          "ColorControl.CurrentSaturation",
          matterSaturationToPercent(request.saturation),
        );
        if (!result.accepted) {
          failure(result.message);
        }
        this.state.currentSaturation = request.saturation;
      }

      override async moveToHueAndSaturation(request: {
        hue: number;
        saturation: number;
        transitionTime: number;
        optionsMask: Record<string, boolean>;
        optionsOverride: Record<string, boolean>;
      }): Promise<void> {
        await this.moveToHue({
          ...request,
          direction: 0,
        });
        await this.moveToSaturation(request);
      }

      override async moveToColorTemperature(request: {
        colorTemperatureMireds: number;
        transitionTime: number;
        optionsMask: Record<string, boolean>;
        optionsOverride: Record<string, boolean>;
      }): Promise<void> {
        const result = await this.env.get(Runtime).attribute(
          this.endpoint.number,
          "ColorControl.ColorTemperatureMireds",
          request.colorTemperatureMireds,
        );
        if (!result.accepted) {
          failure(result.message);
        }
        this.state.colorTemperatureMireds = request.colorTemperatureMireds;
      }
    }

    const WindowCoveringBase = sdk.windowCovering.WindowCoveringServer.with(
      "Lift",
      "PositionAwareLift",
    );
    type LiftRequest = Parameters<InstanceType<typeof WindowCoveringBase>["goToLiftPercentage"]>[0];
    class HostWindowCoveringServer extends WindowCoveringBase {
      override async upOrOpen(): Promise<void> {
        await this.#moveTo(0);
      }

      override async downOrClose(): Promise<void> {
        await this.#moveTo(10_000);
      }

      override async goToLiftPercentage(request: LiftRequest): Promise<void> {
        await this.#moveTo(request.liftPercent100thsValue);
      }

      override async stopMotion(): Promise<void> {
        const result = await this.env.get(Runtime).command(
          this.endpoint.number,
          "WindowCovering.StopMotion",
          {},
        );
        if (!result.accepted) {
          failure(result.message);
        }
        this.state.targetPositionLiftPercent100ths =
          this.state.currentPositionLiftPercent100ths;
      }

      async #moveTo(liftPercent100ths: number): Promise<void> {
        const result = await this.env.get(Runtime).command(
          this.endpoint.number,
          "WindowCovering.GoToLiftPercentage",
          {
            // The Go host owns values in unified-model percent.  Conversion to
            // Matter percent100ths remains exclusively in this adapter.
            liftPercent100ths: matterPercent100thsToPercent(liftPercent100ths),
          },
        );
        if (!result.accepted) {
          failure(result.message);
        }
        this.state.targetPositionLiftPercent100ths = liftPercent100ths;
      }
    }

    const FanControlBase = sdk.fanControl.FanControlServer.with("Auto", "Rocking");
    class HostFanControlServer extends FanControlBase {
      override initialize(): void {
        super.initialize();
        this.events.fanMode$Changing.isAsync = true;
        this.events.percentSetting$Changing.isAsync = true;
        this.events.rockSetting$Changing.isAsync = true;
        this.reactTo(this.events.fanMode$Changing, this.#fanModeChanging);
        this.reactTo(this.events.percentSetting$Changing, this.#percentSettingChanging);
        this.reactTo(this.events.rockSetting$Changing, this.#rockSettingChanging);
      }

      async #fanModeChanging(
        value: number,
        _oldValue: number,
        _context: { readonly offline?: boolean },
      ): Promise<void> {
        const runtime = this.env.get(Runtime);
        if (runtime.isHostUpdate(this.endpoint.number, "FanControl.FanMode")) {
          return;
        }
        const result = await runtime.attribute(
          this.endpoint.number,
          "FanControl.FanMode",
          matterFanModeToUnified(value),
        );
        if (!result.accepted) {
          failure(result.message);
        }
      }

      async #percentSettingChanging(
        value: number | null,
        _oldValue: number | null,
        _context: { readonly offline?: boolean },
      ): Promise<void> {
        const runtime = this.env.get(Runtime);
        if (
          runtime.isHostUpdate(this.endpoint.number, "FanControl.PercentSetting") ||
          value === null
        ) {
          return;
        }
        const result = await runtime.attribute(
          this.endpoint.number,
          "FanControl.PercentSetting",
          value,
        );
        if (!result.accepted) {
          failure(result.message);
        }
      }

      async #rockSettingChanging(
        value: { rockLeftRight?: boolean; rockUpDown?: boolean; rockRound?: boolean },
        _oldValue: { rockLeftRight?: boolean; rockUpDown?: boolean; rockRound?: boolean },
        _context: { readonly offline?: boolean },
      ): Promise<void> {
        const runtime = this.env.get(Runtime);
        if (runtime.isHostUpdate(this.endpoint.number, "FanControl.RockSetting")) {
          return;
        }
        const result = await runtime.attribute(
          this.endpoint.number,
          "FanControl.RockSetting",
          value.rockLeftRight === true || value.rockUpDown === true || value.rockRound === true,
        );
        if (!result.accepted) {
          failure(result.message);
        }
      }
    }

    const ThermostatBase = sdk.thermostat.ThermostatServer.with(
      "Heating",
      "Cooling",
      "AutoMode",
    );
    class HostThermostatServer extends ThermostatBase {
      override async initialize(): Promise<void> {
        await super.initialize();
        this.events.systemMode$Changing.isAsync = true;
        this.events.occupiedHeatingSetpoint$Changing.isAsync = true;
        this.events.occupiedCoolingSetpoint$Changing.isAsync = true;
        this.reactTo(this.events.systemMode$Changing, this.#systemModeChanging);
        this.reactTo(
          this.events.occupiedHeatingSetpoint$Changing,
          this.#occupiedHeatingSetpointChanging,
        );
        this.reactTo(
          this.events.occupiedCoolingSetpoint$Changing,
          this.#occupiedCoolingSetpointChanging,
        );
      }

      async #systemModeChanging(
        value: number,
        _oldValue: number,
        _context: { readonly offline?: boolean },
      ): Promise<void> {
        const runtime = this.env.get(Runtime);
        if (runtime.isHostUpdate(this.endpoint.number, "Thermostat.SystemMode")) {
          return;
        }
        const result = await runtime.attribute(
          this.endpoint.number,
          "Thermostat.SystemMode",
          matterThermostatModeToUnified(value),
        );
        if (!result.accepted) {
          failure(result.message);
        }
      }

      async #occupiedHeatingSetpointChanging(
        value: number,
        _oldValue: number,
        _context: { readonly offline?: boolean },
      ): Promise<void> {
        const runtime = this.env.get(Runtime);
        if (
          runtime.isHostUpdate(
            this.endpoint.number,
            "Thermostat.OccupiedHeatingSetpoint",
          )
        ) {
          return;
        }
        const result = await runtime.attribute(
          this.endpoint.number,
          "Thermostat.OccupiedHeatingSetpoint",
          matterTemperatureToCelsius(value),
        );
        if (!result.accepted) {
          failure(result.message);
        }
      }

      async #occupiedCoolingSetpointChanging(
        value: number,
        _oldValue: number,
        _context: { readonly offline?: boolean },
      ): Promise<void> {
        const runtime = this.env.get(Runtime);
        if (
          runtime.isHostUpdate(
            this.endpoint.number,
            "Thermostat.OccupiedCoolingSetpoint",
          )
        ) {
          return;
        }
        const result = await runtime.attribute(
          this.endpoint.number,
          "Thermostat.OccupiedCoolingSetpoint",
          matterTemperatureToCelsius(value),
        );
        if (!result.accepted) {
          failure(result.message);
        }
      }
    }

    const ThermostatUiBase =
      sdk.thermostatUserInterfaceConfiguration.ThermostatUserInterfaceConfigurationServer;
    class HostThermostatUiServer extends ThermostatUiBase {
      override initialize(): void {
        super.initialize();
        this.events.temperatureDisplayMode$Changing.isAsync = true;
        this.reactTo(
          this.events.temperatureDisplayMode$Changing,
          this.#temperatureDisplayModeChanging,
        );
      }

      async #temperatureDisplayModeChanging(
        value: number,
        _oldValue: number,
        _context: { readonly offline?: boolean },
      ): Promise<void> {
        const runtime = this.env.get(Runtime);
        if (
          runtime.isHostUpdate(
            this.endpoint.number,
            "ThermostatUserInterfaceConfiguration.TemperatureDisplayMode",
          )
        ) {
          return;
        }
        const result = await runtime.attribute(
          this.endpoint.number,
          "ThermostatUserInterfaceConfiguration.TemperatureDisplayMode",
          value === 1 ? "fahrenheit" : "celsius",
        );
        if (!result.accepted) {
          failure(result.message);
        }
      }
    }

    const DoorLockBase = sdk.doorLock.DoorLockServer.with("DoorPositionSensor");
    type LockRequest = Parameters<InstanceType<typeof DoorLockBase>["lockDoor"]>[0];
    type UnlockRequest = Parameters<InstanceType<typeof DoorLockBase>["unlockDoor"]>[0];
    class HostDoorLockServer extends DoorLockBase {
      override async lockDoor(_request: LockRequest): Promise<void> {
        const result = await this.env.get(Runtime).command(
          this.endpoint.number,
          "DoorLock.LockDoor",
          {},
        );
        if (!result.accepted) {
          failure(result.message);
        }
      }

      override async unlockDoor(_request: UnlockRequest): Promise<void> {
        const result = await this.env.get(Runtime).command(
          this.endpoint.number,
          "DoorLock.UnlockDoor",
          {},
        );
        if (!result.accepted) {
          failure(result.message);
        }
      }
    }

    switch (normalizeDeviceType(device.deviceType)) {
      case "switch":
        return sdk.onOffLight.OnOffLightDevice.with(HostOnOffServer, BridgeInfo) as never;
      case "outlet":
        return sdk.onOffPlugInUnit.OnOffPlugInUnitDevice.with(HostOnOffServer, BridgeInfo) as never;
      case "light":
        return sdk.extendedColorLight.ExtendedColorLightDevice.with(
          HostOnOffServer,
          HostLevelControlServer,
          HostColorControlServer,
          BridgeInfo,
        );
      case "temperature":
        return sdk.temperatureSensor.TemperatureSensorDevice.with(BridgeInfo) as never;
      case "humidity":
        return sdk.humiditySensor.HumiditySensorDevice.with(BridgeInfo) as never;
      case "contact":
        return sdk.contactSensor.ContactSensorDevice.with(BridgeInfo) as never;
      case "motion":
      case "occupancy":
        return sdk.occupancySensor.OccupancySensorDevice.with(
          sdk.occupancySensing.OccupancySensingServer.with(
            "PassiveInfrared",
            "OccupancyEvent",
          ),
          BridgeInfo,
        ) as never;
      case "window-covering":
        return sdk.windowCoveringDevice.WindowCoveringDevice.with(
          HostWindowCoveringServer,
          BridgeInfo,
        ) as never;
      case "fan":
        return sdk.fanDevice.FanDevice.with(
          HostFanControlServer,
          HostGenericOnOffServer,
          BridgeInfo,
        ) as never;
      case "thermostat":
        return sdk.thermostatDevice.ThermostatDevice.with(
          HostThermostatServer,
          HostThermostatUiServer,
          BridgeInfo,
        ) as never;
      case "lock":
        return sdk.doorLockDevice.DoorLockDevice.with(
          HostDoorLockServer,
          BridgeInfo,
        ) as never;
      default:
        throw new AdapterOperationError(
          "device_type_unsupported",
          `Matter device type ${device.deviceType} is not supported by the 0.17.6 bridge driver`,
        );
    }
  }

  #initialClusterState(device: DeviceSnapshot): Record<string, Record<string, unknown>> {
    const state: Record<string, Record<string, unknown>> = {};
    const deviceType = normalizeDeviceType(device.deviceType);
    const onOff = device.attributes["OnOff.OnOff"];
    if (typeof onOff === "boolean") {
      state.onOff = { onOff };
    }

    if (deviceType === "light") {
      state.levelControl = {
        currentLevel: percentToMatterLevel(numberValue(device.attributes["LevelControl.CurrentLevel"], 100)),
        minLevel: 1,
        maxLevel: 254,
      };
      state.colorControl = {
        currentHue: degreesToMatterHue(numberValue(device.attributes["ColorControl.CurrentHue"], 0)),
        currentSaturation: percentToMatterSaturation(
          numberValue(device.attributes["ColorControl.CurrentSaturation"], 0),
        ),
        currentX: 0,
        currentY: 0,
        colorTemperatureMireds: integerValue(
          device.attributes["ColorControl.ColorTemperatureMireds"],
          370,
        ),
        colorTempPhysicalMinMireds: 50,
        colorTempPhysicalMaxMireds: 1000,
        coupleColorTempToLevelMinMireds: 50,
        colorMode: 0,
        enhancedColorMode: 0,
      };
    }

    if (deviceType === "temperature") {
      state.temperatureMeasurement = {
        measuredValue: celsiusToMatterTemperature(
          numberValue(device.attributes["TemperatureMeasurement.MeasuredValue"], 0),
        ),
        minMeasuredValue: -10_000,
        maxMeasuredValue: 20_000,
      };
    }

    if (deviceType === "humidity") {
      state.relativeHumidityMeasurement = {
        measuredValue: percentToMatterPercent100ths(
          numberValue(device.attributes["RelativeHumidityMeasurement.MeasuredValue"], 0),
        ),
        minMeasuredValue: 0,
        maxMeasuredValue: 10_000,
      };
    }

    if (deviceType === "contact") {
      state.booleanState = {
        stateValue: booleanValue(
          device.attributes["BooleanState.StateValue"] ?? false,
          "BooleanState.StateValue",
        ),
      };
    }

    if (deviceType === "motion" || deviceType === "occupancy") {
      state.occupancySensing = {
        occupancy: occupancyValue(
          device.attributes["OccupancySensing.Occupancy"] ?? false,
        ),
        occupancySensorType: 0,
        occupancySensorTypeBitmap: { pir: true },
      };
    }

    if (deviceType === "window-covering") {
      const currentPosition = percentToMatterPercent100ths(
        numberValue(device.attributes["WindowCovering.CurrentPositionLiftPercent100ths"], 0),
      );
      const targetPosition = percentToMatterPercent100ths(
        numberValue(
          device.attributes["WindowCovering.TargetPositionLiftPercent100ths"],
          matterPercent100thsToPercent(currentPosition),
        ),
      );
      state.windowCovering = {
        type: 0,
        configStatus: { operational: true, liftPositionAware: true },
        operationalStatus: windowOperationalStatusValue(
          device.attributes["WindowCovering.OperationalStatus"] ?? "stopped",
        ),
        endProductType: 0,
        mode: {},
        safetyStatus: windowSafetyStatusValue(
          device.attributes["WindowCovering.SafetyStatus"] ?? false,
        ),
        currentPositionLiftPercent100ths: currentPosition,
        targetPositionLiftPercent100ths: targetPosition,
        supportsMaintenanceMode: false,
      };
    }

    if (deviceType === "fan") {
      const active = typeof onOff === "boolean" ? onOff : true;
      const fanMode = unifiedFanModeToMatter(
        device.attributes["FanControl.FanMode"],
        active,
      );
      const percentSetting = matterPercent(
        device.attributes["FanControl.PercentSetting"],
        active ? 100 : 0,
      );
      state.fanControl = {
        fanMode,
        fanModeSequence: 2,
        percentSetting: fanMode === 5 ? null : percentSetting,
        percentCurrent: active ? percentSetting : 0,
        rockSupport: { rockLeftRight: true },
        rockSetting: fanRockSettingValue(
          device.attributes["FanControl.RockSetting"] ?? false,
        ),
      };
    }

    if (deviceType === "thermostat") {
      const heatingSetpoint = celsiusToMatterTemperature(
        numberValue(device.attributes["Thermostat.OccupiedHeatingSetpoint"], 20),
      );
      const coolingSetpoint = celsiusToMatterTemperature(
        numberValue(
          device.attributes["Thermostat.OccupiedCoolingSetpoint"],
          Math.max(22.5, matterTemperatureToCelsius(heatingSetpoint) + 2.5),
        ),
      );
      const thermostat: Record<string, unknown> = {
        localTemperature: nullableCelsiusToMatterTemperature(
          device.attributes["Thermostat.LocalTemperature"] ?? 20,
        ),
        controlSequenceOfOperation: 4,
        systemMode: unifiedThermostatModeToMatter(
          device.attributes["Thermostat.SystemMode"] ?? "off",
        ),
        thermostatRunningState: thermostatRunningStateValue(
          device.attributes["Thermostat.ThermostatRunningState"] ?? "off",
        ),
        occupiedHeatingSetpoint: heatingSetpoint,
        occupiedCoolingSetpoint: coolingSetpoint,
      };
      state.thermostat = thermostat;
      state.thermostatUserInterfaceConfiguration = {
        temperatureDisplayMode: temperatureDisplayModeValue(
          device.attributes[
            "ThermostatUserInterfaceConfiguration.TemperatureDisplayMode"
          ] ?? "celsius",
        ),
        keypadLockout: 0,
      };
    }

    if (deviceType === "lock") {
      const lock: Record<string, unknown> = {
        lockState: doorLockStateValue(device.attributes["DoorLock.LockState"] ?? "unknown"),
        lockType: 0,
        actuatorEnabled: true,
        operatingMode: 0,
      };
      const doorState = device.attributes["DoorLock.DoorState"];
      if (doorState !== undefined) {
        lock.doorState = doorStateValue(doorState);
      }
      state.doorLock = lock;
    }
    return state;
  }

  async #applySnapshot(
    endpoint: InstanceType<MatterMain["Endpoint"]>,
    device: DeviceSnapshot,
  ): Promise<void> {
    await endpoint.setStateOf("bridgedDeviceBasicInformation", {
      reachable: device.reachable,
      nodeLabel: device.name.slice(0, 32),
    });
    for (const [path, value] of Object.entries(device.attributes)) {
      await this.#setAttribute(endpoint, path, value);
    }
  }

  async #setAttribute(
    endpoint: InstanceType<MatterMain["Endpoint"]>,
    path: string,
    value: JsonValue,
  ): Promise<void> {
    const runtime = this.#environment?.get(BridgeRuntimeService);
    if (runtime === undefined) {
      throw new AdapterOperationError(
        "not_configured",
        "Matter runtime service is unavailable",
      );
    }
    await runtime.applyHostUpdate(
      endpoint.number,
      path,
      () => this.#setAttributeState(endpoint, path, value),
    );
  }

  async #setAttributeState(
    endpoint: InstanceType<MatterMain["Endpoint"]>,
    path: string,
    value: JsonValue,
  ): Promise<void> {
    switch (path) {
      case "OnOff.OnOff":
        await endpoint.setStateOf("onOff", { onOff: booleanValue(value, path) });
        return;
      case "LevelControl.CurrentLevel":
        await endpoint.setStateOf("levelControl", {
          currentLevel: percentToMatterLevel(numberValue(value)),
        });
        return;
      case "ColorControl.CurrentHue":
        await endpoint.setStateOf("colorControl", {
          currentHue: degreesToMatterHue(numberValue(value)),
        });
        return;
      case "ColorControl.CurrentSaturation":
        await endpoint.setStateOf("colorControl", {
          currentSaturation: percentToMatterSaturation(numberValue(value)),
        });
        return;
      case "ColorControl.ColorTemperatureMireds":
        await endpoint.setStateOf("colorControl", {
          colorTemperatureMireds: integerValue(value),
        });
        return;
      case "TemperatureMeasurement.MeasuredValue":
        await endpoint.setStateOf("temperatureMeasurement", {
          measuredValue: value === null ? null : celsiusToMatterTemperature(numberValue(value)),
        });
        return;
      case "RelativeHumidityMeasurement.MeasuredValue":
        await endpoint.setStateOf("relativeHumidityMeasurement", {
          measuredValue: nullablePercentToMatterPercent100ths(value),
        });
        return;
      case "BooleanState.StateValue":
        await endpoint.setStateOf("booleanState", {
          stateValue: booleanValue(value, path),
        });
        return;
      case "OccupancySensing.Occupancy":
        await endpoint.setStateOf("occupancySensing", {
          occupancy: occupancyValue(value),
        });
        return;
      case "WindowCovering.CurrentPositionLiftPercent100ths":
        await endpoint.setStateOf("windowCovering", {
          currentPositionLiftPercent100ths: nullablePercentToMatterPercent100ths(value),
        });
        return;
      case "WindowCovering.TargetPositionLiftPercent100ths":
        await endpoint.setStateOf("windowCovering", {
          targetPositionLiftPercent100ths: nullablePercentToMatterPercent100ths(value),
        });
        return;
      case "WindowCovering.OperationalStatus":
        await endpoint.setStateOf("windowCovering", {
          operationalStatus: windowOperationalStatusValue(value),
        });
        return;
      case "WindowCovering.SafetyStatus":
        await endpoint.setStateOf("windowCovering", {
          safetyStatus: windowSafetyStatusValue(value),
        });
        return;
      case "FanControl.FanMode":
        await endpoint.setStateOf("fanControl", {
          fanMode: unifiedFanModeToMatter(value, true),
        });
        return;
      case "FanControl.PercentSetting":
        {
          const fan = await endpoint.getStateOf("fanControl");
          const percentSetting = value === null ? null : matterPercent(value);
          await endpoint.setStateOf("fanControl", {
            // Matter requires PercentSetting=null in Auto.  The unified
            // rotation speed still represents observed speed in that mode, so
            // preserve it through PercentCurrent.
            percentSetting: fan.fanMode === 5 ? null : percentSetting,
            ...(percentSetting === null ? {} : { percentCurrent: percentSetting }),
          });
        }
        return;
      case "FanControl.RockSetting":
        await endpoint.setStateOf("fanControl", {
          rockSetting: fanRockSettingValue(value),
        });
        return;
      case "Thermostat.ThermostatRunningState":
        await endpoint.setStateOf("thermostat", {
          thermostatRunningState: thermostatRunningStateValue(value),
        });
        return;
      case "Thermostat.SystemMode":
        await endpoint.setStateOf("thermostat", {
          systemMode: unifiedThermostatModeToMatter(value),
        });
        return;
      case "Thermostat.LocalTemperature":
        await endpoint.setStateOf("thermostat", {
          localTemperature: nullableCelsiusToMatterTemperature(value),
        });
        return;
      case "Thermostat.OccupiedHeatingSetpoint":
        await endpoint.setStateOf("thermostat", {
          occupiedHeatingSetpoint: celsiusToMatterTemperature(numberValue(value)),
        });
        return;
      case "Thermostat.OccupiedCoolingSetpoint":
        await endpoint.setStateOf("thermostat", {
          occupiedCoolingSetpoint: celsiusToMatterTemperature(numberValue(value)),
        });
        return;
      case "ThermostatUserInterfaceConfiguration.TemperatureDisplayMode":
        await endpoint.setStateOf("thermostatUserInterfaceConfiguration", {
          temperatureDisplayMode: temperatureDisplayModeValue(value),
        });
        return;
      case "DoorLock.LockState":
        await endpoint.setStateOf("doorLock", {
          lockState: doorLockStateValue(value),
        });
        return;
      case "DoorLock.DoorState":
        await endpoint.setStateOf("doorLock", {
          doorState: doorStateValue(value),
        });
        return;
      default:
        throw new AdapterOperationError(
          "attribute_unsupported",
          `Matter attribute ${path} is not supported by the endpoint driver`,
        );
    }
  }

  #attachNodeEvents(node: InstanceType<MatterMain["ServerNode"]>): void {
    const events = node.eventsOf(this.#sdk.main.CommissioningServer);
    events.commissioned.on(() => {
      this.#commissioningWindowExpiresAt = null;
      this.#clearCommissioningTimer();
      void this.#emitCommissioningChanged("fabric-added");
    });
    events.decommissioned.on(() => {
      void this.#emitCommissioningChanged("fabric-removed");
    });
    events.fabricsChanged.on((fabricIndex, action) => {
      if (action === "updated") {
        return;
      }
      const manager = node.env.get(this.#sdk.protocol.FabricManager);
      const fabric = manager.maybeFor(fabricIndex);
      if (action === "added" && fabric !== undefined) {
        void this.#emitFabricChanged("added", String(fabric.fabricId), fabric.label);
      }
    });
  }

  #installCommissioningConfigProvider(
    node: InstanceType<MatterMain["ServerNode"]>,
  ): void {
    const sdk = this.#sdk;
    const duration = (): number => this.#commissioningDurationSeconds;
    class HomeLoomCommissioningConfigProvider extends sdk.protocol.CommissioningConfigProvider {
      override get values(): CommissioningOptions.Configuration {
        const { commissioning, productDescription, network } = node.state;
        return {
          ...commissioning,
          productDescription,
          ble: !!network.ble,
          advertisementWindow: sdk.main.Seconds(duration()),
        } as CommissioningOptions.Configuration;
      }
    }
    node.env.set(
      sdk.protocol.CommissioningConfigProvider,
      new HomeLoomCommissioningConfigProvider(),
    );
  }

  #setCommissioningWindow(durationSeconds: number): void {
    this.#commissioningDurationSeconds = durationSeconds;
    this.#clearCommissioningTimer();
    this.#commissioningWindowExpiresAt = new Date(
      Date.now() + durationSeconds * 1_000,
    ).toISOString();
    this.#commissioningTimer = setTimeout(() => {
      this.#commissioningTimer = undefined;
      void this.#expireCommissioningWindow();
    }, durationSeconds * 1_000);
    this.#commissioningTimer.unref();
  }

  #clearCommissioningTimer(): void {
    if (this.#commissioningTimer !== undefined) {
      clearTimeout(this.#commissioningTimer);
      this.#commissioningTimer = undefined;
    }
  }

  async #expireCommissioningWindow(): Promise<void> {
    if (this.#commissioningWindowExpiresAt === null) {
      return;
    }
    if (this.#options.startNetwork) {
      await this.#node?.env
        .maybeGet(this.#sdk.protocol.DeviceCommissioner)
        ?.endCommissioning();
    }
    this.#commissioningWindowExpiresAt = null;
    await this.#emitCommissioningChanged("window-closed");
  }

  async #emitCommissioningChanged(reason: CommissioningChangedEvent["reason"]): Promise<void> {
    await this.#assertContext().emitCommissioningChanged(this.#commissioningEvent(reason));
  }

  #commissioningEvent(reason: CommissioningChangedEvent["reason"]): CommissioningChangedEvent {
    const context = this.#assertContext();
    const fabricCount = this.#node?.env.get(this.#sdk.protocol.FabricManager).length ?? 0;
    return {
      targetId: context.targetId,
      timestamp: new Date().toISOString(),
      state: fabricCount === 0 ? "uncommissioned" : "commissioned",
      windowOpen: this.#commissioningWindowExpiresAt !== null,
      fabricCount,
      reason,
    };
  }

  async #emitFabricChanged(
    change: FabricChangedEvent["change"],
    fabricId?: string,
    label?: string,
  ): Promise<void> {
    const context = this.#assertContext();
    const event: FabricChangedEvent = {
      targetId: context.targetId,
      timestamp: new Date().toISOString(),
      change,
      fabricCount: this.#node?.env.get(this.#sdk.protocol.FabricManager).length ?? 0,
    };
    if (fabricId !== undefined) {
      event.fabricId = fabricId;
    }
    if (label !== undefined && label !== "") {
      event.label = label;
    }
    await context.emitFabricChanged(event);
  }

  #assertContext(): AdapterContext {
    if (this.#context === undefined) {
      throw new AdapterOperationError("not_started", "matter.js bridge driver is not started");
    }
    return this.#context;
  }

  #assertReady(): InstanceType<MatterMain["ServerNode"]> {
    this.#assertContext();
    if (this.#node === undefined) {
      throw new AdapterOperationError("not_configured", "matter.js bridge is not configured");
    }
    return this.#node;
  }
}

export function createMatterJsDriver(sdk: LoadedMatterJs): MatterProtocolAdapter {
  return new MatterJsBridgeDriver(sdk);
}

type NormalizedDeviceType =
  | "switch"
  | "outlet"
  | "light"
  | "temperature"
  | "humidity"
  | "contact"
  | "motion"
  | "occupancy"
  | "window-covering"
  | "fan"
  | "thermostat"
  | "lock"
  | "unsupported";

function normalizeDeviceType(deviceType: string): NormalizedDeviceType {
  switch (deviceType.trim().toLowerCase()) {
    case "switch":
    case "onofflight":
    case "on-off-light":
      return "switch";
    case "outlet":
    case "onoffpluginunit":
    case "on-off-plug-in-unit":
      return "outlet";
    case "light":
    case "lightbulb":
    case "dimmablelight":
    case "extendedcolorlight":
    case "extended-color-light":
      return "light";
    case "temperature":
    case "temperature-sensor":
    case "temperaturesensor":
      return "temperature";
    case "humidity":
    case "humidity-sensor":
    case "humiditysensor":
      return "humidity";
    case "contact":
    case "contact-sensor":
    case "contactsensor":
      return "contact";
    case "motion":
    case "motion-sensor":
    case "motionsensor":
      return "motion";
    case "occupancy":
    case "occupancy-sensor":
    case "occupancysensor":
      return "occupancy";
    case "window-covering":
    case "windowcovering":
      return "window-covering";
    case "fan":
      return "fan";
    case "thermostat":
      return "thermostat";
    case "lock":
    case "door-lock":
    case "doorlock":
      return "lock";
    default:
      return "unsupported";
  }
}

function stableUniqueId(targetId: string, deviceId: string): string {
  return createHash("sha256").update(targetId).update("\0").update(deviceId).digest("hex").slice(0, 32);
}

function safeId(value: string): string {
  return Buffer.from(value, "utf8").toString("base64url");
}

function booleanValue(value: JsonValue, path: string): boolean {
  if (typeof value !== "boolean") {
    throw new AdapterOperationError("invalid_attribute_value", `${path} requires a boolean`);
  }
  return value;
}

function numberValue(value: JsonValue | undefined, fallback?: number): number {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (fallback !== undefined) {
    return fallback;
  }
  throw new AdapterOperationError("invalid_attribute_value", `Matter attribute requires a finite number`);
}

function integerValue(value: JsonValue | undefined, fallback?: number): number {
  const numeric = numberValue(value, fallback);
  if (!Number.isInteger(numeric)) {
    throw new AdapterOperationError("invalid_attribute_value", `Matter attribute requires an integer`);
  }
  return numeric;
}

function percentToMatterLevel(percent: number): number {
  return Math.max(1, Math.min(254, Math.round((percent / 100) * 254)));
}

function matterLevelToPercent(level: number): number {
  return Math.max(0, Math.min(100, (level / 254) * 100));
}

function degreesToMatterHue(degrees: number): number {
  return Math.max(0, Math.min(254, Math.round((degrees / 360) * 254)));
}

function matterHueToDegrees(hue: number): number {
  return Math.max(0, Math.min(360, (hue / 254) * 360));
}

function percentToMatterSaturation(percent: number): number {
  return Math.max(0, Math.min(254, Math.round((percent / 100) * 254)));
}

function matterSaturationToPercent(saturation: number): number {
  return Math.max(0, Math.min(100, (saturation / 254) * 100));
}

function celsiusToMatterTemperature(celsius: number): number {
  return Math.max(-27_315, Math.min(32_767, Math.round(celsius * 100)));
}

function matterTemperatureToCelsius(temperature: number): number {
  return temperature / 100;
}

function nullableCelsiusToMatterTemperature(value: JsonValue): number | null {
  return value === null ? null : celsiusToMatterTemperature(numberValue(value));
}

function percentToMatterPercent100ths(percent: number): number {
  return Math.max(0, Math.min(10_000, Math.round(percent * 100)));
}

function nullablePercentToMatterPercent100ths(value: JsonValue): number | null {
  return value === null ? null : percentToMatterPercent100ths(numberValue(value));
}

function matterPercent100thsToPercent(percent100ths: number): number {
  return Math.max(0, Math.min(100, percent100ths / 100));
}

function matterPercent(value: JsonValue | undefined, fallback?: number): number {
  return Math.max(0, Math.min(100, numberValue(value, fallback)));
}

function occupancyValue(value: JsonValue): { occupied: boolean } {
  if (typeof value === "boolean") {
    return { occupied: value };
  }
  if (isJsonObject(value) && typeof value.occupied === "boolean") {
    return { occupied: value.occupied };
  }
  throw new AdapterOperationError(
    "invalid_attribute_value",
    "OccupancySensing.Occupancy requires a boolean",
  );
}

function windowOperationalStatusValue(
  value: JsonValue,
): { global: number; lift: number; tilt: number } {
  if (isJsonObject(value)) {
    const global = movementStatusValue(value.global ?? 0);
    const lift = movementStatusValue(value.lift ?? global);
    const tilt = movementStatusValue(value.tilt ?? 0);
    return { global, lift, tilt };
  }
  const status = movementStatusValue(value);
  return { global: status, lift: status, tilt: 0 };
}

function movementStatusValue(value: JsonValue): number {
  if (typeof value === "number" && Number.isInteger(value) && value >= 0 && value <= 2) {
    return value;
  }
  if (typeof value === "string") {
    switch (value.trim().toLowerCase()) {
      case "stopped":
      case "idle":
        return 0;
      case "decreasing":
      case "opening":
        return 1;
      case "increasing":
      case "closing":
        return 2;
    }
  }
  throw new AdapterOperationError(
    "invalid_attribute_value",
    "WindowCovering.OperationalStatus requires stopped, decreasing, or increasing",
  );
}

function windowSafetyStatusValue(value: JsonValue): Record<string, boolean> {
  if (typeof value === "boolean") {
    return { obstacleDetected: value };
  }
  if (isJsonObject(value)) {
    return Object.fromEntries(
      Object.entries(value)
        .filter((entry): entry is [string, boolean] => typeof entry[1] === "boolean"),
    );
  }
  throw new AdapterOperationError(
    "invalid_attribute_value",
    "WindowCovering.SafetyStatus requires a boolean safety bitmap",
  );
}

function unifiedFanModeToMatter(value: JsonValue | undefined, active: boolean): number {
  if (!active) {
    return 0;
  }
  if (value === undefined || value === null) {
    return 4;
  }
  if (typeof value === "number" && Number.isInteger(value) && value >= 0 && value <= 6) {
    return value;
  }
  if (typeof value === "string") {
    switch (value.trim().toLowerCase()) {
      case "off":
      case "inactive":
        return 0;
      case "low":
        return 1;
      case "medium":
        return 2;
      case "high":
        return 3;
      case "manual":
      case "on":
        return 4;
      case "auto":
        return 5;
      case "smart":
        return 6;
    }
  }
  throw new AdapterOperationError(
    "invalid_attribute_value",
    "FanControl.FanMode requires off, manual, auto, or a Matter fan mode",
  );
}

function matterFanModeToUnified(value: number): "off" | "manual" | "auto" {
  if (value === 0) {
    return "off";
  }
  if (value === 5 || value === 6) {
    return "auto";
  }
  if (value >= 1 && value <= 4) {
    return "manual";
  }
  throw new AdapterOperationError(
    "invalid_attribute_value",
    `unsupported Matter FanMode ${value}`,
  );
}

function fanRockSettingValue(
  value: JsonValue,
): { rockLeftRight: boolean; rockUpDown?: boolean; rockRound?: boolean } {
  if (typeof value === "boolean") {
    return { rockLeftRight: value };
  }
  if (isJsonObject(value)) {
    const result: {
      rockLeftRight: boolean;
      rockUpDown?: boolean;
      rockRound?: boolean;
    } = {
      rockLeftRight: value.rockLeftRight === true,
    };
    if (typeof value.rockUpDown === "boolean") {
      result.rockUpDown = value.rockUpDown;
    }
    if (typeof value.rockRound === "boolean") {
      result.rockRound = value.rockRound;
    }
    return result;
  }
  throw new AdapterOperationError(
    "invalid_attribute_value",
    "FanControl.RockSetting requires a boolean rocking bitmap",
  );
}

function unifiedThermostatModeToMatter(value: JsonValue): number {
  if (typeof value === "number" && Number.isInteger(value) && value >= 0 && value <= 9) {
    return value;
  }
  if (typeof value === "string") {
    switch (value.trim().toLowerCase()) {
      case "off":
        return 0;
      case "auto":
        return 1;
      case "cool":
        return 3;
      case "heat":
        return 4;
      case "emergency-heat":
        return 5;
      case "precooling":
        return 6;
      case "fan-only":
        return 7;
      case "dry":
        return 8;
      case "sleep":
        return 9;
    }
  }
  throw new AdapterOperationError(
    "invalid_attribute_value",
    "Thermostat.SystemMode requires off, auto, cool, or heat",
  );
}

function matterThermostatModeToUnified(value: number): string {
  const values: Record<number, string> = {
    0: "off",
    1: "auto",
    3: "cool",
    4: "heat",
    5: "emergency-heat",
    6: "precooling",
    7: "fan-only",
    8: "dry",
    9: "sleep",
  };
  const result = values[value];
  if (result === undefined) {
    throw new AdapterOperationError(
      "invalid_attribute_value",
      `unsupported Matter Thermostat.SystemMode ${value}`,
    );
  }
  return result;
}

function thermostatRunningStateValue(value: JsonValue): Record<string, boolean> {
  if (isJsonObject(value)) {
    return Object.fromEntries(
      Object.entries(value)
        .filter((entry): entry is [string, boolean] => typeof entry[1] === "boolean"),
    );
  }
  if (typeof value === "string") {
    switch (value.trim().toLowerCase()) {
      case "off":
      case "idle":
        return {};
      case "heating":
        return { heat: true };
      case "cooling":
        return { cool: true };
    }
  }
  if (typeof value === "number" && Number.isInteger(value) && value >= 0) {
    return {
      heat: (value & 0x1) !== 0,
      cool: (value & 0x2) !== 0,
      fan: (value & 0x4) !== 0,
      heatStage2: (value & 0x8) !== 0,
      coolStage2: (value & 0x10) !== 0,
      fanStage2: (value & 0x20) !== 0,
      fanStage3: (value & 0x40) !== 0,
    };
  }
  throw new AdapterOperationError(
    "invalid_attribute_value",
    "Thermostat.ThermostatRunningState requires off, idle, heating, or cooling",
  );
}

function temperatureDisplayModeValue(value: JsonValue): number {
  if (value === 0 || value === "celsius") {
    return 0;
  }
  if (value === 1 || value === "fahrenheit") {
    return 1;
  }
  throw new AdapterOperationError(
    "invalid_attribute_value",
    "TemperatureDisplayMode requires celsius or fahrenheit",
  );
}

function doorLockStateValue(value: JsonValue): number | null {
  if (value === null || value === "unknown") {
    return null;
  }
  if (typeof value === "number" && Number.isInteger(value) && value >= 0 && value <= 3) {
    return value;
  }
  if (typeof value === "string") {
    switch (value.trim().toLowerCase()) {
      case "jammed":
      case "not-fully-locked":
        return 0;
      case "secured":
      case "locked":
        return 1;
      case "unsecured":
      case "unlocked":
        return 2;
      case "unlatched":
        return 3;
    }
  }
  throw new AdapterOperationError(
    "invalid_attribute_value",
    "DoorLock.LockState requires secured, unsecured, jammed, or unknown",
  );
}

function doorStateValue(value: JsonValue): number | null {
  if (value === null) {
    return null;
  }
  if (typeof value === "boolean") {
    return value ? 0 : 1;
  }
  if (typeof value === "number" && Number.isInteger(value) && value >= 0 && value <= 5) {
    return value;
  }
  if (typeof value === "string") {
    switch (value.trim().toLowerCase()) {
      case "open":
        return 0;
      case "closed":
        return 1;
      case "jammed":
        return 2;
      case "forced-open":
        return 3;
      case "error":
      case "unknown":
        return 4;
      case "ajar":
        return 5;
    }
  }
  throw new AdapterOperationError(
    "invalid_attribute_value",
    "DoorLock.DoorState requires a boolean or Matter door state",
  );
}

function isJsonObject(value: JsonValue): value is { [key: string]: JsonValue } {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * These lists deliberately use the backend Go type identifiers and canonical
 * Cluster.Element paths.  The integration test compares them with the source
 * Catalog so widening the public Catalog cannot silently outrun this driver.
 */
export const MATTER_DRIVER_CATALOG_DEVICE_TYPE_CONSTANTS = [
  "Switch",
  "Outlet",
  "Lightbulb",
  "TemperatureSensor",
  "HumiditySensor",
  "ContactSensor",
  "MotionSensor",
  "OccupancySensor",
  "WindowCovering",
  "Fan",
  "Thermostat",
  "Lock",
] as const;

export const MATTER_DRIVER_ATTRIBUTE_PATHS = [
  "OnOff.OnOff",
  "LevelControl.CurrentLevel",
  "ColorControl.ColorTemperatureMireds",
  "ColorControl.CurrentHue",
  "ColorControl.CurrentSaturation",
  "TemperatureMeasurement.MeasuredValue",
  "RelativeHumidityMeasurement.MeasuredValue",
  "BooleanState.StateValue",
  "OccupancySensing.Occupancy",
  "WindowCovering.CurrentPositionLiftPercent100ths",
  "WindowCovering.TargetPositionLiftPercent100ths",
  "WindowCovering.OperationalStatus",
  "WindowCovering.SafetyStatus",
  "FanControl.FanMode",
  "FanControl.PercentSetting",
  "FanControl.RockSetting",
  "Thermostat.ThermostatRunningState",
  "Thermostat.SystemMode",
  "Thermostat.LocalTemperature",
  "Thermostat.OccupiedHeatingSetpoint",
  "Thermostat.OccupiedCoolingSetpoint",
  "ThermostatUserInterfaceConfiguration.TemperatureDisplayMode",
  "DoorLock.LockState",
  "DoorLock.DoorState",
] as const;

export const MATTER_DRIVER_COMMAND_PATHS = [
  "OnOff.On",
  "OnOff.Off",
  "LevelControl.MoveToLevel",
  "WindowCovering.GoToLiftPercentage",
  "WindowCovering.StopMotion",
  "DoorLock.LockDoor",
  "DoorLock.UnlockDoor",
] as const;
