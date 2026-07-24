import type {
  AttributeUpdate,
  BridgeConfiguration,
  CommissioningWindowResult,
  DeviceSnapshot,
  ReachabilityUpdate,
  RuntimeReplayState,
} from "../contract.js";
import {
  AdapterOperationError,
  type AdapterContext,
  type AdapterDiagnostics,
  type MatterProtocolAdapter,
} from "./adapter.js";
import { createMatterJsDriver } from "./matter-js-driver.js";
import { configureMatterJsLogging, configureMatterJsLoggingOnce } from "./matter-js-logging.js";

export interface LoadedMatterJs {
  readonly main: typeof import("@matter/main");
  readonly protocol: typeof import("@matter/main/protocol");
  readonly types: typeof import("@matter/main/types");
  readonly aggregator: typeof import("@matter/main/endpoints/aggregator");
  readonly onOffLight: typeof import("@matter/main/devices/on-off-light");
  readonly onOffPlugInUnit: typeof import("@matter/main/devices/on-off-plug-in-unit");
  readonly extendedColorLight: typeof import("@matter/main/devices/extended-color-light");
  readonly temperatureSensor: typeof import("@matter/main/devices/temperature-sensor");
  readonly humiditySensor: typeof import("@matter/main/devices/humidity-sensor");
  readonly contactSensor: typeof import("@matter/main/devices/contact-sensor");
  readonly occupancySensor: typeof import("@matter/main/devices/occupancy-sensor");
  readonly windowCoveringDevice: typeof import("@matter/main/devices/window-covering");
  readonly fanDevice: typeof import("@matter/main/devices/fan");
  readonly thermostatDevice: typeof import("@matter/main/devices/thermostat");
  readonly doorLockDevice: typeof import("@matter/main/devices/door-lock");
  readonly lightSensor: typeof import("@matter/main/devices/light-sensor");
  readonly pressureSensor: typeof import("@matter/main/devices/pressure-sensor");
  readonly waterLeakDetector: typeof import("@matter/main/devices/water-leak-detector");
  readonly smokeCoAlarm: typeof import("@matter/main/devices/smoke-co-alarm");
  readonly airQualitySensor: typeof import("@matter/main/devices/air-quality-sensor");
  readonly waterValve: typeof import("@matter/main/devices/water-valve");
  readonly pumpDevice: typeof import("@matter/main/devices/pump");
  readonly airPurifier: typeof import("@matter/main/devices/air-purifier");
  readonly speakerDevice: typeof import("@matter/main/devices/speaker");
  readonly onOff: typeof import("@matter/main/behaviors/on-off");
  readonly levelControl: typeof import("@matter/main/behaviors/level-control");
  readonly colorControl: typeof import("@matter/main/behaviors/color-control");
  readonly temperatureMeasurement: typeof import("@matter/main/behaviors/temperature-measurement");
  readonly relativeHumidityMeasurement: typeof import("@matter/main/behaviors/relative-humidity-measurement");
  readonly booleanState: typeof import("@matter/main/behaviors/boolean-state");
  readonly occupancySensing: typeof import("@matter/main/behaviors/occupancy-sensing");
  readonly windowCovering: typeof import("@matter/main/behaviors/window-covering");
  readonly fanControl: typeof import("@matter/main/behaviors/fan-control");
  readonly thermostat: typeof import("@matter/main/behaviors/thermostat");
  readonly thermostatUserInterfaceConfiguration: typeof import("@matter/main/behaviors/thermostat-user-interface-configuration");
  readonly doorLock: typeof import("@matter/main/behaviors/door-lock");
  readonly illuminanceMeasurement: typeof import("@matter/main/behaviors/illuminance-measurement");
  readonly pressureMeasurement: typeof import("@matter/main/behaviors/pressure-measurement");
  readonly smokeCoAlarmBehavior: typeof import("@matter/main/behaviors/smoke-co-alarm");
  readonly airQuality: typeof import("@matter/main/behaviors/air-quality");
  readonly pm25ConcentrationMeasurement: typeof import("@matter/main/behaviors/pm25-concentration-measurement");
  readonly pm10ConcentrationMeasurement: typeof import("@matter/main/behaviors/pm10-concentration-measurement");
  readonly totalVolatileOrganicCompoundsConcentrationMeasurement: typeof import("@matter/main/behaviors/total-volatile-organic-compounds-concentration-measurement");
  readonly carbonDioxideConcentrationMeasurement: typeof import("@matter/main/behaviors/carbon-dioxide-concentration-measurement");
  readonly carbonMonoxideConcentrationMeasurement: typeof import("@matter/main/behaviors/carbon-monoxide-concentration-measurement");
  readonly valveConfigurationAndControl: typeof import("@matter/main/behaviors/valve-configuration-and-control");
  readonly pumpConfigurationAndControl: typeof import("@matter/main/behaviors/pump-configuration-and-control");
  readonly bridgedDeviceBasicInformation: typeof import("@matter/main/behaviors/bridged-device-basic-information");
}

