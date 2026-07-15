package device

import (
	"strings"
	"testing"
	"time"
)

func contractDevice(deviceType Type, properties ...struct {
	capability string
	property   Property
}) Device {
	capabilities := make(map[string][]Property)
	for _, item := range properties {
		capabilities[item.capability] = append(capabilities[item.capability], item.property)
	}
	result := Device{SchemaVersion: SchemaVersion, ID: "contract-device", ProviderID: "contract-provider", Type: deviceType, LastUpdateAt: time.Now().UTC()}
	result.SetOnline(true)
	endpoint := Endpoint{ID: "main", Type: string(deviceType)}
	for capabilityID, items := range capabilities {
		endpoint.Capabilities = append(endpoint.Capabilities, Capability{ID: capabilityID, Type: capabilityID, Properties: items})
	}
	result.Endpoints = []Endpoint{endpoint}
	return result
}

func boolProperty(id string, value bool) Property {
	return Property{Definition: PropertyDefinition{ID: id, Type: ValueTypeBool, Readable: true}, Value: BoolValue(value)}
}

func enumPropertyForContract(id, value string, options ...string) Property {
	return Property{Definition: PropertyDefinition{ID: id, Type: ValueTypeEnum, Readable: true, Enum: options}, Value: EnumValue(value)}
}

func TestPublisherModelRequiresRequiredParametersAndClassifiesCustom(t *testing.T) {
	missing := contractDevice(TypeLightbulb, struct {
		capability string
		property   Property
	}{"light", Property{Definition: PropertyDefinition{ID: "brightness", Type: ValueTypeNumber}, Value: NumberValue(50)}})
	if err := missing.NormalizeModelParameters(); err == nil || !strings.Contains(err.Error(), "required parameter") {
		t.Fatalf("missing required parameter error = %v", err)
	}

	item := contractDevice(TypeSwitch,
		struct {
			capability string
			property   Property
		}{"switch", boolProperty("power", false)},
		struct {
			capability string
			property   Property
		}{"vendor-acme", Property{Definition: PropertyDefinition{ID: "led-pattern", Type: ValueTypeString, Readable: true}, Value: StringValue("pulse")}},
	)
	if err := item.NormalizeModelParameters(); err != nil {
		t.Fatal(err)
	}
	power, _ := item.Property("main", "switch", "power")
	custom, _ := item.Property("main", "vendor-acme", "led-pattern")
	if power.Definition.ParameterLevel != ParameterRequired || custom.Definition.ParameterLevel != ParameterCustom {
		t.Fatalf("levels = %q, %q", power.Definition.ParameterLevel, custom.Definition.ParameterLevel)
	}

	extension := contractDevice(Type("vendor-device"), struct {
		capability string
		property   Property
	}{"vendor-acme", Property{Definition: PropertyDefinition{ID: "value", Type: ValueTypeString, Readable: true}, Value: StringValue("ready")}})
	if err := extension.NormalizeModelParameters(); err != nil {
		t.Fatal(err)
	}
	extensionProperty, _ := extension.Property("main", "vendor-acme", "value")
	if extensionProperty.Definition.ParameterLevel != ParameterCustom {
		t.Fatalf("extension parameter level = %q", extensionProperty.Definition.ParameterLevel)
	}
}

func TestConsumerProjectionGradesRequiredOptionalAndCustomMappings(t *testing.T) {
	item := contractDevice(TypeSwitch,
		struct {
			capability string
			property   Property
		}{"switch", boolProperty("power", true)},
		struct {
			capability string
			property   Property
		}{"vendor-acme", Property{Definition: PropertyDefinition{ID: "led-pattern", Type: ValueTypeString, Readable: true}, Value: StringValue("pulse")}},
	)
	if err := item.NormalizeModelParameters(); err != nil {
		t.Fatal(err)
	}
	contract := ConsumerModelContract{ConsumerID: "test-target", DeviceType: TypeSwitch, Parameters: []ConsumerParameterMapping{
		{Source: ParameterPath{EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}, Target: "Target.On", Level: ParameterRequired},
		{Source: ParameterPath{EndpointID: "main", CapabilityID: "switch", PropertyID: "indicator"}, Target: "Target.Indicator", Level: ParameterOptional},
		{Source: ParameterPath{EndpointID: "main", CapabilityID: "vendor-acme", PropertyID: "led-pattern"}, Target: "Target.VendorPattern", Level: ParameterCustom},
	}}
	projection, err := ProjectForConsumer(item, contract)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Parameters) != 2 || len(projection.MissingOptional) != 1 {
		t.Fatalf("projection = %#v", projection)
	}

	contract.Parameters[2].Level = ParameterOptional
	if _, err := ProjectForConsumer(item, contract); err == nil || !strings.Contains(err.Error(), "explicit custom") {
		t.Fatalf("implicit custom mapping error = %v", err)
	}
	contract.Parameters[2].Level = ParameterCustom
	contract.Parameters = append(contract.Parameters, ConsumerParameterMapping{Source: ParameterPath{EndpointID: "main", CapabilityID: "switch", PropertyID: "missing"}, Target: "Target.Required", Level: ParameterRequired})
	if _, err := ProjectForConsumer(item, contract); err == nil || !strings.Contains(err.Error(), "requires parameter") {
		t.Fatalf("missing consumer parameter error = %v", err)
	}
}

