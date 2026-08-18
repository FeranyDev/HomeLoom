package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"

	"github.com/AlexxIT/go2rtc/internal/ffmpeg/hardware"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/ffmpeg"
	"github.com/AlexxIT/go2rtc/pkg/shell"
	"go.uber.org/zap"
)

func JPEGWithQuery(b []byte, query url.Values) ([]byte, error) {
	args := parseQuery(query)
	return transcode(b, args.String())
}

func JPEGWithScale(b []byte, width, height int) ([]byte, error) {
	args := defaultArgs()
	args.AddFilter(fmt.Sprintf("scale=%d:%d", width, height))
	return transcode(b, args.String())
}

func JPEGWithScaleContext(ctx context.Context, b []byte, width, height int) ([]byte, error) {
	args := defaultArgs()
	args.AddFilter(fmt.Sprintf("scale=%d:%d", width, height))
	args.Output = "-frames:v 1 -f image2pipe -vcodec mjpeg -"

	cmdArgs := shell.QuoteSplit(args.String())
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Stdin = bytes.NewReader(b)
	output := &boundedSnapshotBuffer{remaining: maxSnapshotBytes}
	cmd.Stdout = output
	stderr := &boundedDiagnosticBuffer{remaining: 4096}
	cmd.Stderr = stderr
	return snapshotTranscodeResult(output.Bytes(), stderr.Bytes(), cmd.Run())
}

func snapshotTranscodeResult(output, stderr []byte, err error) ([]byte, error) {
	if err == nil {
		return output, nil
	}

	fields := []zap.Field{zap.Error(err)}
	if diagnostic := snapshotDiagnostic(stderr); diagnostic != "" {
		fields = append(fields, zap.String("stderr", diagnostic))
	}
	// The configured ffmpeg logger writes through app's secret-redacting sink.
	// Keep the command arguments and input out of this event: snapshots are
	// supplied over stdin, and logging those values could expose a source URI.
	log.Error("snapshot transcode failed", fields...)
	return nil, fmt.Errorf("snapshot transcode failed: %w", err)
}

var snapshotURI = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>,;]+`)

func snapshotDiagnostic(stderr []byte) string {
	// FFmpeg normally receives snapshots through stdin, but its diagnostics can
	// still repeat a URI supplied by a wrapper. Do not expose that URI in the
	// runtime log; app's logger then redacts any registered secrets left in the
	// remaining diagnostic text.
	return snapshotURI.ReplaceAllString(string(stderr), "<redacted-uri>")
}

func transcode(b []byte, args string) ([]byte, error) {
	cmdArgs := shell.QuoteSplit(args)
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Stdin = bytes.NewBuffer(b)
	return cmd.Output()
}

func defaultArgs() *ffmpeg.Args {
	return &ffmpeg.Args{
		Bin:    defaults["bin"],
		Global: defaults["global"],
		Input:  "-i -",
		Codecs: []string{defaults["mjpeg"]},
		Output: defaults["output/mjpeg"],
	}
}

func parseQuery(query url.Values) *ffmpeg.Args {
	args := defaultArgs()

	var width = -1
	var height = -1
	var r, hw string

	for k, v := range query {
		switch k {
		case "width", "w":
			width = core.Atoi(v[0])
		case "height", "h":
			height = core.Atoi(v[0])
		case "rotate":
			r = v[0]
		case "hardware", "hw":
			hw = v[0]
		}
	}

	if width > 0 || height > 0 {
		args.AddFilter(fmt.Sprintf("scale=%d:%d", width, height))
	}

	if r != "" {
		switch r {
		case "90":
			args.AddFilter("transpose=1") // 90 degrees clockwise
		case "180":
			args.AddFilter("transpose=1,transpose=1")
		case "-90", "270":
			args.AddFilter("transpose=2") // 90 degrees counterclockwise
		}
	}

	if hw != "" {
		hardware.MakeHardware(args, hw, defaults)
	}

	return args
}

type boundedDiagnosticBuffer struct {
	bytes.Buffer
	remaining int
}

func (b *boundedDiagnosticBuffer) Write(payload []byte) (int, error) {
	originalLength := len(payload)
	if b.remaining <= 0 {
		return originalLength, nil
	}
	if len(payload) > b.remaining {
		payload = payload[:b.remaining]
	}
	n, _ := b.Buffer.Write(payload)
	b.remaining -= n
	// Report the original write as accepted so an overlong diagnostic cannot
	// make FFmpeg fail for an unrelated reason.
	return originalLength, nil
}