export type MatterJsLoader = () => Promise<LoadedMatterJs>;
export type MatterJsDriverFactory = (sdk: LoadedMatterJs) => MatterProtocolAdapter;

/**
 * Explicit SDK seam. The 0.17.6 dependency is pinned, while concrete Endpoint
 * composition remains a separate driver concern so SDK upgrades cannot leak
 * into IPC or target lifecycle code.
 */
export class MatterJsAdapter implements MatterProtocolAdapter {
  #delegate: MatterProtocolAdapter | undefined;
  readonly #driverFactory: MatterJsDriverFactory | undefined;
  readonly #loader: MatterJsLoader;

  constructor(
    driverFactory: MatterJsDriverFactory = createMatterJsDriver,
    loader: MatterJsLoader = loadPinnedMatterJs,
  ) {
    this.#driverFactory = driverFactory;
    this.#loader = loader;
  }

  async start(context: AdapterContext): Promise<void> {
    if (this.#delegate !== undefined) {
      throw new AdapterOperationError("already_started", "matter.js adapter is already started");
    }
    const sdk = await this.#loader();
    if (this.#driverFactory === undefined) {
      throw new AdapterOperationError(
        "matter_js_driver_unavailable",
        "matter.js 0.17.6 loaded, but the certified bridge driver is not wired; use --adapter fake for IPC development",
      );
    }
    const delegate = this.#driverFactory(sdk);
    await delegate.start(context);
    this.#delegate = delegate;
  }

  async stop(): Promise<void> {
    const delegate = this.#delegate;
    this.#delegate = undefined;
    await delegate?.stop();
  }

  replayState(state: RuntimeReplayState): Promise<void> {
    return this.#requireDelegate().replayState(state);
  }

  configureBridge(configuration: BridgeConfiguration): Promise<void> {
    return this.#requireDelegate().configureBridge(configuration);
  }

  replaceDevices(devices: DeviceSnapshot[]): Promise<void> {
    return this.#requireDelegate().replaceDevices(devices);
  }

  updateAttributes(updates: AttributeUpdate[]): Promise<void> {
    return this.#requireDelegate().updateAttributes(updates);
  }

  updateReachability(updates: ReachabilityUpdate[]): Promise<void> {
    return this.#requireDelegate().updateReachability(updates);
  }

  openCommissioningWindow(durationSeconds: number): Promise<CommissioningWindowResult> {
    return this.#requireDelegate().openCommissioningWindow(durationSeconds);
  }

  closeCommissioningWindow(): Promise<CommissioningWindowResult> {
    return this.#requireDelegate().closeCommissioningWindow();
  }

  removeFabric(fabricId: string): Promise<void> {
    return this.#requireDelegate().removeFabric(fabricId);
  }

  factoryReset(): Promise<void> {
    return this.#requireDelegate().factoryReset();
  }

  diagnostics(): AdapterDiagnostics {
    return this.#requireDelegate().diagnostics();
  }

  #requireDelegate(): MatterProtocolAdapter {
    if (this.#delegate === undefined) {
      throw new AdapterOperationError("not_started", "matter.js adapter is not started");
    }
    return this.#delegate;
  }
}

