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
	Name       string                `json:"name,omitempty"`
	Version    int                   `json:"version"`
	BuiltIn    bool                  `json:"builtIn"`
	Parameters []ModelParameter      `json:"parameters"`
	Custom     CustomParameterPolicy `json:"custom"`
}

func required(capabilityID, propertyID, name string, valueType ValueType, writable bool) ModelParameter {
	return ModelParameter{Path: ParameterPath{EndpointID: "main", CapabilityID: capabilityID, PropertyID: propertyID}, Name: name, Level: ParameterRequired, Type: valueType, Readable: true, Writable: writable, Notifiable: true, Publisher: ParameterRole{Level: ParameterRequired, Behavior: "must-publish"}, Consumer: ParameterRole{Level: ParameterRequired, Behavior: "must-map"}, PublisherNotes: "必须发布且符合统一类型", ConsumerNotes: "Consumer 声明支持该模型时必须映射"}
}

func requiredMeasurement(capabilityID, propertyID, name, unit string, minimum, maximum, step float64) ModelParameter {
	parameter := required(capabilityID, propertyID, name, ValueTypeNumber, false)
	parameter.Unit, parameter.Min, parameter.Max, parameter.Step = unit, &minimum, &maximum, &step
	return parameter
}

func requiredWritableMeasurement(capabilityID, propertyID, name, unit string, minimum, maximum, step float64) ModelParameter {
	parameter := requiredMeasurement(capabilityID, propertyID, name, unit, minimum, maximum, step)
	parameter.Writable = true
	return parameter
}

func requiredEnum(capabilityID, propertyID, name string, writable bool, values ...string) ModelParameter {
	parameter := required(capabilityID, propertyID, name, ValueTypeEnum, writable)
	parameter.Enum = values
	return parameter
}

func requiredInt(capabilityID, propertyID, name, unit string, writable bool, minimum, maximum, step float64) ModelParameter {
	parameter := required(capabilityID, propertyID, name, ValueTypeInt, writable)
	parameter.Unit, parameter.Min, parameter.Max, parameter.Step = unit, &minimum, &maximum, &step
	return parameter
}

func optional(capabilityID, propertyID, name string, valueType ValueType, unit string, writable bool, enum ...string) ModelParameter {
	return ModelParameter{Path: ParameterPath{EndpointID: "main", CapabilityID: capabilityID, PropertyID: propertyID}, Name: name, Level: ParameterOptional, Type: valueType, Unit: unit, Readable: true, Writable: writable, Notifiable: true, Enum: enum, Publisher: ParameterRole{Level: ParameterOptional, Behavior: "publish-if-supported"}, Consumer: ParameterRole{Level: ParameterOptional, Behavior: "map-if-supported"}, PublisherNotes: "Provider 有能力时发布", ConsumerNotes: "Consumer 支持时映射，缺失可降级"}
}

func optionalEnum(capabilityID, propertyID, name string, writable bool, values ...string) ModelParameter {
	return optional(capabilityID, propertyID, name, ValueTypeEnum, "", writable, values...)
}

func optionalMeasurement(capabilityID, propertyID, name, unit string, writable bool, minimum, maximum, step float64) ModelParameter {
	parameter := optional(capabilityID, propertyID, name, ValueTypeNumber, unit, writable)
	parameter.Min, parameter.Max, parameter.Step = &minimum, &maximum, &step
	return parameter
}

func optionalInt(capabilityID, propertyID, name, unit string, writable bool, minimum, maximum, step float64) ModelParameter {
	parameter := optional(capabilityID, propertyID, name, ValueTypeInt, unit, writable)
	parameter.Min, parameter.Max, parameter.Step = &minimum, &maximum, &step
	return parameter
}

func withCustomPolicy(contract ModelContract) ModelContract {
	contract.Custom = CustomParameterPolicy{
		Publisher: ParameterRole{Level: ParameterCustom, Behavior: "preserve-and-mark-custom"},
		Consumer:  ParameterRole{Level: ParameterCustom, Behavior: "explicit-path-mapping-only"},
	}
	return contract
}

func batteryLevelParameters() []ModelParameter {
	return []ModelParameter{
		optionalInt("battery", "level", "电池电量", "percent", false, 0, 100, 1),
		optional("battery", "low", "低电量", ValueTypeBool, "", false),
		optional("battery", "charging", "正在充电", ValueTypeBool, "", false),
	}
}

func batteryParameters() []ModelParameter {
	return append(batteryLevelParameters(),
		optional("security", "tampered", "防拆状态", ValueTypeBool, "", false),
	)
}

func sensorHealthParameters() []ModelParameter {
	return append(batteryParameters(),
		optional("status", "active", "传感器有效", ValueTypeBool, "", false),
		optional("status", "fault", "传感器故障", ValueTypeBool, "", false),
	)
}

func electricalParameters() []ModelParameter {
	return []ModelParameter{
		optionalMeasurement("electrical", "current-power", "当前功率", "watt", false, 0, 100000, 0.1),
		optionalMeasurement("electrical", "voltage", "电压", "volt", false, 0, 1000, 0.1),
		optionalMeasurement("electrical", "current", "电流", "ampere", false, 0, 1000, 0.001),
		optionalMeasurement("electrical", "frequency", "频率", "hertz", false, 0, 1000, 0.1),
		optionalMeasurement("electrical", "power-factor", "功率因数", "ratio", false, 0, 1, 0.01),
		optionalMeasurement("electrical", "energy", "累计电量", "kilowatt-hour", false, 0, 1000000000, 0.001),
	}
}

