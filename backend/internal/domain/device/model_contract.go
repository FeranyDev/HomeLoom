package device

import (
	"fmt"
	"sort"
	"strings"
)

// ParameterPath is the stable address of one property in the unified model.
type ParameterPath struct {
	EndpointID   string `json:"endpointId"`
	CapabilityID string `json:"capabilityId"`
	PropertyID   string `json:"propertyId"`
}

func (p ParameterPath) Key() string {
	return strings.Join([]string{p.EndpointID, p.CapabilityID, p.PropertyID}, "\x00")
}

func (p ParameterPath) String() string {
	return p.EndpointID + "/" + p.CapabilityID + "/" + p.PropertyID
}

type ModelParameter struct {
	Path              ParameterPath  `json:"path"`
	Name              string         `json:"name"`
	Level             ParameterLevel `json:"level"`
	Type              ValueType      `json:"type"`
	Unit              string         `json:"unit,omitempty"`
	Readable          bool           `json:"readable"`
	Writable          bool           `json:"writable"`
	Notifiable        bool           `json:"notifiable"`
	Min               *float64       `json:"min,omitempty"`
	Max               *float64       `json:"max,omitempty"`
	Step              *float64       `json:"step,omitempty"`
	StaleAfterSeconds int            `json:"staleAfterSeconds,omitempty"`
	Enum              []string       `json:"enum,omitempty"`
	Publisher         ParameterRole  `json:"publisher"`
	Consumer          ParameterRole  `json:"consumer"`
	PublisherNotes    string         `json:"publisherNotes,omitempty"`
	ConsumerNotes     string         `json:"consumerNotes,omitempty"`
}

type ParameterRole struct {
	Level    ParameterLevel `json:"level"`
	Behavior string         `json:"behavior"`
}

type CustomParameterPolicy struct {
	Publisher ParameterRole `json:"publisher"`
	Consumer  ParameterRole `json:"consumer"`
}

type ModelContract struct {
	DeviceType Type                  `json:"deviceType"`
	Version    int                   `json:"version"`
	Parameters []ModelParameter      `json:"parameters"`
	Custom     CustomParameterPolicy `json:"custom"`
}

func required(capabilityID, propertyID, name string, valueType ValueType, writable bool) ModelParameter {
	return ModelParameter{Path: ParameterPath{EndpointID: "main", CapabilityID: capabilityID, PropertyID: propertyID}, Name: name, Level: ParameterRequired, Type: valueType, Readable: true, Writable: writable, Notifiable: true, Publisher: ParameterRole{Level: ParameterRequired, Behavior: "must-publish"}, Consumer: ParameterRole{Level: ParameterRequired, Behavior: "must-map"}, PublisherNotes: "必须发布且符合统一类型", ConsumerNotes: "Consumer 声明支持该模型时必须映射"}
}

func optional(capabilityID, propertyID, name string, valueType ValueType, unit string, writable bool, enum ...string) ModelParameter {
	return ModelParameter{Path: ParameterPath{EndpointID: "main", CapabilityID: capabilityID, PropertyID: propertyID}, Name: name, Level: ParameterOptional, Type: valueType, Unit: unit, Readable: true, Writable: writable, Notifiable: true, Enum: enum, Publisher: ParameterRole{Level: ParameterOptional, Behavior: "publish-if-supported"}, Consumer: ParameterRole{Level: ParameterOptional, Behavior: "map-if-supported"}, PublisherNotes: "Provider 有能力时发布", ConsumerNotes: "Consumer 支持时映射，缺失可降级"}
}

func withCustomPolicy(contract ModelContract) ModelContract {
	contract.Custom = CustomParameterPolicy{
		Publisher: ParameterRole{Level: ParameterCustom, Behavior: "preserve-and-mark-custom"},
		Consumer:  ParameterRole{Level: ParameterCustom, Behavior: "explicit-path-mapping-only"},
	}
	return contract
}

func batteryParameters() []ModelParameter {
	return []ModelParameter{
		optional("battery", "level", "电池电量", ValueTypeInt, "percent", false),
		optional("battery", "low", "低电量", ValueTypeBool, "", false),
		optional("security", "tampered", "防拆状态", ValueTypeBool, "", false),
	}
}