export async function loadPinnedMatterJs(): Promise<LoadedMatterJs> {
  // Seed quieter defaults before the Node.js platform boots, then re-apply after
  // Environment.default is installed so destination writers survive the reload.
  const general = await import("@matter/general");
  const loggingApi = {
    Logger: general.Logger as never,
    LogLevel: general.LogLevel as never,
    LogFormat: general.LogFormat as never,
    Environment: general.Environment,
  };
  configureMatterJsLoggingOnce(loggingApi);
  await import("@matter/main/platform");
  configureMatterJsLogging(loggingApi);
  const [
    main,
    protocol,
    types,
    aggregator,
    onOffLight,
    onOffPlugInUnit,
    extendedColorLight,
    temperatureSensor,
    humiditySensor,
    contactSensor,
    occupancySensor,
    windowCoveringDevice,
    fanDevice,
    thermostatDevice,
    doorLockDevice,
    lightSensor,
    pressureSensor,
    waterLeakDetector,
    smokeCoAlarm,
    airQualitySensor,
    waterValve,
    pumpDevice,
    airPurifier,
    speakerDevice,
    onOff,
    levelControl,
    colorControl,
    temperatureMeasurement,
    relativeHumidityMeasurement,
    booleanState,
    occupancySensing,
    windowCovering,
    fanControl,
    thermostat,
    thermostatUserInterfaceConfiguration,
    doorLock,
    illuminanceMeasurement,
    pressureMeasurement,
    smokeCoAlarmBehavior,
    airQuality,
    pm25ConcentrationMeasurement,
    pm10ConcentrationMeasurement,
    totalVolatileOrganicCompoundsConcentrationMeasurement,
    carbonDioxideConcentrationMeasurement,
    carbonMonoxideConcentrationMeasurement,
    valveConfigurationAndControl,
    pumpConfigurationAndControl,
    bridgedDeviceBasicInformation,
  ] = await Promise.all([
    import("@matter/main"),
    import("@matter/main/protocol"),
    import("@matter/main/types"),
    import("@matter/main/endpoints/aggregator"),
    import("@matter/main/devices/on-off-light"),
    import("@matter/main/devices/on-off-plug-in-unit"),
    import("@matter/main/devices/extended-color-light"),
    import("@matter/main/devices/temperature-sensor"),
    import("@matter/main/devices/humidity-sensor"),
    import("@matter/main/devices/contact-sensor"),
    import("@matter/main/devices/occupancy-sensor"),
    import("@matter/main/devices/window-covering"),
    import("@matter/main/devices/fan"),
    import("@matter/main/devices/thermostat"),
    import("@matter/main/devices/door-lock"),
    import("@matter/main/devices/light-sensor"),
    import("@matter/main/devices/pressure-sensor"),
    import("@matter/main/devices/water-leak-detector"),
    import("@matter/main/devices/smoke-co-alarm"),
    import("@matter/main/devices/air-quality-sensor"),
    import("@matter/main/devices/water-valve"),
    import("@matter/main/devices/pump"),
    import("@matter/main/devices/air-purifier"),
    import("@matter/main/devices/speaker"),
    import("@matter/main/behaviors/on-off"),
    import("@matter/main/behaviors/level-control"),
    import("@matter/main/behaviors/color-control"),
    import("@matter/main/behaviors/temperature-measurement"),
    import("@matter/main/behaviors/relative-humidity-measurement"),
    import("@matter/main/behaviors/boolean-state"),
    import("@matter/main/behaviors/occupancy-sensing"),
    import("@matter/main/behaviors/window-covering"),
    import("@matter/main/behaviors/fan-control"),
    import("@matter/main/behaviors/thermostat"),
    import("@matter/main/behaviors/thermostat-user-interface-configuration"),
    import("@matter/main/behaviors/door-lock"),
    import("@matter/main/behaviors/illuminance-measurement"),
    import("@matter/main/behaviors/pressure-measurement"),
    import("@matter/main/behaviors/smoke-co-alarm"),
    import("@matter/main/behaviors/air-quality"),
    import("@matter/main/behaviors/pm25-concentration-measurement"),
    import("@matter/main/behaviors/pm10-concentration-measurement"),
    import("@matter/main/behaviors/total-volatile-organic-compounds-concentration-measurement"),
    import("@matter/main/behaviors/carbon-dioxide-concentration-measurement"),
    import("@matter/main/behaviors/carbon-monoxide-concentration-measurement"),
    import("@matter/main/behaviors/valve-configuration-and-control"),
    import("@matter/main/behaviors/pump-configuration-and-control"),
    import("@matter/main/behaviors/bridged-device-basic-information"),
  ]);
  return {
    main,
    protocol,
    types,
    aggregator,
    onOffLight,
    onOffPlugInUnit,
    extendedColorLight,
    temperatureSensor,
    humiditySensor,
    contactSensor,
    occupancySensor,
    windowCoveringDevice,
    fanDevice,
    thermostatDevice,
    doorLockDevice,
    lightSensor,
    pressureSensor,
    waterLeakDetector,
    smokeCoAlarm,
    airQualitySensor,
    waterValve,
    pumpDevice,
    airPurifier,
    speakerDevice,
    onOff,
    levelControl,
    colorControl,
    temperatureMeasurement,
    relativeHumidityMeasurement,
    booleanState,
    occupancySensing,
    windowCovering,
    fanControl,
    thermostat,
    thermostatUserInterfaceConfiguration,
    doorLock,
    illuminanceMeasurement,
    pressureMeasurement,
    smokeCoAlarmBehavior,
    airQuality,
    pm25ConcentrationMeasurement,
    pm10ConcentrationMeasurement,
    totalVolatileOrganicCompoundsConcentrationMeasurement,
    carbonDioxideConcentrationMeasurement,
    carbonMonoxideConcentrationMeasurement,
    valveConfigurationAndControl,
    pumpConfigurationAndControl,
    bridgedDeviceBasicInformation,
  };
}
