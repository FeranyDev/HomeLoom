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
	if err != nil || len(items) != 1 || items[0] != item {
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
