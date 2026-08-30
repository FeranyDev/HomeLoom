package gree

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

func buildAirConditionerDevice(providerID string, configured DeviceConfig, raw map[string]any, online bool) (device.Device, error) {
	pow := rawInt(raw, "Pow", 0)
	active := pow != 0
	mode := greeMode(rawInt(raw, "Mod", 0))
	targetMode := mode
	if !active {
		targetMode = "off"
	}
	currentState := "off"
	if active {
		switch mode {
		case "cool":
			currentState = "cooling"
		case "heat":
			currentState = "heating"
		case "dry":
			currentState = "drying"
		case "fan":
			currentState = "fan-only"
		default:
			currentState = "idle"
		}
	}

	targetTemperature := rawNumber(raw, "SetTem", 24) + 0.5*float64(rawInt(raw, "TemRec", 0))
	// StHt is the Gree 8°C anti-freeze mode. The unified Air Conditioner v4
	// contract intentionally starts at 16°C, so retain the valid model value
	// while the raw option still records that auxiliary mode is active.
	if rawInt(raw, "StHt", 0) != 0 && targetTemperature < 16 {
		targetTemperature = 16
	}
	if targetTemperature < 16 || targetTemperature > 32 {
		targetTemperature = 24
	}
	currentTemperature, hasCurrentTemperature := greeSensorTemperature(raw, "TemSen", configured.TempSensorOffset)
	humidity, hasHumidity := greeHumidity(raw)
	outsideTemperature, hasOutsideTemperature := greeSensorTemperature(raw, "OutEnvTem", configured.TempSensorOffset)
	fanMode := greeFanMode(rawInt(raw, "WdSpd", 0), rawInt(raw, "Tur", 0), rawInt(raw, "Quiet", 0))
	rotationSpeed := greeFanPercent(rawInt(raw, "WdSpd", 0), rawInt(raw, "Tur", 0))
	fault := greeFault(raw)
	minimumTemperature, maximumTemperature, targetTemperatureStep := 16.0, 32.0, 0.5
	minimumCurrentTemperature, maximumCurrentTemperature, currentTemperatureStep := -100.0, 200.0, 0.1
	minimumPercent, maximumPercent, percentStep := 0.0, 100.0, 1.0
	minimumHumidity, maximumHumidity, humidityStep := 0.0, 100.0, 0.1
	airProperties := []device.Property{
		{Definition: device.PropertyDefinition{ID: "active", Name: "启用", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(active)},
		{Definition: device.PropertyDefinition{ID: "current-state", Name: "当前工作状态", Type: device.ValueTypeEnum, Readable: true, Notifiable: true, Enum: []string{"off", "idle", "cooling", "heating", "drying", "fan-only"}, StaleAfterSeconds: 120}, Value: device.EnumValue(currentState)},
		{Definition: device.PropertyDefinition{ID: "target-mode", Name: "运行模式", Type: device.ValueTypeEnum, Readable: true, Writable: true, Notifiable: true, Enum: []string{"off", "auto", "cool", "heat", "dry", "fan"}, StaleAfterSeconds: 120}, Value: device.EnumValue(targetMode)},
		{Definition: device.PropertyDefinition{ID: "fan-speed", Name: "风速档位", Type: device.ValueTypeEnum, Readable: true, Writable: true, Notifiable: true, Enum: []string{"auto", "low", "medium_low", "medium", "medium_high", "high", "turbo", "quiet"}, StaleAfterSeconds: 120}, Value: device.EnumValue(fanMode)},
		{Definition: device.PropertyDefinition{ID: "rotation-speed", Name: "风速百分比", Type: device.ValueTypeNumber, Unit: "percent", Readable: true, Writable: true, Notifiable: true, Min: &minimumPercent, Max: &maximumPercent, Step: &percentStep, StaleAfterSeconds: 120}, Value: device.NumberValue(rotationSpeed)},
		{Definition: device.PropertyDefinition{ID: "vertical-swing", Name: "上下扫风", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "SwUpDn", 0) != 0)},
		{Definition: device.PropertyDefinition{ID: "horizontal-swing", Name: "左右扫风", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "SwingLfRig", 0) != 0)},
		{Definition: device.PropertyDefinition{ID: "vertical-swing-mode", Name: "上下扫风位置", Type: device.ValueTypeEnum, Readable: true, Writable: true, Notifiable: true, Enum: greeVerticalSwingModes, StaleAfterSeconds: 120}, Value: device.EnumValue(greeVerticalSwingMode(rawInt(raw, "SwUpDn", 0)))},
		{Definition: device.PropertyDefinition{ID: "horizontal-swing-mode", Name: "左右扫风位置", Type: device.ValueTypeEnum, Readable: true, Writable: true, Notifiable: true, Enum: greeHorizontalSwingModes, StaleAfterSeconds: 120}, Value: device.EnumValue(greeHorizontalSwingMode(rawInt(raw, "SwingLfRig", 0)))},
		{Definition: device.PropertyDefinition{ID: "auxiliary-heat", Name: "辅热", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "StHt", 0) != 0)},
		{Definition: device.PropertyDefinition{ID: "eight-degree-heat", Name: "8°C 防冻制热", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "StHt", 0) != 0)},
		{Definition: device.PropertyDefinition{ID: "sleep-mode", Name: "睡眠模式", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "SwhSlp", 0) != 0 && rawInt(raw, "SlpMod", 0) != 0)},
		{Definition: device.PropertyDefinition{ID: "sleep", Name: "睡眠", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "SwhSlp", 0) != 0 && rawInt(raw, "SlpMod", 0) != 0)},
		{Definition: device.PropertyDefinition{ID: "eco-mode", Name: "节能模式", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "SvSt", 0) != 0)},
		{Definition: device.PropertyDefinition{ID: "power-save", Name: "省电模式", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "SvSt", 0) != 0)},
		{Definition: device.PropertyDefinition{ID: "x-fan", Name: "X-Fan", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "Blo", 0) != 0)},
		{Definition: device.PropertyDefinition{ID: "health", Name: "健康模式", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "Health", 0) != 0)},
		{Definition: device.PropertyDefinition{ID: "air", Name: "新风模式", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "Air", 0) != 0)},
		{Definition: device.PropertyDefinition{ID: "display-enabled", Name: "面板显示", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "Lig", 1) != 0)},
		{Definition: device.PropertyDefinition{ID: "display-units", Name: "显示温标", Type: device.ValueTypeEnum, Readable: true, Writable: true, Notifiable: true, Enum: []string{"celsius", "fahrenheit"}, StaleAfterSeconds: 120}, Value: device.EnumValue(greeUnits(rawInt(raw, "TemUn", 0)))},
		{Definition: device.PropertyDefinition{ID: "auto-x-fan", Name: "自动 X-Fan", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawBool(raw, "AutoXFan"))},
		{Definition: device.PropertyDefinition{ID: "auto-light", Name: "自动面板灯", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawBool(raw, "AutoLight"))},
		{Definition: device.PropertyDefinition{ID: "beeper", Name: "蜂鸣器", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "BuzzerCtrl", 1) != 0)},
		{Definition: device.PropertyDefinition{ID: "fault", Name: "故障代码", Type: device.ValueTypeString, Readable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.StringValue(fault)},
	}
	if rawHas(raw, "AntiDirectBlow") {
		airProperties = append(airProperties, device.Property{Definition: device.PropertyDefinition{ID: "anti-direct-blow", Name: "防直吹", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "AntiDirectBlow", 0) != 0)})
	}
	if rawHas(raw, "LigSen") {
		airProperties = append(airProperties, device.Property{Definition: device.PropertyDefinition{ID: "light-sensor", Name: "光线传感器", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 120}, Value: device.BoolValue(rawInt(raw, "LigSen", 1) == 0)})
	}
	temperatureProperties := []device.Property{{Definition: device.PropertyDefinition{ID: "target-temperature", Name: "目标温度", Type: device.ValueTypeNumber, Unit: "celsius", Readable: true, Writable: true, Notifiable: true, Min: &minimumTemperature, Max: &maximumTemperature, Step: &targetTemperatureStep, StaleAfterSeconds: 120}, Value: device.NumberValue(targetTemperature)}}
	if hasCurrentTemperature {
		temperatureProperties = append(temperatureProperties, device.Property{Definition: device.PropertyDefinition{ID: "current-temperature", Name: "当前温度", Type: device.ValueTypeNumber, Unit: "celsius", Readable: true, Notifiable: true, Min: &minimumCurrentTemperature, Max: &maximumCurrentTemperature, Step: &currentTemperatureStep, StaleAfterSeconds: 120}, Value: device.NumberValue(currentTemperature)})
	}
	if hasOutsideTemperature {
		temperatureProperties = append(temperatureProperties, device.Property{Definition: device.PropertyDefinition{ID: "outside-temperature", Name: "室外温度", Type: device.ValueTypeNumber, Unit: "celsius", Readable: true, Notifiable: true, Min: &minimumCurrentTemperature, Max: &maximumCurrentTemperature, Step: &currentTemperatureStep, StaleAfterSeconds: 120}, Value: device.NumberValue(outsideTemperature)})
	}
	capabilities := []device.Capability{{ID: "air-conditioner", Type: "air-conditioner", Properties: airProperties}, {ID: "temperature", Type: "temperature", Properties: temperatureProperties}}
	if hasHumidity {
		capabilities = append(capabilities, device.Capability{ID: "humidity", Type: "humidity", Properties: []device.Property{{Definition: device.PropertyDefinition{ID: "current-humidity", Name: "当前湿度", Type: device.ValueTypeNumber, Unit: "percent", Readable: true, Notifiable: true, Min: &minimumHumidity, Max: &maximumHumidity, Step: &humidityStep, StaleAfterSeconds: 120}, Value: device.NumberValue(humidity)}}})
	}
	item := device.Device{
		SchemaVersion: device.SchemaVersion,
		ID:            configured.ID,
		ProviderID:    providerID,
		Name:          configured.Name,
		Type:          device.TypeAirConditioner,
		HomeID:        configured.HomeID,
		HomeName:      configured.HomeName,
		RoomID:        configured.RoomID,
		RoomName:      configured.RoomName,
		Sequence:      1,
		LastUpdateAt:  time.Now().UTC(),
		RuntimeMode:   device.RuntimeModeLocal,
		Endpoints: []device.Endpoint{{
			ID: "main", Name: "主端点", Type: "air-conditioner", Capabilities: capabilities,
		}},
	}
	item.SetOnline(online)
	if err := item.NormalizeModelParameters(); err != nil {
		return device.Device{}, fmt.Errorf("normalize Air Conditioner v4 model: %w", err)
	}
	return item, nil
}

