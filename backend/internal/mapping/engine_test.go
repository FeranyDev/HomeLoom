package mapping

import (
	"math"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

func numberPointer(value float64) *float64 { return &value }

func TestPreviewTable(t *testing.T) {
	tests := []struct {
		name       string
		profile    Profile
		direction  Direction
		value      *device.PropertyValue
		wantNumber float64
		wantBool   *bool
		wantString string
	}{
		{name: "scale forward", profile: profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformScale, Factor: numberPointer(1.8), Offset: numberPointer(32)}), direction: DirectionForward, value: valuePointer(device.NumberValue(20)), wantNumber: 68},
		{name: "scale reverse", profile: profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformScale, Factor: numberPointer(1.8), Offset: numberPointer(32)}), direction: DirectionReverse, value: valuePointer(device.NumberValue(68)), wantNumber: 20},
		{name: "unit", profile: profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformUnit, FromUnit: "celsius", ToUnit: "kelvin"}), direction: DirectionForward, value: valuePointer(device.NumberValue(20)), wantNumber: 293.15},
		{name: "enum reverse", profile: profile(device.ValueTypeEnum, device.ValueTypeEnum, Transform{Type: TransformEnum, Values: map[string]string{"off": "inactive", "on": "active"}}), direction: DirectionReverse, value: valuePointer(device.EnumValue("active")), wantString: "on"},
	}
	falseValue := false
	tests = append(tests, struct {
		name       string
		profile    Profile
		direction  Direction
		value      *device.PropertyValue
		wantNumber float64
		wantBool   *bool
		wantString string
	}{name: "invert", profile: profile(device.ValueTypeBool, device.ValueTypeBool, Transform{Type: TransformInvert}), direction: DirectionForward, value: valuePointer(device.BoolValue(true)), wantBool: &falseValue})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Preview(PreviewRequest{Profile: test.profile, Direction: test.direction, Value: test.value})
			if err != nil {
				t.Fatal(err)
			}
			if test.wantBool != nil && (result.Value.Bool == nil || *result.Value.Bool != *test.wantBool) {
				t.Fatalf("value = %#v", result.Value)
			}
			if test.wantString != "" && (result.Value.String == nil || *result.Value.String != test.wantString) {
				t.Fatalf("value = %#v", result.Value)
			}
			if test.wantBool == nil && test.wantString == "" && (result.Value.Number == nil || math.Abs(*result.Value.Number-test.wantNumber) > 0.000001) {
				t.Fatalf("value = %#v", result.Value)
			}
			if len(result.Steps) != 1 {
				t.Fatalf("steps = %#v", result.Steps)
			}
		})
	}
}

func TestPreviewDefaultClampAndErrors(t *testing.T) {
	minimum, maximum := 0.0, 100.0
	defaultValue := device.NumberValue(15)
	item := profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformClamp, Min: &minimum, Max: &maximum})
	item.Default = &defaultValue
	result, err := Preview(PreviewRequest{Profile: item, Direction: DirectionForward})
	if err != nil || result.Value.Number == nil || *result.Value.Number != 15 || len(result.Steps) != 2 {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if _, err := Preview(PreviewRequest{Profile: item, Direction: DirectionReverse, Value: valuePointer(device.NumberValue(10))}); err == nil {
		t.Fatal("reverse clamp accepted")
	}

	invalid := profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformScale, Factor: numberPointer(0)})
	if err := Validate(invalid); err == nil {
		t.Fatal("zero factor accepted")
	}
	duplicate := profile(device.ValueTypeEnum, device.ValueTypeEnum, Transform{Type: TransformEnum, Values: map[string]string{"a": "same", "b": "same"}})
	if err := Validate(duplicate); err == nil {
		t.Fatal("ambiguous enum reverse accepted")
	}
}

func profile(input, output device.ValueType, transforms ...Transform) Profile {
	return Profile{SchemaVersion: SchemaVersion, ID: "test-profile", Version: 1, Kind: KindCapability, InputType: input, OutputType: output, Transforms: transforms}
}

func valuePointer(value device.PropertyValue) *device.PropertyValue { return &value }
