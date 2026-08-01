package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	domainmedia "github.com/feranydev/homeloom/backend/internal/domain/media"
	mattertarget "github.com/feranydev/homeloom/backend/internal/targets/matter"
)

type cameraPublicationStore struct {
	mu       sync.Mutex
	revision uint64
	streams  map[string]domainmedia.StreamSpec
}

func (s *cameraPublicationStore) ListMediaStreams(context.Context) ([]domainmedia.StreamSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]domainmedia.StreamSpec, 0, len(s.streams))
	for _, item := range s.streams {
		item.Options = append(json.RawMessage(nil), item.Options...)
		items = append(items, item)
	}
	return items, nil
}

func (s *cameraPublicationStore) SaveMediaStream(_ context.Context, item domainmedia.StreamSpec) (application.MediaConfigVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	item.Options = append(json.RawMessage(nil), item.Options...)
	s.streams[item.ID] = item
	return application.MediaConfigVersion{Generation: 1, Revision: s.revision}, nil
}

func (s *cameraPublicationStore) DeleteMediaStream(context.Context, string) (domainmedia.StreamSpec, application.MediaConfigVersion, error) {
	return domainmedia.StreamSpec{}, application.MediaConfigVersion{}, errors.New("not implemented")
}

func (s *cameraPublicationStore) MediaStreamReplay(context.Context) (domainmedia.StreamReplay, error) {
	return domainmedia.StreamReplay{}, errors.New("not implemented")
}

type cameraPublicationRuntime struct {
	err       error
	mutations []domainmedia.StreamMutation
}

func (r *cameraPublicationRuntime) PublishMediaStreamMutation(_ context.Context, item domainmedia.StreamMutation) error {
	r.mutations = append(r.mutations, item)
	return r.err
}

func cameraPublicationStream(deviceID string, protocol domainmedia.Protocol) domainmedia.StreamSpec {
	return domainmedia.StreamSpec{
		SchemaVersion: domainmedia.SchemaVersion,
		ID:            defaultCameraStreamID(deviceID),
		DeviceID:      deviceID,
		Protocol:      protocol,
		CredentialRef: "camera-credential",
		Profile:       "main",
		Mode:          domainmedia.StreamOnDemand,
		Audio:         true,
		Options:       json.RawMessage(`{"publisher":"none"}`),
	}
}

func publisherOption(t *testing.T, store *cameraPublicationStore, streamID string) string {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	var options struct {
		Publisher string `json:"publisher"`
	}
	if err := json.Unmarshal(store.streams[streamID].Options, &options); err != nil {
		t.Fatalf("decode publisher options: %v", err)
	}
	return options.Publisher
}

