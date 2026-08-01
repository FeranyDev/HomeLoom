package matterwebrtc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/pion/interceptor"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

const (
	defaultKeyframeTimeout = 5 * time.Second
	defaultGatherTimeout   = 3 * time.Second
	defaultDisconnectGrace = 10 * time.Second
)

var (
	ErrSessionActive     = errors.New("Matter WebRTC session is already active")
	ErrSessionNotFound   = errors.New("Matter WebRTC session was not found")
	ErrSourceNotFound    = errors.New("Matter WebRTC stream was not found")
	ErrKeyframeTimeout   = errors.New("Matter WebRTC H.264 keyframe startup timeout")
	ErrUnsupportedOffer  = errors.New("Matter WebRTC offer does not contain H.264 packetization-mode=1")
	errGatheringTimedOut = errors.New("Matter WebRTC ICE gathering timeout")
)

// Source attaches a constrained RTP consumer to one configured stream.
// detach must be safe to call once after a successful attachment.
type Source interface {
	Attach(string, core.Consumer) (detach func(), err error)
}

type Options struct {
	KeyframeTimeout time.Duration
	GatherTimeout   time.Duration
	DisconnectGrace time.Duration
	NewPeer         func() (*webrtc.PeerConnection, error)
	NewSessionID    func() (string, error)
}

type Service struct {
	source Source

	keyframeTimeout time.Duration
	gatherTimeout   time.Duration
	disconnectGrace time.Duration
	newPeer         func() (*webrtc.PeerConnection, error)
	newSessionID    func() (string, error)

	mu      sync.Mutex
	session *session
}

type session struct {
	id       string
	streamID string
	pc       *webrtc.PeerConnection
	consumer *consumer
	detach   func()
	opMu     sync.Mutex
	once     sync.Once
}

func NewService(source Source, options Options) (*Service, error) {
	if source == nil {
		return nil, errors.New("Matter WebRTC source is required")
	}
	if options.KeyframeTimeout <= 0 {
		options.KeyframeTimeout = defaultKeyframeTimeout
	}
	if options.GatherTimeout <= 0 {
		options.GatherTimeout = defaultGatherTimeout
	}
	if options.DisconnectGrace <= 0 {
		options.DisconnectGrace = defaultDisconnectGrace
	}
	if options.NewPeer == nil {
		options.NewPeer = newPeerConnection
	}
	if options.NewSessionID == nil {
		options.NewSessionID = randomSessionID
	}
	return &Service{
		source: source, keyframeTimeout: options.KeyframeTimeout, gatherTimeout: options.GatherTimeout,
		disconnectGrace: options.DisconnectGrace,
		newPeer:         options.NewPeer, newSessionID: options.NewSessionID,
	}, nil
}

func newPeerConnection() (*webrtc.PeerConnection, error) {
	var mediaEngine webrtc.MediaEngine
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
			SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	registry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(&mediaEngine, registry); err != nil {
		return nil, err
	}
	var settings webrtc.SettingEngine
	// No ICE servers are configured, and UDP host candidates are the only
	// network types enabled. This prevents STUN/TURN or TCP candidate expansion.
	settings.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6})
	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(&mediaEngine),
		webrtc.WithInterceptorRegistry(registry),
		webrtc.WithSettingEngine(settings),
	)
	return api.NewPeerConnection(webrtc.Configuration{})
}

func randomSessionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (s *Service) Open(ctx context.Context, streamID, offer string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != nil {
		return "", "", ErrSessionActive
	}
	kinds, err := validateOffer(offer)
	if err != nil {
		return "", "", err
	}
	id, err := s.newSessionID()
	if err != nil {
		return "", "", fmt.Errorf("create Matter WebRTC session ID: %w", err)
	}
	pc, err := s.newPeer()
	if err != nil {
		return "", "", fmt.Errorf("create Matter WebRTC peer: %w", err)
	}
	current := &session{id: id, streamID: streamID, pc: pc}
	cleanup := true
	defer func() {
		if cleanup {
			current.close()
		}
	}()
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		switch state {
		case webrtc.ICEConnectionStateFailed, webrtc.ICEConnectionStateClosed:
			go s.closeIfCurrent(id)
		case webrtc.ICEConnectionStateDisconnected:
			go func() {
				timer := time.NewTimer(s.disconnectGrace)
				defer timer.Stop()
				<-timer.C
				if pc.ICEConnectionState() == webrtc.ICEConnectionStateDisconnected {
					s.closeIfCurrent(id)
				}
			}()
		}
	})
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return "", "", fmt.Errorf("apply Matter WebRTC offer: %w", err)
	}
	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
			SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
		},
		"video", streamID,
	)
	if err != nil {
		return "", "", err
	}
	videoSender, err := pc.AddTrack(videoTrack)
	if err != nil {
		return "", "", fmt.Errorf("add Matter WebRTC H.264 track: %w", err)
	}
	drainRTCP(videoSender)

	tracks := map[string]*webrtc.TrackLocalStaticRTP{core.KindVideo: videoTrack}
	if kinds[core.KindAudio] {
		audioTrack, createErr := webrtc.NewTrackLocalStaticRTP(
			webrtc.RTPCodecCapability{
				MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
				SDPFmtpLine: "minptime=10;useinbandfec=1",
			},
			"audio", streamID,
		)
		if createErr != nil {
			return "", "", createErr
		}
		audioSender, addErr := pc.AddTrack(audioTrack)
		if addErr != nil {
			return "", "", fmt.Errorf("add Matter WebRTC Opus track: %w", addErr)
		}
		drainRTCP(audioSender)
		tracks[core.KindAudio] = audioTrack
	}
	current.consumer = newConsumer(tracks)
	current.detach, err = s.source.Attach(streamID, current.consumer)
	if err != nil {
		return "", "", err
	}
	if err := waitForKeyframe(ctx, current.consumer.keyframe, s.keyframeTimeout); err != nil {
		return "", "", err
	}
	answer, err := createAnswer(ctx, pc, s.gatherTimeout)
	if err != nil {
		return "", "", err
	}
	s.session = current
	cleanup = false
	return id, answer, nil
}

