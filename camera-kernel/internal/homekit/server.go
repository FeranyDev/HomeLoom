package homekit

import (
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AlexxIT/go2rtc/internal/ffmpeg"
	srtp2 "github.com/AlexxIT/go2rtc/internal/srtp"
	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/hap"
	"github.com/AlexxIT/go2rtc/pkg/hap/camera"
	"github.com/AlexxIT/go2rtc/pkg/hap/hds"
	"github.com/AlexxIT/go2rtc/pkg/hap/tlv8"
	"github.com/AlexxIT/go2rtc/pkg/homekit"
	"github.com/AlexxIT/go2rtc/pkg/magic"
	"github.com/AlexxIT/go2rtc/pkg/mdns"
)

const initialControllerRTCPWait = 250 * time.Millisecond

type server struct {
	hap  *hap.Server // server for HAP connection and encryption
	mdns *mdns.ServiceEntry

	pairings []string // pairings list
	conns    []any
	mu       sync.Mutex

	accessory   *hap.Accessory // HAP accessory
	consumers   map[uint64]*homekit.Consumer
	lastSession homekit.SessionStatus
	proxyURL    string
	setupID     string
	stream      string // stream name from YAML
}

func (s *server) MarshalJSON() ([]byte, error) {
	v := struct {
		Name       string `json:"name"`
		DeviceID   string `json:"device_id"`
		Paired     int    `json:"paired,omitempty"`
		CategoryID string `json:"category_id,omitempty"`
		SetupCode  string `json:"setup_code,omitempty"`
		SetupID    string `json:"setup_id,omitempty"`
		Conns      []any  `json:"connections,omitempty"`
	}{
		Name:       s.mdns.Name,
		DeviceID:   s.mdns.Info[hap.TXTDeviceID],
		CategoryID: s.mdns.Info[hap.TXTCategory],
		Paired:     len(s.pairings),
		Conns:      s.conns,
	}
	if v.Paired == 0 {
		v.SetupCode = s.hap.Pin
		v.SetupID = s.setupID
	}
	return json.Marshal(v)
}

func (s *server) Handle(w http.ResponseWriter, r *http.Request) {
	conn, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		log.Warn().Err(err).Str("stream", s.stream).Msg("[homekit] HAP connection hijack failed")
		return
	}

	defer conn.Close()

	// Fix reading from Body after Hijack.
	r.Body = io.NopCloser(rw)

	switch r.RequestURI {
	case hap.PathPairSetup:
		log.Debug().Str("stream", s.stream).Msg("[homekit] pair setup requested")
		id, key, err := s.hap.PairSetup(r, rw)
		if err != nil {
			log.Warn().Err(err).Str("stream", s.stream).Msg("[homekit] pair setup failed")
			return
		}

		s.AddPair(id, key, hap.PermissionAdmin)
		log.Info().Str("stream", s.stream).Msg("[homekit] pair setup completed")

	case hap.PathPairVerify:
		id, key, err := s.hap.PairVerify(r, rw)
		if err != nil {
			log.Debug().Err(err).Str("stream", s.stream).Msg("[homekit] pair verify failed")
			return
		}

		log.Debug().
			Str("stream", s.stream).
			Str("client_id", id).
			Str("remote_address", conn.RemoteAddr().String()).
			Msg("[homekit] pair verify succeeded")

		controller, err := hap.NewConn(conn, rw, key, false)
		if err != nil {
			log.Warn().Err(err).Str("stream", s.stream).Msg("[homekit] encrypted HAP session initialization failed")
			return
		}

		s.AddConn(controller)
		defer s.DelConn(controller)
		defer s.removeEventSubscriptions(controller)
		defer s.stopConsumersForConnection(controller)

		var handler homekit.HandlerFunc

		switch {
		case s.accessory != nil:
			handler = homekit.ServerHandler(s)
		case s.proxyURL != "":
			client, err := hap.Dial(s.proxyURL)
			if err != nil {
				log.Error().Err(err).Caller().Send()
				return
			}
			handler = homekit.ProxyHandler(s, client.Conn)
		}

		started := time.Now()
		if err = handler(controller); err != nil && !expectedHAPConnectionClose(err) {
			log.Warn().
				Err(err).
				Str("stream", s.stream).
				Dur("connected_for", time.Since(started)).
				Msg("[homekit] encrypted HAP session ended with error")
			return
		}
		log.Debug().
			Err(err).
			Str("stream", s.stream).
			Dur("connected_for", time.Since(started)).
			Msg("[homekit] encrypted HAP session peer closed")
	}
}

func expectedHAPConnectionClose(err error) bool {
	return err == nil ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET)
}

type logger struct {
	v any
}

func (l logger) String() string {
	switch v := l.v.(type) {
	case *hap.Conn:
		return "hap " + v.RemoteAddr().String()
	case *hds.Conn:
		return "hds " + v.RemoteAddr().String()
	case *homekit.Consumer:
		return "rtp " + v.RemoteAddr
	}
	return "unknown"
}

