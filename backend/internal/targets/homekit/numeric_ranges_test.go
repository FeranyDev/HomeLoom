package homekit

import (
	"io"
	"log/slog"
	"testing"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func TestConfigureNumericCharacteristicRangeIntersectsAndUsesStricterStep(t *testing.T) {
	current := characteristic.NewTargetTemperature()
	minimum, maximum, step := 18.0, 30.0, 0.5
	if !configureNumericCharacteristicRange(current.C, device.PropertyDefinition{
		Type: device.ValueTypeNumber, Min: &minimum, Max: &maximum, Step: &step,
	}) {
		t.Fatal("overlapping numeric range was rejected")
	}
	if current.MinValue() != 18 || current.MaxValue() != 30 || current.StepValue() != 0.5 {
		t.Fatalf("effective numeric range = %v..%v step %v", current.MinValue(), current.MaxValue(), current.StepValue())
	}
}

func TestConfigureNumericCharacteristicRangeRoundsIntegerBounds(t *testing.T) {
	current := characteristic.NewSetDuration()
	minimum, maximum, step := 10.2, 59.8, 2.2
	if !configureNumericCharacteristicRange(current.C, device.PropertyDefinition{
		Type: device.ValueTypeInt, Min: &minimum, Max: &maximum, Step: &step,
	}) {
		t.Fatal("overlapping integer range was rejected")
	}
	if current.MinValue() != 11 || current.MaxValue() != 59 || current.StepValue() != 3 {
		t.Fatalf("effective integer range = %d..%d step %d", current.MinValue(), current.MaxValue(), current.StepValue())
	}
}

func TestConfigureAccessoryNumericRangesOmitsNonOverlappingCharacteristic(t *testing.T) {
	info := accessory.Info{Name: "cold room", SerialNumber: "cold-room"}
	item := accessory.NewTemperatureSensor(info)
	minimum, maximum := -40.0, -10.0
	source := device.Device{
		ID: "cold-room", Type: device.TypeTemperatureSensor,
		Endpoints: []device.Endpoint{{ID: "main", Capabilities: []device.Capability{{ID: "temperature", Properties: []device.Property{{
			Definition: device.PropertyDefinition{ID: "current-temperature", Type: device.ValueTypeNumber, Min: &minimum, Max: &maximum},
			Value:      device.NumberValue(-20),
		}}}}}},
	}
	configureAccessoryNumericRanges(item.A, source, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if findCharacteristicInService(item.TempSensor.S, characteristic.TypeCurrentTemperature) != nil {
		t.Fatal("non-overlapping HomeKit characteristic was still published")
	}
}

func TestEveryHomeKitNumericContractHasRuntimeRangeMetadata(t *testing.T) {
	for _, contract := range mapping.HomeKitConsumerContracts() {
		model, found := device.ModelContractFor(contract.DeviceType)
		if !found {
			t.Fatalf("model contract %q is missing", contract.DeviceType)
		}
		parameters := make(map[string]device.ModelParameter, len(model.Parameters))
		for _, parameter := range model.Parameters {
			parameters[parameter.Path.Key()] = parameter
		}
		for _, parameter := range contract.Parameters {
			definition := parameters[parameter.Source.Key()]
			if definition.Type != device.ValueTypeInt && definition.Type != device.ValueTypeNumber {
				continue
			}
			if len(homeKitTargetCharacteristicTypes[parameter.Target]) == 0 {
				t.Errorf("numeric HomeKit target %q for %q has no runtime range metadata", parameter.Target, contract.DeviceType)
			}
		}
	}
}

func findCharacteristicInService(currentService *service.S, characteristicType string) *characteristic.C {
	for _, current := range currentService.Cs {
		if current.Type == characteristicType {
			return current
		}
	}
	return nil
}
