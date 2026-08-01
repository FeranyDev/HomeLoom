package homekit

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/brutella/hap"
	"github.com/brutella/hap/rtp"
	"github.com/brutella/hap/tlv8"
)

type cameraMediaStub struct {
	mu sync.Mutex

	snapshotDevice string
	snapshotWidth  int
	snapshotHeight int
	snapshot       []byte
	snapshotErr    error

	prepareDevice string
	prepare       rtp.SetupEndpoints
	prepareErr    error

	streamDevice string
	stream       rtp.StreamConfiguration
	streamErr    error
}

func (s *cameraMediaStub) Snapshot(_ context.Context, deviceID string, width, height int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshotDevice, s.snapshotWidth, s.snapshotHeight = deviceID, width, height
	return append([]byte(nil), s.snapshot...), s.snapshotErr
}

func (s *cameraMediaStub) PrepareStream(_ context.Context, deviceID string, item rtp.SetupEndpoints) (rtp.SetupEndpointsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prepareDevice, s.prepare = deviceID, item
	if s.prepareErr != nil {
		return rtp.SetupEndpointsResponse{}, s.prepareErr
	}
	return rtp.SetupEndpointsResponse{
		Status: rtp.SessionStatusSuccess,
		AccessoryAddr: rtp.Addr{
			IPVersion: rtp.IPAddrVersionv4, IPAddr: "192.0.2.20",
			VideoRtpPort: 5010, AudioRtpPort: 5011,
		},
		Video: item.Video, Audio: item.Audio, SsrcVideo: 11, SsrcAudio: 12,
	}, nil
}

func (s *cameraMediaStub) SetStream(_ context.Context, deviceID string, item rtp.StreamConfiguration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streamDevice, s.stream = deviceID, item
	return s.streamErr
}

func testCryptoSuite() rtp.CryptoSuite {
	return rtp.CryptoSuite{
		Type:       rtp.CryptoSuite_AES_CM_128_HMAC_SHA1_80,
		MasterKey:  bytes.Repeat([]byte{1}, 16),
		MasterSalt: bytes.Repeat([]byte{2}, 14),
	}
}

func testSetupEndpoints() rtp.SetupEndpoints {
	return rtp.SetupEndpoints{
		SessionId: bytes.Repeat([]byte{3}, 16),
		ControllerAddr: rtp.Addr{
			IPVersion: rtp.IPAddrVersionv4, IPAddr: "192.0.2.10",
			VideoRtpPort: 5000, AudioRtpPort: 5001,
		},
		Video: testCryptoSuite(),
		Audio: testCryptoSuite(),
	}
}

