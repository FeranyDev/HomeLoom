package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadYAMLAndEnvironmentOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte("server:\n  address: ':9000'\nstorage:\n  database_url: 'postgres://test:secret@localhost:5432/homeloom_test?sslmode=disable'\n  master_key: './test.key'\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("HOMELOOM_HTTP_ADDRESS", ":9100")
	t.Setenv("HOMELOOM_DATABASE_URL", "postgres://override:secret@127.0.0.1:54329/override?sslmode=disable")
	t.Setenv("HOMELOOM_MASTER_KEY", "./override.key")
	t.Setenv("HOMELOOM_TRUSTED_PROXIES", "127.0.0.1/32, 10.0.0.8")

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Server.Address != ":9100" {
		t.Fatalf("server address = %q, want %q", loaded.Server.Address, ":9100")
	}
	if loaded.Storage.DatabaseURL != "postgres://override:secret@127.0.0.1:54329/override?sslmode=disable" {
		t.Fatalf("database URL = %q", loaded.Storage.DatabaseURL)
	}
	if loaded.Storage.MasterKey != "./override.key" {
		t.Fatalf("master key = %q", loaded.Storage.MasterKey)
	}
	if len(loaded.Server.TrustedProxies) != 2 || loaded.Server.TrustedProxies[1] != "10.0.0.8" {
		t.Fatalf("trusted proxies = %#v", loaded.Server.TrustedProxies)
	}
}

func TestLoadRejectsInvalidTrustedProxy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  trusted_proxies: [not-a-network]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted an invalid trusted proxy")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("unknown: true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted an unknown field")
	}
}

func TestDefaultOnlyListensOnLoopback(t *testing.T) {
	defaults := Default()
	if address := defaults.Server.Address; address != "127.0.0.1:8090" {
		t.Fatalf("default address = %q", address)
	}
	if defaults.Storage.DatabaseURL != "postgres://homeloom:homeloom-dev@127.0.0.1:54329/homeloom?sslmode=disable" || defaults.Storage.MasterKey != "./data/homeloom.key" {
		t.Fatalf("default storage = %#v", defaults.Storage)
	}
}

func TestValidateRequiresPostgreSQLURLAndMasterKey(t *testing.T) {
	for _, test := range []struct {
		name        string
		databaseURL string
		masterKey   string
	}{
		{name: "filesystem database path", databaseURL: "./data/homeloom.db", masterKey: "./data/homeloom.key"},
		{name: "missing host", databaseURL: "postgres:///homeloom", masterKey: "./data/homeloom.key"},
		{name: "missing database", databaseURL: "postgres://localhost", masterKey: "./data/homeloom.key"},
		{name: "missing master key", databaseURL: "postgres://localhost/homeloom"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := Default()
			config.Storage = StorageConfig{DatabaseURL: test.databaseURL, MasterKey: test.masterKey}
			if err := config.Validate(); err == nil {
				t.Fatalf("Validate() accepted %#v", config.Storage)
			}
		})
	}
}

func TestLoadRejectsDatabaseBackedTargets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("targets: []\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted database-backed target configuration")
	}
}
