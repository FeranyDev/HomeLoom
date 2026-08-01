package homekit

import (
	"fmt"
	"io"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/h264"
	"github.com/AlexxIT/go2rtc/pkg/hap/camera"
	"github.com/AlexxIT/go2rtc/pkg/opus"
	"github.com/AlexxIT/go2rtc/pkg/srtp"
	"github.com/pion/rtp"
)

type Consumer struct {
	core.Connection
	conn      net.Conn
	srtpBase  *srtp.Server
	videoSRTP *srtp.Server
	audioSRTP *srtp.Server

	deadline *time.Timer
	done     chan struct{}

	sessionID         string
	localSRTPAddress  string
	srtpBindMode      string
	videoSession      *srtp.Session
	audioSession      *srtp.Session
	audioRTPTime      byte
	audioSampleRate   int
	videoMTU          uint16
	stopOnce          sync.Once
	stopErr           error
	stage             atomic.Uint32
	statusMTU         atomic.Uint32
	setupFailed       atomic.Bool
	ipv4OnlyRejected  atomic.Bool
	videoSelection    VideoSelection
	videoStarted      atomic.Bool
	videoPrimed       atomic.Bool
	videoSPSUnits     atomic.Uint64
	videoPPSUnits     atomic.Uint64
	videoIDRUnits     atomic.Uint64
	videoSTAPAUnits   atomic.Uint64
	videoSTAPAZeroNRI atomic.Uint64
	videoMaxDatagram  atomic.Uint32
}

type VideoSelection struct {
	Width           uint16
	Height          uint16
	Framerate       uint8
	MaxBitrate      uint16
	ProfileID       byte
	Level           byte
	AudioSampleRate int
	AudioPacketTime uint8
	AudioMaxBitrate uint16
}

type SessionStatus struct {
	Active                    bool   `json:"active"`
	State                     string `json:"state"`
	MediaBindMode             string `json:"mediaBindMode,omitempty"`
	VideoWidth                uint16 `json:"videoWidth,omitempty"`
	VideoHeight               uint16 `json:"videoHeight,omitempty"`
	VideoFramerate            uint8  `json:"videoFramerate,omitempty"`
	VideoMaxBitrate           uint16 `json:"videoMaxBitrateKbps,omitempty"`
	VideoProfileID            byte   `json:"videoProfileId"`
	VideoLevel                byte   `json:"videoLevel"`
	VideoMTU                  uint16 `json:"videoMtu,omitempty"`
	VideoPackets              uint64 `json:"videoPackets"`
	VideoBytes                uint64 `json:"videoBytes"`
	VideoWriteErrors          uint64 `json:"videoWriteErrors"`
	VideoSSRC                 uint32 `json:"videoSsrc"`
	VideoRTCPDatagrams        uint64 `json:"videoRtcpDatagrams"`
	VideoRTCPPackets          uint64 `json:"videoRtcpPackets"`
	VideoRTCPFailures         uint64 `json:"videoRtcpFailures"`
	VideoRTCPParseErrors      uint64 `json:"videoRtcpParseErrors"`
	VideoRTCPReceiverReports  uint64 `json:"videoRtcpReceiverReports"`
	VideoRTCPReportBlocks     uint64 `json:"videoRtcpReportBlocks"`
	VideoRTCPMatchedReports   uint64 `json:"videoRtcpMatchedReports"`
	VideoRTCPReportedSSRC     uint32 `json:"videoRtcpReportedSsrc"`
	VideoRTCPFractionLost     uint32 `json:"videoRtcpFractionLost"`
	VideoRTCPTotalLost        uint32 `json:"videoRtcpTotalLost"`
	VideoRTCPLastSequence     uint32 `json:"videoRtcpLastSequence"`
	VideoRTCPJitter           uint32 `json:"videoRtcpJitter"`
	VideoRTCPLastSenderReport uint32 `json:"videoRtcpLastSenderReport"`
	VideoRTCPPLI              uint64 `json:"videoRtcpPli"`
	VideoRTCPFIR              uint64 `json:"videoRtcpFir"`
	VideoRTCPNACK             uint64 `json:"videoRtcpNack"`
	VideoSPSUnits             uint64 `json:"videoSpsUnits"`
	VideoPPSUnits             uint64 `json:"videoPpsUnits"`
	VideoIDRUnits             uint64 `json:"videoIdrUnits"`
	VideoSTAPAUnits           uint64 `json:"videoStapAUnits"`
	VideoSTAPAZeroNRI         uint64 `json:"videoStapAZeroNri"`
	VideoMaxDatagram          uint32 `json:"videoMaxDatagramBytes"`
	AudioPackets              uint64 `json:"audioPackets"`
	AudioBytes                uint64 `json:"audioBytes"`
	AudioSampleRate           int    `json:"audioSampleRateHz,omitempty"`
	AudioPacketTime           uint8  `json:"audioPacketTimeMs,omitempty"`
	AudioMaxBitrate           uint16 `json:"audioMaxBitrateKbps,omitempty"`
	AudioWriteErrors          uint64 `json:"audioWriteErrors"`
	AudioRTCPDatagrams        uint64 `json:"audioRtcpDatagrams"`
	AudioRTCPPackets          uint64 `json:"audioRtcpPackets"`
	AudioRTCPFailures         uint64 `json:"audioRtcpFailures"`
}

