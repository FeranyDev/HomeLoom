package homekit

import (
	"net"
	"testing"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/h264"
	"github.com/AlexxIT/go2rtc/pkg/hap/camera"
	"github.com/AlexxIT/go2rtc/pkg/srtp"
	"github.com/pion/rtp"
)

func normalizedSelectedStreamConfiguration() *camera.SelectedStreamConfiguration {
	return &camera.SelectedStreamConfiguration{
		VideoCodec: camera.VideoCodecConfiguration{
			CodecType: camera.VideoCodecTypeH264,
			CodecParams: []camera.VideoCodecParameters{{
				ProfileID: []byte{camera.VideoCodecProfileConstrainedBaseline},
				Level:     []byte{camera.VideoCodecLevel40},
			}},
			VideoAttrs: []camera.VideoCodecAttributes{{Width: 1280, Height: 720, Framerate: 30}},
			RTPParams:  []camera.RTPParams{{PayloadType: 99, MaxMTU: []uint16{1228}}},
		},
		AudioCodec: camera.AudioCodecConfiguration{
			CodecType: camera.AudioCodecTypeOpus,
			CodecParams: []camera.AudioCodecParameters{{
				Channels: 1, SampleRate: []byte{camera.AudioCodecSampleRate16Khz}, RTPTime: []uint8{20},
			}},
			RTPParams: []camera.RTPParams{{PayloadType: 110}},
		},
	}
}

func TestSelectedStreamConfigurationAcceptsCompatibleControllerSelection(t *testing.T) {
	config := normalizedSelectedStreamConfiguration()
	if !validSelectedStreamConfiguration(config) {
		t.Fatal("normalized HomeKit stream configuration was rejected")
	}
	config.VideoCodec.CodecParams[0].ProfileID[0] = camera.VideoCodecProfileMain
	config.VideoCodec.CodecParams[0].Level[0] = camera.VideoCodecLevel32
	config.VideoCodec.VideoAttrs = []camera.VideoCodecAttributes{{Width: 640, Height: 360, Framerate: 24}}
	if !validSelectedStreamConfiguration(config) {
		t.Fatal("compatible Apple controller selection was rejected")
	}
}

func TestSelectedStreamConfigurationRejectsIncompleteRTPParameters(t *testing.T) {
	config := normalizedSelectedStreamConfiguration()
	config.AudioCodec.RTPParams = nil
	if validSelectedStreamConfiguration(config) {
		t.Fatal("incomplete HomeKit RTP parameters were accepted")
	}
}

func TestSelectedStreamConfigurationRejectsMissingCodecParameters(t *testing.T) {
	config := normalizedSelectedStreamConfiguration()
	config.VideoCodec.CodecParams = nil
	if validSelectedStreamConfiguration(config) {
		t.Fatal("incomplete HomeKit video codec parameters were accepted")
	}
}

func TestSelectedStreamConfigurationRejectsMissingVideoAttributes(t *testing.T) {
	config := normalizedSelectedStreamConfiguration()
	config.VideoCodec.VideoAttrs = nil
	if validSelectedStreamConfiguration(config) {
		t.Fatal("missing HomeKit video attributes were accepted")
	}
}

func TestConsumerMatchesOpusUsingItsRTPClock(t *testing.T) {
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	consumer := NewConsumer(local, srtp.NewServer("127.0.0.1:0"))
	medias := consumer.GetMedias()
	if len(medias) != 2 || len(medias[1].Codecs) != 1 {
		t.Fatalf("consumer medias = %#v", medias)
	}
	audio := medias[1].Codecs[0]
	if audio.Name != "OPUS" || audio.ClockRate != 0 || audio.Channels != 0 {
		t.Fatalf("HomeKit Opus matcher = %s/%d/%d", audio.Name, audio.ClockRate, audio.Channels)
	}
	rtpOpus := &core.Codec{Name: core.CodecOpus, ClockRate: 48000, Channels: 2}
	if !rtpOpus.Match(audio) {
		t.Fatalf("standard RTP Opus track does not match HomeKit consumer: %#v", rtpOpus)
	}
}

