package mcpagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// ResponsesAPIModel is intentionally owned by mcp-agent-runtime instead of
// Core, so an AI API credential is never available to HomeLoom's device
// process or its database layer. It targets the documented OpenAI-compatible
// Responses and Models endpoints, not a particular hosted provider.
type ResponsesAPIModel struct {
	APIKey  string
	Model   string
	BaseURL string
	Client  *http.Client
}

// NewAIServiceModel selects an OpenAI-compatible request protocol without
// binding the persisted configuration to a specific provider brand.
func NewAIServiceModel(config AIServiceConfig) (Model, error) {
	config, err := config.normalized()
	if err != nil {
		return nil, err
	}
	switch config.APIProtocol {
	case AIAPIProtocolResponses:
		return NewResponsesAPIModel(config)
	case AIAPIProtocolChatCompletions:
		return NewChatCompletionsModel(config)
	default:
		return nil, errors.New("unsupported AI API protocol")
	}
}

func NewResponsesAPIModel(config AIServiceConfig) (*ResponsesAPIModel, error) {
	config, err := config.normalized()
	if err != nil {
		return nil, err
	}
	if config.APIKey == "" {
		return nil, errors.New("AI API key is required")
	}
	return &ResponsesAPIModel{APIKey: config.APIKey, Model: config.Model, BaseURL: config.APIBaseURL, Client: config.newAIHTTPClient()}, nil
}

// NewOpenAIResponsesModel remains as a source-compatible constructor for
// integrations compiled against an earlier Agent release. New code should use
// provider-neutral AIServiceConfig and NewResponsesAPIModel.
func NewOpenAIResponsesModel(apiKey, model, baseURL string) (*ResponsesAPIModel, error) {
	return NewResponsesAPIModel(AIServiceConfig{APIKey: apiKey, Model: model, APIBaseURL: baseURL})
}

func (m *ResponsesAPIModel) ListModels(ctx context.Context) ([]AIModel, error) {
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: AIProviderRequestTimeout}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.BaseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create AI models request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+m.APIKey)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request AI models: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read AI models response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("AI models request failed with HTTP %d", response.StatusCode)
	}
	var decoded struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("decode AI models response: %w", err)
	}
	seen := make(map[string]struct{}, len(decoded.Data))
	models := make([]AIModel, 0, len(decoded.Data))
	for _, item := range decoded.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, found := seen[id]; found {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, AIModel{ID: id})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func (m *ResponsesAPIModel) Start(ctx context.Context, instructions, input string) (ModelResponse, error) {
	if strings.TrimSpace(m.Model) == "" {
		return ModelResponse{}, errors.New("AI model is required")
	}
	return m.create(ctx, map[string]any{
		"model":               m.Model,
		"instructions":        instructions,
		"input":               input,
		"tools":               agentTools(),
		"parallel_tool_calls": false,
		"store":               false,
	})
}

func (m *ResponsesAPIModel) Continue(ctx context.Context, instructions, previousResponseID string, outputs []ToolOutput) (ModelResponse, error) {
	if strings.TrimSpace(m.Model) == "" {
		return ModelResponse{}, errors.New("AI model is required")
	}
	input := make([]map[string]any, 0, len(outputs))
	for _, output := range outputs {
		input = append(input, map[string]any{"type": "function_call_output", "call_id": output.CallID, "output": string(output.Output)})
	}
	return m.create(ctx, map[string]any{
		"model":                m.Model,
		"instructions":         instructions,
		"previous_response_id": previousResponseID,
		"input":                input,
		"tools":                agentTools(),
		"parallel_tool_calls":  false,
		"store":                false,
	})
}

func (m *ResponsesAPIModel) create(ctx context.Context, payload any) (ModelResponse, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ModelResponse{}, fmt.Errorf("encode AI response request: %w", err)
	}
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: AIProviderRequestTimeout}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.BaseURL+"/responses", bytes.NewReader(encoded))
	if err != nil {
		return ModelResponse{}, fmt.Errorf("create AI response request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+m.APIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return ModelResponse{}, fmt.Errorf("request AI response: %w", err)
	}
	defer response.Body.Close()
	payloadBytes, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return ModelResponse{}, fmt.Errorf("read AI response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ModelResponse{}, fmt.Errorf("AI response failed with HTTP %d", response.StatusCode)
	}
	var decoded struct {
		ID     string `json:"id"`
		Output []struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(payloadBytes, &decoded); err != nil {
		return ModelResponse{}, fmt.Errorf("decode AI response: %w", err)
	}
	if decoded.ID == "" {
		return ModelResponse{}, errors.New("AI response has no id")
	}
	result := ModelResponse{ID: decoded.ID}
	for _, item := range decoded.Output {
		if item.Type == "function_call" {
			result.Calls = append(result.Calls, FunctionCall{ID: item.CallID, Name: item.Name, Arguments: json.RawMessage(item.Arguments)})
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" {
				result.Text += content.Text
			}
		}
	}
	return result, nil
}

func agentTools() []map[string]any {
	propertyPath := map[string]any{
		"deviceId": map[string]any{"type": "string"}, "endpointId": map[string]any{"type": "string"},
		"capabilityId": map[string]any{"type": "string"}, "propertyId": map[string]any{"type": "string"},
	}
	return []map[string]any{
		{"type": "function", "name": "homeloom_list_devices", "description": "列出管理员已授权给 AI 的家庭设备、属性与备注。", "strict": true, "parameters": map[string]any{"type": "object", "additionalProperties": false}},
		{"type": "function", "name": "homeloom_get_device_state", "description": "读取指定已授权设备的实时属性状态；提出操作前必须调用。", "strict": true, "parameters": map[string]any{"type": "object", "properties": map[string]any{"deviceId": map[string]any{"type": "string"}}, "required": []string{"deviceId"}, "additionalProperties": false}},
		{"type": "function", "name": "homeloom_prepare_property_write", "description": "为一个允许 AI 控制的属性生成待用户批准的写入计划；不会执行操作。", "strict": true, "parameters": map[string]any{"type": "object", "properties": mergeSchemas(propertyPath, map[string]any{"value": propertyValueSchema()}), "required": []string{"deviceId", "endpointId", "capabilityId", "propertyId", "value"}, "additionalProperties": false}},
	}
}

func propertyValueSchema() map[string]any {
	return map[string]any{
		"type": "object", "properties": map[string]any{
			"type": map[string]any{"type": "string", "enum": []string{"bool", "int", "number", "string", "enum"}},
			"bool": map[string]any{"type": "boolean"}, "int": map[string]any{"type": "integer"},
			"number": map[string]any{"type": "number"}, "string": map[string]any{"type": "string"},
		}, "required": []string{"type"}, "additionalProperties": false,
	}
}

func mergeSchemas(left, right map[string]any) map[string]any {
	result := make(map[string]any, len(left)+len(right))
	for key, value := range left {
		result[key] = value
	}
	for key, value := range right {
		result[key] = value
	}
	return result
}
