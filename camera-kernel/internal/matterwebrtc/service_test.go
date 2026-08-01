package matterwebrtc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type fakeSource struct {
	mu          sync.Mutex
	emitVideo   bool
	attachments int
	detachments int
}

func (s *fakeSource) Attach(_ string, target core.Consumer) (func(), error) {
	s.mu.Lock()
	s.attachments++
	s.mu.Unlock()
	for _, media := range target.GetMedias() {
		if media.Kind != core.KindVideo {
			continue
		}
		codec := media.Codecs[0]
		receiver := core.NewReceiver(&core.Media{
			Kind: core.KindVideo, Direction: core.DirectionRecvonly, Codecs: []*core.Codec{codec},
		}, codec)
		if err := target.AddTrack(media, codec, receiver); err != nil {
			return nil, err
		}
		if s.emitVideo {
			receiver.WriteRTP(&rtp.Packet{
				Header:  rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: 1, Timestamp: 90000, SSRC: 1, Marker: true},
				Payload: []byte{0x65, 0x88, 0x84},
			})
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = target.Stop()
			s.mu.Lock()
			s.detachments++
			s.mu.Unlock()
		})
	}, nil
}

func (s *fakeSource) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attachments, s.detachments
}

func controllerOffer(t *testing.T, audio bool) (*webrtc.PeerConnection, string) {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatal(err)
	}
	if audio {
		if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			t.Fatal(err)
		}
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
	case <-time.After(5 * time.Second):
		t.Fatal("controller ICE gathering timeout")
	}
	return pc, pc.LocalDescription().SDP
}

