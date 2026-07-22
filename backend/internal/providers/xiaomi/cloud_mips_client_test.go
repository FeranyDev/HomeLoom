package xiaomi

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

func TestCloudMIPSConnectionConfigUsesOfficialBrokerAndStrictTLS(t *testing.T) {
	oauth := OAuthConfig{Region: "CN", OAuthUUID: "0123456789abcdef0123456789abcdef", ClientID: "123456", AccessToken: "secret"}
	broker, tlsConfig, clientID, username, token, err := cloudMIPSConnectionConfig(oauth)
	if err != nil {
		t.Fatal(err)
	}
	if broker.String() != "tls://cn-ha.mqtt.io.mi.com:8883" || clientID != "ha."+oauth.OAuthUUID || username != oauth.ClientID || token != oauth.AccessToken {
		t.Fatalf("derived connection = %s %q %q %q", broker, clientID, username, token)
	}
	if tlsConfig.InsecureSkipVerify || tlsConfig.ServerName != "cn-ha.mqtt.io.mi.com" {
		t.Fatalf("cloud TLS must perform normal DNS verification: %#v", tlsConfig)
	}
}

func TestCloudMIPSTopicsArePerDeviceAndSkipUnreliableBLEState(t *testing.T) {
	got := cloudMIPSTopics([]string{"proxy.2", "123", "123", "blt.1"})
	want := []string{
		"device/123/up/properties_changed/#", "device/123/up/event_occured/#", "device/123/state/#",
		"device/blt.1/up/properties_changed/#", "device/blt.1/up/event_occured/#",
		"device/proxy.2/up/properties_changed/#", "device/proxy.2/up/event_occured/#",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("topics=%#v want %#v", got, want)
	}
}

func TestParseCloudMIPSPropertyEventAndState(t *testing.T) {
	property, err := parseCloudMIPSMessage("device/did-1/up/properties_changed/2/3", []byte(`{"params":{"did":"did-1","siid":2,"piid":3,"value":24,"timestamp":1720000000123}}`))
	if err != nil || property.Kind != cloudMIPSProperty || property.DID != "did-1" || property.SIID != 2 || property.PIID != 3 || property.Value.(json.Number).String() != "24" || property.ObservedAt.IsZero() {
		t.Fatalf("property=%#v err=%v", property, err)
	}
	event, err := parseCloudMIPSMessage("device/did-1/up/event_occured/3/1", []byte(`{"params":{"siid":3,"eiid":1,"arguments":[{"piid":2,"value":true}]}}`))
	if err != nil || event.Kind != cloudMIPSEvent || event.EIID != 1 || len(event.Arguments) != 1 {
		t.Fatalf("event=%#v err=%v", event, err)
	}
	state, err := parseCloudMIPSMessage("device/did-1/state/online", []byte(`{"device_id":"did-1","event":"online","timestamp":1720000000123}`))
	if err != nil || state.Kind != cloudMIPSState || state.Online == nil || !*state.Online {
		t.Fatalf("state=%#v err=%v", state, err)
	}
}

func TestParseCloudMIPSRejectsMalformedAndMismatchedMessages(t *testing.T) {
	cases := []struct {
		topic   string
		payload string
	}{
		{"device/did-1/up/properties_changed/2/3", `not-json`},
		{"device/did-1/up/properties_changed/2/3", `{"params":{"siid":2,"value":1}}`},
		{"device/did-1/up/event_occured/2/3", `{"params":{"siid":2,"eiid":3}}`},
		{"device/did-1/state/online", `{"device_id":"other","event":"online"}`},
		{"device/did-1/state/unknown", `{"device_id":"did-1","event":"unknown"}`},
	}
	for _, current := range cases {
		if message, err := parseCloudMIPSMessage(current.topic, []byte(current.payload)); err == nil {
			t.Fatalf("parse %q unexpectedly succeeded: %#v", current.topic, message)
		}
	}
}