func TestCameraTargetPublicationEnablesAndDisablesIndependentPublisher(t *testing.T) {
	ctx := context.Background()
	deviceID := "xiaomi-camera-1"
	stream := cameraPublicationStream(deviceID, domainmedia.ProtocolXiaomiMISS)
	store := &cameraPublicationStore{revision: 1, streams: map[string]domainmedia.StreamSpec{stream.ID: stream}}
	runtime := &cameraPublicationRuntime{}
	runtimeDir := t.TempDir()
	streamDir := filepath.Join(runtimeDir, stream.ID)
	if err := os.MkdirAll(streamDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(streamDir, "homekit-identity.json"), []byte(`{"schemaVersion":1,"pin":"123-45-679"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(streamDir, "go2rtc.yaml"), []byte("homekit:\n  \""+stream.ID+"\":\n    pairings: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	publication := newCameraTargetPublication(application.NewMediaService(store, runtime), nil, runtimeDir, 51826)

	pairing, paired, address, err := publication.EnableHomeKitCamera(ctx, "camera-homekit-1", deviceID, "客厅摄像头")
	if err != nil {
		t.Fatalf("EnableHomeKitCamera() error = %v", err)
	}
	if pairing.Code != "123-45-679" || pairing.SetupID == "" || !strings.HasPrefix(pairing.SetupURI, "X-HM://") || len(pairing.QR) == 0 || paired || pairing.Devices[0] != deviceID || address == "" {
		t.Fatalf("pairing = %#v, paired = %v, address = %q", pairing, paired, address)
	}
	if got := publisherOption(t, store, stream.ID); got != "apple-home" {
		t.Fatalf("enabled publisher = %q", got)
	}
	if err := publication.DisableHomeKitCamera(ctx, "camera-homekit-1", deviceID); err != nil {
		t.Fatalf("DisableHomeKitCamera() error = %v", err)
	}
	if got := publisherOption(t, store, stream.ID); got != "none" {
		t.Fatalf("disabled publisher = %q", got)
	}
}

func TestCameraTargetPublicationReadsStructuredPairingState(t *testing.T) {
	deviceID := "xiaomi-camera-1"
	streamID := defaultCameraStreamID(deviceID)
	streamDir := filepath.Join(t.TempDir(), streamID)
	if err := os.MkdirAll(streamDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(streamDir, "homekit-identity.json"), []byte(`{"schemaVersion":1,"pin":"123-45-679"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config := "homekit:\n  \"" + streamID + "\":\n    pairings:\n      - client_id=controller&client_public=0011&permissions=1\n"
	if err := os.WriteFile(filepath.Join(streamDir, "go2rtc.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	publication := newCameraTargetPublication(nil, nil, filepath.Dir(streamDir), 51826)

	pairing, paired, _ := publication.InspectHomeKitCamera(deviceID)
	if !paired {
		t.Fatal("structured Camera Kernel pairing was not detected")
	}
	if pairing.SetupID == "" || !strings.HasPrefix(pairing.SetupURI, "X-HM://") || len(pairing.QR) == 0 {
		t.Fatalf("camera pairing QR was not reconstructed: %#v", pairing)
	}
}

func TestCameraTargetPublicationAcceptsAllScopedInputProtocols(t *testing.T) {
	for _, protocol := range []domainmedia.Protocol{
		domainmedia.ProtocolRTSP, domainmedia.ProtocolONVIF, domainmedia.ProtocolXiaomiMISS,
	} {
		deviceID := string(protocol) + "-camera"
		stream := cameraPublicationStream(deviceID, protocol)
		store := &cameraPublicationStore{revision: 1, streams: map[string]domainmedia.StreamSpec{stream.ID: stream}}
		publication := newCameraTargetPublication(application.NewMediaService(store, &cameraPublicationRuntime{}), nil, t.TempDir(), 51826)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, _, err := publication.EnableHomeKitCamera(ctx, "camera-homekit-1", stream.DeviceID, string(protocol)); err != nil {
			t.Fatalf("EnableHomeKitCamera(%q) = %v", protocol, err)
		}
		if got := publisherOption(t, store, stream.ID); got != "apple-home" {
			t.Fatalf("%q publisher = %q", protocol, got)
		}
	}
}

func TestCameraTargetPublicationRollsBackFailedWorkerUpdate(t *testing.T) {
	stream := cameraPublicationStream("xiaomi-camera-1", domainmedia.ProtocolXiaomiMISS)
	store := &cameraPublicationStore{revision: 1, streams: map[string]domainmedia.StreamSpec{stream.ID: stream}}
	publication := newCameraTargetPublication(application.NewMediaService(store, &cameraPublicationRuntime{err: errors.New("worker offline")}), nil, t.TempDir(), 51826)

	if _, _, _, err := publication.EnableHomeKitCamera(context.Background(), "camera-homekit-1", stream.DeviceID, "客厅"); err == nil {
		t.Fatal("EnableHomeKitCamera() succeeded with failed runtime delivery")
	}
	if got := publisherOption(t, store, stream.ID); got != "none" {
		t.Fatalf("rollback publisher = %q", got)
	}
}

func TestCameraTargetPublicationUsesAllocatedPublisherEndpoint(t *testing.T) {
	runtimeDir := t.TempDir()
	streamID := "camera-collision"
	streamDir := filepath.Join(runtimeDir, streamID)
	if err := os.MkdirAll(streamDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(streamDir, "publisher-endpoint.json"),
		[]byte(`{"schemaVersion":1,"hapPort":52999}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	publication := newCameraTargetPublication(nil, nil, runtimeDir, 51826)
	if address := publication.address(streamID); address != ":52999" {
		t.Fatalf("allocated publisher address = %q", address)
	}
}

func TestCameraTargetPublicationRelaysMatterMediaOverPrivateUnixSocket(t *testing.T) {
	runtimeDir, err := os.MkdirTemp("/tmp", "homeloom-matter-media-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	streamID := "camera-matter-test"
	streamDir := filepath.Join(runtimeDir, streamID)
	if err := os.MkdirAll(streamDir, 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(streamDir, "media.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/matter/webrtc":
			var input struct {
				Operation string `json:"operation"`
				StreamID  string `json:"streamId"`
				SDP       string `json:"sdp"`
			}
			if request.Method != http.MethodPost || json.NewDecoder(request.Body).Decode(&input) != nil ||
				input.Operation != "open" || input.StreamID != streamID || input.SDP != "offer" {
				http.Error(response, "bad request", http.StatusBadRequest)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"sessionId":"session-1","sdp":"answer"}`))
		case "/api/frame.jpeg":
			if request.URL.Query().Get("src") != streamID ||
				request.URL.Query().Get("width") != "640" || request.URL.Query().Get("height") != "360" {
				http.Error(response, "bad request", http.StatusBadRequest)
				return
			}
			response.Header().Set("Content-Type", "image/jpeg")
			_, _ = response.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
		default:
			http.NotFound(response, request)
		}
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		<-done
	})

	publication := newCameraTargetPublication(nil, nil, runtimeDir, 51826)
	webrtc, err := publication.WebRTC(context.Background(), streamID, mattertarget.CameraWebRTCRequest{
		Operation: "open",
		SDP:       "offer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if webrtc.SessionID != "session-1" || webrtc.SDP != "answer" {
		t.Fatalf("WebRTC response = %#v", webrtc)
	}
	snapshot, err := publication.Snapshot(context.Background(), streamID, 640, 360)
	if err != nil {
		t.Fatal(err)
	}
	if string(snapshot) != string([]byte{0xff, 0xd8, 0xff, 0xd9}) {
		t.Fatalf("snapshot = %x", snapshot)
	}
}

func TestCameraTargetPublicationRejectsMatterMediaPathEscape(t *testing.T) {
	publication := newCameraTargetPublication(nil, nil, t.TempDir(), 51826)
	_, err := publication.WebRTC(context.Background(), "../camera-other", mattertarget.CameraWebRTCRequest{
		Operation: "open",
		SDP:       "offer",
	})
	if err == nil {
		t.Fatal("WebRTC accepted a stream path escape")
	}
}
