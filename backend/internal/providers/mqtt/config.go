package mqtt

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

const (
	defaultTopicPrefix = "homeloom"
	defaultKeepAlive   = 30
	defaultTimeout     = 10
	defaultSessionTTL  = 86400
	defaultRetainedAge = 300
)

type TLSConfig struct {
	CAFile             string `json:"caFile,omitempty"`
	CertFile           string `json:"certFile,omitempty"`
	KeyFile            string `json:"keyFile,omitempty"`
	ServerName         string `json:"serverName,omitempty"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"`
}

type Config struct {
	BrokerURL                  string    `json:"brokerUrl"`
	Username                   string    `json:"username,omitempty"`
	Password                   string    `json:"password,omitempty"`
	ClientID                   string    `json:"clientId,omitempty"`
	TopicPrefix                string    `json:"topicPrefix,omitempty"`
	QoS                        byte      `json:"qos,omitempty"`
	KeepAliveSeconds           int       `json:"keepAliveSeconds,omitempty"`
	ConnectTimeoutSeconds      int       `json:"connectTimeoutSeconds,omitempty"`
	SessionExpirySeconds       uint32    `json:"sessionExpirySeconds,omitempty"`
	RetainedStateMaxAgeSeconds int       `json:"retainedStateMaxAgeSeconds,omitempty"`
	TLS                        TLSConfig `json:"tls,omitempty"`
}

func decodeConfig(item providerconfig.Config) (Config, *url.URL, *tls.Config, error) {
	var config Config
	decoder := json.NewDecoder(strings.NewReader(string(item.Config)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, nil, nil, fmt.Errorf("decode mqtt config: %w", err)
	}
	config.applyDefaults(item.ID)
	brokerURL, err := validateConfig(config)
	if err != nil {
		return Config{}, nil, nil, err
	}
	tlsConfig, err := buildTLSConfig(config, brokerURL)
	if err != nil {
		return Config{}, nil, nil, err
	}
	return config, brokerURL, tlsConfig, nil
}

func (c *Config) applyDefaults(providerID string) {
	c.BrokerURL = strings.TrimSpace(c.BrokerURL)
	c.Username = strings.TrimSpace(c.Username)
	c.ClientID = strings.TrimSpace(c.ClientID)
	c.TopicPrefix = strings.Trim(strings.TrimSpace(c.TopicPrefix), "/")
	if c.ClientID == "" {
		c.ClientID = "homeloom-" + providerID
	}
	if c.TopicPrefix == "" {
		c.TopicPrefix = defaultTopicPrefix
	}
	if c.KeepAliveSeconds == 0 {
		c.KeepAliveSeconds = defaultKeepAlive
	}
	if c.ConnectTimeoutSeconds == 0 {
		c.ConnectTimeoutSeconds = defaultTimeout
	}
	if c.SessionExpirySeconds == 0 {
		c.SessionExpirySeconds = defaultSessionTTL
	}
	if c.RetainedStateMaxAgeSeconds == 0 {
		c.RetainedStateMaxAgeSeconds = defaultRetainedAge
	}
}

func validateConfig(config Config) (*url.URL, error) {
	if config.BrokerURL == "" {
		return nil, errors.New("brokerUrl is required")
	}
	brokerURL, err := url.Parse(config.BrokerURL)
	if err != nil || brokerURL.Host == "" {
		return nil, errors.New("brokerUrl must be an absolute MQTT URL")
	}
	if brokerURL.User != nil {
		return nil, errors.New("brokerUrl must not contain credentials; use username and password")
	}
	if brokerURL.Path != "" && brokerURL.Path != "/" && brokerURL.Scheme != "ws" && brokerURL.Scheme != "wss" {
		return nil, errors.New("brokerUrl path is only supported for WebSocket connections")
	}
	switch brokerURL.Scheme {
	case "mqtt", "tls", "mqtts", "ws", "wss":
	default:
		return nil, errors.New("brokerUrl scheme must be mqtt, tls, mqtts, ws, or wss")
	}
	if brokerURL.Scheme == "mqtts" {
		brokerURL.Scheme = "tls"
	}
	if config.QoS > 2 {
		return nil, errors.New("qos must be 0, 1, or 2")
	}
	if config.KeepAliveSeconds < 5 || config.KeepAliveSeconds > 3600 {
		return nil, errors.New("keepAliveSeconds must be between 5 and 3600")
	}
	if config.ConnectTimeoutSeconds < 1 || config.ConnectTimeoutSeconds > 120 {
		return nil, errors.New("connectTimeoutSeconds must be between 1 and 120")
	}
	if config.RetainedStateMaxAgeSeconds < 1 || config.RetainedStateMaxAgeSeconds > 86400 {
		return nil, errors.New("retainedStateMaxAgeSeconds must be between 1 and 86400")
	}
	if config.ClientID == "" || len(config.ClientID) > 128 || strings.ContainsAny(config.ClientID, "+#\x00") {
		return nil, errors.New("clientId must be 1-128 characters and cannot contain MQTT wildcards")
	}
	if strings.ContainsAny(config.TopicPrefix, "+#\x00") || hasEmptyTopicLevel(config.TopicPrefix) {
		return nil, errors.New("topicPrefix must contain non-empty levels without MQTT wildcards")
	}
	if (config.TLS.CertFile == "") != (config.TLS.KeyFile == "") {
		return nil, errors.New("tls.certFile and tls.keyFile must be configured together")
	}
	return brokerURL, nil
}

func hasEmptyTopicLevel(value string) bool {
	for _, level := range strings.Split(value, "/") {
		if strings.TrimSpace(level) == "" {
			return true
		}
	}
	return false
}

func buildTLSConfig(config Config, brokerURL *url.URL) (*tls.Config, error) {
	tlsEnabled := brokerURL.Scheme == "tls" || brokerURL.Scheme == "wss"
	configured := config.TLS.CAFile != "" || config.TLS.CertFile != "" || config.TLS.KeyFile != "" || config.TLS.ServerName != "" || config.TLS.InsecureSkipVerify
	if !tlsEnabled && configured {
		return nil, errors.New("tls options require a tls, mqtts, or wss brokerUrl")
	}
	if !tlsEnabled {
		return nil, nil
	}
	result := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: config.TLS.ServerName, InsecureSkipVerify: config.TLS.InsecureSkipVerify} // #nosec G402 -- explicit user option for private brokers.
	if config.TLS.CAFile != "" {
		content, err := os.ReadFile(config.TLS.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read tls.caFile: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(content) {
			return nil, errors.New("tls.caFile does not contain a valid PEM certificate")
		}
		result.RootCAs = pool
	}
	if config.TLS.CertFile != "" {
		certificate, err := tls.LoadX509KeyPair(config.TLS.CertFile, config.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load mqtt client certificate: %w", err)
		}
		result.Certificates = []tls.Certificate{certificate}
	}
	return result, nil
}

func (c Config) connectTimeout() time.Duration {
	return time.Duration(c.ConnectTimeoutSeconds) * time.Second
}

func (c Config) retainedStateMaxAge() time.Duration {
	return time.Duration(c.RetainedStateMaxAgeSeconds) * time.Second
}
