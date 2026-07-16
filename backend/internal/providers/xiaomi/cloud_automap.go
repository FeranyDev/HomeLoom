package xiaomi

import (
	"strings"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

// autoMapCloudDevice replaces only the generated air-conditioner baseline.
// The baseline exists so a device can be selected before its MIoT Spec is
// loaded, but its guessed SIID/PIID values must never be used once the real
// specification is available. Explicit user mappings are left untouched.
func autoMapCloudDevice(configured DeviceConfig, document miotSpecDocument) (DeviceConfig, bool) {
	if configured.Type != device.TypeAirConditioner || !generatedAirConditionerBaseline(configured.Properties) {
		return configured, false
	}
	mapped := make([]PropertyMapping, 0, 8)
	for _, service := range document.Services {
		serviceName := urnName(service.Type)
		for _, property := range service.Properties {
			propertyName := urnName(property.Type)
			capabilityID, modelPropertyID, displayName := "", "", ""
			switch {
			case serviceName == "air-conditioner" && propertyName == "on":
				capabilityID, modelPropertyID, displayName = "air-conditioner", "active", "启用"
			case serviceName == "air-conditioner" && propertyName == "mode":
				capabilityID, modelPropertyID, displayName = "air-conditioner", "target-mode", "运行模式"
			case serviceName == "air-conditioner" && (propertyName == "current-state" || propertyName == "state") && len(property.ValueList) > 0:
				capabilityID, modelPropertyID, displayName = "air-conditioner", "current-state", "当前工作状态"
			case propertyName == "target-temperature":
				capabilityID, modelPropertyID, displayName = "temperature", "target-temperature", "目标温度"
			case propertyName == "temperature":
				capabilityID, modelPropertyID, displayName = "temperature", "current-temperature", "当前温度"
			case propertyName == "fan-level":
				capabilityID, modelPropertyID, displayName = "air-conditioner", "fan-speed", "风速档位"
			case propertyName == "vertical-swing":
				capabilityID, modelPropertyID, displayName = "air-conditioner", "vertical-swing", "上下扫风"
			case propertyName == "horizontal-swing":
				capabilityID, modelPropertyID, displayName = "air-conditioner", "horizontal-swing", "左右扫风"
			default:
				continue
			}
			mapping := propertyMappingFromSpec("main", capabilityID, service, property, miotValueType(property))
			mapping.PropertyID, mapping.Name, mapping.CapabilityType = modelPropertyID, displayName, capabilityID
			switch modelPropertyID {
			case "current-temperature", "target-temperature":
				mapping.ValueType = device.ValueTypeNumber
			case "active", "vertical-swing", "horizontal-swing":
				mapping.ValueType = device.ValueTypeBool
			case "current-state", "target-mode", "fan-speed":
				mapping.ValueType = device.ValueTypeEnum
			}
			mapping.Enum = canonicalCloudEnum(modelPropertyID, mapping.Enum)
			mapped = append(mapped, mapping)
		}
	}
	if !hasModelProperties(mapped, "active", "target-mode", "target-temperature") {
		return configured, false
	}
	configured.Properties = mapped
	return configured, true
}

func generatedAirConditionerBaseline(properties []PropertyMapping) bool {
	if len(properties) != 3 && len(properties) != 5 {
		return false
	}
	expected := map[string]int{"air-conditioner/active": 1, "air-conditioner/target-mode": 2, "temperature/target-temperature": 3}
	if len(properties) == 5 {
		expected = map[string]int{"air-conditioner/active": 1, "air-conditioner/current-state": 2, "air-conditioner/target-mode": 3, "temperature/current-temperature": 4, "temperature/target-temperature": 5}
	}
	found := make(map[string]bool, len(expected))
	for _, mapping := range properties {
		key := mapping.CapabilityID + "/" + mapping.PropertyID
		piid, ok := expected[key]
		if !ok || mapping.SIID != 2 || mapping.PIID != piid {
			return false
		}
		found[key] = true
	}
	for key := range expected {
		if !found[key] {
			return false
		}
	}
	return true
}

func hasModelProperties(properties []PropertyMapping, propertyIDs ...string) bool {
	found := make(map[string]bool, len(propertyIDs))
	for _, mapping := range properties {
		found[mapping.PropertyID] = true
	}
	for _, propertyID := range propertyIDs {
		if !found[propertyID] {
			return false
		}
	}
	return true
}

func canonicalCloudEnum(propertyID string, values map[string]any) map[string]any {
	if len(values) == 0 {
		return values
	}
	result := make(map[string]any, len(values))
	for name, value := range values {
		canonical := strings.ToLower(strings.TrimSpace(name))
		canonical = strings.NewReplacer("_", "-", " ", "-").Replace(canonical)
		switch canonical {
		case "automatic":
			canonical = "auto"
		case "cooling":
			canonical = "cool"
		case "heating":
			canonical = "heat"
		case "drying", "dehumidify", "dehumidifying":
			canonical = "dry"
		case "fan-only", "fan-mode":
			canonical = "fan"
		case "middle":
			canonical = "medium"
		}
		if propertyID == "current-state" {
			switch canonical {
			case "cool":
				canonical = "cooling"
			case "heat":
				canonical = "heating"
			case "dry":
				canonical = "drying"
			case "fan":
				canonical = "fan-only"
			}
		}
		result[canonical] = value
	}
	return result
}