func (s *server) AddConn(v any) {
	log.Trace().Str("stream", s.stream).Msgf("[homekit] add conn %s", logger{v})
	s.mu.Lock()
	s.conns = append(s.conns, v)
	s.mu.Unlock()
}

func (s *server) DelConn(v any) {
	log.Trace().Str("stream", s.stream).Msgf("[homekit] del conn %s", logger{v})
	s.mu.Lock()
	if i := slices.Index(s.conns, v); i >= 0 {
		s.conns = slices.Delete(s.conns, i, i+1)
	}
	s.mu.Unlock()
}

func (s *server) UpdateStatus() {
	// true status is important, or device may be offline in Apple Home
	if len(s.pairings) == 0 {
		s.mdns.Info[hap.TXTStatusFlags] = hap.StatusNotPaired
	} else {
		s.mdns.Info[hap.TXTStatusFlags] = hap.StatusPaired
	}
}

func (s *server) pairIndex(id string) int {
	id = "client_id=" + id
	for i, pairing := range s.pairings {
		if strings.HasPrefix(pairing, id) {
			return i
		}
	}
	return -1
}

func (s *server) GetPair(id string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	if i := s.pairIndex(id); i >= 0 {
		query, _ := url.ParseQuery(s.pairings[i])
		b, _ := hex.DecodeString(query.Get("client_public"))
		return b
	}
	return nil
}

func (s *server) AddPair(id string, public []byte, permissions byte) {
	log.Debug().Str("stream", s.stream).Msgf("[homekit] add pair id=%s public=%x perm=%d", id, public, permissions)

	s.mu.Lock()
	if s.pairIndex(id) < 0 {
		s.pairings = append(s.pairings, fmt.Sprintf(
			"client_id=%s&client_public=%x&permissions=%d", id, public, permissions,
		))
		s.UpdateStatus()
		s.PatchConfig()
	}
	s.mu.Unlock()
}

func (s *server) DelPair(id string) {
	log.Debug().Str("stream", s.stream).Msgf("[homekit] del pair id=%s", id)

	s.mu.Lock()
	if i := s.pairIndex(id); i >= 0 {
		s.pairings = append(s.pairings[:i], s.pairings[i+1:]...)
		s.UpdateStatus()
		s.PatchConfig()
	}
	s.mu.Unlock()
}

func (s *server) PatchConfig() {
	if err := persistDurablePairings(s.stream, s.pairings); err != nil {
		log.Error().Err(err).Msgf(
			"[homekit] can't save %s pairings=%v", s.stream, s.pairings,
		)
	}
}

func (s *server) GetAccessories(_ net.Conn) []*hap.Accessory {
	log.Debug().
		Str("stream", s.stream).
		Int("service_count", len(s.accessory.Services)).
		Str("config_number", accessoryConfigNumber).
		Msg("[homekit] accessory database requested")
	return []*hap.Accessory{s.accessory}
}

func (s *server) GetCharacteristic(conn net.Conn, aid uint8, iid uint64) any {
	char := s.accessory.GetCharacterByID(iid)
	if char == nil {
		log.Warn().
			Str("stream", s.stream).
			Uint8("aid", aid).
			Uint64("iid", iid).
			Msg("[homekit] unknown characteristic read")
		return nil
	}
	log.Debug().
		Str("stream", s.stream).
		Uint8("aid", aid).
		Uint64("iid", iid).
		Str("characteristic", diagnosticCharacteristicName(char.Type)).
		Str("characteristic_type", char.Type).
		Msg("[homekit] characteristic read")

	switch char.Type {
	case camera.TypeSetupEndpoints:
		// SetupEndpoints is a per-RTP-service exchange. Return the value stored
		// by the immediately preceding write on this exact characteristic.
		// Recomputing it from a global consumer can leak an older session's
		// ports/key material into a different stream slot (including after a
		// Busy response), after which Apple's START necessarily fails its
		// Session ID check.
		var answer camera.SetupEndpointsResponse
		if err := tlv8.UnmarshalBase64(char.Value, &answer); err != nil {
			log.Debug().
				Str("stream", s.stream).
				Err(err).
				Msg("[homekit] SetupEndpoints read has no decodable stored answer")
			return char.Value
		}
		slot, _ := s.streamSlotForCharacteristic(char)
		log.Info().
			Str("stream", s.stream).
			Uint64("stream_slot", slot).
			Uint8("status", answer.Status).
			Str("accessory_address", answer.Address.IPAddr).
			Uint16("video_port", answer.Address.VideoRTPPort).
			Uint16("audio_port", answer.Address.AudioRTPPort).
			Msg("[homekit] SetupEndpoints answer read")
		return char.Value
	}

	return char.Value
}