func TestConsumerDoesNotOwnHAPControlConnection(t *testing.T) {
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	consumer := NewConsumer(local, srtp.NewServer("127.0.0.1:0"))
	if consumer.Transport != nil {
		t.Fatalf("media consumer owns HAP transport: %#v", consumer.Transport)
	}
	if err := consumer.Stop(); err != nil {
		t.Fatal(err)
	}
	writeErr := make(chan error, 1)
	go func() {
		_, err := remote.Write([]byte{1})
		writeErr <- err
	}()
	if err := local.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if _, err := local.Read(buffer); err != nil {
		t.Fatalf("stopping media consumer closed HAP connection: %v", err)
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("write after media stop: %v", err)
	}
}

func TestGetAnswerReportsSetupEndpointsSuccessAndReachableAddress(t *testing.T) {
	if !canListenHomeKitUDP() {
		t.Skip("UDP listen not permitted in this environment")
	}
	// net.Pipe does not expose TCPAddrs, so inject a HAP-like local endpoint.
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	advertised := firstPrivateIPv4()
	server := srtp.NewServer("0.0.0.0:0")
	consumer := NewConsumer(&tcpAddrConn{Conn: local, local: &net.TCPAddr{IP: net.ParseIP(advertised), Port: 51826}}, server)
	videoKey := []byte("video-key-123456")
	videoSalt := []byte("video-salt-123")
	audioKey := []byte("audio-key-123456")
	audioSalt := []byte("audio-salt-123")
	consumer.SetOffer(&camera.SetupEndpointsRequest{
		SessionID: "0123456789abcdef",
		Address: camera.Address{
			IPVersion: 0, IPAddr: "192.0.2.10", VideoRTPPort: 5000, AudioRTPPort: 5002,
		},
		VideoCrypto: camera.SRTPCryptoSuite{CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80, MasterKey: string(videoKey), MasterSalt: string(videoSalt)},
		AudioCrypto: camera.SRTPCryptoSuite{CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80, MasterKey: string(audioKey), MasterSalt: string(audioSalt)},
	})
	answer := consumer.GetAnswer()
	if answer.Status != camera.SetupEndpointsStatusSuccess {
		t.Fatalf("setup endpoints status = %d, want success", answer.Status)
	}
	if answer.Address.IPAddr != advertised {
		t.Fatalf("setup endpoints advertised SRTP address %q", answer.Address.IPAddr)
	}
	if answer.Address.VideoRTPPort == 0 || answer.Address.AudioRTPPort == 0 ||
		answer.Address.VideoRTPPort == answer.Address.AudioRTPPort {
		t.Fatalf("setup endpoints did not allocate independent media ports: %#v", answer.Address)
	}
	if answer.VideoCrypto.CryptoSuite != camera.CryptoAES_CM_128_HMAC_SHA1_80 ||
		answer.AudioCrypto.CryptoSuite != camera.CryptoAES_CM_128_HMAC_SHA1_80 {
		t.Fatalf("setup endpoints crypto suite = video:%d audio:%d", answer.VideoCrypto.CryptoSuite, answer.AudioCrypto.CryptoSuite)
	}
	if answer.VideoCrypto.MasterKey != string(videoKey) ||
		answer.VideoCrypto.MasterSalt != string(videoSalt) ||
		answer.AudioCrypto.MasterKey != string(audioKey) ||
		answer.AudioCrypto.MasterSalt != string(audioSalt) {
		t.Fatalf("setup endpoints did not mirror controller SRTP material")
	}
	if answer.VideoSSRC == 0 || answer.AudioSSRC == 0 {
		t.Fatalf("incomplete setup endpoints answer: %#v", answer)
	}
}

