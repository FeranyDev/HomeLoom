package xiaomi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type fakeMIoTCloud struct {
	mu        sync.Mutex
	login     int
	directory []HubDevice
	values    map[string]any
	writes    []cloudProperty
	actions   []cloudAction
	reads     int
}

func (f *fakeMIoTCloud) Login(context.Context) error {
	f.mu.Lock()
	f.login++
	f.mu.Unlock()
	return nil
}
func (f *fakeMIoTCloud) DeviceList(context.Context) ([]HubDevice, error) {
	return append([]HubDevice(nil), f.directory...), nil
}
func (f *fakeMIoTCloud) GetProperties(_ context.Context, input []cloudProperty) ([]cloudProperty, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	result := make([]cloudProperty, len(input))
	for index, item := range input {
		item.Value = f.values[miotKey(item.SIID, item.PIID)]
		result[index] = item
	}
	return result, nil
}

type fakeMIoTLocal struct {
	mu        sync.Mutex
	values    map[string]any
	readErr   error
	writeErr  error
	actionErr error
	reads     int
	writes    int
	actions   int
}

type fakeIdentityCloud struct {
	mu       sync.Mutex
	login    int
	verified bool
}

func (f *fakeIdentityCloud) Login(context.Context) error {
	f.mu.Lock()
	f.login++
	verified := f.verified
	f.mu.Unlock()
	if !verified {
		return &IdentityVerificationRequiredError{URL: "https://account.xiaomi.com/identity/authStart?context=short"}
	}
	return nil
}
func (*fakeIdentityCloud) DeviceList(context.Context) ([]HubDevice, error) { return nil, nil }
func (*fakeIdentityCloud) GetProperties(context.Context, []cloudProperty) ([]cloudProperty, error) {
	return nil, nil
}
func (*fakeIdentityCloud) SetProperties(context.Context, []cloudProperty) ([]cloudProperty, error) {
	return nil, nil
}
func (*fakeIdentityCloud) Action(context.Context, cloudAction) error { return nil }
func (f *fakeIdentityCloud) VerifyIdentity(_ context.Context, _, code string) error {
	if strings.TrimSpace(code) == "bad" {
		return errors.New("Xiaomi identity verification code was rejected")
	}
	f.mu.Lock()
	f.verified = true
	f.mu.Unlock()
	return nil
}
func (f *fakeIdentityCloud) session() (string, string, string) {
	f.mu.Lock()
	verified := f.verified
	f.mu.Unlock()
	if !verified {
		return "", "", ""
	}
	return "42", "security", "service-token"
}
func (f *fakeIdentityCloud) mediaSession() (string, string) {
	f.mu.Lock()
	verified := f.verified
	f.mu.Unlock()
	if !verified {
		return "", ""
	}
	return "42", "pass-token"
}

func (f *fakeMIoTLocal) GetProperties(_ context.Context, _ miotLocalAccess, input []cloudProperty) ([]cloudProperty, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.readErr != nil {
		return nil, f.readErr
	}
	result := make([]cloudProperty, len(input))
	for index, item := range input {
		item.Value = f.values[miotKey(item.SIID, item.PIID)]
		result[index] = item
	}
	return result, nil
}

func (f *fakeMIoTLocal) SetProperties(_ context.Context, _ miotLocalAccess, input []cloudProperty) ([]cloudProperty, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	return append([]cloudProperty(nil), input...), nil
}

func (f *fakeMIoTLocal) Action(context.Context, miotLocalAccess, cloudAction) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions++
	return f.actionErr
}
func (f *fakeMIoTCloud) SetProperties(_ context.Context, input []cloudProperty) ([]cloudProperty, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, input...)
	for index := range input {
		input[index].Code = 0
	}
	return input, nil
}
func (f *fakeMIoTCloud) Action(_ context.Context, input cloudAction) error {
	f.mu.Lock()
	f.actions = append(f.actions, input)
	f.mu.Unlock()
	return nil
}

