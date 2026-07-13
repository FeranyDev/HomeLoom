package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadYAMLAndEnvironmentOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte("server:\n  address: ':9000'\nstorage:\n  database: './test.db'\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("HOMELOOM_HTTP_ADDRESS", ":9100")
	t.Setenv("HOMELOOM_TRUSTED_PROXIES", "127.0.0.1/32, 10.0.0.8")

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Server.Address != ":9100" {
		t.Fatalf("server address = %q, want %q", loaded.Server.Address, ":9100")
	}
	if loaded.Storage.Database != "./test.db" {
		t.Fatalf("database = %q, want %q", loaded.Storage.Database, "./test.db")
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
	if address := Default().Server.Address; address != "127.0.0.1:8090" {
		t.Fatalf("default address = %q", address)
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
