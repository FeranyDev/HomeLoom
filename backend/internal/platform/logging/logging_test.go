package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestNewUsesCanonicalFieldsAndRedactsSecrets(t *testing.T) {
	var output bytes.Buffer
	logger := New(zapcore.DebugLevel, &output)
	logger.Info("provider token=plain", zap.String("module", "test"), zap.String("password", "secret"))
	text := output.String()
	for _, required := range []string{`"time":`, `"level":"info"`, `"msg":"provider token=********"`, `"component":"backend"`, `"module":"test"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing %s in %s", required, text)
		}
	}
	if strings.Contains(text, "plain") || strings.Contains(text, "secret") {
		t.Fatalf("secret leaked: %s", text)
	}
}

func TestSlogAdapterForwardsLevelsFieldsAndRedaction(t *testing.T) {
	var output bytes.Buffer
	logger := New(zapcore.DebugLevel, &output).With(zap.String("module", "dependency"))
	adapter := SlogAdapter(logger)
	adapter.Warn("dependency failed", "request_id", "request-1", "error", errors.New("token=secret"))
	text := output.String()
	for _, required := range []string{`"level":"warn"`, `"msg":"dependency failed"`, `"module":"dependency"`, `"request_id":"request-1"`, `token=********`} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing %s in %s", required, text)
		}
	}
}

func TestSlogAdapterHonorsZapLevel(t *testing.T) {
	var output bytes.Buffer
	adapter := SlogAdapter(New(zapcore.WarnLevel, &output))
	adapter.LogAttrs(nil, slog.LevelInfo, "hidden", slog.String("key", "value"))
	adapter.Error("shown")
	if strings.Contains(output.String(), "hidden") || !strings.Contains(output.String(), "shown") {
		t.Fatalf("unexpected level output: %s", output.String())
	}
}
