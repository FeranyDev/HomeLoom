package mcpagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// AgentControlClient is used only by Core to proxy an authenticated
// administrator's AI-service settings requests to the local Agent. The API
// key passes through Core but is never persisted there.
type AgentControlClient struct {
	baseURL   string
	tokenFile string
	client    *http.Client
}

type AgentControlError struct{ Status int }

const agentControlTimeout = 15 * time.Second

func (e *AgentControlError) Error() string {
	return fmt.Sprintf("MCP Agent control request failed with HTTP %d", e.Status)
}

func NewAgentControlClient(baseURL, tokenFile string) (*AgentControlClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	tokenFile = strings.TrimSpace(tokenFile)
	if baseURL == "" || tokenFile == "" {
		return nil, errors.New("MCP Agent URL and token file are required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("MCP Agent URL must be a local HTTP URL")
	}
	host := net.ParseIP(parsed.Hostname())
	if host == nil || !host.IsLoopback() {
		return nil, errors.New("MCP Agent URL must use a loopback IP address")
	}
	return &AgentControlClient{baseURL: baseURL, tokenFile: tokenFile, client: &http.Client{Timeout: agentControlTimeout}}, nil
}

func (c *AgentControlClient) AIServiceStatus(ctx context.Context) (AIServiceStatus, error) {
	var response struct {
		Data AIServiceStatus `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/ai/config", nil, &response); err != nil {
		return AIServiceStatus{}, err
	}
	return response.Data, nil
}

func (c *AgentControlClient) UpdateAIService(ctx context.Context, config AIServiceConfig) (AIServiceStatus, error) {
	var response struct {
		Data AIServiceStatus `json:"data"`
	}
	if err := c.do(ctx, http.MethodPut, "/api/v1/ai/config", config, &response); err != nil {
		return AIServiceStatus{}, err
	}
	return response.Data, nil
}

func (c *AgentControlClient) ListAIModels(ctx context.Context) ([]AIModel, error) {
	var response struct {
		Data []AIModel `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/ai/models", nil, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

// StartAIRun, AIRun, and ApproveAIRun keep the conversational Agent surface
// behind Core's authenticated administrator API. The loopback Agent token is
// never exposed to a browser.
func (c *AgentControlClient) StartAIRun(ctx context.Context, message string) (Run, error) {
	return c.StartAIRunWithContext(ctx, RunRequest{Message: message, Context: RunContext{Source: RunSourceInteractive}})
}

// StartAIRunWithContext is reserved for Core-owned automation executions.
// The Agent itself generates the clock data and ignores unrecognized sources.
func (c *AgentControlClient) StartAIRunWithContext(ctx context.Context, input RunRequest) (Run, error) {
	var response struct {
		Data Run `json:"data"`
	}
	if err := c.doWithTimeout(ctx, AIRunControlTimeout, http.MethodPost, "/api/v1/agent/runs", input, &response); err != nil {
		return Run{}, err
	}
	return response.Data, nil
}

// StreamAIRun proxies the Agent's private SSE response to Core. The callback
// receives only display-safe model deltas and the final typed Run; credentials
// and provider protocol details never leave the Agent process.
func (c *AgentControlClient) StreamAIRun(ctx context.Context, input RunRequest, onEvent func(StreamEvent) error) error {
	token, err := readPrivateToken(c.tokenFile)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/agent/runs/stream", bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	response, err := c.clientWithTimeout(AIRunControlTimeout).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return &AgentControlError{Status: response.StatusCode}
	}
	var event string
	var data strings.Builder
	handle := func() error {
		payload := strings.TrimSpace(data.String())
		defer data.Reset()
		if payload == "" {
			return nil
		}
		var value StreamEvent
		if err := json.Unmarshal([]byte(payload), &value); err != nil {
			return err
		}
		if value.Type == "" {
			value.Type = event
		}
		if onEvent != nil {
			return onEvent(value)
		}
		return nil
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 4<<20))
	scanner.Buffer(make([]byte, 32<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case line == "":
			if err := handle(); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return handle()
}

func (c *AgentControlClient) AIRun(ctx context.Context, id string) (Run, error) {
	var response struct {
		Data Run `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/agent/runs/"+url.PathEscape(id), nil, &response); err != nil {
		return Run{}, err
	}
	return response.Data, nil
}

func (c *AgentControlClient) ApproveAIRun(ctx context.Context, id string) (Run, error) {
	return c.approveAIRun(ctx, id, "manual")
}

// ApproveUnattendedAIRun is called exclusively after Core has checked the
// durable automation policy. The Agent includes the source in its private
// gateway request so Core can enforce property-level unattended permission.
func (c *AgentControlClient) ApproveUnattendedAIRun(ctx context.Context, id string) (Run, error) {
	return c.approveAIRun(ctx, id, "unattended")
}

func (c *AgentControlClient) approveAIRun(ctx context.Context, id, mode string) (Run, error) {
	var response struct {
		Data Run `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/agent/runs/"+url.PathEscape(id)+"/approve", map[string]string{"mode": mode}, &response); err != nil {
		return Run{}, err
	}
	return response.Data, nil
}

func (c *AgentControlClient) do(ctx context.Context, method, path string, value, destination any) error {
	return c.doWithClient(ctx, c.client, method, path, value, destination)
}

func (c *AgentControlClient) doWithTimeout(ctx context.Context, timeout time.Duration, method, path string, value, destination any) error {
	return c.doWithClient(ctx, c.clientWithTimeout(timeout), method, path, value, destination)
}

func (c *AgentControlClient) clientWithTimeout(timeout time.Duration) *http.Client {
	client := *c.client
	client.Timeout = timeout
	return &client
}

func (c *AgentControlClient) doWithClient(ctx context.Context, client *http.Client, method, path string, value, destination any) error {
	token, err := readPrivateToken(c.tokenFile)
	if err != nil {
		return err
	}
	var body io.Reader
	if value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode MCP Agent control request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create MCP Agent control request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if value != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request MCP Agent control: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return &AgentControlError{Status: response.StatusCode}
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(destination); err != nil {
		return fmt.Errorf("decode MCP Agent control response: %w", err)
	}
	return nil
}

func readPrivateToken(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read Agent token file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("Agent token file must be a private regular file (mode 0600)")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read Agent token file: %w", err)
	}
	token := strings.TrimSpace(string(content))
	if len(token) < 24 {
		return "", errors.New("Agent token file must contain at least 24 characters")
	}
	return token, nil
}
