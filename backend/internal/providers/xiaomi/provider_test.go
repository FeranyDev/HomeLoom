package xiaomi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type fakeHub struct {
	mu          sync.Mutex
	value       any
	handler     func(hubIncoming)
	actions     int
	connects    int
	closes      int
	connectErr  error
	deviceLists int
	deviceList  json.RawMessage
}

func TestBuildDevicePreservesConfiguredLocation(t *testing.T) {
	item := buildDevice("xiaomi-main", DeviceConfig{
		DID: "123", ID: "xiaomi-123", Name: "客厅灯", Type: device.TypeLightbulb,
		HomeID: "home-main", Home: "我的家", RoomID: "room-living", Room: "客厅",
	})
	if item.HomeID != "home-main" || item.HomeName != "我的家" || item.RoomID != "room-living" || item.RoomName != "客厅" {
		t.Fatalf("unexpected published device location: %+v", item)
	}
}

func (f *fakeHub) Connect(context.Context, context.Context) error {
	f.mu.Lock()
	f.connects++
	err := f.connectErr
	f.mu.Unlock()
	return err
}
func (f *fakeHub) Close(context.Context) error {
	f.mu.Lock()
	f.closes++
	f.mu.Unlock()
	return nil
}
func (f *fakeHub) DeviceList(context.Context) (json.RawMessage, error) {
	f.mu.Lock()
	f.deviceLists++
	deviceList := append(json.RawMessage(nil), f.deviceList...)
	f.mu.Unlock()
	if len(deviceList) > 0 {
		return deviceList, nil
	}
	return json.RawMessage(`{"code":0,"result":{"list":[{"did":"123"}]}}`), nil
}