func NewConsumer(conn net.Conn, server *srtp.Server) *Consumer {
	medias := []*core.Media{
		{
			Kind:      core.KindVideo,
			Direction: core.DirectionSendonly,
			Codecs: []*core.Codec{
				{Name: core.CodecH264},
			},
		},
		{
			Kind:      core.KindAudio,
			Direction: core.DirectionSendonly,
			Codecs: []*core.Codec{
				// Opus RTP always uses a 48 kHz RTP clock even when the encoded
				// signal bandwidth is 16 or 24 kHz. Restricting this matcher to
				// the controller-selected sample rate drops the audio track
				// entirely. RepackToHAP below applies Apple's negotiated packet
				// time and timestamp progression.
				{Name: core.CodecOpus},
			},
		},
	}
	consumer := &Consumer{
		Connection: core.Connection{
			ID:         core.NewID(),
			FormatName: "homekit",
			Protocol:   "rtp",
			RemoteAddr: conn.RemoteAddr().String(),
			Medias:     medias,
		},
		conn:     conn,
		srtpBase: server,
		done:     make(chan struct{}),
		videoMTU: 1378,
	}
	consumer.statusMTU.Store(uint32(consumer.videoMTU))
	return consumer
}

func (c *Consumer) SessionID() string {
	return c.sessionID
}

func (c *Consumer) BelongsTo(conn net.Conn) bool {
	return c.conn == conn
}

