import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import type {
  AttributeWriteEvent,
  CommandEvent,
  DeviceSnapshot,
} from "../src/contract.js";
import { MemoryHostStorage } from "../src/ipc/reconnecting-client.js";
import type { AdapterContext } from "../src/runtime/adapter.js";
import { loadPinnedMatterJs } from "../src/runtime/matter-js-adapter.js";
import {
  MATTER_DRIVER_ATTRIBUTE_PATHS,
  MATTER_DRIVER_CATALOG_DEVICE_TYPE_CONSTANTS,
  MATTER_DRIVER_COMMAND_PATHS,
  MatterJsBridgeDriver,
} from "../src/runtime/matter-js-driver.js";
import { bridgeConfiguration } from "./helpers.js";

test("every backend Matter Catalog type and path is implemented by the driver", async () => {
  const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");
  const catalog = await readFile(
    resolve(repositoryRoot, "backend/internal/mapping/matter_consumer_catalog.go"),
    "utf8",
  );

  const deviceTypes = matches(catalog, /DeviceType:\s*device\.Type([A-Za-z0-9]+)/g);
  const attributePaths = matches(
    catalog,
    /\b(?:required|optional)\("[^"]+",\s*"[^"]+",\s*"([^"]+)"\)/g,
  );
  const commandPaths = matches(
    catalog,
    /\{device\.Type[A-Za-z0-9]+,\s*path\([^)]*\),\s*"([^"]+)"/g,
  );

  assert.deepEqual(
    [...deviceTypes].sort(),
    [...MATTER_DRIVER_CATALOG_DEVICE_TYPE_CONSTANTS].sort(),
  );
  assert.deepEqual(
    [...attributePaths].sort(),
    [...MATTER_DRIVER_ATTRIBUTE_PATHS].sort(),
  );
  assert.deepEqual(
    [...commandPaths].sort(),
    [...MATTER_DRIVER_COMMAND_PATHS].sort(),
  );

  const contracts = [...catalog.matchAll(
    /\{ConsumerID:\s*"matter",\s*DeviceType:\s*device\.Type([A-Za-z0-9]+),\s*Parameters:\s*\[\]device\.ConsumerParameterMapping\{([\s\S]*?)\n\t\t\}\},/g,
  )];
  assert.equal(contracts.length, deviceTypes.size);
  for (const contract of contracts) {
    assert.ok(contract[1] && contract[2]);
    const targets = [
      ...contract[2].matchAll(
        /\b(?:required|optional)\("[^"]+",\s*"[^"]+",\s*"([^"]+)"\)/g,
      ),
    ].map((match) => match[1]);
    assert.equal(
      new Set(targets).size,
      targets.length,
      `Matter ${contract[1]} maps multiple model properties to one target path`,
    );
  }
});

