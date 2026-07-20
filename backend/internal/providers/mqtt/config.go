package mqtt

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

const (
	ModeClient = "client"
	ModeServer = "server"

	defaultKeepAlive   = 30
	defaultTimeout     = 10
	defaultSessionTTL  = 86400
	defaultRetainedAge = 300
	defaultDeviceQoS   = byte(1)
)

type TLSConfig struct {
	CAFile             string `json:"caFile,omitempty"`
	CertFile           string `json:"certFile,omitempty"`
	KeyFile            string `json:"keyFile,omitempty"`
	ServerName         string `json:"serverName,omitempty"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"`
}

type DeviceTopics struct {
	Discovery    string `json:"discovery,omitempty"`
	Availability string `json:"availability,omitempty"`
	State        string `json:"state,omitempty"`
	Command      string `json:"command,omitempty"`
}

type DeviceConfig struct {
	ID          string       `json:"id"`
	TopicPrefix string       `json:"topicPrefix"`
	Protocol    string       `json:"protocol,omitempty"`
	QoS         *byte        `json:"qos,omitempty"`
	Topics      DeviceTopics `json:"topics,omitempty"`
}

func (c DeviceConfig) effectiveQoS() byte {
	if c.QoS == nil {
		return defaultDeviceQoS
	}
	return *c.QoS
}

