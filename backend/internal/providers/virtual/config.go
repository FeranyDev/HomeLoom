package virtual

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

type DeviceConfig struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Type         string              `json:"type"`
	Availability device.Availability `json:"availability,omitempty"`
	Online       *bool               `json:"online,omitempty"`
	Power        *bool               `json:"power,omitempty"`
	Brightness   *float64            `json:"brightness,omitempty"`
	ColorTemp    *int64              `json:"colorTemperature,omitempty"`
	Hue          *float64            `json:"hue,omitempty"`
	Saturation   *float64            `json:"saturation,omitempty"`
	InUse        *bool               `json:"inUse,omitempty"`
	CurrentPower *float64            `json:"currentPower,omitempty"`
	Energy       *float64            `json:"energy,omitempty"`
	Temperature  *float64            `json:"temperature,omitempty"`
	Humidity     *float64            `json:"humidity,omitempty"`
	Contact      *bool               `json:"contact,omitempty"`
	Motion       *bool               `json:"motion,omitempty"`
	BatteryLevel *int64              `json:"batteryLevel,omitempty"`
	LowBattery   *bool               `json:"lowBattery,omitempty"`
	Tampered     *bool               `json:"tampered,omitempty"`
	Active       *bool               `json:"active,omitempty"`
	Speed        *float64            `json:"speed,omitempty"`
	Mode         string              `json:"mode,omitempty"`
	SwingMode    *bool               `json:"swingMode,omitempty"`
	Direction    string              `json:"direction,omitempty"`
	ControlLock  *bool               `json:"controlLock,omitempty"`
	AirQuality   string              `json:"airQuality,omitempty"`
	PM25         *float64            `json:"pm25,omitempty"`
	VOC          *float64            `json:"voc,omitempty"`
	FilterLife   *float64            `json:"filterLife,omitempty"`
	FilterChange *bool               `json:"filterChange,omitempty"`
	Position     *int64              `json:"position,omitempty"`
	Obstruction  *bool               `json:"obstruction,omitempty"`
}

type Config struct {
	LatencyMS    int            `json:"latencyMs"`
	RejectWrites bool           `json:"rejectWrites"`
	Devices      []DeviceConfig `json:"devices"`
}

// AllModelDeviceConfigs returns one deterministic demo device for every
// device type supported by the Virtual Provider. A new slice and new scalar
// pointers are returned on every call so callers can safely customize it.
func AllModelDeviceConfigs() []DeviceConfig {
	boolValue := func(value bool) *bool { return &value }
	numberValue := func(value float64) *float64 { return &value }
	intValue := func(value int64) *int64 { return &value }
	return []DeviceConfig{
		{ID: "virtual-switch-1", Name: "客厅开关", Type: "switch", Online: boolValue(true), Power: boolValue(false)},
		{ID: "virtual-lightbulb-1", Name: "客厅灯", Type: "lightbulb", Online: boolValue(true), Power: boolValue(true), Brightness: numberValue(80), ColorTemp: intValue(250), Hue: numberValue(35), Saturation: numberValue(45)},
		{ID: "virtual-outlet-1", Name: "书房插座", Type: "outlet", Online: boolValue(true), Power: boolValue(false), InUse: boolValue(false), CurrentPower: numberValue(0), Energy: numberValue(1.25)},
		{ID: "virtual-temperature-1", Name: "客厅温度", Type: "temperature-sensor", Online: boolValue(true), Temperature: numberValue(23.6), BatteryLevel: intValue(96), LowBattery: boolValue(false), Tampered: boolValue(false)},
		{ID: "virtual-humidity-1", Name: "客厅湿度", Type: "humidity-sensor", Online: boolValue(true), Humidity: numberValue(56.2), BatteryLevel: intValue(92), LowBattery: boolValue(false), Tampered: boolValue(false)},
		{ID: "virtual-contact-1", Name: "入户门", Type: "contact-sensor", Online: boolValue(true), Contact: boolValue(false), BatteryLevel: intValue(88), LowBattery: boolValue(false), Tampered: boolValue(false)},
		{ID: "virtual-motion-1", Name: "走廊活动", Type: "motion-sensor", Online: boolValue(true), Motion: boolValue(false), BatteryLevel: intValue(84), LowBattery: boolValue(false), Tampered: boolValue(false)},
		{ID: "virtual-fan-1", Name: "卧室风扇", Type: "fan", Online: boolValue(true), Active: boolValue(false), Speed: numberValue(35), Mode: "manual", SwingMode: boolValue(true), Direction: "clockwise", ControlLock: boolValue(false)},
		{ID: "virtual-air-purifier-1", Name: "客厅净化器", Type: "air-purifier", Online: boolValue(true), Active: boolValue(true), Speed: numberValue(60), Mode: "auto", SwingMode: boolValue(false), ControlLock: boolValue(false), AirQuality: "good", PM25: numberValue(12), VOC: numberValue(80), FilterLife: numberValue(82), FilterChange: boolValue(false)},
		{ID: "virtual-window-covering-1", Name: "南窗帘", Type: "window-covering", Online: boolValue(true), Position: intValue(50), Obstruction: boolValue(false)},
	}
}

