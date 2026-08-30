import assert from "node:assert/strict";
import { test } from "node:test";
import type {
  AttributeWriteEvent,
  CommandEvent,
  CommissioningChangedEvent,
  DeviceSnapshot,
  FabricChangedEvent,
} from "../src/contract.js";
import { MemoryHostStorage } from "../src/ipc/reconnecting-client.js";
import type { AdapterContext } from "../src/runtime/adapter.js";
import { loadPinnedMatterJs } from "../src/runtime/matter-js-adapter.js";
import { MatterJsBridgeDriver } from "../src/runtime/matter-js-driver.js";
import { bridgeConfiguration, switchDevice } from "./helpers.js";

test("official matter.js endpoints bridge commands, attributes, and host-owned identity", async (testContext) => {
  const sdk = await loadPinnedMatterJs();
  const storage = new MemoryHostStorage();
  const commands: CommandEvent[] = [];
  const writes: AttributeWriteEvent[] = [];
  const commissioningEvents: CommissioningChangedEvent[] = [];
  const fabricEvents: FabricChangedEvent[] = [];
  const context: AdapterContext = {
    targetId: "matter-real",
    identityNamespace: "identity-real",
    storage,
    async emitCommand(event) {
      commands.push(event);
      return { accepted: true };
    },
    async emitAttributeWrite(event) {
      writes.push(event);
      return { accepted: true };
    },
    async emitCommissioningChanged(event) {
      commissioningEvents.push(event);
    },
    async emitFabricChanged(event) {
      fabricEvents.push(event);
    },
    async emitDiagnostics() {},
  };
  const configuration = bridgeConfiguration("matter-real", {
    identityNamespace: "identity-real",
    commissioningWindowSeconds: 1_200,
  });
  delete configuration.networkInterface;
  const devices = realDevices();
  let driver = new MatterJsBridgeDriver(sdk, { startNetwork: false });
  testContext.after(async () => {
    await driver.stop();
    // ServerNode.erase() reconstructs the node in place and matter.js 0.17.7
    // retains the previous shared mDNS reference. This test owns the only node
    // in its process, so close that official shared service after assertions.
    await sdk.main.Environment.default
      .maybeGet(sdk.protocol.MdnsService)
      ?.close();
  });

  await driver.start(context);
  await driver.replayState({
    revision: 1,
    bridge: configuration,
    devices,
  });

  const initial = driver.diagnostics();
  assert.equal(initial.configured, true);
  assert.equal(initial.deviceCount, 4);
  assert.equal(initial.fabricCount, 0);
  assert.deepEqual(initial.fabrics, []);
  assert.equal(initial.commissioningWindowOpen, true);
  assert.match(initial.manualPairingCode ?? "", /^\d{11}$/);
  assert.match(initial.qrPairingCode ?? "", /^MT:/);
  assert.equal(await driver.readAttributeForTest("switch", "onOff", "onOff"), false);
  assert.equal(await driver.readAttributeForTest("light", "levelControl", "currentLevel"), 127);
  assert.equal(await driver.readAttributeForTest("light", "colorControl", "colorTempPhysicalMinMireds"), 154);
  assert.equal(await driver.readAttributeForTest("light", "colorControl", "colorTempPhysicalMaxMireds"), 370);
  assert.equal(await driver.readAttributeForTest("thermostat", "thermostat", "absMinHeatSetpointLimit"), 1_800);
  assert.equal(await driver.readAttributeForTest("thermostat", "thermostat", "absMaxCoolSetpointLimit"), 3_000);
  assert.equal(
    await driver.readAttributeForTest("temperature", "temperatureMeasurement", "measuredValue"),
    2_350,
  );

  await driver.invokeCommandForTest("switch", "onOff", "on");
  assert.equal(commands.length, 1);
  assert.equal(commands[0]?.path, "OnOff.On");
  assert.equal(commands[0]?.deviceId, "switch");
  assert.equal(await driver.readAttributeForTest("switch", "onOff", "onOff"), true);

  await driver.invokeCommandForTest("light", "colorControl", "moveToHue", {
    hue: 127,
    direction: 0,
    transitionTime: 0,
    optionsMask: {},
    optionsOverride: {},
  });
  assert.equal(writes.length, 1);
  assert.equal(writes[0]?.path, "ColorControl.CurrentHue");
  assert.equal(writes[0]?.value, 180);

  await driver.updateAttributes([
    {
      deviceId: "light",
      path: "LevelControl.CurrentLevel",
      value: 25,
    },
    {
      deviceId: "temperature",
      path: "TemperatureMeasurement.MeasuredValue",
      value: -4.25,
    },
  ]);
  await driver.updateReachability([{ deviceId: "switch", reachable: false }]);
  assert.equal(await driver.readAttributeForTest("light", "levelControl", "currentLevel"), 64);
  assert.equal(
    await driver.readAttributeForTest("temperature", "temperatureMeasurement", "measuredValue"),
    -425,
  );
  assert.equal(
    await driver.readAttributeForTest(
      "switch",
      "bridgedDeviceBasicInformation",
      "reachable",
    ),
    false,
  );

  const firstIdentity = await storedUniqueId(storage);
  assert.ok(firstIdentity.length > 0);
  assert.ok((await storage.list()).every(({ key }) =>
    key === "runtime/identity-generation" || key.startsWith("matter-js/")));

  await driver.stop();
  driver = new MatterJsBridgeDriver(sdk, { startNetwork: false });
  await driver.start(context);
  await driver.replayState({
    revision: 2,
    bridge: configuration,
    devices,
  });
  assert.equal(await storedUniqueId(storage), firstIdentity);
  assert.equal(driver.diagnostics().manualPairingCode, initial.manualPairingCode);

  await driver.updateAttributes([{
    deviceId: "temperature",
    path: "TemperatureMeasurement.MeasuredValue",
    value: 18.75,
  }]);
  await driver.factoryReset();
  assert.equal(driver.diagnostics().identityGeneration, 1);
  assert.equal(driver.diagnostics().fabricCount, 0);
  assert.equal(
    Buffer.from((await storage.get("runtime/identity-generation")) ?? []).toString("utf8"),
    "1",
  );
  assert.notEqual(await storedUniqueId(storage), firstIdentity);
  assert.equal(
    await driver.readAttributeForTest("temperature", "temperatureMeasurement", "measuredValue"),
    1_875,
  );
  assert.ok(fabricEvents.some(({ change }) => change === "reset"));
  assert.ok(commissioningEvents.some(({ reason }) => reason === "factory-reset"));
});

