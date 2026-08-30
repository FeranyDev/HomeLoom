package tuya

import (
	"context"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

func TestCommonSemanticProjectionAndReverseWrite(t *testing.T) {
	minimum, maximum, step := 10.0, 1000.0, 5.0
	api := &fakeAPI{
		devices: []TuyaDevice{{ID: "semantic-light", Name: "灯", Category: "dj", Online: true, Status: []TuyaStatus{{Code: "switch_led", Value: true}, {Code: "bright_value", Value: 650}}}},
		specs: map[string]TuyaSpecification{"semantic-light": {
			Functions: []DPSpec{{Code: "switch_led", Type: DPTypeBoolean}, {Code: "bright_value", Type: DPTypeInteger, Min: &minimum, Max: &maximum, Step: &step, Scale: 1}},
			Status:    []DPSpec{{Code: "switch_led", Type: DPTypeBoolean}, {Code: "bright_value", Type: DPTypeInteger, Min: &minimum, Max: &maximum, Step: &step, Scale: 1}},
		}},
	}
	provider := testProvider(t, api)
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("devices=%#v err=%v", items, err)
	}
	item := items[0]
	power, ok := item.Property("main", "switch", "power")
	if !ok || power.Value.Bool == nil || !*power.Value.Bool || !power.Definition.Writable {
		t.Fatalf("power=%#v", power)
	}
	brightness, ok := item.Property("main", "light", "brightness")
	if !ok || brightness.Value.Number == nil || *brightness.Value.Number != 65 {
		t.Fatalf("brightness=%#v", brightness)
	}
	if brightness.Definition.Min == nil || *brightness.Definition.Min != 1 || brightness.Definition.Max == nil || *brightness.Definition.Max != 100 || brightness.Definition.Step == nil || *brightness.Definition.Step != 0.5 {
		t.Fatalf("brightness range=%#v", brightness.Definition)
	}

	updated, err := provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{
		DeviceID: "tuya-semantic-light", EndpointID: "main", CapabilityID: "light", PropertyID: "brightness", Value: device.NumberValue(42.5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.commands) != 1 || api.commands[0].items[0].Code != "bright_value" || api.commands[0].items[0].Value != int64(425) {
		t.Fatalf("commands=%#v", api.commands)
	}
	if property, ok := updated.Property("main", "light", "brightness"); !ok || property.Value.Number == nil || *property.Value.Number != 42.5 {
		t.Fatalf("updated brightness=%#v", property)
	}
}

func TestCommonSemanticProjectionCoversSensorsAndAppliances(t *testing.T) {
	minimumTemp, maximumTemp, scale := -400.0, 1200.0, 1.0
	minimumHumidity, maximumHumidity := 0.0, 1000.0
	api := &fakeAPI{
		devices: []TuyaDevice{
			{ID: "semantic-climate", Name: "温湿度", Category: "wsdcg", Online: true, Status: []TuyaStatus{{Code: "va_temperature", Value: 235}, {Code: "va_humidity", Value: 523}}},
			{ID: "semantic-fan", Name: "风扇", Category: "fs", Online: true, Status: []TuyaStatus{{Code: "switch", Value: true}, {Code: "fan_speed", Value: "medium"}, {Code: "mode", Value: "natural"}}},
		},
		specs: map[string]TuyaSpecification{
			"semantic-climate": {Status: []DPSpec{{Code: "va_temperature", Type: DPTypeInteger, Min: &minimumTemp, Max: &maximumTemp, Scale: int(scale)}, {Code: "va_humidity", Type: DPTypeInteger, Min: &minimumHumidity, Max: &maximumHumidity, Scale: 1}}},
			"semantic-fan":     {Functions: []DPSpec{{Code: "switch", Type: DPTypeBoolean}, {Code: "fan_speed", Type: DPTypeEnum, EnumValues: []string{"low", "medium", "high"}}, {Code: "mode", Type: DPTypeEnum, EnumValues: []string{"normal", "natural"}}}, Status: []DPSpec{{Code: "switch", Type: DPTypeBoolean}, {Code: "fan_speed", Type: DPTypeEnum, EnumValues: []string{"low", "medium", "high"}}, {Code: "mode", Type: DPTypeEnum, EnumValues: []string{"normal", "natural"}}}},
		},
	}
	provider := testProvider(t, api)
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, _ := provider.DiscoverDevices(context.Background())
	if len(items) != 2 {
		t.Fatalf("devices=%#v", items)
	}
	climate, _ := findDevice(items, "tuya-semantic-climate")
	if climate.Type != device.TypeTemperatureHumiditySensor {
		t.Fatalf("climate type=%q", climate.Type)
	}
	if property, ok := climate.Property("main", "temperature", "current-temperature"); !ok || property.Value.Number == nil || *property.Value.Number != 23.5 {
		t.Fatalf("temperature=%#v", property)
	}
	if property, ok := climate.Property("main", "humidity", "current-humidity"); !ok || property.Value.Number == nil || *property.Value.Number != 52.3 {
		t.Fatalf("humidity=%#v", property)
	}
	fan, _ := findDevice(items, "tuya-semantic-fan")
	if property, ok := fan.Property("main", "fan", "active"); !ok || property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatalf("fan active=%#v", property)
	}
	if property, ok := fan.Property("main", "fan", "speed-level"); !ok || property.Value.String == nil || *property.Value.String != "medium" {
		t.Fatalf("fan speed=%#v", property)
	}
}

func findDevice(items []device.Device, id string) (device.Device, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return device.Device{}, false
}
