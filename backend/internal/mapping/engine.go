package mapping

import (
	"fmt"
	"math"
	"strconv"
	"strings"

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
		types := make([]device.ValueType, len(request.Profile.Transforms)+1)
		types[0] = request.Profile.InputType
		for index, transform := range request.Profile.Transforms {
			types[index+1] = transformOutputType(types[index], transform)
		}
		for index := len(request.Profile.Transforms) - 1; index >= 0; index-- {
			input := cloneValue(current)
			output, err := applyReverse(request.Profile.Transforms[index], current, types[index])
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
	if finalType == device.ValueTypeInt && current.Type == device.ValueTypeNumber && current.Number != nil {
		number := *current.Number
		if math.Trunc(number) == number {
			if number < math.MinInt64 || number > math.MaxInt64 {
				return PreviewResult{}, &ValidationError{Fields: map[string]string{"value": "pipeline output is outside int64 range"}}
			}
			current = device.IntValue(int64(number))
		} else if request.Direction == DirectionReverse {
			for _, transform := range request.Profile.Transforms {
				if transform.Type != TransformRound {
					continue
				}
				switch transform.Mode {
				case "floor":
					number = math.Floor(number)
				case "ceil":
					number = math.Ceil(number)
				default:
					number = math.Round(number)
				}
				if number < math.MinInt64 || number > math.MaxInt64 {
					return PreviewResult{}, &ValidationError{Fields: map[string]string{"value": "pipeline output is outside int64 range"}}
				}
				current = device.IntValue(int64(number))
				break
			}
		}
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
	case TransformReciprocal:
		return reciprocalValue(value)
	case TransformIntNumber:
		if *value.Int < -maxSafeInteger || *value.Int > maxSafeInteger {
			return device.PropertyValue{}, fmt.Errorf("int %d cannot be represented exactly as number", *value.Int)
		}
		return finiteNumber(float64(*value.Int))
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
		return convertUnitValue(transform.FromUnit, transform.ToUnit, numericValue(value))
	case TransformRangeEnum:
		number := numericValue(value)
		for _, band := range transform.Bands {
			if band.Max == nil || number <= *band.Max {
				return device.EnumValue(band.Value), nil
			}
		}
		return device.PropertyValue{}, fmt.Errorf("numeric value %v has no range band", number)
	case TransformEnumNumber:
		for _, band := range transform.Bands {
			if band.Value == *value.String {
				return finiteNumber(band.Reverse)
			}
		}
		return device.PropertyValue{}, fmt.Errorf("enum value %q has no numeric mapping", *value.String)
	case TransformThreshold:
		return device.BoolValue(thresholdMatches(numericValue(value), *transform.Threshold, transform.Operator)), nil
	case TransformBoolNumber:
		if *value.Bool {
			return finiteNumber(*transform.TrueNumber)
		}
		return finiteNumber(*transform.FalseNumber)
	case TransformBoolEnum:
		if *value.Bool {
			return device.EnumValue(transform.TrueValue), nil
		}
		return device.EnumValue(transform.FalseValue), nil
	case TransformEnumBool:
		switch *value.String {
		case transform.TrueValue:
			return device.BoolValue(true), nil
		case transform.FalseValue:
			return device.BoolValue(false), nil
		default:
			return device.PropertyValue{}, fmt.Errorf("enum value %q is neither trueValue nor falseValue", *value.String)
		}
	case TransformMapRange:
		mapped := *transform.OutputMin + (numericValue(value)-*transform.InputMin)*(*transform.OutputMax-*transform.OutputMin)/(*transform.InputMax-*transform.InputMin)
		return finiteNumber(mapped)
	case TransformRound:
		number := numericValue(value)
		switch transform.Mode {
		case "floor":
			number = math.Floor(number)
		case "ceil":
			number = math.Ceil(number)
		default:
			number = math.Round(number)
		}
		if number < math.MinInt64 || number > math.MaxInt64 {
			return device.PropertyValue{}, fmt.Errorf("rounded result is outside int64 range")
		}
		return device.IntValue(int64(number)), nil
	case TransformParseNumber:
		number, err := strconv.ParseFloat(strings.TrimSpace(*value.String), 64)
		if err != nil {
			return device.PropertyValue{}, fmt.Errorf("parse number %q: %w", *value.String, err)
		}
		return finiteNumber(number)
	case TransformNumberString:
		return device.StringValue(strconv.FormatFloat(numericValue(value), 'g', -1, 64)), nil
	default:
		return device.PropertyValue{}, fmt.Errorf("unsupported transform %q", transform.Type)
	}
}

func applyReverse(transform Transform, value device.PropertyValue, expected device.ValueType) (device.PropertyValue, error) {
	switch transform.Type {
	case TransformInvert:
		return device.BoolValue(!*value.Bool), nil
	case TransformReciprocal:
		return reciprocalValue(value)
	case TransformIntNumber:
		number := numericValue(value)
		if math.Trunc(number) != number || number < -float64(maxSafeInteger) || number > float64(maxSafeInteger) {
			return device.PropertyValue{}, fmt.Errorf("number %v is not an exactly representable int", number)
		}
		return device.IntValue(int64(number)), nil
	case TransformScale:
		return finiteNumber((numericValue(value) - float64Value(transform.Offset, 0)) / float64Value(transform.Factor, 1))
	case TransformClamp:
		return device.PropertyValue{}, fmt.Errorf("clamp is not reversible")
	case TransformEnum:
		if source, ok := transform.ReverseValues[*value.String]; ok {
			return stringValueForType(source, expected), nil
		}
		var found string
		var matches int
		for source, target := range transform.Values {
			if target == *value.String {
				found = source
				matches++
			}
		}
		if matches == 1 {
			return stringValueForType(found, expected), nil
		}
		if matches > 1 {
			return device.PropertyValue{}, fmt.Errorf("enum value %q has ambiguous reverse mapping", *value.String)
		}
		return device.PropertyValue{}, fmt.Errorf("enum value %q has no reverse mapping", *value.String)
	case TransformUnit:
		return convertUnitValue(transform.ToUnit, transform.FromUnit, numericValue(value))
	case TransformRangeEnum:
		for _, band := range transform.Bands {
			if band.Value == *value.String {
				return finiteNumber(band.Reverse)
			}
		}
		return device.PropertyValue{}, fmt.Errorf("enum value %q has no reverse range", *value.String)
	case TransformEnumNumber:
		number := numericValue(value)
		for _, band := range transform.Bands {
			if band.Max == nil || number <= *band.Max {
				return stringValueForType(band.Value, expected), nil
			}
		}
		return device.PropertyValue{}, fmt.Errorf("numeric value %v has no reverse range", number)
	case TransformThreshold:
		if *value.Bool {
			return finiteNumber(*transform.TrueNumber)
		}
		return finiteNumber(*transform.FalseNumber)
	case TransformBoolNumber:
		return device.BoolValue(thresholdMatches(numericValue(value), *transform.Threshold, transform.Operator)), nil
	case TransformBoolEnum:
		switch *value.String {
		case transform.TrueValue:
			return device.BoolValue(true), nil
		case transform.FalseValue:
			return device.BoolValue(false), nil
		default:
			return device.PropertyValue{}, fmt.Errorf("enum value %q is neither trueValue nor falseValue", *value.String)
		}
	case TransformEnumBool:
		if *value.Bool {
			return stringValueForType(transform.TrueValue, expected), nil
		}
		return stringValueForType(transform.FalseValue, expected), nil
	case TransformMapRange:
		mapped := *transform.InputMin + (numericValue(value)-*transform.OutputMin)*(*transform.InputMax-*transform.InputMin)/(*transform.OutputMax-*transform.OutputMin)
		return finiteNumber(mapped)
	case TransformRound:
		return finiteNumber(numericValue(value))
	case TransformParseNumber:
		return device.StringValue(strconv.FormatFloat(numericValue(value), 'g', -1, 64)), nil
	case TransformNumberString:
		number, err := strconv.ParseFloat(strings.TrimSpace(*value.String), 64)
		if err != nil {
			return device.PropertyValue{}, fmt.Errorf("parse number %q: %w", *value.String, err)
		}
		if expected == device.ValueTypeInt {
			if math.Trunc(number) != number || number < math.MinInt64 || number > math.MaxInt64 {
				return device.PropertyValue{}, fmt.Errorf("number %q is not a valid int64", *value.String)
			}
			return device.IntValue(int64(number)), nil
		}
		return finiteNumber(number)
	default:
		return device.PropertyValue{}, fmt.Errorf("unsupported transform %q", transform.Type)
	}
}

func reciprocalValue(value device.PropertyValue) (device.PropertyValue, error) {
	number := numericValue(value)
	if number == 0 {
		return device.PropertyValue{}, fmt.Errorf("reciprocal is undefined for zero")
	}
	return finiteNumber(1 / number)
}

const maxSafeInteger int64 = 1<<53 - 1

func validateValue(value device.PropertyValue, expected device.ValueType) error {
	if value.Type != expected {
		return fmt.Errorf("type %q does not match expected %q", value.Type, expected)
	}
	if !value.HasSinglePayload() {
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

func unitConversionSupported(from, to string) bool {
	if _, _, ok := unitFormula(from, to); ok {
		return true
	}
	return nonlinearUnitConversion(from, to)
}

func nonlinearUnitConversion(from, to string) bool {
	return (from == "kelvin" && to == "mired") || (from == "mired" && to == "kelvin")
}

func convertUnitValue(from, to string, value float64) (device.PropertyValue, error) {
	if nonlinearUnitConversion(from, to) {
		if value <= 0 {
			return device.PropertyValue{}, fmt.Errorf("%s value must be greater than zero", from)
		}
		return finiteNumber(1_000_000 / value)
	}
	factor, offset, ok := unitFormula(from, to)
	if !ok {
		return device.PropertyValue{}, fmt.Errorf("unsupported unit conversion %q to %q", from, to)
	}
	return finiteNumber(value*factor + offset)
}
