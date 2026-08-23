package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Storage StorageConfig `yaml:"storage"`
	Logging LoggingConfig `yaml:"logging"`
	Media   MediaConfig   `yaml:"media"`
	Runtime RuntimeConfig `yaml:"runtime"`
	MCP     MCPConfig     `yaml:"mcp"`
}

type LoggingConfig struct {
	ChildLevel string `yaml:"child_level"`
}

type ServerConfig struct {
	Address        string   `yaml:"address"`
	TrustedProxies []string `yaml:"trusted_proxies,omitempty"`
}

type StorageConfig struct {
	DatabaseURL string `yaml:"database_url"`
	MasterKey   string `yaml:"master_key"`
}

type MediaConfig struct {
	Enabled            bool   `yaml:"enabled"`
	CameraKernelBinary string `yaml:"camera_kernel_binary"`
	RuntimeDir         string `yaml:"runtime_dir"`
	HAPHost            string `yaml:"hap_host"`
	HAPPortBase        int    `yaml:"hap_port_base"`
	RTSPPortBase       int    `yaml:"rtsp_port_base"`
	SRTPPortBase       int    `yaml:"srtp_port_base"`
}

// MCPConfig controls Core's private Unix-domain-socket gateway and its
// locally managed MCP/AI child process. The child owns the AI-facing HTTP
// surface and credentials, while Core remains the device-policy authority.
type MCPConfig struct {
	Enabled         bool   `yaml:"enabled"`
	SocketPath      string `yaml:"socket_path"`
	AgentBinary     string `yaml:"agent_binary"`
	RuntimeDir      string `yaml:"runtime_dir"`
	AgentListenAddr string `yaml:"agent_listen_address"`
}

// RuntimeConfig covers bounded in-memory pipelines. EventQueueCapacity is per
// shard; the aggregate capacity is EventQueueShards multiplied by it. State
// checkpoints are deliberately disabled by default and, when enabled, are a
// low-frequency restart cache rather than a durable event history.
type RuntimeConfig struct {
	EventQueueShards              int `yaml:"event_queue_shards"`
	EventQueueCapacity            int `yaml:"event_queue_capacity"`
	StateCheckpointIntervalSecond int `yaml:"state_checkpoint_interval_seconds"`
}

func Default() Config {
	return Config{
		Server: ServerConfig{Address: "127.0.0.1:8090"},
		Storage: StorageConfig{
			DatabaseURL: "postgres://homeloom:homeloom-dev@127.0.0.1:54329/homeloom?sslmode=disable",
			MasterKey:   "./data/homeloom.key",
		},
		Logging: LoggingConfig{ChildLevel: "info"},
		Media: MediaConfig{
			Enabled:            true,
			CameraKernelBinary: "homeloom-camera-kernel",
			RuntimeDir:         "./data/media/publishers",
			HAPHost:            "0.0.0.0",
			HAPPortBase:        51826,
			RTSPPortBase:       18554,
			SRTPPortBase:       20000,
		},
		Runtime: RuntimeConfig{
			EventQueueShards:   8,
			EventQueueCapacity: 128,
		},
		MCP: MCPConfig{
			SocketPath: "./data/mcp/core.sock", AgentBinary: "homeloom-mcp-agent",
			RuntimeDir: "./data/mcp", AgentListenAddr: "127.0.0.1:8091",
		},
	}
}

