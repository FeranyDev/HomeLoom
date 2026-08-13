package network

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

const (
	ProviderType = "network"

	defaultProbeInterval = 30 * time.Second
	defaultProbeTimeout  = 3 * time.Second
	defaultWOLAddress    = "255.255.255.255"
	defaultWOLPort       = 9
)

// Config owns the durable catalog for a LAN power-state Provider. Devices can
// override each timing and Wake-on-LAN option so a sleeping computer does not
// have to share the probing policy of an always-on NAS.
type Config struct {
	Devices              []DeviceConfig `json:"devices"`
	ProbeIntervalSeconds int            `json:"probeIntervalSeconds,omitempty"`
	ProbeTimeoutSeconds  int            `json:"probeTimeoutSeconds,omitempty"`
	OnlineThreshold      int            `json:"onlineThreshold,omitempty"`
	OfflineThreshold     int            `json:"offlineThreshold,omitempty"`
	WOLBroadcastAddress  string         `json:"wolBroadcastAddress,omitempty"`
	WOLPort              int            `json:"wolPort,omitempty"`
	WOLInterface         string         `json:"wolInterface,omitempty"`
}

// DeviceConfig identifies one independently monitored network device. MAC is
// optional for a monitor-only device and required only when wake is invoked.
type DeviceConfig struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Host                 string `json:"host"`
	ProbePort            int    `json:"probePort"`
	MAC                  string `json:"mac,omitempty"`
	ProbeIntervalSeconds int    `json:"probeIntervalSeconds,omitempty"`
	ProbeTimeoutSeconds  int    `json:"probeTimeoutSeconds,omitempty"`
	OnlineThreshold      int    `json:"onlineThreshold,omitempty"`
	OfflineThreshold     int    `json:"offlineThreshold,omitempty"`
	WOLBroadcastAddress  string `json:"wolBroadcastAddress,omitempty"`
	WOLPort              int    `json:"wolPort,omitempty"`
	WOLInterface         string `json:"wolInterface,omitempty"`
}

type monitoredDevice struct {
	DeviceConfig
	mac              net.HardwareAddr
	probeInterval    time.Duration
	probeTimeout     time.Duration
	onlineThreshold  int
	offlineThreshold int
	wolAddress       string
	wolPort          int
	wolInterface     string
}

