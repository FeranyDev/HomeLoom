package gree

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

const (
	ProviderType = "gree"
	defaultPort  = 7000

	defaultRequestTimeout = 5 * time.Second
	defaultPollInterval   = 60 * time.Second
)

// Config is the durable configuration for a local Gree Provider. A device key
// is optional: when it is omitted the Provider performs the selected protocol
// version's bind request with the matching generic Gree key and keeps the
// returned key in memory.
type Config struct {
	Devices               []DeviceConfig `json:"devices"`
	RequestTimeoutSeconds int            `json:"requestTimeoutSeconds,omitempty"`
	PollIntervalSeconds   int            `json:"pollIntervalSeconds,omitempty"`
}

// DeviceConfig describes one Gree indoor unit. MAC accepts the normal
// twelve-hex-digit form and the sub-unit form "submac@parentmac" used by
// multi-split units.
type DeviceConfig struct {
	ID                    string  `json:"id"`
	Name                  string  `json:"name"`
	Host                  string  `json:"host"`
	Port                  int     `json:"port,omitempty"`
	MAC                   string  `json:"mac"`
	UID                   int64   `json:"uid,omitempty"`
	EncryptionKey         string  `json:"encryptionKey,omitempty"`
	EncryptionVersion     int     `json:"encryptionVersion,omitempty"`
	TargetTemperatureStep float64 `json:"targetTemperatureStep,omitempty"`
	TempSensorOffset      *bool   `json:"tempSensorOffset,omitempty"`
	DisableAvailableCheck bool    `json:"disableAvailableCheck,omitempty"`
	AutoXFan              bool    `json:"autoXFan,omitempty"`
	AutoLight             bool    `json:"autoLight,omitempty"`
	HomeID                string  `json:"homeId,omitempty"`
	HomeName              string  `json:"homeName,omitempty"`
	RoomID                string  `json:"roomId,omitempty"`
	RoomName              string  `json:"roomName,omitempty"`
	Enabled               *bool   `json:"enabled,omitempty"`
}

