package mapping

import (
	"fmt"
	"strings"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

// ModelEnumOverride replaces the enum option list of one unified-model property
// without changing its path, type, or parameter level.
type ModelEnumOverride struct {
	ID           string      `json:"id"`
	DeviceType   device.Type `json:"deviceType"`
	EndpointID   string      `json:"endpointId"`
	CapabilityID string      `json:"capabilityId"`
	PropertyID   string      `json:"propertyId"`
	Enum         []string    `json:"enum"`
}

func (o ModelEnumOverride) Path() device.ParameterPath {
	return device.ParameterPath{EndpointID: o.EndpointID, CapabilityID: o.CapabilityID, PropertyID: o.PropertyID}
}

func (o ModelEnumOverride) Key() string {
	return string(o.DeviceType) + "\x00" + o.Path().Key()
}

func ValidateModelEnumOverride(item ModelEnumOverride) error {
	fields := make(map[string]string)
	if !device.ValidStableID(item.ID) {
		fields["id"] = "must be a stable lowercase identifier"
	}
	if !device.ValidStableID(string(item.DeviceType)) {
		fields["deviceType"] = "must be a stable lowercase identifier"
	}
	for name, value := range map[string]string{
		"endpointId": item.EndpointID, "capabilityId": item.CapabilityID, "propertyId": item.PropertyID,
	} {
		if !device.ValidStableID(value) {
			fields[name] = "must be a stable lowercase identifier"
		}
	}
	if len(item.Enum) == 0 {
		fields["enum"] = "must not be empty"
	}
	seen := make(map[string]struct{}, len(item.Enum))
	for index, option := range item.Enum {
		trimmed := strings.TrimSpace(option)
		path := fmt.Sprintf("enum.%d", index)
		if trimmed == "" {
			fields[path] = "must not be empty"
			continue
		}
		if trimmed != option {
			fields[path] = "must not include leading or trailing whitespace"
		}
		if _, duplicate := seen[trimmed]; duplicate {
			fields[path] = "must be unique"
		}
		seen[trimmed] = struct{}{}
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func ModelEnumOverrideID(deviceType device.Type, path device.ParameterPath) string {
	return fmt.Sprintf("enum-%s-%s-%s-%s", deviceType, path.EndpointID, path.CapabilityID, path.PropertyID)
}
