package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
	"github.com/labstack/echo/v4"
)

type apiProviderStore struct {
	items map[string]providerconfig.Config
}

func (s *apiProviderStore) ListProviders(context.Context) ([]providerconfig.Config, error) {
	return nil, nil
}
func (s *apiProviderStore) SaveProvider(_ context.Context, item providerconfig.Config) error {
	s.items[item.ID] = item
	return nil
}
func (s *apiProviderStore) DeleteProvider(_ context.Context, id string) error {
	delete(s.items, id)
	return nil
}

func newTestServer() *Server {
	return newTestServerWithProvider(virtual.NewProvider())
}

func newTestServerWithProvider(provider providersdk.Provider) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	targets := application.NewTargetService([]application.TargetRegistration{{
		Info: application.TargetInfo{ID: "apple-main", Type: "apple-hap", Name: "Main", Enabled: true, Status: "running"},
		QR:   []byte("png-data"),
	}}, nil)
	return NewServer(":0", application.NewDeviceService(provider), targets, logger)
}

func TestListTargetsAndPairingQR(t *testing.T) {
	server := newTestServer()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/targets", nil)
	listResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !bytes.Contains(listResponse.Body.Bytes(), []byte(`"id":"apple-main"`)) {
		t.Fatalf("target response = %d %s", listResponse.Code, listResponse.Body.String())
	}

	qrRequest := httptest.NewRequest(http.MethodGet, "/api/v1/targets/apple-main/pairing-qr", nil)
	qrResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(qrResponse, qrRequest)
	if qrResponse.Code != http.StatusOK || qrResponse.Header().Get(echo.HeaderContentType) != "image/png" {
		t.Fatalf("QR response = %d %q", qrResponse.Code, qrResponse.Header().Get(echo.HeaderContentType))
	}
	if qrResponse.Body.String() != "png-data" {
		t.Fatalf("QR body = %q", qrResponse.Body.String())
	}
}

func TestVersionEndpoint(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/system/version", nil)
	response := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(response, request)
	for _, expected := range []string{`"version":"dev"`, `"commit":"unknown"`, `"goVersion":"go`} {
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
	}
}

func TestErrorsUseStableEnvelopeAndRequestID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/commands/missing", nil)
	request.Header.Set(echo.HeaderXRequestID, "request-123")
	response := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(response, request)
	for _, expected := range []string{`"code":"not_found"`, `"message":"command not found"`, `"requestId":"request-123"`} {
		if response.Code != http.StatusNotFound || response.Header().Get(echo.HeaderXRequestID) != "request-123" || !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Fatalf("response = %d %s, request id = %q", response.Code, response.Body.String(), response.Header().Get(echo.HeaderXRequestID))
		}
	}
}

func TestListDevices(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	response := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data) != 2 {
		t.Fatalf("device count = %d, want 2", len(body.Data))
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"schemaVersion":1`)) || bytes.Contains(response.Body.Bytes(), []byte(`"state":`)) {
		t.Fatalf("device response does not follow schema v1: %s", response.Body.String())
	}
}

func TestListProviders(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	response := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"id":"virtual-main"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"propertyWrite":true`)) {
		t.Fatalf("provider response = %d %s", response.Code, response.Body.String())
	}
}

func TestDiagnosticsAndPrometheusMetrics(t *testing.T) {
	server := newTestServer()
	for _, path := range []string{"/api/v1/diagnostics", "/metrics"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.Code)
		}
		if !bytes.Contains(response.Body.Bytes(), []byte("event")) {
			t.Fatalf("%s body = %s", path, response.Body.String())
		}
		if path == "/api/v1/diagnostics" && !bytes.Contains(response.Body.Bytes(), []byte(`"onlineDevices":2`)) {
			t.Fatalf("diagnostics missing device counts: %s", response.Body.String())
		}
		if path == "/metrics" && !bytes.Contains(response.Body.Bytes(), []byte("homeloom_devices_online 2")) {
			t.Fatalf("metrics missing device counts: %s", response.Body.String())
		}
	}
}

func TestDeviceStatesIncludeProvenance(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/devices/virtual-switch-1/states", nil)
	response := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	for _, expected := range []string{`"propertyId":"power"`, `"providerId":"virtual-main"`, `"quality":"reported"`, `"version":1`} {
		if !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Fatalf("state response missing %s: %s", expected, response.Body.String())
		}
	}
}