func (s *server) SetCharacteristic(conn net.Conn, aid uint8, iid uint64, value any, writeResponse bool) any {
	char := s.accessory.GetCharacterByID(iid)
	if char == nil {
		log.Warn().
			Str("stream", s.stream).
			Uint8("aid", aid).
			Uint64("iid", iid).
			Bool("write_response", writeResponse).
			Int("value_length", diagnosticValueLength(value)).
			Msg("[homekit] unknown characteristic write")
		return nil
	}
	log.Debug().
		Str("stream", s.stream).
		Uint8("aid", aid).
		Uint64("iid", iid).
		Str("characteristic", diagnosticCharacteristicName(char.Type)).
		Str("characteristic_type", char.Type).
		Bool("write_response", writeResponse).
		Int("value_length", diagnosticValueLength(value)).
		Msg("[homekit] characteristic write")

	switch char.Type {
	case "B0", "11A":
		if err := char.Write(value); err != nil {
			log.Warn().
				Err(err).
				Str("stream", s.stream).
				Str("characteristic", diagnosticCharacteristicName(char.Type)).
				Msg("[homekit] scalar characteristic write rejected")
			return nil
		}
		if err := char.NotifyListeners(conn); err != nil {
			log.Debug().
				Err(err).
				Str("stream", s.stream).
				Str("characteristic", diagnosticCharacteristicName(char.Type)).
				Msg("[homekit] scalar characteristic event delivery failed")
		}
		if writeResponse {
			return char.Value
		}
		return nil

	case camera.TypeSetupEndpoints:
		var offer camera.SetupEndpointsRequest
		if err := tlv8.UnmarshalBase64(value, &offer); err != nil {
			log.Warn().Err(err).Str("stream", s.stream).Msg("[homekit] rejected malformed SetupEndpoints")
			s.rememberSessionStatus(homekit.SessionStatus{State: "setup-invalid"})
			return nil
		}
		slot, ok := s.streamSlotForCharacteristic(char)
		if !ok {
			log.Warn().Str("stream", s.stream).Uint64("iid", iid).
				Msg("[homekit] SetupEndpoints is not owned by an RTP stream service")
			return nil
		}

		if current := s.consumerForSlot(slot); current != nil && !current.Stopped() {
			log.Info().
				Str("stream", s.stream).
				Uint64("stream_slot", slot).
				Msg("[homekit] SetupEndpoints rejected because its stream slot is busy")
			busy, err := tlv8.MarshalBase64(camera.SetupEndpointsResponse{
				SessionID: offer.SessionID,
				Status:    camera.SetupEndpointsStatusBusy,
			})
			if err != nil {
				return nil
			}
			char.Value = busy
			if writeResponse {
				return busy
			}
			return nil
		}

		consumer := homekit.NewConsumer(conn, srtp2.Server)
		consumer.SetOffer(&offer)
		s.setConsumer(slot, consumer)
		s.setStreamingStatusOn(char, camera.StreamingStatusInUse)
		log.Info().Str("stream", s.stream).Uint64("stream_slot", slot).Msgf(
			"[homekit] RTP endpoints offered controller=%s video_port=%d audio_port=%d ip_version=%d media_bind=%s",
			offer.Address.IPAddr, offer.Address.VideoRTPPort, offer.Address.AudioRTPPort, offer.Address.IPVersion,
			consumer.SRTPBindMode(),
		)

		// Persist the answer for the standard follow-up GET. A controller that
		// explicitly requests a write response can still receive it inline.
		answer := consumer.GetAnswer()
		encoded, err := tlv8.MarshalBase64(answer)
		if err != nil {
			return nil
		}
		char.Value = encoded
		if answer.Status != camera.SetupEndpointsStatusSuccess {
			s.rememberSessionStatus(consumer.Status())
			_ = consumer.Stop()
			s.clearConsumer(consumer)
		}
		if writeResponse {
			return encoded
		}
		return nil

	case camera.TypeSelectedStreamConfiguration:
		var conf camera.SelectedStreamConfiguration
		if err := tlv8.UnmarshalBase64(value, &conf); err != nil {
			log.Warn().Err(err).Str("stream", s.stream).Msg("[homekit] rejected malformed selected stream configuration")
			s.rememberSessionStatus(homekit.SessionStatus{State: "selected-invalid"})
			return nil
		}
		slot, ok := s.streamSlotForCharacteristic(char)
		if !ok {
			log.Warn().Str("stream", s.stream).Uint64("iid", iid).
				Msg("[homekit] selected stream configuration is not owned by an RTP stream service")
			return nil
		}

		log.Debug().
			Str("stream", s.stream).
			Uint64("stream_slot", slot).
			Uint8("command", conf.Control.Command).
			Str("command_name", diagnosticSessionCommandName(conf.Control.Command)).
			Msg("[homekit] selected stream command received")

		switch conf.Control.Command {
		case camera.SessionCommandEnd:
			if consumer := s.consumerForSlot(slot); consumer != nil &&
				consumer.SessionID() == conf.Control.SessionID {
				status := consumer.Status()
				_ = consumer.Stop()
				s.rememberSessionStatus(status)
				s.clearConsumer(consumer)
				return nil
			}
			if consumer := s.consumerBySession(conf.Control.SessionID); consumer != nil {
				status := consumer.Status()
				_ = consumer.Stop()
				s.rememberSessionStatus(status)
				s.clearConsumer(consumer)
				return nil
			}

		case camera.SessionCommandStart:
			consumer := s.consumerForSlot(slot)
			if consumer == nil {
				log.Warn().
					Str("stream", s.stream).
					Uint64("stream_slot", slot).
					Msg("[homekit] START has no prepared session in its stream slot")
				return nil
			}
			if consumer.SessionID() != conf.Control.SessionID {
				log.Warn().
					Str("stream", s.stream).
					Uint64("stream_slot", slot).
					Msg("[homekit] START session does not match its stream slot")
				return nil
			}

			if !consumer.SetConfig(&conf) {
				log.Warn().Str("stream", s.stream).Uint64("stream_slot", slot).Msgf(
					"[homekit] rejected selected stream configuration video_type=%d video_params=%v video_attrs=%v video_rtp=%d audio_type=%d audio_rtp=%d",
					conf.VideoCodec.CodecType, conf.VideoCodec.CodecParams, conf.VideoCodec.VideoAttrs,
					len(conf.VideoCodec.RTPParams), conf.AudioCodec.CodecType, len(conf.AudioCodec.RTPParams),
				)
				_ = consumer.Stop()
				s.clearConsumer(consumer)
				return nil
			}

			selected := log.Info().
				Str("stream", s.stream).
				Uint64("stream_slot", slot).
				Uint8("video_payload_type", conf.VideoCodec.RTPParams[0].PayloadType).
				Uint16("video_bitrate_kbps", conf.VideoCodec.RTPParams[0].MaxBitrate).
				Str("video_profile", homeKitH264Profile(conf.VideoCodec.CodecParams[0].ProfileID[0])).
				Str("video_level", homeKitH264Level(conf.VideoCodec.CodecParams[0].Level[0])).
				Int("audio_sample_rate_hz", consumer.VideoSelection().AudioSampleRate).
				Uint8("audio_packet_time_ms", conf.AudioCodec.CodecParams[0].RTPTime[0]).
				Uint16("audio_bitrate_kbps", conf.AudioCodec.RTPParams[0].MaxBitrate)
			if len(conf.VideoCodec.VideoAttrs) > 0 {
				selected = selected.
					Uint16("width", conf.VideoCodec.VideoAttrs[0].Width).
					Uint16("height", conf.VideoCodec.VideoAttrs[0].Height).
					Uint8("fps", conf.VideoCodec.VideoAttrs[0].Framerate)
			}
			selected.Msg("[homekit] selected live stream configuration")

			s.AddConn(consumer)
			// Match Scrypted's callback ordering: acknowledge the HAP START write
			// immediately, then wait for UDP return-path readiness and prepare the
			// camera/FFmpeg pipeline asynchronously. Blocking this characteristic
			// write on media startup makes Apple abandon an otherwise valid live
			// session and fall back to periodically refreshed snapshots.
			go s.runConsumer(slot, consumer)
		}
	}
	return nil
}