func NewProviderFromConfig(item providerconfig.Config) (*Provider, error) {
	var config Config
	if len(item.Config) > 0 {
		if err := json.Unmarshal(item.Config, &config); err != nil {
			return nil, fmt.Errorf("decode virtual config: %w", err)
		}
	}
	if config.LatencyMS < 0 || config.LatencyMS > 60_000 {
		return nil, errors.New("latencyMs must be between 0 and 60000")
	}
	if len(config.Devices) == 0 {
		return newProvider(item.ID, item.Name, config, defaultDevices(item.ID)), nil
	}
	devices := make(map[string]device.Device, len(config.Devices))
	for index, definition := range config.Devices {
		definition.ID, definition.Name, definition.Type = strings.TrimSpace(definition.ID), strings.TrimSpace(definition.Name), strings.TrimSpace(definition.Type)
		if definition.ID == "" {
			definition.ID = fmt.Sprintf("%s-device-%d", item.ID, index+1)
		}
		if definition.Name == "" {
			definition.Name = definition.ID
		}
		if _, exists := devices[definition.ID]; exists {
			return nil, fmt.Errorf("duplicate virtual device id %q", definition.ID)
		}
		created, err := configuredDevice(item.ID, definition)
		if err != nil {
			return nil, err
		}
		if err := created.Validate(); err != nil {
			return nil, fmt.Errorf("device %q: %w", definition.ID, err)
		}
		devices[created.ID] = created
	}
	return newProvider(item.ID, item.Name, config, devices), nil
}

