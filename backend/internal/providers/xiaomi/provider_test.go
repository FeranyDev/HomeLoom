package xiaomi

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type fakeHub struct {
	mu       sync.Mutex
	value    any
	handler  func(hubIncoming)
	actions  int
	connects int
}

func (f *fakeHub) Connect(context.Context, context.Context) error {
	f.mu.Lock()
	f.connects++
	f.mu.Unlock()
	return nil
}
func (f *fakeHub) Close(context.Context) error { return nil }
func (f *fakeHub) DeviceList(context.Context) (json.RawMessage, error) {
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
