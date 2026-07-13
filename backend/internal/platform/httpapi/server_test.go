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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/persistence/sqlite"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
	"github.com/labstack/echo/v4"
)

type apiProviderStore struct {
	items map[string]providerconfig.Config
}

type unavailableDatabase struct{}

type apiSettingsStore struct{ values map[string]string }

func (s *apiSettingsStore) GetSetting(_ context.Context, key string) (string, bool, error) {
	value, ok := s.values[key]
	return value, ok, nil
}
func (s *apiSettingsStore) SaveSettings(_ context.Context, values map[string]string) error {
	for key, value := range values {
		s.values[key] = value
	}
	return nil
}

func (unavailableDatabase) DatabaseOperationMetrics() (uint64, time.Duration, time.Duration) {
	return 0, 0, 0
}

func (unavailableDatabase) HealthCheck(context.Context) error { return context.Canceled }

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

func TestManagementAuthenticationLifecycleAndCSRF(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "auth-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := newTestServer()
	server.SetAuthService(application.NewAuthService(store))

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("protected response = %d %s", response.Code, response.Body.String())
	}
	for _, publicPath := range []string{"/health", "/ready", "/api/versions", "/api/v1/system/version"} {
		response = httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, publicPath, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("public %s = %d %s", publicPath, response.Code, response.Body.String())
		}
	}
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("protected metrics = %d %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"initialized":false`) {
		t.Fatalf("initial status = %d %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	setup := httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", bytes.NewBufferString(`{"username":"admin","password":"a-long-password"}`))
	setup.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	server.Handler().ServeHTTP(response, setup)
	if response.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", response.Code, response.Body.String())
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		switch cookie.Name {
		case sessionCookieName:
			sessionCookie = cookie
		case csrfCookieName:
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || sessionCookie.Path != "/" || csrfCookie == nil || csrfCookie.HttpOnly || csrfCookie.Path != "/" {
		t.Fatalf("auth cookies = %#v", response.Result().Cookies())
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	request.AddCookie(sessionCookie)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated read = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.AddCookie(sessionCookie)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("logout without csrf = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.AddCookie(sessionCookie)
	request.Header.Set(csrfHeaderName, csrfCookie.Value)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("logout = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	request.AddCookie(sessionCookie)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("read after logout = %d %s", response.Code, response.Body.String())
	}
}

func TestLoginLimiterLocksAfterFiveFailures(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	for range 5 {
		if allowed, _ := limiter.allowed("client"); !allowed {
			t.Fatal("client locked too early")
		}
		limiter.failed("client")
	}
	if allowed, retry := limiter.allowed("client"); allowed || retry != 5*time.Minute {
		t.Fatalf("allowed = %v, retry = %v", allowed, retry)
	}
	now = now.Add(5 * time.Minute)
	if allowed, _ := limiter.allowed("client"); !allowed {
		t.Fatal("client remained locked")
	}
}

func TestDirectClientIPDoesNotTrustForwardedFor(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	request.RemoteAddr = "192.0.2.10:4567"
	request.Header.Set("X-Forwarded-For", "203.0.113.20")
	if got := directClientIP(request); got != "192.0.2.10" {
		t.Fatalf("direct client ip = %q", got)
	}
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

func TestDeviceEnabledAPIIsPersisted(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "homeloom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := application.NewDeviceService(virtual.NewProvider(), store)
	defer service.Close()
	if err := service.LoadDevicePreferences(ctx); err != nil {
		t.Fatal(err)
	}
	server := NewServer(":0", service, application.NewTargetService(nil, nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	disable := httptest.NewRequest(http.MethodPut, "/api/v1/devices/virtual-switch-1/enabled", bytes.NewBufferString(`{"enabled":false}`))
	disable.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, disable)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"disabled":true`)) || bytes.Contains(response.Body.Bytes(), []byte(`"online":true`)) {
		t.Fatalf("disable response = %d %s", response.Code, response.Body.String())
	}
	ids, err := store.ListDisabledDeviceIDs(ctx)
	if err != nil || len(ids) != 1 || ids[0] != "virtual-switch-1" {
		t.Fatalf("disabled ids = %#v, %v", ids, err)
	}
	enable := httptest.NewRequest(http.MethodPut, "/api/v1/devices/virtual-switch-1/enabled", bytes.NewBufferString(`{"enabled":true}`))
	enable.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, enable)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"online":true`)) {
		t.Fatalf("enable response = %d %s", response.Code, response.Body.String())
	}
}

func TestMutationAuditAndCommandCorrelationID(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "audit-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	devices := application.NewDeviceService(virtual.NewProvider(), store)
	defer devices.Close()
	server := NewServer(":0", devices, application.NewTargetService(nil, nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	server.SetAuditService(application.NewAuditService(store))

	write := httptest.NewRequest(http.MethodPut, "/api/v1/devices/virtual-switch-1/endpoints/main/capabilities/switch/properties/power", bytes.NewBufferString(`{"type":"bool","bool":true}`))
	write.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	write.Header.Set(echo.HeaderXRequestID, "trace-command-1")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, write)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"correlationId":"trace-command-1"`)) {
		t.Fatalf("write response = %d %s", response.Code, response.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v1/audit-events?limit=20", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, list)
	for _, expected := range []string{`"correlationId":"trace-command-1"`, `"resourceType":"device"`, `"resourceId":"virtual-switch-1"`, `"outcome":"succeeded"`} {
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Fatalf("audit response = %d %s", response.Code, response.Body.String())
		}
	}

	invalid := httptest.NewRequest(http.MethodPatch, "/api/v1/devices/virtual-switch-1/simulation", bytes.NewBufferString(`{}`))
	invalid.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	invalid.Header.Set(echo.HeaderXRequestID, "trace-failed-1")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, invalid)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid response = %d %s", response.Code, response.Body.String())
	}
	events, err := store.ListAuditEvents(ctx, 10)
	if err != nil || len(events) != 2 || events[0].CorrelationID != "trace-failed-1" || events[0].Status != http.StatusBadRequest || events[0].Outcome != "failed" {
		t.Fatalf("audit events = %#v, %v", events, err)
	}

	badLimit := httptest.NewRequest(http.MethodGet, "/api/v1/audit-events?limit=501", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, badLimit)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("bad limit response = %d %s", response.Code, response.Body.String())
	}
}

func TestSystemArtifactsAreDownloadsAndDoNotExposeSecrets(t *testing.T) {
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	providers := application.NewProviderService([]providerconfig.Config{{ID: "mqtt-main", Type: "mqtt", Name: "MQTT", Config: json.RawMessage(`{"password":"provider-secret"}`)}}, nil, nil, nil)
	targets := application.NewTargetService([]application.TargetRegistration{{Info: application.TargetInfo{ID: "apple-main", Type: "apple-hap", Name: "Bridge", PairingCode: "111-22-333", SetupURI: "X-HM://secret"}}}, nil)
	server := NewServer(":0", devices, targets, slog.New(slog.NewTextHandler(io.Discard, nil)), providers)
	server.SetExportService(application.NewExportService(devices, providers, targets, nil, nil))

	for _, path := range []string{"/api/v1/system/config-export", "/api/v1/system/diagnostic-bundle"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Disposition"), "attachment;") || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: status=%d headers=%v body=%s", path, response.Code, response.Header(), response.Body.String())
		}
		for _, secret := range []string{"provider-secret", "111-22-333", "X-HM://secret"} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatalf("%s contains %q: %s", path, secret, response.Body.String())
			}
		}
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

func TestDeviceModelContractsExposePublisherAndConsumerLevels(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/device-models", nil)
	response := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(response, request)
	for _, expected := range []string{
		`"deviceType":"lightbulb"`, `"propertyId":"power"`, `"level":"required"`,
		`"propertyId":"brightness"`, `"level":"optional"`, `"behavior":"must-publish"`,
		`"behavior":"must-map"`, `"behavior":"explicit-path-mapping-only"`,
	} {
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Fatalf("model contracts response = %d %s", response.Code, response.Body.String())
		}
	}
}

func TestAPIVersionDiscoveryAndResponseHeader(t *testing.T) {
	server := newTestServer()
	request := httptest.NewRequest(http.MethodGet, "/api/versions", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"current":"v1"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"supported":["v1"]`)) {
		t.Fatalf("versions response = %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/system/version", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get(apiVersionHeader) != "1" {
		t.Fatalf("version header = %q, response = %d", response.Header().Get(apiVersionHeader), response.Code)
	}
}

func TestRuntimeSettingsAPIUpdatesWithoutRestart(t *testing.T) {
	server := newTestServer()
	devices := application.NewDeviceService(virtual.NewProvider())
	t.Cleanup(func() { _ = devices.Close() })
	store := &apiSettingsStore{values: make(map[string]string)}
	settings, err := application.NewSettingsService(context.Background(), store, devices)
	if err != nil {
		t.Fatal(err)
	}
	server.SetSettingsService(settings)

	put := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewBufferString(`{"commandTimeoutSeconds":17,"commandHistoryLimit":800}`))
	put.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, put)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"commandTimeoutSeconds":17`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"commandHistoryLimit":800`)) || store.values["command_timeout_seconds"] != "17" || store.values["command_history_limit"] != "800" {
		t.Fatalf("save response = %d %s, store = %#v", response.Code, response.Body.String(), store.values)
	}
	get := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, get)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"commandTimeoutSeconds":17`)) {
		t.Fatalf("get response = %d %s", response.Code, response.Body.String())
	}

	invalid := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewBufferString(`{"commandTimeoutSeconds":301,"commandHistoryLimit":1000}`))
	invalid.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, invalid)
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"fields":{"commandTimeoutSeconds":`)) {
		t.Fatalf("invalid response = %d %s", response.Code, response.Body.String())
	}
}

