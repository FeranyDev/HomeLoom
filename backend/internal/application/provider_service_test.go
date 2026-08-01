package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
)

type renewableProvider struct {
	id         string
	version    int
	renewals   *atomic.Int32
	renewError error
}

type renewalRuntime struct {
	applyCalls atomic.Int32
	failFirst  atomic.Bool
}

type diagnosticRuntime struct{}

type configOnlyProvider struct{ id string }

func (p *configOnlyProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "catalog", Name: p.id}
}
func (*configOnlyProvider) Capabilities() providersdk.Capabilities { return providersdk.Capabilities{} }
func (*configOnlyProvider) Initialize(context.Context) error       { return nil }
func (*configOnlyProvider) Close(context.Context) error            { return nil }

func (*diagnosticRuntime) Apply(context.Context, providersdk.Provider) error { return nil }
func (*diagnosticRuntime) Remove(context.Context, string) error              { return nil }
func (*diagnosticRuntime) ProviderInfos() []providersdk.RuntimeInfo {
	return []providersdk.RuntimeInfo{{Manifest: providersdk.Manifest{ID: "virtual-main", Type: "virtual", Name: "Virtual"}, Status: "running", Diagnostics: map[string]string{"cloudMqttState": "connected"}}}
}

func (r *renewalRuntime) Apply(context.Context, providersdk.Provider) error {
	r.applyCalls.Add(1)
	if r.failFirst.CompareAndSwap(true, false) {
		return errors.New("runtime temporarily unavailable")
	}
	return nil
}
func (*renewalRuntime) Remove(context.Context, string) error     { return nil }
func (*renewalRuntime) ProviderInfos() []providersdk.RuntimeInfo { return nil }

func (p *renewableProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "renewable", Name: p.id}
}
func (*renewableProvider) Capabilities() providersdk.Capabilities { return providersdk.Capabilities{} }
func (*renewableProvider) Initialize(context.Context) error       { return nil }
func (*renewableProvider) Close(context.Context) error            { return nil }
func (p *renewableProvider) CredentialStatus(now time.Time) (providersdk.CredentialStatus, error) {
	refreshAt := now.Add(-time.Minute)
	if p.version >= 2 {
		refreshAt = now.Add(2 * time.Hour)
	}
	return providersdk.CredentialStatus{Managed: true, RefreshAt: refreshAt, TokenExpiresAt: now.Add(3 * time.Hour)}, nil
}
func (p *renewableProvider) RenewCredentials(context.Context) (json.RawMessage, error) {
	p.renewals.Add(1)
	if p.renewError != nil {
		return nil, p.renewError
	}
	return json.RawMessage(`{"version":2}`), nil
}

type providerStore struct {
	items map[string]providerconfig.Config
}

