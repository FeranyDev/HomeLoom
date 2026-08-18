package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/gree"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/feranydev/homeloom/backend/internal/providers/xiaomi"
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
)

type renewableProvider struct {
	id         string
	version    int
	renewals   *atomic.Int32
	renewError error
}

type credentialRevokingProvider struct {
	id     string
	result providersdk.CredentialRevocation
}

type credentialRevocationRuntime struct {
	removed []string
	err     error
}

func (p *credentialRevokingProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "xiaomi", Name: p.id}
}
func (*credentialRevokingProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{}
}
func (*credentialRevokingProvider) Initialize(context.Context) error { return nil }
func (*credentialRevokingProvider) Close(context.Context) error      { return nil }
func (p *credentialRevokingProvider) RevokeCredentials(context.Context) (providersdk.CredentialRevocation, error) {
	return p.result, nil
}
func (*credentialRevocationRuntime) Apply(context.Context, providersdk.Provider) error { return nil }
func (r *credentialRevocationRuntime) Remove(_ context.Context, id string) error {
	r.removed = append(r.removed, id)
	return r.err
}
func (*credentialRevocationRuntime) ProviderInfos() []providersdk.RuntimeInfo { return nil }

type renewalRuntime struct {
	applyCalls atomic.Int32
	failFirst  atomic.Bool
}

type diagnosticRuntime struct{}

type configOnlyProvider struct{ id string }

type connectionTestProvider struct {
	id          string
	initialized *atomic.Int32
	tested      *atomic.Int32
	closed      *atomic.Int32
	testError   error
}

type networkScanProvider struct {
	id     string
	scans  *atomic.Int32
	closed *atomic.Int32
}

type configChangeProvider struct {
	id      string
	handler func(previous, replacement json.RawMessage)
}

type configChangeRuntime struct{ provider *configChangeProvider }

type authProviderState struct {
	mu       sync.Mutex
	verified bool
	apply    int
	close    int
	url      string
}

type authProvider struct {
	id    string
	state *authProviderState
}

type authProviderRuntime struct {
	state *authProviderState
}

func (p *authProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: xiaomi.XiaomiMIoTCloudProviderType, Name: "Cloud"}
}
func (*authProvider) Capabilities() providersdk.Capabilities { return providersdk.Capabilities{} }
func (p *authProvider) Initialize(context.Context) error {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	if !p.state.verified {
		return &xiaomi.IdentityVerificationRequiredError{URL: p.state.url}
	}
	return nil
}
func (p *authProvider) Close(context.Context) error {
	p.state.mu.Lock()
	p.state.close++
	p.state.mu.Unlock()
	return nil
}
func (p *authProvider) IdentityVerificationURL() (string, bool) {
	p.state.mu.Lock()
	url := p.state.url
	verified := p.state.verified
	p.state.mu.Unlock()
	return url, !verified && url != ""
}
func (p *authProvider) CompleteIdentityVerification(_ context.Context, code string) (json.RawMessage, error) {
	if code == "bad-code" {
		return nil, errors.New("Xiaomi identity verification code was rejected")
	}
	p.state.mu.Lock()
	p.state.verified = true
	p.state.mu.Unlock()
	return json.RawMessage(`{"region":"cn","userId":"42","ssecurity":"security","serviceToken":"service-token","devices":[]}`), nil
}
func (r *authProviderRuntime) Apply(ctx context.Context, item providersdk.Provider) error {
	r.state.mu.Lock()
	r.state.apply++
	r.state.mu.Unlock()
	return item.Initialize(ctx)
}
func (*authProviderRuntime) Remove(context.Context, string) error { return nil }
func (r *authProviderRuntime) ProviderInfos() []providersdk.RuntimeInfo {
	r.state.mu.Lock()
	verified := r.state.verified
	r.state.mu.Unlock()
	if verified {
		return []providersdk.RuntimeInfo{{Manifest: providersdk.Manifest{ID: "cloud-main", Type: xiaomi.XiaomiMIoTCloudProviderType}, Status: "running"}}
	}
	return []providersdk.RuntimeInfo{{Manifest: providersdk.Manifest{ID: "cloud-main", Type: xiaomi.XiaomiMIoTCloudProviderType}, Status: "auth_required"}}
}