func (c *Consumer) Status() SessionStatus {
	status := SessionStatus{
		Active:          true,
		State:           "prepared",
		MediaBindMode:   c.srtpBindMode,
		VideoWidth:      c.videoSelection.Width,
		VideoHeight:     c.videoSelection.Height,
		VideoFramerate:  c.videoSelection.Framerate,
		VideoMaxBitrate: c.videoSelection.MaxBitrate,
		VideoProfileID:  c.videoSelection.ProfileID,
		VideoLevel:      c.videoSelection.Level,
		VideoMTU:        uint16(c.statusMTU.Load()),
		AudioSampleRate: c.videoSelection.AudioSampleRate,
		AudioPacketTime: c.videoSelection.AudioPacketTime,
		AudioMaxBitrate: c.videoSelection.AudioMaxBitrate,
	}
	if c.setupFailed.Load() {
		status.State = "error"
	}
	if c.videoSession == nil || c.audioSession == nil {
		return status
	}
	video := c.videoSession.Stats()
	audio := c.audioSession.Stats()
	status.VideoPackets = video.RTPPacketsSent
	status.VideoBytes = video.RTPBytesSent
	status.VideoWriteErrors = video.RTPWriteErrors
	status.VideoSSRC = video.LocalSSRC
	status.VideoRTCPDatagrams = video.RTCPDatagramsRecv
	status.VideoRTCPPackets = video.RTCPPacketsRecv
	status.VideoRTCPFailures = video.RTCPDecryptErrors
	status.VideoRTCPParseErrors = video.RTCPParseErrors
	status.VideoRTCPReceiverReports = video.RTCPReceiverReports
	status.VideoRTCPReportBlocks = video.RTCPReportBlocks
	status.VideoRTCPMatchedReports = video.RTCPMatchedReports
	status.VideoRTCPReportedSSRC = video.RTCPReportedSSRC
	status.VideoRTCPFractionLost = video.RTCPFractionLost
	status.VideoRTCPTotalLost = video.RTCPTotalLost
	status.VideoRTCPLastSequence = video.RTCPLastSequence
	status.VideoRTCPJitter = video.RTCPJitter
	status.VideoRTCPLastSenderReport = video.RTCPLastSenderReport
	status.VideoRTCPPLI = video.RTCPPLI
	status.VideoRTCPFIR = video.RTCPFIR
	status.VideoRTCPNACK = video.RTCPNACK
	status.VideoSPSUnits = c.videoSPSUnits.Load()
	status.VideoPPSUnits = c.videoPPSUnits.Load()
	status.VideoIDRUnits = c.videoIDRUnits.Load()
	status.VideoSTAPAUnits = c.videoSTAPAUnits.Load()
	status.VideoSTAPAZeroNRI = c.videoSTAPAZeroNRI.Load()
	status.VideoMaxDatagram = c.videoMaxDatagram.Load()
	status.AudioPackets = audio.RTPPacketsSent
	status.AudioBytes = audio.RTPBytesSent
	status.AudioWriteErrors = audio.RTPWriteErrors
	status.AudioRTCPDatagrams = audio.RTCPDatagramsRecv
	status.AudioRTCPPackets = audio.RTCPPacketsRecv
	status.AudioRTCPFailures = audio.RTCPDecryptErrors
	switch {
	case status.VideoPackets > 0:
		status.State = "streaming"
	case c.stage.Load() >= 3:
		status.State = "started"
	case c.stage.Load() >= 2:
		status.State = "answered"
	}
	return status
}

func (c *Consumer) SetOffer(offer *camera.SetupEndpointsRequest) {
	if offer == nil || !validIPv4SetupOffer(offer) {
		c.ipv4OnlyRejected.Store(true)
		c.setupFailed.Store(true)
		c.srtpBindMode = "ipv4-only-rejected"
		c.videoSRTP = nil
		c.audioSRTP = nil
		c.videoSession = nil
		c.audioSession = nil
		return
	}
	c.ipv4OnlyRejected.Store(false)
	c.setupFailed.Store(false)
	c.sessionID = offer.SessionID
	c.localSRTPAddress = advertiseSRTPAddress(c.conn)
	bindAddress := c.localSRTPAddress
	c.srtpBindMode = "advertised-address"
	if c.srtpBase != nil {
		if shouldUseWildcardSRTPBind(offer.Address.IPAddr, c.localSRTPAddress) {
			// macOS Home may advertise a utun address that is also present on
			// the HomeLoom host (for example 198.19.0.1). A socket pinned to
			// the physical accessory address can receive RTCP but sends RTP
			// through the wrong local interface; Apple then reports sequence
			// zero forever. Only use a wildcard socket for such a local alias.
			// When the controller and advertised accessory address are equal,
			// bind that concrete LAN address so the SRTP source tuple is stable.
			c.srtpBindMode = "wildcard-local-controller"
			c.videoSRTP = c.srtpBase.NewWildcardSessionServer(false)
			c.audioSRTP = c.srtpBase.NewWildcardSessionServer(false)
		} else {
			c.videoSRTP = c.srtpBase.NewSessionServerAt(false, bindAddress)
			c.audioSRTP = c.srtpBase.NewSessionServerAt(false, bindAddress)
		}
	}
	c.videoSession = &srtp.Session{
		Remote: &srtp.Endpoint{
			Addr:       offer.Address.IPAddr,
			Port:       offer.Address.VideoRTPPort,
			MasterKey:  []byte(offer.VideoCrypto.MasterKey),
			MasterSalt: []byte(offer.VideoCrypto.MasterSalt),
		},
	}
	c.audioSession = &srtp.Session{
		Remote: &srtp.Endpoint{
			Addr:       offer.Address.IPAddr,
			Port:       offer.Address.AudioRTPPort,
			MasterKey:  []byte(offer.AudioCrypto.MasterKey),
			MasterSalt: []byte(offer.AudioCrypto.MasterSalt),
		},
	}
	c.stage.Store(1)
}