func (s *Service) Reoffer(ctx context.Context, sessionID, offer string) (string, error) {
	current, err := s.current(sessionID)
	if err != nil {
		return "", err
	}
	if _, err := validateOffer(offer); err != nil {
		return "", err
	}
	current.opMu.Lock()
	defer current.opMu.Unlock()
	if err := current.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return "", fmt.Errorf("apply Matter WebRTC reoffer: %w", err)
	}
	return createAnswer(ctx, current.pc, s.gatherTimeout)
}

func (s *Service) AddICECandidate(sessionID string, candidate webrtc.ICECandidateInit) error {
	current, err := s.current(sessionID)
	if err != nil {
		return err
	}
	current.opMu.Lock()
	defer current.opMu.Unlock()
	if err := current.pc.AddICECandidate(candidate); err != nil {
		return fmt.Errorf("add Matter WebRTC ICE candidate: %w", err)
	}
	return nil
}

func (s *Service) Close(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return nil
	}
	if sessionID != s.session.id {
		return ErrSessionNotFound
	}
	current := s.session
	s.session = nil
	current.close()
	return nil
}

func (s *Service) current(sessionID string) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil || s.session.id != sessionID {
		return nil, ErrSessionNotFound
	}
	return s.session, nil
}

func (s *Service) closeIfCurrent(sessionID string) {
	s.mu.Lock()
	if s.session == nil || s.session.id != sessionID {
		s.mu.Unlock()
		return
	}
	current := s.session
	s.session = nil
	s.mu.Unlock()
	current.close()
}

func (s *session) close() {
	s.once.Do(func() {
		if s.detach != nil {
			s.detach()
		} else if s.consumer != nil {
			_ = s.consumer.Stop()
		}
		_ = s.pc.Close()
	})
}

func createAnswer(ctx context.Context, pc *webrtc.PeerConnection, timeout time.Duration) (string, error) {
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("create Matter WebRTC answer: %w", err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("apply Matter WebRTC answer: %w", err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-gathered:
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", errGatheringTimedOut
	}
	if pc.LocalDescription() == nil {
		return "", errors.New("Matter WebRTC local description is unavailable")
	}
	return pc.LocalDescription().SDP, nil
}

func validateOffer(raw string) (map[string]bool, error) {
	var description sdp.SessionDescription
	if err := description.Unmarshal([]byte(raw)); err != nil {
		return nil, fmt.Errorf("parse Matter WebRTC offer: %w", err)
	}
	kinds := make(map[string]bool)
	for _, media := range description.MediaDescriptions {
		for _, format := range media.MediaName.Formats {
			codec := core.UnmarshalCodec(media, format)
			switch media.MediaName.Media {
			case core.KindVideo:
				if codec.Name == core.CodecH264 && codec.ClockRate == 90000 &&
					strings.Contains(strings.ToLower(codec.FmtpLine), "packetization-mode=1") {
					kinds[core.KindVideo] = true
				}
			case core.KindAudio:
				if codec.Name == core.CodecOpus && codec.ClockRate == 48000 {
					kinds[core.KindAudio] = true
				}
			}
		}
	}
	if !kinds[core.KindVideo] {
		return nil, ErrUnsupportedOffer
	}
	return kinds, nil
}

func waitForKeyframe(ctx context.Context, keyframe <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-keyframe:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ErrKeyframeTimeout
	}
}

func drainRTCP(sender *webrtc.RTPSender) {
	go func() {
		for {
			if _, _, err := sender.ReadRTCP(); err != nil {
				return
			}
		}
	}()
}