func (p *configOnlyProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "catalog", Name: p.id}
}
func (*configOnlyProvider) Capabilities() providersdk.Capabilities { return providersdk.Capabilities{} }
func (*configOnlyProvider) Initialize(context.Context) error       { return nil }
func (*configOnlyProvider) Close(context.Context) error            { return nil }

func (p *connectionTestProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "checked", Name: p.id}
}
func (*connectionTestProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{}
}
func (p *connectionTestProvider) Initialize(context.Context) error {
	p.initialized.Add(1)
	return nil
}
func (p *connectionTestProvider) Close(context.Context) error {
	p.closed.Add(1)
	return nil
}

func (p *configChangeProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "config-change", Name: p.id}
}
func (*configChangeProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{}
}
func (*configChangeProvider) Initialize(context.Context) error { return nil }
func (*configChangeProvider) Close(context.Context) error      { return nil }
func (p *configChangeProvider) SetRuntimeConfigChangeHandler(handler func(previous, replacement json.RawMessage)) {
	p.handler = handler
}
func (p *configChangeProvider) emit(previous, replacement json.RawMessage) {
	if p.handler != nil {
		p.handler(previous, replacement)
	}
}
func (*configChangeRuntime) Apply(context.Context, providersdk.Provider) error { return nil }
func (*configChangeRuntime) Remove(context.Context, string) error              { return nil }
func (r *configChangeRuntime) ProviderInfos() []providersdk.RuntimeInfo {
	return []providersdk.RuntimeInfo{{Manifest: r.provider.Manifest(), Status: "running"}}
}
func (r *configChangeRuntime) ProviderAny(id string) (providersdk.Provider, bool) {
	return r.provider, r.provider.id == id
}
func (p *connectionTestProvider) TestConnection(context.Context) error {
	p.tested.Add(1)
	return p.testError
}

func (p *networkScanProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "scan", Name: p.id}
}
func (*networkScanProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{}
}
func (*networkScanProvider) Initialize(context.Context) error { return nil }
func (p *networkScanProvider) Close(context.Context) error {
	p.closed.Add(1)
	return nil
}
func (p *networkScanProvider) Scan(context.Context) ([]providersdk.DiscoveryCandidate, error) {
	p.scans.Add(1)
	return []providersdk.DiscoveryCandidate{{Provider: "scan", Name: "LAN device", Host: "192.0.2.20", Port: 7000, MAC: "aabbccddeeff"}}, nil
}

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

func TestProviderServicePersistsRuntimeAddressRepairWithoutOverwritingNewerConfig(t *testing.T) {
	original := providerconfig.Config{ID: "xiaomi-main", Type: "xiaomi", Name: "Xiaomi", Enabled: true, Config: json.RawMessage(`{"host":"192.168.101.201","port":8883,"gatewayDid":"123456789","privateKey":"secret"}`)}
	store := &providerStore{items: map[string]providerconfig.Config{original.ID: original}}
	provider := &configChangeProvider{id: original.ID}
	service := application.NewProviderService([]providerconfig.Config{original}, store, providersdk.NewFactory(), &configChangeRuntime{provider: provider})

	recovered := json.RawMessage(`{"host":"192.168.101.200","port":8883,"gatewayDid":"123456789","privateKey":"secret"}`)
	provider.emit(original.Config, recovered)
	if got := string(store.items[original.ID].Config); got != string(recovered) {
		t.Fatalf("persisted recovered config = %s", got)
	}
	listed := service.List()
	if len(listed) != 1 || !strings.Contains(string(listed[0].Config.Config), `"host":"192.168.101.200"`) {
		t.Fatalf("listed config = %#v", listed)
	}

	provider.emit(original.Config, json.RawMessage(`{"host":"192.168.101.199"}`))
	if got := string(store.items[original.ID].Config); got != string(recovered) {
		t.Fatalf("stale update overwrote recovered config: %s", got)
	}
}

