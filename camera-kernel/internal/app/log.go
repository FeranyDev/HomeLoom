package app

import (
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/creds"
	"github.com/mattn/go-isatty"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const logComponent = "camera-kernel"

var MemoryLog = newBuffer()

var (
	Logger = zap.NewNop()

	logMu      sync.RWMutex
	logEncoder zapcore.Encoder
	logOutput  zapcore.WriteSyncer
	logLevels  = make(map[string]zap.AtomicLevel)
)

// GetLogger returns a structured logger for module. Every entry contains the
// stable component and module fields used by the parent process log collector.
func GetLogger(module string) *zap.Logger {
	level := GetLogLevel(module)

	logMu.RLock()
	encoder, output := logEncoder, logOutput
	logMu.RUnlock()
	if encoder == nil || output == nil {
		return zap.NewNop()
	}

	return zap.New(zapcore.NewCore(encoder.Clone(), output, level)).With(
		zap.String("component", logComponent),
		zap.String("module", module),
	)
}

// GetLogLevel returns the atomic level controlling a module. A module without
// an explicit setting inherits log.level at the time its logger is created.
func GetLogLevel(module string) zap.AtomicLevel {
	logMu.Lock()
	defer logMu.Unlock()
	if level, ok := logLevels[module]; ok {
		return level
	}

	value := modules[module]
	if value == "" {
		value = modules["level"]
	}
	level := zap.NewAtomicLevelAt(parseLogLevel(value))
	logLevels[module] = level
	return level
}

// LogEnabled reports whether module emits entries at level.
func LogEnabled(module string, level zapcore.Level) bool {
	return GetLogLevel(module).Enabled(level)
}

func parseLogLevel(value string) zapcore.Level {
	switch strings.ToLower(value) {
	case "trace", "debug":
		return zap.DebugLevel
	case "", "info":
		return zap.InfoLevel
	case "warn", "warning":
		return zap.WarnLevel
	case "error":
		return zap.ErrorLevel
	case "dpanic":
		return zap.DPanicLevel
	case "panic":
		return zap.PanicLevel
	case "fatal":
		return zap.FatalLevel
	case "disabled", "quiet":
		return zapcore.Level(127)
	default:
		return zap.InfoLevel
	}
}

// initLogger supports:
// - output: empty (only to memory), stderr, stdout, file[:path]
// - format: empty (autodetect color support), color, json, text
// - time:   empty (disable timestamp), ISO8601, UNIXMS, UNIXMICRO, UNIXNANO
// - level:  disabled, trace, debug, info, warn, error...
func initLogger() {
	var cfg struct {
		Mod map[string]string `yaml:"log"`
	}

	cfg.Mod = modules
	LoadConfig(&cfg)

	var writer io.Writer
	var terminal bool
	switch output, path, _ := strings.Cut(modules["output"], ":"); output {
	case "stderr":
		writer, terminal = os.Stderr, isatty.IsTerminal(os.Stderr.Fd())
	case "stdout":
		writer, terminal = os.Stdout, isatty.IsTerminal(os.Stdout.Fd())
	case "file":
		if path == "" {
			path = "go2rtc.log"
		}
		writer, _ = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	}

	if writer != nil {
		writer = io.MultiWriter(writer, MemoryLog)
	} else {
		writer = MemoryLog
	}
	writer = creds.SecretWriter(writer)

	encoderConfig := zapcore.EncoderConfig{
		MessageKey:     "msg",
		LevelKey:       "level",
		TimeKey:        "time",
		CallerKey:      "caller",
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     logTimeEncoder(modules["time"]),
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
	if modules["time"] == "" {
		encoderConfig.TimeKey = ""
	}

	var encoder zapcore.Encoder
	if format := modules["format"]; format == "json" {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		if format == "color" || (format == "" && terminal) {
			encoderConfig.EncodeLevel = zapcore.LowercaseColorLevelEncoder
		}
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	logMu.Lock()
	logEncoder = encoder
	logOutput = zapcore.AddSync(writer)
	logLevels = make(map[string]zap.AtomicLevel)
	logMu.Unlock()

	Logger = GetLogger("app")
}

func logTimeEncoder(format string) zapcore.TimeEncoder {
	switch strings.ToUpper(format) {
	case "UNIXMICRO":
		return func(t time.Time, enc zapcore.PrimitiveArrayEncoder) { enc.AppendInt64(t.UnixMicro()) }
	case "UNIXNANO":
		return zapcore.EpochNanosTimeEncoder
	case "ISO8601":
		return zapcore.ISO8601TimeEncoder
	default: // Keep the historical Camera Kernel default: UNIXMS.
		return zapcore.EpochMillisTimeEncoder
	}
}

// modules contains log output settings and optional per-module levels.
var modules = map[string]string{
	"format": "",
	"level":  "info",
	// HomeKit negotiation is useful when diagnosing pairing/media failures,
	// but its normal characteristic and session traffic is too noisy for the
	// per-camera diagnostic log. It can still be overridden with log.homekit.
	"homekit": "warn",
	"output":  "stdout",
	"time":    "UNIXMS",
}

const (
	chunkCount = 16
	chunkSize  = 1 << 16
)

type circularBuffer struct {
	chunks [][]byte
	r, w   int
	mu     sync.Mutex
}

func newBuffer() *circularBuffer {
	b := &circularBuffer{chunks: make([][]byte, 0, chunkCount)}
	b.chunks = append(b.chunks, make([]byte, 0, chunkSize))
	return b
}

func (b *circularBuffer) Write(p []byte) (n int, err error) {
	n = len(p)
	b.mu.Lock()
	if len(b.chunks[b.w])+n > chunkSize {
		if b.w++; b.w == chunkCount {
			b.w = 0
		}
		if b.r == b.w {
			if b.r++; b.r == chunkCount {
				b.r = 0
			}
		}
		if b.w == len(b.chunks) {
			b.chunks = append(b.chunks, make([]byte, 0, chunkSize))
		} else {
			b.chunks[b.w] = b.chunks[b.w][:0]
		}
	}
	b.chunks[b.w] = append(b.chunks[b.w], p...)
	b.mu.Unlock()
	return
}

func (b *circularBuffer) WriteTo(w io.Writer) (n int64, err error) {
	buf := make([]byte, 0, chunkCount*chunkSize)
	b.mu.Lock()
	for i := b.r; ; {
		buf = append(buf, b.chunks[i]...)
		if i == b.w {
			break
		}
		if i++; i == chunkCount {
			i = 0
		}
	}
	b.mu.Unlock()
	nn, err := w.Write(buf)
	return int64(nn), err
}

func (b *circularBuffer) Reset() {
	b.mu.Lock()
	b.chunks[0] = b.chunks[0][:0]
	b.r = 0
	b.w = 0
	b.mu.Unlock()
}
