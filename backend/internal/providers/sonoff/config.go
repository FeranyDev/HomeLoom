package sonoff

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

const (
	ProviderType = "sonoff"
	ModeAuto     = "auto"
	ModeLocal    = "local"
	ModeCloud    = "cloud"
)

const (
	defaultRequestTimeoutSeconds = 10
	defaultRefreshInterval       = 60
	defaultDiscoveryTimeout      = 5
	defaultLANPort               = 8081
)

// Config is deliberately explicit about the two transports. Account
// credentials are retained so the cloud client can re-login after an access
// token expires; all password/token/device-key fields are protected by the
// application's provider-secret codec in production storage.
type Config struct {
	Mode                  string         `json:"mode,omitempty"`
	Region                string         `json:"region,omitempty"`
	ManagedDevices        bool           `json:"managedDevices,omitempty"`
	RequestTimeoutSeconds int            `json:"requestTimeoutSeconds,omitempty"`
	RefreshIntervalSec    int            `json:"refreshIntervalSeconds,omitempty"`
	DiscoveryTimeoutSec   int            `json:"discoveryTimeoutSeconds,omitempty"`
	Cloud                 CloudConfig    `json:"cloud,omitempty"`
	Devices               []DeviceConfig `json:"devices"`
}

type CloudConfig struct {
	Endpoint          string `json:"endpoint,omitempty"`
	AccessToken       string `json:"accessToken,omitempty"`
	Username          string `json:"username,omitempty"`
	Password          string `json:"password,omitempty"`
	CountryCode       string `json:"countryCode,omitempty"`
	AppID             string `json:"appId,omitempty"`
	AppSecret         string `json:"appSecret,omitempty"`
	WebSocketEndpoint string `json:"websocketEndpoint,omitempty"`
}

