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
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Online      *bool    `json:"online,omitempty"`
	Power       *bool    `json:"power,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	Humidity    *float64 `json:"humidity,omitempty"`
	Contact     *bool    `json:"contact,omitempty"`
	Motion      *bool    `json:"motion,omitempty"`
}

type Config struct {
	LatencyMS    int            `json:"latencyMs"`
	RejectWrites bool           `json:"rejectWrites"`
	Devices      []DeviceConfig `json:"devices"`
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
		devices[created.ID] = created
	}
	return newProvider(item.ID, item.Name, config, devices), nil
}

func configuredDevice(providerID string, item DeviceConfig) (device.Device, error) {
	online := true
	if item.Online != nil {
		online = *item.Online
	}
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
		return poweredDevice(item.ID, providerID, item.Name, deviceType, online, power), nil
	case "temperature-sensor":
		temperature := 23.6
		if item.Temperature != nil {
			temperature = *item.Temperature
		}
		if temperature < -100 || temperature > 200 {
			return device.Device{}, fmt.Errorf("device %q temperature is outside -100..200", item.ID)
		}
		return temperatureDevice(item.ID, providerID, item.Name, online, temperature), nil
	case "humidity-sensor":
		humidity := 50.0
		if item.Humidity != nil {
			humidity = *item.Humidity
		}
		if humidity < 0 || humidity > 100 {
			return device.Device{}, fmt.Errorf("device %q humidity is outside 0..100", item.ID)
		}
		return humidityDevice(item.ID, providerID, item.Name, online, humidity), nil
	case "contact-sensor":
		value := false
		if item.Contact != nil {
			value = *item.Contact
		}
		return booleanSensorDevice(item.ID, providerID, item.Name, device.TypeContactSensor, "contact", "contact-sensor", "contact-detected", "接触状态", online, value), nil
	case "motion-sensor":
		value := false
		if item.Motion != nil {
			value = *item.Motion
		}
		return booleanSensorDevice(item.ID, providerID, item.Name, device.TypeMotionSensor, "motion", "motion-sensor", "motion-detected", "活动状态", online, value), nil
	default:
		return device.Device{}, fmt.Errorf("device %q has unsupported type %q", item.ID, item.Type)
	}
}
