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
	"go.uber.org/zap"
)

const initialControllerRTCPWait = 250 * time.Millisecond

// HomeKit refreshes the camera tile periodically while the Home view is open.
// Keeping the temporary H.264 producer alive for one minute avoids an
// encoder start/stop cycle between those refreshes without making preload
// cameras permanently consume an encoder when HomeKit is idle.
const homeKitPreviewKeepalive = time.Minute

type server struct {
	hap  *hap.Server // server for HAP connection and encryption
	mdns *mdns.ServiceEntry

	pairings []string // pairings list
	conns    []any
	mu       sync.Mutex

	accessory      *hap.Accessory // HAP accessory
	consumers      map[uint64]*homekit.Consumer
	lastSession    homekit.SessionStatus
	proxyURL       string
	setupID        string
	stream         string // stream name from YAML
	inputStream    string // current shared H.264 input stream for HomeKit sessions
	connectionMode string

	mediaMu             sync.Mutex
	previewTimer        *time.Timer
	previewGeneration   uint64
	previewPreloaded    bool
	previewExpired      bool
	activeMediaSessions int
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

func (s *server) currentInputStream() string {
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	if s.inputStream == "" {
		return s.stream
	}
	return s.inputStream
}

// touchHomeKitPreview starts or refreshes the temporary H.264 producer used by
// preload cameras. always_on already has a permanent shared H.264 producer and
// on_demand must not acquire any background media, so both modes are no-ops.
func (s *server) touchHomeKitPreview() {
	if s.connectionMode != "preload" {
		return
	}

	source := streams.Get(s.stream)
	if source == nil {
		return
	}

	s.mediaMu.Lock()
	if s.inputStream == "" {
		s.inputStream = s.stream
	}
	if s.inputStream == s.stream {
		inputID := ensureHomeKitInputStream(s.stream, source)
		if inputID == s.stream {
			s.mediaMu.Unlock()
			log.Debug("HomeKit preview did not find an H264 fallback", zap.String("stream", s.stream))
			return
		}
		s.inputStream = inputID
	}

	inputID := s.inputStream
	s.previewGeneration++
	generation := s.previewGeneration
	s.previewExpired = false
	if s.previewTimer != nil {
		s.previewTimer.Stop()
	}
	s.previewTimer = time.AfterFunc(homeKitPreviewKeepalive, func() {
		s.expireHomeKitPreview(generation)
	})
	startPreload := !s.previewPreloaded
	s.previewPreloaded = true
	s.mediaMu.Unlock()

	if startPreload {
		if err := streams.AddPreload(inputID, "video"); err != nil {
			s.mediaMu.Lock()
			if s.previewGeneration == generation {
				s.previewPreloaded = false
				s.previewExpired = false
				s.inputStream = s.stream
				if s.previewTimer != nil {
					s.previewTimer.Stop()
					s.previewTimer = nil
				}
			}
			s.mediaMu.Unlock()
			log.Warn("HomeKit temporary H264 preload failed", zap.Error(err), zap.String("stream", s.stream), zap.String("input_stream", inputID))
			return
		}
		log.Info("HomeKit temporary H264 preload started", zap.String("stream", s.stream), zap.String("input_stream", inputID))
	} else {
		log.Debug("HomeKit temporary H264 preload refreshed", zap.String("stream", s.stream), zap.String("input_stream", inputID))
	}
}

func (s *server) expireHomeKitPreview(generation uint64) {
	s.mediaMu.Lock()
	if generation != s.previewGeneration || !s.previewPreloaded {
		s.mediaMu.Unlock()
		return
	}
	s.previewExpired = true
	if s.activeMediaSessions > 0 {
		s.mediaMu.Unlock()
		return
	}
	inputID := s.releaseHomeKitPreviewLocked()
	s.mediaMu.Unlock()
	s.removeHomeKitPreviewPreload(inputID)
}

func (s *server) releaseHomeKitPreviewLocked() string {
	if !s.previewPreloaded || s.inputStream == "" || s.inputStream == s.stream {
		return ""
	}
	inputID := s.inputStream
	s.inputStream = s.stream
	s.previewPreloaded = false
	s.previewExpired = false
	s.previewTimer = nil
	return inputID
}

func (s *server) removeHomeKitPreviewPreload(inputID string) {
	if inputID == "" || inputID == s.stream {
		return
	}
	if err := streams.DelPreload(inputID); err != nil {
		log.Debug("HomeKit temporary H264 preload already stopped", zap.Error(err), zap.String("stream", s.stream), zap.String("input_stream", inputID))
		return
	}
	log.Info("HomeKit temporary H264 preload released", zap.String("stream", s.stream), zap.String("input_stream", inputID))
}

func (s *server) acquireHomeKitSessionInput() (string, func()) {
	s.mediaMu.Lock()
	inputID := s.inputStream
	if inputID == "" {
		inputID = s.stream
	}
	if s.connectionMode == "preload" && inputID != s.stream {
		s.activeMediaSessions++
	}
	s.mediaMu.Unlock()

	return inputID, func() {
		s.mediaMu.Lock()
		if s.connectionMode != "preload" || inputID == s.stream || s.activeMediaSessions == 0 {
			s.mediaMu.Unlock()
			return
		}
		s.activeMediaSessions--
		if s.activeMediaSessions != 0 || !s.previewExpired {
			s.mediaMu.Unlock()
			return
		}
		released := s.releaseHomeKitPreviewLocked()
		s.mediaMu.Unlock()
		s.removeHomeKitPreviewPreload(released)
	}
}

func (s *server) Handle(w http.ResponseWriter, r *http.Request) {
	conn, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		log.Warn("HAP connection hijack failed", zap.Error(err), zap.String("stream", s.stream))
		return
	}

	defer conn.Close()

	// Fix reading from Body after Hijack.
	r.Body = io.NopCloser(rw)

	switch r.RequestURI {
	case hap.PathPairSetup:
		log.Debug("pair setup requested", zap.String("stream", s.stream))
		id, key, err := s.hap.PairSetup(r, rw)
		if err != nil {
			log.Warn("pair setup failed", zap.Error(err), zap.String("stream", s.stream))
			return
		}

		s.AddPair(id, key, hap.PermissionAdmin)
		log.Info("pair setup completed", zap.String("stream", s.stream))

	case hap.PathPairVerify:
		id, key, err := s.hap.PairVerify(r, rw)
		if err != nil {
			log.Debug("pair verify failed", zap.Error(err), zap.String("stream", s.stream))
			return
		}

		log.Debug("pair verify succeeded",
			zap.String("stream", s.stream),
			zap.String("client_id", id),
			zap.String("remote_address", conn.RemoteAddr().String()))

		controller, err := hap.NewConn(conn, rw, key, false)
		if err != nil {
			log.Warn("encrypted HAP session initialization failed", zap.Error(err), zap.String("stream", s.stream))
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
				log.Error("HAP proxy dial failed", zap.Error(err), zap.String("stream", s.stream))
				return
			}
			handler = homekit.ProxyHandler(s, client.Conn)
		}

		started := time.Now()
		if err = handler(controller); err != nil && !expectedHAPConnectionClose(err) {
			log.Warn("encrypted HAP session ended with error", zap.Error(err),
				zap.String("stream", s.stream), zap.Duration("connected_for", time.Since(started)))
			return
		}
		log.Debug("encrypted HAP session peer closed", zap.Error(err),
			zap.String("stream", s.stream), zap.Duration("connected_for", time.Since(started)))
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
	log.Debug("connection added", zap.String("stream", s.stream), zap.Stringer("connection", logger{v}))
	s.mu.Lock()
	s.conns = append(s.conns, v)
	s.mu.Unlock()
}

