package sonoff

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/sonoff/cloud"
	"github.com/feranydev/homeloom/backend/internal/providers/sonoff/lan"
)

type fakeLAN struct {
	commands []string
	err      error
	state    map[string]any
}

func (f *fakeLAN) Command(_ context.Context, _ lan.Request, command string, _ map[string]any) (map[string]any, error) {
	f.commands = append(f.commands, command)
	if f.err != nil {
		return nil, f.err
	}
	return map[string]any{"data": f.state}, nil
}
func (f *fakeLAN) GetState(_ context.Context, _ lan.Request) (map[string]any, error) {
	return map[string]any{"data": f.state}, f.err
}

type fakeCloud struct {
	devices []cloud.Device
	states  []map[string]any
	err     error
}

type pollingCloud struct {
	mu      sync.Mutex
	devices []cloud.Device
}

func (f *pollingCloud) ListDevices(context.Context) ([]cloud.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cloud.Device(nil), f.devices...), nil
}

func (f *pollingCloud) SetDeviceState(context.Context, string, map[string]any) error { return nil }

func (f *pollingCloud) setSwitch(state string) {
	f.mu.Lock()
	f.devices[0].Params = map[string]any{"switch": state}
	f.mu.Unlock()
}

type fakeRealtime struct {
	ready   chan struct{}
	release chan struct{}
	event   cloud.RealtimeEvent
}

func (f *fakeRealtime) Subscribe(ctx context.Context, handler func(cloud.RealtimeEvent)) error {
	select {
	case <-f.ready:
	default:
		close(f.ready)
	}
	select {
	case <-f.release:
		if handler != nil {
			handler(f.event)
		}
		<-ctx.Done()
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeCloud) ListDevices(context.Context) ([]cloud.Device, error) {
	return f.devices, f.err
}
func (f *fakeCloud) SetDeviceState(_ context.Context, _ string, params map[string]any) error {
	f.states = append(f.states, params)
	return f.err
}

func TestProviderUsesLANForConfiguredDevice(t *testing.T) {
	local := &fakeLAN{state: map[string]any{"switch": "on"}}
	provider, err := NewProviderWithTransports(providerconfig.Config{ID: "sonoff-main", Name: "Sonoff", Config: []byte(`{"mode":"local","devices":[{"id":"living-switch","deviceId":"1000abc","name":"客厅开关","uiid":1,"deviceKey":"key","host":"127.0.0.1","params":{"switch":"off"}}]}`)}, local, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	updated, err := provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: "living-switch", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)})
	if err != nil {
		t.Fatal(err)
	}
	if len(local.commands) != 1 || local.commands[0] != "switch" {
		t.Fatalf("LAN commands = %#v", local.commands)
	}
	property, ok := updated.Property("main", "switch", "power")
	if !ok || property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatalf("updated property = %#v", property)
	}
}

func TestProviderAutoFallsBackToCloud(t *testing.T) {
	local := &fakeLAN{err: errors.New("LAN offline")}
	remote := &fakeCloud{}
	provider, err := NewProviderWithTransports(providerconfig.Config{ID: "sonoff-main", Config: []byte(`{"mode":"auto","devices":[{"id":"switch","deviceId":"1000abc","name":"开关","uiid":1,"deviceKey":"key","host":"192.0.2.10","params":{"switch":"off"}}]}`)}, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = provider.DiscoverDevices(context.Background())
	if _, err := provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: "switch", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)}); err != nil {
		t.Fatal(err)
	}
	if len(remote.states) != 1 || remote.states[0]["switch"] != "on" {
		t.Fatalf("cloud states = %#v", remote.states)
	}
}

func TestProviderRefreshIntervalPollsCloudAndPublishesState(t *testing.T) {
	remote := &pollingCloud{devices: []cloud.Device{{DeviceID: "pulse", Name: "微动开关", UIID: 1, Online: true, Params: map[string]any{"switch": "on"}}}}
	provider, err := NewProviderWithTransports(providerconfig.Config{ID: "sonoff-main", Config: []byte(`{"mode":"cloud","managedDevices":true,"refreshIntervalSeconds":15,"cloud":{"accessToken":"token"},"devices":[{"id":"sonoff-pulse","deviceId":"pulse","name":"微动开关","uiid":1}]}`)}, nil, remote)
	if err != nil {
		t.Fatal(err)
	}
	provider.pollInterval = 5 * time.Millisecond
	if _, err := provider.DiscoverDevices(context.Background()); err != nil {
		t.Fatal(err)
	}
	updates := make(chan device.Device, 2)
	unsubscribe := provider.Subscribe(func(item device.Device) { updates <- item })
	defer unsubscribe()
	remote.setSwitch("off")
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer provider.Close(context.Background())
	select {
	case updated := <-updates:
		power, _ := updated.Property("main", "switch", "power")
		if power.Value.Bool == nil || *power.Value.Bool {
			t.Fatalf("polled property = %#v", power)
		}
	case <-time.After(time.Second):
		t.Fatal("configured refresh interval did not publish cloud state")
	}
	if provider.ProviderMetrics()["polls"] == 0 || provider.ProviderDiagnostics()["refreshIntervalSeconds"] != "15" {
		t.Fatalf("metrics = %#v, diagnostics = %#v", provider.ProviderMetrics(), provider.ProviderDiagnostics())
	}
}

