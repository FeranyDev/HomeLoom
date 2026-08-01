package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/feranydev/homeloom/backend/internal/platform/safelog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New builds the canonical HomeLoom logger. It emits structured JSON and
// redacts secrets before encoding them.
func New(level zapcore.Level, output io.Writer) *zap.Logger {
	if output == nil {
		output = os.Stderr
	}
	encoder := zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		TimeKey: "time", LevelKey: "level", NameKey: "logger", CallerKey: "caller",
		MessageKey: "msg", StacktraceKey: "stacktrace",
		EncodeLevel: zapcore.LowercaseLevelEncoder, EncodeTime: zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder, EncodeCaller: zapcore.ShortCallerEncoder,
	})
	core := zapcore.NewCore(encoder, zapcore.AddSync(output), level)
	return zap.New(safelog.NewCore(core), zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)).With(zap.String("component", "backend"))
}

func NewStderr(level zapcore.Level) *zap.Logger { return New(level, os.Stderr) }

func ParseLevel(value string) (zapcore.Level, error) {
	var level zapcore.Level
	err := level.UnmarshalText([]byte(strings.ToLower(strings.TrimSpace(value))))
	return level, err
}

// SlogAdapter bridges dependencies whose public API still requires log/slog.
// Application code should use Zap directly.
func SlogAdapter(logger *zap.Logger) *slog.Logger {
	if logger == nil {
		logger = zap.NewNop()
	}
	return slog.New(&slogHandler{logger: logger})
}

type slogHandler struct {
	logger *zap.Logger
	attrs  []slog.Attr
	groups []string
}

func (h *slogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return h.logger.Core().Enabled(slogLevel(level))
}

func (h *slogHandler) Handle(_ context.Context, record slog.Record) error {
	fields := make([]zap.Field, 0, len(h.attrs)+record.NumAttrs())
	for _, attr := range h.attrs {
		fields = appendSlogAttr(fields, h.groups, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		fields = appendSlogAttr(fields, h.groups, attr)
		return true
	})
	if checked := h.logger.Check(slogLevel(record.Level), record.Message); checked != nil {
		checked.Write(fields...)
	}
	return nil
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &clone
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.groups = append(append([]string(nil), h.groups...), name)
	return &clone
}

func slogLevel(level slog.Level) zapcore.Level {
	switch {
	case level <= slog.LevelDebug:
		return zapcore.DebugLevel
	case level < slog.LevelWarn:
		return zapcore.InfoLevel
	case level < slog.LevelError:
		return zapcore.WarnLevel
	default:
		return zapcore.ErrorLevel
	}
}

func appendSlogAttr(fields []zap.Field, groups []string, attr slog.Attr) []zap.Field {
	attr.Value = attr.Value.Resolve()
	if attr.Value.Kind() == slog.KindGroup {
		nestedGroups := groups
		if attr.Key != "" {
			nestedGroups = append(append([]string(nil), groups...), attr.Key)
		}
		for _, child := range attr.Value.Group() {
			fields = appendSlogAttr(fields, nestedGroups, child)
		}
		return fields
	}
	key := strings.Join(append(append([]string(nil), groups...), attr.Key), ".")
	if err, ok := attr.Value.Any().(error); ok {
		return append(fields, zap.NamedError(key, err))
	}
	return append(fields, zap.Any(key, attr.Value.Any()))
}
