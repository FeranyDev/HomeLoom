package mapping

import (
	"fmt"
	"math"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

func Preview(request PreviewRequest) (PreviewResult, error) {
	if err := Validate(request.Profile); err != nil {
		return PreviewResult{}, err
	}
	if request.Direction == "" {
		request.Direction = DirectionForward
	}
	if request.Direction != DirectionForward && request.Direction != DirectionReverse {
		return PreviewResult{}, &ValidationError{Fields: map[string]string{"direction": "must be forward or reverse"}}
	}
	expectedType := request.Profile.InputType
	if request.Direction == DirectionReverse {
		expectedType = request.Profile.OutputType
	}
	var current device.PropertyValue
	steps := make([]Step, 0, len(request.Profile.Transforms)+1)
	if request.Value == nil {
		if request.Direction == DirectionReverse || request.Profile.Default == nil {
			return PreviewResult{}, &ValidationError{Fields: map[string]string{"value": "is required and no forward default is configured"}}
		}
		current = cloneValue(*request.Profile.Default)
		steps = append(steps, Step{Index: -1, Transform: "default", Output: cloneValue(current)})
	} else {
		current = cloneValue(*request.Value)
	}
	if err := validateValue(current, expectedType); err != nil {
		return PreviewResult{}, &ValidationError{Fields: map[string]string{"value": err.Error()}}
	}
	if request.Direction == DirectionForward {
		for index, transform := range request.Profile.Transforms {
			input := cloneValue(current)
			output, err := applyForward(transform, current)
			if err != nil {
				return PreviewResult{}, transformError(index, err)
			}
			current = output
			steps = append(steps, Step{Index: index, Transform: string(transform.Type), Input: &input, Output: cloneValue(output)})
		}
	} else {
		for index := len(request.Profile.Transforms) - 1; index >= 0; index-- {
			input := cloneValue(current)
			output, err := applyReverse(request.Profile.Transforms[index], current)
			if err != nil {
				return PreviewResult{}, transformError(index, err)
			}
			current = output
			steps = append(steps, Step{Index: index, Transform: string(request.Profile.Transforms[index].Type), Input: &input, Output: cloneValue(output)})
		}
	}
	finalType := request.Profile.OutputType
	if request.Direction == DirectionReverse {
		finalType = request.Profile.InputType
	}
	if finalType == device.ValueTypeInt && current.Type == device.ValueTypeNumber && current.Number != nil && math.Trunc(*current.Number) == *current.Number {
		current = device.IntValue(int64(*current.Number))
	}
	if err := validateValue(current, finalType); err != nil {
		return PreviewResult{}, &ValidationError{Fields: map[string]string{"value": "pipeline output: " + err.Error()}}
	}
	return PreviewResult{ProfileID: request.Profile.ID, ProfileVersion: request.Profile.Version, Direction: request.Direction, Value: current, Steps: steps}, nil
}

func applyForward(transform Transform, value device.PropertyValue) (device.PropertyValue, error) {
	switch transform.Type {
	case TransformInvert:
		return device.BoolValue(!*value.Bool), nil
	case TransformScale:
		number := numericValue(value)
		return finiteNumber(number*float64Value(transform.Factor, 1) + float64Value(transform.Offset, 0))
	case TransformClamp:
		number := numericValue(value)
		if transform.Min != nil {
			number = math.Max(number, *transform.Min)
		}
		if transform.Max != nil {
			number = math.Min(number, *transform.Max)
		}
		return finiteNumber(number)
	case TransformEnum:
		mapped, ok := transform.Values[*value.String]
		if !ok {
			return device.PropertyValue{}, fmt.Errorf("enum value %q has no mapping", *value.String)
		}
		return stringValueForType(mapped, value.Type), nil
	case TransformUnit:
		factor, offset, _ := unitFormula(transform.FromUnit, transform.ToUnit)
		return finiteNumber(numericValue(value)*factor + offset)
	default:
		return device.PropertyValue{}, fmt.Errorf("unsupported transform %q", transform.Type)
	}
}

func applyReverse(transform Transform, value device.PropertyValue) (device.PropertyValue, error) {
	switch transform.Type {
	case TransformInvert:
		return device.BoolValue(!*value.Bool), nil
	case TransformScale:
		return finiteNumber((numericValue(value) - float64Value(transform.Offset, 0)) / float64Value(transform.Factor, 1))
	case TransformClamp:
		return device.PropertyValue{}, fmt.Errorf("clamp is not reversible")
	case TransformEnum:
		for source, target := range transform.Values {
			if target == *value.String {
				return stringValueForType(source, value.Type), nil
			}
		}
		return device.PropertyValue{}, fmt.Errorf("enum value %q has no reverse mapping", *value.String)
	case TransformUnit:
		factor, offset, _ := unitFormula(transform.FromUnit, transform.ToUnit)
		return finiteNumber((numericValue(value) - offset) / factor)
	default:
		return device.PropertyValue{}, fmt.Errorf("unsupported transform %q", transform.Type)
	}
}

func validateValue(value device.PropertyValue, expected device.ValueType) error {
	if value.Type != expected {
		return fmt.Errorf("type %q does not match expected %q", value.Type, expected)
	}
	payloads := 0
	if value.Bool != nil {
		payloads++
	}
	if value.Int != nil {
		payloads++
	}
	if value.Number != nil {
		payloads++
	}
	if value.String != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("must contain exactly one typed payload")
	}
	switch value.Type {
	case device.ValueTypeBool:
		if value.Bool == nil {
			return fmt.Errorf("bool payload is missing")
		}
	case device.ValueTypeInt:
		if value.Int == nil {
			return fmt.Errorf("int payload is missing")
		}
	case device.ValueTypeNumber:
		if value.Number == nil || math.IsNaN(*value.Number) || math.IsInf(*value.Number, 0) {
			return fmt.Errorf("number payload must be finite")
		}
	case device.ValueTypeString, device.ValueTypeEnum:
		if value.String == nil {
			return fmt.Errorf("string payload is missing")
		}
	default:
		return fmt.Errorf("unsupported value type %q", value.Type)
	}
	return nil
}