func TestSelectedStreamConfigurationCapsHomeKitMTU(t *testing.T) {
	if !canListenHomeKitUDP() {
		t.Skip("UDP listen not permitted in this environment")
	}
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	server := srtp.NewServer("127.0.0.1:0")
	consumer := NewConsumer(&tcpAddrConn{
		Conn:   local,
		local:  &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 51826},
		remote: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 54321},
	}, server)
	consumer.SetOffer(&camera.SetupEndpointsRequest{
		SessionID: "0123456789abcdef",
		Address: camera.Address{
			IPVersion: 0, IPAddr: "127.0.0.1", VideoRTPPort: 5000, AudioRTPPort: 5002,
		},
		VideoCrypto: camera.SRTPCryptoSuite{CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80, MasterKey: string(make([]byte, 16)), MasterSalt: string(make([]byte, 14))},
		AudioCrypto: camera.SRTPCryptoSuite{CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80, MasterKey: string(make([]byte, 16)), MasterSalt: string(make([]byte, 14))},
	})
	_ = consumer.GetAnswer()
	config := normalizedSelectedStreamConfiguration()
	config.Control.SessionID = consumer.SessionID()
	config.VideoCodec.RTPParams[0].SSRC = 1001
	config.VideoCodec.RTPParams[0].MaxBitrate = 512
	config.AudioCodec.RTPParams[0].SSRC = 1002
	config.AudioCodec.RTPParams[0].MaxBitrate = 24
	config.VideoCodec.RTPParams[0].MaxMTU = []uint16{1378}
	if !consumer.SetConfig(config) {
		t.Skip("UDP listen unavailable in test environment")
	}
	defer consumer.Stop()
	if consumer.videoMTU != 1200 {
		t.Fatalf("HomeKit video MTU = %d, want 1200", consumer.videoMTU)
	}
	if selection := consumer.VideoSelection(); selection != (VideoSelection{
		Width: 1280, Height: 720, Framerate: 30, MaxBitrate: 512,
		ProfileID:       camera.VideoCodecProfileConstrainedBaseline,
		Level:           camera.VideoCodecLevel40,
		AudioSampleRate: 16000, AudioPacketTime: 20, AudioMaxBitrate: 24,
	}) {
		t.Fatalf("HomeKit video selection = %#v", selection)
	}
	status := consumer.Status()
	if !status.Active || status.State != "started" || status.VideoMTU != 1200 ||
		status.VideoWidth != 1280 || status.VideoHeight != 720 ||
		status.VideoFramerate != 30 || status.VideoMaxBitrate != 512 ||
		status.VideoProfileID != camera.VideoCodecProfileConstrainedBaseline ||
		status.VideoLevel != camera.VideoCodecLevel40 ||
		status.AudioSampleRate != 16000 || status.AudioPacketTime != 20 ||
		status.AudioMaxBitrate != 24 {
		t.Fatalf("HomeKit session status = %#v, want started with MTU 1200", status)
	}
}

func TestHomeKitPlainRTPMTUReservesSRTPAuthenticationTag(t *testing.T) {
	if got := homeKitPlainRTPMTU(1200); got != 1190 {
		t.Fatalf("plain RTP MTU = %d, want 1190", got)
	}
	if got := homeKitPlainRTPMTU(8); got != 8 {
		t.Fatalf("undersized plain RTP MTU = %d, want unchanged", got)
	}
}

func TestConsumerObservesHomeKitH264ParameterSetsAndIDR(t *testing.T) {
	consumer := &Consumer{}
	stapA := &rtp.Packet{Payload: []byte{
		0x78,
		0, 2, 0x67, 1,
		0, 2, 0x68, 2,
	}}
	normalizeHomeKitH264Packet(stapA)
	consumer.observeH264Packet(stapA)
	consumer.observeH264Packet(&rtp.Packet{Payload: []byte{0x7c, 0x85, 1}})
	consumer.observeH264Packet(&rtp.Packet{Payload: []byte{0x7c, 0x45, 2}})

	if got := consumer.videoSPSUnits.Load(); got != 1 {
		t.Fatalf("observed SPS units = %d, want 1", got)
	}
	if got := consumer.videoPPSUnits.Load(); got != 1 {
		t.Fatalf("observed PPS units = %d, want 1", got)
	}
	if got := consumer.videoIDRUnits.Load(); got != 1 {
		t.Fatalf("observed IDR units = %d, want one FU-A start", got)
	}
	if got := consumer.videoSTAPAUnits.Load(); got != 1 {
		t.Fatalf("observed STAP-A units = %d, want 1", got)
	}
	// RFC max-NRI STAP-A should not count as zero-NRI.
	if got := consumer.videoSTAPAZeroNRI.Load(); got != 0 {
		t.Fatalf("observed zero-NRI STAP-A units = %d, want 0", got)
	}
	if got := consumer.videoMaxDatagram.Load(); got != 31 {
		t.Fatalf("observed maximum encrypted datagram = %d, want 31", got)
	}

	consumer.observeH264Packet(&rtp.Packet{Payload: []byte{0x78, 0, 8, 0x67}})
}

