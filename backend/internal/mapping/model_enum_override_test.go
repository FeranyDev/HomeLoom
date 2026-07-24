package mapping

import (
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

func TestValidateModelEnumOverride(t *testing.T) {
	item := ModelEnumOverride{
		ID: "enum-fan-main-fan-target-mode", DeviceType: "fan",
		EndpointID: "main", CapabilityID: "fan", PropertyID: "target-mode",
		Enum: []string{"auto", "low", "high"},
	}
	if err := ValidateModelEnumOverride(item); err != nil {
		t.Fatal(err)
	}
	item.Enum = []string{"auto", "auto"}
	if err := ValidateModelEnumOverride(item); err == nil {
		t.Fatal("duplicate enum accepted")
	}
	item.Enum = []string{" auto"}
	if err := ValidateModelEnumOverride(item); err == nil {
		t.Fatal("whitespace enum accepted")
	}
}

func TestModelEnumOverrideID(t *testing.T) {
	id := ModelEnumOverrideID("fan", device.ParameterPath{EndpointID: "main", CapabilityID: "fan", PropertyID: "target-mode"})
	if id != "enum-fan-main-fan-target-mode" {
		t.Fatalf("id = %q", id)
	}
}