type Config struct {
	Mode                       string         `json:"mode,omitempty"`
	BrokerURL                  string         `json:"brokerUrl"`
	ListenAddress              string         `json:"listenAddress,omitempty"`
	Username                   string         `json:"username,omitempty"`
	Password                   string         `json:"password,omitempty"`
	ClientID                   string         `json:"clientId,omitempty"`
	KeepAliveSeconds           int            `json:"keepAliveSeconds,omitempty"`
	ConnectTimeoutSeconds      int            `json:"connectTimeoutSeconds,omitempty"`
	SessionExpirySeconds       uint32         `json:"sessionExpirySeconds,omitempty"`
	RetainedStateMaxAgeSeconds int            `json:"retainedStateMaxAgeSeconds,omitempty"`
	TLS                        TLSConfig      `json:"tls,omitempty"`
	Devices                    []DeviceConfig `json:"devices,omitempty"`
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
	c.Mode = strings.TrimSpace(c.Mode)
	if c.Mode == "" {
		c.Mode = ModeClient
	}
	c.BrokerURL = strings.TrimSpace(c.BrokerURL)
	c.ListenAddress = strings.TrimSpace(c.ListenAddress)
	c.Username = strings.TrimSpace(c.Username)
	c.ClientID = strings.TrimSpace(c.ClientID)
	if c.Mode == ModeClient && c.ClientID == "" {
		c.ClientID = "homeloom-" + providerID
	}
	if c.Mode == ModeServer && c.ListenAddress == "" {
		c.ListenAddress = "127.0.0.1:1883"
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
	for index := range c.Devices {
		item := &c.Devices[index]
		item.ID = strings.TrimSpace(item.ID)
		item.TopicPrefix = strings.Trim(strings.TrimSpace(item.TopicPrefix), "/")
		item.Protocol = strings.TrimSpace(item.Protocol)
		if item.Protocol == "" {
			item.Protocol = "homeloom-v1"
		}
		if item.QoS == nil {
			value := defaultDeviceQoS
			item.QoS = &value
		}
		item.Topics.applyDefaults(item.TopicPrefix, item.ID)
	}
}

func (t *DeviceTopics) applyDefaults(prefix, deviceID string) {
	t.Discovery = strings.TrimSpace(t.Discovery)
	t.Availability = strings.TrimSpace(t.Availability)
	t.State = strings.TrimSpace(t.State)
	t.Command = strings.TrimSpace(t.Command)
	if t.Discovery == "" {
		t.Discovery = discoveryTopic(prefix, deviceID)
	}
	if t.Availability == "" {
		t.Availability = availabilityTopic(prefix, deviceID)
	}
	if t.State == "" {
		t.State = stateTopicTemplate(prefix, deviceID)
	}
	if t.Command == "" {
		t.Command = commandTopicTemplate(prefix, deviceID)
	}
}

func validateConfig(config Config) (*url.URL, error) {
	if config.Mode != ModeClient && config.Mode != ModeServer {
		return nil, errors.New("mode must be client or server")
	}
	var brokerURL *url.URL
	if config.Mode == ModeClient {
		if config.BrokerURL == "" {
			return nil, errors.New("brokerUrl is required in client mode")
		}
		var err error
		brokerURL, err = url.Parse(config.BrokerURL)
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
		if config.ClientID == "" || len(config.ClientID) > 128 || strings.ContainsAny(config.ClientID, "+#\x00") {
			return nil, errors.New("clientId must be 1-128 characters and cannot contain MQTT wildcards")
		}
	} else {
		if config.BrokerURL != "" {
			return nil, errors.New("brokerUrl is not used in server mode; use listenAddress")
		}
		if err := validateListenAddress(config.ListenAddress); err != nil {
			return nil, err
		}
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
	if err := validateDeviceConfigs(config.Devices); err != nil {
		return nil, err
	}
	if (config.TLS.CertFile == "") != (config.TLS.KeyFile == "") {
		return nil, errors.New("tls.certFile and tls.keyFile must be configured together")
	}
	return brokerURL, nil
}

func validateListenAddress(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("listenAddress must be a TCP host:port address")
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return errors.New("listenAddress port must be between 1 and 65535")
	}
	return nil
}

func validateDeviceConfigs(items []DeviceConfig) error {
	ids := make(map[string]struct{}, len(items))
	type ownedFilter struct{ topic, owner string }
	inboundTopics := make([]ownedFilter, 0, len(items)*3)
	commandTopics := make([]ownedFilter, 0, len(items))
	for index, item := range items {
		field := fmt.Sprintf("devices[%d]", index)
		if !validTopicID(item.ID) {
			return fmt.Errorf("%s.id must be a stable lowercase device id", field)
		}
		if _, duplicate := ids[item.ID]; duplicate {
			return fmt.Errorf("%s.id duplicates device %q", field, item.ID)
		}
		ids[item.ID] = struct{}{}
		if item.TopicPrefix == "" || strings.ContainsAny(item.TopicPrefix, "+#{}\x00") || hasEmptyTopicLevel(item.TopicPrefix) {
			return fmt.Errorf("%s.topicPrefix must contain non-empty levels without MQTT wildcards", field)
		}
		if item.Protocol != "homeloom-v1" {
			return fmt.Errorf("%s.protocol must be homeloom-v1", field)
		}
		if item.QoS == nil || *item.QoS > 2 {
			return fmt.Errorf("%s.qos must be 0, 1, or 2", field)
		}
		if err := validateExactTopic(item.Topics.Discovery); err != nil {
			return fmt.Errorf("%s.topics.discovery: %w", field, err)
		}
		if err := validateExactTopic(item.Topics.Availability); err != nil {
			return fmt.Errorf("%s.topics.availability: %w", field, err)
		}
		if err := validateTopicTemplate(item.Topics.State, stateTopicTokens); err != nil {
			return fmt.Errorf("%s.topics.state: %w", field, err)
		}
		if err := validateTopicTemplate(item.Topics.Command, commandTopicTokens); err != nil {
			return fmt.Errorf("%s.topics.command: %w", field, err)
		}
		for _, topic := range []string{item.Topics.Discovery, item.Topics.Availability, topicSubscription(item.Topics.State)} {
			for _, existing := range inboundTopics {
				if topicFiltersOverlap(topic, existing.topic) {
					return fmt.Errorf("%s MQTT subscription %q conflicts with %q from device %q", field, topic, existing.topic, existing.owner)
				}
			}
			inboundTopics = append(inboundTopics, ownedFilter{topic: topic, owner: item.ID})
		}
		commandFilter := topicSubscription(item.Topics.Command)
		for _, existing := range commandTopics {
			if topicFiltersOverlap(commandFilter, existing.topic) {
				return fmt.Errorf("%s command topic template conflicts with device %q", field, existing.owner)
			}
		}
		commandTopics = append(commandTopics, ownedFilter{topic: commandFilter, owner: item.ID})
	}
	for _, command := range commandTopics {
		for _, inbound := range inboundTopics {
			if topicFiltersOverlap(command.topic, inbound.topic) {
				return fmt.Errorf("device %q command topic conflicts with inbound subscription from device %q", command.owner, inbound.owner)
			}
		}
	}
	return nil
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
	if config.Mode == ModeServer {
		return buildServerTLSConfig(config)
	}
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

func buildServerTLSConfig(config Config) (*tls.Config, error) {
	if config.TLS.ServerName != "" || config.TLS.InsecureSkipVerify {
		return nil, errors.New("tls.serverName and tls.insecureSkipVerify are only valid in client mode")
	}
	if config.TLS.CertFile == "" && config.TLS.KeyFile == "" && config.TLS.CAFile == "" {
		return nil, nil
	}
	if config.TLS.CertFile == "" || config.TLS.KeyFile == "" {
		return nil, errors.New("server mode TLS requires tls.certFile and tls.keyFile")
	}
	certificate, err := tls.LoadX509KeyPair(config.TLS.CertFile, config.TLS.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load mqtt server certificate: %w", err)
	}
	result := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	if config.TLS.CAFile != "" {
		content, readErr := os.ReadFile(config.TLS.CAFile)
		if readErr != nil {
			return nil, fmt.Errorf("read tls.caFile: %w", readErr)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(content) {
			return nil, errors.New("tls.caFile does not contain a valid PEM certificate")
		}
		result.ClientCAs, result.ClientAuth = pool, tls.RequireAndVerifyClientCert
	}
	return result, nil
}

func (c Config) connectTimeout() time.Duration {
	return time.Duration(c.ConnectTimeoutSeconds) * time.Second
}

func (c Config) retainedStateMaxAge() time.Duration {
	return time.Duration(c.RetainedStateMaxAgeSeconds) * time.Second
}
