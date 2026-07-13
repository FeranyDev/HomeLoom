package providermanager_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
)

type failingProvider struct{ id string }

func (p failingProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "test", Name: "Failing"}
}
func (failingProvider) Capabilities() providersdk.Capabilities { return providersdk.Capabilities{} }
func (failingProvider) Initialize(context.Context) error       { return errors.New("connection refused") }
func (failingProvider) Close(context.Context) error            { return nil }

type flakyProvider struct{ attempts atomic.Int32 }

func (*flakyProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: "flaky", Type: "test", Name: "Flaky"}
}
func (*flakyProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true}
}
func (p *flakyProvider) Initialize(context.Context) error {
	if p.attempts.Add(1) == 1 {
		return errors.New("temporary failure")
	}
	return nil
}
func (*flakyProvider) Close(context.Context) error                              { return nil }
func (*flakyProvider) DiscoverDevices(context.Context) ([]device.Device, error) { return nil, nil }

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
	property, err := manager.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"})
	if err != nil || property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatalf("ReadProperty() = %#v, %v", property, err)
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
			if item.Online || !item.Removed {
				t.Fatalf("remove event is not a tombstone = %#v", item)
			}
		case <-time.After(time.Second):
			t.Fatal("missing offline event")
		}
	}
	if len(manager.ProviderInfos()) != 0 {
		t.Fatal("provider still registered")
	}
}

func TestManagerIsolatesProviderInitializationFailure(t *testing.T) {
	manager, err := providermanager.New(failingProvider{id: "broken"}, virtual.NewProvider())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatalf("manager initialization should isolate provider failure: %v", err)
	}
	infos := manager.ProviderInfos()
	if len(infos) != 2 || infos[0].Status != "error" || infos[0].Error != "connection refused" || infos[1].Status != "running" {
		t.Fatalf("infos = %#v", infos)
	}
	items, err := manager.DiscoverDevices(context.Background())
	if err != nil || len(items) != 2 {
		t.Fatalf("healthy discovery = %#v, %v", items, err)
	}
}

func TestManagerAutomaticallyRetriesFailedProvider(t *testing.T) {
	provider := &flakyProvider{}
	manager, _ := providermanager.New(provider)
	defer manager.Close(context.Background())
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	initial := manager.ProviderInfos()[0]
	if initial.Status != "error" || initial.NextRetryAt == nil {
		t.Fatalf("initial = %#v", initial)
	}
	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		info := manager.ProviderInfos()[0]
		if info.Status == "running" {
			if info.RetryCount != 1 || info.NextRetryAt != nil {
				t.Fatalf("recovered = %#v", info)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("provider did not recover: %#v", manager.ProviderInfos()[0])
}
