package logicaldevice_test

import (
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/logicaldevice"
)

func TestConfigValidationRequiresExplicitMultiProviderBinding(t *testing.T) {
	config := logicaldevice.Config{ID: "living-switch", Name: "客厅开关", Type: device.TypeSwitch, Bindings: []logicaldevice.Binding{{SourceRef: logicaldevice.SourceRef{ProviderID: "local", DeviceID: "switch-1"}}}}
	if err := config.Validate(); err == nil {
		t.Fatal("single source logical device was accepted")
	}
	config.Bindings = append(config.Bindings, logicaldevice.Binding{SourceRef: logicaldevice.SourceRef{ProviderID: "cloud", DeviceID: "switch-1"}, Priority: 10})
	if err := config.Validate(); err != nil {
		t.Fatalf("valid multi-provider config rejected: %v", err)
	}
}

func TestConfigValidationRejectsUnboundOrDuplicateRoutes(t *testing.T) {
	config := logicaldevice.Config{
		ID: "living-switch", Name: "客厅开关", Type: device.TypeSwitch,
		Bindings: []logicaldevice.Binding{{SourceRef: logicaldevice.SourceRef{ProviderID: "local", DeviceID: "switch-1"}}, {SourceRef: logicaldevice.SourceRef{ProviderID: "cloud", DeviceID: "switch-1"}, Priority: 10}},
		PropertyRoutes: []logicaldevice.PropertyRoute{{
			Path:       logicaldevice.PropertyPath{EndpointID: "main", CapabilityID: "switch", PropertyID: "power"},
			Candidates: []logicaldevice.PropertyCandidate{{SourceRef: logicaldevice.SourceRef{ProviderID: "other", DeviceID: "switch-1"}, Path: logicaldevice.PropertyPath{EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}}},
		}},
	}
	if err := config.Validate(); err == nil {
		t.Fatal("unbound route source accepted")
	}
	config.PropertyRoutes[0].Candidates[0].ProviderID = "local"
	config.PropertyRoutes = append(config.PropertyRoutes, config.PropertyRoutes[0])
	if err := config.Validate(); err == nil {
		t.Fatal("duplicate property route accepted")
	}
}
