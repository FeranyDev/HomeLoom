package tuya

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	tuyaapi "github.com/feranydev/homeloom/backend/internal/providers/tuya/api"
)

type fakeAPI struct {
	devices      []TuyaDevice
	specs        map[string]TuyaSpecification
	statuses     map[string][]TuyaStatus
	commands     []fakeCommand
	refreshToken Token
	specCalls    map[string]int
	statusCalls  map[string]int
}

type fakeCommand struct {
	deviceID string
	items    []TuyaCommand
}

func (f *fakeAPI) GetToken(context.Context) (Token, error) {
	return Token{AccessToken: "token", UID: "user"}, nil
}
func (f *fakeAPI) RefreshToken(context.Context, string) (Token, error) {
	if f.refreshToken.AccessToken != "" {
		return f.refreshToken, nil
	}
	return Token{AccessToken: "token", UID: "user"}, nil
}
func (f *fakeAPI) SetAccessToken(string) {}
func (f *fakeAPI) ListUserDevices(context.Context, string, int, int) ([]TuyaDevice, error) {
	return append([]TuyaDevice(nil), f.devices...), nil
}
func (f *fakeAPI) GetSpecification(_ context.Context, id string) (TuyaSpecification, error) {
	if f.specCalls == nil {
		f.specCalls = make(map[string]int)
	}
	f.specCalls[id]++
	return f.specs[id], nil
}
func (f *fakeAPI) GetStatus(_ context.Context, id string) ([]TuyaStatus, error) {
	if f.statusCalls == nil {
		f.statusCalls = make(map[string]int)
	}
	f.statusCalls[id]++
	return append([]TuyaStatus(nil), f.statuses[id]...), nil
}
func (f *fakeAPI) SendCommands(_ context.Context, id string, commands []TuyaCommand) error {
	f.commands = append(f.commands, fakeCommand{deviceID: id, items: append([]TuyaCommand(nil), commands...)})
	return nil
}

var _ tuyaapi.API = (*fakeAPI)(nil)

func testProvider(t *testing.T, api *fakeAPI) *Provider {
	t.Helper()
	config, _ := json.Marshal(Config{AccessID: "access", AccessSecret: "secret", UID: "user", AccessToken: "token"})
	provider, err := NewProviderWithAPI(providerconfig.Config{ID: "tuya-main", Name: "Tuya", Type: ProviderType, Config: config}, api)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestProviderRefreshPublishesDynamicSourceCatalog(t *testing.T) {
	minimum, maximum, step := 10.0, 1000.0, 5.0
	api := &fakeAPI{
		devices: []TuyaDevice{{ID: "device-1", Name: "客厅灯", Category: "dj", ProductID: "product-1", ProductName: "Tuya Lamp", Online: true, Status: []TuyaStatus{{Code: "switch_led", Value: true}, {Code: "bright_value", Value: 235}}}},
		specs:   map[string]TuyaSpecification{"device-1": {Category: "dj", Functions: []DPSpec{{Code: "switch_led", Type: DPTypeBoolean}, {Code: "bright_value", Type: DPTypeInteger, Min: &minimum, Max: &maximum, Step: &step, Scale: 1}}, Status: []DPSpec{{Code: "switch_led", Type: DPTypeBoolean}, {Code: "bright_value", Type: DPTypeInteger, Min: &minimum, Max: &maximum, Step: &step, Scale: 1}}}},
	}
	provider := testProvider(t, api)
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close(context.Background()) })
	items, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	item := items[0]
	if item.ID != "tuya-device-1" || item.Type != device.TypeLightbulb || !item.IsOnline() {
		t.Fatalf("device=%#v", item)
	}
	brightness, ok := item.Property("main", "tuya-dp", "bright_value")
	if !ok || brightness.Value.Number == nil || *brightness.Value.Number != 23.5 || brightness.Definition.Min == nil || *brightness.Definition.Min != 1 {
		t.Fatalf("brightness=%#v", brightness)
	}
	switchProperty, ok := item.Property("main", "tuya-dp", "switch_led")
	if !ok || switchProperty.Value.Bool == nil || !*switchProperty.Value.Bool || !switchProperty.Definition.Writable {
		t.Fatalf("switch=%#v", switchProperty)
	}
	catalog, err := provider.SourceCatalog(context.Background())
	if err != nil || len(catalog) != 1 || !catalog[0].Catalog.Complete {
		t.Fatalf("catalog=%#v err=%v", catalog, err)
	}
	if catalog[0].Catalog.Model != "Tuya Lamp" {
		t.Fatalf("catalog model=%q", catalog[0].Catalog.Model)
	}
	status := catalog[0].Catalog.Values["main/tuya-dp/bright_value"]
	if !status.Known || !status.Available {
		t.Fatalf("status=%#v", status)
	}
}

