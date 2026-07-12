package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Storage StorageConfig `yaml:"storage"`
}

type ServerConfig struct {
	Address string `yaml:"address"`
}

type StorageConfig struct {
	Database string `yaml:"database"`
}

func Default() Config {
	return Config{
		Server:  ServerConfig{Address: ":8090"},
		Storage: StorageConfig{Database: "./data/homeloom.db"},
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
	if strings.TrimSpace(c.Storage.Database) == "" {
		return errors.New("storage.database is required")
	}
	return nil
}

func applyEnvironment(config *Config) error {
	if value := os.Getenv("HOMELOOM_HTTP_ADDRESS"); value != "" {
		config.Server.Address = value
	}
	if value := os.Getenv("HOMELOOM_DATABASE"); value != "" {
		config.Storage.Database = value
	}
	return nil
}
