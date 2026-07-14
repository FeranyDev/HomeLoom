package xiaomi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type fakeHub struct {
	mu         sync.Mutex
	value      any
	handler    func(hubIncoming)
	actions    int
	connects   int
	deviceList json.RawMessage
}

func (f *fakeHub) Connect(context.Context, context.Context) error {
	f.mu.Lock()
	f.connects++
	f.mu.Unlock()
	return nil
}
func (f *fakeHub) Close(context.Context) error { return nil }
func (f *fakeHub) DeviceList(context.Context) (json.RawMessage, error) {
	if len(f.deviceList) > 0 {
		return append(json.RawMessage(nil), f.deviceList...), nil
	}
	return json.RawMessage(`{"code":0,"result":{"list":[{"did":"123"}]}}`), nil
}
func (f *fakeHub) GetProperty(context.Context, string, int, int) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, _ := json.Marshal(map[string]any{"code": 0, "result": []any{map[string]any{"value": f.value}}})
	return value, nil
}
func (f *fakeHub) SetProperty(_ context.Context, _ string, _, _ int, value any) (json.RawMessage, error) {
	f.mu.Lock()
	f.value = value
	f.mu.Unlock()
	return json.RawMessage(`{"code":0}`), nil
}
func (f *fakeHub) Action(context.Context, string, int, int, []any) (json.RawMessage, error) {
	f.actions++
	return json.RawMessage(`{"code":0}`), nil
}
func (f *fakeHub) SetIncomingHandler(handler func(hubIncoming)) { f.handler = handler }

func testConfig() Config {
	writable := true
	return Config{PollIntervalSec: 3600, RequestTimeoutSec: 1, Devices: []DeviceConfig{{
		DID: "123", ID: "xiaomi-switch", Name: "米家开关", Type: device.TypeSwitch,
		Properties: []PropertyMapping{{EndpointID: "main", CapabilityID: "switch", CapabilityType: "switch", PropertyID: "power", Name: "开关", ValueType: device.ValueTypeBool, SIID: 2, PIID: 1, Writable: true, Readable: &writable}},
		Actions:    []ActionMapping{{EndpointID: "main", CapabilityID: "switch", CommandID: "identify", SIID: 3, AIID: 1}},
	}}}
}

func TestProviderAllowsNoMappedDevices(t *testing.T) {
	provider, err := newProvider("xiaomi-empty", "米家", Config{Devices: []DeviceConfig{}}, func() hubClient { return &fakeHub{} })
	if err != nil {
		t.Fatal(err)
	}
	devices, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(devices) != 0 {
		t.Fatalf("devices=%#v err=%v", devices, err)
	}
}

func TestProviderReadWriteAndNotification(t *testing.T) {
	hub := &fakeHub{value: false}
	provider, err := newProvider("xiaomi-main", "米家", testConfig(), func() hubClient { return hub })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)

	property, err := provider.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: "xiaomi-switch", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"})
	if err != nil || property.Value.Bool == nil || *property.Value.Bool {
		t.Fatalf("initial property = %#v, err=%v", property, err)
	}
	updated, err := provider.WriteProperty(ctx, providersdk.PropertyWriteRequest{DeviceID: "xiaomi-switch", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)})
	if err != nil {
		t.Fatal(err)
	}
	property, _ = updated.Property("main", "switch", "power")
	if property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatal("write did not update in-memory snapshot")
	}

	events := make(chan device.Device, 1)
	unsubscribe := provider.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()
	hub.handler(hubIncoming{Topic: "master/appMsg/notify/iot/123/property/2.1", Payload: json.RawMessage(`{"did":"123","siid":2,"piid":1,"value":false}`)})
	select {
	case item := <-events:
		property, _ := item.Property("main", "switch", "power")
		if property.Value.Bool == nil || *property.Value.Bool {
			t.Fatal("notification value was not applied")
		}
	case <-ctx.Done():
		t.Fatal("notification was not broadcast")
	}
}