func TestProviderServiceRedactsAndRestoresSecrets(t *testing.T) {
	ctx := context.Background()
	original := providerconfig.Config{ID: "virtual-secret", Type: "virtual", Name: "Secret", Config: []byte(`{"password":"keep-me","ssecurity":"miot-security","nested":{"accessToken":"token-value","tokenExpiresAt":"public"},"accounts":[{"id":"a","password":"secret-a"},{"id":"b","password":"secret-b"}],"devices":[]}`)}
	store := &providerStore{items: map[string]providerconfig.Config{original.ID: original}}
	factory := providersdk.NewFactory()
	if err := factory.Register("virtual", func(item providerconfig.Config) (providersdk.Provider, error) {
		return virtual.NewProviderFromConfig(item)
	}); err != nil {
		t.Fatal(err)
	}
	runtime, _ := providermanager.New()
	service := application.NewProviderService([]providerconfig.Config{original}, store, factory, runtime)
	listed := service.List()
	if len(listed) != 1 || string(listed[0].Config.Config) != `{"accounts":[{"id":"a","password":"********"},{"id":"b","password":"********"}],"devices":[],"nested":{"accessToken":"********","tokenExpiresAt":"public"},"password":"********","ssecurity":"********"}` {
		t.Fatalf("redacted config = %s", listed[0].Config.Config)
	}
	resolved, err := service.ResolveTransientConfig(listed[0].Config)
	if err != nil || !strings.Contains(string(resolved.Config), `"password":"keep-me"`) || !strings.Contains(string(resolved.Config), `"accessToken":"token-value"`) || !strings.Contains(string(resolved.Config), `"ssecurity":"miot-security"`) {
		t.Fatalf("resolved transient config = %s, %v", resolved.Config, err)
	}
	listed[0].Config.Config = []byte(`{"password":"********","nested":{"accessToken":"********","tokenExpiresAt":"changed"},"accounts":[{"id":"b","password":"********"},{"id":"a","password":"********"}],"devices":[]}`)
	if _, err := service.Save(ctx, listed[0].Config); err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(store.items[original.ID].Config, &stored); err != nil {
		t.Fatal(err)
	}
	nested := stored["nested"].(map[string]any)
	accounts := stored["accounts"].([]any)
	if stored["password"] != "keep-me" || nested["accessToken"] != "token-value" || nested["tokenExpiresAt"] != "changed" || accounts[0].(map[string]any)["password"] != "secret-b" || accounts[1].(map[string]any)["password"] != "secret-a" {
		t.Fatalf("stored config = %#v", stored)
	}
	if _, err := service.Save(ctx, providerconfig.Config{ID: "new", Type: "virtual", Config: []byte(`{"password":"********"}`)}); err == nil {
		t.Fatal("new provider accepted a redacted secret placeholder")
	}
}

func TestProviderServiceExposesSanitizedRuntimeDiagnostics(t *testing.T) {
	config := providerconfig.Config{ID: "virtual-main", Type: "virtual", Name: "Virtual", Enabled: true, Config: json.RawMessage(`{"devices":[]}`)}
	service := application.NewProviderService([]providerconfig.Config{config}, &providerStore{items: map[string]providerconfig.Config{config.ID: config}}, providersdk.NewFactory(), &diagnosticRuntime{})
	items := service.List()
	if len(items) != 1 || items[0].Diagnostics["cloudMqttState"] != "connected" {
		t.Fatalf("provider diagnostics=%#v", items)
	}
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
	var changed []string
	service.SetChangeHandler(func(_ context.Context, item providerconfig.Config, deleted bool) error {
		changed = append(changed, item.ID+":"+map[bool]string{false: "saved", true: "deleted"}[deleted])
		return nil
	})
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
	if strings.Join(changed, ",") != "virtual-lab:saved,virtual-lab:saved,virtual-lab:deleted" {
		t.Fatalf("change callbacks = %#v", changed)
	}
}

func TestProviderServiceProtectsCameraControlAndCredentialReferences(t *testing.T) {
	ctx := context.Background()
	source := providerconfig.Config{
		ID: "xiaomi-main", Type: "xiaomi", Name: "Xiaomi", Enabled: false,
		Config: json.RawMessage(`{"devices":[{"id":"camera-source"},{"id":"other"}]}`),
	}
	camera := providerconfig.Config{
		ID: "camera-main", Type: "camera", Name: "Cameras", Enabled: false,
		Config: json.RawMessage(`{"cameras":[{"id":"camera","control":{"providerRef":"xiaomi-main","deviceId":"camera-source"},"xiaomi":{"credentialProviderRef":"xiaomi-main"}}]}`),
	}
	store := &providerStore{items: map[string]providerconfig.Config{source.ID: source, camera.ID: camera}}
	factory := providersdk.NewFactory()
	if err := factory.Register("xiaomi", func(item providerconfig.Config) (providersdk.Provider, error) {
		return &configOnlyProvider{id: item.ID}, nil
	}); err != nil {
		t.Fatal(err)
	}
	service := application.NewProviderService([]providerconfig.Config{source, camera}, store, factory, &diagnosticRuntime{})
	if err := service.Delete(ctx, source.ID); err == nil || !strings.Contains(err.Error(), "referenced") {
		t.Fatalf("delete referenced provider error = %v", err)
	}
	if _, exists := store.items[source.ID]; !exists {
		t.Fatal("referenced provider was deleted from durable store")
	}
	replacement := source
	replacement.Config = json.RawMessage(`{"devices":[{"id":"other"}]}`)
	if _, err := service.Save(ctx, replacement); err == nil || !strings.Contains(err.Error(), "still referenced") {
		t.Fatalf("remove referenced device error = %v", err)
	}
	if !strings.Contains(string(store.items[source.ID].Config), "camera-source") {
		t.Fatal("referenced source device removal was persisted")
	}
}

