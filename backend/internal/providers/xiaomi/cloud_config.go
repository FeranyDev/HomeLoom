package xiaomi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/media"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

const (
	defaultCloudPollInterval = 30
	defaultCloudTimeout      = 15
	cloudConnectionAuto      = "auto"
	cloudConnectionLocal     = "local"
	cloudConnectionCloud     = "cloud"
)

// CloudConfig is the durable configuration for the Xiaomi cloud MIoT
// provider. Password and session fields are encrypted by the provider config
// secret codec before the JSON document is stored in the configured database.
type CloudConfig struct {
	// CredentialsRevoked keeps a disabled configuration after the account
	// password/session has been removed by the administrator.
	CredentialsRevoked bool           `json:"credentialsRevoked,omitempty"`
	Region             string         `json:"region"`
	Username           string         `json:"username,omitempty"`
	Password           string         `json:"password,omitempty"`
	UserID             string         `json:"userId,omitempty"`
	Ssecurity          string         `json:"ssecurity,omitempty"`
	ServiceToken       string         `json:"serviceToken,omitempty"`
	PassToken          string         `json:"passToken,omitempty"`
	PollIntervalSec    int            `json:"pollIntervalSeconds,omitempty"`
	RequestTimeoutSec  int            `json:"requestTimeoutSeconds,omitempty"`
	Devices            []DeviceConfig `json:"devices"`
}

func decodeCloudConfig(item providerconfig.Config) (CloudConfig, error) {
	var config CloudConfig
	decoder := json.NewDecoder(strings.NewReader(string(item.Config)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return CloudConfig{}, fmt.Errorf("decode Xiaomi cloud config: %w", err)
	}
	config.applyDefaults()
	if config.CredentialsRevoked {
		if item.Enabled {
			return CloudConfig{}, errors.New("revoked Xiaomi cloud credentials require the provider to be disabled")
		}
		return config, nil
	}
	if err := config.validate(); err != nil {
		return CloudConfig{}, err
	}
	return config, nil
}

func (c *CloudConfig) applyDefaults() {
	c.Region = strings.ToLower(strings.TrimSpace(c.Region))
	c.Username = strings.TrimSpace(c.Username)
	c.UserID = strings.TrimSpace(c.UserID)
	c.Ssecurity = strings.TrimSpace(c.Ssecurity)
	c.ServiceToken = strings.TrimSpace(c.ServiceToken)
	c.PassToken = strings.TrimSpace(c.PassToken)
	if c.Region == "" {
		c.Region = "cn"
	}
	if c.PollIntervalSec == 0 {
		c.PollIntervalSec = defaultCloudPollInterval
	}
	if c.RequestTimeoutSec == 0 {
		c.RequestTimeoutSec = defaultCloudTimeout
	}
	for index := range c.Devices {
		item := &c.Devices[index]
		item.DID, item.ID = strings.TrimSpace(item.DID), strings.TrimSpace(item.ID)
		item.ConnectionMode = strings.ToLower(strings.TrimSpace(item.ConnectionMode))
		if item.ConnectionMode == "" {
			item.ConnectionMode = cloudConnectionAuto
		}
		if item.ID == "" {
			item.ID = "xiaomi-miot-" + stableID(item.DID)
		}
		applyCameraMediaDefaults(item)
		for propertyIndex := range item.Properties {
			mapping := &item.Properties[propertyIndex]
			if mapping.EndpointID == "" {
				mapping.EndpointID = "main"
			}
			if mapping.Name == "" {
				mapping.Name = mapping.PropertyID
			}
		}
		for actionIndex := range item.Actions {
			mapping := &item.Actions[actionIndex]
			if mapping.EndpointID == "" {
				mapping.EndpointID = "main"
			}
			if mapping.Name == "" {
				mapping.Name = mapping.CommandID
			}
		}
	}
}

func (c CloudConfig) validate() error {
	validRegions := map[string]bool{"cn": true, "de": true, "i2": true, "ru": true, "sg": true, "tw": true, "us": true, "in": true}
	if !validRegions[c.Region] {
		return fmt.Errorf("unsupported Xiaomi cloud region %q", c.Region)
	}
	passwordLogin := c.Username != "" && c.Password != ""
	sessionLogin := c.UserID != "" && c.Ssecurity != "" && c.ServiceToken != ""
	if !passwordLogin && !sessionLogin {
		return errors.New("username and password, or userId, ssecurity and serviceToken are required")
	}
	if c.PollIntervalSec < 15 || c.PollIntervalSec > 3600 {
		return errors.New("pollIntervalSeconds must be between 15 and 3600")
	}
	if c.RequestTimeoutSec < 1 || c.RequestTimeoutSec > 120 {
		return errors.New("requestTimeoutSeconds must be between 1 and 120")
	}
	seenDevices := make(map[string]bool)
	for _, item := range c.Devices {
		if item.DID == "" || item.ID == "" || item.Name == "" || item.Type == "" {
			return errors.New("every device requires did, name and type")
		}
		if seenDevices[item.ID] {
			return fmt.Errorf("duplicate Xiaomi cloud device id %q", item.ID)
		}
		seenDevices[item.ID] = true
		if item.ConnectionMode != cloudConnectionAuto && item.ConnectionMode != cloudConnectionLocal && item.ConnectionMode != cloudConnectionCloud {
			return fmt.Errorf("device %q connectionMode must be auto, local or cloud", item.ID)
		}
		if err := validateCameraMediaConfig(item); err != nil {
			return err
		}
		if item.Media != nil && item.Media.Protocol == media.ProtocolXiaomiMISS &&
			(c.UserID == "" || c.PassToken == "") {
			return fmt.Errorf("device %q Xiaomi MISS media requires userId and passToken from Xiaomi password login", item.ID)
		}
		seenProperties := make(map[string]bool)
		for _, mapping := range item.Properties {
			key := mapping.EndpointID + "/" + mapping.CapabilityID + "/" + mapping.PropertyID
			if mapping.CapabilityID == "" || mapping.CapabilityType == "" || mapping.PropertyID == "" || mapping.SIID <= 0 || mapping.PIID <= 0 {
				return fmt.Errorf("device %q has an incomplete property mapping", item.ID)
			}
			if mapping.ValueType != device.ValueTypeBool && mapping.ValueType != device.ValueTypeInt && mapping.ValueType != device.ValueTypeNumber && mapping.ValueType != device.ValueTypeString && mapping.ValueType != device.ValueTypeEnum {
				return fmt.Errorf("device %q property %q has invalid valueType %q", item.ID, key, mapping.ValueType)
			}
			if seenProperties[key] {
				return fmt.Errorf("device %q has duplicate property mapping %q", item.ID, key)
			}
			seenProperties[key] = true
		}
	}
	return nil
}

func (c CloudConfig) pollInterval() time.Duration {
	return time.Duration(c.PollIntervalSec) * time.Second
}
func (c CloudConfig) requestTimeout() time.Duration {
	return time.Duration(c.RequestTimeoutSec) * time.Second
}