func TestManagedProviderPublishesOnlySavedDevicesAndRetainsThemWhenCloudFails(t *testing.T) {
	remote := &fakeCloud{devices: []cloud.Device{
		{DeviceID: "saved", Name: "eWeLink_saved", UIID: 1, Online: true, DeviceKey: "saved-secret", Params: map[string]any{"switch": "on"}},
		{DeviceID: "transient", Name: "未选择开关", UIID: 1, Online: true, DeviceKey: "other-secret", Params: map[string]any{"switch": "off"}},
	}}
	provider, err := NewProviderWithTransports(providerconfig.Config{ID: "sonoff-main", Config: []byte(`{"mode":"auto","managedDevices":true,"cloud":{"accessToken":"token"},"devices":[{"id":"sonoff-saved","deviceId":"saved","name":"门口微动开关","uiid":1}]}`)}, &fakeLAN{}, remote)
	if err != nil {
		t.Fatal(err)
	}
	items, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != "sonoff-saved" || items[0].Name != "门口微动开关" || !items[0].IsOnline() {
		t.Fatalf("managed discovery = %#v, %v", items, err)
	}

	remote.err = errors.New("temporary eWeLink outage")
	items, err = provider.DiscoverDevices(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != "sonoff-saved" || items[0].IsOnline() {
		t.Fatalf("retained offline discovery = %#v, %v", items, err)
	}
}

func TestCloudDirectoryIsCompleteAndDoesNotExposeDeviceKeys(t *testing.T) {
	remote := &fakeCloud{devices: []cloud.Device{{
		DeviceID: "1001f95735", Name: "双路开关", Model: "DUALR3", UIID: 7, Online: true,
		DeviceKey: "must-not-leak", HomeID: "home-1", HomeName: "我的家", RoomID: "room-1", RoomName: "客厅",
		Params: map[string]any{"switches": []any{map[string]any{"outlet": 0}, map[string]any{"outlet": 1}}},
	}}}
	provider, err := NewProviderWithTransports(providerconfig.Config{ID: "sonoff-main", Config: []byte(`{"mode":"auto","cloud":{"accessToken":"token"},"devices":[{"id":"sonoff-1001f95735","deviceId":"1001f95735","name":"旧名称","uiid":7}]}`)}, &fakeLAN{}, remote)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := provider.DiscoverCloudDevices(context.Background())
	if err != nil || len(directory) != 1 {
		t.Fatalf("cloud directory = %#v, %v", directory, err)
	}
	item := directory[0]
	if item.ID != "sonoff-1001f95735" || item.DeviceID != "1001f95735" || item.Name != "旧名称" || item.Channels != 2 || !item.Configured || !item.Online || item.HomeName != "我的家" || item.RoomName != "客厅" {
		t.Fatalf("directory item = %#v", item)
	}
	encoded, err := json.Marshal(directory)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "must-not-leak") || strings.Contains(strings.ToLower(string(encoded)), "devicekey") || strings.Contains(string(encoded), `"params"`) {
		t.Fatalf("directory leaked private cloud material: %s", encoded)
	}
}

func TestProviderRejectsOutOfRangePropertyWrite(t *testing.T) {
	local := &fakeLAN{}
	provider, err := NewProviderWithTransports(providerconfig.Config{ID: "sonoff-main", Config: []byte(`{"mode":"local","devices":[{"id":"light","deviceId":"1000abc","name":"灯","uiid":36,"deviceKey":"key","host":"127.0.0.1"}]}`)}, local, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DiscoverDevices(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: "light", EndpointID: "main", CapabilityID: "light", PropertyID: "brightness", Value: device.NumberValue(101)})
	if !errors.Is(err, providersdk.ErrPropertyInvalid) {
		t.Fatalf("error = %v, want ErrPropertyInvalid", err)
	}
	if len(local.commands) != 0 {
		t.Fatalf("LAN commands = %#v, want no command", local.commands)
	}
}

func TestProviderBuildsCloudCatalogAndPreservesUnknownParams(t *testing.T) {
	remote := &fakeCloud{devices: []cloud.Device{{DeviceID: "1000abc", Name: "云端插座", Model: "POWR3", UIID: 32, Online: true, DeviceKey: "secret", Params: map[string]any{"switch": "on", "power": 8.0, "vendorFuture": true}}}}
	provider, err := NewProviderWithTransports(providerconfig.Config{ID: "sonoff-main", Config: []byte(`{"mode":"cloud","cloud":{"endpoint":"https://cloud.example","accessToken":"token"},"devices":[]}`)}, nil, remote)
	if err != nil {
		t.Fatal(err)
	}
	items, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	if items[0].Type != device.TypeOutlet {
		t.Fatalf("type = %q", items[0].Type)
	}
	if _, ok := items[0].Property("main", "sonoff-raw", "vendorfuture"); !ok {
		t.Fatal("unknown cloud parameter was not retained")
	}
}

