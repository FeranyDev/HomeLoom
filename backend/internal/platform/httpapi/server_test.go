package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go.uber.org/zap"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/aiautomation"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/mcpagent"
	"github.com/feranydev/homeloom/backend/internal/persistence/gormstore"
	"github.com/feranydev/homeloom/backend/internal/platform/subprocesslog"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	sonoffcloud "github.com/feranydev/homeloom/backend/internal/providers/sonoff/cloud"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/feranydev/homeloom/backend/internal/providers/xiaomi"
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
	"github.com/labstack/echo/v4"
)

type apiProviderStore struct {
	items map[string]providerconfig.Config
}

func TestSonoffLoginHTTPErrorKeepsOnlySafeFailureDetails(t *testing.T) {
	tests := []struct {
		name       string
		source     error
		statusCode int
		message    string
	}{
		{name: "response code", source: &sonoffcloud.ResponseCodeError{Code: 10003}, statusCode: http.StatusBadRequest, message: "Sonoff/eWeLink 登录被拒绝（错误码 10003）"},
		{name: "cloud HTTP status", source: &sonoffcloud.HTTPStatusError{StatusCode: http.StatusForbidden}, statusCode: http.StatusBadGateway, message: "Sonoff/eWeLink 服务响应异常（HTTP 403）"},
		{name: "timeout", source: context.DeadlineExceeded, statusCode: http.StatusGatewayTimeout, message: "Sonoff/eWeLink 登录超时，请检查 HomeLoom 主机的网络后重试"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := sonoffLoginHTTPError(test.source)
			if err.Code != test.statusCode || err.Message != test.message || err.Internal != test.source {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

type unavailableDatabase struct{}

type apiSettingsStore struct{ values map[string]string }

type apiAIService struct {
	status      mcpagent.AIServiceStatus
	input       mcpagent.AIServiceConfig
	models      []mcpagent.AIModel
	run         mcpagent.Run
	streamInput mcpagent.RunRequest
}

type apiAutomationStore struct {
	items map[string]aiautomation.Automation
}

func (s *apiAutomationStore) ListAIAutomations(context.Context) ([]aiautomation.Automation, error) {
	items := make([]aiautomation.Automation, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item)
	}
	return items, nil
}
func (s *apiAutomationStore) GetAIAutomation(_ context.Context, id string) (aiautomation.Automation, bool, error) {
	item, found := s.items[id]
	return item, found, nil
}
func (s *apiAutomationStore) SaveAIAutomation(_ context.Context, item aiautomation.Automation) error {
	s.items[item.ID] = item
	return nil
}
func (s *apiAutomationStore) DeleteAIAutomation(_ context.Context, id string) error {
	delete(s.items, id)
	return nil
}

type apiAutomationRunner struct{}

func (apiAutomationRunner) StartAutomation(context.Context, application.AIAutomationInvocation) (application.AIAutomationRun, error) {
	return application.AIAutomationRun{ID: "run-task-1", Status: "awaiting_approval", Message: "等待批准"}, nil
}
func (apiAutomationRunner) ApproveAutomation(context.Context, string, bool) (application.AIAutomationRun, error) {
	return application.AIAutomationRun{ID: "run-task-1", Status: "executed", Message: "设备操作已提交"}, nil
}

func (s *apiAIService) AIServiceStatus(context.Context) (mcpagent.AIServiceStatus, error) {
	return s.status, nil
}
func (s *apiAIService) UpdateAIService(_ context.Context, input mcpagent.AIServiceConfig) (mcpagent.AIServiceStatus, error) {
	s.input = input
	sessionContext := mcpagent.DefaultSessionContextSettings()
	if input.SessionContext != nil {
		sessionContext = *input.SessionContext
	}
	homePreferences := mcpagent.DefaultHomePreferences()
	if input.HomePreferences != nil {
		homePreferences = *input.HomePreferences
	}
	s.status = mcpagent.AIServiceStatus{APIBaseURL: input.APIBaseURL, APIProxyURL: input.APIProxyURL, Model: input.Model, APIProtocol: input.APIProtocol, AgentInstructions: input.AgentInstructions, DefaultAgentInstructions: mcpagent.DefaultAgentInstructions, SessionContext: sessionContext, HomePreferences: homePreferences, APIKeyConfigured: input.APIKey != "", Configured: input.APIKey != "" && input.Model != ""}
	return s.status, nil
}
func (s *apiAIService) ListAIModels(context.Context) ([]mcpagent.AIModel, error) {
	return s.models, nil
}

func (s *apiAIService) StartAIRun(_ context.Context, message string) (mcpagent.Run, error) {
	s.run.Message = message
	return s.run, nil
}
func (s *apiAIService) StreamAIRun(_ context.Context, input mcpagent.RunRequest, onEvent func(mcpagent.StreamEvent) error) error {
	s.streamInput = input
	if err := onEvent(mcpagent.StreamEvent{Type: "delta", Delta: input.Message}); err != nil {
		return err
	}
	s.run.Message = input.Message
	return onEvent(mcpagent.StreamEvent{Type: "run", Run: &s.run})
}
func (s *apiAIService) AIRun(context.Context, string) (mcpagent.Run, error) { return s.run, nil }
func (s *apiAIService) ApproveAIRun(context.Context, string) (mcpagent.Run, error) {
	s.run.Status = mcpagent.RunExecuted
	return s.run, nil
}

type apiScanProvider struct{ id string }

type apiCredentialRevokingProvider struct{ id string }
type apiCredentialRevocationRuntime struct{ removed []string }

func (p *apiCredentialRevokingProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "xiaomi", Name: p.id}
}
func (*apiCredentialRevokingProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{}
}
func (*apiCredentialRevokingProvider) Initialize(context.Context) error { return nil }
func (*apiCredentialRevokingProvider) Close(context.Context) error      { return nil }
func (*apiCredentialRevokingProvider) RevokeCredentials(context.Context) (providersdk.CredentialRevocation, error) {
	return providersdk.CredentialRevocation{Config: json.RawMessage(`{"credentialsRevoked":true,"devices":[]}`)}, nil
}
func (*apiCredentialRevocationRuntime) Apply(context.Context, providersdk.Provider) error { return nil }
func (r *apiCredentialRevocationRuntime) Remove(_ context.Context, id string) error {
	r.removed = append(r.removed, id)
	return nil
}
func (*apiCredentialRevocationRuntime) ProviderInfos() []providersdk.RuntimeInfo { return nil }

type apiAuthState struct {
	sync.Mutex
	verified bool
	url      string
}

type apiAuthProvider struct {
	id    string
	state *apiAuthState
}

type apiAuthRuntime struct{ state *apiAuthState }

func (p *apiAuthProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: xiaomi.XiaomiMIoTCloudProviderType, Name: "Cloud"}
}
func (*apiAuthProvider) Capabilities() providersdk.Capabilities { return providersdk.Capabilities{} }
func (p *apiAuthProvider) Initialize(context.Context) error {
	p.state.Lock()
	defer p.state.Unlock()
	if !p.state.verified {
		return &xiaomi.IdentityVerificationRequiredError{URL: p.state.url}
	}
	return nil
}
func (*apiAuthProvider) Close(context.Context) error { return nil }
func (p *apiAuthProvider) IdentityVerificationURL() (string, bool) {
	p.state.Lock()
	defer p.state.Unlock()
	return p.state.url, !p.state.verified
}
func (p *apiAuthProvider) CompleteIdentityVerification(context.Context, string) (json.RawMessage, error) {
	p.state.Lock()
	p.state.verified = true
	p.state.Unlock()
	return json.RawMessage(`{"region":"cn","userId":"42","ssecurity":"security","serviceToken":"service-token","devices":[]}`), nil
}
func (r *apiAuthRuntime) Apply(ctx context.Context, provider providersdk.Provider) error {
	return provider.Initialize(ctx)
}
func (*apiAuthRuntime) Remove(context.Context, string) error { return nil }
func (r *apiAuthRuntime) ProviderInfos() []providersdk.RuntimeInfo {
	r.state.Lock()
	verified := r.state.verified
	r.state.Unlock()
	status := "auth_required"
	if verified {
		status = "running"
	}
	return []providersdk.RuntimeInfo{{Manifest: providersdk.Manifest{ID: "cloud-main", Type: xiaomi.XiaomiMIoTCloudProviderType}, Status: status}}
}

func (p *apiScanProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "scan", Name: p.id}
}
func (*apiScanProvider) Capabilities() providersdk.Capabilities { return providersdk.Capabilities{} }
func (*apiScanProvider) Initialize(context.Context) error       { return nil }
func (*apiScanProvider) Close(context.Context) error            { return nil }
func (*apiScanProvider) Scan(context.Context) ([]providersdk.DiscoveryCandidate, error) {
	return []providersdk.DiscoveryCandidate{{Provider: "scan", Name: "LAN candidate", Host: "192.0.2.20", Port: 7000, MAC: "aabbccddeeff"}}, nil
}

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