var modelContracts = map[Type]ModelContract{
	TypeSwitch: {DeviceType: TypeSwitch, Version: 1, Parameters: []ModelParameter{
		required("switch", "power", "开关", ValueTypeBool, true),
	}},
	TypeLightbulb: {DeviceType: TypeLightbulb, Version: 1, Parameters: []ModelParameter{
		required("switch", "power", "开关", ValueTypeBool, true),
		optional("light", "brightness", "亮度", ValueTypeNumber, "percent", true),
		optional("light", "color-temperature", "色温", ValueTypeInt, "mired", true),
		optional("light", "hue", "色相", ValueTypeNumber, "degree", true),
		optional("light", "saturation", "饱和度", ValueTypeNumber, "percent", true),
	}},
	TypeOutlet: {DeviceType: TypeOutlet, Version: 1, Parameters: []ModelParameter{
		required("switch", "power", "开关", ValueTypeBool, true),
		optional("outlet", "in-use", "正在使用", ValueTypeBool, "", false),
		optional("electrical", "current-power", "当前功率", ValueTypeNumber, "watt", false),
		optional("electrical", "energy", "累计电量", ValueTypeNumber, "kilowatt-hour", false),
	}},
	TypeTemperatureSensor: {DeviceType: TypeTemperatureSensor, Version: 1, Parameters: append([]ModelParameter{
		required("temperature", "current-temperature", "当前温度", ValueTypeNumber, false),
	}, batteryParameters()...)},
	TypeHumiditySensor: {DeviceType: TypeHumiditySensor, Version: 1, Parameters: append([]ModelParameter{
		required("humidity", "current-humidity", "当前湿度", ValueTypeNumber, false),
	}, batteryParameters()...)},
	TypeContactSensor: {DeviceType: TypeContactSensor, Version: 1, Parameters: append([]ModelParameter{
		required("contact", "contact-detected", "接触状态", ValueTypeBool, false),
	}, batteryParameters()...)},
	TypeMotionSensor: {DeviceType: TypeMotionSensor, Version: 1, Parameters: append([]ModelParameter{
		required("motion", "motion-detected", "活动状态", ValueTypeBool, false),
	}, batteryParameters()...)},
	TypeFan: {DeviceType: TypeFan, Version: 1, Parameters: []ModelParameter{
		required("fan", "active", "启用", ValueTypeBool, true),
		required("fan", "current-state", "当前状态", ValueTypeEnum, false),
		optional("fan", "target-state", "目标模式", ValueTypeEnum, "", true, "manual", "auto"),
		optional("fan", "rotation-speed", "转速", ValueTypeNumber, "percent", true),
		optional("fan", "swing-mode", "摇头", ValueTypeBool, "", true),
		optional("fan", "rotation-direction", "旋转方向", ValueTypeEnum, "", true, "clockwise", "counter-clockwise"),
		optional("fan", "lock-physical-controls", "物理控制锁", ValueTypeBool, "", true),
	}},
	TypeAirPurifier: {DeviceType: TypeAirPurifier, Version: 1, Parameters: []ModelParameter{
		required("air-purifier", "active", "启用", ValueTypeBool, true),
		required("air-purifier", "current-state", "当前状态", ValueTypeEnum, false),
		optional("air-purifier", "target-state", "目标模式", ValueTypeEnum, "", true, "manual", "auto"),
		optional("air-purifier", "rotation-speed", "净化速度", ValueTypeNumber, "percent", true),
		optional("air-purifier", "swing-mode", "摆风", ValueTypeBool, "", true),
		optional("air-purifier", "lock-physical-controls", "物理控制锁", ValueTypeBool, "", true),
		optional("air-quality", "current-air-quality", "空气质量", ValueTypeEnum, "", false, "unknown", "excellent", "good", "fair", "inferior", "poor"),
		optional("air-quality", "pm2.5-density", "PM2.5", ValueTypeNumber, "microgram-per-cubic-meter", false),
		optional("air-quality", "voc-density", "VOC", ValueTypeNumber, "microgram-per-cubic-meter", false),
		optional("filter", "life-level", "滤芯寿命", ValueTypeNumber, "percent", false),
		optional("filter", "change-indication", "需要更换滤芯", ValueTypeBool, "", false),
	}},
	TypeWindowCovering: {DeviceType: TypeWindowCovering, Version: 1, Parameters: []ModelParameter{
		required("window-covering", "current-position", "当前位置", ValueTypeInt, false),
		required("window-covering", "target-position", "目标位置", ValueTypeInt, true),
		required("window-covering", "position-state", "运动状态", ValueTypeEnum, false),
		optional("window-covering", "obstruction-detected", "障碍物检测", ValueTypeBool, "", false),
	}},
}

func ModelContracts() []ModelContract {
	result := make([]ModelContract, 0, len(modelContracts))
	for _, contract := range modelContracts {
		clone := withCustomPolicy(contract)
		clone.Parameters = append([]ModelParameter(nil), contract.Parameters...)
		for index := range clone.Parameters {
			clone.Parameters[index].Enum = append([]string(nil), clone.Parameters[index].Enum...)
		}
		result = append(result, clone)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DeviceType < result[j].DeviceType })
	return result
}

func ModelContractFor(deviceType Type) (ModelContract, bool) {
	contract, ok := modelContracts[deviceType]
	if !ok {
		return ModelContract{}, false
	}
	contract = withCustomPolicy(contract)
	contract.Parameters = append([]ModelParameter(nil), contract.Parameters...)
	return contract, true
}

func parameterIndex(deviceType Type) map[string]ModelParameter {
	contract, ok := modelContracts[deviceType]
	if !ok {
		return nil
	}
	result := make(map[string]ModelParameter, len(contract.Parameters))
	for _, parameter := range contract.Parameters {
		result[parameter.Path.Key()] = parameter
	}
	return result
}