func cloudTestConfig() CloudConfig {
	configured := testConfig().Devices[0]
	configured.Actions = []ActionMapping{{EndpointID: "main", CapabilityID: "switch", CommandID: "toggle", SIID: 2, AIID: 1}}
	config := CloudConfig{Region: "cn", UserID: "1", Ssecurity: "security", ServiceToken: "token", Devices: []DeviceConfig{configured}}
	config.applyDefaults()
	return config
}

func TestCloudProviderRunsParallelPollingReadWriteAndActionLifecycle(t *testing.T) {
	fake := &fakeMIoTCloud{directory: []HubDevice{{DID: "123", Name: "云端开关", Model: "chuangmi.switch.v1"}}, values: map[string]any{"2.1": false}}
	provider, err := newCloudProvider("xiaomi-miot-cloud-main", "小米 MIoT 云", cloudTestConfig(), func() miotCloudClient { return fake }, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	if provider.Manifest().Type != XiaomiMIoTCloudProviderType || provider.Capabilities().Events {
		t.Fatalf("manifest/capabilities = %#v/%#v", provider.Manifest(), provider.Capabilities())
	}
	directory, err := provider.DiscoverCloudDevices(ctx)
	if err != nil || len(directory) != 1 || directory[0].Model != "chuangmi.switch.v1" {
		t.Fatalf("directory = %#v, %v", directory, err)
	}
	property, err := provider.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: "xiaomi-switch", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"})
	if err != nil || property.Value.Bool == nil || *property.Value.Bool {
		t.Fatalf("read = %#v, %v", property, err)
	}
	updated, err := provider.WriteProperty(ctx, providersdk.PropertyWriteRequest{DeviceID: "xiaomi-switch", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)})
	if err != nil || !updated.IsOnline() {
		t.Fatalf("write = %#v, %v", updated, err)
	}
	if _, err := provider.ExecuteCommand(ctx, providersdk.CommandRequest{DeviceID: "xiaomi-switch", EndpointID: "main", CapabilityID: "switch", CommandID: "toggle"}); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.login != 1 || len(fake.writes) != 1 || len(fake.actions) != 1 {
		t.Fatalf("calls login=%d writes=%d actions=%d", fake.login, len(fake.writes), len(fake.actions))
	}
}