func decodeConfig(item providerconfig.Config) (Config, error) {
	var config Config
	if len(item.Config) > 0 {
		if err := json.Unmarshal(item.Config, &config); err != nil {
			return Config{}, fmt.Errorf("decode network provider config: %w", err)
		}
	}
	if err := normalizeConfig(item, &config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func normalizeConfig(item providerconfig.Config, config *Config) error {
	if !device.ValidStableID(item.ID) {
		return errors.New("network provider id must be a stable lowercase id")
	}
	if strings.TrimSpace(item.Name) == "" {
		return errors.New("network provider name is required")
	}
	if config == nil {
		return errors.New("network provider config is required")
	}
	if len(config.Devices) == 0 {
		return errors.New("network provider requires at least one device")
	}
	if err := validateSeconds("probeIntervalSeconds", config.ProbeIntervalSeconds, 1, 3600); err != nil {
		return err
	}
	if err := validateSeconds("probeTimeoutSeconds", config.ProbeTimeoutSeconds, 1, 120); err != nil {
		return err
	}
	if err := validateThreshold("onlineThreshold", config.OnlineThreshold); err != nil {
		return err
	}
	if err := validateThreshold("offlineThreshold", config.OfflineThreshold); err != nil {
		return err
	}
	if err := validateWOL(config.WOLBroadcastAddress, config.WOLPort); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(config.Devices))
	for index := range config.Devices {
		configured := &config.Devices[index]
		configured.ID = strings.TrimSpace(configured.ID)
		configured.Name = strings.TrimSpace(configured.Name)
		configured.Host = strings.TrimSpace(configured.Host)
		if !device.ValidStableID(configured.ID) {
			return fmt.Errorf("network device %d has an invalid id", index+1)
		}
		if _, exists := seen[configured.ID]; exists {
			return fmt.Errorf("network device id %q is duplicated", configured.ID)
		}
		seen[configured.ID] = struct{}{}
		if configured.Name == "" || configured.Host == "" {
			return fmt.Errorf("network device %q requires name and host", configured.ID)
		}
		if configured.ProbePort < 1 || configured.ProbePort > 65535 {
			return fmt.Errorf("network device %q probePort must be between 1 and 65535", configured.ID)
		}
		if err := validateSeconds("network device "+configured.ID+" probeIntervalSeconds", configured.ProbeIntervalSeconds, 1, 3600); err != nil {
			return err
		}
		if err := validateSeconds("network device "+configured.ID+" probeTimeoutSeconds", configured.ProbeTimeoutSeconds, 1, 120); err != nil {
			return err
		}
		if err := validateThreshold("network device "+configured.ID+" onlineThreshold", configured.OnlineThreshold); err != nil {
			return err
		}
		if err := validateThreshold("network device "+configured.ID+" offlineThreshold", configured.OfflineThreshold); err != nil {
			return err
		}
		if err := validateWOL(configured.WOLBroadcastAddress, configured.WOLPort); err != nil {
			return fmt.Errorf("network device %q: %w", configured.ID, err)
		}
		if configured.MAC != "" {
			mac, err := net.ParseMAC(configured.MAC)
			if err != nil || len(mac) != 6 {
				return fmt.Errorf("network device %q has an invalid MAC address", configured.ID)
			}
			configured.MAC = strings.ToUpper(mac.String())
		}
	}
	return nil
}

func validateSeconds(field string, value, minimum, maximum int) error {
	if value == 0 {
		return nil
	}
	if value < minimum || value > maximum {
		return fmt.Errorf("%s must be between %d and %d", field, minimum, maximum)
	}
	return nil
}

func validateThreshold(field string, value int) error {
	if value == 0 {
		return nil
	}
	if value < 1 || value > 100 {
		return fmt.Errorf("%s must be between 1 and 100", field)
	}
	return nil
}

func validateWOL(address string, port int) error {
	if port != 0 && (port < 1 || port > 65535) {
		return errors.New("wolPort must be between 1 and 65535")
	}
	if strings.TrimSpace(address) == "" {
		return nil
	}
	ip := net.ParseIP(strings.TrimSpace(address))
	if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return errors.New("wolBroadcastAddress must be a usable IPv4 address")
	}
	return nil
}

func materializeDevices(config Config) ([]monitoredDevice, error) {
	interval := secondsOrDefault(config.ProbeIntervalSeconds, defaultProbeInterval)
	timeout := secondsOrDefault(config.ProbeTimeoutSeconds, defaultProbeTimeout)
	onlineThreshold := thresholdOrDefault(config.OnlineThreshold)
	offlineThreshold := thresholdOrDefault(config.OfflineThreshold)
	wolAddress := stringOrDefault(config.WOLBroadcastAddress, defaultWOLAddress)
	wolPort := intOrDefault(config.WOLPort, defaultWOLPort)
	devices := make([]monitoredDevice, 0, len(config.Devices))
	for _, item := range config.Devices {
		configured := monitoredDevice{
			DeviceConfig:     item,
			probeInterval:    secondsOrDefault(item.ProbeIntervalSeconds, interval),
			probeTimeout:     secondsOrDefault(item.ProbeTimeoutSeconds, timeout),
			onlineThreshold:  thresholdOrDefaultWith(item.OnlineThreshold, onlineThreshold),
			offlineThreshold: thresholdOrDefaultWith(item.OfflineThreshold, offlineThreshold),
			wolAddress:       stringOrDefault(item.WOLBroadcastAddress, wolAddress),
			wolPort:          intOrDefault(item.WOLPort, wolPort),
			wolInterface:     stringOrDefault(item.WOLInterface, config.WOLInterface),
		}
		if item.MAC != "" {
			configured.mac, _ = net.ParseMAC(item.MAC)
		}
		devices = append(devices, configured)
	}
	return devices, nil
}

func secondsOrDefault(value int, fallback time.Duration) time.Duration {
	if value == 0 {
		return fallback
	}
	return time.Duration(value) * time.Second
}

func thresholdOrDefault(value int) int {
	return thresholdOrDefaultWith(value, 1)
}

func thresholdOrDefaultWith(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func stringOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func intOrDefault(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}
