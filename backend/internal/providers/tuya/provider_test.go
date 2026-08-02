package tuya

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	tuyaapi "github.com/feranydev/homeloom/backend/internal/providers/tuya/api"
)

type fakeAPI struct {
	devices  []TuyaDevice
	specs    map[string]TuyaSpecification
	statuses map[string][]TuyaStatus
	commands []fakeCommand
}

type fakeCommand struct {
	deviceID string
	items    []TuyaCommand
}

func (f *fakeAPI) GetToken(context.Context) (Token, error) {
	return Token{AccessToken: "token", UID: "user"}, nil
}
func (f *fakeAPI) RefreshToken(context.Context, string) (Token, error) {
	return Token{AccessToken: "token", UID: "user"}, nil
}
func (f *fakeAPI) SetAccessToken(string) {}
func (f *fakeAPI) ListUserDevices(context.Context, string, int, int) ([]TuyaDevice, error) {
	return append([]TuyaDevice(nil), f.devices...), nil
}
func (f *fakeAPI) GetSpecification(_ context.Context, id string) (TuyaSpecification, error) {
	return f.specs[id], nil
}
func (f *fakeAPI) GetStatus(_ context.Context, id string) ([]TuyaStatus, error) {
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
