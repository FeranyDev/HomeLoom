package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmedia "github.com/feranydev/homeloom/backend/internal/domain/media"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/labstack/echo/v4"
)

type mediaRouteStore struct {
	mu         sync.Mutex
	generation uint64
	revision   uint64
	streams    map[string]domainmedia.StreamSpec
}

type mediaRoundTripFunc func(*http.Request) (*http.Response, error)

func (f mediaRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func newMediaRouteStore() *mediaRouteStore {
	return &mediaRouteStore{generation: 1, revision: 1, streams: make(map[string]domainmedia.StreamSpec)}
}

func (s *mediaRouteStore) ListMediaStreams(context.Context) ([]domainmedia.StreamSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]domainmedia.StreamSpec, 0, len(s.streams))
	for _, item := range s.streams {
		item.Options = append(json.RawMessage(nil), item.Options...)
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (s *mediaRouteStore) SaveMediaStream(_ context.Context, item domainmedia.StreamSpec) (application.MediaConfigVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	item.Options = append(json.RawMessage(nil), item.Options...)
	s.streams[item.ID] = item
	return application.MediaConfigVersion{Generation: s.generation, Revision: s.revision}, nil
}

func (s *mediaRouteStore) DeleteMediaStream(_ context.Context, id string) (domainmedia.StreamSpec, application.MediaConfigVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.streams[id]
	if !ok {
		return domainmedia.StreamSpec{}, application.MediaConfigVersion{}, application.ErrMediaStreamNotFound
	}
	delete(s.streams, id)
	s.revision++
	return item, application.MediaConfigVersion{Generation: s.generation, Revision: s.revision}, nil
}

func (s *mediaRouteStore) MediaStreamReplay(ctx context.Context) (domainmedia.StreamReplay, error) {
	items, err := s.ListMediaStreams(ctx)
	if err != nil {
		return domainmedia.StreamReplay{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return domainmedia.StreamReplay{
		SchemaVersion: domainmedia.SchemaVersion,
		Generation:    s.generation,
		Revision:      s.revision,
		Streams:       items,
	}, nil
}

func mediaRouteSpec(id string) domainmedia.StreamSpec {
	return domainmedia.StreamSpec{
		SchemaVersion: domainmedia.SchemaVersion,
		ID:            id,
		DeviceID:      "camera-1",
		Protocol:      domainmedia.ProtocolRTSP,
		CredentialRef: "credential-1",
		Profile:       "main",
		Mode:          domainmedia.StreamOnDemand,
		Audio:         true,
		Options:       json.RawMessage(`{"transport":"tcp"}`),
	}
}

func serveMediaRequest(t *testing.T, server *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload *bytes.Reader
	if body == nil {
		payload = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, payload)
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func TestMediaStreamRoutesCRUDAndCredentialRoundTrip(t *testing.T) {
	server := newTestServer()
	store := newMediaRouteStore()
	server.SetMediaService(application.NewMediaService(store, nil))
	health := serveMediaRequest(t, server, http.MethodGet, "/api/v1/media/health", nil)
	if health.Code != http.StatusOK || !bytes.Contains(health.Body.Bytes(), []byte(`"status":"disabled"`)) {
		t.Fatalf("health = %d %s", health.Code, health.Body.String())
	}

	spec := mediaRouteSpec("stream-1")
	response := serveMediaRequest(t, server, http.MethodPost, "/api/v1/media/streams", spec)
	if response.Code != http.StatusCreated || !bytes.Contains(response.Body.Bytes(), []byte(`"credentialRef":"credential-1"`)) {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}

	spec.ID = "ignored-by-path"
	spec.Mode = domainmedia.StreamAlwaysOn
	response = serveMediaRequest(t, server, http.MethodPut, "/api/v1/media/streams/stream-1", spec)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"mode":"always_on"`)) {
		t.Fatalf("update = %d %s", response.Code, response.Body.String())
	}

	response = serveMediaRequest(t, server, http.MethodGet, "/api/v1/media/streams", nil)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"id":"stream-1"`)) {
		t.Fatalf("list = %d %s", response.Code, response.Body.String())
	}

	response = serveMediaRequest(t, server, http.MethodDelete, "/api/v1/media/streams/stream-1", nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", response.Code, response.Body.String())
	}
	response = serveMediaRequest(t, server, http.MethodDelete, "/api/v1/media/streams/stream-1", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing delete = %d %s", response.Code, response.Body.String())
	}
}

func TestMediaStreamRoutesRejectInvalidAndDuplicateSpecs(t *testing.T) {
	server := newTestServer()
	server.SetMediaService(application.NewMediaService(newMediaRouteStore(), nil))

	spec := mediaRouteSpec("stream-1")
	response := serveMediaRequest(t, server, http.MethodPost, "/api/v1/media/streams", spec)
	if response.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	response = serveMediaRequest(t, server, http.MethodPost, "/api/v1/media/streams", spec)
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d %s", response.Code, response.Body.String())
	}

	spec.ID = "stream-2"
	spec.Options = json.RawMessage(`{"password":"secret-canary"}`)
	response = serveMediaRequest(t, server, http.MethodPost, "/api/v1/media/streams", spec)
	if response.Code != http.StatusBadRequest || bytes.Contains(response.Body.Bytes(), []byte("secret-canary")) {
		t.Fatalf("invalid secret = %d %s", response.Code, response.Body.String())
	}
}

func TestMediaRoutesUnavailableWithoutService(t *testing.T) {
	response := serveMediaRequest(t, newTestServer(), http.MethodGet, "/api/v1/media/streams", nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable = %d %s", response.Code, response.Body.String())
	}
}

func TestMediaPreviewProxiesEnabledDeviceStreamWithoutExposingPublisher(t *testing.T) {
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{
		ID: "camera-provider", Name: "Camera",
		Config: []byte(`{"devices":[{"id":"camera-1","name":"Camera","type":"camera","availability":"unknown"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServerWithProvider(provider)
	store := newMediaRouteStore()
	store.streams["stream-1"] = mediaRouteSpec("stream-1")
	server.SetMediaService(application.NewMediaService(store, nil))
	server.SetMediaPreview(t.TempDir())
	const previewPayload = "ftyp-moov-moof-mdat-preview"
	server.mediaPreview.Transport = mediaRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() != "stream-1" || request.URL.Query().Get("src") != "stream-1" ||
			request.URL.Query().Get("video") != "h264" || request.URL.Query().Has("audio") ||
			request.URL.Query().Has("duration") {
			t.Fatalf("preview upstream request = %s", request.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{echo.HeaderContentType: []string{"video/mp4; codecs=\"avc1.640029\""}},
			Body:       io.NopCloser(bytes.NewBufferString(previewPayload)),
		}, nil
	})

	response := serveMediaRequest(t, server, http.MethodGet, "/api/v1/media/devices/camera-1/preview.mp4", nil)
	if response.Code != http.StatusOK || response.Body.String() != previewPayload ||
		!strings.HasPrefix(response.Header().Get(echo.HeaderContentType), "video/mp4") {
		t.Fatalf("preview = %d %s %#v", response.Code, response.Body.String(), response.Header())
	}
	items, err := server.devices.List(context.Background())
	if err != nil || len(items) != 1 || items[0].EffectiveAvailability() != device.AvailabilityOnline {
		t.Fatalf("device availability after preview = %#v, %v", items, err)
	}
}

func TestMediaPreviewTimesOutWhenPublisherNeverProducesAKeyframe(t *testing.T) {
	server := newTestServer()
	store := newMediaRouteStore()
	store.streams["stream-1"] = mediaRouteSpec("stream-1")
	server.SetMediaService(application.NewMediaService(store, nil))
	server.SetMediaPreview(t.TempDir())
	server.mediaPreviewStartupTimeout = 10 * time.Millisecond
	reader, _ := io.Pipe()
	server.mediaPreview.Transport = mediaRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{echo.HeaderContentType: []string{"video/mp4"}},
			Body:       reader,
		}, nil
	})

	response := serveMediaRequest(t, server, http.MethodGet, "/api/v1/media/devices/camera-1/preview.mp4", nil)
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("keyframe timeout = %d %s", response.Code, response.Body.String())
	}
}

func TestMediaPreviewRejectsMissingAndInvalidUpstreamStreams(t *testing.T) {
	server := newTestServer()
	store := newMediaRouteStore()
	store.streams["stream-1"] = mediaRouteSpec("stream-1")
	server.SetMediaService(application.NewMediaService(store, nil))
	server.SetMediaPreview(t.TempDir())

	missing := serveMediaRequest(t, server, http.MethodGet, "/api/v1/media/devices/other-camera/preview.mp4", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing preview = %d %s", missing.Code, missing.Body.String())
	}
	server.mediaPreview.Transport = mediaRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString("upstream-secret-canary")),
		}, nil
	})
	rejected := serveMediaRequest(t, server, http.MethodGet, "/api/v1/media/devices/camera-1/preview.mp4", nil)
	if rejected.Code != http.StatusBadGateway || bytes.Contains(rejected.Body.Bytes(), []byte("upstream-secret-canary")) {
		t.Fatalf("rejected preview = %d %s", rejected.Code, rejected.Body.String())
	}
}