func TestReadinessReflectsDatabaseHealth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	targets := application.NewTargetService(nil, nil)
	healthy := NewServer(":0", application.NewDeviceService(virtual.NewProvider()), targets, logger)
	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	response := httptest.NewRecorder()
	healthy.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"status":"ready"`)) {
		t.Fatalf("healthy readiness = %d %s", response.Code, response.Body.String())
	}

	unavailable := NewServer(":0", application.NewDeviceService(virtual.NewProvider(), unavailableDatabase{}), targets, logger)
	response = httptest.NewRecorder()
	unavailable.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !bytes.Contains(response.Body.Bytes(), []byte(`"database":"unavailable"`)) {
		t.Fatalf("unavailable readiness = %d %s", response.Code, response.Body.String())
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

func TestValidationErrorsIncludeFieldLocations(t *testing.T) {
	providerRequest := httptest.NewRequest(http.MethodPost, "/api/v1/providers", bytes.NewBufferString(`{"id":"bad id","type":"virtual","name":"Bad","enabled":true,"config":{}}`))
	providerRequest.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	providerResponse := httptest.NewRecorder()
	newProviderManagementTestServer(t).Handler().ServeHTTP(providerResponse, providerRequest)
	if providerResponse.Code != http.StatusBadRequest || !bytes.Contains(providerResponse.Body.Bytes(), []byte(`"fields":{"id":`)) {
		t.Fatalf("provider response = %d %s", providerResponse.Code, providerResponse.Body.String())
	}
	targetRequest := httptest.NewRequest(http.MethodPost, "/api/v1/targets", bytes.NewBufferString(`{"id":"bad id","type":"apple-hap","enabled":true}`))
	targetRequest.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	targetResponse := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(targetResponse, targetRequest)
	if targetResponse.Code != http.StatusBadRequest || !bytes.Contains(targetResponse.Body.Bytes(), []byte(`"fields":{"id":`)) {
		t.Fatalf("target response = %d %s", targetResponse.Code, targetResponse.Body.String())
	}
}

func newProviderManagementTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	config := providerconfig.Config{ID: "virtual-main", Type: "virtual", Name: "Virtual", Enabled: true, Config: []byte(`{"password":"do-not-return"}`)}
	factory := providersdk.NewFactory()
	if err := factory.Register("virtual", func(item providerconfig.Config) (providersdk.Provider, error) {
		return virtual.NewProviderFromConfig(item)
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := providermanager.New(virtual.NewProvider())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	devices := application.NewDeviceService(manager)
	t.Cleanup(func() { _ = devices.Close() })
	providers := application.NewProviderService([]providerconfig.Config{config}, &apiProviderStore{items: map[string]providerconfig.Config{config.ID: config}}, factory, manager)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	targets := application.NewTargetService(nil, nil)
	return NewServer(":0", devices, targets, logger, providers)
}

func TestProviderAPIConfigIsRedacted(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	response := httptest.NewRecorder()
	newProviderManagementTestServer(t).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"password":"********"`)) || bytes.Contains(response.Body.Bytes(), []byte("do-not-return")) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestProviderConnectionTestAPI(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/providers/test", bytes.NewBufferString(`{"type":"virtual","config":{}}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	newProviderManagementTestServer(t).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"reachable":true`)) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
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
	if !bytes.Contains(response.Body.Bytes(), []byte(`"schemaVersion":1`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"availability":"online"`)) || bytes.Contains(response.Body.Bytes(), []byte(`"state":`)) {
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
		if path == "/metrics" {
			for _, name := range []string{"homeloom_event_average_latency_milliseconds", "homeloom_slow_event_handlers_total", "homeloom_database_operations_total", "homeloom_homekit_pushes_total", "homeloom_devices_unknown", "homeloom_command_queue_pending", "homeloom_command_queue_max_pending", "homeloom_commands_outcome_unknown_total", "homeloom_commands_coalesced_total", "homeloom_provider_clock_skew_events_total"} {
				if !bytes.Contains(response.Body.Bytes(), []byte(name)) {
					t.Fatalf("metrics missing %s: %s", name, response.Body.String())
				}
			}
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
	invalid := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/devices/virtual-switch-1/endpoints/main/capabilities/switch/properties/power",
		bytes.NewBufferString(`{"type":"number","number":1}`),
	)
	invalid.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	invalidResponse := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest || !bytes.Contains(invalidResponse.Body.Bytes(), []byte(`"code":"bad_request"`)) {
		t.Fatalf("invalid response = %d %s", invalidResponse.Code, invalidResponse.Body.String())
	}
	nullValue := httptest.NewRequest(http.MethodPut, "/api/v1/devices/virtual-switch-1/endpoints/main/capabilities/switch/properties/power", bytes.NewBufferString(`null`))
	nullValue.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	nullResponse := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(nullResponse, nullValue)
	if nullResponse.Code != http.StatusBadRequest || !bytes.Contains(nullResponse.Body.Bytes(), []byte(`"code":"bad_request"`)) {
		t.Fatalf("null response = %d %s", nullResponse.Code, nullResponse.Body.String())
	}
}

func TestGenericPropertyWritePropagatesRequestTimeoutToVirtualProvider(t *testing.T) {
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "slow", Name: "Slow", Config: json.RawMessage(`{"latencyMs":1000}`)})
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServerWithProvider(provider)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/devices/slow-switch-1/endpoints/main/capabilities/switch/properties/power", bytes.NewBufferString(`{"type":"bool","bool":true}`)).WithContext(ctx)
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	started := time.Now()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusRequestTimeout || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"request_timeout"`)) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("request cancellation took %s", elapsed)
	}
}

