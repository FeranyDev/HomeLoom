// Package mcpagent implements the independent mcp-agent-runtime process. It
// owns the MCP/AI-facing HTTP surface but never opens HomeLoom's database or
// Provider connections.
package mcpagent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
	"github.com/feranydev/homeloom/backend/internal/mcpbridge"
)

const pendingActionTTL = 2 * time.Minute

type Model interface {
	Start(context.Context, string, string) (ModelResponse, error)
	Continue(context.Context, string, string, []ToolOutput) (ModelResponse, error)
}

type ModelResponse struct {
	ID    string
	Text  string
	Calls []FunctionCall
}

type FunctionCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type ToolOutput struct {
	CallID string
	Output json.RawMessage
}

type Runtime struct {
	gateway   mcpbridge.Client
	aiMu      sync.RWMutex
	model     Model
	aiConfig  AIServiceConfig
	aiStore   AIConfigStore
	aiFactory AIModelFactory
	token     [sha256.Size]byte
	now       func() time.Time
	mu        sync.Mutex
	runs      map[string]Run
}

type RunStatus string

const (
	RunCompleted        RunStatus = "completed"
	RunAwaitingApproval RunStatus = "awaiting_approval"
	RunExecuted         RunStatus = "executed"
	RunFailed           RunStatus = "failed"
)

type Run struct {
	ID        string      `json:"id"`
	Status    RunStatus   `json:"status"`
	Message   string      `json:"message"`
	CreatedAt time.Time   `json:"createdAt"`
	ExpiresAt time.Time   `json:"expiresAt,omitempty"`
	Action    *ActionPlan `json:"action,omitempty"`
}

type ActionPlan struct {
	domainmcp.PropertyPath
	Value                device.PropertyValue `json:"value"`
	ExpectedStateVersion *uint64              `json:"expectedStateVersion,omitempty"`
	DeviceName           string               `json:"deviceName"`
	PropertyName         string               `json:"propertyName"`
	UsageNote            string               `json:"usageNote,omitempty"`
}

func NewRuntime(socketPath, token string, model Model) (*Runtime, error) {
	if strings.TrimSpace(socketPath) == "" {
		return nil, errors.New("Core MCP gateway socket is required")
	}
	if len(strings.TrimSpace(token)) < 24 {
		return nil, errors.New("mcp-agent token must contain at least 24 characters")
	}
	return &Runtime{
		gateway: mcpbridge.Client{SocketPath: socketPath, Timeout: 10 * time.Second},
		model:   model,
		aiConfig: AIServiceConfig{
			APIBaseURL:        defaultAIAPIBaseURL,
			APIProtocol:       AIAPIProtocolResponses,
			AgentInstructions: DefaultAgentInstructions,
		},
		token: sha256.Sum256([]byte(token)),
		now:   func() time.Time { return time.Now().UTC() },
		runs:  make(map[string]Run),
	}, nil
}

func (r *Runtime) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("/mcp", r.authenticate(http.HandlerFunc(r.handleMCP)))
	mux.Handle("/api/v1/ai/config", r.authenticate(http.HandlerFunc(r.handleAIConfig)))
	mux.Handle("/api/v1/ai/models", r.authenticate(http.HandlerFunc(r.handleAIModels)))
	mux.Handle("/api/v1/agent/runs", r.authenticate(http.HandlerFunc(r.handleRuns)))
	mux.Handle("/api/v1/agent/runs/", r.authenticate(http.HandlerFunc(r.handleRun)))
	return mux
}

// ConfigureAIService loads private Agent-side AI credentials and enables
// runtime updates without ever exposing the API key from this process.
func (r *Runtime) ConfigureAIService(initial AIServiceConfig, store AIConfigStore, factory AIModelFactory) error {
	if factory == nil {
		return errors.New("AI model factory is required")
	}
	config, err := initial.normalized()
	if err != nil {
		return err
	}
	if store != nil {
		stored, loadErr := store.Load()
		if loadErr == nil {
			config = stored
		} else if !errors.Is(loadErr, os.ErrNotExist) {
			return loadErr
		}
	}
	var model Model
	if config.configured() {
		model, err = factory(config)
		if err != nil {
			return err
		}
	}
	r.aiMu.Lock()
	r.model, r.aiConfig, r.aiStore, r.aiFactory = model, config, store, factory
	r.aiMu.Unlock()
	return nil
}

