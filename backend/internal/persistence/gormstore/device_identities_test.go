package gormstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
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
