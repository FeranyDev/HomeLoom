package mcpagent

import (
	"context"
	"encoding/json"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/mcpbridge"
)

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *mcpRPCError) Error() string { return e.Message }

func (r *Runtime) mcpCall(ctx context.Context, request mcpRequest) mcpResponse {
	if request.JSONRPC != "2.0" {
		return mcpError(request.ID, -32600, "jsonrpc must be 2.0")
	}
	switch request.Method {
	case "initialize":
		return mcpResult(request.ID, map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "homeloom-mcp-agent", "version": "1"},
		})
	case "notifications/initialized":
		return mcpResult(request.ID, map[string]any{})
	case "tools/list":
		return mcpResult(request.ID, map[string]any{"tools": mcpTools()})
	case "tools/call":
		var input struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(request.Params, &input); err != nil {
			return mcpError(request.ID, -32602, "invalid tool arguments")
		}
		value, err := r.callMCPReadTool(ctx, input.Name, input.Arguments)
		if err != nil {
			return mcpResult(request.ID, map[string]any{"content": []map[string]string{{"type": "text", "text": err.Error()}}, "isError": true})
		}
		encoded, _ := json.Marshal(value)
		return mcpResult(request.ID, map[string]any{
			"content":           []map[string]string{{"type": "text", "text": string(encoded)}},
			"structuredContent": value,
		})
	default:
		return mcpError(request.ID, -32601, "method not found")
	}
}

func (r *Runtime) callMCPReadTool(ctx context.Context, name string, arguments json.RawMessage) (any, error) {
	switch name {
	case "homeloom.list_devices":
		var result []application.MCPDevice
		if err := r.gateway.Call(ctx, newID(), mcpbridge.MethodListDevices, "mcp-client-read", map[string]any{}, &result); err != nil {
			return nil, err
		}
		return result, nil
	case "homeloom.get_device_state":
		var input mcpbridge.DeviceStateRequest
		if err := json.Unmarshal(arguments, &input); err != nil || input.DeviceID == "" {
			return nil, &mcpRPCError{Code: -32602, Message: "deviceId is required"}
		}
		var result application.MCPState
		if err := r.gateway.Call(ctx, newID(), mcpbridge.MethodDeviceState, "mcp-client-read", input, &result); err != nil {
			return nil, err
		}
		return result, nil
	default:
		return nil, &mcpRPCError{Code: -32601, Message: "tool not found"}
	}
}

func mcpTools() []map[string]any {
	return []map[string]any{
		{
			"name": "homeloom.list_devices", "title": "列出已授权的 HomeLoom 设备", "description": "仅返回管理员明确开放给 MCP 的设备、属性与使用备注。",
			"inputSchema": map[string]any{"type": "object", "additionalProperties": false},
			"annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": false},
		},
		{
			"name": "homeloom.get_device_state", "title": "读取设备实时状态", "description": "读取一个已授权设备中已开放属性的当前状态。",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"deviceId": map[string]any{"type": "string"}}, "required": []string{"deviceId"}, "additionalProperties": false},
			"annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": false},
		},
	}
}

func mcpResult(id json.RawMessage, result any) mcpResponse {
	return mcpResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func mcpError(id json.RawMessage, code int, message string) mcpResponse {
	return mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpRPCError{Code: code, Message: message}}
}
