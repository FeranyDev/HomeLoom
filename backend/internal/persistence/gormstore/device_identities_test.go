package gormstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
)

func TestStableDeviceIdentityRowsPreserveNativeAndTopologyBindings(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.EnsureProviderDeviceIdentity(ctx, "provider-main", "native:device/1", "living-switch"); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureProviderDeviceIdentity(ctx, "provider-main", "native:device/1", "living-switch"); err != nil {
		t.Fatalf("repeat native identity = %v", err)
	}
	if err := store.EnsureProviderDeviceIdentity(ctx, "provider-main", "native:device/1", "different-device"); !errors.Is(err, ErrProviderDeviceIdentityConflict) {
		t.Fatalf("changed binding error = %v", err)
	}

	item := device.Device{ID: "living-switch", Endpoints: []device.Endpoint{{ID: "main", Capabilities: []device.Capability{{ID: "switch"}, {ID: "diagnostics"}}}}}
	if err := store.EnsureDeviceTopologyIdentity(ctx, item); err != nil {
		t.Fatal(err)
	}
	// A temporary Provider omission is not an identity deletion.
	item.Endpoints = nil
	if err := store.EnsureDeviceTopologyIdentity(ctx, item); err != nil {
		t.Fatal(err)
	}
	var rows []deviceCapabilityIdentityRow
	if err := store.orm.WithContext(ctx).Where("device_id = ?", "living-switch").Order("capability_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].CapabilityID != "diagnostics" || rows[1].CapabilityID != "switch" {
		t.Fatalf("topology identities = %#v", rows)
	}
}

func TestProviderDeviceIdentityRebindsOnlyAfterPreviousProviderIsDeleted(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	oldProvider := providerconfig.Config{ID: "sonoff-old", Type: "sonoff", Name: "Old", Config: []byte(`{}`)}
	newProvider := providerconfig.Config{ID: "sonoff-new", Type: "sonoff", Name: "New", Config: []byte(`{}`)}
	if err := store.SaveProvider(ctx, oldProvider); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureProviderDeviceIdentity(ctx, oldProvider.ID, "1001f95735", "sonoff-1001f95735"); err != nil {
		t.Fatal(err)
	}
	var original providerDeviceIdentityRow
	if err := store.orm.WithContext(ctx).Where("device_id = ?", "sonoff-1001f95735").Take(&original).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProvider(ctx, newProvider); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureProviderDeviceIdentity(ctx, newProvider.ID, "1001f95735", "sonoff-1001f95735"); !errors.Is(err, ErrProviderDeviceIdentityConflict) {
		t.Fatalf("identity moved while previous provider still existed: %v", err)
	}

	if err := store.DeleteProvider(ctx, oldProvider.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureProviderDeviceIdentity(ctx, newProvider.ID, "1001f95735", "sonoff-1001f95735"); err != nil {
		t.Fatalf("rebind orphaned identity: %v", err)
	}
	var rebound providerDeviceIdentityRow
	if err := store.orm.WithContext(ctx).Where("device_id = ?", "sonoff-1001f95735").Take(&rebound).Error; err != nil {
		t.Fatal(err)
	}
	if rebound.ProviderID != newProvider.ID || rebound.ProviderDeviceID != "1001f95735" || rebound.CreatedAt != original.CreatedAt || rebound.UpdatedAt < original.UpdatedAt {
		t.Fatalf("rebound identity = %#v; original = %#v", rebound, original)
	}
	var count int64
	if err := store.orm.WithContext(ctx).Model(&providerDeviceIdentityRow{}).Where("device_id = ?", "sonoff-1001f95735").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("identity rows = %d, %v", count, err)
	}
}

func TestProviderDeviceIdentityMigratesNativeKeyForSameProvider(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	configured := providerconfig.Config{ID: "tuya-main", Type: "tuya", Name: "Tuya", Config: []byte(`{}`)}
	if err := store.SaveProvider(ctx, configured); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureProviderDeviceIdentity(ctx, configured.ID, "tuya-device-1", "tuya-device-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureProviderDeviceIdentity(ctx, configured.ID, "device-1", "tuya-device-1"); err != nil {
		t.Fatalf("migrate native identity: %v", err)
	}
	var row providerDeviceIdentityRow
	if err := store.orm.WithContext(ctx).Where("device_id = ?", "tuya-device-1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.ProviderID != configured.ID || row.ProviderDeviceID != "device-1" {
		t.Fatalf("migrated identity = %#v", row)
	}
}

func TestLogicalIdentityRetentionAndExplicitPurge(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item := testLogicalDevice()
	if err := store.SaveLogicalDevice(ctx, item); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteLogicalDevice(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	var identity logicalDeviceIdentityRow
	if err := store.orm.WithContext(ctx).Where("logical_device_id = ?", item.ID).Take(&identity).Error; err != nil {
		t.Fatal(err)
	}
	if identity.DeletedAt == 0 || identity.PurgeAfter <= identity.DeletedAt {
		t.Fatalf("logical identity retention = %#v", identity)
	}
	if removed, err := store.PruneExpiredStableIdentities(ctx, time.UnixMilli(identity.DeletedAt)); err != nil || removed != 0 {
		t.Fatalf("early identity purge removed=%d err=%v", removed, err)
	}
	if removed, err := store.PruneExpiredStableIdentities(ctx, time.UnixMilli(identity.PurgeAfter)); err != nil || removed != 1 {
		t.Fatalf("due identity purge removed=%d err=%v", removed, err)
	}
	if err := store.SaveLogicalDevice(ctx, item); err != nil {
		t.Fatal(err)
	}
	if err := store.orm.WithContext(ctx).Where("logical_device_id = ?", item.ID).Take(&identity).Error; err != nil || identity.DeletedAt != 0 || identity.PurgeAfter != 0 {
		t.Fatalf("recreated identity = %#v, %v", identity, err)
	}
}

func TestLogicalIdentityBackfillsExistingConfiguration(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item := testLogicalDevice()
	payload, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixMilli()
	if err := store.orm.WithContext(ctx).Create(&logicalDeviceRow{ID: item.ID, DocumentJSON: jsonDocument(payload), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListLogicalDevices(ctx); err != nil {
		t.Fatal(err)
	}
	var identity logicalDeviceIdentityRow
	if err := store.orm.WithContext(ctx).Where("logical_device_id = ?", item.ID).Take(&identity).Error; err != nil || identity.DeletedAt != 0 {
		t.Fatalf("backfilled logical identity = %#v, %v", identity, err)
	}
}

func TestHomeKitAccessoryUUIDIsStableAndTargetScoped(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	config := target.Config{ID: "uuid-target", Type: "apple-hap", Name: "Original", Pin: "12345678"}
	if err := store.SaveTarget(ctx, config); err != nil {
		t.Fatal(err)
	}
	first, err := store.HomeKitAccessoryUUID(ctx, config.ID, "living-switch")
	if err != nil {
		t.Fatal(err)
	}
	config.Name = "Renamed"
	if err := store.SaveTarget(ctx, config); err != nil {
		t.Fatal(err)
	}
	again, err := store.HomeKitAccessoryUUID(ctx, config.ID, "living-switch")
	if err != nil || again != first || len(first) != 36 || first[14] != '4' || (first[19] != '8' && first[19] != '9' && first[19] != 'a' && first[19] != 'b') {
		t.Fatalf("stable UUID first=%q again=%q err=%v", first, again, err)
	}
	if _, err := store.HomeKitAccessoryUUID(ctx, "missing-target", "living-switch"); err == nil {
		t.Fatal("accessory UUID accepted a missing target")
	}
}
