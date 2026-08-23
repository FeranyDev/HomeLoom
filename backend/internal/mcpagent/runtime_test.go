package mcpagent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
	"github.com/feranydev/homeloom/backend/internal/mcpbridge"
)

type planningModel struct{}

func (planningModel) Start(_ context.Context, _ string, _ string) (ModelResponse, error) {
	return ModelResponse{ID: "response-1", Calls: []FunctionCall{{ID: "call-plan", Name: "homeloom_prepare_property_write", Arguments: json.RawMessage(`{"deviceId":"switch-1","endpointId":"main","capabilityId":"switch","propertyId":"power","value":{"type":"bool","bool":true}}`)}}}, nil
}
func (planningModel) Continue(context.Context, string, string, []ToolOutput) (ModelResponse, error) {
	panic("prepare action must stop the Agent loop")
}

type gatewayRecorder struct {
	mu     sync.Mutex
	writes []mcpbridge.PropertyWriteRequest
}

func startGateway(t *testing.T) (string, *gatewayRecorder) {
	return startGatewayWithState(t, true, true)
}

func startGatewayWithState(t *testing.T, known, available bool) (string, *gatewayRecorder) {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "homeloom-mcp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "core.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &gatewayRecorder{}
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				var request mcpbridge.Request
				if err := json.NewDecoder(connection).Decode(&request); err != nil {
					return
				}
				response := mcpbridge.Response{ID: request.ID}
				switch request.Method {
				case mcpbridge.MethodDeviceState:
					result := application.MCPState{MCPDevice: application.MCPDevice{ID: "switch-1", Name: "客厅开关", Properties: []application.MCPProperty{{PropertyPath: domainmcp.PropertyPath{DeviceID: "switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}, Name: "开关", Writable: true, Access: domainmcp.AccessConfirm, UsageNote: "睡眠时不要打开"}}}, States: []domainstate.StateValue{{Key: domainstate.Key{DeviceID: "switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}, Version: 7, Known: known, Available: available}}}
					response.Result, _ = json.Marshal(result)
				case mcpbridge.MethodExecuteProperty:
					var input mcpbridge.PropertyWriteRequest
					_ = json.Unmarshal(request.Params, &input)
					recorder.mu.Lock()
					recorder.writes = append(recorder.writes, input)
					recorder.mu.Unlock()
					response.Result, _ = json.Marshal(mcpbridge.PropertyWriteResult{Command: map[string]string{"id": "command-1"}})
				default:
					response.Error = &mcpbridge.Error{Code: "method_not_found", Message: "unsupported"}
				}
				_ = json.NewEncoder(connection).Encode(response)
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return path, recorder
}

func TestRuntimePlansThenApprovesPropertyWriteThroughGateway(t *testing.T) {
	socket, recorder := startGateway(t)
	runtime, err := NewRuntime(socket, strings.Repeat("t", 24), planningModel{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := runtime.Run(context.Background(), "打开客厅灯")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != RunAwaitingApproval || run.Action == nil || run.Action.ExpectedStateVersion == nil || *run.Action.ExpectedStateVersion != 7 {
		t.Fatalf("planned run = %#v", run)
	}
	completed, err := runtime.Approve(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != RunExecuted || !strings.Contains(completed.Message, "command-1") {
		t.Fatalf("approved run = %#v", completed)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.writes) != 1 || recorder.writes[0].ExpectedStateVersion == nil || *recorder.writes[0].ExpectedStateVersion != 7 || recorder.writes[0].Value.Bool == nil || !*recorder.writes[0].Value.Bool {
		t.Fatalf("gateway writes = %#v", recorder.writes)
	}
}

func TestRuntimeRefusesToPlanWriteWithoutKnownAvailableState(t *testing.T) {
	socket, _ := startGatewayWithState(t, false, true)
	runtime, err := NewRuntime(socket, strings.Repeat("t", 24), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.prepareAction(context.Background(), json.RawMessage(`{"deviceId":"switch-1","endpointId":"main","capabilityId":"switch","propertyId":"power","value":{"type":"bool","bool":true}}`))
	if err == nil || !strings.Contains(err.Error(), "state is unavailable") {
		t.Fatalf("prepare action error = %v", err)
	}
}

func TestMCPHTTPDoesNotExposeDeviceWriteToolsAndRequiresToken(t *testing.T) {
	runtime, err := NewRuntime("/tmp/homeloom-core.sock", strings.Repeat("t", 24), nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 24))
	response = httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d", response.Code)
	}
	var payload struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Result.Tools) != 2 || payload.Result.Tools[0].Name != "homeloom.list_devices" || payload.Result.Tools[1].Name != "homeloom.get_device_state" {
		t.Fatalf("tools = %#v", payload.Result.Tools)
	}
}

func TestMCPHTTPAcceptsInitializedNotificationWithoutJSONRPCResponse(t *testing.T) {
	runtime, err := NewRuntime("/tmp/homeloom-core.sock", strings.Repeat("t", 24), nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 24))
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Body.Len() != 0 {
		t.Fatalf("notification response = %d %q", response.Code, response.Body.String())
	}
}

type responseRoundTripper func(*http.Request) (*http.Response, error)

func (f responseRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestOpenAIResponsesModelSendsInstructionsOnEveryToolRound(t *testing.T) {
	var payloads []map[string]any
	model, err := NewOpenAIResponsesModel("api-key", "test-model", "https://example.test/v1")
	if err != nil {
		t.Fatal(err)
	}
	model.Client = &http.Client{Transport: responseRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer api-key" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		payloads = append(payloads, payload)
		body := `{"id":"response-1","output":[{"type":"function_call","call_id":"call-1","name":"homeloom_list_devices","arguments":"{}"}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	first, err := model.Start(context.Background(), "keep safety rules", "列出设备")
	if err != nil || len(first.Calls) != 1 || first.Calls[0].Name != "homeloom_list_devices" {
		t.Fatalf("start = %#v, %v", first, err)
	}
	if _, err := model.Continue(context.Background(), "keep safety rules", first.ID, []ToolOutput{{CallID: "call-1", Output: json.RawMessage(`[]`)}}); err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 2 || payloads[0]["store"] != false || payloads[1]["previous_response_id"] != "response-1" || payloads[1]["instructions"] != "keep safety rules" {
		t.Fatalf("payloads = %#v", payloads)
	}
}

func TestCommandIDReadsCoreCommandResult(t *testing.T) {
	if got := commandID(map[string]any{"id": "command-123"}); got != "command-123" {
		t.Fatalf("command ID = %q", got)
	}
	if got := commandID(device.BoolValue(true)); got != "已受理" {
		t.Fatalf("fallback command ID = %q", got)
	}
}

func TestResponsesAPIModelRequiresAKeyButAllowsModelDiscoveryBeforeSelection(t *testing.T) {
	if _, err := NewOpenAIResponsesModel("", "model", ""); err == nil {
		t.Fatal("missing key was accepted")
	}
	model, err := NewOpenAIResponsesModel("key", "", "")
	if err != nil {
		t.Fatalf("missing model prevented model discovery client: %v", err)
	}
	if _, err := model.Start(context.Background(), "rules", "hello"); err == nil {
		t.Fatal("model execution accepted an empty model")
	}
}

func TestOpenAIResponsesModelUsesJSONRequestBody(t *testing.T) {
	model, err := NewOpenAIResponsesModel("key", "model", "https://example.test/v1")
	if err != nil {
		t.Fatal(err)
	}
	model.Client = &http.Client{Transport: responseRoundTripper(func(request *http.Request) (*http.Response, error) {
		content, _ := io.ReadAll(request.Body)
		if !bytes.Contains(content, []byte(`"parallel_tool_calls":false`)) {
			t.Errorf("request body = %s", content)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"id":"response-2","output":[]}`))}, nil
	})}
	if _, err := model.Start(context.Background(), "rules", "hello"); err != nil {
		t.Fatal(err)
	}
}
