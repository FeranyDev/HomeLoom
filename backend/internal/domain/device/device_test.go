package device

import (
	"errors"
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

func TestTypedValues(t *testing.T) {
	boolean := BoolValue(true)
	number := NumberValue(23.5)
	text := StringValue("hello")
	enumerated := EnumValue("auto")
	if boolean.Type != ValueTypeBool || boolean.Bool == nil || !*boolean.Bool {
		t.Fatalf("BoolValue() = %#v", boolean)
	}
	if number.Type != ValueTypeNumber || number.Number == nil || *number.Number != 23.5 {
		t.Fatalf("NumberValue() = %#v", number)
	}
	if text.String == nil || *text.String != "hello" || enumerated.Type != ValueTypeEnum || enumerated.String == nil || *enumerated.String != "auto" {
		t.Fatalf("string values = %#v, %#v", text, enumerated)
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