func (c *Consumer) GetAnswer() *camera.SetupEndpointsResponse {
	// Bind SRTP before advertising the accessory endpoint so Apple Home can
	// dial a live UDP socket immediately after the SetupEndpoints response.
	if c.ipv4OnlyRejected.Load() || c.videoSRTP == nil || c.audioSRTP == nil {
		c.setupFailed.Store(true)
		return &camera.SetupEndpointsResponse{
			SessionID: c.sessionID,
			Status:    camera.SetupEndpointsStatusError,
		}
	}
	if err := c.videoSRTP.EnsureListening(); err != nil {
		c.setupFailed.Store(true)
		return &camera.SetupEndpointsResponse{
			SessionID: c.sessionID,
			Status:    camera.SetupEndpointsStatusError,
		}
	}
	if err := c.audioSRTP.EnsureListening(); err != nil {
		c.videoSRTP.Close()
		c.setupFailed.Store(true)
		return &camera.SetupEndpointsResponse{
			SessionID: c.sessionID,
			Status:    camera.SetupEndpointsStatusError,
		}
	}
	// Keep the accessory SRTP material stable across write-response and later
	// GET characteristics reads. Regenerating keys here would make Apple Home
	// encrypt against a different suite than the one we actually send on.
	if c.videoSession.Local == nil {
		c.videoSession.Local = c.srtpEndpoint(c.videoSession.Remote)
		c.videoSession.Local.Port = uint16(c.videoSRTP.Port())
	}
	if c.audioSession.Local == nil {
		c.audioSession.Local = c.srtpEndpoint(c.audioSession.Remote)
		c.audioSession.Local.Port = uint16(c.audioSRTP.Port())
	}
	c.setupFailed.Store(false)
	c.stage.Store(2)
	return &camera.SetupEndpointsResponse{
		SessionID: c.sessionID,
		// SetupEndpoints status is not StreamingStatus. Apple Home aborts live
		// view when this field is anything other than Success, while snapshot
		// requests continue to work over HTTP /resource.
		Status: camera.SetupEndpointsStatusSuccess,
		Address: camera.Address{
			IPVersion:    0,
			IPAddr:       c.videoSession.Local.Addr,
			VideoRTPPort: c.videoSession.Local.Port,
			AudioRTPPort: c.audioSession.Local.Port,
		},
		VideoCrypto: camera.SRTPCryptoSuite{
			CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80,
			MasterKey:   string(c.videoSession.Local.MasterKey),
			MasterSalt:  string(c.videoSession.Local.MasterSalt),
		},
		AudioCrypto: camera.SRTPCryptoSuite{
			CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80,
			MasterKey:   string(c.audioSession.Local.MasterKey),
			MasterSalt:  string(c.audioSession.Local.MasterSalt),
		},
		VideoSSRC: c.videoSession.Local.SSRC,
		AudioSSRC: c.audioSession.Local.SSRC,
	}
}

func validIPv4SetupOffer(offer *camera.SetupEndpointsRequest) bool {
	if offer == nil || offer.Address.IPVersion != 0 {
		return false
	}
	ip := net.ParseIP(offer.Address.IPAddr)
	return ip != nil && ip.To4() != nil
}

