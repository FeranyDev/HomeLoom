package rtsp

import (
	"errors"
	"io"
	"net"
	"net/url"
	"strings"

	"github.com/AlexxIT/go2rtc/internal/app"
	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/rtsp"
	"github.com/AlexxIT/go2rtc/pkg/tcp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func Init() {
	var conf struct {
		Mod struct {
			Listen       string `yaml:"listen" json:"listen"`
			Username     string `yaml:"username" json:"-"`
			Password     string `yaml:"password" json:"-"`
			DefaultQuery string `yaml:"default_query" json:"default_query"`
			PacketSize   uint16 `yaml:"pkt_size" json:"pkt_size,omitempty"`
		} `yaml:"rtsp"`
	}

	// default config
	conf.Mod.Listen = ":8554"
	conf.Mod.DefaultQuery = "video&audio"

	app.LoadConfig(&conf)
	app.Info["rtsp"] = conf.Mod

	log = app.GetLogger("rtsp")

	// RTSP client support
	streams.HandleFunc("rtsp", rtspHandler)
	streams.HandleFunc("rtsps", rtspHandler)
	streams.HandleFunc("rtspx", rtspHandler)

	// RTSP server support
	address := conf.Mod.Listen
	if address == "" {
		return
	}

	ln, err := net.Listen("tcp", address)
	if err != nil {
		log.Error("listen failed", zap.Error(err), zap.String("addr", address))
		return
	}

	_, Port, _ = net.SplitHostPort(address)

	log.Info("listening", zap.String("addr", address))

	if query, err := url.ParseQuery(conf.Mod.DefaultQuery); err == nil {
		defaultMedias = ParseQuery(query)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			c := rtsp.NewServer(conn)
			c.PacketSize = conf.Mod.PacketSize
			// skip check auth for localhost
			if conf.Mod.Username != "" && !conn.RemoteAddr().(*net.TCPAddr).IP.IsLoopback() {
				c.Auth(conf.Mod.Username, conf.Mod.Password)
			}
			go tcpHandler(c)
		}
	}()
}

type Handler func(conn *rtsp.Conn) bool

func HandleFunc(handler Handler) {
	handlers = append(handlers, handler)
}

var Port string

// internal

var log = zap.NewNop()
var handlers []Handler
var defaultMedias []*core.Media

func rtspHandler(rawURL string) (core.Producer, error) {
	rawURL, rawQuery, _ := strings.Cut(rawURL, "#")

	conn := rtsp.NewClient(rawURL)
	conn.Backchannel = true
	conn.UserAgent = app.UserAgent

	if rawQuery != "" {
		query := streams.ParseQuery(rawQuery)
		conn.Backchannel = query.Get("backchannel") == "1"
		conn.Media = query.Get("media")
		conn.Timeout = core.Atoi(query.Get("timeout"))
		conn.Transport = query.Get("transport")
	}

	if log.Core().Enabled(zap.DebugLevel) {
		conn.Listen(func(msg any) {
			switch msg := msg.(type) {
			case *tcp.Request:
				log.Debug("client request", zap.Stringer("request", msg))
			case *tcp.Response:
				log.Debug("client response", zap.Stringer("response", msg))
			case string:
				log.Debug("client message", zap.String("message", msg))
			}
		})
	}

	if err := conn.Dial(); err != nil {
		return nil, err
	}

	if err := conn.Describe(); err != nil {
		if !conn.Backchannel {
			return nil, err
		}
		log.Debug("describe failed", zap.Bool("backchannel", conn.Backchannel), zap.Error(err))

		// second try without backchannel, we need to reconnect
		conn.Backchannel = false
		if err = conn.Dial(); err != nil {
			return nil, err
		}
		if err = conn.Describe(); err != nil {
			return nil, err
		}
	}

	return conn, nil
}

