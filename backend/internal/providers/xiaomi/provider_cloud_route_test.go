package xiaomi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type routeHub struct {
	getCalls, setCalls, actionCalls int
	getErr, setErr, actionErr       error
}

func (h *routeHub) Connect(context.Context, context.Context) error { return nil }
func (h *routeHub) Close(context.Context) error                    { return nil }
func (h *routeHub) DeviceList(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"code":0,"result":[]}`), nil
}
func (h *routeHub) GetProperty(context.Context, string, int, int) (json.RawMessage, error) {
	h.getCalls++
	if h.getErr != nil {
		return nil, h.getErr
	}
	return json.RawMessage(`{"code":0,"result":[{"value":false}]}`), nil
}
func (h *routeHub) SetProperty(context.Context, string, int, int, any) (json.RawMessage, error) {
	h.setCalls++
	if h.setErr != nil {
		return nil, h.setErr
	}
	return json.RawMessage(`{"code":0}`), nil
}
func (h *routeHub) Action(context.Context, string, int, int, []any) (json.RawMessage, error) {
	h.actionCalls++
	if h.actionErr != nil {
		return nil, h.actionErr
	}
	return json.RawMessage(`{"code":0}`), nil
}
func (h *routeHub) SetIncomingHandler(func(hubIncoming)) {}

type routeCloud struct {
	getCalls, setCalls, actionCalls int
	value                           any
}

func (c *routeCloud) DeviceList(context.Context) ([]HubDevice, error) { return nil, nil }
func (c *routeCloud) GetProperties(_ context.Context, input []cloudProperty) ([]cloudProperty, error) {
	c.getCalls++
	result := append([]cloudProperty(nil), input...)
	for index := range result {
		result[index].Code, result[index].Value = 0, c.value
	}
	return result, nil
}
func (c *routeCloud) SetProperties(_ context.Context, input []cloudProperty) ([]cloudProperty, error) {
	c.setCalls++
	result := append([]cloudProperty(nil), input...)
	for index := range result {
		result[index].Code = 0
	}
	return result, nil
}
func (c *routeCloud) Action(context.Context, cloudAction) error { c.actionCalls++; return nil }
func (c *routeCloud) UpdateOAuth(OAuthConfig)                   {}

func routeTestProvider(t *testing.T, mode string, hub hubClient, cloud homeCloudClient, route deviceRoute) (*Provider, DeviceConfig) {
	t.Helper()
	config := testConfig()
	config.Devices[0].ConnectionMode = mode
	provider, err := newProvider("xiaomi-route", "米家路由", config, func() hubClient { return hub })
	if err != nil {
		t.Fatal(err)
	}
	provider.client, provider.cloud = hub, cloud
	provider.routes[config.Devices[0].DID] = route
	return provider, config.Devices[0]
}

func TestAutoRouteFallsBackToOfficialCloudForReadWriteAndAction(t *testing.T) {
	hub := &routeHub{getErr: errors.New("local read unavailable"), setErr: errors.New("local write unavailable"), actionErr: errors.New("local action unavailable")}
	cloud := &routeCloud{value: true}
	provider, configured := routeTestProvider(t, connectionModeAuto, hub, cloud, deviceRoute{local: true, push: true})
	mapping := configured.Properties[0]

	value, err := provider.readPropertyRaw(context.Background(), configured, mapping)
	if err != nil || value != true {
		t.Fatalf("cloud read value=%#v err=%v", value, err)
	}
	if err := provider.writePropertyRaw(context.Background(), configured, mapping, true); err != nil {
		t.Fatal(err)
	}
	if err := provider.executeActionRaw(context.Background(), configured, configured.Actions[0], nil); err != nil {
		t.Fatal(err)
	}
	if hub.getCalls != 1 || hub.setCalls != 1 || hub.actionCalls != 1 || cloud.getCalls != 1 || cloud.setCalls != 1 || cloud.actionCalls != 1 {
		t.Fatalf("hub calls=%d/%d/%d cloud calls=%d/%d/%d", hub.getCalls, hub.setCalls, hub.actionCalls, cloud.getCalls, cloud.setCalls, cloud.actionCalls)
	}
	metrics := provider.ProviderMetrics()
	if metrics["localFailures"] != 3 || metrics["cloudFallbacks"] != 3 || metrics["cloudRequests"] != 3 {
		t.Fatalf("metrics = %#v", metrics)
	}
	item, _ := provider.snapshot(configured.ID)
	if item.RuntimeMode != device.RuntimeModeCloud {
		t.Fatalf("runtime mode = %q", item.RuntimeMode)
	}
}

func TestCloudOnlyRouteSkipsGatewayWhenDeviceIsNotLocallyControllable(t *testing.T) {
	hub, cloud := &routeHub{}, &routeCloud{value: true}
	provider, configured := routeTestProvider(t, connectionModeAuto, hub, cloud, deviceRoute{local: false, cloud: true, push: false})
	if _, err := provider.readPropertyRaw(context.Background(), configured, configured.Properties[0]); err != nil {
		t.Fatal(err)
	}
	if hub.getCalls != 0 || cloud.getCalls != 1 {
		t.Fatalf("hub reads=%d cloud reads=%d", hub.getCalls, cloud.getCalls)
	}
	if len(provider.calibrationMappings(configured, false)) == 0 {
		t.Fatal("cloud-only device must be periodically calibrated")
	}
}

func TestExplicitLocalRouteNeverFallsBackToCloud(t *testing.T) {
	hub := &routeHub{getErr: errors.New("offline")}
	cloud := &routeCloud{value: true}
	provider, configured := routeTestProvider(t, connectionModeLocal, hub, cloud, deviceRoute{local: true, cloud: true, push: true})
	if _, err := provider.readPropertyRaw(context.Background(), configured, configured.Properties[0]); err == nil {
		t.Fatal("expected local route error")
	}
	if cloud.getCalls != 0 || provider.ProviderMetrics()["cloudFallbacks"] != 0 {
		t.Fatalf("cloud calls=%d metrics=%#v", cloud.getCalls, provider.ProviderMetrics())
	}
}

func TestLocalPushDeviceDoesNotNeedPeriodicCalibration(t *testing.T) {
	provider, configured := routeTestProvider(t, connectionModeAuto, &routeHub{}, &routeCloud{}, deviceRoute{local: true, cloud: true, push: true})
	provider.setRuntimeMode(configured.ID, device.RuntimeModeLocal)
	if len(provider.calibrationMappings(configured, false)) != 0 {
		t.Fatal("local push-capable device should be event-driven after its initial read")
	}
}

func TestCloudRefreshBatchesPropertiesAndBroadcastsOneSnapshot(t *testing.T) {
	config := testConfig()
	second := config.Devices[0].Properties[0]
	second.CapabilityID, second.PropertyID, second.PIID = "secondary", "enabled", 2
	config.Devices[0].Properties = append(config.Devices[0].Properties, second)
	hub, cloud := &routeHub{}, &routeCloud{value: true}
	provider, err := newProvider("xiaomi-route", "米家路由", config, func() hubClient { return hub })
	if err != nil {
		t.Fatal(err)
	}
	provider.client, provider.cloud = hub, cloud
	provider.routes[config.Devices[0].DID] = deviceRoute{cloud: true}
	events := make(chan device.Device, 2)
	unsubscribe := provider.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()

	provider.refreshDevice(context.Background(), config.Devices[0], true)
	if cloud.getCalls != 1 {
		t.Fatalf("cloud get calls=%d want one batched request", cloud.getCalls)
	}
	select {
	case item := <-events:
		first, _ := item.Property("main", "switch", "power")
		secondProperty, _ := item.Property("main", "secondary", "enabled")
		if first.Value.Bool == nil || !*first.Value.Bool || secondProperty.Value.Bool == nil || !*secondProperty.Value.Bool {
			t.Fatalf("batched snapshot=%#v", item)
		}
	case <-time.After(time.Second):
		t.Fatal("batched refresh was not broadcast")
	}
	select {
	case item := <-events:
		t.Fatalf("refresh emitted more than one snapshot: %#v", item)
	default:
	}
	if provider.ProviderMetrics()["cloudHttpInitialReads"] != 1 {
		t.Fatalf("metrics=%#v", provider.ProviderMetrics())
	}
}

func TestConnectedCloudMIPSOnlyCompensatesNonNotifiableProperties(t *testing.T) {
	provider, configured := routeTestProvider(t, connectionModeCloud, &routeHub{}, &routeCloud{}, deviceRoute{cloud: true})
	provider.cloudMIPS = &fakeCloudMIPS{}
	if got := provider.calibrationMappings(configured, false); len(got) != 0 {
		t.Fatalf("notifiable mappings should use push: %#v", got)
	}
	notifiable := false
	configured.Properties[0].Notifiable = &notifiable
	if got := provider.calibrationMappings(configured, false); len(got) != 1 {
		t.Fatalf("non-notifiable mappings must be compensated: %#v", got)
	}
}

func TestPeriodicCalibrationSkipsUnmappedSourceCatalogProperties(t *testing.T) {
	provider, configured := routeTestProvider(t, connectionModeAuto, &routeHub{}, &routeCloud{}, deviceRoute{local: true, cloud: true, push: true})
	provider.cloudMIPS = &fakeCloudMIPS{}
	readable, notifiable := true, false
	raw := PropertyMapping{EndpointID: "miot-3", CapabilityID: "service-3", PropertyID: "property-7", ValueType: device.ValueTypeInt, SIID: 3, PIID: 7, Readable: &readable, Notifiable: &notifiable}
	provider.rawProperties[sourcePropertyKey(configured.ID, raw.EndpointID, raw.CapabilityID, raw.PropertyID)] = raw

	if got := provider.calibrationMappings(configured, false); len(got) != 0 {
		t.Fatalf("unmapped source catalog property was periodically calibrated: %#v", got)
	}
	if got := provider.calibrationMappings(configured, true); len(got) != 2 {
		t.Fatalf("initial read must still include configured and complete source properties: %#v", got)
	}

	provider.SetPropertyInterests([]providersdk.PropertyInterest{{ProviderID: provider.id, DeviceID: configured.ID, EndpointID: raw.EndpointID, CapabilityID: raw.CapabilityID, PropertyID: raw.PropertyID}})
	got := provider.calibrationMappings(configured, false)
	if len(got) != 1 || got[0].SIID != raw.SIID || got[0].PIID != raw.PIID {
		t.Fatalf("explicitly mapped raw property must be periodically calibrated: %#v", got)
	}

	provider.SetPropertyInterests(nil)
	if got := provider.calibrationMappings(configured, false); len(got) != 0 {
		t.Fatalf("removed mapping interest remained active: %#v", got)
	}
}

func TestCentralProviderTTLTracksNotifyAndCompensationInterval(t *testing.T) {
	config := testConfig()
	config.PollIntervalSec = 90
	notifiable := false
	second := config.Devices[0].Properties[0]
	second.CapabilityID, second.PropertyID, second.PIID, second.Notifiable = "secondary", "enabled", 2, &notifiable
	config.Devices[0].Properties = append(config.Devices[0].Properties, second)
	provider, err := newProvider("xiaomi-main", "米家", config, func() hubClient { return &routeHub{} })
	if err != nil {
		t.Fatal(err)
	}
	item, _ := provider.snapshot("xiaomi-switch")
	push, _ := item.Property("main", "switch", "power")
	polled, _ := item.Property("main", "secondary", "enabled")
	if push.Definition.StaleAfterSeconds != 360 || polled.Definition.StaleAfterSeconds != 180 {
		t.Fatalf("push TTL=%d polled TTL=%d", push.Definition.StaleAfterSeconds, polled.Definition.StaleAfterSeconds)
	}
}