func rawInt(raw map[string]any, key string, fallback int) int {
	if value, ok := numberFromAny(raw[key]); ok {
		return int(value)
	}
	return fallback
}

func greeMode(value int) string {
	switch value {
	case 1:
		return "cool"
	case 2:
		return "dry"
	case 3:
		return "fan"
	case 4:
		return "heat"
	default:
		return "auto"
	}
}

var greeVerticalSwingModes = []string{
	"default", "swing_full", "fixed_upmost", "fixed_middle_up", "fixed_middle", "fixed_middle_low", "fixed_lowest",
	"swing_downmost", "swing_middle_low", "swing_middle", "swing_middle_up", "swing_upmost",
}

var greeHorizontalSwingModes = []string{
	"default", "swing_full", "fixed_leftmost", "fixed_middle_left", "fixed_middle", "fixed_middle_right", "fixed_rightmost",
}

func greeFanMode(speed, turbo, quiet int) string {
	if turbo != 0 {
		return "turbo"
	}
	if quiet != 0 {
		return "quiet"
	}
	switch speed {
	case 1:
		return "low"
	case 2:
		return "medium_low"
	case 3:
		return "medium"
	case 4:
		return "medium_high"
	case 5:
		return "high"
	default:
		return "auto"
	}
}

func greeVerticalSwingMode(value int) string {
	if value < 0 || value >= len(greeVerticalSwingModes) {
		return greeVerticalSwingModes[0]
	}
	return greeVerticalSwingModes[value]
}

