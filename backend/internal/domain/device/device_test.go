package device

import (
	"errors"
	"strings"
	"testing"
)

func TestPropertyLookupAndUpdate(t *testing.T) {
	item := Device{Endpoints: []Endpoint{{ID: "main", Capabilities: []Capability{{
		ID: "switch", Properties: []Property{{Definition: PropertyDefinition{ID: "power", Type: ValueTypeBool}, Value: BoolValue(false)}},
	}}}}}
	property, ok := item.Property("main", "switch", "power")
	if !ok || property.Value.Bool == nil || *property.Value.Bool {
		t.Fatalf("Property() = %#v, %v", property, ok)
	}
	if !item.SetProperty("main", "switch", "power", BoolValue(true)) {
		t.Fatal("SetProperty() returned false")
	}
	property, _ = item.Property("main", "switch", "power")
	if property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatalf("updated property = %#v", property)
	}
	if item.SetProperty("missing", "switch", "power", BoolValue(true)) {
		t.Fatal("SetProperty() updated a missing endpoint")
	}
}

func TestPropertyRejectsInvalidStateTransport(t *testing.T) {
	property := Property{Definition: PropertyDefinition{ID: "power", Type: ValueTypeBool}, Value: BoolValue(true), StateTransport: "invalid"}
	if err := property.Validate(); err == nil || !strings.Contains(err.Error(), "state transport") {
		t.Fatalf("validation error=%v", err)
	}
}

func TestTypedValues(t *testing.T) {
	boolean := BoolValue(true)
	integer := IntValue(42)
	number := NumberValue(23.5)
	text := StringValue("hello")
	enumerated := EnumValue("auto")
	if boolean.Type != ValueTypeBool || boolean.Bool == nil || !*boolean.Bool {
		t.Fatalf("BoolValue() = %#v", boolean)
	}
	if integer.Type != ValueTypeInt || integer.Int == nil || *integer.Int != 42 {
		t.Fatalf("IntValue() = %#v", integer)
	}
	if number.Type != ValueTypeNumber || number.Number == nil || *number.Number != 23.5 {
		t.Fatalf("NumberValue() = %#v", number)
	}
	if text.String == nil || *text.String != "hello" || enumerated.Type != ValueTypeEnum || enumerated.String == nil || *enumerated.String != "auto" {
		t.Fatalf("string values = %#v, %#v", text, enumerated)
	}
}

func TestAvailabilityNormalizationAndValidation(t *testing.T) {
	legacy := Device{Online: true}
	legacy.NormalizeAvailability()
	if legacy.Availability != AvailabilityOnline || !legacy.IsOnline() || !legacy.Online {
		t.Fatalf("normalized legacy availability = %#v", legacy)
	}
	legacy.SetAvailability(AvailabilityUnknown)
	if legacy.IsOnline() || legacy.Online || legacy.EffectiveAvailability() != AvailabilityUnknown {
		t.Fatalf("unknown availability = %#v", legacy)
	}
	invalid := Device{SchemaVersion: SchemaVersion, ID: "device", ProviderID: "provider", Availability: "missing"}
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidModel) {
		t.Fatalf("invalid availability error = %v", err)
	}
	conflict := Device{SchemaVersion: SchemaVersion, ID: "device", ProviderID: "provider", Availability: AvailabilityOnline}
	if err := conflict.Validate(); !errors.Is(err, ErrInvalidModel) {
		t.Fatalf("conflicting projection error = %v", err)
	}
	invalidMode := Device{SchemaVersion: SchemaVersion, ID: "device", ProviderID: "provider", RuntimeMode: "invalid"}
	if err := invalidMode.ValidateStructure(); !errors.Is(err, ErrInvalidModel) || !strings.Contains(err.Error(), "runtime mode") {
		t.Fatalf("runtime mode error = %v", err)
	}
	invalidTransport := Device{SchemaVersion: SchemaVersion, ID: "device", ProviderID: "provider", StateTransport: "invalid"}
	if err := invalidTransport.ValidateStructure(); !errors.Is(err, ErrInvalidModel) || !strings.Contains(err.Error(), "state transport") {
		t.Fatalf("state transport error = %v", err)
	}
}

func TestCloneDoesNotShareMutableState(t *testing.T) {
	original := Device{Endpoints: []Endpoint{{
		ID: "main", Capabilities: []Capability{{ID: "switch", Properties: []Property{{
			Definition: PropertyDefinition{ID: "power", Enum: []string{"on"}}, Value: BoolValue(true),
		}}}},
	}}}
	clone := original.Clone()
	clone.SetProperty("main", "switch", "power", BoolValue(false))
	clone.Endpoints[0].Capabilities[0].Properties[0].Definition.Enum[0] = "changed"
	property, _ := original.Property("main", "switch", "power")
	if property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatal("clone changed original value")
	}
	if property.Definition.Enum[0] != "on" {
		t.Fatal("clone shared enum storage")
	}
}

func TestValidateModelContract(t *testing.T) {
	minimum, maximum := 0.0, 100.0
	valid := Device{SchemaVersion: SchemaVersion, ID: "room-sensor.1", ProviderID: "virtual-main", Endpoints: []Endpoint{{ID: "main", Capabilities: []Capability{{ID: "mode", Properties: []Property{{Definition: PropertyDefinition{ID: "operating-mode", Type: ValueTypeEnum, Enum: []string{"auto", "manual"}}, Value: EnumValue("auto")}, {Definition: PropertyDefinition{ID: "humidity", Type: ValueTypeNumber, Min: &minimum, Max: &maximum}, Value: NumberValue(50)}}}}}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid model rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Device){
		"schema":             func(item *Device) { item.SchemaVersion = 99 },
		"id":                 func(item *Device) { item.ID = "Invalid ID" },
		"duplicate endpoint": func(item *Device) { item.Endpoints = append(item.Endpoints, item.Endpoints[0]) },
		"wrong value type":   func(item *Device) { item.Endpoints[0].Capabilities[0].Properties[0].Value = StringValue("auto") },
		"invalid enum":       func(item *Device) { item.Endpoints[0].Capabilities[0].Properties[0].Value = EnumValue("invalid") },
		"out of range":       func(item *Device) { item.Endpoints[0].Capabilities[0].Properties[1].Value = NumberValue(101) },
		"disabled online":    func(item *Device) { item.Disabled, item.Availability, item.Online = true, AvailabilityOnline, true },
	} {
		t.Run(name, func(t *testing.T) {
			item := valid.Clone()
			mutate(&item)
			if err := item.Validate(); !errors.Is(err, ErrInvalidModel) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestValidateIntegerPropertyContract(t *testing.T) {
	minimum, maximum, step := 0.0, 100.0, 1.0
	property := Property{Definition: PropertyDefinition{ID: "level", Type: ValueTypeInt, Min: &minimum, Max: &maximum, Step: &step}, Value: IntValue(42)}
	if err := property.Validate(); err != nil {
		t.Fatalf("valid integer rejected: %v", err)
	}
	property.Value = IntValue(101)
	if err := property.Validate(); err == nil {
		t.Fatal("out-of-range integer accepted")
	}
	fractionalStep := 0.5
	property.Value, property.Definition.Step = IntValue(42), &fractionalStep
	if err := property.Validate(); err == nil {
		t.Fatal("fractional integer step accepted")
	}
}
