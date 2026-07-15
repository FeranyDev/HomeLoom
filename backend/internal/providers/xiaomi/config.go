package xiaomi

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

const (
	defaultPort         = 8883
	defaultTimeout      = 10
	defaultPollInterval = 60
)

// Config contains the durable Xiaomi central-hub configuration. Credentials
// and PEM material are stored in the provider JSON so SQLite's existing
// recursive secret encryption can protect them without a sidecar YAML/file.
type Config struct {
	Host               string         `json:"host"`
	Port               int            `json:"port,omitempty"`
	ClientID           string         `json:"clientId"`
	CACertificate      string         `json:"caCertificate"`
	ClientCertificate  string         `json:"clientCertificate"`
	PrivateKey         string         `json:"privateKey"`
	ServerName         string         `json:"serverName,omitempty"`
	InsecureSkipVerify bool           `json:"insecureSkipVerify,omitempty"`
	RequestTimeoutSec  int            `json:"requestTimeoutSeconds,omitempty"`
	PollIntervalSec    int            `json:"pollIntervalSeconds,omitempty"`
	Devices            []DeviceConfig `json:"devices"`
	OAuth              *OAuthConfig   `json:"oauth,omitempty"`
}

// OAuthConfig is persisted for token renewal and certificate rotation. The
// local provider does not need an account password and never stores one.
type OAuthConfig struct {
	ClientID     string `json:"clientId"`
	Region       string `json:"region"`
	RedirectURL  string `json:"redirectUrl"`
	OAuthUUID    string `json:"oauthUuid"`
	VirtualDID   string `json:"virtualDid"`
	UID          string `json:"uid,omitempty"`
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	RefreshAfter int64  `json:"refreshAfter,omitempty"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"`
}

type DeviceConfig struct {
	DID        string            `json:"did"`
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name"`
	Type       device.Type       `json:"type"`
	Model      string            `json:"model,omitempty"`
	Room       string            `json:"room,omitempty"`
	Properties []PropertyMapping `json:"properties"`
	Actions    []ActionMapping   `json:"actions,omitempty"`
}

type PropertyMapping struct {
	EndpointID     string           `json:"endpointId,omitempty"`
	CapabilityID   string           `json:"capabilityId"`
	CapabilityType string           `json:"capabilityType"`
	PropertyID     string           `json:"propertyId"`
	Name           string           `json:"name,omitempty"`
	ValueType      device.ValueType `json:"valueType"`
	SIID           int              `json:"siid"`
	PIID           int              `json:"piid"`
	Unit           string           `json:"unit,omitempty"`
	Readable       *bool            `json:"readable,omitempty"`
	Writable       bool             `json:"writable,omitempty"`
	Notifiable     *bool            `json:"notifiable,omitempty"`
	Min            *float64         `json:"min,omitempty"`
	Max            *float64         `json:"max,omitempty"`
	Step           *float64         `json:"step,omitempty"`
	Enum           map[string]any   `json:"enum,omitempty"`
}

type ActionMapping struct {
	EndpointID   string   `json:"endpointId,omitempty"`
	CapabilityID string   `json:"capabilityId"`
	CommandID    string   `json:"commandId"`
	Name         string   `json:"name,omitempty"`
	SIID         int      `json:"siid"`
	AIID         int      `json:"aiid"`
	Parameters   []string `json:"parameters,omitempty"`
}