func (r *Runtime) AIServiceStatus() AIServiceStatus {
	r.aiMu.RLock()
	defer r.aiMu.RUnlock()
	return r.aiConfig.status()
}

func (r *Runtime) UpdateAIService(input AIServiceConfig) (AIServiceStatus, error) {
	r.aiMu.RLock()
	current, store, factory := r.aiConfig, r.aiStore, r.aiFactory
	r.aiMu.RUnlock()
	if store == nil || factory == nil {
		return AIServiceStatus{}, errors.New("AI service configuration persistence is unavailable")
	}
	if strings.TrimSpace(input.APIKey) == "" {
		input.APIKey = current.APIKey
	}
	config, err := input.normalized()
	if err != nil {
		return AIServiceStatus{}, err
	}
	var model Model
	if config.configured() {
		model, err = factory(config)
		if err != nil {
			return AIServiceStatus{}, err
		}
	}
	if err := store.Save(config); err != nil {
		return AIServiceStatus{}, err
	}
	r.aiMu.Lock()
	r.model, r.aiConfig = model, config
	r.aiMu.Unlock()
	return config.status(), nil
}

func (r *Runtime) ListAIModels(ctx context.Context) ([]AIModel, error) {
	r.aiMu.RLock()
	model, config, factory := r.model, r.aiConfig, r.aiFactory
	r.aiMu.RUnlock()
	if config.APIKey == "" {
		return nil, ErrAIServiceNotConfigured
	}
	if model == nil && factory != nil {
		var err error
		model, err = factory(config)
		if err != nil {
			return nil, err
		}
	}
	if model == nil {
		return nil, ErrAIServiceNotConfigured
	}
	catalog, ok := model.(AIModelCatalog)
	if !ok {
		return nil, errors.New("configured AI service does not support model discovery")
	}
	return catalog.ListModels(ctx)
}

func (r *Runtime) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		const prefix = "Bearer "
		value := strings.TrimSpace(request.Header.Get("Authorization"))
		provided := sha256.Sum256([]byte(strings.TrimPrefix(value, prefix)))
		if !strings.HasPrefix(value, prefix) || subtle.ConstantTimeCompare(provided[:], r.token[:]) != 1 {
			writer.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (r *Runtime) handleMCP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	defer request.Body.Close()
	var rpc mcpRequest
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 128<<10)).Decode(&rpc); err != nil {
		writeJSON(writer, http.StatusBadRequest, mcpError(nil, -32700, "invalid JSON-RPC request"))
		return
	}
	response := r.mcpCall(request.Context(), rpc)
	if len(rpc.ID) == 0 {
		writer.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (r *Runtime) handleRuns(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.aiMu.RLock()
	configured := r.model != nil
	r.aiMu.RUnlock()
	if !configured {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "AI model is not configured"})
		return
	}
	defer request.Body.Close()
	var input struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10)).Decode(&input); err != nil || strings.TrimSpace(input.Message) == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "message is required"})
		return
	}
	run, err := r.Run(request.Context(), input.Message)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeJSON(writer, http.StatusGatewayTimeout, map[string]string{"error": "AI provider request timed out"})
			return
		}
		writeJSON(writer, http.StatusBadGateway, map[string]string{"error": "AI Agent request failed"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]Run{"data": run})
}

func (r *Runtime) handleAIConfig(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeJSON(writer, http.StatusOK, map[string]AIServiceStatus{"data": r.AIServiceStatus()})
	case http.MethodPut:
		defer request.Body.Close()
		var input AIServiceConfig
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10)).Decode(&input); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid AI service configuration"})
			return
		}
		updated, err := r.UpdateAIService(input)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]AIServiceStatus{"data": updated})
	default:
		writer.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (r *Runtime) handleAIModels(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	models, err := r.ListAIModels(request.Context())
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, ErrAIServiceNotConfigured) {
			status = http.StatusServiceUnavailable
		}
		writeJSON(writer, status, map[string]string{"error": "AI model list is unavailable"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string][]AIModel{"data": models})
}