func TestCloudMIPSTokenUpdateIsUsedByExistingClient(t *testing.T) {
	client, err := newCloudMIPSClient(OAuthConfig{Region: "cn", OAuthUUID: "0123456789abcdef0123456789abcdef", ClientID: "client", AccessToken: "old"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client.UpdateOAuth(OAuthConfig{AccessToken: "new"})
	if client.currentAccessToken() != "new" {
		t.Fatalf("access token=%q", client.currentAccessToken())
	}
}

type fakeCloudMIPS struct {
	mu                sync.Mutex
	handler           func(cloudMIPSMessage)
	connectionHandler func(cloudMIPSConnectionEvent)
	connects          int
	dids              []string
	token             string
	closed            bool
}

func (f *fakeCloudMIPS) Connect(context.Context, context.Context) error {
	f.mu.Lock()
	f.connects++
	f.mu.Unlock()
	return nil
}
func (f *fakeCloudMIPS) Close(context.Context) error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}
func (f *fakeCloudMIPS) ReplaceDevices(_ context.Context, dids []string) error {
	f.mu.Lock()
	f.dids = append([]string(nil), dids...)
	f.mu.Unlock()
	return nil
}
func (f *fakeCloudMIPS) UpdateOAuth(oauth OAuthConfig) {
	f.mu.Lock()
	f.token = oauth.AccessToken
	f.mu.Unlock()
}
func (f *fakeCloudMIPS) SetIncomingHandler(handler func(cloudMIPSMessage)) {
	f.mu.Lock()
	f.handler = handler
	f.mu.Unlock()
}
func (f *fakeCloudMIPS) SetConnectionHandler(handler func(cloudMIPSConnectionEvent)) {
	f.mu.Lock()
	f.connectionHandler = handler
	f.mu.Unlock()
}
func (f *fakeCloudMIPS) Stats() cloudMIPSStats { return cloudMIPSStats{Connected: true} }
func (f *fakeCloudMIPS) publish(message cloudMIPSMessage) {
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	if handler != nil {
		handler(message)
	}
}
func (f *fakeCloudMIPS) connection(event cloudMIPSConnectionEvent) {
	f.mu.Lock()
	handler := f.connectionHandler
	f.mu.Unlock()
	if handler != nil {
		handler(event)
	}
}

func TestProviderCloudMIPSUpdatesStateAndDeduplicatesPush(t *testing.T) {
	hub := &fakeHub{value: false}
	provider, err := newProvider("xiaomi-main", "米家", testConfig(), func() hubClient { return hub })
	if err != nil {
		t.Fatal(err)
	}
	cloud := &fakeCloudMIPS{}
	provider.cloudMIPS = cloud
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)

	events := make(chan deviceSnapshot, 3)
	unsubscribe := provider.Subscribe(func(item device.Device) {
		property, _ := item.Property("main", "switch", "power")
		events <- deviceSnapshot{mode: item.RuntimeMode, transport: property.StateTransport, value: property.Value.Bool}
	})
	defer unsubscribe()
	observedAt := time.Now().UTC()
	cloud.publish(cloudMIPSMessage{Kind: cloudMIPSProperty, DID: "123", SIID: 2, PIID: 1, Value: true, ObservedAt: observedAt})
	cloud.publish(cloudMIPSMessage{Kind: cloudMIPSProperty, DID: "123", SIID: 2, PIID: 1, Value: true, ObservedAt: observedAt})
	select {
	case snapshot := <-events:
		if snapshot.value == nil || !*snapshot.value || snapshot.mode != device.RuntimeModeCloud || snapshot.transport != device.StateTransportCloudMQTT {
			t.Fatalf("snapshot=%#v", snapshot)
		}
	case <-ctx.Done():
		t.Fatal("cloud property was not broadcast")
	}
	select {
	case duplicate := <-events:
		t.Fatalf("duplicate push was broadcast: %#v", duplicate)
	case <-time.After(30 * time.Millisecond):
	}
	if provider.ProviderMetrics()["cloudMqttDuplicateMessages"] != 1 {
		t.Fatalf("metrics=%#v", provider.ProviderMetrics())
	}
}

type deviceSnapshot struct {
	mode      device.RuntimeMode
	transport device.StateTransport
	value     *bool
}

type initializationRaceCloud struct {
	provider *Provider
	mips     *fakeCloudMIPS
}

