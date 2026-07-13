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
	want := map[Type]bool{TypeSwitch: false, TypeLightbulb: false, TypeOutlet: false, TypeTemperatureSensor: false, TypeHumiditySensor: false, TypeContactSensor: false, TypeMotionSensor: false, TypeFan: false, TypeAirPurifier: false, TypeWindowCovering: false}
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