func (c *Consumer) SetConfig(conf *camera.SelectedStreamConfiguration) bool {
	if c.sessionID != conf.Control.SessionID || !validSelectedStreamConfiguration(conf) {
		return false
	}
	if c.videoSession == nil || c.audioSession == nil ||
		c.videoSession.Local == nil || c.audioSession.Local == nil ||
		c.videoSRTP == nil || c.audioSRTP == nil {
		return false
	}

	c.SDP = fmt.Sprintf("%+v\n%+v", conf.VideoCodec, conf.AudioCodec)

	c.videoSession.Remote.SSRC = conf.VideoCodec.RTPParams[0].SSRC
	c.videoSession.PayloadType = conf.VideoCodec.RTPParams[0].PayloadType
	c.videoSession.RTCPInterval = rtcpInterval(conf.VideoCodec.RTPParams[0].RTCPInterval)

	c.audioSession.Remote.SSRC = conf.AudioCodec.RTPParams[0].SSRC
	c.audioSession.PayloadType = conf.AudioCodec.RTPParams[0].PayloadType
	c.audioSession.RTCPInterval = rtcpInterval(conf.AudioCodec.RTPParams[0].RTCPInterval)
	c.audioRTPTime = conf.AudioCodec.CodecParams[0].RTPTime[0]
	c.audioSampleRate = homeKitAudioSampleRate(conf.AudioCodec.CodecParams[0].SampleRate[0])
	videoAttrs := conf.VideoCodec.VideoAttrs[0]
	c.videoSelection = VideoSelection{
		Width:           videoAttrs.Width,
		Height:          videoAttrs.Height,
		Framerate:       videoAttrs.Framerate,
		MaxBitrate:      conf.VideoCodec.RTPParams[0].MaxBitrate,
		ProfileID:       conf.VideoCodec.CodecParams[0].ProfileID[0],
		Level:           conf.VideoCodec.CodecParams[0].Level[0],
		AudioSampleRate: c.audioSampleRate,
		AudioPacketTime: c.audioRTPTime,
		AudioMaxBitrate: conf.AudioCodec.RTPParams[0].MaxBitrate,
	}
	if mtu := conf.VideoCodec.RTPParams[0].MaxMTU; len(mtu) > 0 && mtu[0] > 0 {
		c.videoMTU = mtu[0]
		// Scrypted deliberately avoids HomeKit's larger advertised MTU and
		// treats 1200 as the reliable ceiling across Wi-Fi, VPN and relayed
		// HomeHub paths. Smaller packets are harmless on a local LAN and avoid
		// silent SRTP fragmentation on paths with additional encapsulation.
		if c.videoMTU > 1200 {
			c.videoMTU = 1200
		}
	}

	if err := c.videoSRTP.AddSession(c.videoSession); err != nil {
		return false
	}
	if err := c.audioSRTP.AddSession(c.audioSession); err != nil {
		c.videoSRTP.DelSession(c.videoSession)
		return false
	}
	c.statusMTU.Store(uint32(c.videoMTU))
	c.stage.Store(3)

	return true
}

func (c *Consumer) VideoSelection() VideoSelection {
	return c.videoSelection
}

func (c *Consumer) SRTPBindMode() string {
	return c.srtpBindMode
}

func (c *Consumer) WaitForVideoRTCP(timeout time.Duration) bool {
	if c.videoSession == nil {
		return false
	}
	return c.videoSession.WaitForRTCP(timeout, c.done)
}

func (c *Consumer) Done() <-chan struct{} {
	return c.done
}

