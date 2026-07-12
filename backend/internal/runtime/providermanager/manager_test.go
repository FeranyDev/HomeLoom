package providermanager_test

import (
	"context"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
)

func TestManagerDiscoversRoutesEventsAndWrites(t *testing.T) {
	ctx := context.Background()
	manager, err := providermanager.New(virtual.NewProvider())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := manager.DiscoverDevices(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("DiscoverDevices() = %#v, %v", items, err)
	}
	events := make(chan device.Device, 1)
	unsubscribe := manager.Subscribe(func(item device.Device) { events <- item })
	updated, err := manager.WriteProperty(ctx, providersdk.PropertyWriteRequest{
		DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProviderID != "virtual-main" {
		t.Fatalf("provider id = %q", updated.ProviderID)
	}
	select {
	case event := <-events:
		if event.ProviderID != "virtual-main" {
			t.Fatalf("event provider id = %q", event.ProviderID)
		}
	default:
		t.Fatal("provider event was not forwarded")
	}
	unsubscribe()
	infos := manager.ProviderInfos()
	if len(infos) != 1 || infos[0].Status != "running" {
		t.Fatalf("ProviderInfos() = %#v", infos)
	}
	if err := manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if manager.ProviderInfos()[0].Status != "stopped" {
		t.Fatal("provider was not marked stopped")
	}
}

func TestManagerRejectsDuplicateProviderAndUnknownDevice(t *testing.T) {
	if _, err := providermanager.New(virtual.NewProvider(), virtual.NewProvider()); err == nil {
		t.Fatal("duplicate provider id was accepted")
	}
	manager, _ := providermanager.New(virtual.NewProvider())
	manager.Initialize(context.Background())
	manager.DiscoverDevices(context.Background())
	if _, err := manager.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: "missing"}); err != providersdk.ErrDeviceNotFound {
		t.Fatalf("unknown device error = %v", err)
	}
}

func TestManagerHotAppliesAndRemovesProvider(t *testing.T) {
	ctx := context.Background()
	manager, _ := providermanager.New()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	events := make(chan device.Device, 4)
	unsubscribe := manager.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()
	if err := manager.Apply(ctx, virtual.NewProviderWithIdentity("virtual-lab", "Lab")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case item := <-events:
			if !item.Online || item.ProviderID != "virtual-lab" {
				t.Fatalf("apply event = %#v", item)
			}
		case <-time.After(time.Second):
			t.Fatal("missing apply event")
		}
	}
	if err := manager.Remove(ctx, "virtual-lab"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case item := <-events:
			if item.Online {
				t.Fatalf("remove event still online = %#v", item)
			}
		case <-time.After(time.Second):
			t.Fatal("missing offline event")
		}
	}
	if len(manager.ProviderInfos()) != 0 {
		t.Fatal("provider still registered")
	}
}
