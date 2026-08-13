package gormstore

import (
	"context"
	"errors"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

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
