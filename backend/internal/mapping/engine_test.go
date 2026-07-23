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
		{name: "reciprocal forward", profile: profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformReciprocal}), direction: DirectionForward, value: valuePointer(device.NumberValue(4)), wantNumber: 0.25},
		{name: "reciprocal reverse", profile: profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformReciprocal}), direction: DirectionReverse, value: valuePointer(device.NumberValue(0.25)), wantNumber: 4},
		{name: "unit", profile: profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformUnit, FromUnit: "celsius", ToUnit: "kelvin"}), direction: DirectionForward, value: valuePointer(device.NumberValue(20)), wantNumber: 293.15},
		{name: "kelvin to mired", profile: profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformUnit, FromUnit: "kelvin", ToUnit: "mired"}), direction: DirectionForward, value: valuePointer(device.NumberValue(4000)), wantNumber: 250},
		{name: "mired to kelvin", profile: profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformUnit, FromUnit: "kelvin", ToUnit: "mired"}), direction: DirectionReverse, value: valuePointer(device.NumberValue(250)), wantNumber: 4000},
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
	reciprocal := profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformReciprocal})
	if _, err := Preview(PreviewRequest{Profile: reciprocal, Direction: DirectionForward, Value: valuePointer(device.NumberValue(0))}); err == nil {
		t.Fatal("zero reciprocal accepted")
	}
	kelvinMired := profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformUnit, FromUnit: "kelvin", ToUnit: "mired"})
	if _, err := Preview(PreviewRequest{Profile: kelvinMired, Direction: DirectionForward, Value: valuePointer(device.NumberValue(0))}); err == nil {
		t.Fatal("zero kelvin accepted")
	}

	roundedColorTemperature := profile(device.ValueTypeInt, device.ValueTypeInt,
		Transform{Type: TransformUnit, FromUnit: "kelvin", ToUnit: "mired"},
		Transform{Type: TransformRound, Mode: "nearest"},
	)
	forward, err := Preview(PreviewRequest{Profile: roundedColorTemperature, Direction: DirectionForward, Value: valuePointer(device.IntValue(4000))})
	if err != nil || forward.Value.Int == nil || *forward.Value.Int != 250 {
		t.Fatalf("kelvin/mired forward = %#v, error = %v", forward.Value, err)
	}
	reverse, err := Preview(PreviewRequest{Profile: roundedColorTemperature, Direction: DirectionReverse, Value: valuePointer(device.IntValue(333))})
	if err != nil || reverse.Value.Int == nil || *reverse.Value.Int != 3003 {
		t.Fatalf("kelvin/mired reverse = %#v, error = %v", reverse.Value, err)
	}
	overflowingColorTemperature := profile(device.ValueTypeNumber, device.ValueTypeInt,
		Transform{Type: TransformUnit, FromUnit: "kelvin", ToUnit: "mired"},
		Transform{Type: TransformRound, Mode: "nearest"},
	)
	if _, err := Preview(PreviewRequest{Profile: overflowingColorTemperature, Direction: DirectionForward, Value: valuePointer(device.NumberValue(1e-20))}); err == nil {
		t.Fatal("Kelvin/mired integer overflow accepted")
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

func TestExpandedTransformsForwardAndReverse(t *testing.T) {
	bands := []RangeBand{
		{Max: numberPointer(18), Value: "cold", Reverse: 10},
		{Max: numberPointer(28), Value: "comfortable", Reverse: 24},
		{Value: "hot", Reverse: 32},
	}
	tests := []struct {
		name      string
		profile   Profile
		direction Direction
		value     device.PropertyValue
		want      device.PropertyValue
	}{
		{"range enum forward", profile(device.ValueTypeNumber, device.ValueTypeEnum, Transform{Type: TransformRangeEnum, Bands: bands}), DirectionForward, device.NumberValue(25), device.EnumValue("comfortable")},
		{"range enum reverse", profile(device.ValueTypeNumber, device.ValueTypeEnum, Transform{Type: TransformRangeEnum, Bands: bands}), DirectionReverse, device.EnumValue("hot"), device.NumberValue(32)},
		{"enum number forward", profile(device.ValueTypeEnum, device.ValueTypeNumber, Transform{Type: TransformEnumNumber, Bands: bands}), DirectionForward, device.EnumValue("hot"), device.NumberValue(32)},
		{"enum number reverse", profile(device.ValueTypeEnum, device.ValueTypeNumber, Transform{Type: TransformEnumNumber, Bands: bands}), DirectionReverse, device.NumberValue(25), device.EnumValue("comfortable")},
		{"threshold forward", profile(device.ValueTypeNumber, device.ValueTypeBool, Transform{Type: TransformThreshold, Threshold: numberPointer(20), Operator: "gte", TrueNumber: numberPointer(25), FalseNumber: numberPointer(10)}), DirectionForward, device.NumberValue(22), device.BoolValue(true)},
		{"threshold reverse", profile(device.ValueTypeNumber, device.ValueTypeBool, Transform{Type: TransformThreshold, Threshold: numberPointer(20), Operator: "gte", TrueNumber: numberPointer(25), FalseNumber: numberPointer(10)}), DirectionReverse, device.BoolValue(false), device.NumberValue(10)},
		{"bool number forward", profile(device.ValueTypeBool, device.ValueTypeNumber, Transform{Type: TransformBoolNumber, Threshold: numberPointer(20), Operator: "gte", TrueNumber: numberPointer(25), FalseNumber: numberPointer(10)}), DirectionForward, device.BoolValue(false), device.NumberValue(10)},
		{"bool number reverse", profile(device.ValueTypeBool, device.ValueTypeNumber, Transform{Type: TransformBoolNumber, Threshold: numberPointer(20), Operator: "gte", TrueNumber: numberPointer(25), FalseNumber: numberPointer(10)}), DirectionReverse, device.NumberValue(22), device.BoolValue(true)},
		{"bool enum forward", profile(device.ValueTypeBool, device.ValueTypeEnum, Transform{Type: TransformBoolEnum, TrueValue: "active", FalseValue: "inactive"}), DirectionForward, device.BoolValue(true), device.EnumValue("active")},
		{"bool enum reverse", profile(device.ValueTypeBool, device.ValueTypeEnum, Transform{Type: TransformBoolEnum, TrueValue: "active", FalseValue: "inactive"}), DirectionReverse, device.EnumValue("inactive"), device.BoolValue(false)},
		{"enum bool forward", profile(device.ValueTypeEnum, device.ValueTypeBool, Transform{Type: TransformEnumBool, TrueValue: "open", FalseValue: "closed"}), DirectionForward, device.EnumValue("closed"), device.BoolValue(false)},
		{"enum bool reverse", profile(device.ValueTypeString, device.ValueTypeBool, Transform{Type: TransformEnumBool, TrueValue: "yes", FalseValue: "no"}), DirectionReverse, device.BoolValue(true), device.StringValue("yes")},
		{"map range forward", profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformMapRange, InputMin: numberPointer(0), InputMax: numberPointer(100), OutputMin: numberPointer(0), OutputMax: numberPointer(1)}), DirectionForward, device.NumberValue(25), device.NumberValue(0.25)},
		{"map range reverse", profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformMapRange, InputMin: numberPointer(0), InputMax: numberPointer(100), OutputMin: numberPointer(0), OutputMax: numberPointer(1)}), DirectionReverse, device.NumberValue(0.75), device.NumberValue(75)},
		{"round forward", profile(device.ValueTypeNumber, device.ValueTypeInt, Transform{Type: TransformRound, Mode: "nearest"}), DirectionForward, device.NumberValue(2.6), device.IntValue(3)},
		{"round reverse", profile(device.ValueTypeNumber, device.ValueTypeInt, Transform{Type: TransformRound, Mode: "nearest"}), DirectionReverse, device.IntValue(4), device.NumberValue(4)},
		{"int number forward", profile(device.ValueTypeInt, device.ValueTypeNumber, Transform{Type: TransformIntNumber}), DirectionForward, device.IntValue(42), device.NumberValue(42)},
		{"int number reverse", profile(device.ValueTypeInt, device.ValueTypeNumber, Transform{Type: TransformIntNumber}), DirectionReverse, device.NumberValue(42), device.IntValue(42)},
		{"parse number forward", profile(device.ValueTypeString, device.ValueTypeNumber, Transform{Type: TransformParseNumber}), DirectionForward, device.StringValue("23.5"), device.NumberValue(23.5)},
		{"parse number reverse", profile(device.ValueTypeString, device.ValueTypeNumber, Transform{Type: TransformParseNumber}), DirectionReverse, device.NumberValue(23.5), device.StringValue("23.5")},
		{"number string forward", profile(device.ValueTypeNumber, device.ValueTypeString, Transform{Type: TransformNumberString}), DirectionForward, device.NumberValue(42.25), device.StringValue("42.25")},
		{"number string reverse", profile(device.ValueTypeInt, device.ValueTypeString, Transform{Type: TransformNumberString}), DirectionReverse, device.StringValue("42"), device.IntValue(42)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Preview(PreviewRequest{Profile: test.profile, Direction: test.direction, Value: &test.value})
			if err != nil {
				t.Fatal(err)
			}
			if !propertyValuesEqual(result.Value, test.want) {
				t.Fatalf("value = %#v, want %#v", result.Value, test.want)
			}
		})
	}
}

