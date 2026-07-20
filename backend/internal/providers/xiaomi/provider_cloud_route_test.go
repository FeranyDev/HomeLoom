package xiaomi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
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
	if !provider.shouldCalibrate(configured) {
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
	if provider.shouldCalibrate(configured) {
		t.Fatal("local push-capable device should be event-driven after its initial read")
	}
}
