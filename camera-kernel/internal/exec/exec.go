package exec

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AlexxIT/go2rtc/internal/app"
	"github.com/AlexxIT/go2rtc/internal/rtsp"
	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/magic"
	"github.com/AlexxIT/go2rtc/pkg/pcm"
	pkg "github.com/AlexxIT/go2rtc/pkg/rtsp"
	"github.com/AlexxIT/go2rtc/pkg/shell"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func Init() {
	var cfg struct {
		Mod struct {
			AllowPaths []string `yaml:"allow_paths"`
		} `yaml:"exec"`
	}

	app.LoadConfig(&cfg)

	allowPaths = cfg.Mod.AllowPaths

	rtsp.HandleFunc(func(conn *pkg.Conn) bool {
		waitersMu.Lock()
		waiter := waiters[conn.URL.Path]
		waitersMu.Unlock()

		if waiter == nil {
			return false
		}

		// unblocking write to channel
		select {
		case waiter <- conn:
			return true
		default:
			return false
		}
	})

	streams.HandleFunc("exec", execHandle)
	streams.MarkInsecure("exec")

	log = app.GetLogger("exec")
}

var allowPaths []string

func execHandle(rawURL string) (prod core.Producer, err error) {
	rawURL, rawQuery, _ := strings.Cut(rawURL, "#")
	query := streams.ParseQuery(rawQuery)

	var path string

	// RTSP flow should have `{output}` inside URL
	// pipe flow may have `#{params}` inside URL
	if i := strings.Index(rawURL, "{output}"); i > 0 {
		if rtsp.Port == "" {
			return nil, errors.New("exec: rtsp module disabled")
		}

		sum := md5.Sum([]byte(rawURL))
		path = "/" + hex.EncodeToString(sum[:])
		rawURL = rawURL[:i] + "rtsp://127.0.0.1:" + rtsp.Port + path + rawURL[i+8:]
	}

	cmd := shell.NewCommand(rawURL[5:]) // remove `exec:`
	logSource := executableLogSource(cmd.Args[0])
	processLog := app.GetLogger(logSource)
	writer := newLogWriter(processLog, logSource)
	cmd.Stderr = writer

	if allowPaths != nil && !slices.Contains(allowPaths, cmd.Args[0]) {
		_ = cmd.Close()
		return nil, errors.New("exec: bin not in allow_paths: " + cmd.Args[0])
	}

	if s := query.Get("killsignal"); s != "" {
		sig := syscall.Signal(core.Atoi(s))
		cmd.Cancel = func() error {
			log.Debug("process signal sent", zap.Int("signal", int(sig)))
			return cmd.Process.Signal(sig)
		}
	}
	if usesHardwareAccel(cmd.Args) {
		writer.onHardwareFailure = func() {
			log.Warn("hardware acceleration failed; stopping process for software fallback")
			_ = cmd.Close()
		}
	}

	if s := query.Get("killtimeout"); s != "" {
		cmd.WaitDelay = time.Duration(core.Atoi(s)) * time.Second
	}

	if query.Get("backchannel") == "1" {
		return pcm.NewBackchannel(cmd, query.Get("audio"))
	}

	var timeout time.Duration
	if s := query.Get("starttimeout"); s != "" {
		timeout = time.Duration(core.Atoi(s)) * time.Second
	} else {
		timeout = 30 * time.Second
	}

	if path == "" {
		prod, err = handlePipe(rawURL, cmd)
	} else {
		prod, err = handleRTSP(rawURL, cmd, path, timeout)
	}

	if err != nil {
		_ = cmd.Close()
	}

	return
}

func handlePipe(source string, cmd *shell.Command) (core.Producer, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	rd := struct {
		io.Reader
		io.Closer
	}{
		// add buffer for pipe reader to reduce syscall
		bufio.NewReaderSize(stdout, core.BufferSize),
		// stop cmd on close pipe call
		cmd,
	}

	log.Debug("pipe process starting", zap.Strings("args", cmd.Args))

	ts := time.Now()

	if err = cmd.Start(); err != nil {
		return nil, err
	}

	prod, err := magic.Open(rd)
	if err != nil {
		return nil, fmt.Errorf("exec/pipe: %w\n%s", err, cmd.Stderr)
	}

	if info, ok := prod.(core.Info); ok {
		info.SetProtocol("pipe")
		setRemoteInfo(info, source, cmd.Args)
	}

	log.Debug("pipe process started", zap.Duration("launch_duration", time.Since(ts)))

	return prod, nil
}

func handleRTSP(source string, cmd *shell.Command, path string, timeout time.Duration) (core.Producer, error) {
	if log.Core().Enabled(zap.DebugLevel) {
		cmd.Stdout = os.Stdout
	}

	waiter := make(chan *pkg.Conn, 1)

	waitersMu.Lock()
	waiters[path] = waiter
	waitersMu.Unlock()

	defer func() {
		waitersMu.Lock()
		delete(waiters, path)
		waitersMu.Unlock()
	}()

	log.Debug("RTSP process starting", zap.Strings("args", cmd.Args))

	ts := time.Now()

	if err := cmd.Start(); err != nil {
		log.Error("RTSP process failed to start", zap.Error(err), zap.String("source", source))
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-timer.C:
		// haven't received data from app in timeout
		log.Error("RTSP process startup timed out", zap.String("source", source), zap.Duration("timeout", timeout))
		return nil, errors.New("exec: timeout")
	case <-cmd.Done():
		// app fail before we receive any data
		return nil, fmt.Errorf("exec/rtsp\n%s", cmd.Stderr)
	case prod := <-waiter:
		// app started successfully
		log.Debug("RTSP process started", zap.Duration("launch_duration", time.Since(ts)))
		setRemoteInfo(prod, source, cmd.Args)
		prod.OnClose = cmd.Close
		return prod, nil
	}
}

