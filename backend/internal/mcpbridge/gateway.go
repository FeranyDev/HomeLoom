// Package mcpbridge defines the private, local-only Core Gateway used by the
// separate mcp-agent-runtime process. The sidecar never receives database or
// Provider credentials; it can only make these narrowly typed requests.
package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	domainaudit "github.com/feranydev/homeloom/backend/internal/domain/audit"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
)

const ProtocolVersion = "1"

const (
	MethodListDevices     = "devices.list"
	MethodDeviceState     = "devices.state"
	MethodExecuteProperty = "properties.execute"
)

type Request struct {
	Version       string          `json:"version"`
	ID            string          `json:"id"`
	Method        string          `json:"method"`
	CorrelationID string          `json:"correlationId,omitempty"`
	Params        json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type DeviceStateRequest struct {
	DeviceID string `json:"deviceId"`
}

type PropertyWriteRequest struct {
	domainmcp.PropertyPath
	Value                device.PropertyValue `json:"value"`
	ExpectedStateVersion *uint64              `json:"expectedStateVersion,omitempty"`
	AIExecution          *AIExecutionMetadata `json:"aiExecution,omitempty"`
}

// AIExecutionMetadata is supplied only by the private local Agent when it
// executes a previously prepared action. It gives Core enough context to
// enforce the stricter unattended policy and make the audit trail useful
// without exposing any model credentials or prompts.
type AIExecutionMetadata struct {
	RunID          string `json:"runId,omitempty"`
	Source         string `json:"source,omitempty"`
	AutomationID   string `json:"automationId,omitempty"`
	AutomationName string `json:"automationName,omitempty"`
	AutoApproved   bool   `json:"autoApproved,omitempty"`
}

type PropertyWriteResult struct {
	Device  device.Device `json:"device"`
	Command any           `json:"command"`
}

var (
	ErrGatewayAccessDenied = errors.New("MCP gateway access denied")
	ErrStateChanged        = errors.New("device state changed before execution")
)

type Server struct {
	path     string
	tools    *application.MCPToolService
	devices  *application.DeviceService
	audit    *application.AuditService
	mu       sync.Mutex
	listener net.Listener
}

func NewServer(path string, tools *application.MCPToolService, devices *application.DeviceService) *Server {
	return &Server{path: path, tools: tools, devices: devices}
}

func (s *Server) SetAuditService(audit *application.AuditService) { s.audit = audit }

func (s *Server) Serve(ctx context.Context) error {
	if s.path == "" || s.tools == nil || s.devices == nil {
		return errors.New("MCP gateway requires a socket path, tool service, and device service")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create MCP gateway directory: %w", err)
	}
	if info, err := os.Lstat(s.path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("MCP gateway path exists and is not a socket")
		}
		if err := os.Remove(s.path); err != nil {
			return fmt.Errorf("remove stale MCP gateway socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect MCP gateway socket: %w", err)
	}
	listener, err := net.Listen("unix", s.path)
	if err != nil {
		return fmt.Errorf("listen MCP gateway socket: %w", err)
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		_ = listener.Close()
		return fmt.Errorf("protect MCP gateway socket: %w", err)
	}
	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.listener = nil
		s.mu.Unlock()
		_ = listener.Close()
		_ = os.Remove(s.path)
	}()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			if ctx.Err() != nil || errors.Is(acceptErr, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept MCP gateway connection: %w", acceptErr)
		}
		go s.serveConnection(ctx, connection)
	}
}

func (s *Server) Close() error {
	s.mu.Lock()
	listener := s.listener
	s.mu.Unlock()
	if listener != nil {
		return listener.Close()
	}
	return nil
}

func (s *Server) serveConnection(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	decoder, encoder := json.NewDecoder(io.LimitReader(connection, 1<<20)), json.NewEncoder(connection)
	for {
		var request Request
		if err := decoder.Decode(&request); err != nil {
			return
		}
		response := s.Handle(ctx, request)
		if err := encoder.Encode(response); err != nil {
			return
		}
	}
}