func (s *server) SetCharacteristicEvent(conn net.Conn, aid uint8, iid uint64, enabled bool) {
	char := s.accessory.GetCharacterByID(iid)
	if char == nil {
		log.Warn().
			Str("stream", s.stream).
			Uint8("aid", aid).
			Uint64("iid", iid).
			Bool("enabled", enabled).
			Msg("[homekit] unknown characteristic event subscription")
		return
	}
	if !slices.Contains(char.Perms, "ev") {
		log.Warn().
			Str("stream", s.stream).
			Uint8("aid", aid).
			Uint64("iid", iid).
			Str("characteristic", diagnosticCharacteristicName(char.Type)).
			Bool("enabled", enabled).
			Msg("[homekit] unsupported characteristic event subscription")
		return
	}
	if enabled {
		char.AddListener(conn)
	} else {
		char.RemoveListener(conn)
	}
	log.Debug().
		Str("stream", s.stream).
		Uint8("aid", aid).
		Uint64("iid", iid).
		Str("characteristic", diagnosticCharacteristicName(char.Type)).
		Bool("enabled", enabled).
		Msg("[homekit] characteristic event subscription")
}

func (s *server) removeEventSubscriptions(conn net.Conn) {
	for _, service := range s.accessory.Services {
		for _, char := range service.Characters {
			char.RemoveListener(conn)
		}
	}
}