func (c *initializationRaceCloud) DeviceList(context.Context) ([]HubDevice, error) {
	return []HubDevice{{DID: "123", CloudAvailable: true}}, nil
}
func (c *initializationRaceCloud) GetProperties(ctx context.Context, input []cloudProperty) ([]cloudProperty, error) {
	c.mips.publish(cloudMIPSMessage{Kind: cloudMIPSProperty, DID: "123", SIID: 2, PIID: 1, Value: true, ObservedAt: time.Now().UTC()})
	for {
		item, _ := c.provider.snapshot("xiaomi-switch")
		property, _ := item.Property("main", "switch", "power")
		if property.Value.Bool != nil && *property.Value.Bool {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	result := append([]cloudProperty(nil), input...)
	for index := range result {
		result[index].Value = false
	}
	return result, nil
}
func (c *initializationRaceCloud) SetProperties(context.Context, []cloudProperty) ([]cloudProperty, error) {
	return nil, nil
}
func (c *initializationRaceCloud) Action(context.Context, cloudAction) error { return nil }
func (c *initializationRaceCloud) UpdateOAuth(OAuthConfig)                   {}

func TestProviderInitialHTTPResponseDoesNotOverwriteNewerCloudPush(t *testing.T) {
	config := testConfig()
	config.Devices[0].ConnectionMode = connectionModeCloud
	hub := &fakeHub{value: false}
	provider, err := newProvider("xiaomi-main", "米家", config, func() hubClient { return hub })
	if err != nil {
		t.Fatal(err)
	}
	mips := &fakeCloudMIPS{}
	provider.cloudMIPS = mips
	provider.cloud = &initializationRaceCloud{provider: provider, mips: mips}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	item, _ := provider.snapshot("xiaomi-switch")
	property, _ := item.Property("main", "switch", "power")
	if property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatalf("newer push was overwritten by initial HTTP response: %#v", property)
	}
}

type reconcileCloud struct{ calls atomic.Uint64 }

func (c *reconcileCloud) DeviceList(context.Context) ([]HubDevice, error) {
	return []HubDevice{{DID: "123", CloudAvailable: true}}, nil
}
func (c *reconcileCloud) GetProperties(_ context.Context, input []cloudProperty) ([]cloudProperty, error) {
	c.calls.Add(1)
	result := append([]cloudProperty(nil), input...)
	for index := range result {
		result[index].Value = true
	}
	return result, nil
}
func (*reconcileCloud) SetProperties(context.Context, []cloudProperty) ([]cloudProperty, error) {
	return nil, nil
}
func (*reconcileCloud) Action(context.Context, cloudAction) error { return nil }
func (*reconcileCloud) UpdateOAuth(OAuthConfig)                   {}

func TestProviderReconcilesCloudPropertiesAfterMIPSReconnect(t *testing.T) {
	config := testConfig()
	config.Devices[0].ConnectionMode = connectionModeCloud
	hub, mips, cloud := &fakeHub{value: false}, &fakeCloudMIPS{}, &reconcileCloud{}
	provider, err := newProvider("xiaomi-main", "米家", config, func() hubClient { return hub })
	if err != nil {
		t.Fatal(err)
	}
	provider.cloudMIPS, provider.cloud = mips, cloud
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	before := cloud.calls.Load()
	mips.connection(cloudMIPSConnectionEvent{Connected: true, Reconnected: true})
	for cloud.calls.Load() == before && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	if cloud.calls.Load() != before+1 || provider.ProviderMetrics()["cloudHttpReconcileReads"] != 1 {
		t.Fatalf("calls=%d metrics=%#v", cloud.calls.Load(), provider.ProviderMetrics())
	}
}

func TestProviderReconfigureUpdatesCloudSubscriptionsWithoutReconnect(t *testing.T) {
	config := testConfig()
	config.OAuth = &OAuthConfig{Region: "cn", OAuthUUID: "uuid", ClientID: "client", AccessToken: "old"}
	hub := &fakeHub{value: false}
	provider, err := newProvider("xiaomi-main", "米家", config, func() hubClient { return hub })
	if err != nil {
		t.Fatal(err)
	}
	cloud := &fakeCloudMIPS{}
	provider.cloudMIPS = cloud
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := provider.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)

	nextConfig := config
	nextConfig.PollIntervalSec = 1800
	nextConfig.OAuth = &OAuthConfig{Region: "cn", OAuthUUID: "uuid", ClientID: "client", AccessToken: "new"}
	second := config.Devices[0]
	second.DID, second.ID, second.Name = "456", "xiaomi-switch-2", "米家开关 2"
	nextConfig.Devices = append(append([]DeviceConfig(nil), config.Devices...), second)
	next, err := newProvider("xiaomi-main", "米家", nextConfig, func() hubClient { return &fakeHub{} })
	if err != nil {
		t.Fatal(err)
	}
	applied, err := provider.Reconfigure(ctx, next)
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	cloud.mu.Lock()
	connects, dids, token := cloud.connects, append([]string(nil), cloud.dids...), cloud.token
	cloud.mu.Unlock()
	if connects != 1 || !reflect.DeepEqual(dids, []string{"123", "456"}) || token != "new" || provider.config.PollIntervalSec != 1800 {
		t.Fatalf("connects=%d dids=%#v token=%q", connects, dids, token)
	}
}
