package safelog

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestReplaceAttrRedactsKeysAndEmbeddedAssignments(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{ReplaceAttr: ReplaceAttr}))
	logger.Error("provider failed", "password", "plain-secret", "endpoint", "mqtt://host?token=url-secret&client=x", "error", errors.New(`invalid config: api_key="error-secret"`))
	logger.Error("cloud failed", "ssecurity", "miot-security-value", "passToken", "camera-pass-token-value")

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

func TestReplaceAttrDoesNotAlterOrdinaryMetadata(t *testing.T) {
	attribute := ReplaceAttr(nil, slog.String("provider_id", "virtual-main"))
	if attribute.Value.String() != "virtual-main" {
		t.Fatalf("attribute = %#v", attribute)
	}
}
