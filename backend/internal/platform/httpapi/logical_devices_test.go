package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/logicaldevice"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"go.uber.org/zap"
)

type logicalHTTPStore struct {
	items map[string]logicaldevice.Config
}

func (s *logicalHTTPStore) ListLogicalDevices(context.Context) ([]logicaldevice.Config, error) {
	result := make([]logicaldevice.Config, 0, len(s.items))
	for _, item := range s.items {
		result = append(result, item.Clone())
	}
	return result, nil
}
func (s *logicalHTTPStore) SaveLogicalDevice(_ context.Context, item logicaldevice.Config) error {
	if s.items == nil {
		s.items = map[string]logicaldevice.Config{}
	}
	s.items[item.ID] = item.Clone()
	return nil
}
func (s *logicalHTTPStore) DeleteLogicalDevice(_ context.Context, id string) error {
	if _, exists := s.items[id]; !exists {
		return application.ErrLogicalDeviceNotFound
	}
	delete(s.items, id)
	return nil
}

type logicalHTTPRuntime struct {
	items        []logicaldevice.Config
	candidates   []logicaldevice.MatchCandidate
	explanations map[string][]logicaldevice.RouteExplanation
}

func (r *logicalHTTPRuntime) SetLogicalDevices(items []logicaldevice.Config) error {
	r.items = append([]logicaldevice.Config(nil), items...)
	return nil
}
func (r *logicalHTTPRuntime) LogicalDevices() []logicaldevice.Config {
	return append([]logicaldevice.Config(nil), r.items...)
}
func (r *logicalHTTPRuntime) LogicalDeviceCandidates(context.Context) ([]logicaldevice.MatchCandidate, error) {
	return append([]logicaldevice.MatchCandidate(nil), r.candidates...), nil
}
func (r *logicalHTTPRuntime) LogicalDeviceExplanations(id string) ([]logicaldevice.RouteExplanation, bool) {
	items, exists := r.explanations[id]
	return append([]logicaldevice.RouteExplanation(nil), items...), exists
}

func TestLogicalDeviceHTTPCRUDAndCandidates(t *testing.T) {
	store := &logicalHTTPStore{items: map[string]logicaldevice.Config{}}
	runtime := &logicalHTTPRuntime{
		candidates: []logicaldevice.MatchCandidate{{
			Left:    logicaldevice.CandidateStatus{SourceRef: logicaldevice.SourceRef{ProviderID: "local", DeviceID: "light-1"}, Name: "客厅主灯", Type: device.TypeSwitch, HomeID: "home-main"},
			Right:   logicaldevice.CandidateStatus{SourceRef: logicaldevice.SourceRef{ProviderID: "cloud", DeviceID: "light-2"}, Name: "客厅主灯", Type: device.TypeSwitch, HomeID: "home-main"},
			Reasons: []string{"same_type", "same_normalized_name", "same_source_home"},
		}},
		explanations: map[string][]logicaldevice.RouteExplanation{
			"living-light": []logicaldevice.RouteExplanation{{LogicalDeviceID: "living-light", Kind: "property", Path: "main\x00switch\x00power", Reason: "provider_priority"}},
		},
	}
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	server := NewServer(":0", devices, application.NewTargetService(nil, nil), zap.NewNop())
	server.SetLogicalDeviceService(application.NewLogicalDeviceService(store, runtime, nil))
	item := logicaldevice.Config{ID: "living-light", Name: "客厅主灯", Type: device.TypeSwitch, Bindings: []logicaldevice.Binding{{SourceRef: logicaldevice.SourceRef{ProviderID: "local", DeviceID: "light-1"}}, {SourceRef: logicaldevice.SourceRef{ProviderID: "cloud", DeviceID: "light-2"}, Priority: 10}}}
	payload, _ := json.Marshal(item)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/logical-devices", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(runtime.items) != 1 || runtime.items[0].ID != item.ID {
		t.Fatalf("runtime items=%#v", runtime.items)
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/logical-devices/candidates", nil))
	if recorder.Code != http.StatusOK || !containsJSON(recorder.Body.Bytes(), "same_source_home") {
		t.Fatalf("candidates status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/logical-devices/living-light/explanations", nil))
	if recorder.Code != http.StatusOK || !containsJSON(recorder.Body.Bytes(), "provider_priority") {
		t.Fatalf("explanations status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/logical-devices/living-light", nil))
	if recorder.Code != http.StatusNoContent || len(runtime.items) != 0 {
		t.Fatalf("delete status=%d items=%#v", recorder.Code, runtime.items)
	}
}

func containsJSON(payload []byte, value string) bool { return strings.Contains(string(payload), value) }