func TestProviderWritesPhysicalTuyaValueAndHandlesMQTTUpdates(t *testing.T) {
	minimum, maximum, step := 10.0, 1000.0, 5.0
	api := &fakeAPI{
		devices: []TuyaDevice{{ID: "device-1", Name: "灯", Category: "dj", Online: true, Status: []TuyaStatus{{Code: "bright_value", Value: 235}}}},
		specs:   map[string]TuyaSpecification{"device-1": {Functions: []DPSpec{{Code: "bright_value", Type: DPTypeInteger, Min: &minimum, Max: &maximum, Step: &step, Scale: 1}}, Status: []DPSpec{{Code: "bright_value", Type: DPTypeInteger, Min: &minimum, Max: &maximum, Step: &step, Scale: 1}}}},
	}
	provider := testProvider(t, api)
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated, err := provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: "tuya-device-1", EndpointID: "main", CapabilityID: "tuya-dp", PropertyID: "bright_value", Value: device.NumberValue(50)})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.commands) != 1 || api.commands[0].items[0].Code != "bright_value" || api.commands[0].items[0].Value != int64(500) {
		t.Fatalf("commands=%#v", api.commands)
	}
	if property, ok := updated.Property("main", "tuya-dp", "bright_value"); !ok || property.Value.Number == nil || *property.Value.Number != 50 {
		t.Fatalf("updated=%#v", property)
	}
	if err := provider.HandleMQTTMessage([]byte(`{"bizCode":"device","eventType":"dp_report","data":{"devId":"device-1","status":[{"code":"bright_value","value":600}]}}`)); err != nil {
		t.Fatal(err)
	}
	items, _ := provider.DiscoverDevices(context.Background())
	property, ok := items[0].Property("main", "tuya-dp", "bright_value")
	if !ok || property.Value.Number == nil || *property.Value.Number != 60 {
		t.Fatalf("mqtt property=%#v", property)
	}
}

func TestProviderRefreshMarksMissingDeviceRemoved(t *testing.T) {
	api := &fakeAPI{devices: []TuyaDevice{{ID: "device-1", Name: "开关", Category: "kg", Online: true}}, specs: map[string]TuyaSpecification{"device-1": {}}}
	provider := testProvider(t, api)
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	api.devices = nil
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, _ := provider.DiscoverDevices(context.Background())
	if len(items) != 1 || !items[0].Removed || items[0].IsOnline() {
		t.Fatalf("removed=%#v", items)
	}
}

func TestProviderReadPropertyFetchesCurrentCloudStatusAndCachesSpecification(t *testing.T) {
	api := &fakeAPI{
		devices:  []TuyaDevice{{ID: "device-1", Name: "微动开关", Category: "kg", Online: true}},
		specs:    map[string]TuyaSpecification{"device-1": {Functions: []DPSpec{{Code: "switch_1", Type: DPTypeBoolean}}, Status: []DPSpec{{Code: "switch_1", Type: DPTypeBoolean}}}},
		statuses: map[string][]TuyaStatus{"device-1": {{Code: "switch_1", Value: false}}},
	}
	provider := testProvider(t, api)
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	api.statuses["device-1"] = []TuyaStatus{{Code: "switch_1", Value: true}}
	property, err := provider.ReadProperty(context.Background(), providersdk.PropertyReadRequest{DeviceID: "tuya-device-1", EndpointID: "main", CapabilityID: "tuya-dp", PropertyID: "switch_1"})
	if err != nil {
		t.Fatal(err)
	}
	if property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatalf("fresh property = %#v", property)
	}
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.specCalls["device-1"] != 1 || api.statusCalls["device-1"] != 3 {
		t.Fatalf("spec calls=%d status calls=%d", api.specCalls["device-1"], api.statusCalls["device-1"])
	}
}