func (s *server) DelConn(v any) {
	log.Debug("connection removed", zap.String("stream", s.stream), zap.Stringer("connection", logger{v}))
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
	log.Debug("pair added", zap.String("stream", s.stream), zap.String("client_id", id),
		zap.Uint8("permissions", permissions))

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
	log.Debug("pair removed", zap.String("stream", s.stream), zap.String("client_id", id))

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
		log.Error("cannot save pairings", zap.Error(err), zap.String("stream", s.stream), zap.Int("pairing_count", len(s.pairings)))
	}
}

func (s *server) GetAccessories(_ net.Conn) []*hap.Accessory {
	log.Debug("accessory database requested", zap.String("stream", s.stream),
		zap.Int("service_count", len(s.accessory.Services)), zap.String("config_number", accessoryConfigNumber))
	return []*hap.Accessory{s.accessory}
}

func (s *server) GetCharacteristic(conn net.Conn, aid uint8, iid uint64) any {
	char := s.accessory.GetCharacterByID(iid)
	if char == nil {
		log.Warn("unknown characteristic read", zap.String("stream", s.stream), zap.Uint8("aid", aid), zap.Uint64("iid", iid))
		return nil
	}
	log.Debug("characteristic read", zap.String("stream", s.stream), zap.Uint8("aid", aid), zap.Uint64("iid", iid),
		zap.String("characteristic", diagnosticCharacteristicName(char.Type)), zap.String("characteristic_type", char.Type))

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
			log.Debug("SetupEndpoints read has no decodable stored answer", zap.String("stream", s.stream), zap.Error(err))
			return char.Value
		}
		slot, _ := s.streamSlotForCharacteristic(char)
		log.Info("SetupEndpoints answer read", zap.String("stream", s.stream), zap.Uint64("stream_slot", slot),
			zap.Uint8("status", answer.Status), zap.String("accessory_address", answer.Address.IPAddr),
			zap.Uint16("video_port", answer.Address.VideoRTPPort), zap.Uint16("audio_port", answer.Address.AudioRTPPort))
		return char.Value
	}

	return char.Value
}

