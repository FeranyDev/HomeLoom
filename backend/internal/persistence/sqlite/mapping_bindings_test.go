package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func TestTargetScopedConsumerMigrationCopiesLegacyRoutesPerVirtualDevice(t *testing.T) {
	ctx := context.Background()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration-17.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	_, err = database.ExecContext(ctx, `
		CREATE TABLE mapping_bindings (
			id TEXT PRIMARY KEY, stage TEXT NOT NULL, profile_id TEXT NOT NULL DEFAULT '',
			provider_id TEXT NOT NULL DEFAULT '', device_id TEXT NOT NULL DEFAULT '',
			endpoint_id TEXT NOT NULL DEFAULT '', capability_id TEXT NOT NULL DEFAULT '', property_id TEXT NOT NULL DEFAULT '',
			device_type TEXT NOT NULL DEFAULT '', model_endpoint_id TEXT NOT NULL, model_capability_id TEXT NOT NULL, model_property_id TEXT NOT NULL,
			consumer_id TEXT NOT NULL DEFAULT '', consumer_property TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		);
		CREATE UNIQUE INDEX mapping_consumer_target_unique ON mapping_bindings(provider_id, device_id, consumer_id, consumer_property) WHERE stage = 'consumer';
		CREATE TABLE target_virtual_devices (target_id TEXT NOT NULL, id TEXT NOT NULL, source_device_id TEXT NOT NULL);
		INSERT INTO mapping_bindings(id, stage, provider_id, device_id, device_type, model_endpoint_id, model_capability_id, model_property_id, consumer_id, consumer_property, created_at, updated_at)
		VALUES ('legacy-consumer', 'consumer', 'virtual-main', 'switch-one', 'switch', 'main', 'switch', 'power', 'homekit', 'Switch.On', 1, 1);
		INSERT INTO target_virtual_devices(target_id, id, source_device_id) VALUES
			('apple-main', 'living-switch', 'switch-one'),
			('apple-guest', 'guest-switch', 'switch-one');
	`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/017_target_scoped_consumer_mapping.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	var scoped, legacy int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM mapping_bindings WHERE target_id <> '' AND consumer_device_id <> ''").Scan(&scoped); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM mapping_bindings WHERE target_id = '' AND consumer_device_id = ''").Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if scoped != 2 || legacy != 1 {
		t.Fatalf("scoped = %d, legacy = %d", scoped, legacy)
	}
}

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

func TestConsumerMappingUniquenessIsScopedPerTargetVirtualDevice(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "target-consumer-bindings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := mapping.Binding{
		ID: "target-consumer-one", Stage: mapping.StageConsumer,
		ProviderID: "virtual-main", DeviceID: "switch-one", DeviceType: "switch",
		ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power",
		TargetID: "apple-main", ConsumerDeviceID: "living-room-switch",
		ConsumerID: "homekit", ConsumerProperty: "Switch.On", Enabled: true,
	}
	if err := store.SaveMappingBinding(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID, second.TargetID, second.ConsumerDeviceID = "target-consumer-two", "apple-guest", "guest-switch"
	if err := store.SaveMappingBinding(ctx, second); err != nil {
		t.Fatalf("same source and characteristic on another virtual device was rejected: %v", err)
	}
	items, err := store.ListMappingBindings(ctx)
	if err != nil || len(items) != 2 || items[0].TargetID == "" || items[1].ConsumerDeviceID == "" {
		t.Fatalf("scoped bindings = %#v, error = %v", items, err)
	}
	duplicate := first
	duplicate.ID = "target-consumer-duplicate"
	if err := store.SaveMappingBinding(ctx, duplicate); err == nil {
		t.Fatal("duplicate Consumer property on the same target virtual device was accepted")
	}
}
