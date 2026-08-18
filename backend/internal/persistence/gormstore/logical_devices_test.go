package gormstore

import (
	"context"
	"errors"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/logicaldevice"
	"gorm.io/gorm"
)

func testLogicalDevice() logicaldevice.Config {
	return logicaldevice.Config{ID: "living-switch", Name: "客厅开关", Type: device.TypeSwitch, Bindings: []logicaldevice.Binding{
		{SourceRef: logicaldevice.SourceRef{ProviderID: "local", DeviceID: "switch-1"}},
		{SourceRef: logicaldevice.SourceRef{ProviderID: "cloud", DeviceID: "switch-1"}, Priority: 10},
	}}
}

func TestLogicalDevicesPersistAndDelete(t *testing.T) {
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
	item.Name = "客厅主开关"
	if err := store.SaveLogicalDevice(ctx, item); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListLogicalDevices(ctx)
	if err != nil || len(items) != 1 || items[0].Name != item.Name {
		t.Fatalf("logical devices = %#v, %v", items, err)
	}
	if err := store.DeleteLogicalDevice(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteLogicalDevice(ctx, item.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}
