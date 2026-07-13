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

	TransformInvert TransformType = "invert"
	TransformScale  TransformType = "scale"
	TransformClamp  TransformType = "clamp"
	TransformEnum   TransformType = "enum"
	TransformUnit   TransformType = "unit"

	DirectionForward Direction = "forward"
	DirectionReverse Direction = "reverse"
)

type Profile struct {
	SchemaVersion int                   `json:"schemaVersion"`
	ID            string                `json:"id"`
	Version       int                   `json:"version"`
	Kind          ProfileKind           `json:"kind"`
	InputType     device.ValueType      `json:"inputType"`
	OutputType    device.ValueType      `json:"outputType"`
	Default       *device.PropertyValue `json:"default,omitempty"`
	Transforms    []Transform           `json:"transforms"`
}

type Transform struct {
	Type     TransformType     `json:"type"`
	Factor   *float64          `json:"factor,omitempty"`
	Offset   *float64          `json:"offset,omitempty"`
	Min      *float64          `json:"min,omitempty"`
	Max      *float64          `json:"max,omitempty"`
	Values   map[string]string `json:"values,omitempty"`
	FromUnit string            `json:"fromUnit,omitempty"`
	ToUnit   string            `json:"toUnit,omitempty"`
}

type PreviewRequest struct {
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
			if len(transform.Values) == 0 {
				fields[path+".values"] = "must not be empty"
			}
			seen := make(map[string]struct{}, len(transform.Values))
			for source, target := range transform.Values {
				if source == "" || target == "" {
					fields[path+".values"] = "source and target values must not be empty"
				}
				if _, duplicate := seen[target]; duplicate {
					fields[path+".values"] = "target values must be unique for reverse mapping"
				}
				seen[target] = struct{}{}
			}
		case TransformUnit:
			if !numericType(current) {
				fields[path] = "unit conversion requires int or number input"
			}
			if _, _, ok := unitFormula(transform.FromUnit, transform.ToUnit); !ok {
				fields[path] = "unsupported unit conversion"
			}
			current = device.ValueTypeNumber
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