type DeviceConfig struct {
	ID        string         `json:"id"`
	DeviceID  string         `json:"deviceId"`
	Name      string         `json:"name"`
	Model     string         `json:"model,omitempty"`
	UIID      int            `json:"uiid"`
	Type      string         `json:"type,omitempty"`
	HomeID    string         `json:"homeId,omitempty"`
	HomeName  string         `json:"homeName,omitempty"`
	RoomID    string         `json:"roomId,omitempty"`
	RoomName  string         `json:"roomName,omitempty"`
	DeviceKey string         `json:"deviceKey,omitempty"`
	DIY       bool           `json:"diy,omitempty"`
	Host      string         `json:"host,omitempty"`
	Port      int            `json:"port,omitempty"`
	Channels  int            `json:"channels,omitempty"`
	Online    *bool          `json:"online,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
}

func decodeConfig(item providerconfig.Config) (Config, error) {
	var config Config
	decoder := json.NewDecoder(strings.NewReader(string(item.Config)))
	decoder.DisallowUnknownFields()
	if len(item.Config) != 0 {
		if err := decoder.Decode(&config); err != nil {
			return Config{}, fmt.Errorf("decode Sonoff config: %w", err)
		}
	}
	config.applyDefaults()
	if err := config.validate(item.ID); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c *Config) applyDefaults() {
	c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	if c.Mode == "" {
		c.Mode = ModeAuto
	}
	c.Region = strings.ToLower(strings.TrimSpace(c.Region))
	if c.Region == "" {
		c.Region = "auto"
	}
	if c.RequestTimeoutSeconds == 0 {
		c.RequestTimeoutSeconds = defaultRequestTimeoutSeconds
	}
	if c.RefreshIntervalSec == 0 {
		c.RefreshIntervalSec = defaultRefreshInterval
	}
	if c.DiscoveryTimeoutSec == 0 {
		c.DiscoveryTimeoutSec = defaultDiscoveryTimeout
	}
	c.Cloud.Endpoint = strings.TrimRight(strings.TrimSpace(c.Cloud.Endpoint), "/")
	c.Cloud.AccessToken = strings.TrimSpace(c.Cloud.AccessToken)
	c.Cloud.Username = strings.TrimSpace(c.Cloud.Username)
	c.Cloud.Password = strings.TrimSpace(c.Cloud.Password)
	c.Cloud.CountryCode = strings.TrimSpace(c.Cloud.CountryCode)
	c.Cloud.AppID = strings.TrimSpace(c.Cloud.AppID)
	c.Cloud.AppSecret = strings.TrimSpace(c.Cloud.AppSecret)
	c.Cloud.WebSocketEndpoint = strings.TrimSpace(c.Cloud.WebSocketEndpoint)
	if c.Cloud.CountryCode == "" {
		c.Cloud.CountryCode = "+86"
	}
	for index := range c.Devices {
		item := &c.Devices[index]
		item.ID = strings.TrimSpace(item.ID)
		item.DeviceID = strings.TrimSpace(item.DeviceID)
		item.Name = strings.TrimSpace(item.Name)
		item.Model = strings.TrimSpace(item.Model)
		item.Type = strings.TrimSpace(item.Type)
		item.HomeID = strings.TrimSpace(item.HomeID)
		item.HomeName = strings.TrimSpace(item.HomeName)
		item.RoomID = strings.TrimSpace(item.RoomID)
		item.RoomName = strings.TrimSpace(item.RoomName)
		item.DeviceKey = strings.TrimSpace(item.DeviceKey)
		item.Host = strings.TrimSpace(item.Host)
		if item.Port == 0 {
			item.Port = defaultLANPort
		}
		if item.Channels == 0 {
			item.Channels = 1
		}
		if item.Params == nil {
			item.Params = make(map[string]any)
		}
	}
}

func (c Config) validate(providerID string) error {
	if providerID == "" {
		return errors.New("provider id is required")
	}
	if c.Mode != ModeAuto && c.Mode != ModeLocal && c.Mode != ModeCloud {
		return fmt.Errorf("mode must be auto, local or cloud")
	}
	if c.Region != "auto" && c.Region != "cn" && c.Region != "as" && c.Region != "us" && c.Region != "eu" {
		return fmt.Errorf("region must be auto, cn, as, us or eu")
	}
	if c.RequestTimeoutSeconds < 1 || c.RequestTimeoutSeconds > 120 {
		return errors.New("requestTimeoutSeconds must be between 1 and 120")
	}
	if c.RefreshIntervalSec < 15 || c.RefreshIntervalSec > 86400 {
		return errors.New("refreshIntervalSeconds must be between 15 and 86400")
	}
	if c.DiscoveryTimeoutSec < 1 || c.DiscoveryTimeoutSec > 30 {
		return errors.New("discoveryTimeoutSeconds must be between 1 and 30")
	}
	cloudConfigured := c.Mode == ModeCloud || c.Cloud.Endpoint != "" || c.Cloud.AccessToken != "" || c.Cloud.Username != "" || c.Cloud.Password != "" || c.Cloud.WebSocketEndpoint != ""
	if cloudConfigured {
		if c.Cloud.Endpoint != "" {
			parsed, err := url.Parse(c.Cloud.Endpoint)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				return errors.New("cloud.endpoint must be an absolute https URL")
			}
		}
		if c.Cloud.WebSocketEndpoint != "" {
			parsed, err := url.Parse(c.Cloud.WebSocketEndpoint)
			if err != nil || parsed.Scheme != "wss" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				return errors.New("cloud.websocketEndpoint must be an absolute wss URL without user information, query, or fragment")
			}
		}
		if c.Cloud.AccessToken == "" && (c.Cloud.Username == "" || c.Cloud.Password == "") {
			return errors.New("cloud.accessToken or cloud.username/cloud.password is required when cloud transport is configured")
		}
	}
	seen := make(map[string]struct{}, len(c.Devices))
	for index, item := range c.Devices {
		if item.ID == "" {
			return fmt.Errorf("devices[%d].id is required", index)
		}
		if item.DeviceID == "" {
			return fmt.Errorf("devices[%d].deviceId is required", index)
		}
		if item.Name == "" {
			return fmt.Errorf("devices[%d].name is required", index)
		}
		if _, exists := seen[item.ID]; exists {
			return fmt.Errorf("duplicate Sonoff device id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		if item.UIID < 0 {
			return fmt.Errorf("devices[%d].uiid must not be negative", index)
		}
		if item.Port < 1 || item.Port > 65535 {
			return fmt.Errorf("devices[%d].port must be between 1 and 65535", index)
		}
		if item.Channels < 1 || item.Channels > 32 {
			return fmt.Errorf("devices[%d].channels must be between 1 and 32", index)
		}
		if c.Mode == ModeLocal && !item.DIY && item.DeviceKey == "" {
			return fmt.Errorf("devices[%d].deviceKey is required for local mode", index)
		}
		if item.Host == "" && c.Mode == ModeLocal {
			return fmt.Errorf("devices[%d].host is required for local mode", index)
		}
	}
	return nil
}
