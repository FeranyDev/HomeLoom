package ffmpeg

import (
	"bytes"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/creds"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestParseQuery(t *testing.T) {
	args := parseQuery(nil)
	require.Equal(t, `ffmpeg -hide_banner -i - -c:v mjpeg -f mjpeg -`, args.String())

	query, err := url.ParseQuery("h=480")
	require.Nil(t, err)
	args = parseQuery(query)
	require.Equal(t, `ffmpeg -hide_banner -i - -c:v mjpeg -vf "scale=-1:480" -f mjpeg -`, args.String())

	query, err = url.ParseQuery("hw=vaapi")
	require.Nil(t, err)
	args = parseQuery(query)
	require.Equal(t, `ffmpeg -hide_banner -init_hw_device vaapi -hwaccel_output_format vaapi -hwaccel_flags allow_profile_mismatch -i - -c:v mjpeg_vaapi -vf "format=vaapi|nv12,hwupload" -f mjpeg -`, args.String())
}

func TestLogSnapshotTranscodeFailureIncludesBoundedRedactedDiagnostic(t *testing.T) {
	const secret = "snapshot-diagnostic-secret"
	const sourceURI = "rtsp://camera:snapshot-diagnostic-secret@example.invalid/live"

	var output bytes.Buffer
	oldLog := log
	t.Cleanup(func() { log = oldLog })
	creds.AddSecret(secret)
	encoder := zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		MessageKey:  "msg",
		LevelKey:    "level",
		EncodeLevel: zapcore.LowercaseLevelEncoder,
	})
	log = zap.New(zapcore.NewCore(encoder, zapcore.AddSync(creds.SecretWriter(&output)), zap.ErrorLevel))

	diagnostic := &boundedDiagnosticBuffer{remaining: 4096}
	_, _ = diagnostic.Write([]byte("failed to read " + sourceURI + "; token=snapshot-diagnostic-secret\n"))
	_, _ = diagnostic.Write(bytes.Repeat([]byte("x"), 4096))
	_, _ = diagnostic.Write([]byte("must-not-appear"))

	_, err := snapshotTranscodeResult(nil, diagnostic.Bytes(), errors.New("exit status 1"))
	require.EqualError(t, err, "snapshot transcode failed: exit status 1")

	text := output.String()
	for _, want := range []string{`"level":"error"`, `"msg":"snapshot transcode failed"`, `"stderr":"failed to read <redacted-uri>; token=***`} {
		if !strings.Contains(text, want) {
			t.Fatalf("log = %q, missing %q", text, want)
		}
	}
	if strings.Contains(text, secret) || strings.Contains(text, sourceURI) || strings.Contains(text, "rtsp://") || strings.Contains(text, "must-not-appear") {
		t.Fatalf("snapshot diagnostic leaked an unredacted or over-limit value: %q", text)
	}
}

func TestSnapshotTranscodeResultDoesNotLogSuccess(t *testing.T) {
	var output bytes.Buffer
	oldLog := log
	t.Cleanup(func() { log = oldLog })
	encoder := zapcore.NewJSONEncoder(zapcore.EncoderConfig{MessageKey: "msg", LevelKey: "level", EncodeLevel: zapcore.LowercaseLevelEncoder})
	log = zap.New(zapcore.NewCore(encoder, zapcore.AddSync(&output), zap.ErrorLevel))

	payload, err := snapshotTranscodeResult([]byte("jpeg"), []byte("ffmpeg warning"), nil)
	require.NoError(t, err)
	require.Equal(t, []byte("jpeg"), payload)

	if text := output.String(); text != "" {
		t.Fatalf("log = %q", text)
	}
}
