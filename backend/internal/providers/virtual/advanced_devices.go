package virtual

import (
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

const (
	modeManual = "manual"
	modeAuto   = "auto"

	stateInactive   = "inactive"
	stateIdle       = "idle"
	stateBlowingAir = "blowing-air"
	statePurifying  = "purifying-air"

	positionDecreasing = "decreasing"
	positionIncreasing = "increasing"
	positionStopped    = "stopped"
)

func fanDevice(id, providerID, name string, online, active bool, speed float64, mode string) device.Device {
	minimum, maximum, step := 0.0, 100.0, 1.0
	item := device.Device{SchemaVersion: device.SchemaVersion, ID: id, ProviderID: providerID, Name: name, Type: device.TypeFan, Sequence: 1, LastUpdateAt: time.Now().UTC(), Endpoints: []device.Endpoint{{ID: "main", Name: "主端点", Type: "fan", Capabilities: []device.Capability{{ID: "fan", Type: "fan", Properties: []device.Property{
		{Definition: device.PropertyDefinition{ID: "active", Name: "启用", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 15}, Value: device.BoolValue(active)},
		{Definition: device.PropertyDefinition{ID: "current-state", Name: "当前状态", Type: device.ValueTypeEnum, Readable: true, Notifiable: true, Enum: []string{stateInactive, stateIdle, stateBlowingAir}, StaleAfterSeconds: 15}, Value: device.EnumValue(fanState(active, speed))},
		{Definition: device.PropertyDefinition{ID: "target-state", Name: "目标模式", Type: device.ValueTypeEnum, Readable: true, Writable: true, Notifiable: true, Enum: []string{modeManual, modeAuto}, StaleAfterSeconds: 15}, Value: device.EnumValue(mode)},
		{Definition: device.PropertyDefinition{ID: "rotation-speed", Name: "转速", Type: device.ValueTypeNumber, Unit: "percent", Readable: true, Writable: true, Notifiable: true, Min: &minimum, Max: &maximum, Step: &step, StaleAfterSeconds: 15}, Value: device.NumberValue(speed)},
		{Definition: device.PropertyDefinition{ID: "swing-mode", Name: "摇头", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 15}, Value: device.BoolValue(false)},
		{Definition: device.PropertyDefinition{ID: "rotation-direction", Name: "旋转方向", Type: device.ValueTypeEnum, Readable: true, Writable: true, Notifiable: true, Enum: []string{"clockwise", "counter-clockwise"}, StaleAfterSeconds: 15}, Value: device.EnumValue("clockwise")},
		{Definition: device.PropertyDefinition{ID: "lock-physical-controls", Name: "物理控制锁", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 15}, Value: device.BoolValue(false)},
	}}}}}}
	item.SetOnline(online)
	return item
}

func airPurifierDevice(id, providerID, name string, online, active bool, speed float64, mode string, filterLife float64, filterChange bool) device.Device {
	minimum, maximum, step := 0.0, 100.0, 1.0
	item := device.Device{SchemaVersion: device.SchemaVersion, ID: id, ProviderID: providerID, Name: name, Type: device.TypeAirPurifier, Sequence: 1, LastUpdateAt: time.Now().UTC(), Endpoints: []device.Endpoint{{ID: "main", Name: "主端点", Type: "air-purifier", Capabilities: []device.Capability{
		{ID: "air-purifier", Type: "air-purifier", Properties: []device.Property{
			{Definition: device.PropertyDefinition{ID: "active", Name: "启用", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 15}, Value: device.BoolValue(active)},
			{Definition: device.PropertyDefinition{ID: "current-state", Name: "当前状态", Type: device.ValueTypeEnum, Readable: true, Notifiable: true, Enum: []string{stateInactive, stateIdle, statePurifying}, StaleAfterSeconds: 15}, Value: device.EnumValue(airPurifierState(active, speed))},
			{Definition: device.PropertyDefinition{ID: "target-state", Name: "目标模式", Type: device.ValueTypeEnum, Readable: true, Writable: true, Notifiable: true, Enum: []string{modeManual, modeAuto}, StaleAfterSeconds: 15}, Value: device.EnumValue(mode)},
			{Definition: device.PropertyDefinition{ID: "rotation-speed", Name: "净化速度", Type: device.ValueTypeNumber, Unit: "percent", Readable: true, Writable: true, Notifiable: true, Min: &minimum, Max: &maximum, Step: &step, StaleAfterSeconds: 15}, Value: device.NumberValue(speed)},
			{Definition: device.PropertyDefinition{ID: "swing-mode", Name: "摆风", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 15}, Value: device.BoolValue(false)},
			{Definition: device.PropertyDefinition{ID: "lock-physical-controls", Name: "物理控制锁", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 15}, Value: device.BoolValue(false)},
		}},
		{ID: "air-quality", Type: "air-quality-sensor", Properties: []device.Property{
			{Definition: device.PropertyDefinition{ID: "current-air-quality", Name: "空气质量", Type: device.ValueTypeEnum, Readable: true, Notifiable: true, Enum: []string{"unknown", "excellent", "good", "fair", "inferior", "poor"}, StaleAfterSeconds: 60}, Value: device.EnumValue("good")},
			{Definition: device.PropertyDefinition{ID: "pm2.5-density", Name: "PM2.5", Type: device.ValueTypeNumber, Unit: "microgram-per-cubic-meter", Readable: true, Notifiable: true, Min: &minimum, StaleAfterSeconds: 60}, Value: device.NumberValue(12)},
			{Definition: device.PropertyDefinition{ID: "voc-density", Name: "VOC", Type: device.ValueTypeNumber, Unit: "microgram-per-cubic-meter", Readable: true, Notifiable: true, Min: &minimum, StaleAfterSeconds: 60}, Value: device.NumberValue(80)},
		}},
		{ID: "filter", Type: "filter-maintenance", Properties: []device.Property{
			{Definition: device.PropertyDefinition{ID: "life-level", Name: "滤芯寿命", Type: device.ValueTypeNumber, Unit: "percent", Readable: true, Notifiable: true, Min: &minimum, Max: &maximum, Step: &step, StaleAfterSeconds: 300}, Value: device.NumberValue(filterLife)},
			{Definition: device.PropertyDefinition{ID: "change-indication", Name: "需要更换滤芯", Type: device.ValueTypeBool, Readable: true, Notifiable: true, StaleAfterSeconds: 300}, Value: device.BoolValue(filterChange)},
		}, Commands: []device.CommandDefinition{{ID: "reset-filter", Name: "重置滤芯寿命", Idempotent: true}}},
	}}}}
	item.SetOnline(online)
	return item
}

func windowCoveringDevice(id, providerID, name string, online bool, position int64) device.Device {
	minimum, maximum, step := 0.0, 100.0, 1.0
	item := device.Device{SchemaVersion: device.SchemaVersion, ID: id, ProviderID: providerID, Name: name, Type: device.TypeWindowCovering, Sequence: 1, LastUpdateAt: time.Now().UTC(), Endpoints: []device.Endpoint{{ID: "main", Name: "主端点", Type: "window-covering", Capabilities: []device.Capability{{ID: "window-covering", Type: "window-covering", Properties: []device.Property{
		{Definition: device.PropertyDefinition{ID: "current-position", Name: "当前位置", Type: device.ValueTypeInt, Unit: "percent", Readable: true, Notifiable: true, Min: &minimum, Max: &maximum, Step: &step, StaleAfterSeconds: 30}, Value: device.IntValue(position)},
		{Definition: device.PropertyDefinition{ID: "target-position", Name: "目标位置", Type: device.ValueTypeInt, Unit: "percent", Readable: true, Writable: true, Notifiable: true, Min: &minimum, Max: &maximum, Step: &step, StaleAfterSeconds: 30}, Value: device.IntValue(position)},
		{Definition: device.PropertyDefinition{ID: "position-state", Name: "运动状态", Type: device.ValueTypeEnum, Readable: true, Notifiable: true, Enum: []string{positionDecreasing, positionIncreasing, positionStopped}, StaleAfterSeconds: 30}, Value: device.EnumValue(positionStopped)},
		{Definition: device.PropertyDefinition{ID: "obstruction-detected", Name: "障碍物检测", Type: device.ValueTypeBool, Readable: true, Notifiable: true, StaleAfterSeconds: 30}, Value: device.BoolValue(false)},
	}}}}}}
	item.SetOnline(online)
	return item
}

func fanState(active bool, speed float64) string {
	if !active {
		return stateInactive
	}
	if speed <= 0 {
		return stateIdle
	}
	return stateBlowingAir
}

func airPurifierState(active bool, speed float64) string {
	if !active {
		return stateInactive
	}
	if speed <= 0 {
		return stateIdle
	}
	return statePurifying
}
