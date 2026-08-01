package safelog

import (
	"bytes"
	"errors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"strings"
	"testing"
)

func TestCoreRedactsKeysAndEmbeddedAssignments(t *testing.T) {
	var output bytes.Buffer
	core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&output), zapcore.DebugLevel)
	logger := zap.New(NewCore(core))
	logger.Error("provider failed", zap.String("password", "plain-secret"), zap.String("endpoint", "mqtt://host?token=url-secret&client=x"), zap.Error(errors.New(`invalid config: api_key="error-secret"`)))
	logger.Error("cloud failed", zap.String("ssecurity", "miot-security-value"), zap.String("passToken", "camera-pass-token-value"))

	text := output.String()
	for _, secret := range []string{"plain-secret", "url-secret", "error-secret", "miot-security-value", "camera-pass-token-value"} {
		if strings.Contains(text, secret) {
			t.Fatalf("log contains %q: %s", secret, text)
		}
	}
	if strings.Count(text, replacement) < 3 {
		t.Fatalf("expected redacted values: %s", text)
	}
}

func TestCoreDoesNotAlterOrdinaryMetadata(t *testing.T) {
	var output bytes.Buffer
	core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&output), zapcore.DebugLevel)
	zap.New(NewCore(core)).Info("provider ready", zap.String("provider_id", "virtual-main"))
	if !strings.Contains(output.String(), `"provider_id":"virtual-main"`) {
		t.Fatalf("ordinary metadata changed: %s", output.String())
	}
}
