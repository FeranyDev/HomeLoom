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
	t.Setenv("HOMELOOM_MEDIA_ENABLED", "true")
	t.Setenv("HOMELOOM_CAMERA_KERNEL_BIN", "/opt/homeloom/homeloom-camera-kernel")
	t.Setenv("HOMELOOM_MEDIA_RUNTIME_DIR", "/var/lib/homeloom/media")
	t.Setenv("HOMELOOM_HAP_HOST", "192.0.2.10")
	t.Setenv("HOMELOOM_HAP_PORT_BASE", "52000")
	t.Setenv("HOMELOOM_RTSP_PORT_BASE", "20000")
	t.Setenv("HOMELOOM_SRTP_PORT_BASE", "21000")

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
	if !loaded.Media.Enabled {
		t.Fatalf("media = %#v", loaded.Media)
	}
	if loaded.Media.CameraKernelBinary != "/opt/homeloom/homeloom-camera-kernel" ||
		loaded.Media.RuntimeDir != "/var/lib/homeloom/media" ||
		loaded.Media.HAPHost != "192.0.2.10" ||
		loaded.Media.HAPPortBase != 52000 ||
		loaded.Media.RTSPPortBase != 20000 ||
		loaded.Media.SRTPPortBase != 21000 {
		t.Fatalf("media process settings = %#v", loaded.Media)
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
	if defaults.Logging.ChildLevel != "info" {
		t.Fatalf("default logging = %#v", defaults.Logging)
	}
	if defaults.Media.CameraKernelBinary != "homeloom-camera-kernel" ||
		defaults.Media.RuntimeDir != "./data/media/publishers" ||
		defaults.Media.HAPHost != "0.0.0.0" {
		t.Fatalf("default media = %#v", defaults.Media)
	}
}

func TestValidateRejectsInvalidChildLogLevel(t *testing.T) {
	config := Default()
	config.Logging.ChildLevel = "trace"
	if err := config.Validate(); err == nil {
		t.Fatal("Validate() accepted an invalid child log level")
	}
}

func TestExampleConfigurationLoads(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "config.example.yaml")
	if _, err := Load(path); err != nil {
		t.Fatalf("Load(%q) = %v", path, err)
	}
}

func TestValidateRequiresSupportedDatabaseURLAndMasterKey(t *testing.T) {
	for _, test := range []struct {
		name        string
		databaseURL string
		masterKey   string
	}{
		{name: "filesystem database path", databaseURL: "./data/homeloom.db", masterKey: "./data/homeloom.key"},
		{name: "missing host", databaseURL: "postgres:///homeloom", masterKey: "./data/homeloom.key"},
		{name: "missing database", databaseURL: "postgres://localhost", masterKey: "./data/homeloom.key"},
		{name: "missing master key", databaseURL: "postgres://localhost/homeloom"},
		{name: "missing sqlite path", databaseURL: "sqlite:", masterKey: "./data/homeloom.key"},
		{name: "unsupported database", databaseURL: "mysql://localhost/homeloom", masterKey: "./data/homeloom.key"},
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

func TestValidateAcceptsSQLiteURL(t *testing.T) {
	config := Default()
	config.Storage.DatabaseURL = "sqlite://./data/homeloom.db"
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestValidateMedia(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*MediaConfig)
	}{
		{name: "missing camera kernel binary", mutate: func(c *MediaConfig) { c.CameraKernelBinary = "" }},
		{name: "missing runtime directory", mutate: func(c *MediaConfig) { c.RuntimeDir = "" }},
		{name: "loopback HAP host", mutate: func(c *MediaConfig) { c.HAPHost = "127.0.0.1" }},
		{name: "invalid HAP port band", mutate: func(c *MediaConfig) { c.HAPPortBase = 65000 }},
		{name: "overlapping RTSP and SRTP port bands", mutate: func(c *MediaConfig) { c.SRTPPortBase = c.RTSPPortBase + 500 }},
		{name: "touching HAP and SRTP port bands", mutate: func(c *MediaConfig) { c.SRTPPortBase = c.HAPPortBase + 999 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := Default()
			config.Media.Enabled = true
			test.mutate(&config.Media)
			if err := config.Validate(); err == nil {
				t.Fatalf("Validate() accepted %#v", config.Media)
			}
		})
	}
	config := Default()
	config.Media.Enabled = true
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestLoadRejectsInvalidMediaPortEnvironment(t *testing.T) {
	t.Setenv("HOMELOOM_HAP_PORT_BASE", "not-a-port")
	if _, err := Load(""); err == nil {
		t.Fatal("Load() accepted an invalid media port")
	}
}

func TestLoadRejectsInvalidMediaEnabledEnvironment(t *testing.T) {
	t.Setenv("HOMELOOM_MEDIA_ENABLED", "sometimes")
	if _, err := Load(""); err == nil {
		t.Fatal("Load() accepted an invalid media enabled value")
	}
}

func TestEnvironmentCanSelectSQLite(t *testing.T) {
	t.Setenv("HOMELOOM_DATABASE_URL", "sqlite://./data/alternative.db")
	loaded, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Storage.DatabaseURL != "sqlite://./data/alternative.db" {
		t.Fatalf("database URL = %q", loaded.Storage.DatabaseURL)
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
