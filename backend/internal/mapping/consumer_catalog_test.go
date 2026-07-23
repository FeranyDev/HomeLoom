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

func TestConsumerContractRegistryDoesNotFallBackToHomeKit(t *testing.T) {
	if _, found := ConsumerContract("matter", device.TypeSwitch); found {
		t.Fatal("unregistered Matter consumer unexpectedly resolved to HomeKit")
	}
	if _, found := FindConsumerProperty("matter", device.TypeSwitch, "Switch.On"); found {
		t.Fatal("unregistered Matter property unexpectedly resolved to HomeKit")
	}
	contract, found := ConsumerContract("homekit", device.TypeSwitch)
	if !found || contract.ConsumerID != "homekit" {
		t.Fatalf("registered HomeKit contract = %#v, found=%v", contract, found)
	}
	if known, supported := ConsumerModelSupport("homekit", device.TypeSwitch); !known || !supported {
		t.Fatalf("HomeKit switch support = known %v, supported %v", known, supported)
	}
	if known, supported := ConsumerModelSupport("homekit", device.TypeRobotVacuum); !known || supported {
		t.Fatalf("HomeKit robot vacuum support = known %v, supported %v", known, supported)
	}
	if known, supported := ConsumerModelSupport("matter", device.TypeSwitch); known || supported {
		t.Fatalf("unregistered Matter support = known %v, supported %v", known, supported)
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
