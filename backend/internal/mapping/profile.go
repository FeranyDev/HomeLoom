package mapping

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

const SchemaVersion = 1

type ProfileKind string
type TransformType string
type Direction string

const (
	KindProvider   ProfileKind = "provider"
	KindCapability ProfileKind = "capability"
	KindTarget     ProfileKind = "target"

	TransformInvert       TransformType = "invert"
	TransformReciprocal   TransformType = "reciprocal"
	TransformIntNumber    TransformType = "int-number"
	TransformScale        TransformType = "scale"
	TransformClamp        TransformType = "clamp"
	TransformEnum         TransformType = "enum"
	TransformUnit         TransformType = "unit"
	TransformRangeEnum    TransformType = "range-enum"
	TransformEnumNumber   TransformType = "enum-number"
	TransformThreshold    TransformType = "threshold"
	TransformBoolNumber   TransformType = "bool-number"
	TransformBoolEnum     TransformType = "bool-enum"
	TransformEnumBool     TransformType = "enum-bool"
	TransformMapRange     TransformType = "map-range"
	TransformRound        TransformType = "round"
	TransformParseNumber  TransformType = "parse-number"
	TransformNumberString TransformType = "number-string"

	DirectionForward Direction = "forward"
	DirectionReverse Direction = "reverse"
)

type Profile struct {
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	// Identifier is the human-readable, mutable name. Bindings always retain
	// the opaque UUIDv7 ID so changing an identifier cannot break a route.
	Identifier string                `json:"identifier"`
	Version    int                   `json:"version"`
	Kind       ProfileKind           `json:"kind"`
	InputType  device.ValueType      `json:"inputType"`
	OutputType device.ValueType      `json:"outputType"`
	Default    *device.PropertyValue `json:"default,omitempty"`
	Transforms []Transform           `json:"transforms"`
}

type Transform struct {
	Type          TransformType     `json:"type"`
	Factor        *float64          `json:"factor,omitempty"`
	Offset        *float64          `json:"offset,omitempty"`
	Min           *float64          `json:"min,omitempty"`
	Max           *float64          `json:"max,omitempty"`
	Values        map[string]string `json:"values,omitempty"`
	ReverseValues map[string]string `json:"reverseValues,omitempty"`
	FromUnit      string            `json:"fromUnit,omitempty"`
	ToUnit        string            `json:"toUnit,omitempty"`
	Bands         []RangeBand       `json:"bands,omitempty"`
	Threshold     *float64          `json:"threshold,omitempty"`
	Operator      string            `json:"operator,omitempty"`
	TrueNumber    *float64          `json:"trueNumber,omitempty"`
	FalseNumber   *float64          `json:"falseNumber,omitempty"`
	TrueValue     string            `json:"trueValue,omitempty"`
	FalseValue    string            `json:"falseValue,omitempty"`
	InputMin      *float64          `json:"inputMin,omitempty"`
	InputMax      *float64          `json:"inputMax,omitempty"`
	OutputMin     *float64          `json:"outputMin,omitempty"`
	OutputMax     *float64          `json:"outputMax,omitempty"`
	Mode          string            `json:"mode,omitempty"`
}

// RangeBand is an ordered numeric band. Max is inclusive; a nil Max is the
// final catch-all band. Reverse is the canonical numeric representation of
// Value when converting from enum to number.
type RangeBand struct {
	Max     *float64 `json:"max,omitempty"`
	Value   string   `json:"value"`
	Reverse float64  `json:"reverse"`
}

type PreviewRequest struct {
	ProfileID string                `json:"profileId,omitempty"`
	Profile   Profile               `json:"profile"`
	Direction Direction             `json:"direction"`
	Value     *device.PropertyValue `json:"value"`
}

type Step struct {
	Index     int                   `json:"index"`
	Transform string                `json:"transform"`
	Input     *device.PropertyValue `json:"input"`
	Output    device.PropertyValue  `json:"output"`
}

type PreviewResult struct {
	ProfileID      string               `json:"profileId"`
	ProfileVersion int                  `json:"profileVersion"`
	Direction      Direction            `json:"direction"`
	Value          device.PropertyValue `json:"value"`
	Steps          []Step               `json:"steps"`
}