func decodeConfig(item providerconfig.Config) (Config, *url.URL, *tls.Config, error) {
	var config Config
	decoder := json.NewDecoder(strings.NewReader(string(item.Config)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, nil, nil, fmt.Errorf("decode xiaomi config: %w", err)
	}
	config.applyDefaults()
	brokerURL, err := config.validate()
	if err != nil {
		return Config{}, nil, nil, err
	}
	tlsConfig, err := config.tlsConfig()
	if err != nil {
		return Config{}, nil, nil, err
	}
	return config, brokerURL, tlsConfig, nil
}

func (c *Config) applyDefaults() {
	c.Host = strings.TrimSpace(c.Host)
	c.ClientID = strings.TrimSpace(c.ClientID)
	c.ServerName = strings.TrimSpace(c.ServerName)
	if c.Port == 0 {
		c.Port = defaultPort
	}
	if c.RequestTimeoutSec == 0 {
		c.RequestTimeoutSec = defaultTimeout
	}
	if c.PollIntervalSec == 0 {
		c.PollIntervalSec = defaultPollInterval
	}
	for deviceIndex := range c.Devices {
		item := &c.Devices[deviceIndex]
		item.DID = strings.TrimSpace(item.DID)
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" {
			item.ID = "xiaomi-" + stableID(item.DID)
		}
		legacyType := item.Type
		if legacyType == device.TypeTemperatureSensor || legacyType == device.TypeHumiditySensor {
			item.Type = device.TypeSinglePropertySensor
		}
		for propertyIndex := range item.Properties {
			mapping := &item.Properties[propertyIndex]
			if (legacyType == device.TypeTemperatureSensor && mapping.CapabilityID == "temperature" && mapping.PropertyID == "current-temperature") ||
				(legacyType == device.TypeHumiditySensor && mapping.CapabilityID == "humidity" && mapping.PropertyID == "current-humidity") {
				mapping.CapabilityID, mapping.CapabilityType, mapping.PropertyID = "sensor", "sensor", "value"
			}
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

func (c Config) validate() (*url.URL, error) {
	if c.Host == "" {
		return nil, errors.New("host is required")
	}
	if c.Port < 1 || c.Port > 65535 {
		return nil, errors.New("port must be between 1 and 65535")
	}
	if c.ClientID == "" {
		return nil, errors.New("clientId (virtual DID) is required")
	}
	if _, err := strconv.ParseUint(c.ClientID, 10, 64); err != nil {
		return nil, errors.New("clientId must be a decimal virtual DID")
	}
	if c.CACertificate == "" || c.ClientCertificate == "" || c.PrivateKey == "" {
		return nil, errors.New("caCertificate, clientCertificate and privateKey are required")
	}
	if c.RequestTimeoutSec < 1 || c.RequestTimeoutSec > 120 {
		return nil, errors.New("requestTimeoutSeconds must be between 1 and 120")
	}
	if c.PollIntervalSec < 5 || c.PollIntervalSec > 3600 {
		return nil, errors.New("pollIntervalSeconds must be between 5 and 3600")
	}
	seenDevices := make(map[string]bool)
	for _, item := range c.Devices {
		if item.DID == "" || item.ID == "" || item.Name == "" || item.Type == "" {
			return nil, errors.New("every device requires did, name and type")
		}
		if seenDevices[item.ID] {
			return nil, fmt.Errorf("duplicate Xiaomi device id %q", item.ID)
		}
		seenDevices[item.ID] = true
		seenProperties := make(map[string]bool)
		for _, mapping := range item.Properties {
			key := mapping.EndpointID + "/" + mapping.CapabilityID + "/" + mapping.PropertyID
			if mapping.CapabilityID == "" || mapping.CapabilityType == "" || mapping.PropertyID == "" || mapping.SIID <= 0 || mapping.PIID <= 0 {
				return nil, fmt.Errorf("device %q has an incomplete property mapping", item.ID)
			}
			if mapping.ValueType != device.ValueTypeBool && mapping.ValueType != device.ValueTypeInt && mapping.ValueType != device.ValueTypeNumber && mapping.ValueType != device.ValueTypeString && mapping.ValueType != device.ValueTypeEnum {
				return nil, fmt.Errorf("device %q property %q has invalid valueType %q", item.ID, key, mapping.ValueType)
			}
			if seenProperties[key] {
				return nil, fmt.Errorf("device %q has duplicate property mapping %q", item.ID, key)
			}
			seenProperties[key] = true
		}
	}
	return &url.URL{Scheme: "tls", Host: fmt.Sprintf("%s:%d", c.Host, c.Port)}, nil
}

func (c Config) tlsConfig() (*tls.Config, error) {
	roots, intermediates, err := xiaomiCAPools([]byte(c.CACertificate))
	if err != nil {
		return nil, err
	}
	certificate, err := tls.X509KeyPair([]byte(c.ClientCertificate), []byte(c.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("load Xiaomi client certificate/private key: %w", err)
	}
	result := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{certificate}, ServerName: c.ServerName, InsecureSkipVerify: true} // #nosec G402 -- default verification is implemented below without DNS/IP SAN matching.
	if !c.InsecureSkipVerify {
		result.VerifyConnection = verifyXiaomiServerCertificate(roots, intermediates, time.Now)
	}
	return result, nil
}

func xiaomiCAPools(data []byte) (*x509.CertPool, *x509.CertPool, error) {
	roots, intermediates := x509.NewCertPool(), x509.NewCertPool()
	rootCount, certificateCount := 0, 0
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			break
		}
		data = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("parse Xiaomi CA certificate: %w", err)
		}
		if !certificate.IsCA {
			return nil, nil, errors.New("caCertificate contains a non-CA certificate")
		}
		certificateCount++
		if certificate.CheckSignatureFrom(certificate) == nil {
			roots.AddCert(certificate)
			rootCount++
		} else {
			intermediates.AddCert(certificate)
		}
	}
	if certificateCount == 0 {
		return nil, nil, errors.New("caCertificate contains no valid PEM certificate")
	}
	if rootCount == 0 {
		return nil, nil, errors.New("caCertificate contains no self-signed Xiaomi root CA")
	}
	return roots, intermediates, nil
}

func verifyXiaomiServerCertificate(roots, configuredIntermediates *x509.CertPool, now func() time.Time) func(tls.ConnectionState) error {
	return func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("Xiaomi gateway returned no server certificate")
		}
		intermediates := configuredIntermediates.Clone()
		for _, certificate := range state.PeerCertificates[1:] {
			intermediates.AddCert(certificate)
		}
		_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{
			Roots:         roots,
			Intermediates: intermediates,
			CurrentTime:   now(),
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			// DNSName is intentionally empty: Xiaomi gateway certificates are
			// authenticated by their CA chain and ServerAuth EKU, not LAN DNS/IP SAN.
		})
		if err != nil {
			return fmt.Errorf("verify Xiaomi gateway certificate chain: %w", err)
		}
		return nil
	}
}

func (c Config) requestTimeout() time.Duration {
	return time.Duration(c.RequestTimeoutSec) * time.Second
}
func (c Config) pollInterval() time.Duration { return time.Duration(c.PollIntervalSec) * time.Second }

func stableID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var output strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			output.WriteRune(r)
		} else {
			output.WriteByte('-')
		}
	}
	result := strings.Trim(output.String(), "-._")
	if result == "" {
		return "device"
	}
	if len(result) > 55 {
		result = result[:55]
	}
	return result
}