func tcpHandler(conn *rtsp.Conn) {
	var name string
	var closer func()

	trace := log.Core().Enabled(zap.DebugLevel)
	level := zapcore.WarnLevel

	conn.Listen(func(msg any) {
		if trace {
			switch msg := msg.(type) {
			case *tcp.Request:
				log.Debug("server request", zap.Stringer("request", msg))
			case *tcp.Response:
				log.Debug("server response", zap.Stringer("response", msg))
			}
		}

		switch msg {
		case rtsp.MethodDescribe:
			if len(conn.URL.Path) == 0 {
				log.Warn("server received DESCRIBE with empty URL")
				return
			}

			name = conn.URL.Path[1:]

			stream := streams.Get(name)
			if stream == nil {
				return
			}

			log.Debug("new consumer", zap.String("stream", name))

			conn.SessionName = app.UserAgent

			query := conn.URL.Query()
			conn.Medias = ParseQuery(query)
			if conn.Medias == nil {
				for _, media := range defaultMedias {
					conn.Medias = append(conn.Medias, media.Clone())
				}
			}

			if query.Get("backchannel") == "1" {
				conn.Medias = append(conn.Medias, &core.Media{
					Kind:      core.KindAudio,
					Direction: core.DirectionRecvonly,
					Codecs: []*core.Codec{
						{Name: core.CodecOpus, ClockRate: 48000, Channels: 2},
						{Name: core.CodecPCM, ClockRate: 16000},
						{Name: core.CodecPCMA, ClockRate: 16000},
						{Name: core.CodecPCMU, ClockRate: 16000},
						{Name: core.CodecPCM, ClockRate: 8000},
						{Name: core.CodecPCMA, ClockRate: 8000},
						{Name: core.CodecPCMU, ClockRate: 8000},
						{Name: core.CodecAAC, ClockRate: 8000},
						{Name: core.CodecAAC, ClockRate: 16000},
					},
				})
			}

			if s := query.Get("pkt_size"); s != "" {
				conn.PacketSize = uint16(core.Atoi(s))
			}

			// param name like ffmpeg style https://ffmpeg.org/ffmpeg-protocols.html
			if s := query.Get("log_level"); s != "" {
				var parsed zapcore.Level
				if strings.EqualFold(s, "trace") {
					level = zapcore.DebugLevel
				} else if err := parsed.Set(s); err == nil {
					level = parsed
				}
			}

			// will help to protect looping requests to same source
			conn.Connection.Source = query.Get("source")

			if err := stream.AddConsumer(conn); err != nil {
				if checked := log.Check(level, "consumer setup failed"); checked != nil {
					checked.Write(zap.Error(err), zap.String("stream", name))
				}
				return
			}

			closer = func() {
				stream.RemoveConsumer(conn)
			}

		case rtsp.MethodAnnounce:
			if len(conn.URL.Path) == 0 {
				log.Warn("server received ANNOUNCE with empty URL")
				return
			}

			name = conn.URL.Path[1:]

			stream := streams.Get(name)
			if stream == nil {
				return
			}

			query := conn.URL.Query()
			if s := query.Get("timeout"); s != "" {
				conn.Timeout = core.Atoi(s)
			}

			log.Debug("new producer", zap.String("stream", name))

			stream.AddProducer(conn)

			closer = func() {
				stream.RemoveProducer(conn)
			}
		}
	})

	if err := conn.Accept(); err != nil {
		if errors.Is(err, rtsp.FailedAuth) {
			log.Warn("authentication failed", zap.String("remote_addr", conn.Connection.RemoteAddr))
		} else if err != io.EOF {
			if checked := log.Check(level, "connection failed"); checked != nil {
				checked.Write(zap.Error(err), zap.String("remote_addr", conn.Connection.RemoteAddr))
			}
		}
		if closer != nil {
			closer()
		}
		_ = conn.Close()
		return
	}

	for _, handler := range handlers {
		if handler(conn) {
			return
		}
	}

	if closer != nil {
		if err := conn.Handle(); err != nil {
			log.Debug("connection handling failed", zap.Error(err), zap.String("stream", name))
		}

		closer()

		log.Debug("disconnected", zap.String("stream", name))
	}

	_ = conn.Close()
}

func ParseQuery(query map[string][]string) []*core.Media {
	if v := query["mp4"]; v != nil {
		return []*core.Media{
			{
				Kind:      core.KindVideo,
				Direction: core.DirectionSendonly,
				Codecs: []*core.Codec{
					{Name: core.CodecH264},
					{Name: core.CodecH265},
				},
			},
			{
				Kind:      core.KindAudio,
				Direction: core.DirectionSendonly,
				Codecs: []*core.Codec{
					{Name: core.CodecAAC},
				},
			},
		}
	}

	return core.ParseQuery(query)
}