// internal

var (
	log       = zap.NewNop()
	waiters   = make(map[string]chan *pkg.Conn)
	waitersMu sync.Mutex
)

type logWriter struct {
	buf               []byte
	enabled           bool
	logger            *zap.Logger
	logLevel          zapcore.Level
	source            string
	n                 int
	hardwareFailures  int
	onHardwareFailure func()
	hardwareKillArmed bool
}

// newLogWriter preserves subprocess stderr at the configured module level.
// FFmpeg writes diagnostics to stderr even for non-fatal conditions, so using
// a fixed Info event would make that output disappear when log.ffmpeg is warn
// or error. We use the least severe enabled non-terminal level instead.
func newLogWriter(logger *zap.Logger, source string) *logWriter {
	level := subprocessLogLevel(logger)
	return &logWriter{
		buf:      make([]byte, 512),
		enabled:  logger.Core().Enabled(level),
		logger:   logger,
		logLevel: level,
		source:   source,
	}
}

func subprocessLogLevel(logger *zap.Logger) zapcore.Level {
	for _, level := range []zapcore.Level{zap.InfoLevel, zap.WarnLevel, zap.ErrorLevel} {
		if logger.Core().Enabled(level) {
			return level
		}
	}
	// Do not use DPanic, Panic, or Fatal for subprocess output: those levels
	// alter process control flow. The logger will suppress this Error event if
	// its configured threshold is higher or logging is disabled.
	return zap.ErrorLevel
}

func (l *logWriter) String() string {
	if l.n == len(l.buf) {
		return string(l.buf) + "..."
	}
	return string(l.buf[:l.n])
}

func (l *logWriter) Write(p []byte) (n int, err error) {
	if l.n < cap(l.buf) {
		l.n += copy(l.buf[l.n:], p)
	}
	n = len(p)
	line := trimSpace(p)
	if l.enabled && line != nil {
		l.logger.Log(l.logLevel, string(line), zap.String("output_stream", "stderr"))
	}
	if l.onHardwareFailure != nil && !l.hardwareKillArmed && isHardwareAccelFailure(line) {
		l.hardwareFailures++
		if l.hardwareFailures >= 3 {
			l.hardwareKillArmed = true
			go l.onHardwareFailure()
		}
	}
	return
}

func executableLogSource(path string) string {
	path = strings.ToLower(path)
	if strings.Contains(path, "ffmpeg") {
		return "ffmpeg"
	}
	return "exec"
}

func usesHardwareAccel(args []string) bool {
	for _, arg := range args {
		switch {
		case arg == "videotoolbox", arg == "vaapi", arg == "cuda", arg == "dxva2", arg == "rkmpp",
			arg == "h264_videotoolbox", arg == "hevc_videotoolbox",
			arg == "h264_vaapi", arg == "hevc_vaapi",
			arg == "h264_nvenc", arg == "hevc_nvenc",
			arg == "h264_qsv", arg == "hevc_qsv",
			arg == "h264_v4l2m2m", arg == "hevc_v4l2m2m",
			arg == "h264_rkmpp", arg == "hevc_rkmpp",
			strings.Contains(arg, "videotoolbox"),
			strings.Contains(arg, "_vaapi"),
			strings.Contains(arg, "_nvenc"),
			strings.Contains(arg, "_qsv"),
			strings.Contains(arg, "_rkmpp"):
			return true
		}
	}
	return false
}

func isHardwareAccelFailure(line []byte) bool {
	if len(line) == 0 {
		return false
	}
	s := string(line)
	return strings.Contains(s, "hardware accelerator failed") ||
		strings.Contains(s, "output image buffer is null") ||
		(strings.Contains(s, "Error while decoding stream") && strings.Contains(s, "Unknown error")) ||
		strings.Contains(s, "Cannot create a videotoolbox") ||
		(strings.Contains(s, "VideoToolbox encoder") && strings.Contains(s, "failed")) ||
		strings.Contains(s, "-12908") ||
		strings.Contains(s, "-17694")
}

func trimSpace(b []byte) []byte {
	start := 0
	stop := len(b)
	for ; start < stop; start++ {
		if b[start] >= ' ' {
			break // trim all ASCII before 0x20
		}
	}
	for ; ; stop-- {
		if stop == start {
			return nil // skip empty output
		}
		if b[stop-1] > ' ' {
			break // trim all ASCII before 0x21
		}
	}
	return b[start:stop]
}

func setRemoteInfo(info core.Info, source string, args []string) {
	info.SetSource(source)

	if i := core.Index(args, "-i"); i > 0 && i < len(args)-1 {
		rawURL := args[i+1]
		if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
			info.SetRemoteAddr(u.Host)
			info.SetURL(rawURL)
		}
	}
}