func configuredDevice(providerID string, item DeviceConfig) (device.Device, error) {
	availability := device.AvailabilityOnline
	if item.Online != nil {
		if !*item.Online {
			availability = device.AvailabilityOffline
		}
	}
	if item.Availability != "" {
		if item.Availability != device.AvailabilityOnline && item.Availability != device.AvailabilityOffline && item.Availability != device.AvailabilityUnknown {
			return device.Device{}, fmt.Errorf("device %q has invalid availability %q", item.ID, item.Availability)
		}
		availability = item.Availability
	}
	online := availability == device.AvailabilityOnline
	finish := func(created device.Device) device.Device { created.SetAvailability(availability); return created }
	switch item.Type {
	case "switch", "lightbulb", "outlet":
		power := false
		if item.Power != nil {
			power = *item.Power
		}
		deviceType := device.TypeSwitch
		if item.Type == "lightbulb" {
			deviceType = device.TypeLightbulb
		}
		if item.Type == "outlet" {
			deviceType = device.TypeOutlet
		}
		created := poweredDevice(item.ID, providerID, item.Name, deviceType, online, power)
		if err := applyPoweredDeviceConfig(&created, item); err != nil {
			return device.Device{}, err
		}
		return finish(created), nil
	case "temperature-sensor":
		temperature := 23.6
		if item.Temperature != nil {
			temperature = *item.Temperature
		}
		if temperature < -100 || temperature > 200 {
			return device.Device{}, fmt.Errorf("device %q temperature is outside -100..200", item.ID)
		}
		created := temperatureDevice(item.ID, providerID, item.Name, online, temperature)
		if err := applySensorStatusConfig(&created, item); err != nil {
			return device.Device{}, err
		}
		return finish(created), nil
	case "humidity-sensor":
		humidity := 50.0
		if item.Humidity != nil {
			humidity = *item.Humidity
		}
		if humidity < 0 || humidity > 100 {
			return device.Device{}, fmt.Errorf("device %q humidity is outside 0..100", item.ID)
		}
		created := humidityDevice(item.ID, providerID, item.Name, online, humidity)
		if err := applySensorStatusConfig(&created, item); err != nil {
			return device.Device{}, err
		}
		return finish(created), nil
	case "contact-sensor":
		value := false
		if item.Contact != nil {
			value = *item.Contact
		}
		created := booleanSensorDevice(item.ID, providerID, item.Name, device.TypeContactSensor, "contact", "contact-sensor", "contact-detected", "接触状态", online, value)
		if err := applySensorStatusConfig(&created, item); err != nil {
			return device.Device{}, err
		}
		return finish(created), nil
	case "motion-sensor":
		value := false
		if item.Motion != nil {
			value = *item.Motion
		}
		created := booleanSensorDevice(item.ID, providerID, item.Name, device.TypeMotionSensor, "motion", "motion-sensor", "motion-detected", "活动状态", online, value)
		if err := applySensorStatusConfig(&created, item); err != nil {
			return device.Device{}, err
		}
		return finish(created), nil
	case "fan":
		active, speed, mode, err := configurableActiveDeviceValues(item)
		if err != nil {
			return device.Device{}, err
		}
		created := fanDevice(item.ID, providerID, item.Name, online, active, speed, mode)
		applyAdvancedConfig(&created, item)
		return finish(created), nil
	case "air-purifier":
		active, speed, mode, err := configurableActiveDeviceValues(item)
		if err != nil {
			return device.Device{}, err
		}
		filterLife := 100.0
		if item.FilterLife != nil {
			filterLife = *item.FilterLife
		}
		if filterLife < 0 || filterLife > 100 {
			return device.Device{}, fmt.Errorf("device %q filterLife is outside 0..100", item.ID)
		}
		filterChange := filterLife <= 10
		if item.FilterChange != nil {
			filterChange = *item.FilterChange
		}
		created := airPurifierDevice(item.ID, providerID, item.Name, online, active, speed, mode, filterLife, filterChange)
		if err := applyAirQualityConfig(&created, item); err != nil {
			return device.Device{}, err
		}
		applyAdvancedConfig(&created, item)
		return finish(created), nil
	case "window-covering":
		position := int64(0)
		if item.Position != nil {
			position = *item.Position
		}
		if position < 0 || position > 100 {
			return device.Device{}, fmt.Errorf("device %q position is outside 0..100", item.ID)
		}
		created := windowCoveringDevice(item.ID, providerID, item.Name, online, position)
		if item.Obstruction != nil {
			created.SetProperty("main", "window-covering", "obstruction-detected", device.BoolValue(*item.Obstruction))
		}
		return finish(created), nil
	default:
		return device.Device{}, fmt.Errorf("device %q has unsupported type %q", item.ID, item.Type)
	}
}

func configurableActiveDeviceValues(item DeviceConfig) (bool, float64, string, error) {
	active := false
	if item.Active != nil {
		active = *item.Active
	}
	speed := 50.0
	if item.Speed != nil {
		speed = *item.Speed
	}
	if speed < 0 || speed > 100 {
		return false, 0, "", fmt.Errorf("device %q speed is outside 0..100", item.ID)
	}
	mode := strings.TrimSpace(item.Mode)
	if mode == "" {
		mode = "manual"
	}
	if mode != "manual" && mode != "auto" {
		return false, 0, "", fmt.Errorf("device %q mode must be manual or auto", item.ID)
	}
	return active, speed, mode, nil
}