func (r *Runtime) handleRun(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/api/v1/agent/runs/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "run not found"})
		return
	}
	if len(parts) == 1 && request.Method == http.MethodGet {
		run, found := r.RunByID(parts[0])
		if !found {
			writeJSON(writer, http.StatusNotFound, map[string]string{"error": "run not found"})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]Run{"data": run})
		return
	}
	if len(parts) == 2 && parts[1] == "approve" && request.Method == http.MethodPost {
		run, err := r.Approve(request.Context(), parts[0])
		if err != nil {
			status := http.StatusConflict
			if errors.Is(err, ErrRunNotFound) {
				status = http.StatusNotFound
			}
			writeJSON(writer, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]Run{"data": run})
		return
	}
	writer.WriteHeader(http.StatusMethodNotAllowed)
}

func (r *Runtime) Run(ctx context.Context, message string) (Run, error) {
	r.aiMu.RLock()
	model, instructions := r.model, r.aiConfig.AgentInstructions
	r.aiMu.RUnlock()
	if model == nil {
		return Run{}, ErrAIServiceNotConfigured
	}
	response, err := model.Start(ctx, instructions, message)
	if err != nil {
		return Run{}, err
	}
	for calls := 0; calls < 4; calls++ {
		if len(response.Calls) == 0 {
			run := Run{ID: newID(), Status: RunCompleted, Message: strings.TrimSpace(response.Text), CreatedAt: r.now()}
			if run.Message == "" {
				run.Message = "已完成状态查询。"
			}
			r.save(run)
			return run, nil
		}
		outputs := make([]ToolOutput, 0, len(response.Calls))
		for _, call := range response.Calls {
			if call.Name == "homeloom_prepare_property_write" {
				action, actionErr := r.prepareAction(ctx, call.Arguments)
				if actionErr != nil {
					outputs = append(outputs, toolError(call.ID, actionErr))
					continue
				}
				now := r.now()
				run := Run{ID: newID(), Status: RunAwaitingApproval, Message: "已生成设备操作计划，等待你的明确批准。", CreatedAt: now, ExpiresAt: now.Add(pendingActionTTL), Action: &action}
				r.save(run)
				return run, nil
			}
			output, callErr := r.invokeReadTool(ctx, call)
			if callErr != nil {
				outputs = append(outputs, toolError(call.ID, callErr))
				continue
			}
			outputs = append(outputs, ToolOutput{CallID: call.ID, Output: output})
		}
		response, err = model.Continue(ctx, instructions, response.ID, outputs)
		if err != nil {
			return Run{}, err
		}
	}
	return Run{}, errors.New("AI Agent exceeded the tool call limit")
}

var (
	ErrRunNotFound        = errors.New("agent run not found")
	ErrRunNotApprovable   = errors.New("agent run is not awaiting approval")
	ErrRunApprovalExpired = errors.New("agent run approval has expired")
)

func (r *Runtime) Approve(ctx context.Context, id string) (Run, error) {
	r.mu.Lock()
	r.pruneLocked()
	run, found := r.runs[id]
	if !found {
		r.mu.Unlock()
		return Run{}, ErrRunNotFound
	}
	if run.Status != RunAwaitingApproval || run.Action == nil {
		r.mu.Unlock()
		return Run{}, ErrRunNotApprovable
	}
	if !run.ExpiresAt.After(r.now()) {
		delete(r.runs, id)
		r.mu.Unlock()
		return Run{}, ErrRunApprovalExpired
	}
	action := *run.Action
	r.mu.Unlock()
	var result mcpbridge.PropertyWriteResult
	err := r.gateway.Call(ctx, newID(), mcpbridge.MethodExecuteProperty, "mcp-agent-"+id, mcpbridge.PropertyWriteRequest{PropertyPath: action.PropertyPath, Value: action.Value, ExpectedStateVersion: action.ExpectedStateVersion}, &result)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		run.Status, run.Message = RunFailed, "设备操作未执行："+safeError(err)
		r.runs[id] = run
		return run, err
	}
	run.Status, run.Message, run.ExpiresAt = RunExecuted, "设备操作已提交，命令编号："+commandID(result.Command), time.Time{}
	r.runs[id] = run
	return run, nil
}