func openService(t *testing.T, source *fakeSource, audio bool) (*Service, *webrtc.PeerConnection, string, string) {
	t.Helper()
	controller, offer := controllerOffer(t, audio)
	service, err := NewService(source, Options{
		KeyframeTimeout: time.Second, GatherTimeout: 5 * time.Second,
		NewSessionID: func() (string, error) { return "session-one", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, answer, err := service.Open(context.Background(), "camera-stream", offer)
	if err != nil {
		_ = controller.Close()
		t.Fatal(err)
	}
	return service, controller, sessionID, answer
}

func TestPeerNegotiatesOnlyConstrainedCodecs(t *testing.T) {
	source := &fakeSource{emitVideo: true}
	service, controller, sessionID, answer := openService(t, source, true)
	defer controller.Close()
	defer service.Close(sessionID)
	lower := strings.ToLower(answer)
	if !strings.Contains(lower, "h264/90000") || !strings.Contains(lower, "packetization-mode=1") ||
		!strings.Contains(lower, "opus/48000") {
		t.Fatalf("constrained codecs missing from answer:\n%s", answer)
	}
	for _, forbidden := range []string{"vp8/90000", "vp9/90000", "av1/90000", "pcmu/8000", "pcma/8000"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("answer exposed forbidden codec %q:\n%s", forbidden, answer)
		}
	}
	if strings.Contains(lower, " typ relay ") || strings.Contains(lower, " typ srflx ") {
		t.Fatalf("answer exposed a non-host ICE candidate:\n%s", answer)
	}
}

func TestSingleSessionAndIdempotentCloseReleaseSource(t *testing.T) {
	source := &fakeSource{emitVideo: true}
	service, controller, sessionID, _ := openService(t, source, false)
	defer controller.Close()
	secondController, offer := controllerOffer(t, false)
	defer secondController.Close()
	if _, _, err := service.Open(context.Background(), "second", offer); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("second Open() error = %v", err)
	}
	if err := service.Close(sessionID); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(sessionID); err != nil {
		t.Fatalf("idempotent Close() error = %v", err)
	}
	attached, detached := source.counts()
	if attached != 1 || detached != 1 {
		t.Fatalf("source lifecycle = attach %d detach %d", attached, detached)
	}
}

func TestICEClosedStateReleasesSession(t *testing.T) {
	source := &fakeSource{emitVideo: true}
	service, controller, sessionID, _ := openService(t, source, false)
	defer controller.Close()
	current, err := service.current(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.pc.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := service.current(sessionID); errors.Is(err, ErrSessionNotFound) {
			_, detached := source.counts()
			if detached != 1 {
				t.Fatalf("detach count = %d", detached)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("closed ICE state did not release the session")
}

func TestKeyframeStartupTimeoutCleansUp(t *testing.T) {
	source := &fakeSource{}
	_, offer := controllerOffer(t, false)
	service, err := NewService(source, Options{
		KeyframeTimeout: 20 * time.Millisecond, GatherTimeout: time.Second,
		NewSessionID: func() (string, error) { return "timeout", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Open(context.Background(), "camera-stream", offer); !errors.Is(err, ErrKeyframeTimeout) {
		t.Fatalf("Open() error = %v", err)
	}
	attached, detached := source.counts()
	if attached != 1 || detached != 1 {
		t.Fatalf("timeout lifecycle = attach %d detach %d", attached, detached)
	}
}

func TestHandlerOperationsAndStrictInputLimits(t *testing.T) {
	source := &fakeSource{emitVideo: true}
	service, err := NewService(source, Options{
		KeyframeTimeout: time.Second, GatherTimeout: 5 * time.Second,
		NewSessionID: func() (string, error) { return "handler-session", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	controller, offer := controllerOffer(t, false)
	defer controller.Close()
	handler := service.Handler()

	open := request{Operation: "open", StreamID: "camera-stream", SDP: offer}
	openRecorder := postJSON(t, handler, open)
	if openRecorder.Code != http.StatusOK || !strings.Contains(openRecorder.Body.String(), `"sessionId":"handler-session"`) {
		t.Fatalf("open response = %d %s", openRecorder.Code, openRecorder.Body.String())
	}
	var opened response
	if err := json.Unmarshal(openRecorder.Body.Bytes(), &opened); err != nil {
		t.Fatal(err)
	}
	if err := controller.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: opened.SDP}); err != nil {
		t.Fatal(err)
	}
	reoffer, err := controller.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.SetLocalDescription(reoffer); err != nil {
		t.Fatal(err)
	}
	reofferResponse := postJSON(t, handler, request{
		Operation: "reoffer", SessionID: "handler-session", SDP: controller.LocalDescription().SDP,
	})
	if reofferResponse.Code != http.StatusOK || !strings.Contains(reofferResponse.Body.String(), `"sessionId":"handler-session"`) {
		t.Fatalf("reoffer response = %d %s", reofferResponse.Code, reofferResponse.Body.String())
	}
	hostCandidate := "candidate:1 1 udp 2130706431 192.0.2.1 5000 typ host"
	addICE := postJSON(t, handler, request{
		Operation: "addIce", SessionID: "handler-session",
		Candidate: &webrtc.ICECandidateInit{Candidate: hostCandidate},
	})
	if addICE.Code != http.StatusOK {
		t.Fatalf("addIce response = %d %s", addICE.Code, addICE.Body.String())
	}
	second := postJSON(t, handler, open)
	if second.Code != http.StatusConflict {
		t.Fatalf("second open status = %d", second.Code)
	}
	closeResponse := postJSON(t, handler, request{Operation: "close", SessionID: "handler-session"})
	if closeResponse.Code != http.StatusOK || !strings.Contains(closeResponse.Body.String(), `"closed":true`) {
		t.Fatalf("close response = %d %s", closeResponse.Code, closeResponse.Body.String())
	}
	if repeated := postJSON(t, handler, request{Operation: "close", SessionID: "handler-session"}); repeated.Code != http.StatusOK {
		t.Fatalf("repeated close status = %d", repeated.Code)
	}

	for name, raw := range map[string]string{
		"unknown field":    `{"operation":"close","sessionId":"x","token":"secret"}`,
		"relay candidate":  `{"operation":"addIce","sessionId":"x","candidate":{"candidate":"candidate:1 1 UDP 1 192.0.2.1 5000 typ relay"}}`,
		"oversize session": `{"operation":"close","sessionId":"` + strings.Repeat("x", maxSessionIDBytes+1) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/matter/webrtc", strings.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusBadRequest || strings.Contains(recorder.Body.String(), "secret") {
				t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
			}
		})
	}
	method := httptest.NewRecorder()
	handler.ServeHTTP(method, httptest.NewRequest(http.MethodGet, "/api/matter/webrtc", nil))
	if method.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d", method.Code)
	}
}

func postJSON(t *testing.T, handler http.Handler, input request) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/matter/webrtc", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestValidateOfferRejectsUnconstrainedVideo(t *testing.T) {
	for _, offer := range []string{
		"",
		"v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=rtpmap:96 VP8/90000\r\n",
		"v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=rtpmap:96 H264/90000\r\na=fmtp:96 packetization-mode=0\r\n",
		"v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96 97\r\na=rtpmap:96 H264/90000\r\na=fmtp:97 packetization-mode=1\r\n",
	} {
		if _, err := validateOffer(offer); err == nil {
			t.Fatalf("validateOffer(%q) accepted unsupported SDP", offer)
		}
	}
}

func TestStreamsSourceReportsMissingStream(t *testing.T) {
	if _, err := (streamsSource{}).Attach("matter-webrtc-missing-stream", nil); !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("Attach() error = %v", err)
	}
}