func TestModelCatalogCoversEverySupportedType(t *testing.T) {
	want := map[Type]bool{TypeSwitch: false, TypeLightbulb: false, TypeOutlet: false, TypeSinglePropertySensor: false, TypeTemperatureHumiditySensor: false, TypeContactSensor: false, TypeMotionSensor: false, TypeFan: false, TypeAirPurifier: false, TypeWindowCovering: false}
	for _, contract := range ModelContracts() {
		if _, exists := want[contract.DeviceType]; !exists || len(contract.Parameters) == 0 {
			t.Fatalf("unexpected contract %#v", contract)
		}
		want[contract.DeviceType] = true
	}
	for deviceType, found := range want {
		if !found {
			t.Fatalf("missing contract for %q", deviceType)
		}
	}
}

func TestSensorModelsExposeGenericAndCombinedMeasurements(t *testing.T) {
	tests := []struct {
		deviceType Type
		paths      []ParameterPath
		version    int
	}{
		{TypeSinglePropertySensor, []ParameterPath{{EndpointID: "main", CapabilityID: "sensor", PropertyID: "value"}}, 1},
		{TypeTemperatureHumiditySensor, []ParameterPath{{EndpointID: "main", CapabilityID: "temperature", PropertyID: "current-temperature"}, {EndpointID: "main", CapabilityID: "humidity", PropertyID: "current-humidity"}}, 1},
	}
	for _, test := range tests {
		contract, ok := ModelContractFor(test.deviceType)
		if !ok || contract.Version != test.version || len(contract.Parameters) != len(test.paths)+2 {
			t.Fatalf("contract %q = %#v", test.deviceType, contract)
		}
		for index, path := range test.paths {
			parameter := contract.Parameters[index]
			if parameter.Path != path || parameter.Level != ParameterRequired || parameter.Type != ValueTypeNumber {
				t.Errorf("parameter %q[%d] = %#v", test.deviceType, index, parameter)
			}
			if test.deviceType == TypeSinglePropertySensor && (parameter.Unit != "" || parameter.Min != nil || parameter.Max != nil) {
				t.Errorf("generic sensor unexpectedly fixes semantics: %#v", parameter)
			}
		}
		batteryLevel, batteryLow := contract.Parameters[len(test.paths)], contract.Parameters[len(test.paths)+1]
		if batteryLevel.Path != (ParameterPath{EndpointID: "main", CapabilityID: "battery", PropertyID: "level"}) || batteryLevel.Level != ParameterOptional || batteryLevel.Type != ValueTypeInt || batteryLevel.Unit != "percent" {
			t.Errorf("battery level %q = %#v", test.deviceType, batteryLevel)
		}
		if batteryLow.Path != (ParameterPath{EndpointID: "main", CapabilityID: "battery", PropertyID: "low"}) || batteryLow.Level != ParameterOptional || batteryLow.Type != ValueTypeBool {
			t.Errorf("low battery %q = %#v", test.deviceType, batteryLow)
		}
	}
}

func TestLegacyTemperatureAndHumiditySnapshotsNormalizeToGenericSensor(t *testing.T) {
	for _, test := range []struct {
		legacy                     Type
		capability, property, unit string
	}{
		{TypeTemperatureSensor, "temperature", "current-temperature", "celsius"},
		{TypeHumiditySensor, "humidity", "current-humidity", "percent"},
	} {
		item := contractDevice(test.legacy, struct {
			capability string
			property   Property
		}{test.capability, Property{Definition: PropertyDefinition{ID: test.property, Name: test.property, Type: ValueTypeNumber, Unit: test.unit, Readable: true}, Value: NumberValue(21.5)}}, struct {
			capability string
			property   Property
		}{"battery", Property{Definition: PropertyDefinition{ID: "level", Name: "Battery", Type: ValueTypeInt, Unit: "percent", Readable: true}, Value: IntValue(80)}}, struct {
			capability string
			property   Property
		}{"battery", Property{Definition: PropertyDefinition{ID: "low", Name: "Low Battery", Type: ValueTypeBool, Readable: true}, Value: BoolValue(false)}}, struct {
			capability string
			property   Property
		}{"vendor", Property{Definition: PropertyDefinition{ID: "raw", Name: "Raw", Type: ValueTypeString, Readable: true}, Value: StringValue("kept")}})
		if err := item.NormalizeModelParameters(); err != nil {
			t.Fatal(err)
		}
		property, found := item.Property("main", "sensor", "value")
		batteryLevel, batteryFound := item.Property("main", "battery", "level")
		batteryLow, lowFound := item.Property("main", "battery", "low")
		custom, customFound := item.Property("main", "vendor", "raw")
		if item.Type != TypeSinglePropertySensor || !found || property.Definition.Unit != test.unit || !batteryFound || batteryLevel.Definition.ParameterLevel != ParameterOptional || !lowFound || batteryLow.Definition.ParameterLevel != ParameterOptional || !customFound || custom.Definition.ParameterLevel != ParameterCustom {
			t.Fatalf("normalized %q = %#v", test.legacy, item)
		}
	}
}