func (c *Consumer) Stopped() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *Consumer) AddTrack(media *core.Media, codec *core.Codec, track *core.Receiver) error {
	var session *srtp.Session
	if codec.Kind() == core.KindVideo {
		session = c.videoSession
	} else {
		session = c.audioSession
	}

	sender := core.NewSender(media, track.Codec)

	if c.deadline == nil {
		c.deadline = time.NewTimer(time.Second * 30)

		sender.Handler = func(packet *rtp.Packet) {
			c.deadline.Reset(core.ConnDeadline)
			_, _ = session.WriteRTP(packet)
		}
	} else {
		sender.Handler = func(packet *rtp.Packet) {
			_, _ = session.WriteRTP(packet)
		}
	}

	switch codec.Name {
	case core.CodecH264:
		writeRTP := sender.Handler
		pay := h264.RTPPay(homeKitPlainRTPMTU(c.videoMTU), func(packet *rtp.Packet) {
			normalizeHomeKitH264Packet(packet)
			c.observeH264Packet(packet)
			writeRTP(packet)
		})
		// Drop until IDR so mid-GOP joins (always-on shared stream) do not
		// feed Apple undecodable P-frames. The first IDR from a newly started
		// FFmpeg pipeline is a warm-up frame: its decoder may still be
		// recovering the upstream reference chain, so wait for the next IDR
		// before exposing video to Apple.
		waitKey := func(packet *rtp.Packet) {
			if !c.acceptHomeKitVideoKeyframe(packet) {
				return
			}
			pay(packet)
		}
		if track.Codec.IsRTP() {
			sender.Handler = h264.RTPDepay(track.Codec, waitKey)
		} else {
			sender.Handler = h264.RepairAVCC(track.Codec, waitKey)
		}
	case core.CodecOpus:
		repack := opus.RepackToHAP(c.audioSampleRate, c.audioRTPTime, sender.Handler)
		sender.Handler = func(packet *rtp.Packet) {
			if !c.videoStarted.Load() {
				return
			}
			repack(packet)
		}
	}

	sender.HandleRTP(track)
	c.Senders = append(c.Senders, sender)
	return nil
}

func normalizeHomeKitH264Packet(packet *rtp.Packet) {
	if packet == nil || len(packet.Payload) == 0 || packet.Payload[0]&0x1f != 24 {
		return
	}
	// RFC 6184: STAP-A NRI must be the maximum nal_ref_idc of aggregated NALUs.
	// The generic payloader already uses 0x78; recompute from payload so any
	// zero-NRI input is corrected before SRTP. Keep F=0 and type=24.
	maxNRI := byte(0)
	for payload := packet.Payload[1:]; len(payload) >= 2; {
		length := int(payload[0])<<8 | int(payload[1])
		payload = payload[2:]
		if length <= 0 || length > len(payload) {
			break
		}
		if nri := payload[0] & 0x60; nri > maxNRI {
			maxNRI = nri
		}
		payload = payload[length:]
	}
	packet.Payload[0] = maxNRI | 24
}

func (c *Consumer) observeH264Packet(packet *rtp.Packet) {
	if packet == nil || len(packet.Payload) == 0 {
		return
	}
	const (
		rtpHeaderSize   = 12
		srtpAuthTagSize = 10
	)
	size := uint32(rtpHeaderSize + len(packet.Payload) + srtpAuthTagSize)
	for current := c.videoMaxDatagram.Load(); size > current; current = c.videoMaxDatagram.Load() {
		if c.videoMaxDatagram.CompareAndSwap(current, size) {
			break
		}
	}

	switch naluType := packet.Payload[0] & 0x1f; naluType {
	case 24: // STAP-A
		c.videoSTAPAUnits.Add(1)
		if packet.Payload[0]&0xe0 == 0 {
			c.videoSTAPAZeroNRI.Add(1)
		}
		for payload := packet.Payload[1:]; len(payload) >= 2; {
			length := int(payload[0])<<8 | int(payload[1])
			payload = payload[2:]
			if length == 0 || length > len(payload) {
				return
			}
			c.observeH264NALUType(payload[0] & 0x1f)
			payload = payload[length:]
		}
	case 28: // FU-A: count the NAL only once, on its start fragment.
		if len(packet.Payload) >= 2 && packet.Payload[1]&0x80 != 0 {
			c.observeH264NALUType(packet.Payload[1] & 0x1f)
		}
	default:
		c.observeH264NALUType(naluType)
	}
}

func (c *Consumer) observeH264NALUType(naluType byte) {
	switch naluType {
	case 5:
		c.videoIDRUnits.Add(1)
	case 7:
		c.videoSPSUnits.Add(1)
	case 8:
		c.videoPPSUnits.Add(1)
	}
}

