package gormstore

import (
	"context"
	"errors"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
)

func TestMCPConfigsPersistUpdateAndClearPropertyOverride(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	deviceConfig := domainmcp.DeviceConfig{DeviceID: "desk-lamp", Enabled: true, UsageNote: "书房主灯", DefaultAccess: domainmcp.AccessRead}
	if err := store.SaveMCPDeviceConfig(ctx, deviceConfig); err != nil {
		t.Fatal(err)
	}
	storedDevice, found, err := store.GetMCPDeviceConfig(ctx, deviceConfig.DeviceID)
	if err != nil || !found || storedDevice != deviceConfig {
		t.Fatalf("device config = %#v, found=%v, err=%v", storedDevice, found, err)
	}
	propertyConfig := domainmcp.PropertyConfig{PropertyPath: domainmcp.PropertyPath{DeviceID: "desk-lamp", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}, UsageNote: "离开时可以关闭", Access: domainmcp.AccessConfirm}
	if err := store.SaveMCPPropertyConfig(ctx, propertyConfig); err != nil {
		t.Fatal(err)
	}
	propertyConfig.UsageNote = "仅在已打开时建议关闭"
	if err := store.SaveMCPPropertyConfig(ctx, propertyConfig); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListMCPPropertyConfigs(ctx, deviceConfig.DeviceID)
	if err != nil || len(items) != 1 || items[0] != propertyConfig {
		t.Fatalf("property configs = %#v, %v", items, err)
	}
	if err := store.DeleteMCPPropertyConfig(ctx, propertyConfig.PropertyPath); err != nil {
		t.Fatal(err)
	}
	_, found, err = store.GetMCPPropertyConfig(ctx, propertyConfig.PropertyPath)
	if err != nil || found {
		t.Fatalf("property config found=%v, err=%v", found, err)
	}
}

func TestDeviceLocationPreferencesPersistAndClear(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	preference := device.LocationPreference{DeviceID: "xiaomi-light", HomeID: "home-main", HomeName: "我的家", RoomID: "room-study", RoomName: "书房"}
	if err := store.SetDeviceLocationPreference(ctx, preference); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListDeviceLocationPreferences(ctx)
	if err != nil || len(items) != 1 || items[0] != preference {
		t.Fatalf("locations = %#v, %v", items, err)
	}
	preference.RoomID, preference.RoomName = "room-living", "客厅"
	if err := store.SetDeviceLocationPreference(ctx, preference); err != nil {
		t.Fatal(err)
	}
	items, _ = store.ListDeviceLocationPreferences(ctx)
	if len(items) != 1 || items[0].RoomName != "客厅" {
		t.Fatalf("updated locations = %#v", items)
	}
	if err := store.ClearDeviceLocationPreference(ctx, preference.DeviceID); err != nil {
		t.Fatal(err)
	}
	items, _ = store.ListDeviceLocationPreferences(ctx)
	if len(items) != 0 {
		t.Fatalf("cleared locations = %#v", items)
	}
}

func TestDeviceLocationCatalogCRUDAndAssignmentProtection(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	home := device.LocationHome{ID: "home-main", Name: "我的家"}
	room := device.LocationRoom{ID: "room-study", HomeID: home.ID, Name: "书房"}
	if err := store.SaveDeviceLocationHome(ctx, home); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDeviceLocationRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	homes, err := store.ListDeviceLocationHomes(ctx)
	if err != nil || len(homes) != 1 || len(homes[0].Rooms) != 1 || homes[0].Rooms[0] != room {
		t.Fatalf("catalog = %#v, %v", homes, err)
	}
	if err := store.SetDeviceLocationPreference(ctx, device.LocationPreference{DeviceID: "light", HomeID: home.ID, HomeName: home.Name, RoomID: room.ID, RoomName: room.Name}); err != nil {
		t.Fatal(err)
	}
	home.Name, room.Name = "父母家", "卧室"
	if err := store.SaveDeviceLocationHome(ctx, home); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDeviceLocationRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	preferences, _ := store.ListDeviceLocationPreferences(ctx)
	if len(preferences) != 1 || preferences[0].HomeName != "父母家" || preferences[0].RoomName != "卧室" {
		t.Fatalf("renamed preference = %#v", preferences)
	}
	if err := store.DeleteDeviceLocationRoom(ctx, room.ID); !errors.Is(err, device.ErrLocationInUse) {
		t.Fatalf("delete assigned room error = %v", err)
	}
	if err := store.DeleteDeviceLocationHome(ctx, home.ID); !errors.Is(err, device.ErrLocationInUse) {
		t.Fatalf("delete assigned home error = %v", err)
	}
	if err := store.ClearDeviceLocationPreference(ctx, "light"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteDeviceLocationRoom(ctx, room.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteDeviceLocationHome(ctx, home.ID); err != nil {
		t.Fatal(err)
	}
}