func (s *server) runConsumer(slot uint64, consumer *homekit.Consumer) {
	if streams.Get(s.stream) == nil {
		log.Warn().Str("stream", s.stream).Uint64("stream_slot", slot).
			Msg("[homekit] start live stream missing source")
		_ = consumer.Stop()
		s.rememberSessionStatus(consumer.Status())
		s.DelConn(consumer)
		s.clearConsumer(consumer)
		return
	}

	selection := consumer.VideoSelection()
	log.Info().
		Str("stream", s.stream).
		Uint64("stream_slot", slot).
		Uint16("video_width", selection.Width).
		Uint16("video_height", selection.Height).
		Uint8("video_framerate", selection.Framerate).
		Uint16("video_max_bitrate_kbps", selection.MaxBitrate).
		Str("video_profile", homeKitH264Profile(selection.ProfileID)).
		Str("video_level", homeKitH264Level(selection.Level)).
		Int("audio_sample_rate_hz", selection.AudioSampleRate).
		Uint8("audio_packet_time_ms", selection.AudioPacketTime).
		Uint16("audio_max_bitrate_kbps", selection.AudioMaxBitrate).
		Msg("[homekit] applying controller media selection")
	// Session-local software transcode matching the controller selection
	// (width/height/bitrate). The always-on shared 720p stream is for preload
	// only: feeding 720p@CRF into a 360p@132K HomeKit session causes FIR storms
	// and unplayable video. Do not use VideoToolbox HEVC hard-decode here.
	stream := streams.NewStream(homeKitSessionSources(s.stream, selection))
	attached := false
	defer func() {
		if attached {
			stream.RemoveConsumer(consumer)
		} else {
			_ = consumer.Stop()
		}
		status := consumer.Status()
		s.rememberSessionStatus(status)
		s.DelConn(consumer)
		s.clearConsumer(consumer)
		log.Info().
			Str("stream", s.stream).
			Uint64("stream_slot", slot).
			Str("state", status.State).
			Uint64("video_packets", status.VideoPackets).
			Uint64("video_bytes", status.VideoBytes).
			Uint64("video_write_errors", status.VideoWriteErrors).
			Uint32("video_ssrc", status.VideoSSRC).
			Uint64("video_rtcp_datagrams", status.VideoRTCPDatagrams).
			Uint64("video_rtcp_packets", status.VideoRTCPPackets).
			Uint64("video_rtcp_failures", status.VideoRTCPFailures).
			Uint64("video_rtcp_parse_errors", status.VideoRTCPParseErrors).
			Uint64("video_rtcp_receiver_reports", status.VideoRTCPReceiverReports).
			Uint64("video_rtcp_report_blocks", status.VideoRTCPReportBlocks).
			Uint64("video_rtcp_matched_reports", status.VideoRTCPMatchedReports).
			Uint32("video_rtcp_reported_ssrc", status.VideoRTCPReportedSSRC).
			Uint32("video_rtcp_fraction_lost", status.VideoRTCPFractionLost).
			Uint32("video_rtcp_total_lost", status.VideoRTCPTotalLost).
			Uint32("video_rtcp_last_sequence", status.VideoRTCPLastSequence).
			Uint32("video_rtcp_jitter", status.VideoRTCPJitter).
			Uint32("video_rtcp_last_sender_report", status.VideoRTCPLastSenderReport).
			Uint64("video_rtcp_pli", status.VideoRTCPPLI).
			Uint64("video_rtcp_fir", status.VideoRTCPFIR).
			Uint64("video_rtcp_nack", status.VideoRTCPNACK).
			Uint64("video_sps_units", status.VideoSPSUnits).
			Uint64("video_pps_units", status.VideoPPSUnits).
			Uint64("video_idr_units", status.VideoIDRUnits).
			Uint64("video_stap_a_units", status.VideoSTAPAUnits).
			Uint64("video_stap_a_zero_nri", status.VideoSTAPAZeroNRI).
			Uint32("video_max_datagram_bytes", status.VideoMaxDatagram).
			Msg("[homekit] live stream consumer ended")
	}()

	rtcpWaitStarted := time.Now()
	rtcpReady := consumer.WaitForVideoRTCP(initialControllerRTCPWait)
	if consumer.Stopped() {
		return
	}
	log.Info().
		Str("stream", s.stream).
		Uint64("stream_slot", slot).
		Bool("initial_rtcp", rtcpReady).
		Int64("initial_rtcp_wait_ms", time.Since(rtcpWaitStarted).Milliseconds()).
		Msg("[homekit] controller UDP return path checked")

	if err := stream.AddConsumer(consumer); err != nil {
		log.Warn().Err(err).Str("stream", s.stream).Uint64("stream_slot", slot).
			Msg("[homekit] start live stream consumer failed")
		return
	}
	attached = true
	startStatus := consumer.Status()
	log.Info().Str("stream", s.stream).Uint64("stream_slot", slot).
		Bool("rtcp_before_media", startStatus.VideoRTCPDatagrams > 0).
		Msg("[homekit] live stream consumer started")

	_, _ = consumer.WriteTo(nil)
}

func homeKitSessionSource(streamID string, selection homekit.VideoSelection) string {
	sources := homeKitSessionSources(streamID, selection)
	return sources[len(sources)-1]
}

