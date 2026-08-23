package application_test

import (
	"context"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

type memoryMCPConfigs struct {
	devices    map[string]domainmcp.DeviceConfig
	properties map[domainmcp.PropertyPath]domainmcp.PropertyConfig
}

func newMemoryMCPConfigs() *memoryMCPConfigs {
	return &memoryMCPConfigs{devices: make(map[string]domainmcp.DeviceConfig), properties: make(map[domainmcp.PropertyPath]domainmcp.PropertyConfig)}
}

func (s *memoryMCPConfigs) GetMCPDeviceConfig(_ context.Context, id string) (domainmcp.DeviceConfig, bool, error) {
	value, ok := s.devices[id]
	return value, ok, nil
}
func (s *memoryMCPConfigs) SaveMCPDeviceConfig(_ context.Context, value domainmcp.DeviceConfig) error {
	s.devices[value.DeviceID] = value
	return nil
}
func (s *memoryMCPConfigs) ListMCPPropertyConfigs(_ context.Context, id string) ([]domainmcp.PropertyConfig, error) {
	result := make([]domainmcp.PropertyConfig, 0)
	for _, value := range s.properties {
		if value.DeviceID == id {
			result = append(result, value)
		}
	}
	return result, nil
}
func (s *memoryMCPConfigs) GetMCPPropertyConfig(_ context.Context, path domainmcp.PropertyPath) (domainmcp.PropertyConfig, bool, error) {
	value, ok := s.properties[path]
	return value, ok, nil
}
func (s *memoryMCPConfigs) SaveMCPPropertyConfig(_ context.Context, value domainmcp.PropertyConfig) error {
	s.properties[value.PropertyPath] = value
	return nil
}
func (s *memoryMCPConfigs) DeleteMCPPropertyConfig(_ context.Context, path domainmcp.PropertyPath) error {
	delete(s.properties, path)
	return nil
}

func TestMCPConfigServiceValidatesLiveDevicePathsAndEffectiveAccess(t *testing.T) {
	ctx := context.Background()
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	store := newMemoryMCPConfigs()
	service := application.NewMCPConfigService(store, devices)
	if _, err := service.SaveDevice(ctx, domainmcp.DeviceConfig{DeviceID: "missing", Enabled: true, DefaultAccess: domainmcp.AccessRead}); err != application.ErrMCPDeviceNotFound {
		t.Fatalf("save missing device error = %v", err)
	}
	if _, err := service.SaveDevice(ctx, domainmcp.DeviceConfig{DeviceID: "virtual-switch-1", Enabled: true, UsageNote: "走廊灯", DefaultAccess: domainmcp.AccessRead}); err != nil {
		t.Fatal(err)
	}
	path := domainmcp.PropertyPath{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}
	if _, err := service.SaveProperty(ctx, domainmcp.PropertyConfig{PropertyPath: path, UsageNote: "夜间才建议关闭", Access: domainmcp.AccessConfirm}); err != nil {
		t.Fatal(err)
	}
	effective, err := service.EffectiveProperty(ctx, path)
	if err != nil || effective.EffectiveAccess != domainmcp.AccessConfirm || effective.UsageNote == "" {
		t.Fatalf("effective = %#v, err=%v", effective, err)
	}
	if _, err := service.SaveProperty(ctx, domainmcp.PropertyConfig{PropertyPath: domainmcp.PropertyPath{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "missing"}}); err != application.ErrMCPPropertyNotFound {
		t.Fatalf("save missing property error = %v", err)
	}
}