func newProviderScanTestServer(t *testing.T) *Server {
	t.Helper()
	factory := providersdk.NewFactory()
	if err := factory.Register("scan", func(item providerconfig.Config) (providersdk.Provider, error) {
		return &apiScanProvider{id: item.ID}, nil
	}); err != nil {
		t.Fatal(err)
	}
	providers := application.NewProviderService(nil, nil, factory, nil)
	devices := application.NewDeviceService(virtual.NewProvider())
	t.Cleanup(func() { _ = devices.Close() })
	return NewServer(":0", devices, application.NewTargetService(nil, nil), zap.NewNop(), providers)
}

func TestManagementAuthenticationLifecycleAndCSRF(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
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

func TestDeviceMCPConfigRoutesPersistDeviceAndPropertyNotes(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	server := NewServer(":0", devices, application.NewTargetService(nil, nil), zap.NewNop())
	server.SetMCPConfigService(application.NewMCPConfigService(store, devices))

	deviceRequest := httptest.NewRequest(http.MethodPut, "/api/v1/devices/virtual-switch-1/mcp-config", strings.NewReader(`{"enabled":true,"usageNote":"走廊主灯","defaultAccess":"read"}`))
	deviceRequest.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	deviceResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(deviceResponse, deviceRequest)
	if deviceResponse.Code != http.StatusOK || !strings.Contains(deviceResponse.Body.String(), `"usageNote":"走廊主灯"`) {
		t.Fatalf("save device MCP config = %d %s", deviceResponse.Code, deviceResponse.Body.String())
	}

	propertyRequest := httptest.NewRequest(http.MethodPut, "/api/v1/devices/virtual-switch-1/mcp-properties/main/switch/power", strings.NewReader(`{"usageNote":"夜间才建议关闭","access":"confirm"}`))
	propertyRequest.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	propertyResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(propertyResponse, propertyRequest)
	if propertyResponse.Code != http.StatusOK || !strings.Contains(propertyResponse.Body.String(), `"access":"confirm"`) {
		t.Fatalf("save property MCP config = %d %s", propertyResponse.Code, propertyResponse.Body.String())
	}

	listResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/v1/devices/virtual-switch-1/mcp-properties", nil))
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"usageNote":"夜间才建议关闭"`) {
		t.Fatalf("list property MCP configs = %d %s", listResponse.Code, listResponse.Body.String())
	}
}

func TestAIServiceConfigRoutesProxyWithoutReturningAPIKey(t *testing.T) {
	server := newTestServer()
	ai := &apiAIService{status: mcpagent.AIServiceStatus{APIBaseURL: "https://models.example.test/v1", APIProxyURL: "http://127.0.0.1:7890", Model: "model-a", APIKeyConfigured: true, Configured: true, AgentInstructions: "默认提示词", DefaultAgentInstructions: "默认提示词", SessionContext: mcpagent.DefaultSessionContextSettings()}, models: []mcpagent.AIModel{{ID: "model-a"}}}
	server.SetAIService(ai)

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/ai-service/config", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"apiKeyConfigured":true`) || !strings.Contains(response.Body.String(), `"apiProxyUrl":"http://127.0.0.1:7890"`) || !strings.Contains(response.Body.String(), `"defaultAgentInstructions":"默认提示词"`) || !strings.Contains(response.Body.String(), `"sessionContext":{"enabled":true`) || strings.Contains(response.Body.String(), "secret-api-key") {
		t.Fatalf("get AI config = %d %s", response.Code, response.Body.String())
	}

	request := httptest.NewRequest(http.MethodPut, "/api/v1/ai-service/config", strings.NewReader(`{"apiBaseUrl":"https://models.example.test/v1","apiProxyUrl":"https://proxy.example.test:8443","apiKey":"secret-api-key","model":"model-b","agentInstructions":"用简洁 Markdown 回复。","sessionContext":{"enabled":false,"currentTime":true,"timeZone":false,"weekday":true,"runSource":true,"triggerState":false,"regionLanguage":true,"temperatureUnit":false},"homePreferences":{"timeZone":"America/New_York","regionLanguage":"en-US","temperatureUnit":"fahrenheit"}}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || ai.input.APIKey != "secret-api-key" || ai.input.APIProxyURL != "https://proxy.example.test:8443" || ai.input.AgentInstructions != "用简洁 Markdown 回复。" || ai.input.SessionContext == nil || ai.input.SessionContext.Enabled || ai.input.SessionContext.TimeZone || ai.input.SessionContext.TriggerState || ai.input.SessionContext.TemperatureUnit || ai.input.HomePreferences == nil || ai.input.HomePreferences.TimeZone != "America/New_York" || ai.input.HomePreferences.RegionLanguage != "en-US" || ai.input.HomePreferences.TemperatureUnit != mcpagent.TemperatureUnitFahrenheit || strings.Contains(response.Body.String(), "secret-api-key") || !strings.Contains(response.Body.String(), `"model":"model-b"`) || !strings.Contains(response.Body.String(), `"apiProxyUrl":"https://proxy.example.test:8443"`) || !strings.Contains(response.Body.String(), `"temperatureUnit":"fahrenheit"`) {
		t.Fatalf("save AI config = %d %s input=%#v", response.Code, response.Body.String(), ai.input)
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/ai-service/models", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"model-a"`) {
		t.Fatalf("list AI models = %d %s", response.Code, response.Body.String())
	}
}

func TestAIServiceTimeoutErrorsAreRetryableRequestTimeouts(t *testing.T) {
	for _, source := range []error{context.DeadlineExceeded, &mcpagent.AgentControlError{Status: http.StatusGatewayTimeout}} {
		err := aiServiceHTTPError(source)
		httpErr, ok := err.(*echo.HTTPError)
		if !ok || httpErr.Code != http.StatusRequestTimeout || httpErr.Message != "AI 思考超时，请稍后重试或缩短请求" {
			t.Fatalf("timeout error = %#v", err)
		}
	}
}

func TestAIServiceRunRoutesRemainBehindCoreAuthentication(t *testing.T) {
	server := newTestServer()
	ai := &apiAIService{run: mcpagent.Run{ID: "run-1", Status: mcpagent.RunAwaitingApproval}}
	server.SetAIService(ai)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ai-service/runs", strings.NewReader(`{"message":"打开客厅灯"}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"run-1"`) || !strings.Contains(response.Body.String(), "打开客厅灯") {
		t.Fatalf("create run = %d %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	streamRequest := httptest.NewRequest(http.MethodPost, "/api/v1/ai-service/runs/stream", strings.NewReader(`{"message":"流式检查","history":[{"role":"user","content":"上一条问题"}]}`))
	streamRequest.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	server.Handler().ServeHTTP(response, streamRequest)
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get(echo.HeaderContentType), "text/event-stream") || !strings.Contains(response.Body.String(), `"delta":"流式检查"`) || !strings.Contains(response.Body.String(), `"type":"run"`) || ai.streamInput.Context.Source != mcpagent.RunSourceInteractive || len(ai.streamInput.History) != 1 || ai.streamInput.History[0].Content != "上一条问题" {
		t.Fatalf("stream run = %d %s input=%#v", response.Code, response.Body.String(), ai.streamInput)
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/ai-service/runs/run-1/approve", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"executed"`) {
		t.Fatalf("approve run = %d %s", response.Code, response.Body.String())
	}
}

func TestAIAutomationRoutesPersistHistoryAndRunUnattendedByDefault(t *testing.T) {
	store := &apiAutomationStore{items: map[string]aiautomation.Automation{}}
	service, err := application.NewAIAutomationService(context.Background(), store, nil, apiAutomationRunner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	server := newTestServer()
	server.SetAIAutomationService(service)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/ai-service/automations", strings.NewReader(`{"name":"检查","enabled":true,"kind":"schedule","prompt":"检查状态","intervalSeconds":60}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"kind":"schedule"`) || !strings.Contains(response.Body.String(), `"executionMode":"unattended"`) {
		t.Fatalf("create AI automation = %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Data aiautomation.Automation `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Data.ID == "" {
		t.Fatalf("decode created automation: %#v err=%v", body, err)
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/ai-service/automations/"+body.Data.ID+"/run", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"executed"`) || !strings.Contains(response.Body.String(), `"runHistory"`) || !strings.Contains(response.Body.String(), `"autoApproved":true`) {
		t.Fatalf("run AI automation = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/ai-service/automations", strings.NewReader(`{"name":"人工确认","enabled":true,"kind":"schedule","prompt":"检查状态","executionMode":"manual","intervalSeconds":60}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create manual AI automation = %d %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Data.ID == "" {
		t.Fatalf("decode manual automation: %#v err=%v", body, err)
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/ai-service/automations/"+body.Data.ID+"/run", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"awaiting_approval"`) {
		t.Fatalf("run manual AI automation = %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/ai-service/automations/"+body.Data.ID+"/runs/run-task-1/approve", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"executed"`) {
		t.Fatalf("approve manual AI automation = %d %s", response.Code, response.Body.String())
	}
}

func TestDatabaseBackupAndRestoreStagingAPI(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testStoreCredentials(t)
	store, err := gormstore.Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := newTestServer()
	server.SetMaintenanceService(application.NewMaintenanceService(store, keyPath, gormstore.ValidateRestoreCandidate, gormstore.PendingRestorePaths, gormstore.WritePendingRestoreMarker))

	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/backup", bytes.NewBufferString(`{"confirmation":"BACKUP"}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.HasPrefix(response.Body.Bytes(), []byte("PK")) || !strings.Contains(response.Header().Get(echo.HeaderContentDisposition), "homeloom-backup-") {
		t.Fatalf("backup = %d, headers = %#v, bytes = %d", response.Code, response.Header(), response.Body.Len())
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "backup.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(response.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("confirmation", "RESTORE"); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/system/restore", &body)
	request.Header.Set(echo.HeaderContentType, form.FormDataContentType())
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"requiresRestart":true`) {
		t.Fatalf("restore stage = %d %s", response.Code, response.Body.String())
	}
}

func TestMasterKeyRotationAPIRequiresAdministratorCSRFAndDoesNotExposeKeys(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testStoreCredentials(t)
	store, err := gormstore.Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := newTestServer()
	server.SetMaintenanceService(application.NewMaintenanceService(store, keyPath, gormstore.ValidateRestoreCandidate, gormstore.PendingRestorePaths, gormstore.WritePendingRestoreMarker))
	auth := application.NewAuthService(store)
	server.SetAuthService(auth)
	session, err := auth.Setup(ctx, "admin", "a-long-password")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/system/master-key", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/system/master-key/rotate", bytes.NewBufferString(`{"confirmation":"ROTATE"}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("rotation without csrf = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/system/master-key/rotate", bytes.NewBufferString(`{"confirmation":"WRONG"}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	request.Header.Set(csrfHeaderName, session.CSRFToken)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "type ROTATE") {
		t.Fatalf("rotation wrong confirmation = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/system/master-key/rotate", bytes.NewBufferString(`{"confirmation":"ROTATE"}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	request.Header.Set(csrfHeaderName, session.CSRFToken)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"activeVersion":2`) || strings.Contains(response.Body.String(), "keys") || strings.Contains(response.Body.String(), "master-keyring") {
		t.Fatalf("rotation = %d %s", response.Code, response.Body.String())
	}
	if cacheControl := response.Header().Get(echo.HeaderCacheControl); cacheControl != "no-store" {
		t.Fatalf("rotation cache control = %q", cacheControl)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/system/master-key", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"activeVersion":2`) || strings.Contains(response.Body.String(), "keys") {
		t.Fatalf("rotation status = %d %s", response.Code, response.Body.String())
	}
}

func TestPairingMaintenanceRequiresExactConfirmation(t *testing.T) {
	server := newTestServer()
	for _, item := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/targets/apple-main/pairing/regenerate", `{"confirmation":"REGENERATE"}`},
		{http.MethodDelete, "/api/v1/targets/apple-main/pairing-identity", `{"confirmation":"CLEAR"}`},
	} {
		request := httptest.NewRequest(item.method, item.path, bytes.NewBufferString(item.body))
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "confirmation") {
			t.Fatalf("%s = %d %s", item.path, response.Code, response.Body.String())
		}
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

func TestTrustedProxyControlsClientIPAndSecureCookies(t *testing.T) {
	server := newTestServer()
	if err := server.SetTrustedProxies([]string{"192.0.2.0/24", "2001:db8::1"}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	request.RemoteAddr = "192.0.2.10:4567"
	request.Header.Set(echo.HeaderXForwardedFor, "198.51.100.7, 192.0.2.20")
	request.Header.Set("X-Forwarded-Proto", "https")
	if got := server.clientIP(request); got != "198.51.100.7" {
		t.Fatalf("trusted proxy client ip = %q", got)
	}
	if !server.secureCookieRequest(request) {
		t.Fatal("trusted HTTPS proxy did not produce secure cookies")
	}
	request.RemoteAddr = "203.0.113.9:4567"
	if got := server.clientIP(request); got != "203.0.113.9" {
		t.Fatalf("untrusted proxy client ip = %q", got)
	}
	if server.secureCookieRequest(request) {
		t.Fatal("untrusted forwarded protocol produced secure cookies")
	}
	if err := server.SetTrustedProxies([]string{"invalid"}); err == nil {
		t.Fatal("invalid trusted proxy was accepted")
	}
}

func newTestServerWithProvider(provider providersdk.Provider) *Server {
	logger := zap.NewNop()
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

func TestTargetCRUDAPIPersistsConfiguration(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	targets := application.NewTargetService(nil, store)
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	server := NewServer(":0", devices, targets, zap.NewNop())

	request := httptest.NewRequest(http.MethodPost, "/api/v1/targets", bytes.NewBufferString(`{"id":"api-bridge","type":"apple-hap","name":"API Bridge","enabled":true,"address":":51827","pin":"23456789","setupId":"API1"}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"api-bridge"`) || !strings.Contains(response.Body.String(), `"status":"disabled"`) {
		t.Fatalf("create target = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/api/v1/targets/api-bridge", bytes.NewBufferString(`{"type":"apple-hap","name":"Renamed Bridge","enabled":false,"address":":51827","pin":"23456789","setupId":"API1"}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"Renamed Bridge"`) {
		t.Fatalf("update target = %d %s", response.Code, response.Body.String())
	}
	persisted, err := store.ListTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range persisted {
		if item.ID == "api-bridge" {
			found = item.Name == "Renamed Bridge" && !item.Enabled && item.StorePath == "data/hap/api-bridge"
		}
	}
	if !found {
		t.Fatalf("updated target was not persisted: %#v", persisted)
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/v1/targets/api-bridge", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete target = %d %s", response.Code, response.Body.String())
	}
	persisted, err = store.ListTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range persisted {
		if item.ID == "api-bridge" {
			t.Fatalf("deleted target remains persisted: %#v", item)
		}
	}
}

func TestMatterTargetAPIAcceptsProtocolConfigWithoutLeakingPasscode(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	targets := application.NewTargetService(nil, store)
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	server := NewServer(":0", devices, targets, zap.NewNop())

	body := `{"id":"matter-api","type":"matter","name":"Matter API","enabled":false,"matterConfig":{"networkInterface":"en0","udpPort":5540,"discriminator":1234,"passcode":"20202021","vendorId":65521,"productId":32768,"productName":"HomeLoom","serialNumber":"matter-api","commissioningWindowSeconds":300}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/targets", bytes.NewBufferString(body))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("create Matter target = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "20202021") || strings.Contains(response.Body.String(), `"passcode"`) {
		t.Fatalf("Matter API leaked commissioning passcode: %s", response.Body.String())
	}
	for _, expected := range []string{`"networkInterface":"en0"`, `"udpPort":5540`, `"discriminator":1234`, `"certification":"test"`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("Matter API response missing %s: %s", expected, response.Body.String())
		}
	}
	persisted, err := store.ListTargets(ctx)
	var savedPasscode string
	for _, current := range persisted {
		if current.ID == "matter-api" && current.MatterConfig != nil {
			savedPasscode = current.MatterConfig.Passcode
		}
	}
	if err != nil || savedPasscode != "20202021" {
		t.Fatalf("persisted Matter config = %#v, %v", persisted, err)
	}
}

func TestMatterDangerousAPIsRequireExactConfirmation(t *testing.T) {
	targets := application.NewTargetService(nil, nil)
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	server := NewServer(":0", devices, targets, zap.NewNop())
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/v1/targets/matter/commissioning-window", `{"durationSeconds":300}`},
		{http.MethodDelete, "/api/v1/targets/matter/fabrics/fabric-1", `{"confirmation":"DELETE FABRIC matter"}`},
		{http.MethodPost, "/api/v1/targets/matter/factory-reset", `{"confirmation":"FACTORY RESET"}`},
		{http.MethodPost, "/api/v1/targets/matter/endpoints/lamp/device-type", `{"deviceType":"lightbulb","confirmation":"CHANGE ENDPOINT TYPE matter lamp switch"}`},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"confirmation"`) {
			t.Fatalf("%s %s = %d %s", test.method, test.path, response.Code, response.Body.String())
		}
	}
}

func TestDeviceEnabledAPIIsPersisted(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := application.NewDeviceService(virtual.NewProvider(), store)
	defer service.Close()
	if err := service.LoadDevicePreferences(ctx); err != nil {
		t.Fatal(err)
	}
	server := NewServer(":0", service, application.NewTargetService(nil, nil), zap.NewNop())
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

func TestDeviceNameAPIOverridesAndRestoresSourceName(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := application.NewDeviceService(virtual.NewProvider(), store)
	defer service.Close()
	if err := service.LoadDevicePreferences(ctx); err != nil {
		t.Fatal(err)
	}
	server := NewServer(":0", service, application.NewTargetService(nil, nil), zap.NewNop())
	update := httptest.NewRequest(http.MethodPut, "/api/v1/devices/virtual-switch-1/name", bytes.NewBufferString(`{"name":"玄关主开关"}`))
	update.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, update)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"name":"玄关主开关"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"nameOverridden":true`)) {
		t.Fatalf("name update = %d %s", response.Code, response.Body.String())
	}
	preferences, err := store.ListDeviceNamePreferences(ctx)
	if err != nil || len(preferences) != 1 || preferences[0].Name != "玄关主开关" {
		t.Fatalf("stored names = %#v, %v", preferences, err)
	}
	reset := httptest.NewRequest(http.MethodDelete, "/api/v1/devices/virtual-switch-1/name", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, reset)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"name":"客厅开关"`)) || bytes.Contains(response.Body.Bytes(), []byte(`"nameOverridden":true`)) {
		t.Fatalf("name reset = %d %s", response.Code, response.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodPut, "/api/v1/devices/virtual-switch-1/name", bytes.NewBufferString(`{"name":""}`))
	invalid.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, invalid)
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"name"`)) {
		t.Fatalf("invalid name = %d %s", response.Code, response.Body.String())
	}
}

func TestDeviceLocationAPIOverridesAndRestoresProviderLocation(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := application.NewDeviceService(virtual.NewProvider(), store)
	defer service.Close()
	if err := service.LoadDevicePreferences(ctx); err != nil {
		t.Fatal(err)
	}
	server := NewServer(":0", service, application.NewTargetService(nil, nil), zap.NewNop())
	homeRequest := httptest.NewRequest(http.MethodPost, "/api/v1/locations/homes", bytes.NewBufferString(`{"name":"我的家"}`))
	homeRequest.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, homeRequest)
	if response.Code != http.StatusCreated {
		t.Fatalf("create home = %d %s", response.Code, response.Body.String())
	}
	homes, err := store.ListDeviceLocationHomes(ctx)
	if err != nil || len(homes) != 1 {
		t.Fatalf("homes = %#v, %v", homes, err)
	}
	roomRequest := httptest.NewRequest(http.MethodPost, "/api/v1/locations/homes/"+homes[0].ID+"/rooms", bytes.NewBufferString(`{"name":"书房"}`))
	roomRequest.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, roomRequest)
	if response.Code != http.StatusCreated {
		t.Fatalf("create room = %d %s", response.Code, response.Body.String())
	}
	homes, _ = store.ListDeviceLocationHomes(ctx)
	listLocations := httptest.NewRequest(http.MethodGet, "/api/v1/locations", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, listLocations)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"name":"我的家"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"name":"书房"`)) {
		t.Fatalf("list locations = %d %s", response.Code, response.Body.String())
	}
	customBody := fmt.Sprintf(`{"mode":"custom","homeId":%q,"roomId":%q}`, homes[0].ID, homes[0].Rooms[0].ID)
	custom := httptest.NewRequest(http.MethodPut, "/api/v1/devices/virtual-switch-1/location", bytes.NewBufferString(customBody))
	custom.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, custom)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"locationMode":"custom"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"roomName":"书房"`)) {
		t.Fatalf("custom location response = %d %s", response.Code, response.Body.String())
	}
	locations, err := store.ListDeviceLocationPreferences(ctx)
	if err != nil || len(locations) != 1 || locations[0].HomeName != "我的家" || locations[0].RoomName != "书房" {
		t.Fatalf("stored locations = %#v, %v", locations, err)
	}
	deleteAssigned := httptest.NewRequest(http.MethodDelete, "/api/v1/locations/homes/"+homes[0].ID, nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, deleteAssigned)
	if response.Code != http.StatusConflict {
		t.Fatalf("delete assigned home = %d %s", response.Code, response.Body.String())
	}
	inherit := httptest.NewRequest(http.MethodPut, "/api/v1/devices/virtual-switch-1/location", bytes.NewBufferString(`{"mode":"source"}`))
	inherit.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, inherit)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"locationMode":"source"`)) {
		t.Fatalf("source location response = %d %s", response.Code, response.Body.String())
	}
	locations, _ = store.ListDeviceLocationPreferences(ctx)
	if len(locations) != 0 {
		t.Fatalf("source mode retained custom locations = %#v", locations)
	}
	invalid := httptest.NewRequest(http.MethodPut, "/api/v1/devices/virtual-switch-1/location", bytes.NewBufferString(`{"mode":"custom","roomId":"missing"}`))
	invalid.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, invalid)
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"homeId"`)) {
		t.Fatalf("invalid location response = %d %s", response.Code, response.Body.String())
	}
}

func TestMutationAuditAndCommandCorrelationID(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	devices := application.NewDeviceService(virtual.NewProvider(), store)
	defer devices.Close()
	server := NewServer(":0", devices, application.NewTargetService(nil, nil), zap.NewNop())
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
	for _, expected := range []string{`"correlationId":"trace-command-1"`, `"resourceType":"device"`, `"resourceId":"virtual-switch-1"`, `"outcome":"succeeded"`, `"label":"目标属性"`, `"value":"main.switch.power"`, `"label":"属性变更"`, `"value":"power: off → on"`} {
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
	server := NewServer(":0", devices, targets, zap.NewNop(), providers)
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
		`"deviceType":"thermostat"`, `"propertyId":"target-temperature"`, `"name":"恒温器"`, `"builtIn":true`,
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

func TestRuntimeLogsAPIUsesCursorAndDisablesCaching(t *testing.T) {
	devices := application.NewDeviceService(nil, nil)
	server := NewServer(":0", devices, application.NewTargetService(nil, nil), zap.NewNop())
	logs := subprocesslog.New(10)
	logs.Append("matter", "matter-main", []byte("ready"))
	logs.Append("camera-kernel", "camera-1", []byte("streaming"))
	server.SetSubprocessLogs(logs)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/system/runtime-logs?after=1&limit=5", nil)
	recorder := httptest.NewRecorder()
	server.echo.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d, cache = %q, body = %s", recorder.Code, recorder.Header().Get("Cache-Control"), recorder.Body.String())
	}
	var response struct {
		Data []subprocesslog.Entry `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].Message != "streaming" || response.Data[0].Sequence != 2 {
		t.Fatalf("logs = %#v", response.Data)
	}
}

