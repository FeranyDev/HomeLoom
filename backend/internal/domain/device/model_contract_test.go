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
	want := map[Type]bool{
		TypeSwitch: false, TypeLightbulb: false, TypeOutlet: false,
		TypeTemperatureSensor: false, TypeHumiditySensor: false, TypeTemperatureHumiditySensor: false,
		TypePressureSensor: false, TypeNoiseSensor: false, TypeWaterLevelSensor: false, TypeSoilMoistureSensor: false,
		TypeContactSensor: false, TypeMotionSensor: false,
		TypeFan: false, TypeAirPurifier: false, TypeWindowCovering: false,
		TypeIlluminanceSensor: false, TypeOccupancySensor: false, TypeLeakSensor: false,
		TypeSmokeSensor: false, TypeCarbonMonoxideSensor: false, TypeCarbonDioxideSensor: false,
		TypeAirQualitySensor: false, TypeThermostat: false, TypeAirConditioner: false, TypeHeaterCooler: false,
		TypeHumidifierDehumidifier: false, TypeLock: false, TypeGarageDoor: false,
		TypeSecuritySystem: false, TypeValve: false, TypePump: false, TypeWaterHeater: false,
		TypePowerMeter: false, TypeEVCharger: false, TypeSpeaker: false, TypeRobotVacuum: false,
	}
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

func TestExpandedModelContractsExposeCompleteMetadata(t *testing.T) {
	if got := len(ModelContracts()); got != 36 {
		t.Fatalf("built-in model count = %d, want 36", got)
	}
	for _, contract := range ModelContracts() {
		if contract.Name == "" || contract.Version < 1 || !contract.BuiltIn {
			t.Errorf("incomplete model metadata: %#v", contract)
		}
		for _, parameter := range contract.Parameters {
			if parameter.Path.EndpointID == "" || parameter.Path.CapabilityID == "" || parameter.Path.PropertyID == "" || parameter.Name == "" {
				t.Errorf("incomplete parameter path in %q: %#v", contract.DeviceType, parameter)
			}
			if parameter.Type == ValueTypeEnum && len(parameter.Enum) == 0 {
				t.Errorf("enum parameter has no values in %q: %s", contract.DeviceType, parameter.Path)
			}
			if parameter.Min != nil && parameter.Max != nil && *parameter.Min > *parameter.Max {
				t.Errorf("invalid range in %q: %#v", contract.DeviceType, parameter)
			}
		}
	}

	thermostat, ok := ModelContractFor(TypeThermostat)
	if !ok {
		t.Fatal("thermostat contract is missing")
	}
	paths := make(map[string]ModelParameter, len(thermostat.Parameters))
	for _, parameter := range thermostat.Parameters {
		paths[parameter.Path.String()] = parameter
	}
	if target := paths["main/temperature/target-temperature"]; target.Level != ParameterRequired || !target.Writable || target.Unit != "celsius" {
		t.Errorf("thermostat target temperature = %#v", target)
	}
	if mode := paths["main/thermostat/target-mode"]; mode.Type != ValueTypeEnum || !mode.Writable || len(mode.Enum) != 4 {
		t.Errorf("thermostat target mode = %#v", mode)
	}

	airConditioner, ok := ModelContractFor(TypeAirConditioner)
	if !ok {
		t.Fatal("air conditioner contract is missing")
	}
	if airConditioner.Version != 3 {
		t.Fatalf("air conditioner version = %d", airConditioner.Version)
	}
	airConditionerPaths := make(map[string]ModelParameter, len(airConditioner.Parameters))
	for _, parameter := range airConditioner.Parameters {
		airConditionerPaths[parameter.Path.String()] = parameter
	}
	if target := airConditionerPaths["main/temperature/target-temperature"]; target.Level != ParameterRequired || !target.Writable || target.Min == nil || *target.Min != 16 || target.Max == nil || *target.Max != 32 {
		t.Errorf("air conditioner target temperature = %#v", target)
	}
	if mode := airConditionerPaths["main/air-conditioner/target-mode"]; mode.Type != ValueTypeEnum || !mode.Writable || len(mode.Enum) != 6 {
		t.Errorf("air conditioner target mode = %#v", mode)
	}
	for _, path := range []string{"main/air-conditioner/current-state", "main/temperature/current-temperature"} {
		if parameter := airConditionerPaths[path]; parameter.Level != ParameterOptional {
			t.Errorf("air conditioner source-dependent parameter %s = %#v", path, parameter)
		}
	}
	for _, path := range []string{"main/air-conditioner/fan-speed", "main/air-conditioner/vertical-swing", "main/air-conditioner/horizontal-swing", "main/air-conditioner/auxiliary-heat", "main/air-conditioner/sleep-mode", "main/filter/life-level"} {
		if _, found := airConditionerPaths[path]; !found {
			t.Errorf("air conditioner optional parameter %s is missing", path)
		}
	}
}

