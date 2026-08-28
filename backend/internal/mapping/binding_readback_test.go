package mapping

import "testing"

func TestProviderBindingValidatesPerPropertyReadbackSchedule(t *testing.T) {
	binding := Binding{
		ID: "switch-power", Stage: StageProvider, ProviderID: "sonoff-main", DeviceID: "sonoff-switch",
		EndpointID: "main", CapabilityID: "switch", PropertyID: "power",
		ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power",
		Enabled: true, ReadbackEnabled: true, ReadbackDelaysMS: []int{500, 2000, 5000},
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
		t.Fatal("consumer binding accepted Provider readback policy")
	}
}