func TestSetPower(t *testing.T) {
	server := newTestServer()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/devices/virtual-switch-1/properties/power",
		bytes.NewBufferString(`{"value":true}`),
	)
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"id":"power"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"bool":true`)) {
		t.Fatalf("response does not contain updated power: %s", response.Body.String())
	}
}

func TestSetPowerRejectsInvalidValue(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/devices/virtual-switch-1/properties/power",
		bytes.NewBufferString(`{"value":"yes"}`),
	)
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestSetPowerReturnsDomainErrors(t *testing.T) {
	cases := []struct {
		path   string
		status int
	}{
		{"/api/v1/devices/missing/properties/power", http.StatusNotFound},
		{"/api/v1/devices/virtual-temperature-1/properties/power", http.StatusUnprocessableEntity},
	}
	for _, item := range cases {
		request := httptest.NewRequest(http.MethodPut, item.path, bytes.NewBufferString(`{"value":true}`))
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		response := httptest.NewRecorder()
		newTestServer().Handler().ServeHTTP(response, request)
		if response.Code != item.status {
			t.Fatalf("%s status = %d, want %d", item.path, response.Code, item.status)
		}
	}
}

func TestMissingPairingQRReturnsNotFound(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/targets/missing/pairing-qr", nil)
	response := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestCommandEndpoints(t *testing.T) {
	server := newTestServer()
	write := httptest.NewRequest(http.MethodPut, "/api/v1/devices/virtual-switch-1/properties/power", bytes.NewBufferString(`{"value":true}`))
	write.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	writeResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(writeResponse, write)
	var body struct {
		Command struct {
			ID string `json:"id"`
		} `json:"command"`
	}
	if err := json.Unmarshal(writeResponse.Body.Bytes(), &body); err != nil || body.Command.ID == "" {
		t.Fatalf("command response = %s, %v", writeResponse.Body.String(), err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/commands/"+body.Command.ID, nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	missing := httptest.NewRequest(http.MethodGet, "/api/v1/commands/missing", nil)
	missingResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d", missingResponse.Code)
	}
}

func TestGenericPropertyWrite(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/devices/virtual-switch-1/endpoints/main/capabilities/switch/properties/power",
		bytes.NewBufferString(`{"type":"bool","bool":true}`),
	)
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"propertyId":"power"`)) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	unsupported := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/devices/virtual-switch-1/endpoints/main/capabilities/switch/properties/unknown",
		bytes.NewBufferString(`{"type":"bool","bool":true}`),
	)
	unsupported.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	unsupportedResponse := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(unsupportedResponse, unsupported)
	if unsupportedResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", unsupportedResponse.Code)
	}
}

func TestGenericPropertyRead(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/devices/virtual-temperature-1/endpoints/main/capabilities/temperature/properties/current-temperature", nil)
	response := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"id":"current-temperature"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"number":23.6`)) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	missing := httptest.NewRequest(http.MethodGet, "/api/v1/devices/missing/endpoints/main/capabilities/temperature/properties/current-temperature", nil)
	missingResponse := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d", missingResponse.Code)
	}
}

func TestSimulateVirtualDevice(t *testing.T) {
	server := newTestServer()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/devices/virtual-temperature-1/simulation", bytes.NewBufferString(`{"online":false,"temperature":17.5}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"online":false`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"id":"current-temperature"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"number":17.5`)) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	time.Sleep(20 * time.Millisecond)
	devicesRequest := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	devicesResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(devicesResponse, devicesRequest)
	if !bytes.Contains(devicesResponse.Body.Bytes(), []byte(`"id":"current-temperature"`)) || !bytes.Contains(devicesResponse.Body.Bytes(), []byte(`"number":17.5`)) {
		t.Fatalf("registry was not updated: %s", devicesResponse.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodPatch, "/api/v1/devices/virtual-temperature-1/simulation", bytes.NewBufferString(`{}`))
	invalid.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	invalidResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d", invalidResponse.Code)
	}
}

func TestSimulateVirtualSensorProperties(t *testing.T) {
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "sensors", Config: []byte(`{"devices":[{"id":"humidity","type":"humidity-sensor"},{"id":"door","type":"contact-sensor"},{"id":"motion","type":"motion-sensor"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServerWithProvider(provider)
	for _, test := range []struct {
		deviceID string
		body     string
		want     string
	}{
		{deviceID: "humidity", body: `{"humidity":72.5}`, want: `"number":72.5`},
		{deviceID: "door", body: `{"contact":true}`, want: `"bool":true`},
		{deviceID: "motion", body: `{"motion":true}`, want: `"bool":true`},
	} {
		t.Run(test.deviceID, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPatch, "/api/v1/devices/"+test.deviceID+"/simulation", bytes.NewBufferString(test.body))
			request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(test.want)) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestDeviceEventStreamPublishesSnapshots(t *testing.T) {
	httpServer := httptest.NewServer(newTestServer().Handler())
	defer httpServer.Close()
	request, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/api/v1/events/devices", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream response = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	scanner := bufio.NewScanner(response.Body)
	if !scanner.Scan() || scanner.Text() != "event: ready" {
		t.Fatalf("first stream line = %q", scanner.Text())
	}
	for scanner.Scan() {
		if scanner.Text() == "" {
			break
		}
	}
	patch, _ := http.NewRequest(http.MethodPatch, httpServer.URL+"/api/v1/devices/virtual-switch-1/simulation", bytes.NewBufferString(`{"power":true}`))
	patch.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	patchResponse, err := http.DefaultClient.Do(patch)
	if err != nil {
		t.Fatal(err)
	}
	patchResponse.Body.Close()
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"id":"virtual-switch-1"`) && strings.Contains(line, `"id":"power"`) && strings.Contains(line, `"bool":true`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("device event not received: %v", scanner.Err())
	}
}