func TestProviderManagedDeviceSurvivesDirectoryOmissionAndKeepsTypeOverride(t *testing.T) {
	specification := TuyaSpecification{Functions: []DPSpec{{Code: "switch_1", Type: DPTypeBoolean}}, Status: []DPSpec{{Code: "switch_1", Type: DPTypeBoolean}}}
	config, err := json.Marshal(Config{
		AccessID: "access", AccessSecret: "secret", UID: "user", AccessToken: "token", ManagedDevices: true,
		Devices: []DeviceConfig{{ID: "garden-pulse", DeviceID: "device-1", Name: "花园脉冲", Type: string(device.TypeValve), Category: "kg", Specification: specification, Status: []TuyaStatus{{Code: "switch_1", Value: false}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{specs: map[string]TuyaSpecification{"device-1": specification}, statuses: map[string][]TuyaStatus{"device-1": {{Code: "switch_1", Value: true}}}}
	provider, err := NewProviderWithAPI(providerconfig.Config{ID: "tuya-main", Name: "Tuya", Type: ProviderType, Config: config}, api)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, _ := provider.DiscoverDevices(context.Background())
	if len(items) != 1 || items[0].Removed || items[0].IsOnline() || items[0].Type != device.TypeValve {
		t.Fatalf("retained managed device = %#v", items)
	}
	api.devices = []TuyaDevice{{ID: "device-1", Name: "云端名称", Category: "kg", Online: true}}
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, _ = provider.DiscoverDevices(context.Background())
	if len(items) != 1 || items[0].ID != "garden-pulse" || items[0].Type != device.TypeValve || !items[0].IsOnline() {
		t.Fatalf("refreshed managed device = %#v", items)
	}
	if remote, ok := provider.ProviderDeviceID("garden-pulse"); !ok || remote != "device-1" {
		t.Fatalf("provider identity = %q, %v", remote, ok)
	}
}

func TestProviderPersistsRefreshedTokenAndKeepsRotatingRefreshToken(t *testing.T) {
	config, err := json.Marshal(Config{AccessID: "access", AccessSecret: "secret", UID: "user", AccessToken: "expired", RefreshToken: "refresh-old", TokenExpiresAt: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{refreshToken: Token{AccessToken: "access-new", UID: "user", ExpiresIn: 3600}}
	provider, err := NewProviderWithAPI(providerconfig.Config{ID: "tuya-main", Name: "Tuya", Type: ProviderType, Config: config}, api)
	if err != nil {
		t.Fatal(err)
	}
	changes := make(chan json.RawMessage, 1)
	provider.SetRuntimeConfigChangeHandler(func(_, replacement json.RawMessage) { changes <- replacement })
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case replacement := <-changes:
		var persisted Config
		if err := json.Unmarshal(replacement, &persisted); err != nil {
			t.Fatal(err)
		}
		if persisted.AccessToken != "access-new" || persisted.RefreshToken != "refresh-old" || persisted.TokenExpiresAt.Before(time.Now().Add(50*time.Minute)) {
			t.Fatalf("persisted token config = %#v", persisted)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for persisted token config")
	}
}

func TestProviderReportsExpiredMQTTCredentialsWithoutBreakingHTTP(t *testing.T) {
	config, err := json.Marshal(Config{
		AccessID: "access", AccessSecret: "secret", UID: "user", AccessToken: "token",
		MQTT: &MQTTConfig{Enabled: true, URL: "mqtts://mqtt.example.com:8883", Username: "user", Password: "secret", ClientID: "client-1", SourceTopic: "tuya/device", ExpiresAt: time.Now().Add(-time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{devices: []TuyaDevice{{ID: "device-1", Name: "开关", Category: "kg", Online: true}}, specs: map[string]TuyaSpecification{"device-1": {}}}
	provider, err := NewProviderWithAPI(providerconfig.Config{ID: "tuya-main", Name: "Tuya", Type: ProviderType, Config: config}, api)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(context.Background())
	deadline := time.Now().Add(time.Second)
	for provider.ProviderDiagnostics()["mqttLastError"] == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	diagnostics := provider.ProviderDiagnostics()
	if diagnostics["state"] != "running" || diagnostics["mqtt"] != "reconnecting" || diagnostics["mqttLastError"] == "" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	items, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(items) != 1 || !items[0].IsOnline() {
		t.Fatalf("HTTP fallback devices=%#v err=%v", items, err)
	}
}