// HomeKit's selected MTU is the maximum encrypted UDP datagram size. Pion's
// H264 payloader accounts for the 12-byte RTP header, while SRTP appends a
// 10-byte HMAC-SHA1-80 authentication tag after packetization.
func homeKitPlainRTPMTU(selected uint16) uint16 {
	const srtpAuthTagSize = 10
	if selected > srtpAuthTagSize {
		return selected - srtpAuthTagSize
	}
	return selected
}

func validSelectedStreamConfiguration(conf *camera.SelectedStreamConfiguration) bool {
	// Apple controllers are allowed to select a compatible profile/level and
	// can encode optional fields differently between OS releases. The
	// accessory advertisement remains the source of truth; validate only the
	// mandatory shape needed below so a compatible selection is not rejected
	// while every indexed access remains safe.
	if conf == nil ||
		conf.VideoCodec.CodecType != camera.VideoCodecTypeH264 ||
		len(conf.VideoCodec.CodecParams) != 1 ||
		len(conf.VideoCodec.CodecParams[0].ProfileID) == 0 ||
		len(conf.VideoCodec.CodecParams[0].Level) == 0 ||
		len(conf.VideoCodec.VideoAttrs) != 1 ||
		conf.VideoCodec.VideoAttrs[0].Width == 0 ||
		conf.VideoCodec.VideoAttrs[0].Height == 0 ||
		conf.VideoCodec.VideoAttrs[0].Framerate == 0 ||
		len(conf.VideoCodec.RTPParams) != 1 ||
		conf.AudioCodec.CodecType != camera.AudioCodecTypeOpus ||
		len(conf.AudioCodec.CodecParams) != 1 ||
		len(conf.AudioCodec.CodecParams[0].SampleRate) == 0 ||
		len(conf.AudioCodec.CodecParams[0].RTPTime) == 0 ||
		len(conf.AudioCodec.RTPParams) != 1 {
		return false
	}
	return true
}

func (c *Consumer) acceptHomeKitVideoKeyframe(packet *rtp.Packet) bool {
	if c.videoStarted.Load() {
		return true
	}
	if packet == nil || len(packet.Payload) < 5 || !h264.IsKeyframe(packet.Payload) {
		return false
	}
	if !c.videoPrimed.Swap(true) {
		return false
	}
	c.videoStarted.Store(true)
	return true
}

func homeKitAudioSampleRate(value byte) int {
	switch value {
	case camera.AudioCodecSampleRate8Khz:
		return 8000
	case camera.AudioCodecSampleRate24Khz:
		return 24000
	default:
		return 16000
	}
}

func (c *Consumer) WriteTo(io.Writer) (int64, error) {
	const controllerIdleTimeout = 30 * time.Second
	started := time.Now()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var mediaDeadline <-chan time.Time
	if c.deadline != nil {
		mediaDeadline = c.deadline.C
	}
	for {
		select {
		case <-mediaDeadline:
			return 0, nil
		case <-c.done:
			return 0, nil
		case now := <-ticker.C:
			lastRTCP := time.Time{}
			if c.videoSession != nil {
				lastRTCP = c.videoSession.LastRTCPAt()
			}
			if controllerRTCPExpired(now, started, lastRTCP, controllerIdleTimeout) {
				return 0, nil
			}
		}
	}
}

func controllerRTCPExpired(now, started, lastRTCP time.Time, timeout time.Duration) bool {
	if lastRTCP.IsZero() {
		return now.Sub(started) >= timeout
	}
	return now.Sub(lastRTCP) >= timeout
}

func (c *Consumer) Stop() error {
	c.stopOnce.Do(func() {
		close(c.done)
		if c.deadline != nil {
			c.deadline.Reset(0)
		}
		if c.videoSRTP != nil {
			c.videoSRTP.DelSession(c.videoSession)
		}
		if c.audioSRTP != nil {
			c.audioSRTP.DelSession(c.audioSession)
		}
		c.stopErr = c.Connection.Stop()
	})
	return c.stopErr
}

