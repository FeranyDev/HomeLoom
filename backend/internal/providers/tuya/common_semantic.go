package tuya

import (
	"strings"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

// commonSemanticRule describes a stable HomeLoom property backed by one of
// Tuya's product-specific DPs. The raw tuya-dp capability is always retained;
// these rules add a useful common surface without hiding vendor extensions.
type commonSemanticRule struct {
	CapabilityID   string
	CapabilityType string
	PropertyID     string
	Name           string
	Unit           string
	Type           device.ValueType
	Codes          []string
	Writable       bool
	Min            *float64
	Max            *float64
	Step           *float64
	Enum           []string
}

func commonSemanticRules(item TuyaDevice, specs map[string]DPSpec) []commonSemanticRule {
	typeValue := inferDeviceType(item.Category, specs)
	rules := make([]commonSemanticRule, 0, 20)
	add := func(rule commonSemanticRule) {
		if _, _, ok := findCommonDP(specs, rule.Codes); ok {
			rules = append(rules, rule)
		}
	}
	addPower := func(capabilityID, capabilityType string) {
		add(commonSemanticRule{CapabilityID: capabilityID, CapabilityType: capabilityType, PropertyID: "power", Name: "开关", Type: device.ValueTypeBool, Codes: []string{"switch_led", "switch_1", "switch", "fan_switch", "power", "relay"}, Writable: true})
	}
	addActive := func(capabilityID, capabilityType string) {
		add(commonSemanticRule{CapabilityID: capabilityID, CapabilityType: capabilityType, PropertyID: "active", Name: "启用", Type: device.ValueTypeBool, Codes: []string{"switch", "switch_1", "fan_switch", "power", "work_state"}, Writable: true})
	}
	percent := func(capabilityID, propertyID, name string, writable bool, codes ...string) commonSemanticRule {
		minimum, maximum, step := 0.0, 100.0, 1.0
		return commonSemanticRule{CapabilityID: capabilityID, CapabilityType: commonCapabilityType(capabilityID), PropertyID: propertyID, Name: name, Unit: "percent", Type: device.ValueTypeNumber, Codes: codes, Writable: writable, Min: &minimum, Max: &maximum, Step: &step}
	}
	measurement := func(capabilityID, propertyID, name, unit string, writable bool, codes ...string) commonSemanticRule {
		return commonSemanticRule{CapabilityID: capabilityID, CapabilityType: commonCapabilityType(capabilityID), PropertyID: propertyID, Name: name, Unit: unit, Type: device.ValueTypeNumber, Codes: codes, Writable: writable}
	}

	switch typeValue {
	case device.TypeLightbulb:
		addPower("switch", "switch")
		add(percent("light", "brightness", "亮度", true, "bright_value", "bright_value_v2", "brightness", "bright"))
		add(commonSemanticRule{CapabilityID: "light", CapabilityType: "light", PropertyID: "color-temperature", Name: "色温", Unit: "mired", Type: device.ValueTypeInt, Codes: []string{"temp_value", "colour_temperature", "color_temperature", "color_temp"}, Writable: true})
		add(measurement("light", "hue", "色相", "degree", true, "colour_data_h", "color_hue", "hue"))
		add(percent("light", "saturation", "饱和度", true, "colour_data_s", "color_saturation", "saturation"))
	case device.TypeOutlet:
		addPower("switch", "switch")
		add(measurement("electrical", "current-power", "当前功率", "watt", false, "cur_power", "current_power"))
		add(measurement("electrical", "voltage", "电压", "volt", false, "cur_voltage", "voltage"))
		add(measurement("electrical", "current", "电流", "ampere", false, "cur_current", "current"))
		add(measurement("electrical", "energy", "累计电量", "kilowatt-hour", false, "add_ele", "total_energy", "energy"))
		add(commonSemanticRule{CapabilityID: "outlet", CapabilityType: "outlet", PropertyID: "in-use", Name: "正在使用", Type: device.ValueTypeBool, Codes: []string{"in_use"}})
	case device.TypeSwitch:
		addPower("switch", "switch")
	case device.TypeFan:
		addActive("fan", "fan")
		add(measurement("fan", "rotation-speed", "转速", "percent", true, "fan_speed_percent", "speed_percent", "speed_value"))
		add(commonSemanticRule{CapabilityID: "fan", CapabilityType: "fan", PropertyID: "speed-level", Name: "风速档位", Type: device.ValueTypeEnum, Codes: []string{"fan_speed", "speed", "level"}, Writable: true})
		add(commonSemanticRule{CapabilityID: "fan", CapabilityType: "fan", PropertyID: "target-state", Name: "目标模式", Type: device.ValueTypeEnum, Codes: []string{"mode", "work_mode", "fan_mode"}, Writable: true})
		add(commonSemanticRule{CapabilityID: "fan", CapabilityType: "fan", PropertyID: "swing-mode", Name: "摇头", Type: device.ValueTypeBool, Codes: []string{"swing", "switch_horizontal", "shake"}, Writable: true})
	case device.TypeAirPurifier:
		addActive("air-purifier", "air-purifier")
		add(measurement("air-purifier", "rotation-speed", "净化速度", "percent", true, "speed_percent", "fan_speed_percent", "speed_value"))
		add(commonSemanticRule{CapabilityID: "air-purifier", CapabilityType: "air-purifier", PropertyID: "target-state", Name: "目标模式", Type: device.ValueTypeEnum, Codes: []string{"mode", "work_mode", "air_mode"}, Writable: true})
		add(measurement("air-quality", "pm2.5-density", "PM2.5", "microgram-per-cubic-meter", false, "pm25", "pm2_5", "pm25_value"))
		add(measurement("air-quality", "voc-density", "VOC", "microgram-per-cubic-meter", false, "voc", "voc_value"))
	case device.TypeWindowCovering:
		add(measurement("window-covering", "target-position", "目标位置", "percent", true, "percent_control", "position", "position_1", "target_position"))
		add(measurement("window-covering", "current-position", "当前位置", "percent", false, "cur_position", "current_position", "position_current"))
	case device.TypeAirConditioner:
		addActive("air-conditioner", "air-conditioner")
		add(measurement("temperature", "current-temperature", "当前温度", "celsius", false, "temp_current", "va_temperature", "temperature"))
		add(measurement("temperature", "target-temperature", "目标温度", "celsius", true, "temp_set", "temp_set_value", "target_temperature"))
		add(commonSemanticRule{CapabilityID: "air-conditioner", CapabilityType: "air-conditioner", PropertyID: "target-mode", Name: "运行模式", Type: device.ValueTypeEnum, Codes: []string{"mode", "work_mode", "air_mode"}, Writable: true})
		add(commonSemanticRule{CapabilityID: "air-conditioner", CapabilityType: "air-conditioner", PropertyID: "fan-speed", Name: "风速档位", Type: device.ValueTypeEnum, Codes: []string{"fan_speed", "wind_speed", "speed"}, Writable: true})
	case device.TypeTemperatureHumiditySensor:
		add(measurement("temperature", "current-temperature", "当前温度", "celsius", false, "va_temperature", "temp_current", "temperature", "temp_value"))
		add(measurement("humidity", "current-humidity", "当前湿度", "percent", false, "va_humidity", "humidity_value", "humidity"))
	case device.TypeTemperatureSensor:
		add(measurement("temperature", "current-temperature", "当前温度", "celsius", false, "va_temperature", "temp_current", "temperature", "temp_value"))
	case device.TypeHumiditySensor:
		add(measurement("humidity", "current-humidity", "当前湿度", "percent", false, "va_humidity", "humidity_value", "humidity"))
	case device.TypeContactSensor:
		add(commonSemanticRule{CapabilityID: "contact", CapabilityType: "contact-sensor", PropertyID: "contact-detected", Name: "接触状态", Type: device.ValueTypeBool, Codes: []string{"doorcontact", "contact", "door_contact", "switch"}})
	case device.TypeMotionSensor:
		add(commonSemanticRule{CapabilityID: "motion", CapabilityType: "motion-sensor", PropertyID: "motion-detected", Name: "活动状态", Type: device.ValueTypeBool, Codes: []string{"pir", "motion", "presence", "presence_state"}})
		add(measurement("illuminance", "current-illuminance", "当前照度", "lux", false, "illuminance", "illumination", "light_value"))
	case device.TypeIlluminanceSensor:
		add(measurement("illuminance", "current-illuminance", "当前照度", "lux", false, "illuminance", "illumination", "light_value"))
	case device.TypeLeakSensor:
		add(commonSemanticRule{CapabilityID: "leak", CapabilityType: "leak-sensor", PropertyID: "leak-detected", Name: "漏水状态", Type: device.ValueTypeBool, Codes: []string{"watersensor", "water_sensor", "leak", "water_leak"}})
	case device.TypeSmokeSensor:
		add(commonSemanticRule{CapabilityID: "smoke", CapabilityType: "smoke-sensor", PropertyID: "smoke-detected", Name: "烟雾状态", Type: device.ValueTypeBool, Codes: []string{"smoke", "smoke_sensor", "smoke_alarm"}})
	case device.TypeAirQualitySensor:
		add(commonSemanticRule{CapabilityID: "air-quality", CapabilityType: "air-quality-sensor", PropertyID: "current-air-quality", Name: "当前空气质量", Type: device.ValueTypeEnum, Codes: []string{"air_quality", "quality", "air_quality_index"}})
		add(measurement("air-quality", "pm2.5-density", "PM2.5", "microgram-per-cubic-meter", false, "pm25", "pm2_5", "pm25_value"))
		add(measurement("air-quality", "voc-density", "VOC", "microgram-per-cubic-meter", false, "voc", "voc_value"))
	case device.TypeWaterHeater:
		addActive("water-heater", "water-heater")
		add(measurement("temperature", "current-temperature", "当前水温", "celsius", false, "temp_current", "water_temp", "temperature"))
		add(measurement("temperature", "target-temperature", "目标水温", "celsius", true, "temp_set", "target_temperature"))
	case device.TypeHumidifierDehumidifier:
		addActive("humidifier-dehumidifier", "humidifier-dehumidifier")
		add(measurement("humidity", "current-humidity", "当前湿度", "percent", false, "humidity_value", "humidity"))
		add(measurement("humidity", "target-humidity", "目标湿度", "percent", true, "humidity_set", "target_humidity"))
	case device.TypePowerMeter:
		add(measurement("electrical", "current-power", "当前功率", "watt", false, "cur_power", "current_power", "power"))
		add(measurement("electrical", "voltage", "电压", "volt", false, "cur_voltage", "voltage"))
		add(measurement("electrical", "current", "电流", "ampere", false, "cur_current", "current"))
		add(measurement("electrical", "energy", "累计电量", "kilowatt-hour", false, "add_ele", "total_energy", "energy"))
	case device.TypeRobotVacuum:
		addActive("robot-vacuum", "robot-vacuum")
		add(commonSemanticRule{CapabilityID: "robot-vacuum", CapabilityType: "robot-vacuum", PropertyID: "target-mode", Name: "清扫模式", Type: device.ValueTypeEnum, Codes: []string{"clean_mode", "mode", "work_mode"}, Writable: true})
		add(measurement("robot-vacuum", "cleaning-progress", "清扫进度", "percent", false, "cleaning_progress", "cleaning_progress_value"))
		add(measurement("robot-vacuum", "fan-speed", "吸力", "percent", true, "fan_speed", "suction", "suction_level"))
	}
	minimum, maximum, step := 0.0, 100.0, 1.0
	add(commonSemanticRule{CapabilityID: "battery", CapabilityType: "battery", PropertyID: "level", Name: "电池电量", Unit: "percent", Type: device.ValueTypeInt, Codes: []string{"battery_percentage", "battery_value", "battery_level", "battery"}, Min: &minimum, Max: &maximum, Step: &step})
	add(commonSemanticRule{CapabilityID: "battery", CapabilityType: "battery", PropertyID: "low", Name: "低电量", Type: device.ValueTypeBool, Codes: []string{"low_battery", "battery_low"}})
	add(commonSemanticRule{CapabilityID: "security", CapabilityType: "security-status", PropertyID: "tampered", Name: "防拆状态", Type: device.ValueTypeBool, Codes: []string{"tampered", "tamper_alarm"}})
	return rules
}

func commonCapabilityType(capabilityID string) string {
	switch capabilityID {
	case "temperature":
		return "temperature-sensor"
	case "humidity":
		return "humidity-sensor"
	case "illuminance":
		return "illuminance-sensor"
	case "air-quality":
		return "air-quality-sensor"
	case "electrical":
		return "electrical-meter"
	default:
		return capabilityID
	}
}

func findCommonDP(specs map[string]DPSpec, codes []string) (string, DPSpec, bool) {
	for _, code := range codes {
		if spec, ok := specs[code]; ok {
			return code, spec, true
		}
	}
	for code, spec := range specs {
		for _, candidate := range codes {
			if sanitizeDPID(code) == sanitizeDPID(candidate) || strings.EqualFold(code, candidate) {
				return code, spec, true
			}
		}
	}
	return "", DPSpec{}, false
}

func commonDPCode(item TuyaDevice, specs map[string]DPSpec, endpointID, capabilityID, propertyID string) (string, DPSpec, bool) {
	if endpointID != "main" {
		return "", DPSpec{}, false
	}
	for _, rule := range commonSemanticRules(item, specs) {
		if rule.CapabilityID != capabilityID || rule.PropertyID != propertyID {
			continue
		}
		return findCommonDP(specs, rule.Codes)
	}
	return "", DPSpec{}, false
}

func buildCommonCapabilities(item TuyaDevice, specs map[string]DPSpec, statuses map[string]TuyaStatus) []device.Capability {
	rules := commonSemanticRules(item, specs)
	capabilityIndex := make(map[string]int)
	capabilities := make([]device.Capability, 0, len(rules))
	for _, rule := range rules {
		code, spec, ok := findCommonDP(specs, rule.Codes)
		if !ok {
			continue
		}
		definition, value := propertyFromSpec(spec, statuses[code])
		definition.ID = rule.PropertyID
		definition.Name = rule.Name
		definition.Readable = spec.Readable
		definition.Writable = spec.Writable && rule.Writable
		definition.Notifiable = definition.Readable
		if rule.Unit != "" {
			definition.Unit = rule.Unit
		}
		if rule.Min != nil {
			definition.Min = cloneFloat(rule.Min)
		}
		if rule.Max != nil {
			definition.Max = cloneFloat(rule.Max)
		}
		if rule.Step != nil {
			definition.Step = cloneFloat(rule.Step)
		}
		if len(rule.Enum) > 0 {
			definition.Enum = append([]string(nil), rule.Enum...)
		}
		if rule.Type != "" {
			definition.Type, value = coerceCommonValue(rule.Type, value, definition.Enum)
		}
		property := device.Property{Definition: definition, Value: value, StateTransport: device.StateTransportCloudHTTP}
		index, exists := capabilityIndex[rule.CapabilityID]
		if !exists {
			index = len(capabilities)
			capabilityIndex[rule.CapabilityID] = index
			capabilities = append(capabilities, device.Capability{ID: rule.CapabilityID, Type: rule.CapabilityType})
		}
		capabilities[index].Properties = append(capabilities[index].Properties, property)
	}
	return capabilities
}

func coerceCommonValue(want device.ValueType, value device.PropertyValue, enum []string) (device.ValueType, device.PropertyValue) {
	switch want {
	case device.ValueTypeBool:
		if value.Bool != nil {
			return want, device.BoolValue(*value.Bool)
		}
	case device.ValueTypeNumber:
		if number, ok := propertyValueNumber(value); ok {
			return want, device.NumberValue(number)
		}
	case device.ValueTypeInt:
		if number, ok := propertyValueNumber(value); ok {
			return want, device.IntValue(int64(number))
		}
	case device.ValueTypeEnum:
		if value.String != nil {
			if len(enum) == 0 || containsString(enum, *value.String) {
				return want, device.EnumValue(*value.String)
			}
		}
	case device.ValueTypeString:
		if value.String != nil {
			return want, device.StringValue(*value.String)
		}
	}
	return value.Type, value
}

func propertyValueNumber(value device.PropertyValue) (float64, bool) {
	if value.Number != nil {
		return *value.Number, true
	}
	if value.Int != nil {
		return float64(*value.Int), true
	}
	return 0, false
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