func numericValue(value device.PropertyValue) float64 {
	if value.Int != nil {
		return float64(*value.Int)
	}
	return *value.Number
}

func finiteNumber(value float64) (device.PropertyValue, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return device.PropertyValue{}, fmt.Errorf("numeric result is not finite")
	}
	return device.NumberValue(value), nil
}

func stringValueForType(value string, kind device.ValueType) device.PropertyValue {
	if kind == device.ValueTypeEnum {
		return device.EnumValue(value)
	}
	return device.StringValue(value)
}

func cloneValue(value device.PropertyValue) device.PropertyValue {
	switch value.Type {
	case device.ValueTypeBool:
		if value.Bool != nil {
			return device.BoolValue(*value.Bool)
		}
	case device.ValueTypeInt:
		if value.Int != nil {
			return device.IntValue(*value.Int)
		}
	case device.ValueTypeNumber:
		if value.Number != nil {
			return device.NumberValue(*value.Number)
		}
	case device.ValueTypeString:
		if value.String != nil {
			return device.StringValue(*value.String)
		}
	case device.ValueTypeEnum:
		if value.String != nil {
			return device.EnumValue(*value.String)
		}
	}
	return value
}

func transformError(index int, err error) error {
	return &ValidationError{Fields: map[string]string{fmt.Sprintf("profile.transforms.%d", index): err.Error()}}
}

func unitFormula(from, to string) (factor, offset float64, ok bool) {
	if from == to && from != "" {
		return 1, 0, true
	}
	switch from + ":" + to {
	case "celsius:fahrenheit":
		return 1.8, 32, true
	case "fahrenheit:celsius":
		return 5.0 / 9.0, -32 * 5.0 / 9.0, true
	case "celsius:kelvin":
		return 1, 273.15, true
	case "kelvin:celsius":
		return 1, -273.15, true
	case "ratio:percent":
		return 100, 0, true
	case "percent:ratio":
		return 0.01, 0, true
	default:
		return 0, 0, false
	}
}
