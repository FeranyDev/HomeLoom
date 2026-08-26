package mcpagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// ChatCompletionsModel adapts the OpenAI-compatible Chat Completions tool
// protocol. Unlike Responses, it must retain the assistant tool-call turn so
// the next request can append role=tool messages.
type ChatCompletionsModel struct {
	APIKey  string
	Model   string
	BaseURL string
	Client  *http.Client

	mu            sync.Mutex
	conversations map[string][]map[string]any
}

func NewChatCompletionsModel(config AIServiceConfig) (*ChatCompletionsModel, error) {
	config, err := config.normalized()
	if err != nil {
		return nil, err
	}
	if config.APIKey == "" {
		return nil, errors.New("AI API key is required")
	}
	return &ChatCompletionsModel{APIKey: config.APIKey, Model: config.Model, BaseURL: config.APIBaseURL, Client: config.newAIHTTPClient(), conversations: make(map[string][]map[string]any)}, nil
}

func (m *ChatCompletionsModel) ListModels(ctx context.Context) ([]AIModel, error) {
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

func (m *ChatCompletionsModel) Start(ctx context.Context, instructions, input string) (ModelResponse, error) {
	if strings.TrimSpace(m.Model) == "" {
		return ModelResponse{}, errors.New("AI model is required")
	}
	return m.create(ctx, []map[string]any{{"role": "system", "content": instructions}, {"role": "user", "content": input}})
}

func (m *ChatCompletionsModel) Continue(ctx context.Context, _ string, previousResponseID string, outputs []ToolOutput) (ModelResponse, error) {
	m.mu.Lock()
	messages, found := m.conversations[previousResponseID]
	if found {
		delete(m.conversations, previousResponseID)
	}
	m.mu.Unlock()
	if !found {
		return ModelResponse{}, errors.New("AI tool-call conversation is no longer available")
	}
	for _, output := range outputs {
		messages = append(messages, map[string]any{"role": "tool", "tool_call_id": output.CallID, "content": string(output.Output)})
	}
	return m.create(ctx, messages)
}

func (m *ChatCompletionsModel) StartStream(ctx context.Context, instructions, input string, onDelta func(string)) (ModelResponse, error) {
	if strings.TrimSpace(m.Model) == "" {
		return ModelResponse{}, errors.New("AI model is required")
	}
	return m.createStream(ctx, []map[string]any{{"role": "system", "content": instructions}, {"role": "user", "content": input}}, onDelta)
}

func (m *ChatCompletionsModel) ContinueStream(ctx context.Context, _ string, previousResponseID string, outputs []ToolOutput, onDelta func(string)) (ModelResponse, error) {
	m.mu.Lock()
	messages, found := m.conversations[previousResponseID]
	if found {
		delete(m.conversations, previousResponseID)
	}
	m.mu.Unlock()
	if !found {
		return ModelResponse{}, errors.New("AI tool-call conversation is no longer available")
	}
	for _, output := range outputs {
		messages = append(messages, map[string]any{"role": "tool", "tool_call_id": output.CallID, "content": string(output.Output)})
	}
	return m.createStream(ctx, messages, onDelta)
}

func (m *ChatCompletionsModel) create(ctx context.Context, messages []map[string]any) (ModelResponse, error) {
	payload := map[string]any{"model": m.Model, "messages": messages, "tools": chatCompletionTools(), "parallel_tool_calls": false}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ModelResponse{}, fmt.Errorf("encode AI chat completion request: %w", err)
	}
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: AIProviderRequestTimeout}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.BaseURL+"/chat/completions", bytes.NewReader(encoded))
	if err != nil {
		return ModelResponse{}, fmt.Errorf("create AI chat completion request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+m.APIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return ModelResponse{}, fmt.Errorf("request AI chat completion: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return ModelResponse{}, fmt.Errorf("read AI chat completion: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ModelResponse{}, fmt.Errorf("AI chat completion failed with HTTP %d", response.StatusCode)
	}
	var decoded struct {
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ModelResponse{}, fmt.Errorf("decode AI chat completion: %w", err)
	}
	if decoded.ID == "" || len(decoded.Choices) == 0 {
		return ModelResponse{}, errors.New("AI chat completion has no choice")
	}
	message := decoded.Choices[0].Message
	result := ModelResponse{ID: decoded.ID, Text: strings.TrimSpace(message.Content)}
	assistant := map[string]any{"role": "assistant", "content": message.Content}
	if message.ReasoningContent != "" {
		// DeepSeek requires this field to be sent back on a subsequent tool
		// round when the model used thinking mode.
		assistant["reasoning_content"] = message.ReasoningContent
	}
	if len(message.ToolCalls) > 0 {
		calls := make([]map[string]any, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			if call.ID == "" || call.Function.Name == "" || !json.Valid([]byte(call.Function.Arguments)) {
				return ModelResponse{}, errors.New("AI chat completion returned an invalid tool call")
			}
			result.Calls = append(result.Calls, FunctionCall{ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)})
			calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Function.Name, "arguments": call.Function.Arguments}})
		}
		assistant["tool_calls"] = calls
	}
	m.storeConversation(result.ID, append(messages, assistant))
	return result, nil
}