func greeHorizontalSwingMode(value int) string {
	if value < 0 || value >= len(greeHorizontalSwingModes) {
		return greeHorizontalSwingModes[0]
	}
	return greeHorizontalSwingModes[value]
}

func greeFanPercent(speed, turbo int) float64 {
	if turbo != 0 {
		return 100
	}
	if speed < 0 {
		return 0
	}
	if speed > 5 {
		speed = 5
	}
	return float64(speed * 20)
}

func greeUnits(value int) string {
	if value != 0 {
		return "fahrenheit"
	}
	return "celsius"
}

func rawHas(raw map[string]any, key string) bool {
	value, ok := raw[key]
	return ok && value != nil
}

func rawBool(raw map[string]any, key string) bool {
	if value, ok := raw[key].(bool); ok {
		return value
	}
	return rawInt(raw, key, 0) != 0
}

func greeSensorTemperature(raw map[string]any, key string, offset *bool) (float64, bool) {
	value, ok := numberFromAny(raw[key])
	if !ok {
		return 0, false
	}
	if offset != nil {
		if *offset {
			value -= 40
		}
	} else if value >= 40 {
		value -= 40
	}
	if value < -100 || value > 200 {
		return 0, false
	}
	return value, true
}

func greeHumidity(raw map[string]any) (float64, bool) {
	value, ok := numberFromAny(raw["DwatSen"])
	if !ok || value < 0 || value > 100 {
		return 0, false
	}
	return value, true
}

func greeFault(raw map[string]any) string {
	for _, key := range []string{"Fault", "FaultCode", "ErrCode", "Err", "ErrorCode"} {
		value, exists := raw[key]
		if !exists {
			continue
		}
		if text, ok := value.(string); ok {
			text = strings.TrimSpace(text)
			if text == "" || text == "0" {
				return ""
			}
			return text
		}
		if number, ok := numberFromAny(value); ok {
			if number == 0 {
				return ""
			}
			return strconv.Itoa(int(number))
		}
	}
	return ""
}