func TestReadinessReflectsDatabaseHealth(t *testing.T) {
	logger := zap.NewNop()
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

func TestProviderScanEndpointUsesTransientDiscoveryCapability(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/providers/scan", bytes.NewBufferString(`{"type":"scan","enabled":false,"config":{}}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	newProviderScanTestServer(t).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"host":"192.0.2.20"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"mac":"aabbccddeeff"`)) {
		t.Fatalf("provider scan response = %d %s", response.Code, response.Body.String())
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
	logger := zap.NewNop()
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

func TestXiaomiSubdeviceDirectoryRequiresRunningXiaomiProvider(t *testing.T) {
	server := newProviderManagementTestServer(t)

	missing := httptest.NewRequest(http.MethodGet, "/api/v1/xiaomi/providers/missing/devices", nil)
	missingResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusConflict || !strings.Contains(missingResponse.Body.String(), "enabled and connected") {
		t.Fatalf("missing provider response = %d %s", missingResponse.Code, missingResponse.Body.String())
	}

	wrongType := httptest.NewRequest(http.MethodGet, "/api/v1/xiaomi/providers/virtual-main/devices", nil)
	wrongTypeResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(wrongTypeResponse, wrongType)
	if wrongTypeResponse.Code != http.StatusBadRequest || !strings.Contains(wrongTypeResponse.Body.String(), "not a Xiaomi") {
		t.Fatalf("wrong provider response = %d %s", wrongTypeResponse.Code, wrongTypeResponse.Body.String())
	}
}

func TestXiaomiMIoTCloudDirectoryIsNotAliasedToCentralHub(t *testing.T) {
	server := newProviderManagementTestServer(t)
	wrongType := httptest.NewRequest(http.MethodGet, "/api/v1/xiaomi-miot-cloud/providers/virtual-main/devices", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, wrongType)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "third-party cloud") {
		t.Fatalf("wrong provider response = %d %s", response.Code, response.Body.String())
	}
	missing := httptest.NewRequest(http.MethodGet, "/api/v1/xiaomi-miot-cloud/providers/missing/devices", nil)
	missingResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusConflict || !strings.Contains(missingResponse.Body.String(), "enabled and connected") {
		t.Fatalf("missing provider response = %d %s", missingResponse.Code, missingResponse.Body.String())
	}
}

func TestSonoffDirectoryRequiresRunningSonoffProvider(t *testing.T) {
	server := newProviderManagementTestServer(t)

	missing := httptest.NewRequest(http.MethodGet, "/api/v1/sonoff/providers/missing/devices", nil)
	missingResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusConflict || !strings.Contains(missingResponse.Body.String(), "enabled and connected") {
		t.Fatalf("missing provider response = %d %s", missingResponse.Code, missingResponse.Body.String())
	}

	wrongType := httptest.NewRequest(http.MethodGet, "/api/v1/sonoff/providers/virtual-main/devices", nil)
	wrongTypeResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(wrongTypeResponse, wrongType)
	if wrongTypeResponse.Code != http.StatusBadRequest || !strings.Contains(wrongTypeResponse.Body.String(), "not a Sonoff") {
		t.Fatalf("wrong provider response = %d %s", wrongTypeResponse.Code, wrongTypeResponse.Body.String())
	}
}

func TestTuyaDirectoryRequiresRunningTuyaProvider(t *testing.T) {
	server := newProviderManagementTestServer(t)

	missing := httptest.NewRequest(http.MethodGet, "/api/v1/tuya/providers/missing/devices", nil)
	missingResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusConflict || !strings.Contains(missingResponse.Body.String(), "enabled and connected") {
		t.Fatalf("missing provider response = %d %s", missingResponse.Code, missingResponse.Body.String())
	}

	wrongType := httptest.NewRequest(http.MethodGet, "/api/v1/tuya/providers/virtual-main/devices", nil)
	wrongTypeResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(wrongTypeResponse, wrongType)
	if wrongTypeResponse.Code != http.StatusBadRequest || !strings.Contains(wrongTypeResponse.Body.String(), "not a Tuya") {
		t.Fatalf("wrong provider response = %d %s", wrongTypeResponse.Code, wrongTypeResponse.Body.String())
	}
}

func TestXiaomiMIoTCloudLoginAPIValidatesTwoStepRequests(t *testing.T) {
	server := newTestServer()
	start := httptest.NewRequest(http.MethodPost, "/api/v1/xiaomi-miot-cloud/login/start", bytes.NewBufferString(`{"region":"cn"}`))
	start.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	startResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusBadRequest || !strings.Contains(startResponse.Body.String(), "Xiaomi account is required") {
		t.Fatalf("start response = %d %s", startResponse.Code, startResponse.Body.String())
	}

	verify := httptest.NewRequest(http.MethodPost, "/api/v1/xiaomi-miot-cloud/login/verify", bytes.NewBufferString(`{"challengeId":"","code":""}`))
	verify.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	verifyResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(verifyResponse, verify)
	if verifyResponse.Code != http.StatusBadRequest || !strings.Contains(verifyResponse.Body.String(), "challengeId and verification code are required") {
		t.Fatalf("verify response = %d %s", verifyResponse.Code, verifyResponse.Body.String())
	}
}

func TestXiaomiProviderAuthChallengeAPIReadsAndVerifiesWithoutSecrets(t *testing.T) {
	state := &apiAuthState{url: "https://account.xiaomi.com/identity/authStart?context=short"}
	factory := providersdk.NewFactory()
	if err := factory.Register(xiaomi.XiaomiMIoTCloudProviderType, func(item providerconfig.Config) (providersdk.Provider, error) {
		return &apiAuthProvider{id: item.ID, state: state}, nil
	}); err != nil {
		t.Fatal(err)
	}
	store := &apiProviderStore{items: make(map[string]providerconfig.Config)}
	service := application.NewProviderService(nil, store, factory, &apiAuthRuntime{state: state})
	config := providerconfig.Config{ID: "cloud-main", Type: xiaomi.XiaomiMIoTCloudProviderType, Name: "Cloud", Enabled: true, Config: json.RawMessage(`{"region":"cn","username":"owner","password":"not-in-response","devices":[]}`)}
	_, saveErr := service.Save(context.Background(), config)
	if saveErr == nil {
		t.Fatal("expected auth challenge from provider save")
	}
	server := NewServer(":0", application.NewDeviceService(virtual.NewProvider()), application.NewTargetService(nil, nil), zap.NewNop(), service)
	challengeResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(challengeResponse, httptest.NewRequest(http.MethodGet, "/api/v1/xiaomi-miot-cloud/providers/cloud-main/auth-challenge", nil))
	if challengeResponse.Code != http.StatusOK || strings.Contains(challengeResponse.Body.String(), "not-in-response") || !strings.Contains(challengeResponse.Body.String(), "challengeId") {
		t.Fatalf("challenge response=%d %s", challengeResponse.Code, challengeResponse.Body.String())
	}
	var challengeBody struct {
		Data struct {
			ChallengeID string `json:"challengeId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(challengeResponse.Body.Bytes(), &challengeBody); err != nil || challengeBody.Data.ChallengeID == "" {
		t.Fatalf("challenge body=%s error=%v", challengeResponse.Body.String(), err)
	}
	verifyRequest := httptest.NewRequest(http.MethodPost, "/api/v1/xiaomi-miot-cloud/providers/cloud-main/auth-challenge/verify", strings.NewReader(`{"challengeId":"`+challengeBody.Data.ChallengeID+`","code":"123456"}`))
	verifyRequest.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	verifyResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(verifyResponse, verifyRequest)
	if verifyResponse.Code != http.StatusOK || !strings.Contains(verifyResponse.Body.String(), `"status":"running"`) || strings.Contains(verifyResponse.Body.String(), "not-in-response") {
		t.Fatalf("verify response=%d %s", verifyResponse.Code, verifyResponse.Body.String())
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
			for _, name := range []string{"homeloom_event_average_latency_milliseconds", "homeloom_slow_event_handlers_total", "homeloom_database_operations_total", "homeloom_homekit_pushes_total", "homeloom_devices_unknown", "homeloom_command_queue_pending", "homeloom_command_queue_max_pending", "homeloom_commands_outcome_unknown_total", "homeloom_commands_coalesced_total", "homeloom_provider_clock_skew_events_total", "homeloom_provider_snapshot_age_events_total", "homeloom_provider_max_snapshot_age_milliseconds"} {
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
	store, err := openTestStore(t, ctx)
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
	server := NewServer(":0", devices, application.NewTargetService(nil, nil), zap.NewNop())
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

	createBinding := httptest.NewRequest(http.MethodPost, "/api/v1/mapping/bindings", bytes.NewBufferString(`{"profileId":"custom-invert","providerId":"virtual-main","deviceId":"virtual-switch-1","endpointId":"main","capabilityId":"switch","propertyId":"power","enabled":true,"readbackEnabled":true,"readbackDelaysMs":[250,1000,3000]}`))
	createBinding.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, createBinding)
	if response.Code != http.StatusCreated || !bytes.Contains(response.Body.Bytes(), []byte(`"profileId":"custom-invert"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"readbackEnabled":true`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"readbackDelaysMs":[250,1000,3000]`)) {
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

func TestMappingCatalogCustomPropertyAndConsumerRouteAPI(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles, err := application.NewProfileService(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	devices := application.NewDeviceService(virtual.NewProvider(), profiles)
	defer devices.Close()
	server := NewServer(":0", devices, application.NewTargetService(nil, nil), zap.NewNop())
	server.SetProfileService(profiles)

	create := httptest.NewRequest(http.MethodPost, "/api/v1/device-models/custom-properties", bytes.NewBufferString(`{"id":"switch-led-pattern","deviceType":"switch","endpointId":"main","endpointName":"Main","endpointType":"main","capabilityId":"vendor-acme","capabilityType":"vendor-acme","definition":{"id":"led-pattern","name":"LED Pattern","type":"enum","enum":["off","pulse"],"readable":true,"writable":true,"notifiable":true}}`))
	create.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, create)
	if response.Code != http.StatusCreated || !bytes.Contains(response.Body.Bytes(), []byte(`"parameterLevel":"custom"`)) {
		t.Fatalf("custom create = %d %s", response.Code, response.Body.String())
	}

	catalog := httptest.NewRequest(http.MethodGet, "/api/v1/mapping/catalog", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, catalog)
	for _, expected := range []string{`"providers"`, `"models"`, `"consumers"`, `"led-pattern"`, `"Switch.On"`, `"catalog"`, `"complete":true`, `"source":"provider-discovery"`} {
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Fatalf("catalog = %d %s", response.Code, response.Body.String())
		}
	}
	consumers := httptest.NewRequest(http.MethodGet, "/api/v1/mapping/consumers", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, consumers)
	for _, expected := range []string{`"id":"homekit"`, `"deviceType":"air-conditioner"`, `"deviceType":"thermostat"`, `"deviceType":"speaker"`} {
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Fatalf("consumer catalogs = %d %s", response.Code, response.Body.String())
		}
	}

	consumerBinding := httptest.NewRequest(http.MethodPost, "/api/v1/mapping/bindings", bytes.NewBufferString(`{"stage":"consumer","providerId":"virtual-main","deviceId":"virtual-switch-1","deviceType":"switch","modelEndpointId":"main","modelCapabilityId":"switch","modelPropertyId":"power","consumerId":"homekit","consumerProperty":"Switch.On","enabled":true}`))
	consumerBinding.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, consumerBinding)
	if response.Code != http.StatusCreated || !bytes.Contains(response.Body.Bytes(), []byte(`"stage":"consumer"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"consumerProperty":"Switch.On"`)) {
		t.Fatalf("consumer binding = %d %s", response.Code, response.Body.String())
	}
}