func filterParameters() []ModelParameter {
	return []ModelParameter{
		optionalMeasurement("filter", "life-level", "滤芯寿命", "percent", false, 0, 100, 1),
		optional("filter", "change-indication", "需要更换滤芯", ValueTypeBool, "", false),
		optionalInt("filter", "used-time", "滤芯已使用时长", "hour", false, 0, 1000000, 1),
		optionalInt("filter", "remaining-time", "滤芯剩余时长", "hour", false, 0, 1000000, 1),
	}
}

var modelContracts = map[Type]ModelContract{
	TypeSwitch: {DeviceType: TypeSwitch, Name: "开关", Version: 2, Parameters: []ModelParameter{
		required("switch", "power", "开关", ValueTypeBool, true),
		optional("switch", "in-use", "正在使用", ValueTypeBool, "", false),
		optional("switch", "lock-physical-controls", "物理控制锁", ValueTypeBool, "", true),
		optional("status", "fault", "设备故障", ValueTypeBool, "", false),
	}},
	TypeLightbulb: {DeviceType: TypeLightbulb, Name: "灯泡", Version: 2, Parameters: []ModelParameter{
		required("switch", "power", "开关", ValueTypeBool, true),
		optionalMeasurement("light", "brightness", "亮度", "percent", true, 0, 100, 1),
		optionalInt("light", "color-temperature", "色温", "mired", true, 50, 1000, 1),
		optionalMeasurement("light", "hue", "色相", "degree", true, 0, 360, 0.1),
		optionalMeasurement("light", "saturation", "饱和度", "percent", true, 0, 100, 0.1),
		optionalEnum("light", "color-mode", "颜色模式", false, "brightness", "color-temperature", "color"),
		optionalEnum("light", "effect", "灯效", true, "none", "reading", "night", "color-loop", "custom"),
		optional("light", "adaptive-lighting", "自适应照明", ValueTypeBool, "", true),
		optional("status", "fault", "灯具故障", ValueTypeBool, "", false),
	}},
	TypeOutlet: {DeviceType: TypeOutlet, Name: "插座", Version: 2, Parameters: append([]ModelParameter{
		required("switch", "power", "开关", ValueTypeBool, true),
		optional("outlet", "in-use", "正在使用", ValueTypeBool, "", false),
		optional("outlet", "overload", "过载", ValueTypeBool, "", false),
		optional("outlet", "lock-physical-controls", "物理控制锁", ValueTypeBool, "", true),
	}, electricalParameters()...)},
	TypeTemperatureSensor: {DeviceType: TypeTemperatureSensor, Name: "温度传感器", Version: 1, Parameters: append([]ModelParameter{
		requiredMeasurement("temperature", "current-temperature", "当前温度", "celsius", -100, 200, 0.1),
		optionalMeasurement("temperature", "minimum-temperature", "最低温度", "celsius", false, -100, 200, 0.1),
		optionalMeasurement("temperature", "maximum-temperature", "最高温度", "celsius", false, -100, 200, 0.1),
	}, sensorHealthParameters()...)},
	TypeHumiditySensor: {DeviceType: TypeHumiditySensor, Name: "湿度传感器", Version: 1, Parameters: append([]ModelParameter{
		requiredMeasurement("humidity", "current-humidity", "当前湿度", "percent", 0, 100, 0.1),
		optionalMeasurement("humidity", "minimum-humidity", "最低湿度", "percent", false, 0, 100, 0.1),
		optionalMeasurement("humidity", "maximum-humidity", "最高湿度", "percent", false, 0, 100, 0.1),
	}, sensorHealthParameters()...)},
	TypeTemperatureHumiditySensor: {DeviceType: TypeTemperatureHumiditySensor, Name: "温湿度传感器", Version: 2, Parameters: append([]ModelParameter{
		requiredMeasurement("temperature", "current-temperature", "当前温度", "celsius", -100, 200, 0.1),
		requiredMeasurement("humidity", "current-humidity", "当前湿度", "percent", 0, 100, 0.1),
		optionalMeasurement("temperature", "dew-point", "露点温度", "celsius", false, -100, 100, 0.1),
		optionalMeasurement("humidity", "absolute-humidity", "绝对湿度", "gram-per-cubic-meter", false, 0, 1000, 0.1),
	}, sensorHealthParameters()...)},
	TypePressureSensor: {DeviceType: TypePressureSensor, Name: "气压传感器", Version: 1, Parameters: append([]ModelParameter{
		requiredMeasurement("pressure", "current-pressure", "当前气压", "hectopascal", 0, 2000, 0.1),
		optionalEnum("pressure", "trend", "气压趋势", false, "falling", "steady", "rising"),
	}, sensorHealthParameters()...)},
	TypeNoiseSensor: {DeviceType: TypeNoiseSensor, Name: "噪声传感器", Version: 1, Parameters: append([]ModelParameter{
		requiredMeasurement("noise", "current-level", "当前声级", "decibel", 0, 200, 0.1),
		optionalMeasurement("noise", "peak-level", "峰值声级", "decibel", false, 0, 200, 0.1),
	}, sensorHealthParameters()...)},
	TypeWaterLevelSensor: {DeviceType: TypeWaterLevelSensor, Name: "水位传感器", Version: 1, Parameters: append([]ModelParameter{
		requiredMeasurement("water-level", "current-level", "当前水位", "percent", 0, 100, 0.1),
		optional("water-level", "low", "低水位", ValueTypeBool, "", false),
		optional("water-level", "overflow", "溢水", ValueTypeBool, "", false),
	}, sensorHealthParameters()...)},
	TypeSoilMoistureSensor: {DeviceType: TypeSoilMoistureSensor, Name: "土壤湿度传感器", Version: 1, Parameters: append([]ModelParameter{
		requiredMeasurement("soil-moisture", "current-moisture", "当前土壤湿度", "percent", 0, 100, 0.1),
		optionalMeasurement("soil-moisture", "conductivity", "土壤电导率", "microsiemens-per-centimeter", false, 0, 100000, 1),
		optionalMeasurement("temperature", "current-temperature", "土壤温度", "celsius", false, -100, 200, 0.1),
	}, sensorHealthParameters()...)},
	TypeContactSensor: {DeviceType: TypeContactSensor, Name: "接触传感器", Version: 2, Parameters: append([]ModelParameter{
		required("contact", "contact-detected", "接触状态", ValueTypeBool, false),
		optionalInt("contact", "open-duration", "持续打开时长", "second", false, 0, 31536000, 1),
		optionalInt("contact", "event-count", "触发次数", "count", false, 0, 1000000000, 1),
	}, sensorHealthParameters()...)},
	TypeMotionSensor: {DeviceType: TypeMotionSensor, Name: "活动传感器", Version: 2, Parameters: append([]ModelParameter{
		required("motion", "motion-detected", "活动状态", ValueTypeBool, false),
		optionalEnum("motion", "sensitivity", "灵敏度", true, "low", "medium", "high"),
		optionalInt("motion", "clear-after", "无人延迟", "second", true, 0, 86400, 1),
		optionalMeasurement("illuminance", "current-illuminance", "当前照度", "lux", false, 0, 100000, 0.1),
	}, sensorHealthParameters()...)},
	TypeFan: {DeviceType: TypeFan, Name: "风扇", Version: 2, Parameters: []ModelParameter{
		required("fan", "active", "启用", ValueTypeBool, true),
		requiredEnum("fan", "current-state", "当前状态", false, "inactive", "idle", "blowing-air"),
		optional("fan", "target-state", "目标模式", ValueTypeEnum, "", true, "manual", "auto"),
		optionalMeasurement("fan", "rotation-speed", "转速", "percent", true, 0, 100, 1),
		optionalEnum("fan", "speed-level", "风速档位", true, "auto", "low", "medium", "high", "turbo"),
		optional("fan", "swing-mode", "摇头", ValueTypeBool, "", true),
		optional("fan", "rotation-direction", "旋转方向", ValueTypeEnum, "", true, "clockwise", "counter-clockwise"),
		optional("fan", "lock-physical-controls", "物理控制锁", ValueTypeBool, "", true),
		optionalInt("fan", "set-duration", "定时关闭", "second", true, 0, 86400, 1),
		optionalInt("fan", "remaining-duration", "剩余时长", "second", false, 0, 86400, 1),
		optional("status", "fault", "设备故障", ValueTypeBool, "", false),
	}},
	TypeAirPurifier: {DeviceType: TypeAirPurifier, Name: "空气净化器", Version: 2, Parameters: append([]ModelParameter{
		required("air-purifier", "active", "启用", ValueTypeBool, true),
		requiredEnum("air-purifier", "current-state", "当前状态", false, "inactive", "idle", "purifying-air"),
		optional("air-purifier", "target-state", "目标模式", ValueTypeEnum, "", true, "manual", "auto"),
		optionalMeasurement("air-purifier", "rotation-speed", "净化速度", "percent", true, 0, 100, 1),
		optional("air-purifier", "swing-mode", "摆风", ValueTypeBool, "", true),
		optional("air-purifier", "lock-physical-controls", "物理控制锁", ValueTypeBool, "", true),
		optional("air-quality", "current-air-quality", "空气质量", ValueTypeEnum, "", false, "unknown", "excellent", "good", "fair", "inferior", "poor"),
		optionalMeasurement("air-quality", "pm2.5-density", "PM2.5", "microgram-per-cubic-meter", false, 0, 10000, 0.1),
		optionalMeasurement("air-quality", "pm10-density", "PM10", "microgram-per-cubic-meter", false, 0, 10000, 0.1),
		optionalMeasurement("air-quality", "voc-density", "VOC", "microgram-per-cubic-meter", false, 0, 100000, 0.1),
		optionalMeasurement("air-quality", "carbon-dioxide-level", "二氧化碳浓度", "ppm", false, 0, 100000, 1),
		optionalMeasurement("temperature", "current-temperature", "当前温度", "celsius", false, -100, 200, 0.1),
		optionalMeasurement("humidity", "current-humidity", "当前湿度", "percent", false, 0, 100, 0.1),
		optionalInt("air-purifier", "set-duration", "定时关闭", "second", true, 0, 86400, 1),
		optionalInt("air-purifier", "remaining-duration", "剩余时长", "second", false, 0, 86400, 1),
		optional("status", "fault", "设备故障", ValueTypeBool, "", false),
	}, filterParameters()...)},
	TypeWindowCovering: {DeviceType: TypeWindowCovering, Name: "窗帘", Version: 2, Parameters: []ModelParameter{
		requiredInt("window-covering", "current-position", "当前位置", "percent", false, 0, 100, 1),
		requiredInt("window-covering", "target-position", "目标位置", "percent", true, 0, 100, 1),
		requiredEnum("window-covering", "position-state", "运动状态", false, "decreasing", "increasing", "stopped"),
		optional("window-covering", "obstruction-detected", "障碍物检测", ValueTypeBool, "", false),
		optionalInt("window-covering", "current-horizontal-tilt", "当前水平倾角", "degree", false, -90, 90, 1),
		optionalInt("window-covering", "target-horizontal-tilt", "目标水平倾角", "degree", true, -90, 90, 1),
		optionalInt("window-covering", "current-vertical-tilt", "当前垂直倾角", "degree", false, -90, 90, 1),
		optionalInt("window-covering", "target-vertical-tilt", "目标垂直倾角", "degree", true, -90, 90, 1),
		optional("window-covering", "hold-position", "暂停运动", ValueTypeBool, "", true),
		optional("status", "fault", "设备故障", ValueTypeBool, "", false),
	}},
	TypeIlluminanceSensor: {DeviceType: TypeIlluminanceSensor, Name: "照度传感器", Version: 2, Parameters: append([]ModelParameter{
		requiredMeasurement("illuminance", "current-illuminance", "当前照度", "lux", 0, 100000, 0.1),
		optionalMeasurement("illuminance", "minimum-illuminance", "最低照度", "lux", false, 0, 100000, 0.1),
		optionalMeasurement("illuminance", "maximum-illuminance", "最高照度", "lux", false, 0, 100000, 0.1),
	}, sensorHealthParameters()...)},
	TypeOccupancySensor: {DeviceType: TypeOccupancySensor, Name: "占用传感器", Version: 2, Parameters: append([]ModelParameter{
		required("occupancy", "occupancy-detected", "占用状态", ValueTypeBool, false),
		optionalInt("occupancy", "person-count", "人数", "count", false, 0, 1000, 1),
		optionalEnum("occupancy", "sensitivity", "灵敏度", true, "low", "medium", "high"),
		optionalInt("occupancy", "clear-after", "无人延迟", "second", true, 0, 86400, 1),
	}, sensorHealthParameters()...)},
	TypeLeakSensor: {DeviceType: TypeLeakSensor, Name: "漏水传感器", Version: 2, Parameters: append([]ModelParameter{
		required("leak", "leak-detected", "漏水状态", ValueTypeBool, false),
		optionalMeasurement("water-level", "current-level", "检测水位", "millimeter", false, 0, 100000, 0.1),
	}, sensorHealthParameters()...)},
	TypeSmokeSensor: {DeviceType: TypeSmokeSensor, Name: "烟雾传感器", Version: 2, Parameters: append([]ModelParameter{
		required("smoke", "smoke-detected", "烟雾状态", ValueTypeBool, false),
		optionalMeasurement("smoke", "density", "烟雾浓度", "percent", false, 0, 100, 0.1),
	}, sensorHealthParameters()...)},
	TypeCarbonMonoxideSensor: {DeviceType: TypeCarbonMonoxideSensor, Name: "一氧化碳传感器", Version: 2, Parameters: append([]ModelParameter{
		required("carbon-monoxide", "detected", "一氧化碳告警", ValueTypeBool, false),
		optionalMeasurement("carbon-monoxide", "current-level", "当前浓度", "ppm", false, 0, 10000, 0.1),
		optionalMeasurement("carbon-monoxide", "peak-level", "峰值浓度", "ppm", false, 0, 10000, 0.1),
	}, sensorHealthParameters()...)},
	TypeCarbonDioxideSensor: {DeviceType: TypeCarbonDioxideSensor, Name: "二氧化碳传感器", Version: 2, Parameters: append([]ModelParameter{
		required("carbon-dioxide", "detected", "二氧化碳告警", ValueTypeBool, false),
		optionalMeasurement("carbon-dioxide", "current-level", "当前浓度", "ppm", false, 0, 100000, 1),
		optionalMeasurement("carbon-dioxide", "peak-level", "峰值浓度", "ppm", false, 0, 100000, 1),
	}, sensorHealthParameters()...)},
	TypeAirQualitySensor: {DeviceType: TypeAirQualitySensor, Name: "空气质量传感器", Version: 2, Parameters: append([]ModelParameter{
		requiredEnum("air-quality", "current-air-quality", "当前空气质量", false, "unknown", "excellent", "good", "fair", "inferior", "poor"),
		optionalMeasurement("air-quality", "index", "空气质量指数", "aqi", false, 0, 1000, 1),
		optionalMeasurement("air-quality", "pm2.5-density", "PM2.5", "microgram-per-cubic-meter", false, 0, 10000, 0.1),
		optionalMeasurement("air-quality", "pm10-density", "PM10", "microgram-per-cubic-meter", false, 0, 10000, 0.1),
		optionalMeasurement("air-quality", "voc-density", "VOC", "microgram-per-cubic-meter", false, 0, 100000, 0.1),
		optionalMeasurement("air-quality", "carbon-monoxide-level", "一氧化碳浓度", "ppm", false, 0, 10000, 0.1),
		optionalMeasurement("air-quality", "carbon-dioxide-level", "二氧化碳浓度", "ppm", false, 0, 100000, 1),
		optionalMeasurement("air-quality", "nitrogen-dioxide-density", "二氧化氮浓度", "microgram-per-cubic-meter", false, 0, 10000, 0.1),
		optionalMeasurement("air-quality", "ozone-density", "臭氧浓度", "microgram-per-cubic-meter", false, 0, 10000, 0.1),
		optionalMeasurement("temperature", "current-temperature", "当前温度", "celsius", false, -100, 200, 0.1),
		optionalMeasurement("humidity", "current-humidity", "当前湿度", "percent", false, 0, 100, 0.1),
	}, sensorHealthParameters()...)},
	TypeThermostat: {DeviceType: TypeThermostat, Name: "恒温器", Version: 2, Parameters: []ModelParameter{
		requiredEnum("thermostat", "current-state", "当前工作状态", false, "off", "heating", "cooling", "idle"),
		requiredEnum("thermostat", "target-mode", "目标模式", true, "off", "heat", "cool", "auto"),
		requiredMeasurement("temperature", "current-temperature", "当前温度", "celsius", -100, 200, 0.1),
		requiredWritableMeasurement("temperature", "target-temperature", "目标温度", "celsius", 5, 35, 0.1),
		optionalMeasurement("temperature", "heating-threshold", "制热阈值", "celsius", true, 5, 35, 0.1),
		optionalMeasurement("temperature", "cooling-threshold", "制冷阈值", "celsius", true, 5, 35, 0.1),
		optionalMeasurement("humidity", "current-humidity", "当前湿度", "percent", false, 0, 100, 0.1),
		optional("thermostat", "display-units", "显示温标", ValueTypeEnum, "", true, "celsius", "fahrenheit"),
		optionalEnum("thermostat", "fan-mode", "风机模式", true, "auto", "on", "circulate"),
		optional("thermostat", "hold", "温度保持", ValueTypeBool, "", true),
		optional("thermostat", "lock-physical-controls", "物理控制锁", ValueTypeBool, "", true),
		optional("status", "fault", "设备故障", ValueTypeBool, "", false),
	}},
	TypeAirConditioner: {DeviceType: TypeAirConditioner, Name: "空调", Version: 3, Parameters: append([]ModelParameter{
		required("air-conditioner", "active", "启用", ValueTypeBool, true),
		optionalEnum("air-conditioner", "current-state", "当前工作状态", false, "off", "idle", "cooling", "heating", "drying", "fan-only"),
		requiredEnum("air-conditioner", "target-mode", "运行模式", true, "off", "auto", "cool", "heat", "dry", "fan"),
		optionalMeasurement("temperature", "current-temperature", "当前温度", "celsius", false, -100, 200, 0.1),
		requiredWritableMeasurement("temperature", "target-temperature", "目标温度", "celsius", 16, 32, 0.5),
		optionalEnum("air-conditioner", "fan-speed", "风速档位", true, "auto", "low", "medium", "high", "turbo"),
		optionalMeasurement("air-conditioner", "rotation-speed", "风速百分比", "percent", true, 0, 100, 1),
		optional("air-conditioner", "vertical-swing", "上下扫风", ValueTypeBool, "", true),
		optional("air-conditioner", "horizontal-swing", "左右扫风", ValueTypeBool, "", true),
		optionalEnum("air-conditioner", "wind-direction", "导风方向", true, "auto", "up", "middle", "down"),
		optional("air-conditioner", "auxiliary-heat", "辅热", ValueTypeBool, "", true),
		optional("air-conditioner", "sleep-mode", "睡眠模式", ValueTypeBool, "", true),
		optional("air-conditioner", "eco-mode", "节能模式", ValueTypeBool, "", true),
		optional("air-conditioner", "display-enabled", "面板显示", ValueTypeBool, "", true),
		optionalMeasurement("humidity", "current-humidity", "当前湿度", "percent", false, 0, 100, 0.1),
		optional("air-conditioner", "display-units", "显示温标", ValueTypeEnum, "", true, "celsius", "fahrenheit"),
		optional("air-conditioner", "fault", "故障代码", ValueTypeString, "", false),
		optionalInt("air-conditioner", "set-duration", "定时关闭", "second", true, 0, 86400, 1),
		optionalInt("air-conditioner", "remaining-duration", "剩余时长", "second", false, 0, 86400, 1),
	}, filterParameters()...)},
	TypeHeaterCooler: {DeviceType: TypeHeaterCooler, Name: "冷暖设备", Version: 2, Parameters: []ModelParameter{
		required("heater-cooler", "active", "启用", ValueTypeBool, true),
		requiredEnum("heater-cooler", "current-state", "当前工作状态", false, "inactive", "idle", "heating", "cooling"),
		requiredEnum("heater-cooler", "target-state", "目标模式", true, "auto", "heat", "cool"),
		requiredMeasurement("temperature", "current-temperature", "当前温度", "celsius", -100, 200, 0.1),
		optionalMeasurement("temperature", "heating-threshold", "制热阈值", "celsius", true, 5, 35, 0.1),
		optionalMeasurement("temperature", "cooling-threshold", "制冷阈值", "celsius", true, 5, 35, 0.1),
		optionalMeasurement("heater-cooler", "rotation-speed", "风速", "percent", true, 0, 100, 1),
		optional("heater-cooler", "swing-mode", "摆风", ValueTypeBool, "", true),
		optional("heater-cooler", "lock-physical-controls", "物理控制锁", ValueTypeBool, "", true),
		optionalInt("heater-cooler", "set-duration", "定时关闭", "second", true, 0, 86400, 1),
		optionalInt("heater-cooler", "remaining-duration", "剩余时长", "second", false, 0, 86400, 1),
		optional("status", "fault", "设备故障", ValueTypeBool, "", false),
	}},
	TypeHumidifierDehumidifier: {DeviceType: TypeHumidifierDehumidifier, Name: "加湿除湿器", Version: 2, Parameters: append([]ModelParameter{
		required("humidifier-dehumidifier", "active", "启用", ValueTypeBool, true),
		requiredEnum("humidifier-dehumidifier", "current-state", "当前工作状态", false, "inactive", "idle", "humidifying", "dehumidifying"),
		requiredEnum("humidifier-dehumidifier", "target-state", "目标模式", true, "auto", "humidify", "dehumidify"),
		requiredMeasurement("humidity", "current-humidity", "当前湿度", "percent", 0, 100, 0.1),
		requiredWritableMeasurement("humidity", "target-humidity", "目标湿度", "percent", 0, 100, 1),
		optionalMeasurement("humidifier-dehumidifier", "water-level", "水位", "percent", false, 0, 100, 1),
		optional("humidifier-dehumidifier", "water-shortage", "缺水", ValueTypeBool, "", false),
		optional("humidifier-dehumidifier", "lock-physical-controls", "物理控制锁", ValueTypeBool, "", true),
		optionalMeasurement("humidifier-dehumidifier", "rotation-speed", "风速", "percent", true, 0, 100, 1),
		optionalInt("humidifier-dehumidifier", "set-duration", "定时关闭", "second", true, 0, 86400, 1),
		optional("status", "fault", "设备故障", ValueTypeBool, "", false),
	}, filterParameters()...)},
	TypeLock: {DeviceType: TypeLock, Name: "门锁", Version: 2, Parameters: append([]ModelParameter{
		requiredEnum("lock", "current-state", "当前锁定状态", false, "unsecured", "secured", "jammed", "unknown"),
		requiredEnum("lock", "target-state", "目标锁定状态", true, "unsecured", "secured"),
		optional("lock", "jammed", "锁舌卡住", ValueTypeBool, "", false),
		optional("lock", "door-open", "门已打开", ValueTypeBool, "", false),
		optionalEnum("lock", "last-operation", "最近操作", false, "lock", "unlock", "auto-lock", "key", "inside", "unknown"),
		optional("lock", "lock-physical-controls", "物理控制锁", ValueTypeBool, "", true),
	}, sensorHealthParameters()...)},
	TypeGarageDoor: {DeviceType: TypeGarageDoor, Name: "车库门", Version: 2, Parameters: []ModelParameter{
		requiredEnum("garage-door", "current-state", "当前门状态", false, "open", "closed", "opening", "closing", "stopped", "unknown"),
		requiredEnum("garage-door", "target-state", "目标门状态", true, "open", "closed"),
		optional("garage-door", "obstruction-detected", "障碍物检测", ValueTypeBool, "", false),
		optional("garage-door", "lock-current-state", "当前锁定状态", ValueTypeEnum, "", false, "unsecured", "secured", "jammed", "unknown"),
		optional("garage-door", "lock-target-state", "目标锁定状态", ValueTypeEnum, "", true, "unsecured", "secured"),
		optional("status", "fault", "设备故障", ValueTypeBool, "", false),
	}},
	TypeSecuritySystem: {DeviceType: TypeSecuritySystem, Name: "安防系统", Version: 2, Parameters: []ModelParameter{
		requiredEnum("security-system", "current-state", "当前布防状态", false, "stay-arm", "away-arm", "night-arm", "disarmed", "triggered"),
		requiredEnum("security-system", "target-state", "目标布防状态", true, "stay-arm", "away-arm", "night-arm", "disarmed"),
		optional("security-system", "alarm-type", "告警类型", ValueTypeEnum, "", false, "none", "unknown", "burglar", "fire", "water", "panic"),
		optional("security-system", "siren-active", "警号启用", ValueTypeBool, "", true),
		optionalInt("security-system", "exit-delay", "离家布防延迟", "second", true, 0, 3600, 1),
		optionalInt("security-system", "entry-delay", "进入撤防延迟", "second", true, 0, 3600, 1),
		optional("security", "tampered", "防拆状态", ValueTypeBool, "", false),
		optional("status", "fault", "系统故障", ValueTypeBool, "", false),
	}},
	TypeValve: {DeviceType: TypeValve, Name: "阀门", Version: 2, Parameters: []ModelParameter{
		required("valve", "active", "启用", ValueTypeBool, true),
		required("valve", "in-use", "正在使用", ValueTypeBool, false),
		requiredEnum("valve", "valve-type", "阀门类型", false, "generic", "irrigation", "shower", "faucet"),
		optionalInt("valve", "set-duration", "设定时长", "second", true, 0, 86400, 1),
		optionalInt("valve", "remaining-duration", "剩余时长", "second", false, 0, 86400, 1),
		optionalMeasurement("valve", "position", "阀门开度", "percent", true, 0, 100, 1),
		optionalMeasurement("flow", "current-flow", "当前流量", "liter-per-minute", false, 0, 100000, 0.1),
		optionalMeasurement("flow", "total-volume", "累计流量", "liter", false, 0, 1000000000, 0.1),
		optional("status", "fault", "阀门故障", ValueTypeBool, "", false),
	}},
	TypePump: {DeviceType: TypePump, Name: "水泵", Version: 1, Parameters: []ModelParameter{
		required("pump", "active", "启用", ValueTypeBool, true),
		requiredEnum("pump", "current-state", "当前状态", false, "inactive", "starting", "running", "stopping", "fault"),
		optionalMeasurement("pump", "speed", "转速", "percent", true, 0, 100, 1),
		optionalMeasurement("flow", "current-flow", "当前流量", "liter-per-minute", false, 0, 100000, 0.1),
		optionalMeasurement("pressure", "current-pressure", "当前压力", "kilopascal", false, 0, 100000, 0.1),
		optionalInt("pump", "set-duration", "设定时长", "second", true, 0, 86400, 1),
		optionalInt("pump", "remaining-duration", "剩余时长", "second", false, 0, 86400, 1),
		optional("status", "fault", "水泵故障", ValueTypeBool, "", false),
	}},
	TypeWaterHeater: {DeviceType: TypeWaterHeater, Name: "热水器", Version: 1, Parameters: []ModelParameter{
		required("water-heater", "active", "启用", ValueTypeBool, true),
		requiredEnum("water-heater", "current-state", "当前状态", false, "inactive", "idle", "heating", "keeping-warm", "fault"),
		requiredMeasurement("temperature", "current-temperature", "当前水温", "celsius", 0, 100, 0.1),
		requiredWritableMeasurement("temperature", "target-temperature", "目标水温", "celsius", 5, 100, 0.5),
		optionalEnum("water-heater", "mode", "运行模式", true, "standard", "eco", "boost", "vacation"),
		optionalMeasurement("water-heater", "water-level", "水量", "percent", false, 0, 100, 1),
		optionalInt("water-heater", "remaining-time", "剩余加热时间", "second", false, 0, 86400, 1),
		optional("water-heater", "lock-physical-controls", "物理控制锁", ValueTypeBool, "", true),
		optional("status", "fault", "设备故障", ValueTypeBool, "", false),
	}},
	TypePowerMeter: {DeviceType: TypePowerMeter, Name: "电力计量器", Version: 1, Parameters: append([]ModelParameter{
		requiredMeasurement("electrical", "current-power", "当前功率", "watt", 0, 1000000000, 0.1),
		optionalEnum("electrical", "power-flow", "功率方向", false, "importing", "exporting", "idle"),
		optionalMeasurement("electrical", "reactive-power", "无功功率", "var", false, -1000000000, 1000000000, 0.1),
		optionalMeasurement("electrical", "apparent-power", "视在功率", "volt-ampere", false, 0, 1000000000, 0.1),
	}, append(electricalParameters()[1:], sensorHealthParameters()...)...)},
	TypeEVCharger: {DeviceType: TypeEVCharger, Name: "电动汽车充电桩", Version: 1, Parameters: append([]ModelParameter{
		required("ev-charger", "active", "允许充电", ValueTypeBool, true),
		requiredEnum("ev-charger", "current-state", "当前状态", false, "disconnected", "connected", "charging", "paused", "complete", "fault"),
		optionalMeasurement("ev-charger", "target-current", "目标电流", "ampere", true, 0, 1000, 0.1),
		optionalMeasurement("ev-charger", "session-energy", "本次充电量", "kilowatt-hour", false, 0, 1000000, 0.001),
		optionalInt("ev-charger", "session-duration", "本次充电时长", "second", false, 0, 31536000, 1),
		optionalInt("ev-charger", "remaining-duration", "预计剩余时长", "second", false, 0, 31536000, 1),
		optional("ev-charger", "cable-locked", "充电枪锁定", ValueTypeBool, "", false),
		optional("status", "fault", "充电故障", ValueTypeBool, "", false),
	}, electricalParameters()...)},
	TypeSpeaker: {DeviceType: TypeSpeaker, Name: "扬声器", Version: 2, Parameters: []ModelParameter{
		required("speaker", "active", "启用", ValueTypeBool, true),
		requiredWritableMeasurement("speaker", "volume", "音量", "percent", 0, 100, 1),
		required("speaker", "mute", "静音", ValueTypeBool, true),
		optionalEnum("speaker", "current-media-state", "当前媒体状态", false, "playing", "paused", "stopped", "loading", "interrupted"),
		optionalEnum("speaker", "target-media-state", "目标媒体状态", true, "play", "pause", "stop"),
		optional("speaker", "input-source", "输入源", ValueTypeString, "", true),
		optional("speaker", "title", "媒体标题", ValueTypeString, "", false),
		optional("speaker", "artist", "艺术家", ValueTypeString, "", false),
		optionalInt("speaker", "position", "播放位置", "second", true, 0, 31536000, 1),
		optionalInt("speaker", "duration", "媒体时长", "second", false, 0, 31536000, 1),
		optional("speaker", "shuffle", "随机播放", ValueTypeBool, "", true),
		optionalEnum("speaker", "repeat", "循环模式", true, "off", "one", "all"),
	}},
	TypeTelevision: {DeviceType: TypeTelevision, Name: "电视", Version: 2, Parameters: []ModelParameter{
		required("television", "active", "电源", ValueTypeBool, true),
		requiredWritableMeasurement("television", "volume", "音量", "percent", 0, 100, 1),
		requiredEnum("television", "target-media-state", "目标媒体状态", true, "play", "pause", "stop"),
		requiredEnum("television", "remote-key", "遥控按键", true, "select", "up", "down", "left", "right", "exit", "back", "info", "play", "pause", "stop", "rewind", "fast-forward", "next", "previous", "power", "volume-up", "volume-down", "mute"),
		optionalEnum("television", "current-media-state", "当前媒体状态", false, "playing", "paused", "stopped", "loading", "interrupted"),
	}},
	TypeCamera: {DeviceType: TypeCamera, Name: "摄像头", Version: 2, Parameters: append([]ModelParameter{
		optional("camera", "privacy", "隐私模式", ValueTypeBool, "", true),
		optional("camera", "indicator", "状态指示灯", ValueTypeBool, "", true),
		optionalEnum("camera", "night-vision", "夜视模式", true, "off", "on", "auto"),
		optional("camera", "motion-detection", "移动侦测", ValueTypeBool, "", true),
		optional("camera", "motion-detected", "检测到移动", ValueTypeBool, "", false),
		optional("media", "live-stream", "实时视频", ValueTypeBool, "", false),
		optional("media", "snapshot", "快照", ValueTypeBool, "", false),
		optional("media", "microphone", "麦克风", ValueTypeBool, "", false),
		optional("media", "talkback", "双向语音", ValueTypeBool, "", false),
		optionalEnum("ptz", "movement", "云台移动", true, "stop", "up", "down", "left", "right", "up-left", "up-right", "down-left", "down-right"),
		optionalInt("ptz", "movement-speed", "云台速度", "percent", true, 1, 100, 1),
		optionalMeasurement("ptz", "pan-position", "水平位置", "degree", true, -360, 360, 0.1),
		optionalMeasurement("ptz", "tilt-position", "垂直位置", "degree", true, -180, 180, 0.1),
		optionalMeasurement("ptz", "zoom-level", "变焦倍率", "percent", true, 0, 100, 1),
		optional("ptz", "current-preset", "当前记忆点", ValueTypeString, "", false),
		optional("ptz", "target-preset", "目标记忆点", ValueTypeString, "", true),
		optionalInt("ptz", "preset-count", "记忆点数量", "count", false, 0, 1000, 1),
	}, batteryLevelParameters()...)},
	TypeRobotVacuum: {DeviceType: TypeRobotVacuum, Name: "扫地机器人", Version: 2, Parameters: append([]ModelParameter{
		required("robot-vacuum", "active", "启用", ValueTypeBool, true),
		requiredEnum("robot-vacuum", "current-state", "当前工作状态", false, "idle", "cleaning", "paused", "returning", "charging", "error"),
		requiredEnum("robot-vacuum", "target-mode", "目标模式", true, "vacuum", "mop", "vacuum-and-mop", "spot"),
		optionalMeasurement("robot-vacuum", "cleaning-progress", "清洁进度", "percent", false, 0, 100, 1),
		optionalMeasurement("robot-vacuum", "fan-speed", "吸力", "percent", true, 0, 100, 1),
		optional("robot-vacuum", "fault", "故障代码", ValueTypeString, "", false),
		optional("battery", "charging", "正在充电", ValueTypeBool, "", false),
		optionalInt("battery", "level", "电池电量", "percent", false, 0, 100, 1),
		optional("battery", "low", "低电量", ValueTypeBool, "", false),
		optionalMeasurement("robot-vacuum", "cleaned-area", "已清洁面积", "square-meter", false, 0, 100000, 0.1),
		optionalInt("robot-vacuum", "cleaning-time", "清洁时长", "second", false, 0, 31536000, 1),
		optionalMeasurement("robot-vacuum", "water-level", "拖地水量", "percent", true, 0, 100, 1),
		optional("robot-vacuum", "mop-attached", "拖布已安装", ValueTypeBool, "", false),
		optional("robot-vacuum", "dust-bin-present", "尘盒已安装", ValueTypeBool, "", false),
	}, filterParameters()...)},
}

func ModelContracts() []ModelContract {
	result := make([]ModelContract, 0, len(modelContracts))
	for _, contract := range modelContracts {
		clone := withCustomPolicy(contract)
		clone.BuiltIn = true
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
	contract.BuiltIn = true
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
	// Source is the Consumer adapter path. DefaultModelPath is the unified-model
	// path used when no per-device binding overrides it.
	Source           ParameterPath  `json:"source"`
	DefaultModelPath ParameterPath  `json:"defaultModelPath,omitempty"`
	Target           string         `json:"target"`
	Level            ParameterLevel `json:"level"`
}

func (m ConsumerParameterMapping) ModelPath() ParameterPath {
	if m.DefaultModelPath.EndpointID != "" || m.DefaultModelPath.CapabilityID != "" || m.DefaultModelPath.PropertyID != "" {
		return m.DefaultModelPath
	}
	return m.Source
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
		_, standard := index[mapping.ModelPath().Key()]
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