func TestConsumerWaitsForSecondStartupIDR(t *testing.T) {
	consumer := &Consumer{}
	parameterSetsAndIDR := h264.JoinNALU([]byte{0x67, 1}, []byte{0x68, 2}, []byte{0x65, 3})
	packet := &rtp.Packet{Payload: parameterSetsAndIDR}

	if consumer.acceptHomeKitVideoKeyframe(packet) {
		t.Fatal("first startup IDR was forwarded")
	}
	if consumer.videoStarted.Load() {
		t.Fatal("consumer started on warm-up IDR")
	}
	if !consumer.acceptHomeKitVideoKeyframe(packet) {
		t.Fatal("second startup IDR was not forwarded")
	}
	if !consumer.videoStarted.Load() {
		t.Fatal("consumer did not start after a complete startup IDR")
	}
}

func TestNormalizeHomeKitH264PacketUsesMaxNRI(t *testing.T) {
	// Zero-NRI input with high-NRI SPS (0x67 → NRI=3) must raise STAP-A NRI.
	stapA := &rtp.Packet{Payload: []byte{0x18, 0, 2, 0x67, 1, 0, 2, 0x68, 2}}
	normalizeHomeKitH264Packet(stapA)
	if got := stapA.Payload[0]; got != 0x78 {
		t.Fatalf("HomeKit STAP-A header = 0x%02x, want 0x78 (max NRI|24)", got)
	}

	fuA := &rtp.Packet{Payload: []byte{0x7c, 0x85, 1}}
	normalizeHomeKitH264Packet(fuA)
	if got := fuA.Payload[0]; got != 0x7c {
		t.Fatalf("HomeKit FU-A header changed to 0x%02x", got)
	}
}

func canListenHomeKitUDP() bool {
	first, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return false
	}
	defer first.Close()
	second, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return false
	}
	_ = second.Close()
	return true
}

type tcpAddrConn struct {
	net.Conn
	local  net.Addr
	remote net.Addr
}

func (c *tcpAddrConn) LocalAddr() net.Addr {
	if c.local != nil {
		return c.local
	}
	return c.Conn.LocalAddr()
}

func (c *tcpAddrConn) RemoteAddr() net.Addr {
	if c.remote != nil {
		return c.remote
	}
	return c.Conn.RemoteAddr()
}

func TestAdvertiseSRTPAddressFallsBackFromWildcardListener(t *testing.T) {
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	conn := &tcpAddrConn{
		Conn:   local,
		local:  &net.TCPAddr{IP: net.IPv4zero, Port: 51826},
		remote: &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 54321},
	}
	got := advertiseSRTPAddress(conn)
	if got == "" || got == "0.0.0.0" || got == "::" {
		t.Fatalf("wildcard listener fell back to unusable address %q", got)
	}
}

func TestAdvertiseSRTPAddressRejectsWildcardListener(t *testing.T) {
	if got := usableSRTPIP(net.IPv4zero); got != "" {
		t.Fatalf("wildcard IPv4 accepted: %q", got)
	}
	if got := usableSRTPIP(net.IPv6zero); got != "" {
		t.Fatalf("wildcard IPv6 accepted: %q", got)
	}
	if got := usableSRTPIP(net.ParseIP("192.0.2.20")); got != "192.0.2.20" {
		t.Fatalf("usable unicast rejected: %q", got)
	}
	if got := usableSRTPIP(net.ParseIP("fe80::1234")); got != "fe80::1234" {
		t.Fatalf("IPv6 link-local address rejected: %q", got)
	}
}

func TestSRTPBindAddressPreservesIPv6LinkLocalZone(t *testing.T) {
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	conn := &tcpAddrConn{
		Conn:  local,
		local: &net.TCPAddr{IP: net.ParseIP("fe80::1234"), Port: 51826, Zone: "en0"},
	}
	if got := srtpBindAddress(conn, "fe80::1234"); got != "fe80::1234%en0" {
		t.Fatalf("IPv6 link-local bind address = %q", got)
	}
	if got := srtpBindAddress(conn, "192.0.2.30"); got != "192.0.2.30" {
		t.Fatalf("unrelated advertised address received interface zone: %q", got)
	}
}

