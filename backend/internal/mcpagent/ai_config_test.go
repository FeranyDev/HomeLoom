package mcpagent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memoryAIConfigStore struct {
	config AIServiceConfig
	err    error
}

func (s *memoryAIConfigStore) Load() (AIServiceConfig, error) { return s.config, s.err }
func (s *memoryAIConfigStore) Save(config AIServiceConfig) error {
	s.config = config
	s.err = nil
	return nil
}

type catalogModel struct{ models []AIModel }

func (catalogModel) Start(context.Context, string, string) (ModelResponse, error) {
	return ModelResponse{}, nil
}
func (catalogModel) Continue(context.Context, string, string, []ToolOutput) (ModelResponse, error) {
	return ModelResponse{}, nil
}
func (m catalogModel) ListModels(context.Context) ([]AIModel, error) { return m.models, nil }

func TestFileAIConfigStorePersistsPrivateConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp", "ai-config.json")
	store, err := NewFileAIConfigStore(path)
	if err != nil {
		t.Fatal(err)
	}
	config := AIServiceConfig{APIBaseURL: "https://models.example.test/v1", APIKey: "secret-api-key", Model: "model-a", APIProtocol: AIAPIProtocolResponses, AgentInstructions: "使用简洁的中文回复。"}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}
	loaded, err := store.Load()
	if err != nil || loaded != config {
		t.Fatalf("load = %#v, %v", loaded, err)
	}
	encoded, _ := json.Marshal(loaded.status())
	if strings.Contains(string(encoded), "secret-api-key") || !strings.Contains(string(encoded), `"apiKeyConfigured":true`) || !strings.Contains(string(encoded), "使用简洁的中文回复。") || !strings.Contains(string(encoded), DefaultAgentInstructions) {
		t.Fatalf("status leaked or omitted key state: %s", encoded)
	}
}

