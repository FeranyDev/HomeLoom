package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

type bootstrapStore struct {
	items []providerconfig.Config
	saves int
}

func (s *bootstrapStore) ListProviders(context.Context) ([]providerconfig.Config, error) {
	return append([]providerconfig.Config(nil), s.items...), nil
}

func (s *bootstrapStore) SaveProvider(_ context.Context, item providerconfig.Config) error {
	for index := range s.items {
		if s.items[index].ID == item.ID {
			s.items[index] = item
			s.saves++
			return nil
		}
	}
	s.items = append(s.items, item)
	s.saves++
	return nil
}

func TestInitializeAllVirtualModelsPersistsEverySupportedTypeOnce(t *testing.T) {
	store := &bootstrapStore{items: []providerconfig.Config{{
		ID: "virtual-main", Type: "virtual", Name: "Virtual Provider", Enabled: true,
		Config: json.RawMessage(`{"latencyMs":12}`),
	}}}
	changed, err := initializeAllVirtualModels(context.Background(), store)
	if err != nil || !changed || store.saves != 1 {
		t.Fatalf("initialize = %v, %v, saves=%d", changed, err, store.saves)
	}
	provider, err := virtual.NewProviderFromConfig(store.items[0])
	if err != nil {
		t.Fatal(err)
	}
	items, err := provider.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[device.Type]bool{
		device.TypeSwitch: false, device.TypeLightbulb: false, device.TypeOutlet: false,
		device.TypeSinglePropertySensor:      false,
		device.TypeTemperatureHumiditySensor: false,
		device.TypeContactSensor:             false, device.TypeMotionSensor: false,
		device.TypeFan: false, device.TypeAirPurifier: false, device.TypeWindowCovering: false,
	}
	for _, item := range items {
		if _, exists := want[item.Type]; !exists || want[item.Type] {
			t.Fatalf("unexpected or duplicate device type %q", item.Type)
		}
		want[item.Type] = true
	}
	if len(items) != len(want) {
		t.Fatalf("device count = %d, want %d", len(items), len(want))
	}
	for itemType, found := range want {
		if !found {
			t.Fatalf("device type %q was not initialized", itemType)
		}
	}
	var persisted map[string]json.RawMessage
	if err := json.Unmarshal(store.items[0].Config, &persisted); err != nil {
		t.Fatal(err)
	}
	if string(persisted["latencyMs"]) != "12" {
		t.Fatalf("existing config was not preserved: %s", store.items[0].Config)
	}

	changed, err = initializeAllVirtualModels(context.Background(), store)
	if err != nil || changed || store.saves != 1 {
		t.Fatalf("second initialize = %v, %v, saves=%d", changed, err, store.saves)
	}
}

func TestInitializeAllVirtualModelsDoesNotReplaceCustomDevices(t *testing.T) {
	config := json.RawMessage(`{"devices":[{"id":"custom","type":"switch"}]}`)
	store := &bootstrapStore{items: []providerconfig.Config{{ID: "virtual-main", Type: "virtual", Config: config}}}
	changed, err := initializeAllVirtualModels(context.Background(), store)
	if err != nil || changed || store.saves != 0 || string(store.items[0].Config) != string(config) {
		t.Fatalf("initialize replaced custom config: changed=%v err=%v saves=%d config=%s", changed, err, store.saves, store.items[0].Config)
	}
}
