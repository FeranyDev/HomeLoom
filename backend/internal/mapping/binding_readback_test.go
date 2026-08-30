package mapping

import "testing"

func TestProviderBindingValidatesPerPropertyReadbackSchedule(t *testing.T) {
	presentationStep := 1.0
	binding := Binding{
		ID: "switch-power", Stage: StageProvider, ProviderID: "sonoff-main", DeviceID: "sonoff-switch",
		EndpointID: "main", CapabilityID: "switch", PropertyID: "power",
		ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power",
		Enabled: true, ReadbackEnabled: true, ReadbackDelaysMS: []int{500, 2000, 5000}, PresentationStep: &presentationStep,
	}
	if err := ValidateBinding(binding); err != nil {
		t.Fatalf("valid readback binding: %v", err)
	}
	binding.ReadbackDelaysMS = []int{2000, 500}
	if err := ValidateBinding(binding); err == nil {
		t.Fatal("unordered readback schedule was accepted")
	}
	binding.Stage = StageConsumer
	binding.DeviceType = "switch"
	binding.ConsumerID = "homekit"
	binding.ConsumerProperty = "Switch.On"
	if err := ValidateBinding(binding); err == nil {
		t.Fatal("consumer binding accepted Provider-only mapping configuration")
	}
}

func TestProviderBindingRequiresPresentationStepOfAtLeastOneHundredth(t *testing.T) {
	validStep := 0.01
	binding := Binding{
		ID: "temperature-step", Stage: StageProvider, ProviderID: "gree-main", DeviceID: "living-room-ac",
		EndpointID: "main", CapabilityID: "temperature", PropertyID: "target-temperature",
		ModelEndpointID: "main", ModelCapabilityID: "temperature", ModelPropertyID: "target-temperature",
		Enabled: true, PresentationStep: &validStep,
	}
	if err := ValidateBinding(binding); err != nil {
		t.Fatalf("minimum presentation step should be valid: %v", err)
	}

	tooSmallStep := 0.009
	binding.PresentationStep = &tooSmallStep
	if err := ValidateBinding(binding); err == nil {
		t.Fatal("presentation step below 0.01 was accepted")
	}
}
