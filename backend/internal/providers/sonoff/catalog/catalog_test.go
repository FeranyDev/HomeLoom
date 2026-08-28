package catalog

import (
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

func TestBuildDeviceMapsCommonPowerAndMeasurements(t *testing.T) {
	item, err := BuildDevice(DeviceInput{ID: "powr3", ProviderID: "sonoff-main", Name: "插座", UIID: 32, Online: true, Params: map[string]any{"switch": "on", "power": 12.5, "voltage": 220.0, "current": 0.2, "energy": 1.4, "future": "kept"}})
	if err != nil {
		t.Fatal(err)
	}
	if item.Type != device.TypeOutlet || !item.IsOnline() {
		t.Fatalf("device = %#v", item)
	}
	if property, ok := item.Property("main", "switch", "power"); !ok || property.StateTransport != device.StateTransportPending {
		t.Fatalf("default state transport = %#v", property)
	}
	if property, ok := item.Property("main", "switch", "power"); !ok || property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatalf("power = %#v", property)
	}
	if property, ok := item.Property("main", "electrical", "current-power"); !ok || property.Value.Number == nil || *property.Value.Number != 12.5 {
		t.Fatalf("current power = %#v", property)
	}
	if property, ok := item.Property("main", "sonoff-raw", "future"); !ok || property.Value.String == nil || *property.Value.String != "kept" {
		t.Fatalf("unknown parameter = %#v", property)
	}
}

func TestBuildDevicePropagatesStateTransportToProperties(t *testing.T) {
	item, err := BuildDevice(DeviceInput{ID: "switch", ProviderID: "sonoff-main", Name: "开关", UIID: 1, Online: true, RuntimeMode: string(device.RuntimeModeLocal), StateTransport: string(device.StateTransportLocalMQTT), Params: map[string]any{"switch": "on", "future": true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range item.Endpoints {
		for _, capability := range endpoint.Capabilities {
			for _, property := range capability.Properties {
				if property.StateTransport != device.StateTransportLocalMQTT {
					t.Fatalf("%s/%s/%s transport = %q", endpoint.ID, capability.ID, property.Definition.ID, property.StateTransport)
				}
			}
		}
	}
}

func TestBuildDeviceMapsTemperatureHumidityAndMultichannel(t *testing.T) {
	item, err := BuildDevice(DeviceInput{ID: "dual", ProviderID: "sonoff-main", Name: "双路", UIID: 7, Channels: 1, Online: true, Params: map[string]any{"switch": "off", "switches": []any{map[string]any{"switch": "on"}, map[string]any{"switch": "off"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Endpoints) != 2 {
		t.Fatalf("endpoints = %#v", item.Endpoints)
	}
	if property, ok := item.Property("channel-1", "switch", "power-1"); !ok || property.Value.Bool == nil || *property.Value.Bool {
		t.Fatalf("second channel = %#v", property)
	}
	if property, ok := item.Property("channel-0", "switch", "power-0"); !ok || property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatalf("first channel did not use per-outlet state = %#v", property)
	}
	climate, err := BuildDevice(DeviceInput{ID: "th", ProviderID: "sonoff-main", Name: "温湿度", UIID: 15, Online: true, Params: map[string]any{"temperature": 23.4, "humidity": 56.0}})
	if err != nil {
		t.Fatal(err)
	}
	if climate.Type != device.TypeTemperatureHumiditySensor {
		t.Fatalf("type = %q", climate.Type)
	}
}

func TestEncodePropertyCommandUsesZeroconfCommands(t *testing.T) {
	item, err := BuildDevice(DeviceInput{ID: "switch", ProviderID: "sonoff-main", Name: "开关", UIID: 1, Online: true, Params: map[string]any{"switch": "off"}})
	if err != nil {
		t.Fatal(err)
	}
	command, data, err := EncodePropertyCommand(item, providersdk.PropertyWriteRequest{DeviceID: item.ID, EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)})
	if err != nil || command != "switch" || data["switch"] != "on" {
		t.Fatalf("command=%q data=%#v err=%v", command, data, err)
	}
}