func (c *Consumer) srtpEndpoint(controller *srtp.Endpoint) *srtp.Endpoint {
	// Scrypted and Homebridge Camera FFmpeg both echo the controller-provided
	// SRTP key/salt in SetupEndpoints and use that material for the outbound
	// media session. Although HAP permits the accessory to return independent
	// material, mirroring the controller offer is the most widely exercised
	// implementation path in current Apple Home clients.
	var masterKey, masterSalt []byte
	if controller != nil {
		masterKey = append([]byte(nil), controller.MasterKey...)
		masterSalt = append([]byte(nil), controller.MasterSalt...)
	}
	return &srtp.Endpoint{
		Addr:       c.localSRTPAddress,
		MasterKey:  masterKey,
		MasterSalt: masterSalt,
		SSRC:       rand.Uint32(),
	}
}

// advertiseSRTPAddress returns a unicast address Apple Home can dial for RTP.
// HAP may accept on 0.0.0.0/::, but advertising that wildcard back in
// SetupEndpoints leaves the controller without a routable media destination.
// Snapshot still works because /resource is HTTP on the already-open HAP TCP
// connection; live view needs a concrete UDP/SRTP endpoint.
func advertiseSRTPAddress(conn net.Conn) string {
	if conn != nil {
		if local, ok := conn.LocalAddr().(*net.TCPAddr); ok {
			if ip := usableSRTPIP(local.IP); ip != "" {
				return ip
			}
		}
		if remote, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
			if ip := routeLocalIP(remote.IP); ip != "" {
				return ip
			}
		}
	}
	return firstPrivateIPv4()
}

func isLocalInterfaceAddress(address string) bool {
	addrs, err := net.InterfaceAddrs()
	return err == nil && interfaceAddressesContain(address, addrs)
}

func shouldUseWildcardSRTPBind(controllerAddress, advertisedAddress string) bool {
	controller := net.ParseIP(stripIPZone(controllerAddress))
	advertised := net.ParseIP(stripIPZone(advertisedAddress))
	if controller == nil || controller.To4() == nil || advertised == nil || advertised.To4() == nil || controller.Equal(advertised) {
		return false
	}
	return isLocalInterfaceAddress(controllerAddress)
}

func stripIPZone(address string) string {
	if zone := strings.LastIndexByte(address, '%'); zone >= 0 {
		return address[:zone]
	}
	return address
}

func interfaceAddressesContain(address string, addrs []net.Addr) bool {
	target := net.ParseIP(stripIPZone(address))
	if target == nil || target.To4() == nil {
		return false
	}
	for _, addr := range addrs {
		var candidate net.IP
		switch value := addr.(type) {
		case *net.IPAddr:
			candidate = value.IP
		case *net.IPNet:
			candidate = value.IP
		default:
			text := addr.String()
			if zone := strings.LastIndexByte(text, '%'); zone >= 0 {
				text = text[:zone]
			}
			if ip, _, err := net.ParseCIDR(text); err == nil {
				candidate = ip
			} else {
				candidate = net.ParseIP(text)
			}
		}
		if candidate != nil && target.Equal(candidate) {
			return true
		}
	}
	return false
}

func usableSRTPIP(ip net.IP) string {
	if ip == nil {
		return ""
	}
	v4 := ip.To4()
	if v4 == nil || v4.IsUnspecified() || v4.IsLoopback() || v4.IsMulticast() {
		return ""
	}
	return v4.String()
}

func routeLocalIP(remote net.IP) string {
	if remote == nil || remote.To4() == nil {
		return ""
	}
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: remote.To4(), Port: 9})
	if err != nil {
		return ""
	}
	defer conn.Close()
	if local, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return usableSRTPIP(local.IP)
	}
	return ""
}

func firstPrivateIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch value := addr.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			if v4 := ip.To4(); v4 != nil && v4.IsPrivate() {
				return v4.String()
			}
		}
	}
	return "127.0.0.1"
}

func toDuration(seconds float32) time.Duration {
	return time.Duration(seconds * float32(time.Second))
}

func rtcpInterval(seconds float32) time.Duration {
	interval := toDuration(seconds)
	if interval <= 0 {
		return 500 * time.Millisecond
	}
	return interval
}
