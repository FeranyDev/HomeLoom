package tuya

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	ProviderType = "tuya"

	defaultBaseURL        = "https://openapi.tuyaus.com"
	defaultRequestTimeout = 15 * time.Second
	defaultPollInterval   = 6 * time.Hour
	minimumPollInterval   = 30 * time.Second
	maximumPollInterval   = 24 * time.Hour
	maximumRequestTimeout = 120 * time.Second
	maximumPageSize       = 100
	defaultMQTTKeepAlive  = 60
	defaultMQTTQoS        = byte(1)
)

// Config is one Tuya cloud account. OpenAPI credentials or Home Assistant's
// User Code/device-sharing session are stored in the provider config so
// HomeLoom's existing recursive secret protection handles them together with
// the other provider credentials.
//
// The account is intentionally not modelled as one provider per device. The
// device directory and each device's dynamic specification belong to the
// account provider and are reconciled in place.
type Config struct {
	AuthType          string        `json:"authType,omitempty"`
	BaseURL           string        `json:"baseUrl,omitempty"`
	Endpoint          string        `json:"endpoint,omitempty"`
	Region            string        `json:"region,omitempty"`
	AccessID          string        `json:"accessId"`
	AccessSecret      string        `json:"accessSecret"`
	UID               string        `json:"uid"`
	UserCode          string        `json:"userCode,omitempty"`
	ClientID          string        `json:"clientId,omitempty"`
	TerminalID        string        `json:"terminalId,omitempty"`
	AccessToken       string        `json:"accessToken,omitempty"`
	RefreshToken      string        `json:"refreshToken,omitempty"`
	TokenExpiresAt    time.Time     `json:"tokenExpiresAt,omitempty"`
	RequestTimeoutSec int           `json:"requestTimeoutSeconds,omitempty"`
	PollIntervalSec   int           `json:"pollIntervalSeconds,omitempty"`
	MQTT              *MQTTConfig   `json:"mqtt,omitempty"`
	Quirks            []QuirkConfig `json:"quirks,omitempty"`
}

// MQTTConfig is an optional already-authorized Tuya message channel. Tuya's
// cloud API exposes the temporary MQTT credentials through the app-specific
// device.openHubConfig action rather than the ordinary OpenAPI resource API;
// callers may supply those short-lived values here. HTTP polling remains the
// authoritative recovery path when MQTT is disabled or disconnected.
type MQTTConfig struct {
	Enabled      bool      `json:"enabled,omitempty"`
	URL          string    `json:"url"`
	Username     string    `json:"username"`
	Password     string    `json:"password"`
	ClientID     string    `json:"clientId"`
	SourceTopic  string    `json:"sourceTopic"`
	AccessKey    string    `json:"accessKey,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
	KeepAliveSec int       `json:"keepAliveSeconds,omitempty"`
	QoS          *byte     `json:"qos,omitempty"`
}

type QuirkConfig struct {
	ProductID string       `json:"productId"`
	Patches   []QuirkPatch `json:"patches,omitempty"`
}

type QuirkPatch struct {
	Operation string   `json:"operation"`
	Code      string   `json:"code"`
	NewCode   string   `json:"newCode,omitempty"`
	Type      DPType   `json:"type,omitempty"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	Step      *float64 `json:"step,omitempty"`
	Scale     *int     `json:"scale,omitempty"`
	Unit      string   `json:"unit,omitempty"`
	Readable  *bool    `json:"readable,omitempty"`
	Writable  *bool    `json:"writable,omitempty"`
	Enum      []string `json:"enum,omitempty"`
}

func (c *Config) applyDefaults() {
	c.AuthType = strings.ToLower(strings.TrimSpace(c.AuthType))
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.Endpoint = strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if c.BaseURL == "" {
		c.BaseURL = regionBaseURL(c.Region)
	}
	if c.RequestTimeoutSec == 0 {
		c.RequestTimeoutSec = int(defaultRequestTimeout / time.Second)
	}
	if c.PollIntervalSec == 0 {
		c.PollIntervalSec = int(defaultPollInterval / time.Second)
	}
	c.UID = strings.TrimSpace(c.UID)
	c.AccessID = strings.TrimSpace(c.AccessID)
	c.UserCode = strings.TrimSpace(c.UserCode)
	c.ClientID = strings.TrimSpace(c.ClientID)
	c.TerminalID = strings.TrimSpace(c.TerminalID)
	if c.MQTT != nil {
		c.MQTT.URL = strings.TrimSpace(c.MQTT.URL)
		c.MQTT.Username = strings.TrimSpace(c.MQTT.Username)
		c.MQTT.ClientID = strings.TrimSpace(c.MQTT.ClientID)
		c.MQTT.SourceTopic = strings.TrimSpace(c.MQTT.SourceTopic)
		if c.MQTT.KeepAliveSec == 0 {
			c.MQTT.KeepAliveSec = defaultMQTTKeepAlive
		}
		if c.MQTT.QoS == nil {
			qos := defaultMQTTQoS
			c.MQTT.QoS = &qos
		}
	}
}