func TestProviderDebouncesGatewayDirectoryChanges(t *testing.T) {
	hub := &fakeHub{value: false}
	provider, err := newProvider("xiaomi-main", "米家", testConfig(), func() hubClient { return hub })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	for index := 0; index < 3; index++ {
		hub.handler(hubIncoming{Topic: topicDeviceListChange, Payload: json.RawMessage(`{}`)})
	}
	for {
		hub.mu.Lock()
		calls := hub.deviceLists
		hub.mu.Unlock()
		if calls >= 2 || ctx.Err() != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	hub.mu.Lock()
	calls := hub.deviceLists
	hub.mu.Unlock()
	if calls != 2 || provider.ProviderMetrics()["directoryRefreshes"] != 1 {
		t.Fatalf("device list calls=%d metrics=%#v", calls, provider.ProviderMetrics())
	}
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

func TestProviderAcceptsOutOfRangeMIoTSentinelButRejectsItForWrites(t *testing.T) {
	minimum, maximum := float64(16), float64(30)
	readable := true
	config := testConfig()
	config.Devices[0].Properties = append(config.Devices[0].Properties, PropertyMapping{EndpointID: "miot-2", CapabilityID: "service-2", CapabilityType: "air-conditioner", PropertyID: "property-24", Name: "目标温度", ValueType: device.ValueTypeInt, SIID: 2, PIID: 24, Writable: true, Readable: &readable, Min: &minimum, Max: &maximum})
	hub := &fakeHub{value: float64(0)}
	provider, err := newProvider("xiaomi-main", "米家", config, func() hubClient { return hub })
	if err != nil {
		t.Fatal(err)
	}
	initial, _ := provider.snapshot("xiaomi-switch")
	initialProperty, _ := initial.Property("miot-2", "service-2", "property-24")
	if initialProperty.Value.Int == nil || *initialProperty.Value.Int != 16 || initial.ValidateStructure() != nil {
		t.Fatalf("initial property=%#v validation=%v", initialProperty, initial.ValidateStructure())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	observed, err := provider.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: "xiaomi-switch", EndpointID: "miot-2", CapabilityID: "service-2", PropertyID: "property-24"})
	if err != nil || observed.Value.Int == nil || *observed.Value.Int != 0 || observed.Definition.Min == nil || *observed.Definition.Min != 0 {
		t.Fatalf("observed property=%#v err=%v", observed, err)
	}
	item, _ := provider.snapshot("xiaomi-switch")
	if err := item.ValidateStructure(); err != nil {
		t.Fatalf("out-of-range native sentinel invalidated event: %v", err)
	}
	if _, err := provider.WriteProperty(ctx, providersdk.PropertyWriteRequest{DeviceID: "xiaomi-switch", EndpointID: "miot-2", CapabilityID: "service-2", PropertyID: "property-24", Value: device.IntValue(0)}); err != providersdk.ErrPropertyInvalid {
		t.Fatalf("write error=%v want %v", err, providersdk.ErrPropertyInvalid)
	}
}

func TestProviderInitializeIsIdempotent(t *testing.T) {
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
	if err := provider.Initialize(ctx); err != nil {
		t.Fatalf("second Initialize() = %v", err)
	}
	hub.mu.Lock()
	connects := hub.connects
	hub.mu.Unlock()
	if connects != 1 {
		t.Fatalf("MQTT connection count = %d", connects)
	}
}

func TestProviderRecoversGatewayAddressByDIDAfterInitialTimeout(t *testing.T) {
	config := testConfig()
	config.Host, config.Port, config.GatewayDID = "192.168.101.201", 8883, "123456789"
	timedOut := &fakeHub{connectErr: ErrGatewayInitialConnectionTimeout}
	recovered := &fakeHub{}
	provider, err := newProvider("xiaomi-main", "米家", config, func() hubClient { return timedOut })
	if err != nil {
		t.Fatal(err)
	}
	provider.recoveryFactory = func(next Config) (hubClient, error) {
		if next.Host != "192.168.101.200" || next.Port != 8883 || next.GatewayDID != config.GatewayDID {
			t.Fatalf("recovery config = %#v", next)
		}
		return recovered, nil
	}
	provider.discoverGateways = func(context.Context) ([]Gateway, error) {
		return []Gateway{
			{DID: "another-hub", Addresses: []string{"192.168.101.202"}, Port: 8883, MQTTEnabled: true},
			{DID: config.GatewayDID, Addresses: []string{"192.168.101.200"}, Port: 8883, MQTTEnabled: true},
		}, nil
	}
	changes := make(chan struct {
		previous    json.RawMessage
		replacement json.RawMessage
	}, 1)
	provider.SetRuntimeConfigChangeHandler(func(previous, replacement json.RawMessage) {
		changes <- struct {
			previous    json.RawMessage
			replacement json.RawMessage
		}{previous: previous, replacement: replacement}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	provider.mu.RLock()
	activeHost := provider.config.Host
	provider.mu.RUnlock()
	if activeHost != "192.168.101.200" {
		t.Fatalf("active host = %q", activeHost)
	}
	timedOut.mu.Lock()
	closed := timedOut.closes
	timedOut.mu.Unlock()
	if closed != 1 {
		t.Fatalf("timed out connection close count = %d", closed)
	}
	select {
	case change := <-changes:
		var updated map[string]any
		if err := json.Unmarshal(change.replacement, &updated); err != nil || updated["host"] != "192.168.101.200" || updated["gatewayDid"] != config.GatewayDID {
			t.Fatalf("recovered config = %s, %v", change.replacement, err)
		}
	case <-ctx.Done():
		t.Fatal("missing recovered address update")
	}
}

func TestProviderDoesNotRecoverGatewayAddressWithoutDID(t *testing.T) {
	config := testConfig()
	config.Host, config.Port = "192.168.101.201", 8883
	timedOut := &fakeHub{connectErr: ErrGatewayInitialConnectionTimeout}
	provider, err := newProvider("xiaomi-main", "米家", config, func() hubClient { return timedOut })
	if err != nil {
		t.Fatal(err)
	}
	discoveries := 0
	provider.discoverGateways = func(context.Context) ([]Gateway, error) {
		discoveries++
		return nil, nil
	}
	if err := provider.Initialize(context.Background()); !errors.Is(err, ErrGatewayInitialConnectionTimeout) {
		t.Fatalf("Initialize() error = %v", err)
	}
	if discoveries != 0 {
		t.Fatalf("mDNS discovery count = %d", discoveries)
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
	valueStatus := catalog[0].Catalog.Values[providersdk.SourceValueKey("miot-2", "service-2", "property-2")]
	if !valueStatus.Known || !valueStatus.Available || valueStatus.ObservedAt.IsZero() {
		t.Fatalf("native temperature status=%#v", valueStatus)
	}
	capability := catalog[0].Endpoints[len(catalog[0].Endpoints)-1].Capabilities[0]
	if len(capability.Commands) != 1 || len(capability.Events) != 1 {
		t.Fatalf("native definitions=%#v", capability)
	}
	read, err := provider.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: "xiaomi-switch", EndpointID: "miot-2", CapabilityID: "service-2", PropertyID: "property-2"})
	if err != nil || read.Value.Number == nil || *read.Value.Number != 23.5 {
		t.Fatalf("native read=%#v err=%v", read, err)
	}
	discovered, _ := provider.DiscoverDevices(ctx)
	if len(discovered) != 1 || discovered[0].NormalizeModelParameters() != nil {
		t.Fatalf("unified snapshot is invalid: %#v", discovered)
	}
	if _, exposed := discovered[0].Property("miot-2", "service-2", "property-2"); exposed {
		t.Fatalf("device detail snapshot exposed native MIoT property: %#v", discovered[0])
	}
	written, err := provider.WriteProperty(ctx, providersdk.PropertyWriteRequest{DeviceID: "xiaomi-switch", EndpointID: "miot-2", CapabilityID: "service-2", PropertyID: "property-2", Value: device.NumberValue(24.5)})
	if err != nil {
		t.Fatalf("native write=%#v err=%v", written, err)
	}
	if _, exposed := written.Property("miot-2", "service-2", "property-2"); exposed {
		t.Fatalf("write response exposed native MIoT property: %#v", written)
	}
	catalog, _ = provider.SourceCatalog(ctx)
	writtenProperty, _ := catalog[0].Property("miot-2", "service-2", "property-2")
	if writtenProperty.Value.Number == nil || *writtenProperty.Value.Number != 24.5 {
		t.Fatalf("mapping catalog did not retain native write value: %#v", writtenProperty)
	}

	events := make(chan device.Device, 1)
	unsubscribe := provider.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()
	hub.handler(hubIncoming{Topic: "master/appMsg/notify/iot/123/property/2.2", Payload: json.RawMessage(`{"did":"123","siid":2,"piid":2,"value":25.5}`)})
	select {
	case updated := <-events:
		if _, exposed := updated.Property("miot-2", "service-2", "property-2"); exposed {
			t.Fatalf("public device event exposed native MIoT property: %#v", updated)
		}
	case <-ctx.Done():
		t.Fatal("native notification was not broadcast")
	}
	catalog, _ = provider.SourceCatalog(ctx)
	notifiedProperty, _ := catalog[0].Property("miot-2", "service-2", "property-2")
	if notifiedProperty.Value.Number == nil || *notifiedProperty.Value.Number != 25.5 {
		t.Fatalf("mapping catalog did not retain native notification: %#v", notifiedProperty)
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

func TestProviderHotAppliesRenewedCredentialsForNextConnection(t *testing.T) {
	hub := &fakeHub{value: false}
	config := testConfig()
	config.ClientCertificate, config.PrivateKey = "old-certificate", "old-key"
	config.OAuth = &OAuthConfig{ClientID: "1", Region: "cn", OAuthUUID: "0123456789abcdef0123456789abcdef", VirtualDID: "2", UID: "3", AccessToken: "old-access", RefreshToken: "old-refresh", RefreshAfter: 1, ExpiresAt: 2}
	current, err := newProvider("xiaomi-main", "米家", config, func() hubClient { return hub })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := current.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	replacementHub := &fakeHub{value: false}
	nextConfig := config
	nextConfig.ClientCertificate = "renewed-certificate"
	nextOAuth := *config.OAuth
	nextOAuth.AccessToken, nextOAuth.RefreshToken, nextOAuth.RefreshAfter, nextOAuth.ExpiresAt = "new-access", "new-refresh", 3, 4
	nextConfig.OAuth = &nextOAuth
	replacement, err := newProvider("xiaomi-main", "米家", nextConfig, func() hubClient { return replacementHub })
	if err != nil {
		t.Fatal(err)
	}
	handled, err := current.Reconfigure(ctx, replacement)
	if err != nil || !handled {
		t.Fatalf("Reconfigure() = %v, %v", handled, err)
	}
	hub.mu.Lock()
	oldConnections := hub.connects
	hub.mu.Unlock()
	replacementHub.mu.Lock()
	renewedConnections := replacementHub.connects
	replacementHub.mu.Unlock()
	if renewedConnections != 0 || oldConnections != 1 {
		t.Fatalf("connections before reconnect old=%d renewed=%d", oldConnections, renewedConnections)
	}
	if err := current.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := current.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer current.Close(ctx)
	replacementHub.mu.Lock()
	renewedConnections = replacementHub.connects
	replacementHub.mu.Unlock()
	if renewedConnections != 1 {
		t.Fatalf("renewed credential factory connections=%d", renewedConnections)
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

func TestProviderLetsManagerReplaceUninitializedInstance(t *testing.T) {
	current, _ := newProvider("xiaomi-main", "米家", testConfig(), func() hubClient { return &fakeHub{} })
	replacement, _ := newProvider("xiaomi-main", "新名称", testConfig(), func() hubClient { return &fakeHub{} })
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