// UnmarshalJSON also accepts the field names used by the Home Assistant Gree
// configuration (encryption_key, encryption_version, mac_address, ip and the
// snake_case runtime options).
// They are normalized into the backend's camelCase representation before
// validation; no protocol-specific compatibility leaks into the Device model.
func (c *DeviceConfig) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID                         string          `json:"id"`
		Name                       string          `json:"name"`
		Host                       string          `json:"host"`
		IP                         string          `json:"ip"`
		Port                       int             `json:"port"`
		MAC                        string          `json:"mac"`
		MACAddress                 string          `json:"macAddress"`
		UID                        json.RawMessage `json:"uid"`
		EncryptionKey              string          `json:"encryptionKey"`
		EncryptionKeySnake         string          `json:"encryption_key"`
		Key                        string          `json:"key"`
		EncryptionVersion          int             `json:"encryptionVersion"`
		EncryptionVerSnake         int             `json:"encryption_version"`
		TargetTemperatureStep      float64         `json:"targetTemperatureStep"`
		TargetTemperatureStepSnake float64         `json:"target_temperature_step"`
		TempSensorOffset           *bool           `json:"tempSensorOffset"`
		TempSensorOffsetSnake      *bool           `json:"temp_sensor_offset"`
		DisableAvailableCheck      bool            `json:"disableAvailableCheck"`
		DisableAvailableSnake      bool            `json:"disable_available_check"`
		AutoXFan                   bool            `json:"autoXFan"`
		AutoXFanSnake              bool            `json:"auto_xfan"`
		AutoLight                  bool            `json:"autoLight"`
		AutoLightSnake             bool            `json:"auto_light"`
		HomeID                     string          `json:"homeId"`
		HomeName                   string          `json:"homeName"`
		RoomID                     string          `json:"roomId"`
		RoomName                   string          `json:"roomName"`
		Enabled                    *bool           `json:"enabled"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	uid, err := decodeInt64(wire.UID)
	if err != nil {
		return fmt.Errorf("decode gree uid: %w", err)
	}
	key := wire.EncryptionKey
	if key == "" {
		key = wire.EncryptionKeySnake
	}
	if key == "" {
		key = wire.Key
	}
	version := wire.EncryptionVersion
	if version == 0 {
		version = wire.EncryptionVerSnake
	}
	targetTemperatureStep := wire.TargetTemperatureStep
	if targetTemperatureStep == 0 {
		targetTemperatureStep = wire.TargetTemperatureStepSnake
	}
	tempSensorOffset := wire.TempSensorOffset
	if tempSensorOffset == nil {
		tempSensorOffset = wire.TempSensorOffsetSnake
	}
	host := wire.Host
	if host == "" {
		host = wire.IP
	}
	mac := wire.MAC
	if mac == "" {
		mac = wire.MACAddress
	}
	*c = DeviceConfig{
		ID:                    wire.ID,
		Name:                  wire.Name,
		Host:                  host,
		Port:                  wire.Port,
		MAC:                   mac,
		UID:                   uid,
		EncryptionKey:         key,
		EncryptionVersion:     version,
		TargetTemperatureStep: targetTemperatureStep,
		TempSensorOffset:      tempSensorOffset,
		DisableAvailableCheck: wire.DisableAvailableCheck || wire.DisableAvailableSnake,
		AutoXFan:              wire.AutoXFan || wire.AutoXFanSnake,
		AutoLight:             wire.AutoLight || wire.AutoLightSnake,
		HomeID:                wire.HomeID,
		HomeName:              wire.HomeName,
		RoomID:                wire.RoomID,
		RoomName:              wire.RoomName,
		Enabled:               wire.Enabled,
	}
	return nil
}

func decodeInt64(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return strconv.ParseInt(string(number), 10, 64)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return 0, nil
		}
		return strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	}
	return 0, errors.New("uid must be an integer")
}

func decodeConfig(item providerconfig.Config) (Config, error) {
	var wire struct {
		Devices               []DeviceConfig `json:"devices"`
		Device                *DeviceConfig  `json:"device"`
		RequestTimeoutSeconds int            `json:"requestTimeoutSeconds"`
		RequestTimeoutSec     int            `json:"requestTimeoutSec"`
		PollIntervalSeconds   int            `json:"pollIntervalSeconds"`
		PollIntervalSec       int            `json:"pollIntervalSec"`
	}
	if err := json.Unmarshal(item.Config, &wire); err != nil {
		return Config{}, fmt.Errorf("decode gree config: %w", err)
	}
	config := Config{
		Devices:               append([]DeviceConfig(nil), wire.Devices...),
		RequestTimeoutSeconds: wire.RequestTimeoutSeconds,
		PollIntervalSeconds:   wire.PollIntervalSeconds,
	}
	if config.RequestTimeoutSeconds == 0 {
		config.RequestTimeoutSeconds = wire.RequestTimeoutSec
	}
	if config.PollIntervalSeconds == 0 {
		config.PollIntervalSeconds = wire.PollIntervalSec
	}
	if wire.Device != nil {
		config.Devices = append(config.Devices, *wire.Device)
	}
	if err := normalizeConfig(&config, item.ID); err != nil {
		return Config{}, err
	}
	return config, nil
}

func normalizeConfig(config *Config, providerID string) error {
	if config.RequestTimeoutSeconds == 0 {
		config.RequestTimeoutSeconds = int(defaultRequestTimeout / time.Second)
	}
	if config.RequestTimeoutSeconds < 1 || config.RequestTimeoutSeconds > 120 {
		return errors.New("requestTimeoutSeconds must be between 1 and 120")
	}
	if config.PollIntervalSeconds == 0 {
		config.PollIntervalSeconds = int(defaultPollInterval / time.Second)
	}
	if config.PollIntervalSeconds < 1 || config.PollIntervalSeconds > 3600 {
		return errors.New("pollIntervalSeconds must be between 1 and 3600")
	}
	if !device.ValidStableID(providerID) {
		return errors.New("gree Provider id must be a stable lowercase id")
	}

	seen := make(map[string]struct{}, len(config.Devices))
	for index := range config.Devices {
		item := &config.Devices[index]
		item.ID = strings.TrimSpace(item.ID)
		item.Name = strings.TrimSpace(item.Name)
		item.Host = strings.TrimSpace(item.Host)
		item.EncryptionKey = strings.TrimSpace(item.EncryptionKey)
		item.HomeID = strings.TrimSpace(item.HomeID)
		item.HomeName = strings.TrimSpace(item.HomeName)
		item.RoomID = strings.TrimSpace(item.RoomID)
		item.RoomName = strings.TrimSpace(item.RoomName)
		if item.Host == "" {
			return fmt.Errorf("devices[%d].host is required", index)
		}
		subMAC, mainMAC, err := normalizeMAC(item.MAC)
		if err != nil {
			if strings.TrimSpace(item.MAC) == "" {
				return fmt.Errorf("devices[%d].mac is required", index)
			}
			return fmt.Errorf("devices[%d].mac: %w", index, err)
		}
		item.MAC = subMAC
		if subMAC != mainMAC {
			item.MAC += "@" + mainMAC
		}
		if item.Port == 0 {
			item.Port = defaultPort
		}
		if item.Port < 1 || item.Port > 65535 {
			return fmt.Errorf("device %q port must be between 1 and 65535", item.ID)
		}
		if item.UID < 0 {
			return fmt.Errorf("device %q uid cannot be negative", item.ID)
		}
		if item.EncryptionVersion == 0 {
			item.EncryptionVersion = 1
		}
		if item.EncryptionVersion != 1 && item.EncryptionVersion != 2 {
			return fmt.Errorf("device %q only supports Gree encryptionVersion 1 or 2", item.ID)
		}
		if item.TargetTemperatureStep != 0 && (item.TargetTemperatureStep < 0.1 || item.TargetTemperatureStep > 5) {
			return fmt.Errorf("device %q targetTemperatureStep must be between 0.1 and 5", item.ID)
		}
		if item.ID == "" {
			item.ID = "gree-" + mainMAC
		}
		if !device.ValidStableID(item.ID) {
			return fmt.Errorf("device id %q must be a stable lowercase id", item.ID)
		}
		if item.Name == "" {
			suffix := item.ID
			if len(suffix) > 4 {
				suffix = suffix[len(suffix)-4:]
			}
			item.Name = "Gree " + suffix
		}
		if _, exists := seen[item.ID]; exists {
			return fmt.Errorf("duplicate Gree device id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		if item.EncryptionKey != "" {
			var err error
			if item.EncryptionVersion == 2 {
				_, err = newAESGCM([]byte(item.EncryptionKey))
			} else {
				_, err = newAESBlock([]byte(item.EncryptionKey))
			}
			if err != nil {
				return fmt.Errorf("device %q encryptionKey: %w", item.ID, err)
			}
		}
	}
	return nil
}

func normalizeMAC(value string) (subMAC, mainMAC string, err error) {
	parts := strings.Split(strings.TrimSpace(value), "@")
	if len(parts) > 2 {
		return "", "", errors.New("mac may contain at most one @ separator")
	}
	if len(parts) == 1 {
		parts = append(parts, parts[0])
	}
	clean := func(input string) (string, error) {
		input = strings.ToLower(strings.TrimSpace(input))
		input = strings.NewReplacer(":", "", "-", "", " ", "").Replace(input)
		if len(input) != 12 {
			return "", errors.New("mac must contain 12 hexadecimal digits")
		}
		if _, err := hex.DecodeString(input); err != nil {
			return "", errors.New("mac must contain 12 hexadecimal digits")
		}
		return input, nil
	}
	subMAC, err = clean(parts[0])
	if err != nil {
		return "", "", err
	}
	mainMAC, err = clean(parts[1])
	if err != nil {
		return "", "", err
	}
	return subMAC, mainMAC, nil
}

func (c Config) requestTimeout() time.Duration {
	return time.Duration(c.RequestTimeoutSeconds) * time.Second
}

func (c Config) pollInterval() time.Duration {
	return time.Duration(c.PollIntervalSeconds) * time.Second
}

func (c DeviceConfig) enabled() bool {
	return c.Enabled == nil || *c.Enabled
}

func (c DeviceConfig) macParts() (subMAC, mainMAC string) {
	subMAC, mainMAC, _ = normalizeMAC(c.MAC)
	return subMAC, mainMAC
}