func regionBaseURL(region string) string {
	switch strings.ToLower(strings.TrimSpace(region)) {
	case "cn", "china":
		return "https://openapi.tuyacn.com"
	case "eu", "europe":
		return "https://openapi.tuyaeu.com"
	case "in", "india":
		return "https://openapi.tuyain.com"
	case "sg", "asia", "asia-pacific":
		return "https://openapi-ueaz.tuyaus.com"
	case "us", "america", "us-east":
		return "https://openapi.tuyaus.com"
	default:
		return defaultBaseURL
	}
}

func (c Config) validate(providerID string) error {
	if strings.TrimSpace(providerID) == "" {
		return errors.New("tuya provider id is required")
	}
	if strings.TrimSpace(c.UID) == "" {
		return errors.New("tuya uid is required")
	}
	if c.usesSharing() {
		if c.UserCode == "" || c.AccessToken == "" || c.RefreshToken == "" || c.TerminalID == "" {
			return errors.New("tuya sharing login requires userCode, terminalId, accessToken and refreshToken")
		}
		parsed, err := url.Parse(c.Endpoint)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return errors.New("tuya sharing endpoint must be an absolute HTTPS URL")
		}
	} else {
		if strings.TrimSpace(c.AccessID) == "" || strings.TrimSpace(c.AccessSecret) == "" {
			return errors.New("tuya accessId and accessSecret are required")
		}
		parsed, err := url.Parse(c.BaseURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return errors.New("tuya baseUrl must be an absolute HTTPS URL")
		}
	}
	if c.RequestTimeoutSec < 1 || time.Duration(c.RequestTimeoutSec)*time.Second > maximumRequestTimeout {
		return fmt.Errorf("tuya requestTimeoutSeconds must be between 1 and %d", int(maximumRequestTimeout/time.Second))
	}
	if c.PollIntervalSec < int(minimumPollInterval/time.Second) || time.Duration(c.PollIntervalSec)*time.Second > maximumPollInterval {
		return fmt.Errorf("tuya pollIntervalSeconds must be between %d and %d", int(minimumPollInterval/time.Second), int(maximumPollInterval/time.Second))
	}
	if c.MQTT != nil && c.MQTT.Enabled {
		if c.MQTT.URL == "" || c.MQTT.Username == "" || c.MQTT.Password == "" || c.MQTT.ClientID == "" || c.MQTT.SourceTopic == "" {
			return errors.New("enabled tuya mqtt requires url, username, password, clientId and sourceTopic")
		}
		mqttURL, parseErr := url.Parse(c.MQTT.URL)
		if parseErr != nil || mqttURL.Host == "" || (mqttURL.Scheme != "mqtt" && mqttURL.Scheme != "mqtts") {
			return errors.New("tuya mqtt url must be an absolute MQTT URL")
		}
		if c.MQTT.KeepAliveSec < 5 || c.MQTT.KeepAliveSec > 3600 {
			return errors.New("tuya mqtt keepAliveSeconds must be between 5 and 3600")
		}
		if c.MQTT.QoS == nil || *c.MQTT.QoS > 2 {
			return errors.New("tuya mqtt qos must be 0, 1 or 2")
		}
	}
	return nil
}

func (c Config) usesSharing() bool {
	return c.AuthType == "sharing" || c.AuthType == "homeassistant"
}

func decodeConfig(itemID string, raw json.RawMessage) (Config, error) {
	var config Config
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &config); err != nil {
			return Config{}, fmt.Errorf("decode tuya config: %w", err)
		}
	}
	config.applyDefaults()
	if err := config.validate(itemID); err != nil {
		return Config{}, err
	}
	return config, nil
}