// maxHomeKitVideoFramerate caps the delivered live stream frame rate. Apple
// controllers request 30fps regardless of the advertised video attributes, so
// the session-local ffmpeg transcode clamps the requested rate instead.
const maxHomeKitVideoFramerate = 20

// homeKitMinimumVideoBitrateKbps is the lowest video bitrate accepted for the
// selected resolution. Apple Home requests far lower rates than a stable
// libx264 encode needs (299Kbps for 720p, 132Kbps for 360p, 68Kbps for 240p):
// the constant VBV underflow yields frames the controller decoder rejects with
// FIR storms and a ~30s session teardown. The controller treats its requested
// bitrate as a ceiling, not an exact target, so exceeding it on a LAN is safe.
// Values are the recommended H.264 tiers for each resolution at ~20fps.
func homeKitMinimumVideoBitrateKbps(width, height int) int {
	switch {
	case width >= 1920 && height >= 1080:
		return 2000
	case width >= 1280 && height >= 720:
		return 1000
	case width >= 960 && height >= 540:
		return 800
	case width >= 640 && height >= 360:
		return 500
	case width >= 320 && height >= 240:
		return 300
	default:
		return 0
	}
}

// homeKitMinimumAudioBitrateKbps is the lowest Opus audio bitrate accepted for
// the selected resolution. Apple Home requests 24Kbps for every resolution,
// below Opus's recommended range for SWB speech, which makes lower-resolution
// sessions sound muffled. The controller treats the bitrate as a ceiling, so
// exceeding it on a LAN is safe.
func homeKitMinimumAudioBitrateKbps(width, height int) int {
	switch {
	case width >= 1280 && height >= 720:
		return 48
	case width >= 640 && height >= 360:
		return 40
	case width >= 320 && height >= 240:
		return 32
	default:
		return 0
	}
}

func homeKitSessionSources(streamID string, selection homekit.VideoSelection) []string {
	// The negotiated sample rate drives the RTP timestamp clock Apple keys on.
	// The encoded Opus content can carry more bandwidth than the negotiated
	// rate (e.g. 24kHz SWB instead of 16kHz WB for 240p) without changing the
	// played duration, because the TOC signals frame size and Apple only reads
	// the timestamp for pacing.
	audioSampleRate := selection.AudioSampleRate
	if audioSampleRate == 0 {
		audioSampleRate = 16000
	}
	encodeSampleRate := audioSampleRate
	if encodeSampleRate < 24000 {
		encodeSampleRate = 24000
	}
	framerate := selection.Framerate
	if framerate > maxHomeKitVideoFramerate {
		log.Debug().
			Str("stream", streamID).
			Uint8("requested_fps", selection.Framerate).
			Uint8("applied_fps", maxHomeKitVideoFramerate).
			Msg("[homekit] clamped controller framerate to accessory cap")
		framerate = maxHomeKitVideoFramerate
	}
	bitrate := selection.MaxBitrate
	if bitrate > 0 {
		if minBitrate := homeKitMinimumVideoBitrateKbps(int(selection.Width), int(selection.Height)); uint16(minBitrate) > bitrate {
			log.Debug().
				Str("stream", streamID).
				Uint16("requested_bitrate_kbps", selection.MaxBitrate).
				Uint16("applied_bitrate_kbps", uint16(minBitrate)).
				Msg("[homekit] raised controller bitrate to accessory floor")
			bitrate = uint16(minBitrate)
		}
	}
	audioBitrate := selection.AudioMaxBitrate
	if audioBitrate > 0 {
		if minBitrate := homeKitMinimumAudioBitrateKbps(int(selection.Width), int(selection.Height)); uint16(minBitrate) > audioBitrate {
			log.Debug().
				Str("stream", streamID).
				Uint16("requested_audio_bitrate_kbps", selection.AudioMaxBitrate).
				Uint16("applied_audio_bitrate_kbps", uint16(minBitrate)).
				Msg("[homekit] raised controller audio bitrate to accessory floor")
			audioBitrate = uint16(minBitrate)
		}
	}
	query := []string{
		"audio=opus/" + fmt.Sprintf("%d", encodeSampleRate),
		"profile=" + homeKitH264Profile(selection.ProfileID),
		"level=" + homeKitH264Level(selection.Level),
		fmt.Sprintf("width=%d", selection.Width),
		fmt.Sprintf("height=%d", selection.Height),
		fmt.Sprintf("framerate=%d", framerate),
		"keyframe_interval=1",
	}
	if bitrate > 0 {
		query = append(query, fmt.Sprintf("bitrate=%dK", bitrate))
	}
	// Do NOT pass audio_packet_time to ffmpeg: it makes ffmpeg emit 60ms
	// code-3 bundles itself, and the go2rtc HAP packer (RepackToHAP) then
	// bundles those again into 180ms packets, garbling the audio. Let ffmpeg
	// emit plain 20ms frames and let RepackToHAP build the 60ms HAP packet.
	if audioBitrate > 0 {
		query = append(query, fmt.Sprintf("audio_bitrate=%dK", audioBitrate))
	}
	return ffmpegTranscodeFallbackURIs(streamID, query)
}