func TestExpandedTransformValidation(t *testing.T) {
	tests := []Profile{
		profile(device.ValueTypeNumber, device.ValueTypeEnum, Transform{Type: TransformRangeEnum, Bands: []RangeBand{{Max: numberPointer(10), Value: "low", Reverse: 5}}}),
		profile(device.ValueTypeEnum, device.ValueTypeNumber, Transform{Type: TransformEnumNumber, Bands: []RangeBand{{Max: numberPointer(10), Value: "low", Reverse: 5}}}),
		profile(device.ValueTypeNumber, device.ValueTypeBool, Transform{Type: TransformThreshold, Threshold: numberPointer(10), Operator: "gte", TrueNumber: numberPointer(5), FalseNumber: numberPointer(0)}),
		profile(device.ValueTypeBool, device.ValueTypeNumber, Transform{Type: TransformBoolNumber, Threshold: numberPointer(10), Operator: "gte", TrueNumber: numberPointer(5), FalseNumber: numberPointer(0)}),
		profile(device.ValueTypeBool, device.ValueTypeEnum, Transform{Type: TransformBoolEnum, TrueValue: "same", FalseValue: "same"}),
		profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformMapRange, InputMin: numberPointer(0), InputMax: numberPointer(0), OutputMin: numberPointer(0), OutputMax: numberPointer(1)}),
		profile(device.ValueTypeString, device.ValueTypeInt, Transform{Type: TransformRound, Mode: "nearest"}),
		profile(device.ValueTypeString, device.ValueTypeNumber, Transform{Type: TransformReciprocal}),
		profile(device.ValueTypeNumber, device.ValueTypeNumber, Transform{Type: TransformIntNumber}),
	}
	for index, item := range tests {
		if err := Validate(item); err == nil {
			t.Errorf("invalid expanded transform %d was accepted", index)
		}
	}
}