func TestCommandEventStreamPublishesLifecycle(t *testing.T) {
	httpServer := httptest.NewServer(newTestServer().Handler())
	defer httpServer.Close()
	response, err := http.Get(httpServer.URL + "/api/v1/events/commands")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	if !scanner.Scan() || scanner.Text() != "event: ready" {
		t.Fatalf("ready = %q", scanner.Text())
	}
	for scanner.Scan() {
		if scanner.Text() == "" {
			break
		}
	}
	write, _ := http.NewRequest(http.MethodPut, httpServer.URL+"/api/v1/devices/virtual-switch-1/properties/power", bytes.NewBufferString(`{"value":true}`))
	write.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	writeResponse, err := http.DefaultClient.Do(write)
	if err != nil {
		t.Fatal(err)
	}
	writeResponse.Body.Close()
	statuses := make([]string, 0, 4)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var command struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &command); err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, command.Status)
		if command.Status == "confirmed" {
			break
		}
	}
	if len(statuses) < 4 || statuses[0] != "queued" || statuses[len(statuses)-1] != "confirmed" {
		t.Fatalf("statuses = %#v", statuses)
	}
}

func TestStateEventStreamPublishesQualityChanges(t *testing.T) {
	httpServer := httptest.NewServer(newTestServer().Handler())
	defer httpServer.Close()
	response, err := http.Get(httpServer.URL + "/api/v1/events/states?deviceId=virtual-switch-1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	if !scanner.Scan() || scanner.Text() != "event: ready" {
		t.Fatalf("ready = %q", scanner.Text())
	}
	for scanner.Scan() {
		if scanner.Text() == "" {
			break
		}
	}
	patch, _ := http.NewRequest(http.MethodPatch, httpServer.URL+"/api/v1/devices/virtual-switch-1/simulation", bytes.NewBufferString(`{"online":false}`))
	patch.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	patchResponse, err := http.DefaultClient.Do(patch)
	if err != nil {
		t.Fatal(err)
	}
	patchResponse.Body.Close()
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"deviceId":"virtual-switch-1"`) && strings.Contains(line, `"quality":"stale"`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("stale state event not received: %v", scanner.Err())
	}
}

func TestTargetEventStreamPublishesRuntimeStatus(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	targets := application.NewTargetService([]application.TargetRegistration{{Info: application.TargetInfo{ID: "bridge", Name: "Bridge", Status: "running"}}}, nil)
	httpServer := httptest.NewServer(NewServer(":0", devices, targets, logger).Handler())
	defer httpServer.Close()
	response, err := http.Get(httpServer.URL + "/api/v1/events/targets")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	if !scanner.Scan() || scanner.Text() != "event: ready" {
		t.Fatalf("ready = %q", scanner.Text())
	}
	for scanner.Scan() {
		if scanner.Text() == "" {
			break
		}
	}
	targets.SetStatus("bridge", "error")
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"id":"bridge"`) && strings.Contains(line, `"status":"error"`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("target status event not received: %v", scanner.Err())
	}
}

func TestRestartProviderEndpoint(t *testing.T) {
	ctx := context.Background()
	config := providerconfig.Config{ID: "virtual-main", Type: "virtual", Name: "Virtual", Enabled: true, Config: []byte(`{}`)}
	factory := providersdk.NewFactory()
	if err := factory.Register("virtual", func(item providerconfig.Config) (providersdk.Provider, error) {
		return virtual.NewProviderFromConfig(item)
	}); err != nil {
		t.Fatal(err)
	}
	manager, _ := providermanager.New(virtual.NewProvider())
	_ = manager.Initialize(ctx)
	devices := application.NewDeviceService(manager)
	defer devices.Close()
	store := &apiProviderStore{items: map[string]providerconfig.Config{config.ID: config}}
	providers := application.NewProviderService([]providerconfig.Config{config}, store, factory, manager)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	targets := application.NewTargetService(nil, nil)
	server := NewServer(":0", devices, targets, logger, providers)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/providers/virtual-main/restart", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"status":"running"`)) {
		t.Fatalf("restart response = %d %s", response.Code, response.Body.String())
	}
}
