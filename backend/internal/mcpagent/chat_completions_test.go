package mcpagent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestChatCompletionsModelPreservesToolCallConversation(t *testing.T) {
	model, err := NewChatCompletionsModel(AIServiceConfig{APIBaseURL: "https://example.test/v1", APIKey: "api-key", Model: "tool-model", APIProtocol: AIAPIProtocolChatCompletions})
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	model.Client = &http.Client{Transport: responseRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer api-key" {
			t.Fatalf("request = %s %q", request.URL, request.Header.Get("Authorization"))
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		tools := payload["tools"].([]any)
		function := tools[0].(map[string]any)["function"].(map[string]any)
		if function["name"] != "homeloom_list_devices" || function["strict"] != nil {
			t.Fatalf("chat tools = %#v", function)
		}
		requests++
		if requests == 1 {
			body := `{"id":"chat-1","choices":[{"message":{"role":"assistant","content":null,"reasoning_content":"需要读取设备","tool_calls":[{"id":"call-1","type":"function","function":{"name":"homeloom_list_devices","arguments":"{}"}}]}}]}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		}
		messages := payload["messages"].([]any)
		if len(messages) != 4 || messages[2].(map[string]any)["tool_calls"] == nil || messages[2].(map[string]any)["reasoning_content"] != "需要读取设备" || messages[3].(map[string]any)["role"] != "tool" || messages[3].(map[string]any)["content"] != `[]` {
			t.Fatalf("continued messages = %#v", messages)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"id":"chat-2","choices":[{"message":{"role":"assistant","content":"设备状态正常"}}]}`))}, nil
	})}

	first, err := model.Start(context.Background(), "obey rules", "列出设备")
	if err != nil || len(first.Calls) != 1 || first.Calls[0].ID != "call-1" {
		t.Fatalf("start = %#v, %v", first, err)
	}
	second, err := model.Continue(context.Background(), "obey rules", first.ID, []ToolOutput{{CallID: "call-1", Output: json.RawMessage(`[]`)}})
	if err != nil || second.Text != "设备状态正常" || len(second.Calls) != 0 {
		t.Fatalf("continue = %#v, %v", second, err)
	}
}

func TestChatCompletionsModelStreamsDeltas(t *testing.T) {
	model, err := NewChatCompletionsModel(AIServiceConfig{APIBaseURL: "https://example.test/v1", APIKey: "api-key", Model: "stream-model", APIProtocol: AIAPIProtocolChatCompletions})
	if err != nil {
		t.Fatal(err)
	}
	model.Client = &http.Client{Transport: responseRoundTripper(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["stream"] != true || request.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("streaming request = %#v, accept=%q", payload, request.Header.Get("Accept"))
		}
		body := "data: {\"id\":\"chat-stream\",\"choices\":[{\"delta\":{\"content\":\"设备\"}}]}\n\ndata: {\"id\":\"chat-stream\",\"choices\":[{\"delta\":{\"content\":\"状态正常\"}}]}\n\ndata: [DONE]\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	var deltas []string
	result, err := model.StartStream(context.Background(), "rules", "检查状态", func(delta string) { deltas = append(deltas, delta) })
	if err != nil || result.ID != "chat-stream" || result.Text != "设备状态正常" || strings.Join(deltas, "") != result.Text {
		t.Fatalf("stream result = %#v deltas=%#v err=%v", result, deltas, err)
	}
}

func TestAIServiceConfigMigratesExistingDeepSeekConfigurationToChatCompletions(t *testing.T) {
	config, err := (AIServiceConfig{APIBaseURL: "https://api.deepseek.com", APIKey: "key", Model: "deepseek-v4-flash"}).normalized()
	if err != nil || config.APIProtocol != AIAPIProtocolChatCompletions || config.AgentInstructions != DefaultAgentInstructions {
		t.Fatalf("config = %#v, %v", config, err)
	}
}