func TestStandaloneCameraPublisherConfiguresRTPAndStableIdentity(t *testing.T) {
	store := hap.NewMemStore()
	media := &cameraMediaStub{snapshot: []byte{0xff, 0xd8, 0xff, 0xd9}}
	config := CameraPublisherConfig{
		ID: "camera-publisher-1", DeviceID: "camera-1", Name: "Camera",
		Address: "127.0.0.1:0", Pin: "12345678", SetupID: "CAM1", Store: store,
	}
	publisher, err := NewCameraPublisher(config, media, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if publisher.camera.A.Id != 1 || publisher.camera.A.Type != 17 {
		t.Fatalf("standalone camera identity = aid %d type %d", publisher.camera.A.Id, publisher.camera.A.Type)
	}
	if publisher.PairingInfo().Code != "123-45-678" ||
		len(publisher.PairingInfo().QR) == 0 ||
		len(publisher.PairingInfo().Devices) != 1 ||
		publisher.PairingInfo().Devices[0] != "camera-1" {
		t.Fatalf("camera pairing info = %#v", publisher.PairingInfo())
	}
	firstUUID, err := store.Get("uuid")
	if err != nil || len(firstUUID) == 0 {
		t.Fatalf("first camera identity UUID = %q, %v", firstUUID, err)
	}
	if _, err := NewCameraPublisher(config, media, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	secondUUID, err := store.Get("uuid")
	if err != nil || !bytes.Equal(firstUUID, secondUUID) {
		t.Fatalf("camera identity UUID changed: %q -> %q, %v", firstUUID, secondUUID, err)
	}

	management := publisher.camera.StreamManagement1
	if len(management.SupportedVideoStreamConfiguration.Value()) == 0 ||
		len(management.SupportedAudioStreamConfiguration.Value()) == 0 ||
		len(management.SupportedRTPConfiguration.Value()) == 0 {
		t.Fatal("camera supported RTP capabilities are empty")
	}
	if len(publisher.camera.StreamManagement2.SupportedVideoStreamConfiguration.Value()) == 0 {
		t.Fatal("camera second RTP management service is not configured")
	}
	assertStreamingStatus(t, management.StreamingStatus.Value(), rtp.StreamingStatusAvailable)

	setup := testSetupEndpoints()
	setupPayload, err := tlv8.Marshal(setup)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/characteristics", nil)
	response, status := management.SetupEndpoints.SetValueRequest(
		base64.StdEncoding.EncodeToString(setupPayload),
		request,
	)
	if status != hap.JsonStatusSuccess {
		t.Fatalf("SetupEndpoints status = %d", status)
	}
	encodedResponse, ok := response.(string)
	if !ok {
		t.Fatalf("SetupEndpoints response = %#v", response)
	}
	responsePayload, err := base64.StdEncoding.DecodeString(encodedResponse)
	if err != nil {
		t.Fatal(err)
	}
	var prepared rtp.SetupEndpointsResponse
	if err := tlv8.Unmarshal(responsePayload, &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.Status != rtp.SessionStatusSuccess ||
		!bytes.Equal(prepared.SessionId, setup.SessionId) ||
		media.prepareDevice != "camera-1" {
		t.Fatalf("prepared RTP session = %#v, device %q", prepared, media.prepareDevice)
	}

	start := rtp.StreamConfiguration{
		Command: rtp.SessionControlCommand{
			Identifier: append([]byte(nil), setup.SessionId...),
			Type:       rtp.SessionControlCommandTypeStart,
		},
	}
	startPayload, err := tlv8.Marshal(start)
	if err != nil {
		t.Fatal(err)
	}
	if _, status := management.SelectedRTPStreamConfiguration.SetValueRequest(
		base64.StdEncoding.EncodeToString(startPayload),
		request,
	); status != hap.JsonStatusSuccess {
		t.Fatalf("start stream status = %d", status)
	}
	if media.streamDevice != "camera-1" || media.stream.Command.Type != rtp.SessionControlCommandTypeStart {
		t.Fatalf("forwarded stream = %#v, device %q", media.stream, media.streamDevice)
	}
	assertStreamingStatus(t, management.StreamingStatus.Value(), rtp.StreamingStatusBusy)

	start.Command.Type = rtp.SessionControlCommandTypeEnd
	endPayload, err := tlv8.Marshal(start)
	if err != nil {
		t.Fatal(err)
	}
	if _, status := management.SelectedRTPStreamConfiguration.SetValueRequest(
		base64.StdEncoding.EncodeToString(endPayload),
		request,
	); status != hap.JsonStatusSuccess {
		t.Fatalf("end stream status = %d", status)
	}
	assertStreamingStatus(t, management.StreamingStatus.Value(), rtp.StreamingStatusAvailable)
}

func TestCameraPublisherWithoutMediaIsExplicitlyUnavailable(t *testing.T) {
	publisher, err := NewCameraPublisher(CameraPublisherConfig{
		ID: "camera-publisher-unavailable", DeviceID: "camera-unavailable", Name: "Camera",
		Address: "127.0.0.1:0", Pin: "12345678", SetupID: "CAM2", Store: hap.NewMemStore(),
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	management := publisher.camera.StreamManagement1
	assertStreamingStatus(t, management.StreamingStatus.Value(), rtp.StreamingStatusUnavailable)
	setupPayload, err := tlv8.Marshal(testSetupEndpoints())
	if err != nil {
		t.Fatal(err)
	}
	if _, status := management.SetupEndpoints.SetValueRequest(
		base64.StdEncoding.EncodeToString(setupPayload),
		httptest.NewRequest(http.MethodPut, "/characteristics", nil),
	); status != hap.JsonStatusResourceBusy {
		t.Fatalf("unavailable SetupEndpoints status = %d", status)
	}
}

func TestCameraPublisherRejectsMalformedSRTPMaterialBeforeForwarding(t *testing.T) {
	media := &cameraMediaStub{}
	publisher, err := NewCameraPublisher(CameraPublisherConfig{
		ID: "camera-publisher-invalid-srtp", DeviceID: "camera-invalid-srtp", Name: "Camera",
		Address: "127.0.0.1:0", Pin: "12345678", SetupID: "CAM3", Store: hap.NewMemStore(),
	}, media, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	setup := testSetupEndpoints()
	setup.Video.MasterKey = setup.Video.MasterKey[:8]
	payload, err := tlv8.Marshal(setup)
	if err != nil {
		t.Fatal(err)
	}
	if _, status := publisher.camera.StreamManagement1.SetupEndpoints.SetValueRequest(
		base64.StdEncoding.EncodeToString(payload),
		httptest.NewRequest(http.MethodPut, "/characteristics", nil),
	); status != hap.JsonStatusInvalidValueInRequest {
		t.Fatalf("malformed SRTP status = %d", status)
	}
	if media.prepareDevice != "" {
		t.Fatalf("malformed SRTP material reached media worker for %q", media.prepareDevice)
	}
}

func TestCameraSnapshotHandlerAuthorizesValidatesAndForwards(t *testing.T) {
	media := &cameraMediaStub{snapshot: []byte{0xff, 0xd8, 0x01, 0x02, 0xff, 0xd9}}
	handler := newCameraSnapshotHandler(
		func(*http.Request) bool { return true },
		media,
		map[uint64]string{1: "camera-1"},
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/resource",
		strings.NewReader(`{"aid":1,"resource-type":"image","image-width":640,"image-height":360}`),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/jpeg" ||
		!bytes.Equal(response.Body.Bytes(), media.snapshot) {
		t.Fatalf("snapshot response = %d %v %x", response.Code, response.Header(), response.Body.Bytes())
	}
	if media.snapshotDevice != "camera-1" || media.snapshotWidth != 640 || media.snapshotHeight != 360 {
		t.Fatalf("snapshot request = %q %dx%d", media.snapshotDevice, media.snapshotWidth, media.snapshotHeight)
	}

	unauthorized := newCameraSnapshotHandler(
		func(*http.Request) bool { return false },
		media,
		map[uint64]string{1: "camera-1"},
	)
	response = httptest.NewRecorder()
	unauthorized.ServeHTTP(response, request.Clone(context.Background()))
	if response.Code != 470 {
		t.Fatalf("unauthorized snapshot status = %d", response.Code)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPost,
		"/resource",
		strings.NewReader(`{"aid":2,"resource-type":"image","image-width":640,"image-height":360}`),
	))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown camera snapshot status = %d", response.Code)
	}

	media.snapshot = []byte("not-a-jpeg")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPost,
		"/resource",
		strings.NewReader(`{"aid":1,"resource-type":"image","image-width":640,"image-height":360}`),
	))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("invalid JPEG snapshot status = %d", response.Code)
	}
}

func assertStreamingStatus(t testing.TB, payload []byte, expected byte) {
	t.Helper()
	var status rtp.StreamingStatus
	if err := tlv8.Unmarshal(payload, &status); err != nil {
		t.Fatal(err)
	}
	if status.Status != expected {
		t.Fatalf("streaming status = %d, want %d", status.Status, expected)
	}
}
