package safelog

import (
	"fmt"
	"regexp"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const replacement = "********"

var assignedSecret = regexp.MustCompile(`(?i)(password|passphrase|passwd|pwd|secret|token|api[_-]?key|private[_-]?key|credential|authorization|pairing[_-]?code|setup[_-]?uri|pin)(["']?\s*[:=]\s*["']?)([^"',\s&}]+)`)

func RedactText(value string) string {
	return assignedSecret.ReplaceAllString(value, fmt.Sprintf("$1$2%s", replacement))
}

// NewCore wraps a Zap core so messages and structured fields are redacted
// before they reach any encoder or sink.
func NewCore(core zapcore.Core) zapcore.Core { return &redactingCore{Core: core} }

type redactingCore struct{ zapcore.Core }

func (c *redactingCore) With(fields []zapcore.Field) zapcore.Core {
	return &redactingCore{Core: c.Core.With(redactFields(fields))}
}

func (c *redactingCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return checked.AddCore(entry, c)
	}
	return checked
}

func (c *redactingCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	entry.Message = RedactText(entry.Message)
	return c.Core.Write(entry, redactFields(fields))
}

func redactFields(fields []zapcore.Field) []zapcore.Field {
	redacted := make([]zapcore.Field, len(fields))
	for index, field := range fields {
		redacted[index] = redactField(field)
	}
	return redacted
}

func redactField(field zapcore.Field) zapcore.Field {
	if sensitiveKey(field.Key) {
		return zap.String(field.Key, replacement)
	}
	switch field.Type {
	case zapcore.StringType:
		field.String = RedactText(field.String)
	case zapcore.ErrorType:
		if err, ok := field.Interface.(error); ok {
			return zap.String(field.Key, RedactText(err.Error()))
		}
	}
	return field
}

func sensitiveKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(key))
	if normalized == "ssecurity" {
		return true
	}
	for _, part := range []string{"password", "passphrase", "passwd", "secret", "token", "apikey", "privatekey", "devicekey", "credential", "authorization", "pairingcode", "setupuri", "pin"} {
		if strings.Contains(normalized, part) {
			return true
		}
	}
	return false
}
