package device

import "testing"

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
	if boolean.Type != ValueTypeBool || boolean.Bool == nil || !*boolean.Bool {
		t.Fatalf("BoolValue() = %#v", boolean)
	}
	if number.Type != ValueTypeNumber || number.Number == nil || *number.Number != 23.5 {
		t.Fatalf("NumberValue() = %#v", number)
	}
}
