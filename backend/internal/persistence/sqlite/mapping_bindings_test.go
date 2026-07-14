package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func TestMappingBindingsPersistAndEnforceUniquePropertyPath(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "bindings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item := mapping.Binding{ID: "binding-one", ProfileID: "builtin-active-low", ProviderID: "virtual-main", DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Enabled: true}
	if err := store.SaveMappingBinding(ctx, item); err != nil {
		t.Fatal(err)
	}
	duplicate := item
	duplicate.ID = "binding-two"
	if err := store.SaveMappingBinding(ctx, duplicate); err == nil {
		t.Fatal("duplicate property binding was accepted")
	}
	items, err := store.ListMappingBindings(ctx)
	if err != nil || len(items) != 1 || items[0].ID != item.ID || items[0].EffectiveStage() != mapping.StageProvider || items[0].ModelPath() != item.SourcePath() || items[0].ProfileID != item.ProfileID || !items[0].Enabled {
		t.Fatalf("bindings = %#v, error = %v", items, err)
	}
	item.Enabled = false
	if err := store.SaveMappingBinding(ctx, item); err != nil {
		t.Fatal(err)
	}
	items, _ = store.ListMappingBindings(ctx)
	if items[0].Enabled {
		t.Fatal("binding update was not persisted")
	}
	if err := store.DeleteMappingBinding(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
}

func TestConsumerMappingUniquenessIsScopedPerDevice(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "consumer-bindings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := mapping.Binding{ID: "consumer-one", Stage: mapping.StageConsumer, ProviderID: "virtual-main", DeviceID: "switch-one", DeviceType: "switch", ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power", ConsumerID: "homekit", ConsumerProperty: "Switch.On", Enabled: true}
	if err := store.SaveMappingBinding(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID, second.DeviceID = "consumer-two", "switch-two"
	if err := store.SaveMappingBinding(ctx, second); err != nil {
		t.Fatalf("same Consumer property on another device was rejected: %v", err)
	}
	duplicate := first
	duplicate.ID = "consumer-duplicate"
	if err := store.SaveMappingBinding(ctx, duplicate); err == nil {
		t.Fatal("duplicate Consumer property on the same device was accepted")
	}
}