// ProfileIdentityMigration describes one persisted Profile whose legacy
// identifier-backed ID must become an opaque UUIDv7. PreviousID is migration
// metadata only and is never serialized into a Profile document.
type ProfileIdentityMigration struct {
	PreviousID string
	Profile    Profile
}

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for key := range e.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s: %s", key, e.Fields[key]))
	}
	return strings.Join(parts, "; ")
}

func Validate(profile Profile) error {
	fields := make(map[string]string)
	if profile.SchemaVersion != SchemaVersion {
		fields["profile.schemaVersion"] = fmt.Sprintf("must be %d", SchemaVersion)
	}
	if !device.ValidStableID(profile.ID) {
		fields["profile.id"] = "must be a stable lowercase identifier"
	}
	if profile.Identifier != "" && !device.ValidStableID(profile.Identifier) {
		fields["profile.identifier"] = "must be a stable lowercase identifier"
	}
	if profile.Version < 1 {
		fields["profile.version"] = "must be positive"
	}
	if profile.Kind != KindProvider && profile.Kind != KindCapability && profile.Kind != KindTarget {
		fields["profile.kind"] = "must be provider, capability, or target"
	}
	if !validValueType(profile.InputType) {
		fields["profile.inputType"] = "unsupported value type"
	}
	if !validValueType(profile.OutputType) {
		fields["profile.outputType"] = "unsupported value type"
	}
	if profile.Default != nil {
		if err := validateValue(*profile.Default, profile.InputType); err != nil {
			fields["profile.default"] = err.Error()
		}
	}
	current := profile.InputType
	for index, transform := range profile.Transforms {
		path := fmt.Sprintf("profile.transforms.%d", index)
		switch transform.Type {
		case TransformInvert:
			if current != device.ValueTypeBool {
				fields[path] = "invert requires bool input"
			}
		case TransformReciprocal:
			if !numericType(current) {
				fields[path] = "reciprocal requires int or number input"
			}
			current = device.ValueTypeNumber
		case TransformIntNumber:
			if current != device.ValueTypeInt {
				fields[path] = "int-number requires int input"
			}
			current = device.ValueTypeNumber
		case TransformScale:
			if !numericType(current) {
				fields[path] = "scale requires int or number input"
			}
			factor := float64Value(transform.Factor, 1)
			if factor == 0 || math.IsNaN(factor) || math.IsInf(factor, 0) {
				fields[path+".factor"] = "must be finite and non-zero"
			}
			if offset := float64Value(transform.Offset, 0); math.IsNaN(offset) || math.IsInf(offset, 0) {
				fields[path+".offset"] = "must be finite"
			}
			current = device.ValueTypeNumber
		case TransformClamp:
			if !numericType(current) {
				fields[path] = "clamp requires int or number input"
			}
			if transform.Min == nil && transform.Max == nil {
				fields[path] = "clamp requires min or max"
			}
			if transform.Min != nil && transform.Max != nil && *transform.Min > *transform.Max {
				fields[path] = "minimum exceeds maximum"
			}
			if transform.Min != nil && (math.IsNaN(*transform.Min) || math.IsInf(*transform.Min, 0)) {
				fields[path+".min"] = "must be finite"
			}
			if transform.Max != nil && (math.IsNaN(*transform.Max) || math.IsInf(*transform.Max, 0)) {
				fields[path+".max"] = "must be finite"
			}
			current = device.ValueTypeNumber
		case TransformEnum:
			if current != device.ValueTypeEnum && current != device.ValueTypeString {
				fields[path] = "enum mapping requires enum or string input"
			}
			validateEnumValues(fields, path, transform.Values, transform.ReverseValues)
		case TransformUnit:
			if !numericType(current) {
				fields[path] = "unit conversion requires int or number input"
			}
			if !unitConversionSupported(transform.FromUnit, transform.ToUnit) {
				fields[path] = "unsupported unit conversion"
			}
			current = device.ValueTypeNumber
		case TransformRangeEnum:
			if !numericType(current) {
				fields[path] = "range-enum requires int or number input"
			}
			validateRangeBands(fields, path, transform.Bands)
			current = device.ValueTypeEnum
		case TransformEnumNumber:
			if current != device.ValueTypeEnum && current != device.ValueTypeString {
				fields[path] = "enum-number requires enum or string input"
			}
			validateRangeBands(fields, path, transform.Bands)
			current = device.ValueTypeNumber
		case TransformThreshold:
			if !numericType(current) {
				fields[path] = "threshold requires int or number input"
			}
			validateThreshold(fields, path, transform)
			current = device.ValueTypeBool
		case TransformBoolNumber:
			if current != device.ValueTypeBool {
				fields[path] = "bool-number requires bool input"
			}
			validateThreshold(fields, path, transform)
			current = device.ValueTypeNumber
		case TransformBoolEnum:
			if current != device.ValueTypeBool {
				fields[path] = "bool-enum requires bool input"
			}
			validateBooleanLabels(fields, path, transform.TrueValue, transform.FalseValue)
			current = device.ValueTypeEnum
		case TransformEnumBool:
			if current != device.ValueTypeEnum && current != device.ValueTypeString {
				fields[path] = "enum-bool requires enum or string input"
			}
			validateBooleanLabels(fields, path, transform.TrueValue, transform.FalseValue)
			current = device.ValueTypeBool
		case TransformMapRange:
			if !numericType(current) {
				fields[path] = "map-range requires int or number input"
			}
			for name, value := range map[string]*float64{"inputMin": transform.InputMin, "inputMax": transform.InputMax, "outputMin": transform.OutputMin, "outputMax": transform.OutputMax} {
				if value == nil || !finitePointer(value) {
					fields[path+"."+name] = "must be a finite number"
				}
			}
			if transform.InputMin != nil && transform.InputMax != nil && *transform.InputMin == *transform.InputMax {
				fields[path] = "input range must not be zero"
			}
			if transform.OutputMin != nil && transform.OutputMax != nil && *transform.OutputMin == *transform.OutputMax {
				fields[path] = "output range must not be zero for reverse mapping"
			}
			current = device.ValueTypeNumber
		case TransformRound:
			if !numericType(current) {
				fields[path] = "round requires int or number input"
			}
			if transform.Mode != "nearest" && transform.Mode != "floor" && transform.Mode != "ceil" {
				fields[path+".mode"] = "must be nearest, floor, or ceil"
			}
			current = device.ValueTypeInt
		case TransformParseNumber:
			if current != device.ValueTypeString {
				fields[path] = "parse-number requires string input"
			}
			current = device.ValueTypeNumber
		case TransformNumberString:
			if !numericType(current) {
				fields[path] = "number-string requires int or number input"
			}
			current = device.ValueTypeString
		default:
			fields[path+".type"] = "unsupported transform"
		}
	}
	if validValueType(profile.OutputType) && current != profile.OutputType {
		fields["profile.outputType"] = fmt.Sprintf("pipeline produces %s", current)
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func validValueType(value device.ValueType) bool {
	return value == device.ValueTypeBool || value == device.ValueTypeInt || value == device.ValueTypeNumber || value == device.ValueTypeString || value == device.ValueTypeEnum
}

func numericType(value device.ValueType) bool {
	return value == device.ValueTypeInt || value == device.ValueTypeNumber
}

func float64Value(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	return *value
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func finitePointer(value *float64) bool { return value != nil && finite(*value) }

func validateEnumValues(fields map[string]string, path string, values, reverseValues map[string]string) {
	if len(values) == 0 {
		fields[path+".values"] = "must not be empty"
		return
	}
	sourcesByTarget := make(map[string][]string, len(values))
	for source, target := range values {
		if source == "" || target == "" {
			fields[path+".values"] = "source and target values must not be empty"
			return
		}
		sourcesByTarget[target] = append(sourcesByTarget[target], source)
	}
	for target, sources := range sourcesByTarget {
		canonical, hasReverse := reverseValues[target]
		if len(sources) > 1 && !hasReverse {
			fields[path+".reverseValues."+target] = "required when multiple sources map to this target"
			continue
		}
		if !hasReverse {
			continue
		}
		if canonical == "" {
			fields[path+".reverseValues."+target] = "must not be empty"
			continue
		}
		if !containsString(sources, canonical) {
			fields[path+".reverseValues."+target] = "must be a source that maps to this target"
		}
	}
	for target, source := range reverseValues {
		if target == "" {
			fields[path+".reverseValues"] = "reverse targets must not be empty"
			continue
		}
		if _, ok := values[source]; !ok {
			fields[path+".reverseValues."+target] = "must be a source defined by values"
			continue
		}
		// A reverse key matching a value emitted by the forward table is the
		// canonical reverse route for that output, so it must remain one of
		// that output's sources. Other reverse keys are deliberate write-only
		// aliases: for example heat -> auto and cool -> auto while auto is the
		// single canonical value published by forward state updates.
		if sources, emitted := sourcesByTarget[target]; emitted && !containsString(sources, source) {
			fields[path+".reverseValues."+target] = "must be a source that maps to this target"
		}
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func validateBooleanLabels(fields map[string]string, path, trueValue, falseValue string) {
	if strings.TrimSpace(trueValue) == "" || strings.TrimSpace(falseValue) == "" {
		fields[path] = "trueValue and falseValue must not be empty"
	} else if trueValue == falseValue {
		fields[path] = "trueValue and falseValue must be different"
	}
}

func validateRangeBands(fields map[string]string, path string, bands []RangeBand) {
	if len(bands) < 2 {
		fields[path+".bands"] = "must contain at least two ordered bands"
		return
	}
	seen := make(map[string]struct{}, len(bands))
	var previousMax *float64
	for index, band := range bands {
		bandPath := fmt.Sprintf("%s.bands.%d", path, index)
		if strings.TrimSpace(band.Value) == "" {
			fields[bandPath+".value"] = "must not be empty"
		}
		if _, duplicate := seen[band.Value]; duplicate {
			fields[bandPath+".value"] = "must be unique for reverse mapping"
		}
		seen[band.Value] = struct{}{}
		if !finite(band.Reverse) {
			fields[bandPath+".reverse"] = "must be finite"
		}
		if band.Max == nil {
			if index != len(bands)-1 {
				fields[bandPath+".max"] = "only the final band may omit max"
			}
		} else {
			if !finite(*band.Max) {
				fields[bandPath+".max"] = "must be finite"
			}
			if previousMax != nil && *band.Max <= *previousMax {
				fields[bandPath+".max"] = "must be greater than the previous max"
			}
		}
		if previousMax != nil && band.Reverse <= *previousMax {
			fields[bandPath+".reverse"] = "must be greater than the previous max"
		}
		if band.Max != nil && band.Reverse > *band.Max {
			fields[bandPath+".reverse"] = "must be within this band"
		}
		if band.Max != nil {
			value := *band.Max
			previousMax = &value
		}
	}
	if bands[len(bands)-1].Max != nil {
		fields[path+".bands"] = "final band must omit max to cover remaining values"
	}
}

func validateThreshold(fields map[string]string, path string, transform Transform) {
	if transform.Threshold == nil || !finite(*transform.Threshold) {
		fields[path+".threshold"] = "must be a finite number"
	}
	if transform.Operator != "gte" && transform.Operator != "gt" && transform.Operator != "lte" && transform.Operator != "lt" {
		fields[path+".operator"] = "must be gte, gt, lte, or lt"
	}
	if transform.TrueNumber == nil || !finitePointer(transform.TrueNumber) {
		fields[path+".trueNumber"] = "must be a finite reverse value"
	}
	if transform.FalseNumber == nil || !finitePointer(transform.FalseNumber) {
		fields[path+".falseNumber"] = "must be a finite reverse value"
	}
	if transform.Threshold != nil && transform.TrueNumber != nil && transform.FalseNumber != nil && finite(*transform.Threshold) && finite(*transform.TrueNumber) && finite(*transform.FalseNumber) {
		if !thresholdMatches(*transform.TrueNumber, *transform.Threshold, transform.Operator) {
			fields[path+".trueNumber"] = "reverse value must evaluate to true"
		}
		if thresholdMatches(*transform.FalseNumber, *transform.Threshold, transform.Operator) {
			fields[path+".falseNumber"] = "reverse value must evaluate to false"
		}
	}
}

func thresholdMatches(value, threshold float64, operator string) bool {
	switch operator {
	case "gte":
		return value >= threshold
	case "gt":
		return value > threshold
	case "lte":
		return value <= threshold
	case "lt":
		return value < threshold
	default:
		return false
	}
}

func transformOutputType(input device.ValueType, transform Transform) device.ValueType {
	switch transform.Type {
	case TransformReciprocal, TransformIntNumber, TransformScale, TransformClamp, TransformUnit, TransformMapRange, TransformParseNumber, TransformEnumNumber, TransformBoolNumber:
		return device.ValueTypeNumber
	case TransformRangeEnum, TransformBoolEnum:
		return device.ValueTypeEnum
	case TransformThreshold, TransformEnumBool:
		return device.ValueTypeBool
	case TransformRound:
		return device.ValueTypeInt
	case TransformNumberString:
		return device.ValueTypeString
	default:
		return input
	}
}