func (r *Runtime) RunByID(id string) (Run, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	run, found := r.runs[id]
	return run, found
}

func (r *Runtime) save(run Run) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	r.runs[run.ID] = run
}

func (r *Runtime) pruneLocked() {
	now := r.now()
	for id, run := range r.runs {
		if !run.ExpiresAt.IsZero() && !run.ExpiresAt.After(now) {
			delete(r.runs, id)
		}
	}
}

func (r *Runtime) invokeReadTool(ctx context.Context, call FunctionCall) (json.RawMessage, error) {
	switch call.Name {
	case "homeloom_list_devices":
		var result []application.MCPDevice
		if err := r.gateway.Call(ctx, newID(), mcpbridge.MethodListDevices, "mcp-agent-read", map[string]any{}, &result); err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "homeloom_get_device_state":
		var input mcpbridge.DeviceStateRequest
		if err := json.Unmarshal(call.Arguments, &input); err != nil || input.DeviceID == "" {
			return nil, errors.New("deviceId is required")
		}
		var result application.MCPState
		if err := r.gateway.Call(ctx, newID(), mcpbridge.MethodDeviceState, "mcp-agent-read", input, &result); err != nil {
			return nil, err
		}
		return json.Marshal(result)
	default:
		return nil, errors.New("unsupported Agent tool")
	}
}

func (r *Runtime) prepareAction(ctx context.Context, arguments json.RawMessage) (ActionPlan, error) {
	var input struct {
		domainmcp.PropertyPath
		Value device.PropertyValue `json:"value"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return ActionPlan{}, errors.New("invalid property write request")
	}
	if err := input.PropertyPath.Validate(); err != nil {
		return ActionPlan{}, err
	}
	var snapshot application.MCPState
	if err := r.gateway.Call(ctx, newID(), mcpbridge.MethodDeviceState, "mcp-agent-plan", mcpbridge.DeviceStateRequest{DeviceID: input.DeviceID}, &snapshot); err != nil {
		return ActionPlan{}, err
	}
	for _, property := range snapshot.Properties {
		if property.PropertyPath != input.PropertyPath {
			continue
		}
		if property.Access != domainmcp.AccessConfirm || !property.Writable {
			return ActionPlan{}, errors.New("this device property is not approved for AI control")
		}
		var version *uint64
		stateFound := false
		for _, state := range snapshot.States {
			if state.Key.DeviceID == input.DeviceID && state.Key.EndpointID == input.EndpointID && state.Key.CapabilityID == input.CapabilityID && state.Key.PropertyID == input.PropertyID {
				stateFound = true
				if !state.Known || !state.Available {
					return ActionPlan{}, errors.New("current device state is unavailable; AI control cannot be planned")
				}
				value := state.Version
				version = &value
				break
			}
		}
		if !stateFound || version == nil {
			return ActionPlan{}, errors.New("current device state is unavailable; AI control cannot be planned")
		}
		return ActionPlan{PropertyPath: input.PropertyPath, Value: input.Value, ExpectedStateVersion: version, DeviceName: snapshot.Name, PropertyName: property.Name, UsageNote: property.UsageNote}, nil
	}
	return ActionPlan{}, errors.New("device property is not exposed to the AI Agent")
}

func toolError(callID string, err error) ToolOutput {
	payload, _ := json.Marshal(map[string]string{"error": safeError(err)})
	return ToolOutput{CallID: callID, Output: payload}
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func commandID(value any) string {
	encoded, _ := json.Marshal(value)
	var command struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(encoded, &command) == nil && command.ID != "" {
		return command.ID
	}
	return "已受理"
}

func newID() string {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
