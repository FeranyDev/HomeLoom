package safelog

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

const replacement = "********"

var assignedSecret = regexp.MustCompile(`(?i)(password|passphrase|passwd|pwd|secret|token|api[_-]?key|private[_-]?key|credential|authorization|pairing[_-]?code|setup[_-]?uri|pin)(["']?\s*[:=]\s*["']?)([^"',\s&}]+)`)

// ReplaceAttr is suitable for slog.HandlerOptions.ReplaceAttr. It redacts
// sensitive keys and best-effort secret assignments embedded in errors/URLs.
func ReplaceAttr(_ []string, attr slog.Attr) slog.Attr {
	if sensitiveKey(attr.Key) {
		return slog.String(attr.Key, replacement)
	}
	value := attr.Value.Resolve()
	switch value.Kind() {
	case slog.KindString:
		attr.Value = slog.StringValue(RedactText(value.String()))
	case slog.KindAny:
		if err, ok := value.Any().(error); ok {
			attr.Value = slog.StringValue(RedactText(err.Error()))
		}
	}
	return attr
}

func RedactText(value string) string {
	return assignedSecret.ReplaceAllString(value, fmt.Sprintf("$1$2%s", replacement))
}

func sensitiveKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(key))
	for _, part := range []string{"password", "passphrase", "passwd", "secret", "token", "apikey", "privatekey", "credential", "authorization", "pairingcode", "setupuri", "pin"} {
		if strings.Contains(normalized, part) {
			return true
		}
	}
	return false
}