func ffmpegTranscodeFallbackURIs(streamID string, query []string) []string {
	join := func(videoParts ...string) string {
		uri := "ffmpeg:" + streamID
		for _, part := range videoParts {
			if part != "" {
				uri += "#" + part
			}
		}
		for _, part := range query {
			if part != "" {
				uri += "#" + part
			}
		}
		return uri
	}
	// Software HEVC decode + libx264 only. VideoToolbox hard-decode fails on
	// mid-GOP MISS joins; hardware encode is optional later.
	return []string{join("video=h264")}
}

func homeKitH264Profile(profile byte) string {
	switch profile {
	case camera.VideoCodecProfileConstrainedBaseline:
		return "baseline"
	case camera.VideoCodecProfileHigh:
		return "high"
	default:
		return "main"
	}
}

func homeKitH264Level(level byte) string {
	switch level {
	case camera.VideoCodecLevel32:
		return "3.2"
	case camera.VideoCodecLevel40:
		return "4.0"
	default:
		return "3.1"
	}
}

func (s *server) clearConsumer(consumer *homekit.Consumer) {
	var slots []uint64
	s.mu.Lock()
	for slot, current := range s.consumers {
		if current == consumer {
			delete(s.consumers, slot)
			slots = append(slots, slot)
		}
	}
	s.mu.Unlock()
	for _, slot := range slots {
		s.setStreamingStatusForSlot(slot, camera.StreamingStatusAvailable)
	}
}

func (s *server) currentConsumer() *homekit.Consumer {
	consumers := s.consumerSnapshot()
	if len(consumers) == 0 {
		return nil
	}
	return consumers[0]
}

func (s *server) consumerForSlot(slot uint64) *homekit.Consumer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consumers[slot]
}

func (s *server) consumerBySession(sessionID string) *homekit.Consumer {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, consumer := range s.consumers {
		if consumer.SessionID() == sessionID {
			return consumer
		}
	}
	return nil
}

func (s *server) consumerSnapshot() []*homekit.Consumer {
	s.mu.Lock()
	defer s.mu.Unlock()
	consumers := make([]*homekit.Consumer, 0, len(s.consumers))
	if s.accessory != nil {
		for _, service := range s.accessory.Services {
			if consumer := s.consumers[service.IID]; consumer != nil {
				consumers = append(consumers, consumer)
			}
		}
		return consumers
	}
	for _, consumer := range s.consumers {
		consumers = append(consumers, consumer)
	}
	return consumers
}

func (s *server) setConsumer(slot uint64, consumer *homekit.Consumer) {
	s.mu.Lock()
	if s.consumers == nil {
		s.consumers = make(map[uint64]*homekit.Consumer)
	}
	s.consumers[slot] = consumer
	s.mu.Unlock()
}

func (s *server) streamSlotForCharacteristic(target *hap.Character) (uint64, bool) {
	if target == nil || s.accessory == nil {
		return 0, false
	}
	for _, service := range s.accessory.Services {
		if service.Type != "110" {
			continue
		}
		for _, character := range service.Characters {
			if character == target {
				return service.IID, true
			}
		}
	}
	return 0, false
}

func (s *server) connections() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]any(nil), s.conns...)
}

func (s *server) stopConsumersForConnection(conn net.Conn) {
	stopped := make(map[*homekit.Consumer]struct{})
	for _, consumer := range s.consumerSnapshot() {
		if !consumer.BelongsTo(conn) {
			continue
		}
		status := consumer.Status()
		_ = consumer.Stop()
		s.rememberSessionStatus(status)
		s.clearConsumer(consumer)
		stopped[consumer] = struct{}{}
	}
	for _, candidate := range s.connections() {
		consumer, ok := candidate.(*homekit.Consumer)
		if !ok || !consumer.BelongsTo(conn) {
			continue
		}
		if _, exists := stopped[consumer]; exists {
			continue
		}
		status := consumer.Status()
		_ = consumer.Stop()
		s.rememberSessionStatus(status)
		s.clearConsumer(consumer)
	}
}

func (s *server) rememberSessionStatus(status homekit.SessionStatus) {
	status.Active = false
	s.mu.Lock()
	s.lastSession = status
	s.mu.Unlock()
}

func (s *server) sessionStatus() homekit.SessionStatus {
	consumers := s.consumerSnapshot()
	s.mu.Lock()
	last := s.lastSession
	s.mu.Unlock()
	var active homekit.SessionStatus
	for _, consumer := range consumers {
		status := consumer.Status()
		if sessionStateRank(status.State) > sessionStateRank(active.State) {
			active = status
		}
	}
	if active.State != "" {
		return active
	}
	if last.State != "" {
		return last
	}
	return homekit.SessionStatus{State: "idle"}
}

func (s *server) setStreamingStatus(status byte) {
	s.setStreamingStatusOn(nil, status)
}