func TestProviderInfersCloudMultiChannelProtocolAndWritesOutlet(t *testing.T) {
	remote := &fakeCloud{devices: []cloud.Device{{
		DeviceID: "1000dual", Name: "云端双路开关", UIID: 7, Online: true,
		Params: map[string]any{"switch": "off", "switches": []any{
			map[string]any{"outlet": 0, "switch": "on"},
			map[string]any{"outlet": 1, "switch": "off"},
		}},
	}}}
	provider, err := NewProviderWithTransports(providerconfig.Config{ID: "sonoff-main", Config: []byte(`{"mode":"cloud","cloud":{"endpoint":"https://cloud.example","accessToken":"token"},"devices":[]}`)}, nil, remote)
	if err != nil {
		t.Fatal(err)
	}
	items, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(items) != 1 || len(items[0].Endpoints) != 2 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	property, ok := items[0].Property("channel-0", "switch", "power-0")
	if !ok || property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatalf("first channel = %#v", property)
	}
	_, err = provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{
		DeviceID: items[0].ID, EndpointID: "channel-1", CapabilityID: "switch", PropertyID: "power-1", Value: device.BoolValue(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(remote.states) != 1 {
		t.Fatalf("cloud states = %#v", remote.states)
	}
	switches, ok := remote.states[0]["switches"].([]map[string]any)
	if !ok || len(switches) != 1 || switches[0]["outlet"] != 1 || switches[0]["switch"] != "on" {
		t.Fatalf("cloud multi-channel command = %#v", remote.states[0])
	}
}

func TestProviderScanBuildsTransientLANCandidatesWithoutPersistingThem(t *testing.T) {
	provider, err := NewProviderWithTransports(providerconfig.Config{ID: "sonoff-main", Config: []byte(`{"mode":"local","devices":[{"id":"configured-switch","deviceId":"configured","name":"已配置开关","uiid":1,"deviceKey":"key","host":"192.0.2.9"}]}`)}, &fakeLAN{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider.discoverLAN = func(context.Context, time.Duration) ([]lan.Service, error) {
		return []lan.Service{
			{Instance: "New Plug", Address: "192.0.2.30", Port: 8081, TXT: map[string]string{"id": "new-device", "type": "plug", "encrypt": "true", "data1": `{"apikey":"must-not-leak"}`}},
			{Instance: "Existing", Address: "192.0.2.31", Port: 8081, TXT: map[string]string{"id": "configured", "type": "plug", "encrypt": "false"}},
			{Address: "192.0.2.32", Port: 8081, TXT: map[string]string{"type": "missing-id"}},
		}, nil
	}
	candidates, err := provider.Scan(context.Background())
	if err != nil || len(candidates) != 2 {
		t.Fatalf("candidates=%#v err=%v", candidates, err)
	}
	newCandidate := candidates[1]
	if newCandidate.ID != "sonoff-new-device" || newCandidate.Host != "192.0.2.30" || newCandidate.Metadata["deviceId"] != "new-device" || newCandidate.Metadata["encrypted"] != "true" {
		t.Fatalf("new candidate=%#v", newCandidate)
	}
	if _, leaked := newCandidate.Metadata["data1"]; leaked {
		t.Fatalf("candidate leaked TXT data: %#v", newCandidate)
	}
	if candidates[0].Metadata["configured"] != "true" {
		t.Fatalf("existing candidate was not marked configured: %#v", candidates[0])
	}
	if len(provider.config.Devices) != 1 || provider.config.Devices[0].DeviceID != "configured" {
		t.Fatalf("scan modified provider config: %#v", provider.config.Devices)
	}
}

func TestProviderAppliesInjectedRealtimeStateAndStopsOnClose(t *testing.T) {
	online := true
	realtime := &fakeRealtime{ready: make(chan struct{}), release: make(chan struct{}), event: cloud.RealtimeEvent{DeviceID: "1000abc", Params: map[string]any{"switch": "on"}, Online: &online}}
	provider, err := NewProviderWithTransportsAndRealtime(providerconfig.Config{ID: "sonoff-main", Config: []byte(`{"mode":"auto","cloud":{"accessToken":"token"},"devices":[{"id":"switch","deviceId":"1000abc","name":"开关","uiid":1,"deviceKey":"key","host":"192.0.2.10","params":{"switch":"off"}}]}`)}, &fakeLAN{}, &fakeCloud{}, realtime)
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan device.Device, 1)
	unsubscribe := provider.Subscribe(func(item device.Device) { updates <- item })
	defer unsubscribe()
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-realtime.ready
	if _, err := provider.DiscoverDevices(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(realtime.release)
	select {
	case item := <-updates:
		property, exists := item.Property("main", "switch", "power")
		if !exists || property.Value.Bool == nil || !*property.Value.Bool {
			t.Fatalf("realtime item=%#v", item)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for realtime update")
	}
	if got := provider.ProviderMetrics()["realtimeEvents"]; got != 1 {
		t.Fatalf("realtimeEvents=%d", got)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := provider.Close(closeCtx); err != nil {
		t.Fatalf("close provider: %v", err)
	}
}