func TestRuntimeAIServiceConfigurationAndModelDiscoveryDoNotExposeKey(t *testing.T) {
	runtime, err := NewRuntime("/tmp/homeloom-core.sock", strings.Repeat("t", 24), nil)
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryAIConfigStore{err: os.ErrNotExist}
	if err := runtime.ConfigureAIService(AIServiceConfig{}, store, func(AIServiceConfig) (Model, error) {
		return catalogModel{models: []AIModel{{ID: "beta"}, {ID: "alpha"}}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/ai/config", strings.NewReader(`{"apiBaseUrl":"https://models.example.test/v1","apiProxyUrl":"http://127.0.0.1:7890","apiKey":"secret-api-key","model":"beta","apiProtocol":"responses","agentInstructions":"每次回复用 Markdown 列表。"}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 24))
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "secret-api-key") || !strings.Contains(response.Body.String(), `"configured":true`) {
		t.Fatalf("save = %d %s", response.Code, response.Body.String())
	}
	if store.config.APIKey != "secret-api-key" || store.config.APIProxyURL != "http://127.0.0.1:7890" || store.config.AgentInstructions != "每次回复用 Markdown 列表。" {
		t.Fatalf("stored config = %#v", store.config)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/ai/models", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 24))
	response = httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"alpha"`) || strings.Contains(response.Body.String(), "secret-api-key") {
		t.Fatalf("models = %d %s", response.Code, response.Body.String())
	}
}

type instructionModel struct{ instructions string }

func (m *instructionModel) Start(_ context.Context, instructions, _ string) (ModelResponse, error) {
	m.instructions = instructions
	return ModelResponse{ID: "response-1", Text: "完成"}, nil
}
func (*instructionModel) Continue(context.Context, string, string, []ToolOutput) (ModelResponse, error) {
	return ModelResponse{}, nil
}

func TestRuntimeUsesConfiguredAgentInstructions(t *testing.T) {
	runtime, err := NewRuntime("/tmp/homeloom-core.sock", strings.Repeat("t", 24), nil)
	if err != nil {
		t.Fatal(err)
	}
	model := &instructionModel{}
	store := &memoryAIConfigStore{config: AIServiceConfig{APIBaseURL: "https://models.example.test/v1", APIKey: "secret-api-key", Model: "model-a", AgentInstructions: "只用一句话回答。"}}
	if err := runtime.ConfigureAIService(AIServiceConfig{}, store, func(AIServiceConfig) (Model, error) { return model, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Run(context.Background(), "设备状态如何？"); err != nil {
		t.Fatal(err)
	}
	if model.instructions != "只用一句话回答。" {
		t.Fatalf("instructions = %q", model.instructions)
	}
}

func TestAIServiceConfigDefaultsAndLimitsAgentInstructions(t *testing.T) {
	config, err := (AIServiceConfig{}).normalized()
	if err != nil || config.AgentInstructions != DefaultAgentInstructions {
		t.Fatalf("default config = %#v, %v", config, err)
	}
	if _, err := (AIServiceConfig{AgentInstructions: strings.Repeat("a", maxAgentInstructionsBytes+1)}).normalized(); err == nil {
		t.Fatal("oversized Agent instructions were accepted")
	}
}

func TestAIServiceConfigUsesValidatedForwardProxy(t *testing.T) {
	config, err := (AIServiceConfig{
		APIBaseURL:  "https://models.example.test/v1",
		APIProxyURL: "http://127.0.0.1:7890/",
		APIKey:      "api-key",
		Model:       "model-a",
	}).normalized()
	if err != nil || config.APIProxyURL != "http://127.0.0.1:7890" {
		t.Fatalf("proxy config = %#v, %v", config, err)
	}
	model, err := NewResponsesAPIModel(config)
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := model.Client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", model.Client.Transport)
	}
	proxy, err := transport.Proxy(httptest.NewRequest(http.MethodGet, "https://provider.example.test/models", nil))
	if err != nil || proxy == nil || proxy.String() != "http://127.0.0.1:7890" {
		t.Fatalf("proxy = %v, %v", proxy, err)
	}
	for _, proxyURL := range []string{"socks5://127.0.0.1:7890", "http://name:password@127.0.0.1:7890", "http://127.0.0.1:7890/?token=secret"} {
		if _, err := (AIServiceConfig{APIProxyURL: proxyURL}).normalized(); err == nil {
			t.Fatalf("invalid proxy %q was accepted", proxyURL)
		}
	}
}

func TestAIClientTimeoutsAllowExtendedReasoningWithoutExtendingConfigurationCalls(t *testing.T) {
	config := AIServiceConfig{APIBaseURL: "https://models.example.test/v1", APIKey: "api-key", Model: "model-a"}
	responses, err := NewResponsesAPIModel(config)
	if err != nil || responses.Client.Timeout != AIProviderRequestTimeout {
		t.Fatalf("responses timeout = %v, %v", responses.Client.Timeout, err)
	}
	chat, err := NewChatCompletionsModel(AIServiceConfig{APIBaseURL: "https://models.example.test/v1", APIKey: "api-key", Model: "model-a", APIProtocol: AIAPIProtocolChatCompletions})
	if err != nil || chat.Client.Timeout != AIProviderRequestTimeout {
		t.Fatalf("chat timeout = %v, %v", chat.Client.Timeout, err)
	}
	tokenPath := filepath.Join(t.TempDir(), "agent.token")
	if err := os.WriteFile(tokenPath, []byte(strings.Repeat("t", 24)), 0o600); err != nil {
		t.Fatal(err)
	}
	control, err := NewAgentControlClient("http://127.0.0.1:8091", tokenPath)
	if err != nil || control.client.Timeout != agentControlTimeout || control.clientWithTimeout(AIRunControlTimeout).Timeout != AIRunControlTimeout {
		t.Fatalf("control timeout = %#v, %v", control, err)
	}
}

type timeoutModel struct{}

func (timeoutModel) Start(context.Context, string, string) (ModelResponse, error) {
	return ModelResponse{}, context.DeadlineExceeded
}
func (timeoutModel) Continue(context.Context, string, string, []ToolOutput) (ModelResponse, error) {
	return ModelResponse{}, context.DeadlineExceeded
}

func TestRuntimeHTTPReturnsGatewayTimeoutForProviderTimeout(t *testing.T) {
	runtime, err := NewRuntime("/tmp/homeloom-core.sock", strings.Repeat("t", 24), timeoutModel{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/runs", strings.NewReader(`{"message":"请分析设备"}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 24))
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusGatewayTimeout || !strings.Contains(response.Body.String(), "timed out") {
		t.Fatalf("timeout response = %d %s", response.Code, response.Body.String())
	}
}

func TestResponsesAPIModelListsModelsWithBearerAuthentication(t *testing.T) {
	model, err := NewResponsesAPIModel(AIServiceConfig{APIBaseURL: "https://models.example.test/v1", APIKey: "api-key", Model: "model-a"})
	if err != nil {
		t.Fatal(err)
	}
	model.Client = &http.Client{Transport: responseRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/models" || request.Header.Get("Authorization") != "Bearer api-key" {
			t.Fatalf("request = %s %q", request.URL, request.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"zeta"},{"id":"alpha"},{"id":"alpha"}]}`))}, nil
	})}
	models, err := model.ListModels(context.Background())
	if err != nil || len(models) != 2 || models[0].ID != "alpha" || models[1].ID != "zeta" {
		t.Fatalf("models = %#v, %v", models, err)
	}
}

func TestAgentControlClientUsesPrivateTokenAndNeverRequiresCoreKeyStorage(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "agent.token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("t", 24)), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 24) {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/api/v1/ai/config":
			if request.Method == http.MethodPut {
				var input AIServiceConfig
				if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.APIKey != "secret-api-key" || input.AgentInstructions != "只回答状态。" {
					t.Fatalf("input = %#v, %v", input, err)
				}
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": AIServiceStatus{APIBaseURL: "https://models.example.test/v1", Model: "model-a", APIKeyConfigured: true, Configured: true}})
		case "/api/v1/ai/models":
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []AIModel{{ID: "model-a"}}})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewAgentControlClient(server.URL, tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	client.client = server.Client()
	status, err := client.UpdateAIService(context.Background(), AIServiceConfig{APIBaseURL: "https://models.example.test/v1", APIKey: "secret-api-key", Model: "model-a", AgentInstructions: "只回答状态。"})
	if err != nil || !status.Configured || status.APIKeyConfigured != true {
		t.Fatalf("update = %#v, %v", status, err)
	}
	models, err := client.ListAIModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "model-a" {
		t.Fatalf("models = %#v, %v", models, err)
	}
}