func TestModelContractsDoNotContainDuplicatePropertyPaths(t *testing.T) {
	for _, contract := range ModelContracts() {
		seen := make(map[string]bool, len(contract.Parameters))
		for _, parameter := range contract.Parameters {
			key := parameter.Path.Key()
			if seen[key] {
				t.Errorf("model %q contains duplicate property path %s", contract.DeviceType, parameter.Path)
			}
			seen[key] = true
		}
	}
	if _, found := ModelContractFor(Type("single-property-sensor")); found {
		t.Fatal("removed generic single-property sensor remains in the built-in catalog")
	}
}

func TestSensorModelsExposeExplicitMeasurementSemantics(t *testing.T) {
	tests := []struct {
		deviceType Type
		paths      []ParameterPath
	}{
		{TypeTemperatureSensor, []ParameterPath{{EndpointID: "main", CapabilityID: "temperature", PropertyID: "current-temperature"}}},
		{TypeHumiditySensor, []ParameterPath{{EndpointID: "main", CapabilityID: "humidity", PropertyID: "current-humidity"}}},
		{TypeTemperatureHumiditySensor, []ParameterPath{{EndpointID: "main", CapabilityID: "temperature", PropertyID: "current-temperature"}, {EndpointID: "main", CapabilityID: "humidity", PropertyID: "current-humidity"}}},
		{TypePressureSensor, []ParameterPath{{EndpointID: "main", CapabilityID: "pressure", PropertyID: "current-pressure"}}},
		{TypeNoiseSensor, []ParameterPath{{EndpointID: "main", CapabilityID: "noise", PropertyID: "current-level"}}},
		{TypeWaterLevelSensor, []ParameterPath{{EndpointID: "main", CapabilityID: "water-level", PropertyID: "current-level"}}},
		{TypeSoilMoistureSensor, []ParameterPath{{EndpointID: "main", CapabilityID: "soil-moisture", PropertyID: "current-moisture"}}},
	}
	for _, test := range tests {
		contract, ok := ModelContractFor(test.deviceType)
		if !ok {
			t.Fatalf("contract %q = %#v", test.deviceType, contract)
		}
		parameters := make(map[ParameterPath]ModelParameter, len(contract.Parameters))
		for _, parameter := range contract.Parameters {
			parameters[parameter.Path] = parameter
		}
		for _, path := range test.paths {
			parameter, found := parameters[path]
			if !found || parameter.Level != ParameterRequired || parameter.Type != ValueTypeNumber || parameter.Unit == "" || parameter.Min == nil || parameter.Max == nil {
				t.Errorf("semantic measurement %q %s = %#v", test.deviceType, path, parameter)
			}
		}
		for _, path := range []ParameterPath{
			{EndpointID: "main", CapabilityID: "battery", PropertyID: "level"},
			{EndpointID: "main", CapabilityID: "battery", PropertyID: "low"},
			{EndpointID: "main", CapabilityID: "battery", PropertyID: "charging"},
			{EndpointID: "main", CapabilityID: "status", PropertyID: "fault"},
		} {
			if parameter, found := parameters[path]; !found || parameter.Level != ParameterOptional {
				t.Errorf("shared sensor capability %q %s = %#v", test.deviceType, path, parameter)
			}
		}
	}
}

func TestTemperatureAndHumiditySnapshotsKeepExplicitModelSemantics(t *testing.T) {
	for _, test := range []struct {
		deviceType                 Type
		capability, property, unit string
	}{
		{TypeTemperatureSensor, "temperature", "current-temperature", "celsius"},
		{TypeHumiditySensor, "humidity", "current-humidity", "percent"},
	} {
		item := contractDevice(test.deviceType, struct {
			capability string
			property   Property
		}{test.capability, Property{Definition: PropertyDefinition{ID: test.property, Name: test.property, Type: ValueTypeNumber, Unit: test.unit, Readable: true}, Value: NumberValue(21.5)}}, struct {
			capability string
			property   Property
		}{"vendor", Property{Definition: PropertyDefinition{ID: "raw", Name: "Raw", Type: ValueTypeString, Readable: true}, Value: StringValue("kept")}})
		if err := item.NormalizeModelParameters(); err != nil {
			t.Fatal(err)
		}
		property, found := item.Property("main", test.capability, test.property)
		custom, customFound := item.Property("main", "vendor", "raw")
		if item.Type != test.deviceType || !found || property.Definition.Unit != test.unit || property.Definition.ParameterLevel != ParameterRequired || !customFound || custom.Definition.ParameterLevel != ParameterCustom {
			t.Fatalf("normalized %q = %#v", test.deviceType, item)
		}
	}
}