function realDevices(): DeviceSnapshot[] {
  return [
    switchDevice("switch", 2),
    {
      id: "light",
      endpointId: 3,
      deviceType: "ExtendedColorLight",
      name: "Living room light",
      reachable: true,
      attributes: {
        "OnOff.OnOff": true,
        "LevelControl.CurrentLevel": 50,
        "ColorControl.CurrentHue": 180,
        "ColorControl.CurrentSaturation": 25,
        "ColorControl.ColorTemperatureMireds": 370,
      },
      constraints: {
        "ColorControl.ColorTemperatureMireds": { min: 154, max: 370, step: 5 },
      },
    },
    {
      id: "temperature",
      endpointId: 4,
      deviceType: "TemperatureSensor",
      name: "Living room temperature",
      reachable: true,
      attributes: {
        "TemperatureMeasurement.MeasuredValue": 23.5,
      },
    },
    {
      id: "thermostat",
      endpointId: 5,
      deviceType: "Thermostat",
      name: "Living room thermostat",
      reachable: true,
      attributes: {
        "Thermostat.ThermostatRunningState": "heating",
        "Thermostat.SystemMode": "heat",
        "Thermostat.LocalTemperature": 20.5,
        "Thermostat.OccupiedHeatingSetpoint": 21,
        "Thermostat.OccupiedCoolingSetpoint": 25,
      },
      constraints: {
        "Thermostat.OccupiedHeatingSetpoint": { min: 18, max: 30, step: 1 },
        "Thermostat.OccupiedCoolingSetpoint": { min: 18, max: 30, step: 1 },
      },
    },
  ];
}

async function storedUniqueId(storage: MemoryHostStorage): Promise<string> {
  const suffix = `/${Buffer.from("uniqueId", "utf8").toString("base64url")}`;
  const entry = (await storage.list()).find(({ key }) => key.endsWith(suffix));
  assert.ok(entry, "matter.js basic-information uniqueId was not persisted through HostStorage");
  return entry.valueBase64;
}