func TestMappingPreviewAPIExplainsForwardAndReverseTransforms(t *testing.T) {
	server := newTestServer()
	forward := httptest.NewRequest(http.MethodPost, "/api/v1/mapping/preview", bytes.NewBufferString(`{"profile":{"schemaVersion":1,"id":"temperature-map","version":1,"kind":"capability","inputType":"number","outputType":"number","transforms":[{"type":"scale","factor":1.8,"offset":32}]},"direction":"forward","value":{"type":"number","number":20}}`))
	forward.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, forward)
	for _, expected := range []string{`"number":68`, `"transform":"scale"`, `"profileId":"temperature-map"`} {
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
	}

	reverseClamp := httptest.NewRequest(http.MethodPost, "/api/v1/mapping/preview", bytes.NewBufferString(`{"profile":{"schemaVersion":1,"id":"clamped","version":1,"kind":"target","inputType":"number","outputType":"number","transforms":[{"type":"clamp","min":0,"max":100}]},"direction":"reverse","value":{"type":"number","number":50}}`))
	reverseClamp.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, reverseClamp)
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"profile.transforms.0":"clamp is not reversible"`)) {
		t.Fatalf("reverse response = %d %s", response.Code, response.Body.String())
	}
}

func TestMappingProfileCRUDHotReloadAndExport(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "profiles-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles, err := application.NewProfileService(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	devices := application.NewDeviceService(virtual.NewProvider(), store)
	defer devices.Close()
	server := NewServer(":0", devices, application.NewTargetService(nil, nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	server.SetProfileService(profiles)

	create := httptest.NewRequest(http.MethodPost, "/api/v1/mapping/profiles", bytes.NewBufferString(`{"schemaVersion":1,"id":"custom-invert","version":1,"kind":"provider","inputType":"bool","outputType":"bool","transforms":[{"type":"invert"}]}`))
	create.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, create)
	if response.Code != http.StatusCreated || !bytes.Contains(response.Body.Bytes(), []byte(`"id":"custom-invert"`)) {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}

	preview := httptest.NewRequest(http.MethodPost, "/api/v1/mapping/preview", bytes.NewBufferString(`{"profileId":"custom-invert","direction":"forward","value":{"type":"bool","bool":true}}`))
	preview.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, preview)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"bool":false`)) {
		t.Fatalf("preview = %d %s", response.Code, response.Body.String())
	}

	createBinding := httptest.NewRequest(http.MethodPost, "/api/v1/mapping/bindings", bytes.NewBufferString(`{"profileId":"custom-invert","providerId":"virtual-main","deviceId":"virtual-switch-1","endpointId":"main","capabilityId":"switch","propertyId":"power","enabled":true}`))
	createBinding.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, createBinding)
	if response.Code != http.StatusCreated || !bytes.Contains(response.Body.Bytes(), []byte(`"profileId":"custom-invert"`)) {
		t.Fatalf("create binding = %d %s", response.Code, response.Body.String())
	}
	var bindingResponse struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &bindingResponse); err != nil || bindingResponse.Data.ID == "" {
		t.Fatalf("decode binding = %#v, %v", bindingResponse, err)
	}
	removeInUse := httptest.NewRequest(http.MethodDelete, "/api/v1/mapping/profiles/custom-invert", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, removeInUse)
	if response.Code != http.StatusConflict {
		t.Fatalf("delete in-use profile = %d %s", response.Code, response.Body.String())
	}
	removeBinding := httptest.NewRequest(http.MethodDelete, "/api/v1/mapping/bindings/"+bindingResponse.Data.ID, nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, removeBinding)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete binding = %d %s", response.Code, response.Body.String())
	}

	sameVersion := httptest.NewRequest(http.MethodPut, "/api/v1/mapping/profiles/custom-invert", bytes.NewBufferString(`{"schemaVersion":1,"version":1,"kind":"provider","inputType":"bool","outputType":"bool","transforms":[{"type":"invert"}]}`))
	sameVersion.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, sameVersion)
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"version":"must be greater than current version 1"`)) {
		t.Fatalf("update = %d %s", response.Code, response.Body.String())
	}

	export := httptest.NewRequest(http.MethodGet, "/api/v1/mapping/profiles/export", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, export)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Header().Get("Content-Disposition"), "attachment") || !bytes.Contains(response.Body.Bytes(), []byte(`"id":"custom-invert"`)) || bytes.Contains(response.Body.Bytes(), []byte(`builtin-active-low`)) {
		t.Fatalf("export = %d %v %s", response.Code, response.Header(), response.Body.String())
	}
	exported := append([]byte(nil), response.Body.Bytes()...)

	remove := httptest.NewRequest(http.MethodDelete, "/api/v1/mapping/profiles/custom-invert", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, remove)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", response.Code, response.Body.String())
	}
	preview = httptest.NewRequest(http.MethodPost, "/api/v1/mapping/preview", bytes.NewBufferString(`{"profileId":"custom-invert","direction":"forward","value":{"type":"bool","bool":true}}`))
	preview.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, preview)
	if response.Code != http.StatusNotFound {
		t.Fatalf("deleted preview = %d %s", response.Code, response.Body.String())
	}
	importRequest := httptest.NewRequest(http.MethodPost, "/api/v1/mapping/profiles/import", bytes.NewReader(exported))
	importRequest.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, importRequest)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"id":"custom-invert"`)) {
		t.Fatalf("import = %d %s", response.Code, response.Body.String())
	}
	preview = httptest.NewRequest(http.MethodPost, "/api/v1/mapping/preview", bytes.NewBufferString(`{"profileId":"custom-invert","direction":"forward","value":{"type":"bool","bool":true}}`))
	preview.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, preview)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"bool":false`)) {
		t.Fatalf("imported preview = %d %s", response.Code, response.Body.String())
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

func TestUnknownStateAPIUsesNullWithoutInventingProviderValue(t *testing.T) {
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "unknown-api", Name: "Unknown", Config: []byte(`{"devices":[{"id":"pending","type":"switch","availability":"unknown"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/devices/pending/states", nil)
	response := httptest.NewRecorder()
	newTestServerWithProvider(provider).Handler().ServeHTTP(response, request)
	for _, expected := range []string{`"value":null`, `"quality":"unknown"`, `"known":false`, `"available":false`, `"unavailableReason":"availability-unknown"`} {
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Fatalf("unknown state response = %d %s", response.Code, response.Body.String())
		}
	}
}

func TestGenericCommandExecution(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/devices/virtual-switch-1/endpoints/main/capabilities/switch/commands/set-power", bytes.NewBufferString(`{"parameters":{"value":{"type":"bool","bool":true}}}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(response, request)
	for _, expected := range []string{`"kind":"action"`, `"commandId":"set-power"`, `"status":"confirmed"`, `"bool":true`} {
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
	}
	invalid := httptest.NewRequest(http.MethodPost, "/api/v1/devices/virtual-switch-1/endpoints/main/capabilities/switch/commands/set-power", bytes.NewBufferString(`{}`))
	invalid.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	invalidResponse := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid response = %d %s", invalidResponse.Code, invalidResponse.Body.String())
	}
}