func applyPoweredDeviceConfig(created *device.Device, item DeviceConfig) error {
	for _, value := range []struct {
		propertyID string
		value      *float64
		minimum    float64
		maximum    float64
	}{
		{"brightness", item.Brightness, 0, 100}, {"hue", item.Hue, 0, 360}, {"saturation", item.Saturation, 0, 100},
	} {
		if value.value == nil {
			continue
		}
		if *value.value < value.minimum || *value.value > value.maximum {
			return fmt.Errorf("device %q %s is outside %.0f..%.0f", item.ID, value.propertyID, value.minimum, value.maximum)
		}
		created.SetProperty("main", "light", value.propertyID, device.NumberValue(*value.value))
	}
	if item.ColorTemp != nil {
		if *item.ColorTemp < 140 || *item.ColorTemp > 500 {
			return fmt.Errorf("device %q colorTemperature is outside 140..500", item.ID)
		}
		created.SetProperty("main", "light", "color-temperature", device.IntValue(*item.ColorTemp))
	}
	if item.InUse != nil {
		created.SetProperty("main", "outlet", "in-use", device.BoolValue(*item.InUse))
	}
	for _, value := range []struct {
		propertyID string
		value      *float64
	}{{"current-power", item.CurrentPower}, {"energy", item.Energy}} {
		if value.value == nil {
			continue
		}
		if *value.value < 0 {
			return fmt.Errorf("device %q %s must not be negative", item.ID, value.propertyID)
		}
		created.SetProperty("main", "electrical", value.propertyID, device.NumberValue(*value.value))
	}
	return nil
}

func applySensorStatusConfig(created *device.Device, item DeviceConfig) error {
	level := int64(100)
	if item.BatteryLevel != nil {
		level = *item.BatteryLevel
	}
	if level < 0 || level > 100 {
		return fmt.Errorf("device %q batteryLevel is outside 0..100", item.ID)
	}
	low := level <= 20
	if item.LowBattery != nil {
		low = *item.LowBattery
	}
	tampered := false
	if item.Tampered != nil {
		tampered = *item.Tampered
	}
	created.SetProperty("main", "battery", "level", device.IntValue(level))
	created.SetProperty("main", "battery", "low", device.BoolValue(low))
	created.SetProperty("main", "security", "tampered", device.BoolValue(tampered))
	return nil
}

func applyAdvancedConfig(created *device.Device, item DeviceConfig) {
	capabilityID := "fan"
	if item.Type == "air-purifier" {
		capabilityID = "air-purifier"
	}
	if item.SwingMode != nil {
		created.SetProperty("main", capabilityID, "swing-mode", device.BoolValue(*item.SwingMode))
	}
	if item.ControlLock != nil {
		created.SetProperty("main", capabilityID, "lock-physical-controls", device.BoolValue(*item.ControlLock))
	}
	direction := strings.TrimSpace(item.Direction)
	if direction != "" {
		created.SetProperty("main", capabilityID, "rotation-direction", device.EnumValue(direction))
	}
}

func applyAirQualityConfig(created *device.Device, item DeviceConfig) error {
	quality := strings.TrimSpace(item.AirQuality)
	if quality != "" {
		allowed := map[string]bool{"unknown": true, "excellent": true, "good": true, "fair": true, "inferior": true, "poor": true}
		if !allowed[quality] {
			return fmt.Errorf("device %q airQuality is unsupported", item.ID)
		}
		created.SetProperty("main", "air-quality", "current-air-quality", device.EnumValue(quality))
	}
	for _, value := range []struct {
		propertyID string
		value      *float64
	}{{"pm2.5-density", item.PM25}, {"voc-density", item.VOC}} {
		if value.value == nil {
			continue
		}
		if *value.value < 0 {
			return fmt.Errorf("device %q %s must not be negative", item.ID, value.propertyID)
		}
		created.SetProperty("main", "air-quality", value.propertyID, device.NumberValue(*value.value))
	}
	return nil
}