func TestInterfaceAddressesContainControllerTUNAddress(t *testing.T) {
	addrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("192.168.101.197"), Mask: net.CIDRMask(24, 32)},
		&net.IPAddr{IP: net.ParseIP("198.19.0.1")},
		&net.IPAddr{IP: net.ParseIP("fe80::1234"), Zone: "utun8"},
	}
	for _, address := range []string{"198.19.0.1", "fe80::1234%utun8"} {
		if !interfaceAddressesContain(address, addrs) {
			t.Fatalf("local interface address %q was not detected", address)
		}
	}
	if interfaceAddressesContain("192.168.101.114", addrs) {
		t.Fatal("remote HomePod address was treated as a local interface")
	}
	if interfaceAddressesContain("not-an-ip", addrs) {
		t.Fatal("invalid controller address was treated as a local interface")
	}
}

func TestLocalControllerUsesWildcardSRTPBinding(t *testing.T) {
	if !canListenHomeKitUDP() {
		t.Skip("UDP listen not permitted in this environment")
	}
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	consumer := NewConsumer(
		&tcpAddrConn{
			Conn:  local,
			local: &net.TCPAddr{IP: net.ParseIP("192.0.2.30"), Port: 51826},
		},
		srtp.NewServer("127.0.0.1:8443"),
	)
	consumer.SetOffer(&camera.SetupEndpointsRequest{
		SessionID: "0123456789abcdef",
		Address: camera.Address{
			IPVersion: 0, IPAddr: "127.0.0.1", VideoRTPPort: 5000, AudioRTPPort: 5002,
		},
		VideoCrypto: camera.SRTPCryptoSuite{
			CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80,
			MasterKey:   string(make([]byte, 16)),
			MasterSalt:  string(make([]byte, 14)),
		},
		AudioCrypto: camera.SRTPCryptoSuite{
			CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80,
			MasterKey:   string(make([]byte, 16)),
			MasterSalt:  string(make([]byte, 14)),
		},
	})
	t.Cleanup(func() { _ = consumer.Stop() })

	if got := consumer.SRTPBindMode(); got != "wildcard-local-controller" {
		t.Fatalf("SRTP bind mode = %q", got)
	}
	if answer := consumer.GetAnswer(); answer.Status != camera.SetupEndpointsStatusSuccess {
		t.Fatalf("SetupEndpoints answer status = %d", answer.Status)
	}
}

func TestControllerMatchingAdvertisedAddressUsesConcreteSRTPBinding(t *testing.T) {
	if !canListenHomeKitUDP() {
		t.Skip("UDP listen not permitted in this environment")
	}
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	consumer := NewConsumer(
		&tcpAddrConn{
			Conn:  local,
			local: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 51826},
		},
		srtp.NewServer("127.0.0.1:8443"),
	)
	consumer.SetOffer(&camera.SetupEndpointsRequest{
		SessionID: "0123456789abcdef",
		Address: camera.Address{
			IPVersion: 0, IPAddr: "127.0.0.1", VideoRTPPort: 5000, AudioRTPPort: 5002,
		},
		VideoCrypto: camera.SRTPCryptoSuite{
			CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80,
			MasterKey:   string(make([]byte, 16)),
			MasterSalt:  string(make([]byte, 14)),
		},
		AudioCrypto: camera.SRTPCryptoSuite{
			CryptoSuite: camera.CryptoAES_CM_128_HMAC_SHA1_80,
			MasterKey:   string(make([]byte, 16)),
			MasterSalt:  string(make([]byte, 14)),
		},
	})
	t.Cleanup(func() { _ = consumer.Stop() })

	if got := consumer.SRTPBindMode(); got != "advertised-address" {
		t.Fatalf("SRTP bind mode = %q", got)
	}
	if answer := consumer.GetAnswer(); answer.Status != camera.SetupEndpointsStatusSuccess {
		t.Fatalf("SetupEndpoints answer status = %d", answer.Status)
	}
}

func TestControllerRTCPExpiryMatchesScryptedLivenessWindow(t *testing.T) {
	now := time.Unix(100, 0)
	started := now.Add(-31 * time.Second)
	if !controllerRTCPExpired(now, started, time.Time{}, 30*time.Second) {
		t.Fatal("session without RTCP did not expire")
	}
	if controllerRTCPExpired(now, started, now.Add(-5*time.Second), 30*time.Second) {
		t.Fatal("recent controller RTCP was treated as expired")
	}
	if !controllerRTCPExpired(now, started, now.Add(-31*time.Second), 30*time.Second) {
		t.Fatal("stale controller RTCP did not expire")
	}
}
