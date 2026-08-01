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
	"github.com/rs/zerolog"
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
	writer := &logWriter{
		buf:   make([]byte, 512),
		debug: log.Debug().Enabled(),
	}
	cmd.Stderr = writer

	if allowPaths != nil && !slices.Contains(allowPaths, cmd.Args[0]) {
		_ = cmd.Close()
		return nil, errors.New("exec: bin not in allow_paths: " + cmd.Args[0])
	}

	if s := query.Get("killsignal"); s != "" {
		sig := syscall.Signal(core.Atoi(s))
		cmd.Cancel = func() error {
			log.Debug().Msgf("[exec] kill with signal=%d", sig)
			return cmd.Process.Signal(sig)
		}
	}
	if usesHardwareAccel(cmd.Args) {
		writer.onHardwareFailure = func() {
			log.Warn().Msg("[exec] hardware acceleration failed; stopping process for software fallback")
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

	log.Debug().Strs("args", cmd.Args).Msg("[exec] run pipe")

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

	log.Debug().Stringer("launch", time.Since(ts)).Msg("[exec] run pipe")

	return prod, nil
}

func handleRTSP(source string, cmd *shell.Command, path string, timeout time.Duration) (core.Producer, error) {
	if log.Trace().Enabled() {
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

	log.Debug().Strs("args", cmd.Args).Msg("[exec] run rtsp")

	ts := time.Now()

	if err := cmd.Start(); err != nil {
		log.Error().Err(err).Str("source", source).Msg("[exec]")
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-timer.C:
		// haven't received data from app in timeout
		log.Error().Str("source", source).Msg("[exec] timeout")
		return nil, errors.New("exec: timeout")
	case <-cmd.Done():
		// app fail before we receive any data
		return nil, fmt.Errorf("exec/rtsp\n%s", cmd.Stderr)
	case prod := <-waiter:
		// app started successfully
		log.Debug().Stringer("launch", time.Since(ts)).Msg("[exec] run rtsp")
		setRemoteInfo(prod, source, cmd.Args)
		prod.OnClose = cmd.Close
		return prod, nil
	}
}

// internal

var (
	log       zerolog.Logger
	waiters   = make(map[string]chan *pkg.Conn)
	waitersMu sync.Mutex
)

type logWriter struct {
	buf                 []byte
	debug               bool
	n                   int
	hardwareFailures    int
	onHardwareFailure   func()
	hardwareKillArmed   bool
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
	if l.debug && line != nil {
		log.Debug().Msgf("[exec] %s", line)
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
