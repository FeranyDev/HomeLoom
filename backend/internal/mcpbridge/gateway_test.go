package mcpbridge

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

type memoryConfigStore struct {
	devices    map[string]domainmcp.DeviceConfig
	properties map[domainmcp.PropertyPath]domainmcp.PropertyConfig
}

func newMemoryConfigStore() *memoryConfigStore {
	return &memoryConfigStore{devices: map[string]domainmcp.DeviceConfig{}, properties: map[domainmcp.PropertyPath]domainmcp.PropertyConfig{}}
}
func (s *memoryConfigStore) GetMCPDeviceConfig(_ context.Context, id string) (domainmcp.DeviceConfig, bool, error) {
	item, ok := s.devices[id]
	return item, ok, nil
}
func (s *memoryConfigStore) SaveMCPDeviceConfig(_ context.Context, item domainmcp.DeviceConfig) error {
	s.devices[item.DeviceID] = item
	return nil
}
func (s *memoryConfigStore) ListMCPPropertyConfigs(_ context.Context, id string) ([]domainmcp.PropertyConfig, error) {
	result := []domainmcp.PropertyConfig{}
	for _, item := range s.properties {
		if item.DeviceID == id {
			result = append(result, item)
		}
	}
	return result, nil
}
func (s *memoryConfigStore) GetMCPPropertyConfig(_ context.Context, path domainmcp.PropertyPath) (domainmcp.PropertyConfig, bool, error) {
	item, ok := s.properties[path]
	return item, ok, nil
}
func (s *memoryConfigStore) SaveMCPPropertyConfig(_ context.Context, item domainmcp.PropertyConfig) error {
	s.properties[item.PropertyPath] = item
	return nil
}
func (s *memoryConfigStore) DeleteMCPPropertyConfig(_ context.Context, path domainmcp.PropertyPath) error {
	delete(s.properties, path)
	return nil
}

func TestGatewayOnlyExecutesConfirmProperties(t *testing.T) {
	ctx := context.Background()
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	configs := application.NewMCPConfigService(newMemoryConfigStore(), devices)
	path := domainmcp.PropertyPath{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}
	if _, err := configs.SaveDevice(ctx, domainmcp.DeviceConfig{DeviceID: path.DeviceID, Enabled: true, DefaultAccess: domainmcp.AccessHidden}); err != nil {
		t.Fatal(err)
	}
	if _, err := configs.SaveProperty(ctx, domainmcp.PropertyConfig{PropertyPath: path, Access: domainmcp.AccessConfirm, UsageNote: "走廊灯"}); err != nil {
		t.Fatal(err)
	}
	gateway := NewServer("unused.sock", application.NewMCPToolService(devices, configs), devices)

	params, _ := json.Marshal(PropertyWriteRequest{PropertyPath: path, Value: device.BoolValue(true)})
	response := gateway.Handle(ctx, Request{Version: ProtocolVersion, ID: "write-1", Method: MethodExecuteProperty, Params: params})
	if response.Error != nil {
		t.Fatalf("confirm write error = %#v", response.Error)
	}
	var written PropertyWriteResult
	if err := json.Unmarshal(response.Result, &written); err != nil {
		t.Fatal(err)
	}
	if written.Command == nil {
		t.Fatalf("write result did not contain a command: %#v", written)
	}

	if _, err := configs.SaveProperty(ctx, domainmcp.PropertyConfig{PropertyPath: path, Access: domainmcp.AccessRead}); err != nil {
		t.Fatal(err)
	}
	response = gateway.Handle(ctx, Request{Version: ProtocolVersion, ID: "write-2", Method: MethodExecuteProperty, Params: params})
	if response.Error == nil || response.Error.Code != "access_denied" {
		t.Fatalf("read-only write response = %#v", response)
	}

	params, _ = json.Marshal(PropertyWriteRequest{PropertyPath: path, Value: device.BoolValue(false), ExpectedStateVersion: pointer(uint64(999))})
	if _, err := configs.SaveProperty(ctx, domainmcp.PropertyConfig{PropertyPath: path, Access: domainmcp.AccessConfirm}); err != nil {
		t.Fatal(err)
	}
	response = gateway.Handle(ctx, Request{Version: ProtocolVersion, ID: "write-3", Method: MethodExecuteProperty, Params: params})
	if response.Error == nil || response.Error.Code != "state_changed" {
		t.Fatalf("stale write response = %#v", response)
	}
}

func pointer[T any](value T) *T { return &value }
