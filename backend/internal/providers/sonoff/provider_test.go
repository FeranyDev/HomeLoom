package sonoff

import (
	"context"
	"errors"
	"testing"

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