func TestProviderLoadsCompleteMIoTSourceCatalog(t *testing.T) {
	specType := "urn:miot-spec-v2:device:switch:0000A003:vendor-v1:1"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/instance" || request.URL.Query().Get("type") != specType {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"type":"urn:miot-spec-v2:device:switch:0000A003:vendor-v1:1","description":"Switch","services":[{"iid":2,"type":"urn:miot-spec-v2:service:switch:0000780C:vendor-v1:1","description":"Switch","properties":[{"iid":1,"type":"urn:miot-spec-v2:property:on:00000006:vendor-v1:1","description":"Switch Status","format":"bool","access":["read","write","notify"]},{"iid":2,"type":"urn:miot-spec-v2:property:temperature:00000020:vendor-v1:1","description":"Temperature","format":"float","access":["read","write","notify"],"unit":"celsius","value-range":[-20,80,0.1]}],"actions":[{"iid":1,"type":"urn:miot-spec-v2:action:toggle:0000280C:vendor-v1:1","description":"Toggle","in":[],"out":[]}],"events":[{"iid":1,"type":"urn:miot-spec-v2:event:fault:00005005:vendor-v1:1","description":"Fault","arguments":[2]}]}]}`))
	}))
	defer server.Close()
	resolver := NewSpecResolver(nil)
	resolver.baseURL, resolver.client = server.URL, server.Client()
	hub := &fakeHub{value: 23.5, deviceList: json.RawMessage(`{"code":0,"result":{"list":[{"did":"123","name":"米家开关","model":"vendor.switch.v1","specType":"urn:miot-spec-v2:device:switch:0000A003:vendor-v1:1"}]}}`)}
	provider, err := newProviderWithResolver("xiaomi-main", "米家", testConfig(), func() hubClient { return hub }, resolver)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)

	catalog, err := provider.SourceCatalog(ctx)
	if err != nil || len(catalog) != 1 || !catalog[0].Catalog.Complete || catalog[0].Catalog.SpecType != specType {
		t.Fatalf("catalog=%#v err=%v", catalog, err)
	}
	property, ok := catalog[0].Property("miot-2", "service-2", "property-2")
	if !ok || property.Definition.Name != "Temperature" || property.Definition.Min == nil || *property.Definition.Min != -20 {
		t.Fatalf("native temperature property=%#v", property)
	}
	capability := catalog[0].Endpoints[len(catalog[0].Endpoints)-1].Capabilities[0]
	if len(capability.Commands) != 1 || len(capability.Events) != 1 {
		t.Fatalf("native definitions=%#v", capability)
	}
	read, err := provider.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: "xiaomi-switch", EndpointID: "miot-2", CapabilityID: "service-2", PropertyID: "property-2"})
	if err != nil || read.Value.Number == nil || *read.Value.Number != 23.5 {
		t.Fatalf("native read=%#v err=%v", read, err)
	}
	written, err := provider.WriteProperty(ctx, providersdk.PropertyWriteRequest{DeviceID: "xiaomi-switch", EndpointID: "miot-2", CapabilityID: "service-2", PropertyID: "property-2", Value: device.NumberValue(24.5)})
	writtenProperty, _ := written.Property("miot-2", "service-2", "property-2")
	if err != nil || writtenProperty.Value.Number == nil || *writtenProperty.Value.Number != 24.5 {
		t.Fatalf("native write=%#v err=%v", writtenProperty, err)
	}
	discovered, _ := provider.DiscoverDevices(ctx)
	if len(discovered) != 1 || discovered[0].NormalizeModelParameters() != nil {
		t.Fatalf("complete native snapshot did not preserve unified model compatibility: %#v", discovered)
	}

	events := make(chan device.Device, 1)
	unsubscribe := provider.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()
	hub.handler(hubIncoming{Topic: "master/appMsg/notify/iot/123/property/2.2", Payload: json.RawMessage(`{"did":"123","siid":2,"piid":2,"value":25.5}`)})
	select {
	case updated := <-events:
		property, _ := updated.Property("miot-2", "service-2", "property-2")
		if property.Value.Number == nil || *property.Value.Number != 25.5 {
			t.Fatalf("native notification=%#v", property)
		}
	case <-ctx.Done():
		t.Fatal("native notification was not broadcast")
	}
}

func TestProviderReconfiguresMappingsWithoutSecondConnection(t *testing.T) {
	hub := &fakeHub{value: false}
	current, err := newProvider("xiaomi-main", "米家", testConfig(), func() hubClient { return hub })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := current.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer current.Close(ctx)

	nextConfig := testConfig()
	nextConfig.Devices = append(nextConfig.Devices, DeviceConfig{DID: "456", ID: "xiaomi-temperature", Name: "温度", Type: device.TypeTemperatureSensor, Properties: []PropertyMapping{{EndpointID: "main", CapabilityID: "temperature", CapabilityType: "temperature-measurement", PropertyID: "current-temperature", ValueType: device.ValueTypeNumber, SIID: 2, PIID: 1}}})
	replacementHub := &fakeHub{}
	replacement, err := newProvider("xiaomi-main", "新名称", nextConfig, func() hubClient { return replacementHub })
	if err != nil {
		t.Fatal(err)
	}
	handled, err := current.Reconfigure(ctx, replacement)
	if err != nil || !handled {
		t.Fatalf("Reconfigure() = %v, %v", handled, err)
	}
	items, err := current.DiscoverDevices(ctx)
	if err != nil || len(items) != 2 || current.Manifest().Name != "新名称" {
		t.Fatalf("devices=%#v manifest=%#v err=%v", items, current.Manifest(), err)
	}
	hub.mu.Lock()
	connects := hub.connects
	hub.mu.Unlock()
	replacementHub.mu.Lock()
	replacementConnects := replacementHub.connects
	replacementHub.mu.Unlock()
	if connects != 1 || replacementConnects != 0 {
		t.Fatalf("connection counts current=%d replacement=%d", connects, replacementConnects)
	}
}

func TestProviderDeclinesLiveTransportReconfiguration(t *testing.T) {
	current, _ := newProvider("xiaomi-main", "米家", testConfig(), func() hubClient { return &fakeHub{} })
	nextConfig := testConfig()
	nextConfig.Host = "192.168.1.2"
	replacement, _ := newProvider("xiaomi-main", "米家", nextConfig, func() hubClient { return &fakeHub{} })
	handled, err := current.Reconfigure(context.Background(), replacement)
	if err != nil || handled {
		t.Fatalf("Reconfigure() = %v, %v", handled, err)
	}
}

func TestResponseValueAndEnumMapping(t *testing.T) {
	value, err := responseValue(json.RawMessage(`{"code":0,"result":[{"did":"1","value":2}]}`))
	if err != nil || value.(json.Number).String() != "2" {
		t.Fatalf("value=%#v err=%v", value, err)
	}
	mapping := PropertyMapping{ValueType: device.ValueTypeEnum, Enum: map[string]any{"off": float64(0), "auto": float64(2)}}
	decoded, err := decodePropertyValue(mapping, json.Number("2"))
	if err != nil || decoded.String == nil || *decoded.String != "auto" {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
}