func TestCustomUnifiedModelAPI(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles, err := application.NewProfileService(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	devices := application.NewDeviceService(virtual.NewProvider(), profiles)
	defer devices.Close()
	server := NewServer(":0", devices, application.NewTargetService(nil, nil), zap.NewNop())
	server.SetProfileService(profiles)

	create := httptest.NewRequest(http.MethodPost, "/api/v1/device-models/custom-models", bytes.NewBufferString(`{"deviceType":"air-quality-monitor","name":"空气质量监测器","version":1}`))
	create.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, create)
	if response.Code != http.StatusCreated || !bytes.Contains(response.Body.Bytes(), []byte(`"deviceType":"air-quality-monitor"`)) {
		t.Fatalf("custom model create = %d %s", response.Code, response.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v1/device-models", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, list)
	for _, expected := range []string{`"name":"空气质量监测器"`, `"builtIn":false`, `"parameters":[]`} {
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(expected)) {
			t.Fatalf("custom model list = %d %s", response.Code, response.Body.String())
		}
	}

	remove := httptest.NewRequest(http.MethodDelete, "/api/v1/device-models/custom-models/air-quality-monitor", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, remove)
	if response.Code != http.StatusNoContent {
		t.Fatalf("custom model delete = %d %s", response.Code, response.Body.String())
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

func TestUnifiedEventStreamPublishesDeviceChanges(t *testing.T) {
	httpServer := httptest.NewServer(newTestServer().Handler())
	defer httpServer.Close()
	request, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/api/v1/events", nil)
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

func TestUnifiedEventStreamPublishesBoundedRuntimeLogsWithCursor(t *testing.T) {
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	logs := subprocesslog.New(10)
	logs.Append("backend", "main", []byte(`{"level":"info","msg":"first"}`))
	logs.Append("matter", "matter-main", []byte(`{"level":"info","msg":"token=do-not-leak runtime ready"}`))
	server := NewServer(":0", devices, application.NewTargetService(nil, nil), zap.NewNop())
	server.SetSubprocessLogs(logs)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	request, err := http.NewRequest(http.MethodGet, httpServer.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Last-Event-ID", "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control = %q", response.Header.Get("Cache-Control"))
	}
	scanner := bufio.NewScanner(response.Body)
	if !scanner.Scan() || scanner.Text() != "event: ready" {
		t.Fatalf("ready = %q", scanner.Text())
	}
	if !scanner.Scan() || scanner.Text() != "data: {}" || !scanner.Scan() || scanner.Text() != "" {
		t.Fatalf("malformed ready event")
	}
	if !scanner.Scan() || scanner.Text() != "id: 2" {
		t.Fatalf("replayed id = %q", scanner.Text())
	}
	if !scanner.Scan() || scanner.Text() != "event: runtime-log" {
		t.Fatalf("replayed event = %q", scanner.Text())
	}
	if !scanner.Scan() {
		t.Fatal("missing runtime log data")
	}
	payload := scanner.Text()
	if !strings.Contains(payload, `"sequence":2`) || !strings.Contains(payload, `"message":"token=******** runtime ready"`) || strings.Contains(payload, "do-not-leak") {
		t.Fatalf("replayed log = %q", payload)
	}
}

func TestUnifiedEventStreamSignalsRuntimeLogGapWhenReplayWindowWasOverwritten(t *testing.T) {
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	logs := subprocesslog.New(2)
	logs.Append("backend", "main", []byte("first"))
	logs.Append("backend", "main", []byte("second"))
	logs.Append("backend", "main", []byte("third"))
	server := NewServer(":0", devices, application.NewTargetService(nil, nil), zap.NewNop())
	server.SetSubprocessLogs(logs)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	response, err := http.Get(httpServer.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	for index, want := range []string{"event: ready", "data: {}", "", "event: runtime-log-gap"} {
		if !scanner.Scan() || scanner.Text() != want {
			t.Fatalf("line %d = %q, want %q", index, scanner.Text(), want)
		}
	}
	if !scanner.Scan() {
		t.Fatal("missing runtime-log-gap payload")
	}
	var gap runtimeLogGap
	if err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &gap); err != nil {
		t.Fatal(err)
	}
	if gap != (runtimeLogGap{After: 0, Before: 2}) {
		t.Fatalf("gap = %#v", gap)
	}
}

func TestLegacySplitEventStreamsAreRemoved(t *testing.T) {
	httpServer := httptest.NewServer(newTestServer().Handler())
	defer httpServer.Close()
	for _, path := range []string{"audit", "devices", "runtime", "commands", "states", "targets"} {
		response, err := http.Get(httpServer.URL + "/api/v1/events/" + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("legacy event stream %q status = %d", path, response.StatusCode)
		}
	}
}

func TestRuntimeChangesOnlyIncludesChangedCategories(t *testing.T) {
	providers := []map[string]any{{"id": "xiaomi", "status": "running"}}
	diagnostics := map[string]any{"eventsProcessed": float64(3)}
	previousProviders, _ := json.Marshal(providers)
	previousDiagnostics, _ := json.Marshal(diagnostics)

	delta, _, _ := runtimeChanges(previousProviders, previousDiagnostics, providers, diagnostics)
	if len(delta) != 0 {
		t.Fatalf("unchanged runtime delta = %#v", delta)
	}
	diagnostics["eventsProcessed"] = float64(4)
	delta, encodedProviders, encodedDiagnostics := runtimeChanges(previousProviders, previousDiagnostics, providers, diagnostics)
	if len(delta) != 1 || delta["diagnostics"] == nil {
		t.Fatalf("diagnostic runtime delta = %#v", delta)
	}
	if !bytes.Equal(encodedProviders, previousProviders) || bytes.Equal(encodedDiagnostics, previousDiagnostics) {
		t.Fatalf("encoded runtime snapshots did not preserve category changes")
	}
}

func TestUnifiedEventStreamPublishesCommandLifecycle(t *testing.T) {
	httpServer := httptest.NewServer(newTestServer().Handler())
	defer httpServer.Close()
	response, err := http.Get(httpServer.URL + "/api/v1/events")
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
	commandEvent := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			commandEvent = line == "event: command"
			continue
		}
		if !commandEvent || !strings.HasPrefix(line, "data: ") {
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
	// Provider state can confirm a fast local write before the accepted
	// notification is delivered. Confirmed already implies acceptance, so the
	// stream contract permits queued -> sent -> confirmed.
	if len(statuses) < 3 || statuses[0] != "queued" || statuses[1] != "sent" || statuses[len(statuses)-1] != "confirmed" {
		t.Fatalf("statuses = %#v", statuses)
	}
}

func TestUnifiedEventStreamPublishesPersistedAuditMutation(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	devices := application.NewDeviceService(virtual.NewProvider(), store)
	defer devices.Close()
	server := NewServer(":0", devices, application.NewTargetService(nil, nil), zap.NewNop())
	server.SetAuditService(application.NewAuditService(store))
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	response, err := http.Get(httpServer.URL + "/api/v1/events")
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

func TestUnifiedEventStreamPublishesStateQualityChanges(t *testing.T) {
	httpServer := httptest.NewServer(newTestServer().Handler())
	defer httpServer.Close()
	response, err := http.Get(httpServer.URL + "/api/v1/events")
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

func TestUnifiedEventStreamPublishesTargetRuntimeStatus(t *testing.T) {
	logger := zap.NewNop()
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	targets := application.NewTargetService([]application.TargetRegistration{{Info: application.TargetInfo{ID: "bridge", Name: "Bridge", Status: "running"}}}, nil)
	httpServer := httptest.NewServer(NewServer(":0", devices, targets, logger).Handler())
	defer httpServer.Close()
	response, err := http.Get(httpServer.URL + "/api/v1/events")
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
	logger := zap.NewNop()
	targets := application.NewTargetService(nil, nil)
	server := NewServer(":0", devices, targets, logger, providers)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/providers/virtual-main/restart", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"status":"running"`)) {
		t.Fatalf("restart response = %d %s", response.Code, response.Body.String())
	}
}

func TestRevokeProviderCredentialsEndpointRequiresConfirmationAndReturnsSafeOutcome(t *testing.T) {
	config := providerconfig.Config{ID: "xiaomi-main", Type: "xiaomi", Name: "Xiaomi", Enabled: true, Config: json.RawMessage(`{"accessToken":"secret-token","devices":[]}`)}
	factory := providersdk.NewFactory()
	if err := factory.Register("xiaomi", func(item providerconfig.Config) (providersdk.Provider, error) {
		return &apiCredentialRevokingProvider{id: item.ID}, nil
	}); err != nil {
		t.Fatal(err)
	}
	store := &apiProviderStore{items: map[string]providerconfig.Config{config.ID: config}}
	runtime := &apiCredentialRevocationRuntime{}
	providers := application.NewProviderService([]providerconfig.Config{config}, store, factory, runtime)
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	server := NewServer(":0", devices, application.NewTargetService(nil, nil), zap.NewNop(), providers)

	incorrect := httptest.NewRecorder()
	incorrectRequest := httptest.NewRequest(http.MethodPost, "/api/v1/providers/xiaomi-main/credentials/revoke", strings.NewReader(`{"confirmation":"REVOKE another"}`))
	incorrectRequest.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	server.Handler().ServeHTTP(incorrect, incorrectRequest)
	if incorrect.Code != http.StatusBadRequest {
		t.Fatalf("incorrect confirmation = %d %s", incorrect.Code, incorrect.Body.String())
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/providers/xiaomi-main/credentials/revoke", strings.NewReader(`{"confirmation":"REVOKE xiaomi-main"}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"localRevoked":true`)) || bytes.Contains(response.Body.Bytes(), []byte("secret-token")) || response.Header().Get(echo.HeaderCacheControl) != "no-store" {
		t.Fatalf("revocation response = %d %s headers=%v", response.Code, response.Body.String(), response.Header())
	}
	if saved := store.items[config.ID]; saved.Enabled || !bytes.Contains(saved.Config, []byte(`"credentialsRevoked":true`)) || len(runtime.removed) != 1 {
		t.Fatalf("saved=%#v removed=%v", saved, runtime.removed)
	}
}
