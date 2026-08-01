package app

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/creds"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestHomeKitModuleDefaultsToWarn(t *testing.T) {
	if got := modules["homekit"]; got != "warn" {
		t.Fatalf("homekit module log level = %q, want warn", got)
	}
}

func TestLoggerWritesNormalizedJSON(t *testing.T) {
	oldModules := modules
	oldConfigs := configs
	oldMemory := MemoryLog
	t.Cleanup(func() {
		modules = oldModules
		configs = oldConfigs
		MemoryLog = oldMemory
		initLogger()
	})

	modules = map[string]string{
		"format": "json",
		"level":  "info",
		"output": "",
		"time":   "ISO8601",
	}
	configs = nil
	MemoryLog = newBuffer()
	initLogger()

	GetLogger("ffmpeg").Info("process output", zap.String("source", "stderr"))

	var output bytes.Buffer
	_, _ = MemoryLog.WriteTo(&output)
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
		t.Fatalf("decode log entry: %v\n%s", err, output.String())
	}
	for key, want := range map[string]any{
		"level":     "info",
		"msg":       "process output",
		"component": "camera-kernel",
		"module":    "ffmpeg",
		"source":    "stderr",
	} {
		if got := entry[key]; got != want {
			t.Errorf("%s = %#v, want %#v", key, got, want)
		}
	}
	if _, ok := entry["time"].(string); !ok {
		t.Errorf("time = %#v, want ISO 8601 string", entry["time"])
	}
}

func TestLoggerHonorsModuleLevelAndRedactsSecrets(t *testing.T) {
	oldModules := modules
	oldConfigs := configs
	oldMemory := MemoryLog
	t.Cleanup(func() {
		modules = oldModules
		configs = oldConfigs
		MemoryLog = oldMemory
		initLogger()
	})

	modules = map[string]string{
		"format": "json",
		"level":  "error",
		"ffmpeg": "debug",
		"output": "",
		"time":   "",
	}
	configs = nil
	MemoryLog = newBuffer()
	initLogger()

	const secret = "zap-test-camera-token"
	creds.AddSecret(secret)
	GetLogger("api").Info("filtered")
	GetLogger("ffmpeg").Debug("process command", zap.String("token", secret))

	if !LogEnabled("ffmpeg", zapcore.DebugLevel) {
		t.Fatal("ffmpeg debug logging should be enabled")
	}
	if LogEnabled("api", zapcore.InfoLevel) {
		t.Fatal("api info logging should be disabled")
	}

	var output bytes.Buffer
	_, _ = MemoryLog.WriteTo(&output)
	if bytes.Contains(output.Bytes(), []byte(secret)) {
		t.Fatalf("secret leaked in log: %s", output.String())
	}
	if !bytes.Contains(output.Bytes(), []byte(`"token":"***"`)) {
		t.Fatalf("redacted token missing: %s", output.String())
	}
}