func TestProviderServiceRedactsAndRestoresSecrets(t *testing.T) {
	ctx := context.Background()
	original := providerconfig.Config{ID: "virtual-secret", Type: "virtual", Name: "Secret", Config: []byte(`{"password":"keep-me","ssecurity":"miot-security","nested":{"accessToken":"token-value","deviceKey":"sonoff-key","tokenExpiresAt":"public"},"accounts":[{"id":"a","password":"secret-a"},{"id":"b","password":"secret-b"}],"devices":[]}`)}
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
	if len(listed) != 1 || string(listed[0].Config.Config) != `{"accounts":[{"id":"a","password":"********"},{"id":"b","password":"********"}],"devices":[],"nested":{"accessToken":"********","deviceKey":"********","tokenExpiresAt":"public"},"password":"********","ssecurity":"********"}` {
		t.Fatalf("redacted config = %s", listed[0].Config.Config)
	}
	resolved, err := service.ResolveTransientConfig(listed[0].Config)
	if err != nil || !strings.Contains(string(resolved.Config), `"password":"keep-me"`) || !strings.Contains(string(resolved.Config), `"accessToken":"token-value"`) || !strings.Contains(string(resolved.Config), `"deviceKey":"sonoff-key"`) || !strings.Contains(string(resolved.Config), `"ssecurity":"miot-security"`) {
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

func TestProviderServiceRedactsAndRestoresGreeEncryptionKey(t *testing.T) {
	const encryptionKey = "0123456789abcdef"

	original := providerconfig.Config{
		ID: "gree-main", Type: gree.ProviderType, Name: "Gree", Config: json.RawMessage(`{
			"devices":[{"id":"living-ac","name":"Living AC","host":"192.0.2.10","mac":"AA:BB:CC:DD:EE:FF","encryptionKey":"0123456789abcdef"}]
		}`),
	}
	store := &providerStore{items: map[string]providerconfig.Config{original.ID: original}}
	factory := providersdk.NewFactory()
	if err := factory.Register(gree.ProviderType, func(item providerconfig.Config) (providersdk.Provider, error) {
		return gree.NewProviderFromConfig(item)
	}); err != nil {
		t.Fatal(err)
	}
	runtime, err := providermanager.New()
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewProviderService([]providerconfig.Config{original}, store, factory, runtime)

	listed := service.List()
	if len(listed) != 1 {
		t.Fatalf("listed providers = %#v", listed)
	}
	var listedConfig struct {
		Devices []struct {
			EncryptionKey string `json:"encryptionKey"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(listed[0].Config.Config, &listedConfig); err != nil {
		t.Fatal(err)
	}
	if len(listedConfig.Devices) != 1 || listedConfig.Devices[0].EncryptionKey != "********" || strings.Contains(string(listed[0].Config.Config), encryptionKey) {
		t.Fatalf("listed Gree config = %s", listed[0].Config.Config)
	}

	edited := listed[0].Config
	edited.Config = json.RawMessage(`{
		"devices":[{"id":"living-ac","name":"Living AC","host":"192.0.2.11","mac":"AA:BB:CC:DD:EE:FF","encryptionKey":"********"}]
	}`)
	info, err := service.Save(context.Background(), edited)
	if err != nil {
		t.Fatal(err)
	}
	var storedConfig struct {
		Devices []struct {
			Host          string `json:"host"`
			EncryptionKey string `json:"encryptionKey"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(store.items[original.ID].Config, &storedConfig); err != nil {
		t.Fatal(err)
	}
	if len(storedConfig.Devices) != 1 || storedConfig.Devices[0].Host != "192.0.2.11" || storedConfig.Devices[0].EncryptionKey != encryptionKey {
		t.Fatalf("stored Gree config = %s", store.items[original.ID].Config)
	}
	if strings.Contains(string(info.Config.Config), encryptionKey) || !strings.Contains(string(info.Config.Config), `"encryptionKey":"********"`) {
		t.Fatalf("save response exposed Gree encryptionKey: %s", info.Config.Config)
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

func TestProviderServiceRetainsAndCompletesXiaomiAuthChallenge(t *testing.T) {
	ctx := context.Background()
	state := &authProviderState{url: "https://account.xiaomi.com/identity/authStart?context=short"}
	store := &providerStore{items: make(map[string]providerconfig.Config)}
	factory := providersdk.NewFactory()
	if err := factory.Register(xiaomi.XiaomiMIoTCloudProviderType, func(item providerconfig.Config) (providersdk.Provider, error) {
		return &authProvider{id: item.ID, state: state}, nil
	}); err != nil {
		t.Fatal(err)
	}
	runtime := &authProviderRuntime{state: state}
	service := application.NewProviderService(nil, store, factory, runtime)
	config := providerconfig.Config{ID: "cloud-main", Type: xiaomi.XiaomiMIoTCloudProviderType, Name: "Cloud", Enabled: true, Config: json.RawMessage(`{"region":"cn","username":"owner","password":"secret","devices":[]}`)}
	info, err := service.Save(ctx, config)
	var challengeErr *application.ProviderAuthChallengeError
	if !errors.As(err, &challengeErr) || info.AuthChallenge == nil {
		t.Fatalf("Save() info=%#v error=%v", info, err)
	}
	challenge := *info.AuthChallenge
	if challenge.Status != "auth_required" || challenge.ChallengeID == "" || challenge.VerificationURL == "" || !challenge.ExpiresAt.After(time.Now()) {
		t.Fatalf("challenge=%#v", challenge)
	}
	listed := service.List()
	if len(listed) != 1 || listed[0].Status != "auth_required" || listed[0].AuthChallenge == nil || listed[0].AuthChallenge.ChallengeID != challenge.ChallengeID {
		t.Fatalf("listed=%#v", listed)
	}
	if strings.Contains(string(listed[0].Config.Config), "secret") {
		t.Fatalf("provider list leaked password: %s", listed[0].Config.Config)
	}
	if _, err := service.VerifyAuthChallenge(ctx, config.ID, challenge.ChallengeID, "bad-code"); err == nil || strings.Contains(err.Error(), "bad-code") {
		t.Fatalf("bad-code error=%v", err)
	}
	if _, ok := service.GetAuthChallenge(config.ID); !ok {
		t.Fatal("failed verification discarded challenge")
	}
	verified, err := service.VerifyAuthChallenge(ctx, config.ID, challenge.ChallengeID, "123456")
	if err != nil {
		t.Fatal(err)
	}
	if verified.Status != "running" || verified.AuthChallenge != nil {
		t.Fatalf("verified provider=%#v", verified)
	}
	var stored struct {
		Password     string `json:"password"`
		UserID       string `json:"userId"`
		Ssecurity    string `json:"ssecurity"`
		ServiceToken string `json:"serviceToken"`
	}
	if err := json.Unmarshal(store.items[config.ID].Config, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Password != "" || stored.UserID != "42" || stored.Ssecurity != "security" || stored.ServiceToken != "service-token" {
		t.Fatalf("stored credentials=%#v", stored)
	}
	if _, err := service.VerifyAuthChallenge(ctx, config.ID, challenge.ChallengeID, "123456"); err == nil || !strings.Contains(err.Error(), "missing or expired") {
		t.Fatalf("challenge reuse error=%v", err)
	}
	state.mu.Lock()
	applyCalls, closes := state.apply, state.close
	state.mu.Unlock()
	if applyCalls != 2 || closes == 0 {
		t.Fatalf("runtime apply/close=%d/%d", applyCalls, closes)
	}
}

func TestProviderServiceSerializesConcurrentXiaomiAuthSuccess(t *testing.T) {
	state := &authProviderState{url: "https://account.xiaomi.com/identity/authStart"}
	store := &providerStore{items: make(map[string]providerconfig.Config)}
	factory := providersdk.NewFactory()
	if err := factory.Register(xiaomi.XiaomiMIoTCloudProviderType, func(item providerconfig.Config) (providersdk.Provider, error) {
		return &authProvider{id: item.ID, state: state}, nil
	}); err != nil {
		t.Fatal(err)
	}
	service := application.NewProviderService(nil, store, factory, &authProviderRuntime{state: state})
	config := providerconfig.Config{ID: "cloud-concurrent", Type: xiaomi.XiaomiMIoTCloudProviderType, Enabled: true, Config: json.RawMessage(`{"region":"cn","username":"owner","password":"secret","devices":[]}`)}
	info, err := service.Save(context.Background(), config)
	var challengeErr *application.ProviderAuthChallengeError
	if !errors.As(err, &challengeErr) || info.AuthChallenge == nil {
		t.Fatalf("Save() info=%#v error=%v", info, err)
	}
	challengeID := info.AuthChallenge.ChallengeID
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, verifyErr := service.VerifyAuthChallenge(context.Background(), config.ID, challengeID, "123456")
			errs <- verifyErr
		}()
	}
	var successes, failures int
	for i := 0; i < 2; i++ {
		if err := <-errs; err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent verification successes/failures=%d/%d", successes, failures)
	}
	state.mu.Lock()
	applyCalls := state.apply
	state.mu.Unlock()
	if applyCalls != 2 {
		t.Fatalf("runtime apply calls=%d", applyCalls)
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

func TestProviderServiceRevokesXiaomiCredentialsDurablyBeforeDisconnectOutcome(t *testing.T) {
	original := providerconfig.Config{ID: "xiaomi-main", Type: "xiaomi", Name: "Xiaomi", Enabled: true, Config: json.RawMessage(`{"accessToken":"secret-token","devices":[]}`)}
	store := &providerStore{items: map[string]providerconfig.Config{original.ID: original}}
	factory := providersdk.NewFactory()
	if err := factory.Register("xiaomi", func(item providerconfig.Config) (providersdk.Provider, error) {
		return &credentialRevokingProvider{id: item.ID, result: providersdk.CredentialRevocation{
			Config: json.RawMessage(`{"credentialsRevoked":true,"devices":[]}`), RemoteAttempted: true, RemoteError: "configured endpoint failed",
		}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	runtime := &credentialRevocationRuntime{err: errors.New("disconnect timed out")}
	service := application.NewProviderService([]providerconfig.Config{original}, store, factory, runtime)
	changed := 0
	service.SetChangeHandler(func(context.Context, providerconfig.Config, bool) error {
		changed++
		return errors.New("downstream refresh delayed")
	})

	result, err := service.RevokeXiaomiCredentials(context.Background(), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.LocalRevoked || !result.RemoteAttempted || result.RemoteError == "" || result.DisconnectError == "" || result.ReconciliationError == "" {
		t.Fatalf("revoke result = %#v", result)
	}
	stored := store.items[original.ID]
	if stored.Enabled || strings.Contains(string(stored.Config), "secret-token") || !strings.Contains(string(stored.Config), `"credentialsRevoked":true`) {
		t.Fatalf("stored revoked provider = %#v", stored)
	}
	if len(runtime.removed) != 1 || runtime.removed[0] != original.ID || changed != 1 {
		t.Fatalf("runtime removals=%v changes=%d", runtime.removed, changed)
	}
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

func TestProviderServiceConnectionTestUsesOptionalProviderCheck(t *testing.T) {
	testError := errors.New("live device unavailable")
	initialized := &atomic.Int32{}
	tested := &atomic.Int32{}
	closed := &atomic.Int32{}
	factory := providersdk.NewFactory()
	if err := factory.Register("checked", func(item providerconfig.Config) (providersdk.Provider, error) {
		return &connectionTestProvider{id: item.ID, initialized: initialized, tested: tested, closed: closed, testError: testError}, nil
	}); err != nil {
		t.Fatal(err)
	}
	runtime, _ := providermanager.New()
	service := application.NewProviderService(nil, &providerStore{items: make(map[string]providerconfig.Config)}, factory, runtime)
	if err := service.TestConnection(context.Background(), providerconfig.Config{Type: "checked", Config: []byte("{}")}); !errors.Is(err, testError) {
		t.Fatalf("TestConnection() error = %v, want %v", err, testError)
	}
	if initialized.Load() != 0 || tested.Load() != 1 || closed.Load() != 1 {
		t.Fatalf("optional check lifecycle = initialized %d, tested %d, closed %d", initialized.Load(), tested.Load(), closed.Load())
	}
}

func TestProviderServiceScansTransientProviderWithoutPersistingOrUsingRuntimeCatalog(t *testing.T) {
	scans := &atomic.Int32{}
	closed := &atomic.Int32{}
	factory := providersdk.NewFactory()
	if err := factory.Register("scan", func(item providerconfig.Config) (providersdk.Provider, error) {
		return &networkScanProvider{id: item.ID, scans: scans, closed: closed}, nil
	}); err != nil {
		t.Fatal(err)
	}
	store := &providerStore{items: make(map[string]providerconfig.Config)}
	runtime, err := providermanager.New()
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewProviderService(nil, store, factory, runtime)
	items, err := service.Scan(context.Background(), providerconfig.Config{Type: "scan", Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Host != "192.0.2.20" {
		t.Fatalf("scan result = %#v", items)
	}
	if scans.Load() != 1 || closed.Load() != 1 {
		t.Fatalf("transient scanner lifecycle = scans %d, closed %d", scans.Load(), closed.Load())
	}
	if len(store.items) != 0 || len(service.List()) != 0 || len(runtime.ProviderInfos()) != 0 {
		t.Fatalf("scan changed desired or runtime state: store=%#v list=%#v runtime=%#v", store.items, service.List(), runtime.ProviderInfos())
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
