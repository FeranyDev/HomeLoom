package mapping

import (
	"fmt"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

// CustomModelProperty extends one versioned unified device model without
// changing the built-in contract. The address remains Endpoint / Capability /
// Property, and the definition carries the complete value contract.
type CustomModelProperty struct {
	ID             string                    `json:"id"`
	DeviceType     device.Type               `json:"deviceType"`
	EndpointID     string                    `json:"endpointId"`
	EndpointName   string                    `json:"endpointName"`
	EndpointType   string                    `json:"endpointType"`
	CapabilityID   string                    `json:"capabilityId"`
	CapabilityType string                    `json:"capabilityType"`
	Definition     device.PropertyDefinition `json:"definition"`
}

func (p CustomModelProperty) Path() device.ParameterPath {
	return device.ParameterPath{EndpointID: p.EndpointID, CapabilityID: p.CapabilityID, PropertyID: p.Definition.ID}
}

func (p CustomModelProperty) Key() string {
	return string(p.DeviceType) + "\x00" + p.Path().Key()
}

func ValidateCustomModelProperty(item CustomModelProperty) error {
	fields := make(map[string]string)
	if !device.ValidStableID(item.ID) {
		fields["id"] = "must be a stable lowercase identifier"
	}
	if !device.ValidStableID(string(item.DeviceType)) {
		fields["deviceType"] = "must be a stable lowercase identifier"
	}
	for name, value := range map[string]string{
		"endpointId": item.EndpointID, "capabilityId": item.CapabilityID, "definition.id": item.Definition.ID,
	} {
		if !device.ValidStableID(value) {
			fields[name] = "must be a stable lowercase identifier"
		}
	}
	if item.EndpointName == "" {
		fields["endpointName"] = "is required"
	}
	if item.EndpointType == "" {
		fields["endpointType"] = "is required"
	}
	if item.CapabilityType == "" {
		fields["capabilityType"] = "is required"
	}
	if item.Definition.Name == "" {
		fields["definition.name"] = "is required"
	}
	item.Definition.ParameterLevel = device.ParameterCustom
	if err := validateCustomDefinition(item.Definition); err != nil {
		fields["definition"] = err.Error()
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func validateCustomDefinition(definition device.PropertyDefinition) error {
	var value device.PropertyValue
	switch definition.Type {
	case device.ValueTypeBool:
		value = device.BoolValue(false)
	case device.ValueTypeInt:
		candidate := int64(0)
		if definition.Min != nil {
			candidate = int64(*definition.Min)
		}
		value = device.IntValue(candidate)
	case device.ValueTypeNumber:
		candidate := 0.0
		if definition.Min != nil {
			candidate = *definition.Min
		}
		value = device.NumberValue(candidate)
	case device.ValueTypeString:
		value = device.StringValue("")
	case device.ValueTypeEnum:
		if len(definition.Enum) == 0 {
			return fmt.Errorf("enum options are required")
		}
		value = device.EnumValue(definition.Enum[0])
	default:
		return fmt.Errorf("unsupported value type %q", definition.Type)
	}
	return (device.Property{Definition: definition, Value: value}).Validate()
}

func CustomModelParameter(item CustomModelProperty) device.ModelParameter {
	definition := item.Definition
	return device.ModelParameter{
		Path: item.Path(), Name: definition.Name, Level: device.ParameterCustom,
		Type: definition.Type, Unit: definition.Unit, Readable: definition.Readable,
		Writable: definition.Writable, Notifiable: definition.Notifiable,
		Min: definition.Min, Max: definition.Max, Step: definition.Step,
		StaleAfterSeconds: definition.StaleAfterSeconds,
		Enum:              append([]string(nil), definition.Enum...),
		Publisher:         device.ParameterRole{Level: device.ParameterCustom, Behavior: "preserve-and-mark-custom"},
		Consumer:          device.ParameterRole{Level: device.ParameterCustom, Behavior: "explicit-path-mapping-only"},
		PublisherNotes:    "Provider 通过显式路由发布自定义属性",
		ConsumerNotes:     "Consumer 通过显式路由使用自定义属性",
	}
}