// NormalizeModelParameters is the Provider/publisher boundary. It annotates
// standard parameters, marks unknown properties as custom, and validates all
// required parameters before a snapshot reaches the registry or a Target.
func (d *Device) NormalizeModelParameters() error {
	index := parameterIndex(d.Type)
	for endpointIndex := range d.Endpoints {
		endpoint := &d.Endpoints[endpointIndex]
		for capabilityIndex := range endpoint.Capabilities {
			capability := &endpoint.Capabilities[capabilityIndex]
			for propertyIndex := range capability.Properties {
				definition := &capability.Properties[propertyIndex].Definition
				parameter, standard := index[(ParameterPath{EndpointID: endpoint.ID, CapabilityID: capability.ID, PropertyID: definition.ID}).Key()]
				if standard {
					definition.ParameterLevel = parameter.Level
				} else {
					definition.ParameterLevel = ParameterCustom
				}
			}
		}
	}
	return d.Validate()
}

func validatePublisherModel(d Device) error {
	index := parameterIndex(d.Type)
	if index == nil {
		return nil
	}
	found := make(map[string]bool, len(index))
	for _, endpoint := range d.Endpoints {
		for _, capability := range endpoint.Capabilities {
			for _, property := range capability.Properties {
				path := ParameterPath{EndpointID: endpoint.ID, CapabilityID: capability.ID, PropertyID: property.Definition.ID}
				parameter, standard := index[path.Key()]
				if !standard {
					if property.Definition.ParameterLevel != "" && property.Definition.ParameterLevel != ParameterCustom {
						return fmt.Errorf("custom parameter %s cannot declare level %q", path, property.Definition.ParameterLevel)
					}
					continue
				}
				found[path.Key()] = true
				if property.Definition.ParameterLevel != "" && property.Definition.ParameterLevel != parameter.Level {
					return fmt.Errorf("parameter %s level %q does not match contract %q", path, property.Definition.ParameterLevel, parameter.Level)
				}
				if property.Definition.Type != parameter.Type {
					return fmt.Errorf("parameter %s type %q does not match contract %q", path, property.Definition.Type, parameter.Type)
				}
			}
		}
	}
	for key, parameter := range index {
		if parameter.Level == ParameterRequired && !found[key] {
			return fmt.Errorf("required parameter %s is missing", parameter.Path)
		}
	}
	return nil
}

type ConsumerParameterMapping struct {
	Source ParameterPath  `json:"source"`
	Target string         `json:"target"`
	Level  ParameterLevel `json:"level"`
}

type ConsumerModelContract struct {
	ConsumerID string                     `json:"consumerId"`
	DeviceType Type                       `json:"deviceType"`
	Parameters []ConsumerParameterMapping `json:"parameters"`
}

type MappedParameter struct {
	Source   ParameterPath  `json:"source"`
	Target   string         `json:"target"`
	Level    ParameterLevel `json:"level"`
	Property Property       `json:"property"`
}

type ConsumerProjection struct {
	ConsumerID      string            `json:"consumerId"`
	DeviceID        string            `json:"deviceId"`
	Parameters      []MappedParameter `json:"parameters"`
	MissingOptional []ParameterPath   `json:"missingOptional,omitempty"`
}

// ProjectForConsumer is the device-user boundary. Required mappings fail
// closed, optional mappings degrade cleanly, and custom properties are only
// exposed when the consumer explicitly marks that source mapping as custom.
func ProjectForConsumer(item Device, contract ConsumerModelContract) (ConsumerProjection, error) {
	if contract.ConsumerID == "" || contract.DeviceType != item.Type {
		return ConsumerProjection{}, fmt.Errorf("consumer contract does not match device type %q", item.Type)
	}
	result := ConsumerProjection{ConsumerID: contract.ConsumerID, DeviceID: item.ID, Parameters: make([]MappedParameter, 0, len(contract.Parameters))}
	index := parameterIndex(item.Type)
	for _, mapping := range contract.Parameters {
		if mapping.Target == "" || (mapping.Level != ParameterRequired && mapping.Level != ParameterOptional && mapping.Level != ParameterCustom) {
			return ConsumerProjection{}, fmt.Errorf("invalid consumer mapping for %s", mapping.Source)
		}
		property, exists := item.Property(mapping.Source.EndpointID, mapping.Source.CapabilityID, mapping.Source.PropertyID)
		if !exists {
			if mapping.Level == ParameterRequired {
				return ConsumerProjection{}, fmt.Errorf("consumer %q requires parameter %s", contract.ConsumerID, mapping.Source)
			}
			result.MissingOptional = append(result.MissingOptional, mapping.Source)
			continue
		}
		_, standard := index[mapping.Source.Key()]
		if mapping.Level == ParameterCustom && standard {
			return ConsumerProjection{}, fmt.Errorf("standard parameter %s cannot use a custom consumer mapping", mapping.Source)
		}
		if mapping.Level != ParameterCustom && !standard {
			return ConsumerProjection{}, fmt.Errorf("custom parameter %s requires an explicit custom consumer mapping", mapping.Source)
		}
		result.Parameters = append(result.Parameters, MappedParameter{Source: mapping.Source, Target: mapping.Target, Level: mapping.Level, Property: property})
	}
	return result, nil
}
