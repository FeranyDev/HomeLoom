package mapping

import (
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

func TestMeasurementSensorsKeepExplicitHomeKitSemantics(t *testing.T) {
	tests := []struct {
		deviceType   device.Type
		modelPath    device.ParameterPath
		consumerPath string
	}{
		{device.TypeTemperatureSensor, device.ParameterPath{EndpointID: "main", CapabilityID: "temperature", PropertyID: "current-temperature"}, "TemperatureSensor.CurrentTemperature"},
		{device.TypeHumiditySensor, device.ParameterPath{EndpointID: "main", CapabilityID: "humidity", PropertyID: "current-humidity"}, "HumiditySensor.CurrentRelativeHumidity"},
	}
	for _, test := range tests {
		contract, found := HomeKitConsumerContract(test.deviceType)
		if !found || len(contract.Parameters) != 3 {
			t.Fatalf("contract %q = %#v, found=%v", test.deviceType, contract, found)
		}
		if parameter := contract.Parameters[0]; parameter.ModelPath() != test.modelPath || parameter.Level != device.ParameterRequired || parameter.Target != test.consumerPath {
			t.Fatalf("measurement mapping %q = %#v", test.deviceType, parameter)
		}
		property, found := FindConsumerProperty("homekit", test.deviceType, test.consumerPath)
		if !found || property.DefaultModelPath != test.modelPath || property.Type != device.ValueTypeNumber {
			t.Fatalf("catalog measurement property %q = %#v", test.deviceType, property)
		}
	}
}

func TestHomeKitCatalogPublishesProtocolNumericConstraints(t *testing.T) {
	tests := []struct {
		deviceType device.Type
		propertyID string
		min        float64
		max        float64
		step       float64
	}{
		{device.TypeLightbulb, "Lightbulb.ColorTemperature", 140, 500, 1},
		{device.TypeTemperatureSensor, "TemperatureSensor.CurrentTemperature", 0, 100, 0.1},
		{device.TypeAirQualitySensor, "AirQualitySensor.PM2.5Density", 0, 1000, 1},
		{device.TypeThermostat, "Thermostat.HeatingThresholdTemperature", 0, 25, 0.1},
		{device.TypeValve, "Valve.SetDuration", 0, 3600, 1},
	}
	for _, test := range tests {
		property, found := FindConsumerProperty("homekit", test.deviceType, test.propertyID)
		if !found || property.Min == nil || property.Max == nil || property.Step == nil {
			t.Fatalf("HomeKit property %q constraints = %#v, found=%v", test.propertyID, property, found)
		}
		if *property.Min != test.min || *property.Max != test.max || *property.Step != test.step {
			t.Errorf("HomeKit property %q range = %v..%v step %v; want %v..%v step %v", test.propertyID, *property.Min, *property.Max, *property.Step, test.min, test.max, test.step)
		}
	}
}

func TestConsumerContractRegistryDoesNotFallBackToHomeKit(t *testing.T) {
	matter, found := ConsumerContract("matter", device.TypeSwitch)
	if !found || matter.ConsumerID != "matter" || len(matter.Parameters) != 1 || matter.Parameters[0].Target != "OnOff.OnOff" {
		t.Fatalf("registered Matter switch contract = %#v, found=%v", matter, found)
	}
	if _, found := FindConsumerProperty("matter", device.TypeSwitch, "Switch.On"); found {
		t.Fatal("Matter property unexpectedly fell back to HomeKit")
	}
	contract, found := ConsumerContract("homekit", device.TypeSwitch)
	if !found || contract.ConsumerID != "homekit" {
		t.Fatalf("registered HomeKit contract = %#v, found=%v", contract, found)
	}
	if known, supported := ConsumerModelSupport("homekit", device.TypeSwitch); !known || !supported {
		t.Fatalf("HomeKit switch support = known %v, supported %v", known, supported)
	}
	if property, found := FindConsumerProperty("homekit", device.TypeNetworkDevice, "Switch.On"); !found || property.DefaultModelPath != (device.ParameterPath{EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}) || !property.Writable {
		t.Fatalf("HomeKit network power mapping = %#v, found=%v", property, found)
	}
	if command, found := FindConsumerProperty("matter", device.TypeNetworkDevice, "OnOff.On"); !found || command.DefaultModelPath != (device.ParameterPath{EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}) {
		t.Fatalf("Matter network wake command = %#v, found=%v", command, found)
	}
	if known, supported := ConsumerModelSupport("homekit", device.TypeRobotVacuum); !known || supported {
		t.Fatalf("HomeKit robot vacuum support = known %v, supported %v", known, supported)
	}
	if known, supported := ConsumerModelSupport("matter", device.TypeSwitch); !known || !supported {
		t.Fatalf("Matter switch support = known %v, supported %v", known, supported)
	}
	if known, supported := ConsumerModelSupport("matter", device.TypeRobotVacuum); !known || supported {
		t.Fatalf("unsupported Matter model = known %v, supported %v", known, supported)
	}
	if known, supported := ConsumerModelSupport("matter", device.TypeTemperatureHumiditySensor); !known || supported {
		t.Fatalf("non-standard Matter aggregate sensor = known %v, supported %v", known, supported)
	}
}

func TestMatterCatalogDeclaresClusterMetadataAndModelConstraints(t *testing.T) {
	property, found := FindConsumerProperty("matter", device.TypeTemperatureSensor, "TemperatureMeasurement.MeasuredValue")
	if !found {
		t.Fatal("Matter temperature attribute is missing")
	}
	if property.Name != "当前温度" || property.OriginalName != "TemperatureMeasurement.MeasuredValue" {
		t.Fatalf("Matter temperature labels = %#v", property)
	}
	if property.Cluster != "TemperatureMeasurement" || property.Element != "MeasuredValue" || property.Kind != "attribute" {
		t.Fatalf("Matter temperature path metadata = %#v", property)
	}
	if property.Type != device.ValueTypeNumber || property.Unit != "celsius" || property.Min == nil || property.Max == nil || property.Step == nil {
		t.Fatalf("Matter temperature constraints = %#v", property)
	}
}

func TestMatterCatalogPublishesCommandsSeparatelyFromAttributes(t *testing.T) {
	command, found := FindConsumerProperty("matter", device.TypeLock, "DoorLock.UnlockDoor")
	if !found {
		t.Fatal("Matter DoorLock.UnlockDoor command is missing")
	}
	if command.Kind != "command" || command.Cluster != "DoorLock" || command.Element != "UnlockDoor" {
		t.Fatalf("Matter command metadata = %#v", command)
	}
	if command.Readable || !command.Writable || command.Notifiable {
		t.Fatalf("Matter command directions = %#v", command)
	}
	attribute, found := FindConsumerProperty("matter", device.TypeLock, "DoorLock.LockState")
	if !found || attribute.Kind != "attribute" || !attribute.Readable || !attribute.Notifiable {
		t.Fatalf("Matter lock attribute = %#v, found=%v", attribute, found)
	}
	for _, path := range []string{"MediaPlayback.Play", "MediaPlayback.Pause", "MediaPlayback.Stop", "KeypadInput.SendKey"} {
		command, found := FindConsumerProperty("matter", device.TypeTelevision, path)
		if !found || command.Kind != "command" || !command.Writable || command.Readable || command.Notifiable {
			t.Fatalf("Matter television command %q = %#v, found=%v", path, command, found)
		}
	}
	keyCommand, found := FindConsumerProperty("matter", device.TypeTelevision, "KeypadInput.SendKey")
	if !found || len(keyCommand.Enum) == 0 || !containsString(keyCommand.Enum, "volume-up") || !containsString(keyCommand.Enum, "volume-down") {
		t.Fatalf("Matter television volume keys = %#v, found=%v", keyCommand, found)
	}
}
func TestMatterFirstDeviceBatchIsExplicitlySupported(t *testing.T) {
	supported := []device.Type{
		device.TypeSwitch, device.TypeOutlet, device.TypeLightbulb,
		device.TypeTemperatureSensor, device.TypeHumiditySensor,
		device.TypeNetworkDevice,
		device.TypeContactSensor, device.TypeMotionSensor, device.TypeOccupancySensor,
		device.TypeWindowCovering, device.TypeFan, device.TypeThermostat, device.TypeLock,
		device.TypeIlluminanceSensor, device.TypePressureSensor, device.TypeLeakSensor,
		device.TypeSmokeSensor, device.TypeCarbonMonoxideSensor, device.TypeAirQualitySensor,
		device.TypeValve, device.TypePump, device.TypeAirPurifier, device.TypeSpeaker, device.TypeTelevision,
	}
	for _, deviceType := range supported {
		known, ok := ConsumerModelSupport("matter", deviceType)
		if !known || !ok {
			t.Errorf("Matter support for %q = known %v, supported %v", deviceType, known, ok)
		}
	}
}

func TestMatterCatalogUsesWritableAndSemanticallyMatchingAttributes(t *testing.T) {
	fan, found := ConsumerContract("matter", device.TypeFan)
	if !found {
		t.Fatal("Matter fan contract is missing")
	}
	for _, parameter := range fan.Parameters {
		if parameter.Target == "FanControl.FanMode" && parameter.Source.PropertyID != "target-state" {
			t.Fatalf("FanMode must route to writable target-state, got %#v", parameter)
		}
	}
	thermostat, found := ConsumerContract("matter", device.TypeThermostat)
	if !found {
		t.Fatal("Matter thermostat contract is missing")
	}
	want := map[string]string{
		"current-state": "Thermostat.ThermostatRunningState",
		"display-units": "ThermostatUserInterfaceConfiguration.TemperatureDisplayMode",
	}
	for _, parameter := range thermostat.Parameters {
		if target, exists := want[parameter.Source.PropertyID]; exists {
			if parameter.Target != target {
				t.Fatalf("thermostat %s = %s; want %s", parameter.Source.PropertyID, parameter.Target, target)
			}
			delete(want, parameter.Source.PropertyID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing thermostat mappings: %#v", want)
	}
	lock, found := ConsumerContract("matter", device.TypeLock)
	if !found {
		t.Fatal("Matter lock contract is missing")
	}
	for _, parameter := range lock.Parameters {
		if parameter.Source.PropertyID == "target-state" || parameter.Source.PropertyID == "last-operation" {
			t.Fatalf("read-only DoorLock attributes must not masquerade as %s: %#v", parameter.Source.PropertyID, parameter)
		}
	}
}

func TestAirConditionerOffersHomeKitHeaterCoolerSemantics(t *testing.T) {
	contract, found := HomeKitConsumerContract(device.TypeAirConditioner)
	if !found {
		t.Fatal("HomeKit air-conditioner contract is missing")
	}
	want := map[string]device.ParameterLevel{
		"HeaterCooler.Active":                      device.ParameterRequired,
		"HeaterCooler.CurrentHeaterCoolerState":    device.ParameterOptional,
		"HeaterCooler.TargetHeaterCoolerState":     device.ParameterRequired,
		"HeaterCooler.CurrentTemperature":          device.ParameterOptional,
		"HeaterCooler.TargetTemperature":           device.ParameterRequired,
		"HeaterCooler.RotationSpeed":               device.ParameterOptional,
		"HeaterCooler.SwingMode":                   device.ParameterOptional,
		"HumiditySensor.CurrentRelativeHumidity":   device.ParameterOptional,
		"HeaterCooler.TemperatureDisplayUnits":     device.ParameterOptional,
		"FilterMaintenance.FilterLifeLevel":        device.ParameterOptional,
		"FilterMaintenance.FilterChangeIndication": device.ParameterOptional,
	}
	if len(contract.Parameters) != len(want) {
		t.Fatalf("air-conditioner parameters = %#v", contract.Parameters)
	}
	for _, parameter := range contract.Parameters {
		if level, ok := want[parameter.Target]; !ok || level != parameter.Level {
			t.Fatalf("unexpected air-conditioner mapping = %#v", parameter)
		}
	}
	if known, supported := ConsumerModelSupport("homekit", device.TypeAirConditioner); !known || !supported {
		t.Fatalf("HomeKit air-conditioner support = known %v, supported %v", known, supported)
	}
}

func TestHomeKitSupportsEveryNativeUnifiedModel(t *testing.T) {
	unsupported := map[device.Type]bool{
		// Cameras are published by the isolated Media Worker and are not a
		// regular accessory on the existing HomeKit bridge target.
		device.TypeCamera:         true,
		device.TypePressureSensor: true, device.TypeNoiseSensor: true,
		device.TypeWaterLevelSensor: true, device.TypeSoilMoistureSensor: true,
		device.TypePump: true, device.TypeWaterHeater: true, device.TypePowerMeter: true,
		device.TypeEVCharger: true, device.TypeRobotVacuum: true,
	}
	contracts := HomeKitConsumerContracts()
	if len(contracts) != len(device.ModelContracts())-len(unsupported) {
		t.Fatalf("HomeKit contracts = %d, models = %d", len(contracts), len(device.ModelContracts()))
	}
	for _, model := range device.ModelContracts() {
		_, found := HomeKitConsumerContract(model.DeviceType)
		if found == unsupported[model.DeviceType] {
			t.Errorf("HomeKit support for %q = %v, unsupported = %v", model.DeviceType, found, unsupported[model.DeviceType])
		}
	}
}

func TestAirPurifierHomeKitContractIsOptionalBeyondActiveAndCurrentState(t *testing.T) {
	contract, found := HomeKitConsumerContract(device.TypeAirPurifier)
	if !found {
		t.Fatal("HomeKit air-purifier contract is missing")
	}
	want := map[string]device.ParameterLevel{
		"AirPurifier.Active":                       device.ParameterRequired,
		"AirPurifier.CurrentAirPurifierState":      device.ParameterRequired,
		"AirPurifier.TargetAirPurifierState":       device.ParameterOptional,
		"AirPurifier.RotationSpeed":                device.ParameterOptional,
		"AirPurifier.SwingMode":                    device.ParameterOptional,
		"AirPurifier.LockPhysicalControls":         device.ParameterOptional,
		"AirQualitySensor.AirQuality":              device.ParameterOptional,
		"AirQualitySensor.PM2.5Density":            device.ParameterOptional,
		"AirQualitySensor.VOCDensity":              device.ParameterOptional,
		"FilterMaintenance.FilterLifeLevel":        device.ParameterOptional,
		"FilterMaintenance.FilterChangeIndication": device.ParameterOptional,
	}
	if len(contract.Parameters) != len(want) {
		t.Fatalf("air-purifier parameters = %#v", contract.Parameters)
	}
	for _, parameter := range contract.Parameters {
		if level, ok := want[parameter.Target]; !ok || level != parameter.Level {
			t.Fatalf("unexpected air-purifier mapping = %#v", parameter)
		}
	}
}
