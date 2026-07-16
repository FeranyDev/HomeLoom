package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Storage StorageConfig `yaml:"storage"`
}

type ServerConfig struct {
	Address        string   `yaml:"address"`
	TrustedProxies []string `yaml:"trusted_proxies,omitempty"`
}

type StorageConfig struct {
	DatabaseURL string `yaml:"database_url"`
	MasterKey   string `yaml:"master_key"`
}

func Default() Config {
	return Config{
		Server: ServerConfig{Address: "127.0.0.1:8090"},
		Storage: StorageConfig{
			DatabaseURL: "postgres://homeloom:homeloom-dev@127.0.0.1:54329/homeloom?sslmode=disable",
			MasterKey:   "./data/homeloom.key",
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
	if strings.TrimSpace(c.Server.Address) == "" {
		return errors.New("server.address is required")
	}
	if strings.TrimSpace(c.Storage.DatabaseURL) == "" {
		return errors.New("storage.database_url is required")
	}
	databaseURL, err := url.Parse(c.Storage.DatabaseURL)
	if err != nil || (databaseURL.Scheme != "postgres" && databaseURL.Scheme != "postgresql") || databaseURL.Host == "" || strings.Trim(databaseURL.Path, "/") == "" {
		return errors.New("storage.database_url must be a PostgreSQL URL with host and database name")
	}
	if strings.TrimSpace(c.Storage.MasterKey) == "" {
		return errors.New("storage.master_key is required")
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
	return nil
}