func TestIntNumberRejectsLossyConversions(t *testing.T) {
	item := profile(device.ValueTypeInt, device.ValueTypeNumber, Transform{Type: TransformIntNumber})
	if _, err := Preview(PreviewRequest{Profile: item, Direction: DirectionForward, Value: valuePointer(device.IntValue(maxSafeInteger + 1))}); err == nil {
		t.Fatal("unsafe int was converted to number")
	}
	if _, err := Preview(PreviewRequest{Profile: item, Direction: DirectionReverse, Value: valuePointer(device.NumberValue(42.5))}); err == nil {
		t.Fatal("fractional number was converted to int")
	}
	if _, err := Preview(PreviewRequest{Profile: item, Direction: DirectionReverse, Value: valuePointer(device.NumberValue(float64(maxSafeInteger + 1)))}); err == nil {
		t.Fatal("unsafe number was converted to int")
	}
}

func propertyValuesEqual(left, right device.PropertyValue) bool {
	if left.Type != right.Type {
		return false
	}
	switch left.Type {
	case device.ValueTypeBool:
		return left.Bool != nil && right.Bool != nil && *left.Bool == *right.Bool
	case device.ValueTypeInt:
		return left.Int != nil && right.Int != nil && *left.Int == *right.Int
	case device.ValueTypeNumber:
		return left.Number != nil && right.Number != nil && math.Abs(*left.Number-*right.Number) < 0.000001
	default:
		return left.String != nil && right.String != nil && *left.String == *right.String
	}
}

func profile(input, output device.ValueType, transforms ...Transform) Profile {
	return Profile{SchemaVersion: SchemaVersion, ID: "test-profile", Version: 1, Kind: KindCapability, InputType: input, OutputType: output, Transforms: transforms}
}

func valuePointer(value device.PropertyValue) *device.PropertyValue { return &value }