func TestConfiguredProviderDeviceIDs(t *testing.T) {
	tests := []struct {
		name          string
		config        providerconfig.Config
		want          []string
		authoritative bool
	}{
		{name: "ordinary provider", config: providerconfig.Config{Type: "xiaomi", Config: json.RawMessage(`{"devices":[{"id":"one"},{"id":"two"},{"id":" "}]}`)}, want: []string{"one", "two"}, authoritative: true},
		{name: "camera provider", config: providerconfig.Config{Type: "camera", Config: json.RawMessage(`{"cameras":[{"id":"camera-one"}]}`)}, want: []string{"camera-one"}, authoritative: true},
		{name: "missing device list", config: providerconfig.Config{Type: "discovery", Config: json.RawMessage(`{"host":"localhost"}`)}},
		{name: "invalid device list", config: providerconfig.Config{Type: "xiaomi", Config: json.RawMessage(`{"devices":null}`)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, authoritative := application.ConfiguredProviderDeviceIDs(test.config)
			if authoritative != test.authoritative {
				t.Fatalf("authoritative = %v, want %v", authoritative, test.authoritative)
			}
			for _, id := range test.want {
				if _, exists := got[id]; !exists {
					t.Fatalf("device IDs = %#v, missing %q", got, id)
				}
			}
			if len(got) != len(test.want) {
				t.Fatalf("device IDs = %#v, want %#v", got, test.want)
			}
		})
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

func TestProviderServiceRenewsPersistsAndAppliesDueCredentials(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	renewals := &atomic.Int32{}
	factory := providersdk.NewFactory()
	if err := factory.Register("renewable", func(item providerconfig.Config) (providersdk.Provider, error) {
		var config struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(item.Config, &config); err != nil {
			return nil, err
		}
		return &renewableProvider{id: item.ID, version: config.Version, renewals: renewals}, nil
	}); err != nil {
		t.Fatal(err)
	}
	config := providerconfig.Config{ID: "renewable-main", Type: "renewable", Name: "Renewable", Enabled: true, Config: json.RawMessage(`{"version":1}`)}
	current, err := factory.Create(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := providermanager.New(current)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(ctx)
	if err := runtime.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	store := &providerStore{items: map[string]providerconfig.Config{config.ID: config}}
	service := application.NewProviderService([]providerconfig.Config{config}, store, factory, runtime)
	next, err := service.RefreshDueCredentials(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if renewals.Load() != 1 || string(store.items[config.ID].Config) != `{"version":2}` || !next.After(now) {
		t.Fatalf("renewals=%d stored=%s next=%s", renewals.Load(), store.items[config.ID].Config, next)
	}
}

func TestProviderServiceBacksOffFailedCredentialRenewal(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	renewals := &atomic.Int32{}
	factory := providersdk.NewFactory()
	if err := factory.Register("renewable", func(item providerconfig.Config) (providersdk.Provider, error) {
		return &renewableProvider{id: item.ID, version: 1, renewals: renewals, renewError: errors.New("cloud unavailable")}, nil
	}); err != nil {
		t.Fatal(err)
	}
	config := providerconfig.Config{ID: "renewable-main", Type: "renewable", Enabled: true, Config: json.RawMessage(`{"version":1}`)}
	current, _ := factory.Create(config)
	runtime, _ := providermanager.New(current)
	_ = runtime.Initialize(ctx)
	defer runtime.Close(ctx)
	store := &providerStore{items: map[string]providerconfig.Config{config.ID: config}}
	service := application.NewProviderService([]providerconfig.Config{config}, store, factory, runtime)
	if _, err := service.RefreshDueCredentials(ctx, now); err == nil {
		t.Fatal("renewal failure was hidden")
	}
	if _, err := service.RefreshDueCredentials(ctx, now.Add(30*time.Second)); err != nil {
		t.Fatalf("backoff pass=%v", err)
	}
	if renewals.Load() != 1 {
		t.Fatalf("renewals during backoff=%d", renewals.Load())
	}
	info := service.List()[0]
	if info.CredentialError == "" || info.CredentialRetryAt == nil || !info.CredentialRetryAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("provider info=%#v", info)
	}
}

func TestProviderServiceRetriesRuntimeApplyWithoutRenewingAgain(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	renewals := &atomic.Int32{}
	factory := providersdk.NewFactory()
	if err := factory.Register("renewable", func(item providerconfig.Config) (providersdk.Provider, error) {
		var config struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(item.Config, &config); err != nil {
			return nil, err
		}
		return &renewableProvider{id: item.ID, version: config.Version, renewals: renewals}, nil
	}); err != nil {
		t.Fatal(err)
	}
	config := providerconfig.Config{ID: "renewable-main", Type: "renewable", Enabled: true, Config: json.RawMessage(`{"version":1}`)}
	store := &providerStore{items: map[string]providerconfig.Config{config.ID: config}}
	runtime := &renewalRuntime{}
	runtime.failFirst.Store(true)
	service := application.NewProviderService([]providerconfig.Config{config}, store, factory, runtime)
	if _, err := service.RefreshDueCredentials(ctx, now); err == nil {
		t.Fatal("runtime apply failure was hidden")
	}
	if string(store.items[config.ID].Config) != `{"version":2}` || renewals.Load() != 1 {
		t.Fatalf("stored=%s renewals=%d", store.items[config.ID].Config, renewals.Load())
	}
	if _, err := service.RefreshDueCredentials(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if renewals.Load() != 1 || runtime.applyCalls.Load() != 2 {
		t.Fatalf("renewals=%d apply calls=%d", renewals.Load(), runtime.applyCalls.Load())
	}
}

func TestProviderServiceConnectionTestDoesNotPersistOrApply(t *testing.T) {
	store := &providerStore{items: make(map[string]providerconfig.Config)}
	factory := providersdk.NewFactory()
	if err := factory.Register("virtual", func(item providerconfig.Config) (providersdk.Provider, error) {
		return virtual.NewProviderFromConfig(item)
	}); err != nil {
		t.Fatal(err)
	}
	runtime, _ := providermanager.New()
	service := application.NewProviderService(nil, store, factory, runtime)
	if err := service.TestConnection(context.Background(), providerconfig.Config{Type: "virtual", Config: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if len(store.items) != 0 || len(service.List()) != 0 || len(runtime.ProviderInfos()) != 0 {
		t.Fatalf("connection test changed desired or runtime state: store=%#v list=%#v runtime=%#v", store.items, service.List(), runtime.ProviderInfos())
	}
}

func TestProviderServiceRestartsEnabledProvider(t *testing.T) {
	ctx := context.Background()
	config := providerconfig.Config{ID: "virtual-main", Type: "virtual", Name: "Virtual", Enabled: true, Config: []byte(`{}`)}
	store := &providerStore{items: map[string]providerconfig.Config{config.ID: config}}
	factory := providersdk.NewFactory()
	if err := factory.Register("virtual", func(item providerconfig.Config) (providersdk.Provider, error) {
		return virtual.NewProviderFromConfig(item)
	}); err != nil {
		t.Fatal(err)
	}
	runtime, _ := providermanager.New()
	_ = runtime.Initialize(ctx)
	service := application.NewProviderService([]providerconfig.Config{config}, store, factory, runtime)
	info, err := service.Restart(ctx, config.ID)
	if err != nil || info.Status != "running" {
		t.Fatalf("restart = %#v, %v", info, err)
	}
	if _, err := service.Restart(ctx, "missing"); err == nil {
		t.Fatal("missing provider restart succeeded")
	}
}
