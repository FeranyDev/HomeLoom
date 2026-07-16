package xiaomi

import (
	"context"
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
	result := make([]cloudProperty, len(input))
	for index, item := range input {
		item.Value = f.values[miotKey(item.SIID, item.PIID)]
		result[index] = item
	}
	return result, nil
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