func (m *ChatCompletionsModel) createStream(ctx context.Context, messages []map[string]any, onDelta func(string)) (ModelResponse, error) {
	payload := map[string]any{"model": m.Model, "messages": messages, "tools": chatCompletionTools(), "parallel_tool_calls": false, "stream": true}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ModelResponse{}, fmt.Errorf("encode AI chat completion stream: %w", err)
	}
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: AIProviderRequestTimeout}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.BaseURL+"/chat/completions", bytes.NewReader(encoded))
	if err != nil {
		return ModelResponse{}, fmt.Errorf("create AI chat completion stream: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+m.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	response, err := client.Do(request)
	if err != nil {
		return ModelResponse{}, fmt.Errorf("request AI chat completion stream: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ModelResponse{}, fmt.Errorf("AI chat completion stream failed with HTTP %d", response.StatusCode)
	}
	type pendingCall struct{ ID, Name, Arguments string }
	calls := map[int]*pendingCall{}
	result := ModelResponse{}
	var data strings.Builder
	handle := func() error {
		payload := strings.TrimSpace(data.String())
		defer data.Reset()
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		var chunk struct {
			ID      string `json:"id"`
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("decode AI chat completion stream: %w", err)
		}
		if chunk.ID != "" {
			result.ID = chunk.ID
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				result.Text += choice.Delta.Content
				if onDelta != nil {
					onDelta(choice.Delta.Content)
				}
			}
			for _, part := range choice.Delta.ToolCalls {
				call := calls[part.Index]
				if call == nil {
					call = &pendingCall{}
					calls[part.Index] = call
				}
				if part.ID != "" {
					call.ID = part.ID
				}
				if part.Function.Name != "" {
					call.Name = part.Function.Name
				}
				call.Arguments += part.Function.Arguments
			}
		}
		return nil
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 4<<20))
	scanner.Buffer(make([]byte, 32<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case line == "":
			if err := handle(); err != nil {
				return ModelResponse{}, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ModelResponse{}, fmt.Errorf("read AI chat completion stream: %w", err)
	}
	if err := handle(); err != nil {
		return ModelResponse{}, err
	}
	if result.ID == "" {
		return ModelResponse{}, errors.New("AI chat completion stream has no id")
	}
	indexes := make([]int, 0, len(calls))
	for index := range calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	assistant := map[string]any{"role": "assistant", "content": result.Text}
	if len(indexes) > 0 {
		encodedCalls := make([]map[string]any, 0, len(indexes))
		for _, index := range indexes {
			call := calls[index]
			if call.ID == "" || call.Name == "" || !json.Valid([]byte(call.Arguments)) {
				return ModelResponse{}, errors.New("AI chat completion stream returned an invalid tool call")
			}
			result.Calls = append(result.Calls, FunctionCall{ID: call.ID, Name: call.Name, Arguments: json.RawMessage(call.Arguments)})
			encodedCalls = append(encodedCalls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": call.Arguments}})
		}
		assistant["tool_calls"] = encodedCalls
	}
	m.storeConversation(result.ID, append(messages, assistant))
	return result, nil
}

func (m *ChatCompletionsModel) storeConversation(id string, messages []map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.conversations) >= 64 {
		for id := range m.conversations {
			delete(m.conversations, id)
			break
		}
	}
	m.conversations[id] = messages
}

func chatCompletionTools() []map[string]any {
	definitions := agentTools()
	tools := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": definition["name"], "description": definition["description"], "parameters": definition["parameters"]}})
	}
	return tools
}