func (s *server) setStreamingStatusOn(target *hap.Character, status byte) {
	if target != nil {
		if slot, ok := s.streamSlotForCharacteristic(target); ok {
			s.setStreamingStatusForSlot(slot, status)
		}
		return
	}
	for _, service := range s.accessory.Services {
		if service.Type != "110" {
			continue
		}
		s.setStreamingStatusForSlot(service.IID, status)
	}
}

func (s *server) setStreamingStatusForSlot(slot uint64, status byte) {
	if s.accessory == nil {
		return
	}
	for _, service := range s.accessory.Services {
		if service.Type != "110" || service.IID != slot {
			continue
		}
		character := service.GetCharacter(camera.TypeStreamingStatus)
		if character == nil {
			return
		}
		if err := character.Set(camera.StreamingStatus{Status: status}); err != nil {
			log.Debug().
				Err(err).
				Str("stream", s.stream).
				Uint64("stream_slot", slot).
				Msg("[homekit] streaming status event delivery failed")
		}
		return
	}
}

func sessionStateRank(state string) int {
	switch state {
	case "streaming":
		return 5
	case "started":
		return 4
	case "answered":
		return 3
	case "prepared":
		return 2
	case "error":
		return 1
	default:
		return 0
	}
}

func (s *server) GetImage(conn net.Conn, width, height int) []byte {
	log.Debug().
		Str("stream", s.stream).
		Int("width", width).
		Int("height", height).
		Msg("[homekit] snapshot requested")

	stream := streams.Get(s.stream)
	cons := magic.NewKeyframe()

	if err := stream.AddConsumer(cons); err != nil {
		return nil
	}

	once := &core.OnceBuffer{} // init and first frame
	_, _ = cons.WriteTo(once)
	b := once.Buffer()

	stream.RemoveConsumer(cons)

	switch cons.CodecName() {
	case core.CodecH264, core.CodecH265:
		var err error
		if b, err = ffmpeg.JPEGWithScale(b, width, height); err != nil {
			return nil
		}
	}

	return b
}

func diagnosticCharacteristicName(characteristicType string) string {
	switch characteristicType {
	case camera.TypeStreamingStatus:
		return "streaming-status"
	case camera.TypeSupportedVideoStreamConfiguration:
		return "supported-video-stream-configuration"
	case camera.TypeSupportedAudioStreamConfiguration:
		return "supported-audio-stream-configuration"
	case camera.TypeSupportedRTPConfiguration:
		return "supported-rtp-configuration"
	case camera.TypeSelectedStreamConfiguration:
		return "selected-stream-configuration"
	case camera.TypeSetupEndpoints:
		return "setup-endpoints"
	case "B0":
		return "active"
	case "11A":
		return "microphone-mute"
	default:
		return "type-" + characteristicType
	}
}

func diagnosticSessionCommandName(command byte) string {
	switch command {
	case camera.SessionCommandEnd:
		return "end"
	case camera.SessionCommandStart:
		return "start"
	case camera.SessionCommandSuspend:
		return "suspend"
	case camera.SessionCommandResume:
		return "resume"
	case camera.SessionCommandReconfigure:
		return "reconfigure"
	default:
		return "unknown"
	}
}

func diagnosticValueLength(value any) int {
	switch value := value.(type) {
	case string:
		return len(value)
	case []byte:
		return len(value)
	default:
		return 0
	}
}

func calcName(name, seed string) string {
	if name != "" {
		return name
	}
	b := sha512.Sum512([]byte(seed))
	return fmt.Sprintf("go2rtc-%02X%02X", b[0], b[2])
}

func calcDeviceID(deviceID, seed string) string {
	if deviceID != "" {
		if len(deviceID) >= 17 {
			// 1. Returd device_id as is (ex. AA:BB:CC:DD:EE:FF)
			return deviceID
		}
		// 2. Use device_id as seed if not zero
		seed = deviceID
	}
	b := sha512.Sum512([]byte(seed))
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", b[32], b[34], b[36], b[38], b[40], b[42])
}

func calcDevicePrivate(private, seed string) []byte {
	if private != "" {
		// 1. Decode private from HEX string
		if b, _ := hex.DecodeString(private); len(b) == ed25519.PrivateKeySize {
			// 2. Return if OK
			return b
		}
		// 3. Use private as seed if not zero
		seed = private
	}
	b := sha512.Sum512([]byte(seed))
	return ed25519.NewKeyFromSeed(b[:ed25519.SeedSize])
}

func calcSetupID(seed string) string {
	b := sha512.Sum512([]byte(seed))
	return fmt.Sprintf("%02X%02X", b[44], b[46])
}

func calcCategoryID(categoryID string) string {
	switch categoryID {
	case "bridge":
		return hap.CategoryBridge
	case "doorbell":
		return hap.CategoryDoorbell
	}
	if core.Atoi(categoryID) > 0 {
		return categoryID
	}
	return hap.CategoryCamera
}
