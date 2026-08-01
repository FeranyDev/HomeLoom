package exec

import (
	"bytes"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestLogWriterForwardsFFmpegStderrAtInfo(t *testing.T) {
	var output bytes.Buffer
	encoder := zapcore.NewJSONEncoder(zapcore.EncoderConfig{MessageKey: "msg", LevelKey: "level", EncodeLevel: zapcore.LowercaseLevelEncoder})
	logger := zap.New(zapcore.NewCore(encoder, zapcore.AddSync(&output), zap.InfoLevel)).With(
		zap.String("component", "camera-kernel"), zap.String("module", "ffmpeg"))
	writer := &logWriter{buf: make([]byte, 512), enabled: true, logger: logger, source: executableLogSource("/opt/homeloom/ffmpeg")}
	if _, err := writer.Write([]byte("decoder warning\n")); err != nil {
		t.Fatal(err)
	}
	if text := output.String(); !strings.Contains(text, `"msg":"decoder warning"`) || !strings.Contains(text, `"module":"ffmpeg"`) || !strings.Contains(text, `"output_stream":"stderr"`) {
		t.Fatalf("log = %q", text)
	}
}

func TestExecutableLogSourceFallsBackToExec(t *testing.T) {
	if got := executableLogSource("/usr/bin/helper"); got != "exec" {
		t.Fatalf("source = %q", got)
	}
}
