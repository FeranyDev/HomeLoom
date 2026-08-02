package tuya

import (
	"context"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

// tuyaCommonRawDP describes the small part of a Tuya specification that the
// provider publishes without interpreting it as a HomeLoom capability. The
// same DP is returned from status so that the test exercises the complete
// specification/status reconciliation path used by Refresh.
type tuyaCommonRawDP struct {
	code     string
	dpType   DPType
	min      *float64
	max      *float64
	step     *float64
	scale    int
	unit     string
	enum     []string
	rawValue any
	want     device.PropertyValue
	writable bool
}

type tuyaCommonDevice struct {
	name     string
	remoteID string
	category string
	wantType device.Type
	dps      []tuyaCommonRawDP
}

func TestTuyaProviderPublishesCommonRawDPContracts(t *testing.T) {
	common := []tuyaCommonDevice{
		{
			name:     "light",
			remoteID: "common-light",
			category: "dj",
			wantType: device.TypeLightbulb,
			dps: []tuyaCommonRawDP{
				{
					code: "switch_led", dpType: DPTypeBoolean, rawValue: true,
					want: device.BoolValue(true), writable: true,
				},
				{
					code: "bright_value", dpType: DPTypeInteger,
					min: tuyaCommonMappingFloat(10), max: tuyaCommonMappingFloat(1000), step: tuyaCommonMappingFloat(5),
					scale: 1, unit: "%", rawValue: 650,
					want: device.NumberValue(65), writable: true,
				},
				{
					code: "temp_value", dpType: DPTypeInteger,
					min: tuyaCommonMappingFloat(0), max: tuyaCommonMappingFloat(1000), step: tuyaCommonMappingFloat(1),
					scale: 0, unit: "K", rawValue: 450,
					want: device.IntValue(450), writable: true,
				},
				{
					code: "colour_data", dpType: DPTypeJSON,
					rawValue: `{"h":120,"s":80,"v":100}`,
					want:     device.StringValue(`{"h":120,"s":80,"v":100}`), writable: true,
				},
			},
		},
		{
			name:     "switch",
			remoteID: "common-switch",
			category: "kg",
			wantType: device.TypeSwitch,
			dps: []tuyaCommonRawDP{
				{
					code: "switch_1", dpType: DPTypeBoolean, rawValue: false,
					want: device.BoolValue(false), writable: true,
				},
			},
		},
		{
			name:     "outlet-energy",
			remoteID: "common-outlet",
			category: "cz",
			wantType: device.TypeOutlet,
			dps: []tuyaCommonRawDP{
				{
					code: "switch_1", dpType: DPTypeBoolean, rawValue: true,
					want: device.BoolValue(true), writable: true,
				},
				{
					code: "cur_power", dpType: DPTypeInteger,
					min: tuyaCommonMappingFloat(0), max: tuyaCommonMappingFloat(50000), step: tuyaCommonMappingFloat(1),
					scale: 1, unit: "W", rawValue: 1234,
					want: device.NumberValue(123.4),
				},
				{
					code: "cur_voltage", dpType: DPTypeInteger,
					min: tuyaCommonMappingFloat(0), max: tuyaCommonMappingFloat(3000), step: tuyaCommonMappingFloat(1),
					scale: 1, unit: "V", rawValue: 2300,
					want: device.NumberValue(230),
				},
				{
					code: "cur_current", dpType: DPTypeInteger,
					min: tuyaCommonMappingFloat(0), max: tuyaCommonMappingFloat(100000), step: tuyaCommonMappingFloat(1),
					scale: 3, unit: "A", rawValue: 567,
					want: device.NumberValue(0.567),
				},
				{
					code: "add_ele", dpType: DPTypeInteger,
					min: tuyaCommonMappingFloat(0), max: tuyaCommonMappingFloat(1000000), step: tuyaCommonMappingFloat(1),
					scale: 3, unit: "kWh", rawValue: 12345,
					want: device.NumberValue(12.345),
				},
			},
		},
		{
			name:     "fan",
			remoteID: "common-fan",
			category: "fs",
			wantType: device.TypeFan,
			dps: []tuyaCommonRawDP{
				{
					code: "switch", dpType: DPTypeBoolean, rawValue: true,
					want: device.BoolValue(true), writable: true,
				},
				{
					code: "fan_speed", dpType: DPTypeInteger,
					min: tuyaCommonMappingFloat(1), max: tuyaCommonMappingFloat(3), step: tuyaCommonMappingFloat(1), rawValue: 2,
					want: device.IntValue(2), writable: true,
				},
				{
					code: "mode", dpType: DPTypeEnum,
					enum: []string{"normal", "natural", "sleep"}, rawValue: "natural",
					want: device.EnumValue("natural"), writable: true,
				},
			},
		},
		{
			name:     "curtain",
			remoteID: "common-curtain",
			category: "cl",
			wantType: device.TypeWindowCovering,
			dps: []tuyaCommonRawDP{
				{
					code: "percent_control", dpType: DPTypeInteger,
					min: tuyaCommonMappingFloat(0), max: tuyaCommonMappingFloat(100), step: tuyaCommonMappingFloat(1), rawValue: 42,
					want: device.IntValue(42), writable: true,
				},
			},
		},
		{
			name:     "temperature-humidity",
			remoteID: "common-temp-humidity",
			category: "wsdcg",
			wantType: device.TypeTemperatureHumiditySensor,
			dps: []tuyaCommonRawDP{
				{
					code: "va_temperature", dpType: DPTypeInteger,
					min: tuyaCommonMappingFloat(-400), max: tuyaCommonMappingFloat(1200), step: tuyaCommonMappingFloat(1),
					scale: 1, unit: "℃", rawValue: 235,
					want: device.NumberValue(23.5),
				},
				{
					code: "va_humidity", dpType: DPTypeInteger,
					min: tuyaCommonMappingFloat(0), max: tuyaCommonMappingFloat(1000), step: tuyaCommonMappingFloat(1),
					scale: 1, unit: "%", rawValue: 523,
					want: device.NumberValue(52.3),
				},
			},
		},
		{
			name:     "humidity",
			remoteID: "common-humidity",
			category: "sj",
			wantType: device.TypeHumiditySensor,
			dps: []tuyaCommonRawDP{
				{
					code: "humidity_value", dpType: DPTypeInteger,
					min: tuyaCommonMappingFloat(0), max: tuyaCommonMappingFloat(1000), step: tuyaCommonMappingFloat(1),
					scale: 1, unit: "%", rawValue: 625,
					want: device.NumberValue(62.5),
				},
			},
		},
		{
			name:     "contact",
			remoteID: "common-contact",
			category: "mcs",
			wantType: device.TypeContactSensor,
			dps: []tuyaCommonRawDP{
				{code: "doorcontact", dpType: DPTypeBoolean, rawValue: false, want: device.BoolValue(false)},
			},
		},
		{
			name:     "motion",
			remoteID: "common-motion",
			category: "pir",
			wantType: device.TypeMotionSensor,
			dps: []tuyaCommonRawDP{
				{code: "pir", dpType: DPTypeBoolean, rawValue: true, want: device.BoolValue(true)},
			},
		},
		{
			name:     "leak",
			remoteID: "common-leak",
			category: "ywbj",
			wantType: device.TypeLeakSensor,
			dps: []tuyaCommonRawDP{
				{code: "watersensor", dpType: DPTypeBoolean, rawValue: true, want: device.BoolValue(true)},
			},
		},
		{
			name:     "smoke",
			remoteID: "common-smoke",
			category: "ywb",
			wantType: device.TypeSmokeSensor,
			dps: []tuyaCommonRawDP{
				{code: "smoke", dpType: DPTypeBoolean, rawValue: true, want: device.BoolValue(true)},
			},
		},
		{
			name:     "illuminance",
			remoteID: "common-illuminance",
			category: "illuminance",
			wantType: device.TypeIlluminanceSensor,
			dps: []tuyaCommonRawDP{
				{
					code: "illuminance", dpType: DPTypeInteger,
					min: tuyaCommonMappingFloat(0), max: tuyaCommonMappingFloat(100000), step: tuyaCommonMappingFloat(1),
					scale: 0, unit: "lux", rawValue: 380,
					want: device.IntValue(380),
				},
			},
		},
	}

	api := &fakeAPI{devices: make([]TuyaDevice, 0, len(common)), specs: make(map[string]TuyaSpecification, len(common))}
	for _, item := range common {
		status := make([]TuyaStatus, 0, len(item.dps))
		functions := make([]DPSpec, 0, len(item.dps))
		for _, dp := range item.dps {
			spec := dp.spec()
			status = append(status, TuyaStatus{Code: dp.code, Value: dp.rawValue})
			if dp.writable {
				functions = append(functions, spec)
			}
		}
		api.devices = append(api.devices, TuyaDevice{
			ID: item.remoteID, Name: item.name, Category: item.category, Online: true, Status: status,
		})
		api.specs[item.remoteID] = TuyaSpecification{Category: item.category, Functions: functions, Status: tuyaCommonStatusSpecs(item.dps)}
	}

	provider := testProvider(t, api)
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, err := provider.DiscoverDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(common) {
		t.Fatalf("published %d devices, want %d: %#v", len(items), len(common), items)
	}

	byID := make(map[string]device.Device, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	for _, want := range common {
		want := want
		t.Run(want.name, func(t *testing.T) {
			item, ok := byID[stableDeviceID(want.remoteID)]
			if !ok {
				t.Fatalf("device %q was not published", want.remoteID)
			}
			if item.Type != want.wantType {
				t.Fatalf("device type = %q, want %q", item.Type, want.wantType)
			}
			if !item.IsOnline() || item.StateTransport != device.StateTransportCloudHTTP {
				t.Fatalf("availability/transport = %q/%q, want online/cloud-http", item.EffectiveAvailability(), item.StateTransport)
			}
			if len(item.Endpoints) != 1 {
				t.Fatalf("raw endpoint shape = %#v", item.Endpoints)
			}
			var capability device.Capability
			for _, candidate := range item.Endpoints[0].Capabilities {
				if candidate.ID == "tuya-dp" {
					capability = candidate
					break
				}
			}
			if capability.ID != "tuya-dp" || len(capability.Properties) != len(want.dps) {
				t.Fatalf("raw capability = %#v", capability)
			}
			for _, dp := range want.dps {
				property, ok := item.Property("main", "tuya-dp", dp.code)
				if !ok {
					t.Fatalf("raw DP %q was not published", dp.code)
				}
				if !property.Value.Equal(dp.want) {
					t.Errorf("DP %q value = %#v, want %#v", dp.code, property.Value, dp.want)
				}
				if property.StateTransport != device.StateTransportCloudHTTP {
					t.Errorf("DP %q transport = %q, want %q", dp.code, property.StateTransport, device.StateTransportCloudHTTP)
				}
				if !property.Definition.Readable || property.Definition.Writable != dp.writable {
					t.Errorf("DP %q permissions = readable:%t writable:%t, want readable:true writable:%t", dp.code, property.Definition.Readable, property.Definition.Writable, dp.writable)
				}
				if property.Definition.Unit != normalizedCommonUnit(dp.unit) {
					t.Errorf("DP %q unit = %q, want %q", dp.code, property.Definition.Unit, normalizedCommonUnit(dp.unit))
				}
				assertCommonMappingFloat(t, dp.code+" min", property.Definition.Min, scaledCommonMappingFloat(dp.min, dp.scale))
				assertCommonMappingFloat(t, dp.code+" max", property.Definition.Max, scaledCommonMappingFloat(dp.max, dp.scale))
				assertCommonMappingFloat(t, dp.code+" step", property.Definition.Step, scaledCommonMappingFloat(dp.step, dp.scale))
				if len(property.Definition.Enum) != len(dp.enum) {
					t.Errorf("DP %q enum = %#v, want %#v", dp.code, property.Definition.Enum, dp.enum)
				} else {
					for index := range dp.enum {
						if property.Definition.Enum[index] != dp.enum[index] {
							t.Errorf("DP %q enum = %#v, want %#v", dp.code, property.Definition.Enum, dp.enum)
							break
						}
					}
				}
			}
		})
	}
}

func (dp tuyaCommonRawDP) spec() DPSpec {
	return DPSpec{
		Code: dp.code, Type: dp.dpType, Min: dp.min, Max: dp.max, Step: dp.step,
		Scale: dp.scale, Unit: dp.unit, EnumValues: append([]string(nil), dp.enum...), Readable: true,
		Writable: dp.writable,
	}
}

func tuyaCommonStatusSpecs(dps []tuyaCommonRawDP) []DPSpec {
	result := make([]DPSpec, 0, len(dps))
	for _, dp := range dps {
		spec := dp.spec()
		spec.Writable = false
		result = append(result, spec)
	}
	return result
}

func tuyaCommonMappingFloat(value float64) *float64 { return &value }

func scaledCommonMappingFloat(value *float64, scale int) *float64 {
	if value == nil {
		return nil
	}
	result := scaleValue(*value, scale)
	return &result
}

func assertCommonMappingFloat(t *testing.T, name string, got, want *float64) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
		return
	}
	if *got != *want {
		t.Errorf("%s = %v, want %v", name, *got, *want)
	}
}

func normalizedCommonUnit(value string) string {
	return normalizeUnit(value)
}