func TestCommandIdempotencyKeyPreventsDuplicateExecution(t *testing.T) {
	server := newTestServer()
	execute := func(command, body, key string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/devices/virtual-switch-1/endpoints/main/capabilities/switch/commands/"+command, bytes.NewBufferString(body))
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	first := execute("toggle", `{}`, "toggle-once")
	replay := execute("toggle", `{}`, "toggle-once")
	var firstBody, replayBody struct {
		Command struct {
			ID string `json:"id"`
		} `json:"command"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &firstBody)
	_ = json.Unmarshal(replay.Body.Bytes(), &replayBody)
	if first.Code != http.StatusOK || replay.Code != http.StatusOK || firstBody.Command.ID == "" || replayBody.Command.ID != firstBody.Command.ID || !bytes.Contains(replay.Body.Bytes(), []byte(`"bool":true`)) {
		t.Fatalf("first = %s, replay = %s", first.Body.String(), replay.Body.String())
	}
	execute("set-power", `{"parameters":{"value":{"type":"bool","bool":true}}}`, "set-once")
	conflict := execute("set-power", `{"parameters":{"value":{"type":"bool","bool":false}}}`, "set-once")
	if conflict.Code != http.StatusConflict || !bytes.Contains(conflict.Body.Bytes(), []byte(`"code":"conflict"`)) {
		t.Fatalf("conflict = %d %s", conflict.Code, conflict.Body.String())
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
	sequence := httptest.NewRequest(http.MethodPatch, "/api/v1/devices/virtual-temperature-1/simulation", bytes.NewBufferString(`{"sequence":12,"repeat":2}`))
	sequence.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	sequenceResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(sequenceResponse, sequence)
	if sequenceResponse.Code != http.StatusOK || !bytes.Contains(sequenceResponse.Body.Bytes(), []byte(`"sequence":12`)) {
		t.Fatalf("sequence response = %d %s", sequenceResponse.Code, sequenceResponse.Body.String())
	}
}

func TestSimulateUnknownAvailability(t *testing.T) {
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/devices/virtual-switch-1/simulation", bytes.NewBufferString(`{"availability":"unknown"}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	newTestServer().Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"availability":"unknown","online":false`)) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
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

func TestSimulateAdvancedHomeKitProperties(t *testing.T) {
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "advanced", Config: []byte(`{"devices":[{"id":"fan","type":"fan"},{"id":"air","type":"air-purifier"},{"id":"shade","type":"window-covering"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServerWithProvider(provider)
	for _, test := range []struct{ deviceID, body, want string }{
		{deviceID: "fan", body: `{"active":true,"speed":45,"mode":"auto"}`, want: `"string":"blowing-air"`},
		{deviceID: "air", body: `{"filterLife":7,"filterChange":true}`, want: `"id":"change-indication"`},
		{deviceID: "shade", body: `{"position":65}`, want: `"int":65`},
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

func TestAuditEventStreamPublishesPersistedMutation(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "audit-stream.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	devices := application.NewDeviceService(virtual.NewProvider(), store)
	defer devices.Close()
	server := NewServer(":0", devices, application.NewTargetService(nil, nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	server.SetAuditService(application.NewAuditService(store))
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	response, err := http.Get(httpServer.URL + "/api/v1/events/audit")
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

	mutation, _ := http.NewRequest(http.MethodPatch, httpServer.URL+"/api/v1/devices/virtual-switch-1/simulation", bytes.NewBufferString(`{"power":true}`))
	mutation.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	mutation.Header.Set(echo.HeaderXRequestID, "trace-stream-1")
	mutationResponse, err := http.DefaultClient.Do(mutation)
	if err != nil {
		t.Fatal(err)
	}
	mutationResponse.Body.Close()
	if mutationResponse.StatusCode != http.StatusOK {
		t.Fatalf("mutation status = %d", mutationResponse.StatusCode)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"correlationId":"trace-stream-1"`) && strings.Contains(line, `"outcome":"succeeded"`) {
			return
		}
	}
	t.Fatalf("audit event not received: %v", scanner.Err())
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