test("all first-batch Catalog types construct official endpoints and bridge state", async (testContext) => {
  const sdk = await loadPinnedMatterJs();
  const commands: CommandEvent[] = [];
  const writes: AttributeWriteEvent[] = [];
  const context: AdapterContext = {
    targetId: "matter-first-batch",
    identityNamespace: "matter-first-batch",
    storage: new MemoryHostStorage(),
    async emitCommand(event) {
      commands.push(event);
      return { accepted: true };
    },
    async emitAttributeWrite(event) {
      writes.push(event);
      return { accepted: true };
    },
    async emitCommissioningChanged() {},
    async emitFabricChanged() {},
    async emitDiagnostics() {},
  };
  const configuration = bridgeConfiguration("matter-first-batch");
  delete configuration.networkInterface;
  const driver = new MatterJsBridgeDriver(sdk, { startNetwork: false });
  testContext.after(async () => {
    await driver.stop();
    await sdk.main.Environment.default
      .maybeGet(sdk.protocol.MdnsService)
      ?.close();
  });

  await driver.start(context);
  await driver.replayState({
    revision: 1,
    bridge: configuration,
    devices: firstBatchDevices(),
  });

  assert.equal(driver.diagnostics().deviceCount, 13);
  for (const device of firstBatchDevices()) {
    const deviceTypeList = await driver.readAttributeForTest(
      device.id,
      "descriptor",
      "deviceTypeList",
    );
    assert.ok(
      Array.isArray(deviceTypeList) && deviceTypeList.length > 0,
      `${device.deviceType} did not construct an official descriptor`,
    );
  }

  assert.equal(
    await driver.readAttributeForTest(
      "humidity",
      "relativeHumidityMeasurement",
      "measuredValue",
    ),
    4_250,
  );
  assert.equal(
    await driver.readAttributeForTest(
      "temperature-humidity",
      "temperatureMeasurement",
      "measuredValue",
    ),
    2_125,
  );
  assert.equal(
    await driver.readAttributeForTest(
      "temperature-humidity",
      "relativeHumidityMeasurement",
      "measuredValue",
    ),
    5_550,
  );
  assert.equal(
    await driver.readAttributeForTest("contact", "booleanState", "stateValue"),
    true,
  );
  const motionOccupancy = await driver.readAttributeForTest(
    "motion",
    "occupancySensing",
    "occupancy",
  ) as { occupied?: boolean };
  const roomOccupancy = await driver.readAttributeForTest(
    "occupancy",
    "occupancySensing",
    "occupancy",
  ) as { occupied?: boolean };
  assert.equal(motionOccupancy.occupied, true);
  assert.equal(roomOccupancy.occupied, false);
  assert.equal(
    await driver.readAttributeForTest(
      "window-covering",
      "windowCovering",
      "currentPositionLiftPercent100ths",
    ),
    2_500,
  );
  assert.equal(
    await driver.readAttributeForTest(
      "window-covering",
      "windowCovering",
      "targetPositionLiftPercent100ths",
    ),
    7_500,
  );
  const operationalStatus = await driver.readAttributeForTest(
    "window-covering",
    "windowCovering",
    "operationalStatus",
  ) as { global?: number; lift?: number; tilt?: number };
  assert.equal(operationalStatus.global, 2);
  assert.equal(operationalStatus.lift, 2);
  assert.equal(operationalStatus.tilt, 0);
  assert.equal(
    await driver.readAttributeForTest("fan", "fanControl", "fanMode"),
    5,
  );
  assert.equal(
    await driver.readAttributeForTest("fan", "fanControl", "percentSetting"),
    null,
  );
  assert.equal(
    await driver.readAttributeForTest("thermostat", "thermostat", "localTemperature"),
    2_050,
  );
  assert.equal(
    await driver.readAttributeForTest("thermostat", "thermostat", "systemMode"),
    4,
  );
  assert.equal(
    await driver.readAttributeForTest(
      "thermostat",
      "thermostatUserInterfaceConfiguration",
      "temperatureDisplayMode",
    ),
    1,
  );
  assert.equal(
    await driver.readAttributeForTest("lock", "doorLock", "lockState"),
    1,
  );
  assert.equal(
    await driver.readAttributeForTest("lock", "doorLock", "doorState"),
    0,
  );

  await driver.invokeCommandForTest("fan", "onOff", "off");
  await driver.invokeCommandForTest(
    "window-covering",
    "windowCovering",
    "goToLiftPercentage",
    { liftPercent100thsValue: 4_250 },
  );
  await driver.invokeCommandForTest(
    "window-covering",
    "windowCovering",
    "stopMotion",
  );
  await driver.invokeCommandForTest("lock", "doorLock", "lockDoor", {
    pinCode: null,
  });
  await driver.invokeCommandForTest("lock", "doorLock", "unlockDoor", {
    pinCode: null,
  });
  assert.deepEqual(
    commands.map(({ path }) => path),
    [
      "OnOff.Off",
      "WindowCovering.GoToLiftPercentage",
      "WindowCovering.StopMotion",
      "DoorLock.LockDoor",
      "DoorLock.UnlockDoor",
    ],
  );
  assert.equal(commands[1]?.fields.liftPercent100ths, 42.5);

  await driver.writeAttributeForTest("fan", "FanControl.FanMode", 3);
  await driver.writeAttributeForTest("fan", "FanControl.PercentSetting", 42);
  await driver.writeAttributeForTest("fan", "FanControl.RockSetting", {
    rockLeftRight: false,
  });
  await driver.writeAttributeForTest("thermostat", "Thermostat.SystemMode", 3);
  await driver.writeAttributeForTest(
    "thermostat",
    "Thermostat.OccupiedHeatingSetpoint",
    2_150,
  );
  await driver.writeAttributeForTest(
    "thermostat",
    "Thermostat.OccupiedCoolingSetpoint",
    2_450,
  );
  await driver.writeAttributeForTest(
    "thermostat",
    "ThermostatUserInterfaceConfiguration.TemperatureDisplayMode",
    0,
  );
  assert.deepEqual(
    writes.map(({ path, value }) => [path, value]),
    [
      ["FanControl.FanMode", "manual"],
      ["FanControl.PercentSetting", 42],
      ["FanControl.RockSetting", false],
      ["Thermostat.SystemMode", "cool"],
      ["Thermostat.OccupiedHeatingSetpoint", 21.5],
      ["Thermostat.OccupiedCoolingSetpoint", 24.5],
      [
        "ThermostatUserInterfaceConfiguration.TemperatureDisplayMode",
        "celsius",
      ],
    ],
  );

  await driver.updateAttributes([
    { deviceId: "fan", path: "FanControl.PercentSetting", value: 33 },
    { deviceId: "thermostat", path: "Thermostat.SystemMode", value: "auto" },
  ]);
  assert.equal(writes.length, 7, "host replay must not loop back as controller writes");

  await driver.updateReachability([
    { deviceId: "humidity", reachable: false },
    { deviceId: "lock", reachable: false },
  ]);
  assert.equal(
    await driver.readAttributeForTest(
      "humidity",
      "bridgedDeviceBasicInformation",
      "reachable",
    ),
    false,
  );
  assert.equal(
    await driver.readAttributeForTest(
      "lock",
      "bridgedDeviceBasicInformation",
      "reachable",
    ),
    false,
  );
});

