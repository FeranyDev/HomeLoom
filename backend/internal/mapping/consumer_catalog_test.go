package mapping

import (
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

func TestSinglePropertySensorOffersSelectableHomeKitSemantics(t *testing.T) {
	contract, found := HomeKitConsumerContract(device.TypeSinglePropertySensor)
	if !found || len(contract.Parameters) != 4 {
		t.Fatalf("contract = %#v, found=%v", contract, found)
	}
	wantModelPath := device.ParameterPath{EndpointID: "main", CapabilityID: "sensor", PropertyID: "value"}
	wantTargets := map[string]bool{
		"TemperatureSensor.CurrentTemperature":   false,
		"HumiditySensor.CurrentRelativeHumidity": false,
	}
	for _, parameter := range contract.Parameters[:2] {
		if parameter.ModelPath() != wantModelPath || parameter.Level != device.ParameterOptional {
			t.Fatalf("parameter = %#v", parameter)
		}
		if _, exists := wantTargets[parameter.Target]; !exists {
			t.Fatalf("unexpected target %q", parameter.Target)
		}
		wantTargets[parameter.Target] = true
	}
	if contract.Parameters[2].Source != (device.ParameterPath{EndpointID: "main", CapabilityID: "battery", PropertyID: "level"}) || contract.Parameters[2].Target != "BatteryService.BatteryLevel" || contract.Parameters[2].Level != device.ParameterOptional {
		t.Fatalf("battery level mapping = %#v", contract.Parameters[2])
	}
	if contract.Parameters[3].Source != (device.ParameterPath{EndpointID: "main", CapabilityID: "battery", PropertyID: "low"}) || contract.Parameters[3].Target != "BatteryService.StatusLowBattery" || contract.Parameters[3].Level != device.ParameterOptional {
		t.Fatalf("low battery mapping = %#v", contract.Parameters[3])
	}
	for target, found := range wantTargets {
		if !found {
			t.Fatalf("missing target %q", target)
		}
	}

	catalog := BuiltInConsumerCatalogs()
	var matched int
	for _, property := range catalog[0].Properties {
		if property.DeviceType == device.TypeSinglePropertySensor {
			matched++
			if property.ID == "TemperatureSensor.CurrentTemperature" || property.ID == "HumiditySensor.CurrentRelativeHumidity" {
				if property.DefaultModelPath != wantModelPath || property.Type != device.ValueTypeNumber {
					t.Fatalf("catalog sensor property = %#v", property)
				}
			}
		}
	}
	if matched != 4 {
		t.Fatalf("single sensor consumer properties = %d", matched)
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
	unsupported := map[device.Type]bool{device.TypeRobotVacuum: true}
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
