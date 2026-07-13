package application_test

import (
	"context"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
)

type providerStore struct {
	items map[string]providerconfig.Config
}

func (s *providerStore) ListProviders(context.Context) ([]providerconfig.Config, error) {
	return nil, nil
}
func (s *providerStore) SaveProvider(_ context.Context, item providerconfig.Config) error {
	s.items[item.ID] = item
	return nil
}
func (s *providerStore) DeleteProvider(_ context.Context, id string) error {
	delete(s.items, id)
	return nil
}

func TestProviderServiceAppliesDisablesAndDeletes(t *testing.T) {
	ctx := context.Background()
	store := &providerStore{items: make(map[string]providerconfig.Config)}
	factory := providersdk.NewFactory()
	if err := factory.Register("virtual", func(item providerconfig.Config) (providersdk.Provider, error) {
		return virtual.NewProviderFromConfig(item)
	}); err != nil {
		t.Fatal(err)
	}
	runtime, _ := providermanager.New()
	if err := runtime.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	service := application.NewProviderService(nil, store, factory, runtime)
	info, err := service.Save(ctx, providerconfig.Config{ID: "virtual-lab", Type: "virtual", Name: "Lab", Enabled: true})
	if err != nil || info.Status != "running" {
		t.Fatalf("save = %#v, %v", info, err)
	}
	if devices, _ := runtime.DiscoverDevices(ctx); len(devices) != 2 || devices[0].ProviderID != "virtual-lab" {
		t.Fatalf("devices = %#v", devices)
	}
	info, err = service.Save(ctx, providerconfig.Config{ID: "virtual-lab", Type: "virtual", Name: "Lab", Enabled: false})
	if err != nil || info.Status != "disabled" {
		t.Fatalf("disable = %#v, %v", info, err)
	}
	if err := service.Delete(ctx, "virtual-lab"); err != nil {
		t.Fatal(err)
	}
	if len(service.List()) != 0 || len(store.items) != 0 {
		t.Fatal("provider was not deleted")
	}
}

func TestProviderServiceValidatesConfiguration(t *testing.T) {
	store := &providerStore{items: make(map[string]providerconfig.Config)}
	factory := providersdk.NewFactory()
	if err := factory.Register("virtual", func(item providerconfig.Config) (providersdk.Provider, error) {
		return virtual.NewProviderFromConfig(item)
	}); err != nil {
		t.Fatal(err)
	}
	runtime, _ := providermanager.New()
	service := application.NewProviderService(nil, store, factory, runtime)
	if _, err := service.Save(context.Background(), providerconfig.Config{ID: "bad id", Config: []byte(`[]`)}); err == nil {
		t.Fatal("expected validation error")
	}
	if _, err := service.Save(context.Background(), providerconfig.Config{ID: "invalid", Type: "virtual", Config: []byte(`{"devices":[{"type":"unknown"}]}`)}); err == nil {
		t.Fatal("expected provider-specific validation error")
	}
	if len(store.items) != 0 {
		t.Fatal("invalid configuration reached durable storage")
	}
}