func (s *Server) Handle(ctx context.Context, request Request) Response {
	if request.Version != ProtocolVersion || request.ID == "" {
		return errorResponse(request.ID, "invalid_request", "protocol version and request id are required")
	}
	if request.CorrelationID != "" {
		ctx = application.WithCorrelationID(ctx, request.CorrelationID)
	}
	var result any
	var err error
	switch request.Method {
	case MethodListDevices:
		result, err = s.tools.ListDevices(ctx)
	case MethodDeviceState:
		var input DeviceStateRequest
		if err = json.Unmarshal(request.Params, &input); err == nil {
			result, err = s.tools.DeviceState(ctx, input.DeviceID)
		}
	case MethodExecuteProperty:
		var input PropertyWriteRequest
		if err = json.Unmarshal(request.Params, &input); err == nil {
			result, err = s.executeProperty(ctx, input)
		}
	default:
		return errorResponse(request.ID, "method_not_found", "unsupported MCP gateway method")
	}
	if err != nil {
		return errorFor(request.ID, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return errorResponse(request.ID, "internal", "encode MCP gateway response")
	}
	return Response{ID: request.ID, Result: encoded}
}

func (s *Server) executeProperty(ctx context.Context, input PropertyWriteRequest) (result PropertyWriteResult, resultErr error) {
	defer func() {
		if s.audit == nil {
			return
		}
		outcome, status := domainaudit.OutcomeSucceeded, 200
		if resultErr != nil {
			outcome, status = domainaudit.OutcomeFailed, 409
		}
		event := domainaudit.Event{
			CorrelationID: application.CorrelationID(ctx), Actor: "mcp-agent-runtime", Action: "execute_property",
			ResourceType: "device-property", ResourceID: input.DeviceID + "/" + input.EndpointID + "/" + input.CapabilityID + "/" + input.PropertyID,
			Method: "MCP", Route: "unix://homeloom-core/mcp", Status: status, Outcome: outcome,
		}
		if result.Command != nil {
			event.Details = []domainaudit.Detail{{Label: "command", Value: "accepted"}}
		}
		if input.AIExecution != nil {
			if input.AIExecution.RunID != "" {
				event.Details = append(event.Details, domainaudit.Detail{Label: "agent_run", Value: input.AIExecution.RunID})
			}
			if input.AIExecution.AutomationID != "" {
				event.Details = append(event.Details, domainaudit.Detail{Label: "automation", Value: input.AIExecution.AutomationID})
			}
			if input.AIExecution.AutomationName != "" {
				event.Details = append(event.Details, domainaudit.Detail{Label: "automation_name", Value: input.AIExecution.AutomationName})
			}
			if input.AIExecution.Source != "" {
				event.Details = append(event.Details, domainaudit.Detail{Label: "source", Value: input.AIExecution.Source})
			}
			if input.AIExecution.AutoApproved {
				event.Details = append(event.Details, domainaudit.Detail{Label: "approval", Value: "unattended"})
			}
		}
		_, _ = s.audit.Record(ctx, event)
	}()
	if err := input.PropertyPath.Validate(); err != nil {
		return PropertyWriteResult{}, err
	}
	effective, err := s.tools.PropertyAccess(ctx, input.PropertyPath)
	if err != nil {
		return PropertyWriteResult{}, err
	}
	if effective.EffectiveAccess != domainmcp.AccessConfirm {
		return PropertyWriteResult{}, ErrGatewayAccessDenied
	}
	if input.AIExecution != nil && input.AIExecution.AutoApproved && !effective.UnattendedAIAllowed {
		return PropertyWriteResult{}, ErrGatewayAccessDenied
	}
	if input.ExpectedStateVersion != nil && !stateVersionMatches(s.devices.States(input.DeviceID), input.PropertyPath, *input.ExpectedStateVersion) {
		return PropertyWriteResult{}, ErrStateChanged
	}
	updated, command, err := s.devices.ExecuteProperty(ctx, input.DeviceID, input.EndpointID, input.CapabilityID, input.PropertyID, input.Value)
	if err != nil {
		return PropertyWriteResult{}, err
	}
	return PropertyWriteResult{Device: updated, Command: command}, nil
}

func stateVersionMatches(states []domainstate.StateValue, path domainmcp.PropertyPath, version uint64) bool {
	for _, state := range states {
		if state.Key.DeviceID == path.DeviceID && state.Key.EndpointID == path.EndpointID && state.Key.CapabilityID == path.CapabilityID && state.Key.PropertyID == path.PropertyID {
			return state.Version == version
		}
	}
	return false
}

func errorFor(id string, err error) Response {
	switch {
	case errors.Is(err, application.ErrMCPDeviceNotFound), errors.Is(err, application.ErrMCPPropertyNotFound):
		return errorResponse(id, "not_found", "device resource not found")
	case errors.Is(err, application.ErrMCPAccessDenied), errors.Is(err, ErrGatewayAccessDenied):
		return errorResponse(id, "access_denied", "MCP access is not permitted for this resource")
	case errors.Is(err, ErrStateChanged):
		return errorResponse(id, "state_changed", err.Error())
	case errors.Is(err, domainmcp.ErrInvalidConfig):
		return errorResponse(id, "invalid_request", err.Error())
	default:
		return errorResponse(id, "operation_failed", err.Error())
	}
}

func errorResponse(id, code, message string) Response {
	return Response{ID: id, Error: &Error{Code: code, Message: message}}
}

type Client struct {
	SocketPath string
	Timeout    time.Duration
}

func (c Client) Call(ctx context.Context, id, method, correlationID string, params any, result any) error {
	if c.SocketPath == "" {
		return errors.New("MCP gateway socket path is required")
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode MCP gateway request: %w", err)
	}
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return fmt.Errorf("connect MCP gateway: %w", err)
	}
	defer connection.Close()
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	_ = connection.SetDeadline(time.Now().Add(timeout))
	request := Request{Version: ProtocolVersion, ID: id, Method: method, CorrelationID: correlationID, Params: payload}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return fmt.Errorf("write MCP gateway request: %w", err)
	}
	var response Response
	if err := json.NewDecoder(io.LimitReader(connection, 1<<20)).Decode(&response); err != nil {
		return fmt.Errorf("read MCP gateway response: %w", err)
	}
	if response.ID != id {
		return errors.New("MCP gateway response id does not match request")
	}
	if response.Error != nil {
		return &RemoteError{Code: response.Error.Code, Message: response.Error.Message}
	}
	if err := json.Unmarshal(response.Result, result); err != nil {
		return fmt.Errorf("decode MCP gateway response: %w", err)
	}
	return nil
}

type RemoteError struct {
	Code    string
	Message string
}

func (e *RemoteError) Error() string { return e.Code + ": " + e.Message }
