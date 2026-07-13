package virtual

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

func TestSetPower(t *testing.T) {
	provider := NewProvider()

	updated, err := provider.SetPower(context.Background(), "virtual-switch-1", true)
	if err != nil {
		t.Fatalf("SetPower() error = %v", err)
	}
	if updated.State.Power == nil || !*updated.State.Power {
		t.Fatal("SetPower() did not persist the requested value")
	}

	items, err := provider.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, item := range items {
		if item.ID == updated.ID && (item.State.Power == nil || !*item.State.Power) {
			t.Fatal("List() returned stale switch state")
		}
	}
}

func TestProviderUsesDatabaseDeviceConfiguration(t *testing.T) {
	provider, err := NewProviderFromConfig(providerconfig.Config{ID: "virtual-lab", Name: "Lab", Config: []byte(`{
		"devices":[
			{"id":"desk-light","name":"Desk light","type":"switch","power":true},
			{"id":"outdoor-temp","name":"Outdoor","type":"temperature-sensor","temperature":12.5,"online":false}
		]}`)})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := provider.List(context.Background())
	if len(items) != 2 {
		t.Fatalf("devices = %#v", items)
	}
	if items[0].ID != "desk-light" || items[0].State.Power == nil || !*items[0].State.Power {
		t.Fatalf("switch = %#v", items[0])
	}
	if items[1].ID != "outdoor-temp" || items[1].Online || items[1].State.Temperature == nil || *items[1].State.Temperature != 12.5 {
		t.Fatalf("sensor = %#v", items[1])
	}
}

func TestProviderSimulatesRejectedAndCancelledWrites(t *testing.T) {
	rejecting, err := NewProviderFromConfig(providerconfig.Config{ID: "reject", Config: []byte(`{"rejectWrites":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rejecting.SetPower(context.Background(), "reject-switch-1", true); !errors.Is(err, providersdk.ErrWriteRejected) {
		t.Fatalf("error = %v", err)
	}
	delayed, err := NewProviderFromConfig(providerconfig.Config{ID: "slow", Config: []byte(`{"latencyMs":1000}`)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := delayed.SetPower(ctx, "slow-switch-1", true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if time.Since(started) > 250*time.Millisecond {
		t.Fatal("write did not honor context cancellation")
	}
}

func TestProviderRejectsInvalidDeviceConfiguration(t *testing.T) {
	for _, raw := range []string{`{"latencyMs":-1}`, `{"devices":[{"id":"x","type":"unknown"}]}`, `{"devices":[{"id":"x","type":"switch"},{"id":"x","type":"switch"}]}`} {
		if _, err := NewProviderFromConfig(providerconfig.Config{ID: "invalid", Config: []byte(raw)}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestSimulateUpdatesStateAndPublishesSnapshot(t *testing.T) {
	provider := NewProvider()
	events := make(chan device.Device, 1)
	unsubscribe := provider.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()
	online := false
	temperature := 18.25
	updated, err := provider.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: "virtual-temperature-1", Online: &online, Properties: []providersdk.PropertyWriteRequest{{EndpointID: "main", CapabilityID: "temperature", PropertyID: "current-temperature", Value: device.NumberValue(temperature)}}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Online || updated.State.Temperature == nil || *updated.State.Temperature != temperature {
		t.Fatalf("updated = %#v", updated)
	}
	select {
	case event := <-events:
		if event.ID != updated.ID || event.Online {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("simulation event was not published")
	}
	invalid := 500.0
	if _, err := provider.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: updated.ID, Properties: []providersdk.PropertyWriteRequest{{EndpointID: "main", CapabilityID: "temperature", PropertyID: "current-temperature", Value: device.NumberValue(invalid)}}}); !errors.Is(err, providersdk.ErrSimulationInvalid) {
		t.Fatalf("invalid error = %v", err)
	}
}

func TestNewProviderStartsWithFreshRuntimeState(t *testing.T) {
	first := NewProvider()
	if _, err := first.SetPower(context.Background(), "virtual-switch-1", true); err != nil {
		t.Fatal(err)
	}
	second := NewProvider()
	items, _ := second.List(context.Background())
	for _, item := range items {
		if item.ID == "virtual-switch-1" && (item.State.Power == nil || *item.State.Power) {
			t.Fatal("new provider restored an old runtime state")
		}
	}
}

func TestProviderManifestAndCapabilities(t *testing.T) {
	provider := NewProvider()
	manifest := provider.Manifest()
	capabilities := provider.Capabilities()
	if manifest.ID != "virtual-main" || manifest.Type != "virtual" || manifest.Version == "" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if !capabilities.Discovery || !capabilities.PropertyWrite || !capabilities.Events {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}

func TestSetPowerErrors(t *testing.T) {
	provider := NewProvider()
	if _, err := provider.SetPower(context.Background(), "missing", true); !errors.Is(err, application.ErrDeviceNotFound) {
		t.Fatalf("missing device error = %v", err)
	}
	if _, err := provider.SetPower(context.Background(), "virtual-temperature-1", true); !errors.Is(err, application.ErrPropertyUnsupported) {
		t.Fatalf("unsupported property error = %v", err)
	}
}

func TestSetPowerNotifiesSubscribers(t *testing.T) {
	provider := NewProvider()
	notifications := make(chan bool, 1)
	unsubscribe := provider.Subscribe(func(item device.Device) {
		if item.State.Power != nil {
			notifications <- *item.State.Power
		}
	})
	defer unsubscribe()

	if _, err := provider.SetPower(context.Background(), "virtual-switch-1", true); err != nil {
		t.Fatalf("SetPower() error = %v", err)
	}
	select {
	case value := <-notifications:
		if !value {
			t.Fatal("subscriber received the wrong value")
		}
	default:
		t.Fatal("subscriber was not notified")
	}
}