func Load(path string) (Config, error) {
	config := Default()
	if path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		decoder := yaml.NewDecoder(strings.NewReader(string(content)))
		decoder.KnownFields(true)
		if err := decoder.Decode(&config); err != nil {
			return Config{}, fmt.Errorf("decode config: %w", err)
		}
	}

	if err := applyEnvironment(&config); err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c Config) Validate() error {
	switch strings.ToLower(strings.TrimSpace(c.Logging.ChildLevel)) {
	case "debug", "info", "warn", "error":
	default:
		return errors.New("logging.child_level must be debug, info, warn, or error")
	}
	if strings.TrimSpace(c.Server.Address) == "" {
		return errors.New("server.address is required")
	}
	if strings.TrimSpace(c.Storage.DatabaseURL) == "" {
		return errors.New("storage.database_url is required")
	}
	databaseURL, err := url.Parse(c.Storage.DatabaseURL)
	if err != nil {
		return errors.New("storage.database_url must be a PostgreSQL or SQLite URL")
	}
	switch databaseURL.Scheme {
	case "postgres", "postgresql":
		if databaseURL.Host == "" || strings.Trim(databaseURL.Path, "/") == "" {
			return errors.New("storage.database_url must be a PostgreSQL URL with host and database name")
		}
	case "sqlite":
		if strings.TrimSpace(strings.TrimPrefix(c.Storage.DatabaseURL, "sqlite:")) == "" {
			return errors.New("storage.database_url must include a SQLite database path")
		}
	default:
		return errors.New("storage.database_url must use the postgres, postgresql, or sqlite scheme")
	}
	if strings.TrimSpace(c.Storage.MasterKey) == "" {
		return errors.New("storage.master_key is required")
	}
	if c.Media.Enabled {
		if strings.TrimSpace(c.Media.CameraKernelBinary) == "" {
			return errors.New("media.camera_kernel_binary is required when media is enabled")
		}
		if strings.TrimSpace(c.Media.RuntimeDir) == "" {
			return errors.New("media.runtime_dir is required when media is enabled")
		}
		if err := validateReachableHAPHost(c.Media.HAPHost); err != nil {
			return err
		}
		if err := validateMediaPortBands(c.Media); err != nil {
			return err
		}
	}
	if c.Runtime.EventQueueShards < 1 || c.Runtime.EventQueueShards > 128 {
		return errors.New("runtime.event_queue_shards must be between 1 and 128")
	}
	if c.Runtime.EventQueueCapacity < 1 || c.Runtime.EventQueueCapacity > 65_536 {
		return errors.New("runtime.event_queue_capacity must be between 1 and 65536")
	}
	if interval := c.Runtime.StateCheckpointIntervalSecond; interval != 0 && (interval < 60 || interval > 86_400) {
		return errors.New("runtime.state_checkpoint_interval_seconds must be 0 or between 60 and 86400")
	}
	if c.MCP.Enabled && strings.TrimSpace(c.MCP.SocketPath) == "" {
		return errors.New("mcp.socket_path is required when mcp.enabled is true")
	}
	if c.MCP.Enabled {
		if strings.TrimSpace(c.MCP.AgentBinary) == "" {
			return errors.New("mcp.agent_binary is required when mcp.enabled is true")
		}
		if strings.TrimSpace(c.MCP.RuntimeDir) == "" {
			return errors.New("mcp.runtime_dir is required when mcp.enabled is true")
		}
		host, port, err := net.SplitHostPort(strings.TrimSpace(c.MCP.AgentListenAddr))
		if err != nil || host == "" || port == "" {
			return errors.New("mcp.agent_listen_address must be a loopback host and port")
		}
		ip := net.ParseIP(strings.Trim(host, "[]"))
		if ip == nil || !ip.IsLoopback() {
			return errors.New("mcp.agent_listen_address must use a loopback IP address")
		}
		parsedPort, err := strconv.Atoi(port)
		if err != nil || parsedPort < 1 || parsedPort > 65_535 {
			return errors.New("mcp.agent_listen_address must include a valid TCP port")
		}
	}
	for _, value := range c.Server.TrustedProxies {
		value = strings.TrimSpace(value)
		if value == "" {
			return errors.New("server.trusted_proxies cannot contain an empty value")
		}
		if net.ParseIP(value) == nil {
			if _, _, err := net.ParseCIDR(value); err != nil {
				return fmt.Errorf("server.trusted_proxies contains invalid IP or CIDR %q", value)
			}
		}
	}
	return nil
}