func TestCloudProviderInitializeIsIdempotent(t *testing.T) {
	fake := &fakeMIoTCloud{values: map[string]any{"2.1": false}}
	provider, err := newCloudProvider("xiaomi-miot-cloud-main", "Cloud", cloudTestConfig(), func() miotCloudClient { return fake }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := provider.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.login != 1 {
		t.Fatalf("login count = %d", fake.login)
	}
}

func TestCloudProviderRetainsIdentityChallengeAndBuildsSessionConfig(t *testing.T) {
	fake := &fakeIdentityCloud{}
	config := cloudTestConfig()
	config.Username, config.Password = "owner@example.com", "secret-password"
	config.UserID, config.Ssecurity, config.ServiceToken = "", "", ""
	provider, err := newCloudProvider("xiaomi-miot-cloud-auth", "Cloud", config, func() miotCloudClient { return fake }, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	err = provider.Initialize(ctx)
	var required *IdentityVerificationRequiredError
	if !errors.As(err, &required) || required.URL == "" {
		t.Fatalf("Initialize() error=%v", err)
	}
	url, ok := provider.IdentityVerificationURL()
	if !ok || url != required.URL {
		t.Fatalf("identity URL=%q ok=%v required=%q", url, ok, required.URL)
	}
	if err := provider.Initialize(ctx); !errors.As(err, &required) {
		t.Fatalf("repeated Initialize() error=%v", err)
	}
	if _, err := provider.CompleteIdentityVerification(ctx, "bad"); err == nil || strings.Contains(err.Error(), "bad") {
		t.Fatalf("bad code error=%v", err)
	}
	encoded, err := provider.CompleteIdentityVerification(ctx, "123456")
	if err != nil {
		t.Fatal(err)
	}
	var persisted CloudConfig
	if err := json.Unmarshal(encoded, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Password != "secret-password" || persisted.UserID != "42" || persisted.Ssecurity != "security" || persisted.ServiceToken != "service-token" || persisted.PassToken != "pass-token" {
		t.Fatalf("persisted config=%#v", persisted)
	}
	if _, ok := provider.IdentityVerificationURL(); ok {
		t.Fatal("identity URL remained after successful verification")
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCloudProviderAutoPrefersLocalForReadWriteAndAction(t *testing.T) {
	token := "30313233343536373839616263646566"
	fakeCloud := &fakeMIoTCloud{directory: []HubDevice{{DID: "123", Name: "LAN switch", LocalIP: "192.168.1.20", Token: token, Local: true}}, values: map[string]any{"2.1": false}}
	fakeLocal := &fakeMIoTLocal{values: map[string]any{"2.1": false}}
	provider, err := newCloudProvider("xiaomi-miot-cloud-main", "Cloud", cloudTestConfig(), func() miotCloudClient { return fakeCloud }, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider.local = fakeLocal
	pending, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(pending) != 1 || pending[0].RuntimeMode != device.RuntimeModePending {
		t.Fatalf("pending devices = %#v, error = %v", pending, err)
	}
	ctx := context.Background()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	if _, err := provider.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: "xiaomi-switch", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.WriteProperty(ctx, providersdk.PropertyWriteRequest{DeviceID: "xiaomi-switch", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ExecuteCommand(ctx, providersdk.CommandRequest{DeviceID: "xiaomi-switch", EndpointID: "main", CapabilityID: "switch", CommandID: "toggle"}); err != nil {
		t.Fatal(err)
	}
	fakeLocal.mu.Lock()
	localReads, localWrites, localActions := fakeLocal.reads, fakeLocal.writes, fakeLocal.actions
	fakeLocal.mu.Unlock()
	fakeCloud.mu.Lock()
	cloudReads, cloudWrites, cloudActions := fakeCloud.reads, len(fakeCloud.writes), len(fakeCloud.actions)
	fakeCloud.mu.Unlock()
	if localReads < 2 || localWrites != 1 || localActions != 1 || cloudReads != 0 || cloudWrites != 0 || cloudActions != 0 {
		t.Fatalf("local=%d/%d/%d cloud=%d/%d/%d", localReads, localWrites, localActions, cloudReads, cloudWrites, cloudActions)
	}
	items, err := provider.DiscoverDevices(ctx)
	if err != nil || len(items) != 1 || items[0].RuntimeMode != device.RuntimeModeLocal {
		t.Fatalf("runtime devices = %#v, error = %v", items, err)
	}
	fakeLocal.mu.Lock()
	fakeLocal.readErr = errors.New("LAN became unavailable")
	fakeLocal.mu.Unlock()
	if _, err := provider.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: "xiaomi-switch", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}); err != nil {
		t.Fatalf("dynamic cloud fallback: %v", err)
	}
	items, err = provider.DiscoverDevices(ctx)
	if err != nil || len(items) != 1 || items[0].RuntimeMode != device.RuntimeModeCloud {
		t.Fatalf("fallback runtime devices = %#v, error = %v", items, err)
	}
}

func TestCloudProviderAutoFallsBackToCloudAfterLocalFailure(t *testing.T) {
	token := "30313233343536373839616263646566"
	fakeCloud := &fakeMIoTCloud{directory: []HubDevice{{DID: "123", Name: "LAN switch", LocalIP: "192.168.1.20", Token: token, Local: true}}, values: map[string]any{"2.1": false}}
	fakeLocal := &fakeMIoTLocal{readErr: errors.New("device timed out"), writeErr: errors.New("device timed out"), actionErr: errors.New("device timed out")}
	provider, err := newCloudProvider("xiaomi-miot-cloud-main", "Cloud", cloudTestConfig(), func() miotCloudClient { return fakeCloud }, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider.local = fakeLocal
	ctx := context.Background()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	property, err := provider.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: "xiaomi-switch", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"})
	if err != nil || property.Value.Bool == nil || *property.Value.Bool {
		t.Fatalf("property=%#v error=%v", property, err)
	}
	if _, err := provider.WriteProperty(ctx, providersdk.PropertyWriteRequest{DeviceID: "xiaomi-switch", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)}); err != nil {
		t.Fatalf("fallback write: %v", err)
	}
	if _, err := provider.ExecuteCommand(ctx, providersdk.CommandRequest{DeviceID: "xiaomi-switch", EndpointID: "main", CapabilityID: "switch", CommandID: "toggle"}); err != nil {
		t.Fatalf("fallback action: %v", err)
	}
	metrics := provider.ProviderMetrics()
	if metrics["localFailures"] < 4 || metrics["cloudFallbacks"] < 4 {
		t.Fatalf("metrics = %#v", metrics)
	}
	fakeCloud.mu.Lock()
	defer fakeCloud.mu.Unlock()
	if len(fakeCloud.writes) != 1 || len(fakeCloud.actions) != 1 {
		t.Fatalf("cloud writes/actions = %d/%d", len(fakeCloud.writes), len(fakeCloud.actions))
	}
	items, err := provider.DiscoverDevices(ctx)
	if err != nil || len(items) != 1 || items[0].RuntimeMode != device.RuntimeModeCloud {
		t.Fatalf("runtime devices = %#v, error = %v", items, err)
	}
}

func TestCloudProviderCloudModeSkipsAvailableLocalTransport(t *testing.T) {
	config := cloudTestConfig()
	config.Devices[0].ConnectionMode = cloudConnectionCloud
	fakeCloud := &fakeMIoTCloud{directory: []HubDevice{{DID: "123", Name: "LAN switch", LocalIP: "192.168.1.20", Token: "30313233343536373839616263646566", Local: true}}, values: map[string]any{"2.1": false}}
	fakeLocal := &fakeMIoTLocal{values: map[string]any{"2.1": true}}
	provider, err := newCloudProvider("xiaomi-miot-cloud-main", "Cloud", config, func() miotCloudClient { return fakeCloud }, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider.local = fakeLocal
	ctx := context.Background()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	if _, err := provider.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: "xiaomi-switch", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}); err != nil {
		t.Fatal(err)
	}
	fakeLocal.mu.Lock()
	defer fakeLocal.mu.Unlock()
	if fakeLocal.reads != 0 {
		t.Fatalf("local reads = %d", fakeLocal.reads)
	}
	items, err := provider.DiscoverDevices(ctx)
	if err != nil || len(items) != 1 || items[0].RuntimeMode != device.RuntimeModeCloud {
		t.Fatalf("runtime devices = %#v, error = %v", items, err)
	}
}

func TestCloudProviderAddsMappedDevicesWithoutCreatingAnotherCloudSession(t *testing.T) {
	fake := &fakeMIoTCloud{values: map[string]any{"2.1": false}}
	config := cloudTestConfig()
	provider, err := newCloudProvider("xiaomi-miot-cloud-main", "Cloud", config, func() miotCloudClient { return fake }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(context.Background())
	second := config.Devices[0]
	second.DID, second.ID, second.Name = "456", "xiaomi-miot-second", "Second"
	nextConfig := config
	nextConfig.Devices = append(append([]DeviceConfig(nil), config.Devices...), second)
	replacement, err := newCloudProvider("xiaomi-miot-cloud-main", "Cloud", nextConfig, func() miotCloudClient { return fake }, nil)
	if err != nil {
		t.Fatal(err)
	}
	handled, err := provider.Reconfigure(context.Background(), replacement)
	if err != nil || !handled {
		t.Fatalf("reconfigure handled=%v error=%v", handled, err)
	}
	items, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(items) != 2 {
		t.Fatalf("devices = %#v, %v", items, err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.login != 1 {
		t.Fatalf("login count = %d", fake.login)
	}
}