func (s *server) SetCharacteristic(conn net.Conn, aid uint8, iid uint64, value any, writeResponse bool) any {
	char := s.accessory.GetCharacterByID(iid)
	if char == nil {
		log.Warn("unknown characteristic write", zap.String("stream", s.stream), zap.Uint8("aid", aid), zap.Uint64("iid", iid),
			zap.Bool("write_response", writeResponse), zap.Int("value_length", diagnosticValueLength(value)))
		return nil
	}
	log.Debug("characteristic write", zap.String("stream", s.stream), zap.Uint8("aid", aid), zap.Uint64("iid", iid),
		zap.String("characteristic", diagnosticCharacteristicName(char.Type)), zap.String("characteristic_type", char.Type),
		zap.Bool("write_response", writeResponse), zap.Int("value_length", diagnosticValueLength(value)))

	switch char.Type {
	case "B0", "11A":
		if err := char.Write(value); err != nil {
			log.Warn("scalar characteristic write rejected", zap.Error(err), zap.String("stream", s.stream),
				zap.String("characteristic", diagnosticCharacteristicName(char.Type)))
			return nil
		}
		if err := char.NotifyListeners(conn); err != nil {
			log.Debug("scalar characteristic event delivery failed", zap.Error(err), zap.String("stream", s.stream),
				zap.String("characteristic", diagnosticCharacteristicName(char.Type)))
		}
		if writeResponse {
			return char.Value
		}
		return nil

	case camera.TypeSetupEndpoints:
		var offer camera.SetupEndpointsRequest
		if err := tlv8.UnmarshalBase64(value, &offer); err != nil {
			log.Warn("rejected malformed SetupEndpoints", zap.Error(err), zap.String("stream", s.stream))
			s.rememberSessionStatus(homekit.SessionStatus{State: "setup-invalid"})
			return nil
		}
		slot, ok := s.streamSlotForCharacteristic(char)
		if !ok {
			log.Warn("SetupEndpoints is not owned by an RTP stream service", zap.String("stream", s.stream), zap.Uint64("iid", iid))
			return nil
		}

		if current := s.consumerForSlot(slot); current != nil && !current.Stopped() {
			log.Info("SetupEndpoints rejected because its stream slot is busy", zap.String("stream", s.stream), zap.Uint64("stream_slot", slot))
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
		log.Info("RTP endpoints offered", zap.String("stream", s.stream), zap.Uint64("stream_slot", slot),
			zap.String("controller", offer.Address.IPAddr), zap.Uint16("video_port", offer.Address.VideoRTPPort),
			zap.Uint16("audio_port", offer.Address.AudioRTPPort), zap.Uint8("ip_version", offer.Address.IPVersion),
			zap.String("media_bind", consumer.SRTPBindMode()))

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
			log.Warn("rejected malformed selected stream configuration", zap.Error(err), zap.String("stream", s.stream))
			s.rememberSessionStatus(homekit.SessionStatus{State: "selected-invalid"})
			return nil
		}
		slot, ok := s.streamSlotForCharacteristic(char)
		if !ok {
			log.Warn("selected stream configuration is not owned by an RTP stream service", zap.String("stream", s.stream), zap.Uint64("iid", iid))
			return nil
		}

		log.Debug("selected stream command received", zap.String("stream", s.stream), zap.Uint64("stream_slot", slot),
			zap.Uint8("command", conf.Control.Command), zap.String("command_name", diagnosticSessionCommandName(conf.Control.Command)))

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
				log.Warn("START has no prepared session in its stream slot", zap.String("stream", s.stream), zap.Uint64("stream_slot", slot))
				return nil
			}
			if consumer.SessionID() != conf.Control.SessionID {
				log.Warn("START session does not match its stream slot", zap.String("stream", s.stream), zap.Uint64("stream_slot", slot))
				return nil
			}

			if !consumer.SetConfig(&conf) {
				log.Warn("rejected selected stream configuration", zap.String("stream", s.stream), zap.Uint64("stream_slot", slot),
					zap.Uint8("video_type", conf.VideoCodec.CodecType), zap.Any("video_params", conf.VideoCodec.CodecParams),
					zap.Any("video_attrs", conf.VideoCodec.VideoAttrs), zap.Int("video_rtp_count", len(conf.VideoCodec.RTPParams)),
					zap.Uint8("audio_type", conf.AudioCodec.CodecType), zap.Int("audio_rtp_count", len(conf.AudioCodec.RTPParams)))
				_ = consumer.Stop()
				s.clearConsumer(consumer)
				return nil
			}

			fields := []zap.Field{
				zap.String("stream", s.stream), zap.Uint64("stream_slot", slot),
				zap.Uint8("video_payload_type", conf.VideoCodec.RTPParams[0].PayloadType),
				zap.Uint16("video_bitrate_kbps", conf.VideoCodec.RTPParams[0].MaxBitrate),
				zap.String("video_profile", homeKitH264Profile(conf.VideoCodec.CodecParams[0].ProfileID[0])),
				zap.String("video_level", homeKitH264Level(conf.VideoCodec.CodecParams[0].Level[0])),
				zap.Int("audio_sample_rate_hz", consumer.VideoSelection().AudioSampleRate),
				zap.Uint8("audio_packet_time_ms", conf.AudioCodec.CodecParams[0].RTPTime[0]),
				zap.Uint16("audio_bitrate_kbps", conf.AudioCodec.RTPParams[0].MaxBitrate),
			}
			if len(conf.VideoCodec.VideoAttrs) > 0 {
				fields = append(fields, zap.Uint16("width", conf.VideoCodec.VideoAttrs[0].Width),
					zap.Uint16("height", conf.VideoCodec.VideoAttrs[0].Height), zap.Uint8("fps", conf.VideoCodec.VideoAttrs[0].Framerate))
			}
			log.Info("selected live stream configuration", fields...)

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
		log.Warn("unknown characteristic event subscription", zap.String("stream", s.stream), zap.Uint8("aid", aid),
			zap.Uint64("iid", iid), zap.Bool("enabled", enabled))
		return
	}
	if !slices.Contains(char.Perms, "ev") {
		log.Warn("unsupported characteristic event subscription", zap.String("stream", s.stream), zap.Uint8("aid", aid),
			zap.Uint64("iid", iid), zap.String("characteristic", diagnosticCharacteristicName(char.Type)), zap.Bool("enabled", enabled))
		return
	}
	if enabled {
		char.AddListener(conn)
	} else {
		char.RemoveListener(conn)
	}
	log.Debug("characteristic event subscription", zap.String("stream", s.stream), zap.Uint8("aid", aid), zap.Uint64("iid", iid),
		zap.String("characteristic", diagnosticCharacteristicName(char.Type)), zap.Bool("enabled", enabled))
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
		log.Warn("start live stream missing source", zap.String("stream", s.stream), zap.Uint64("stream_slot", slot))
		_ = consumer.Stop()
		s.rememberSessionStatus(consumer.Status())
		s.DelConn(consumer)
		s.clearConsumer(consumer)
		return
	}

	selection := consumer.VideoSelection()
	log.Info("applying controller media selection", zap.String("stream", s.stream), zap.Uint64("stream_slot", slot),
		zap.Uint16("video_width", selection.Width), zap.Uint16("video_height", selection.Height),
		zap.Uint8("video_framerate", selection.Framerate), zap.Uint16("video_max_bitrate_kbps", selection.MaxBitrate),
		zap.String("video_profile", homeKitH264Profile(selection.ProfileID)), zap.String("video_level", homeKitH264Level(selection.Level)),
		zap.Int("audio_sample_rate_hz", selection.AudioSampleRate), zap.Uint8("audio_packet_time_ms", selection.AudioPacketTime),
		zap.Uint16("audio_max_bitrate_kbps", selection.AudioMaxBitrate))
	// Session-local software transcode matching the controller selection
	// (width/height/bitrate). The shared input stream is only a warm, clean H264
	// source; it is still resized here because feeding 720p@CRF directly into a
	// 360p@132K HomeKit session causes FIR storms and unplayable video. Do not
	// use VideoToolbox HEVC hard-decode here.
	inputStream, releaseInput := s.acquireHomeKitSessionInput()
	defer releaseInput()
	stream := streams.NewStream(homeKitSessionSources(inputStream, selection))
	defer func() {
		// RemoveConsumer is idempotent for the HAP consumer and also stops any
		// session-local FFmpeg producer when AddConsumer partially started it.
		// This is important for always_on: the shared H.264 producer remains, but
		// the controller-specific second transcode must not remain alive.
		stream.RemoveConsumer(consumer)
		log.Debug("HomeKit session transcode released", zap.String("stream", s.stream), zap.Uint64("stream_slot", slot))
		status := consumer.Status()
		s.rememberSessionStatus(status)
		s.DelConn(consumer)
		s.clearConsumer(consumer)
		log.Info("live stream consumer ended", zap.String("stream", s.stream), zap.Uint64("stream_slot", slot),
			zap.String("state", status.State), zap.Uint64("video_packets", status.VideoPackets), zap.Uint64("video_bytes", status.VideoBytes),
			zap.Uint64("video_write_errors", status.VideoWriteErrors), zap.Uint32("video_ssrc", status.VideoSSRC),
			zap.Uint64("video_rtcp_datagrams", status.VideoRTCPDatagrams), zap.Uint64("video_rtcp_packets", status.VideoRTCPPackets),
			zap.Uint64("video_rtcp_failures", status.VideoRTCPFailures), zap.Uint64("video_rtcp_parse_errors", status.VideoRTCPParseErrors),
			zap.Uint64("video_rtcp_receiver_reports", status.VideoRTCPReceiverReports), zap.Uint64("video_rtcp_report_blocks", status.VideoRTCPReportBlocks),
			zap.Uint64("video_rtcp_matched_reports", status.VideoRTCPMatchedReports), zap.Uint32("video_rtcp_reported_ssrc", status.VideoRTCPReportedSSRC),
			zap.Uint32("video_rtcp_fraction_lost", status.VideoRTCPFractionLost), zap.Uint32("video_rtcp_total_lost", status.VideoRTCPTotalLost),
			zap.Uint32("video_rtcp_last_sequence", status.VideoRTCPLastSequence), zap.Uint32("video_rtcp_jitter", status.VideoRTCPJitter),
			zap.Uint32("video_rtcp_last_sender_report", status.VideoRTCPLastSenderReport), zap.Uint64("video_rtcp_pli", status.VideoRTCPPLI),
			zap.Uint64("video_rtcp_fir", status.VideoRTCPFIR), zap.Uint64("video_rtcp_nack", status.VideoRTCPNACK),
			zap.Uint64("video_sps_units", status.VideoSPSUnits), zap.Uint64("video_pps_units", status.VideoPPSUnits),
			zap.Uint64("video_idr_units", status.VideoIDRUnits), zap.Uint64("video_stap_a_units", status.VideoSTAPAUnits),
			zap.Uint64("video_stap_a_zero_nri", status.VideoSTAPAZeroNRI), zap.Uint32("video_max_datagram_bytes", status.VideoMaxDatagram))
	}()

	rtcpWaitStarted := time.Now()
	rtcpReady := consumer.WaitForVideoRTCP(initialControllerRTCPWait)
	if consumer.Stopped() {
		return
	}
	log.Info("controller UDP return path checked", zap.String("stream", s.stream), zap.Uint64("stream_slot", slot),
		zap.Bool("initial_rtcp", rtcpReady), zap.Int64("initial_rtcp_wait_ms", time.Since(rtcpWaitStarted).Milliseconds()))

	if err := stream.AddConsumer(consumer); err != nil {
		log.Warn("start live stream consumer failed", zap.Error(err), zap.String("stream", s.stream), zap.Uint64("stream_slot", slot))
		return
	}
	startStatus := consumer.Status()
	log.Info("live stream consumer started", zap.String("stream", s.stream), zap.Uint64("stream_slot", slot),
		zap.Bool("rtcp_before_media", startStatus.VideoRTCPDatagrams > 0))

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
		log.Debug("clamped controller framerate to accessory cap", zap.String("stream", streamID),
			zap.Uint8("requested_fps", selection.Framerate), zap.Uint8("applied_fps", maxHomeKitVideoFramerate))
		framerate = maxHomeKitVideoFramerate
	}
	bitrate := selection.MaxBitrate
	if bitrate > 0 {
		if minBitrate := homeKitMinimumVideoBitrateKbps(int(selection.Width), int(selection.Height)); uint16(minBitrate) > bitrate {
			log.Debug("raised controller bitrate to accessory floor", zap.String("stream", streamID),
				zap.Uint16("requested_bitrate_kbps", selection.MaxBitrate), zap.Uint16("applied_bitrate_kbps", uint16(minBitrate)))
			bitrate = uint16(minBitrate)
		}
	}
	audioBitrate := selection.AudioMaxBitrate
	if audioBitrate > 0 {
		if minBitrate := homeKitMinimumAudioBitrateKbps(int(selection.Width), int(selection.Height)); uint16(minBitrate) > audioBitrate {
			log.Debug("raised controller audio bitrate to accessory floor", zap.String("stream", streamID),
				zap.Uint16("requested_audio_bitrate_kbps", selection.AudioMaxBitrate), zap.Uint16("applied_audio_bitrate_kbps", uint16(minBitrate)))
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
			log.Debug("streaming status event delivery failed", zap.Error(err), zap.String("stream", s.stream), zap.Uint64("stream_slot", slot))
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
	log.Debug("snapshot requested", zap.String("stream", s.stream), zap.Int("width", width), zap.Int("height", height))

	stream := streams.Get(s.currentInputStream())
	if stream == nil {
		return nil
	}
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

	if len(b) > 0 {
		// Apple Home requests snapshots while the Home tab is visible. Treat a
		// successful image as activity, not merely a request, so failed retries
		// do not keep a temporary encoder alive indefinitely.
		s.touchHomeKitPreview()
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
