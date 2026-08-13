package main

import (
	"encoding/json"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/network"
)

func TestRegisterNetworkProviderCreatesConfiguredProvider(t *testing.T) {
	factory := providersdk.NewFactory()
	if err := registerNetworkProvider(factory); err != nil {
		t.Fatal(err)
	}

	instance, err := factory.Create(providerconfig.Config{
		ID: "network-main", Type: network.ProviderType, Name: "LAN devices",
		Config: json.RawMessage(`{"devices":[{"id":"nas","name":"NAS","host":"192.0.2.10","mac":"AA:BB:CC:DD:EE:FF","probePort":22}]}`),
	})
	if err != nil {
		t.Fatalf("factory did not create network Provider: %v", err)
	}
	if manifest := instance.Manifest(); manifest.ID != "network-main" || manifest.Type != network.ProviderType {
		t.Fatalf("manifest = %#v", manifest)
	}
	if capabilities := instance.Capabilities(); !capabilities.Discovery || !capabilities.PropertyWrite || capabilities.Commands || !capabilities.Events {
		t.Fatalf("network Provider capabilities = %#v", capabilities)
	}
}
