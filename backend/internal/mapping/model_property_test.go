package mapping

import (
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

func TestCustomModelParameterPreservesCompleteValueConstraints(t *testing.T) {
	min, max, step := 0.0, 100.0, 0.5
	parameter := CustomModelParameter(CustomModelProperty{
		DeviceType: device.TypeLightbulb,
		EndpointID: "main", CapabilityID: "vendor-light",
		Definition: device.PropertyDefinition{
			ID: "level", Name: "Vendor level", Type: device.ValueTypeNumber, ParameterLevel: device.ParameterCustom,
			Readable: true, Writable: true, Notifiable: true, Min: &min, Max: &max, Step: &step, StaleAfterSeconds: 30,
		},
	})
	if parameter.Min == nil || *parameter.Min != min || parameter.Max == nil || *parameter.Max != max || parameter.Step == nil || *parameter.Step != step || parameter.StaleAfterSeconds != 30 {
		t.Fatalf("constraints were not preserved: %#v", parameter)
	}
}

func TestValidateCustomModelPropertyRejectsNegativeStaleTTL(t *testing.T) {
	item := CustomModelProperty{ID: "air-quality-index", DeviceType: device.TypeAirQualitySensor, EndpointID: "main", EndpointName: "Main", EndpointType: "sensor", CapabilityID: "air-quality", CapabilityType: "air-quality", Definition: device.PropertyDefinition{ID: "index", Name: "Index", Type: device.ValueTypeNumber, Readable: true, Notifiable: true, StaleAfterSeconds: -1}}
	if err := ValidateCustomModelProperty(item); err == nil {
		t.Fatal("negative staleAfterSeconds was accepted")
	}
}
