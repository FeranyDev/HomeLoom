package exec

import (
	"bytes"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestLogWriterForwardsFFmpegStderrAtConfiguredLevel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		level zapcore.Level
		want  string
	}{
		{name: "info", level: zap.InfoLevel, want: "info"},
		{name: "warn", level: zap.WarnLevel, want: "warn"},
		{name: "error", level: zap.ErrorLevel, want: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			encoder := zapcore.NewJSONEncoder(zapcore.EncoderConfig{MessageKey: "msg", LevelKey: "level", EncodeLevel: zapcore.LowercaseLevelEncoder})
			logger := zap.New(zapcore.NewCore(encoder, zapcore.AddSync(&output), tc.level)).With(
				zap.String("component", "camera-kernel"), zap.String("module", "ffmpeg"))
			writer := newLogWriter(logger, executableLogSource("/opt/homeloom/ffmpeg"))
			if _, err := writer.Write([]byte("decoder warning\n")); err != nil {
				t.Fatal(err)
			}

			text := output.String()
			for _, want := range []string{
				`"level":"` + tc.want + `"`,
				`"msg":"decoder warning"`,
				`"module":"ffmpeg"`,
				`"output_stream":"stderr"`,
			} {
				if !strings.Contains(text, want) {
					t.Fatalf("log = %q, missing %q", text, want)
				}
			}
		})
	}
}

func TestExecutableLogSourceFallsBackToExec(t *testing.T) {
	if got := executableLogSource("/usr/bin/helper"); got != "exec" {
		t.Fatalf("source = %q", got)
	}
}
