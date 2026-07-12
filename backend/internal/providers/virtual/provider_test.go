package virtual

import (
	"context"
	"errors"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
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