function matches(source: string, pattern: RegExp): Set<string> {
  return new Set([...source.matchAll(pattern)].map((match) => {
    assert.ok(match[1]);
    return match[1];
  }));
}

function firstBatchDevices(): DeviceSnapshot[] {
  return [
    snapshot("switch", 2, "switch", {
      "OnOff.OnOff": false,
    }),
    snapshot("outlet", 3, "outlet", {
      "OnOff.OnOff": true,
    }),
    snapshot("light", 4, "lightbulb", {
      "OnOff.OnOff": true,
      "LevelControl.CurrentLevel": 50,
      "ColorControl.ColorTemperatureMireds": 370,
      "ColorControl.CurrentHue": 180,
      "ColorControl.CurrentSaturation": 25,
    }),
    snapshot("temperature", 5, "temperature-sensor", {
      "TemperatureMeasurement.MeasuredValue": 23.5,
    }),
    snapshot("humidity", 6, "humidity-sensor", {
      "RelativeHumidityMeasurement.MeasuredValue": 42.5,
    }),
    snapshot("temperature-humidity", 7, "temperature-humidity-sensor", {
      "TemperatureMeasurement.MeasuredValue": 21.25,
      "RelativeHumidityMeasurement.MeasuredValue": 55.5,
    }),
    snapshot("contact", 8, "contact-sensor", {
      "BooleanState.StateValue": true,
    }),
    snapshot("motion", 9, "motion-sensor", {
      "OccupancySensing.Occupancy": true,
    }),
    snapshot("occupancy", 10, "occupancy-sensor", {
      "OccupancySensing.Occupancy": false,
    }),
    snapshot("window-covering", 11, "window-covering", {
      "WindowCovering.CurrentPositionLiftPercent100ths": 25,
      "WindowCovering.TargetPositionLiftPercent100ths": 75,
      "WindowCovering.OperationalStatus": "increasing",
      "WindowCovering.SafetyStatus": true,
    }),
    snapshot("fan", 12, "fan", {
      "OnOff.OnOff": true,
      "FanControl.FanMode": "auto",
      "FanControl.PercentSetting": 63,
      "FanControl.RockSetting": true,
    }),
    snapshot("thermostat", 13, "thermostat", {
      "Thermostat.ThermostatRunningState": "heating",
      "Thermostat.SystemMode": "heat",
      "Thermostat.LocalTemperature": 20.5,
      "Thermostat.OccupiedHeatingSetpoint": 22,
      "Thermostat.OccupiedCoolingSetpoint": 25,
      "ThermostatUserInterfaceConfiguration.TemperatureDisplayMode": "fahrenheit",
    }),
    snapshot("lock", 14, "lock", {
      "DoorLock.LockState": "secured",
      "DoorLock.DoorState": true,
    }),
  ];
}

function snapshot(
  id: string,
  endpointId: number,
  deviceType: string,
  attributes: DeviceSnapshot["attributes"],
): DeviceSnapshot {
  return {
    id,
    endpointId,
    deviceType,
    name: `First batch ${id}`,
    reachable: true,
    attributes,
  };
}