func applyEnvironment(config *Config) error {
	if value := os.Getenv("HOMELOOM_HTTP_ADDRESS"); value != "" {
		config.Server.Address = value
	}
	if value := os.Getenv("HOMELOOM_DATABASE_URL"); value != "" {
		config.Storage.DatabaseURL = value
	}
	if value := os.Getenv("HOMELOOM_MASTER_KEY"); value != "" {
		config.Storage.MasterKey = value
	}
	if value, present := os.LookupEnv("HOMELOOM_TRUSTED_PROXIES"); present {
		config.Server.TrustedProxies = nil
		for _, item := range strings.Split(value, ",") {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				config.Server.TrustedProxies = append(config.Server.TrustedProxies, trimmed)
			}
		}
	}
	if value, present := os.LookupEnv("HOMELOOM_MEDIA_ENABLED"); present {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return errors.New("HOMELOOM_MEDIA_ENABLED must be a boolean")
		}
		config.Media.Enabled = enabled
	}
	if value := os.Getenv("HOMELOOM_CAMERA_KERNEL_BIN"); value != "" {
		config.Media.CameraKernelBinary = strings.TrimSpace(value)
	}
	if value := os.Getenv("HOMELOOM_MEDIA_RUNTIME_DIR"); value != "" {
		config.Media.RuntimeDir = strings.TrimSpace(value)
	}
	if value, present := os.LookupEnv("HOMELOOM_MCP_ENABLED"); present {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return errors.New("HOMELOOM_MCP_ENABLED must be a boolean")
		}
		config.MCP.Enabled = enabled
	}
	if value := os.Getenv("HOMELOOM_MCP_SOCKET"); value != "" {
		config.MCP.SocketPath = strings.TrimSpace(value)
	}
	if value := os.Getenv("HOMELOOM_MCP_AGENT_BIN"); value != "" {
		config.MCP.AgentBinary = strings.TrimSpace(value)
	}
	if value := os.Getenv("HOMELOOM_MCP_RUNTIME_DIR"); value != "" {
		config.MCP.RuntimeDir = strings.TrimSpace(value)
	}
	if value := os.Getenv("HOMELOOM_MCP_AGENT_LISTEN"); value != "" {
		config.MCP.AgentListenAddr = strings.TrimSpace(value)
	}
	if value := os.Getenv("HOMELOOM_HAP_HOST"); value != "" {
		config.Media.HAPHost = strings.TrimSpace(value)
	}
	for name, destination := range map[string]*int{
		"HOMELOOM_HAP_PORT_BASE":  &config.Media.HAPPortBase,
		"HOMELOOM_RTSP_PORT_BASE": &config.Media.RTSPPortBase,
		"HOMELOOM_SRTP_PORT_BASE": &config.Media.SRTPPortBase,
	} {
		if value := os.Getenv(name); value != "" {
			port, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("%s must be an integer", name)
			}
			*destination = port
		}
	}
	for name, destination := range map[string]*int{
		"HOMELOOM_EVENT_QUEUE_SHARDS":                &config.Runtime.EventQueueShards,
		"HOMELOOM_EVENT_QUEUE_CAPACITY":              &config.Runtime.EventQueueCapacity,
		"HOMELOOM_STATE_CHECKPOINT_INTERVAL_SECONDS": &config.Runtime.StateCheckpointIntervalSecond,
	} {
		if value := os.Getenv(name); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("%s must be an integer", name)
			}
			*destination = parsed
		}
	}
	return nil
}

func validateReachableHAPHost(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return errors.New("media.hap_host is required when media is enabled")
	}
	if strings.EqualFold(host, "localhost") {
		return errors.New("media.hap_host must be reachable by Apple Home controllers")
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil && ip.IsLoopback() {
		return errors.New("media.hap_host must be reachable by Apple Home controllers")
	}
	return nil
}

func validateMediaPortBands(config MediaConfig) error {
	const bandWidth = 1000
	type band struct {
		name string
		base int
	}
	bands := []band{
		{name: "hap_port_base", base: config.HAPPortBase},
		{name: "rtsp_port_base", base: config.RTSPPortBase},
		{name: "srtp_port_base", base: config.SRTPPortBase},
	}
	for _, current := range bands {
		if current.base < 1024 || current.base+bandWidth-1 > 65535 {
			return fmt.Errorf("media.%s must reserve 1000 ports within 1024..65535", current.name)
		}
	}
	for index, current := range bands {
		currentEnd := current.base + bandWidth - 1
		for _, other := range bands[index+1:] {
			otherEnd := other.base + bandWidth - 1
			if current.base <= otherEnd && other.base <= currentEnd {
				return fmt.Errorf(
					"media.%s (%d..%d) must not overlap media.%s (%d..%d)",
					current.name, current.base, currentEnd, other.name, other.base, otherEnd,
				)
			}
		}
	}
	return nil
}
