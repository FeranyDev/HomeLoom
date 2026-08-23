package application_test

import (
	"context"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

func TestMCPToolServiceOnlyExposesExplicitlyEnabledProperties(t *testing.T) {
	ctx := context.Background()
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	configs := newMemoryMCPConfigs()
	configService := application.NewMCPConfigService(configs, devices)
	tools := application.NewMCPToolService(devices, configService)
	if items, err := tools.ListDevices(ctx); err != nil || len(items) != 0 {
		t.Fatalf("default MCP devices = %#v, %v", items, err)
	}
	if _, err := configService.SaveDevice(ctx, domainmcp.DeviceConfig{DeviceID: "virtual-switch-1", Enabled: true, UsageNote: "走廊灯", DefaultAccess: domainmcp.AccessHidden}); err != nil {
		t.Fatal(err)
	}
	path := domainmcp.PropertyPath{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}
	if _, err := configService.SaveProperty(ctx, domainmcp.PropertyConfig{PropertyPath: path, Access: domainmcp.AccessConfirm, UsageNote: "夜间才建议关闭"}); err != nil {
		t.Fatal(err)
	}
	items, err := tools.ListDevices(ctx)
	if err != nil || len(items) != 1 || len(items[0].Properties) != 1 {
		t.Fatalf("MCP devices = %#v, %v", items, err)
	}
	if items[0].UsageNote != "走廊灯" || items[0].Properties[0].Access != domainmcp.AccessConfirm || items[0].Properties[0].UsageNote != "夜间才建议关闭" {
		t.Fatalf("MCP metadata = %#v", items[0])
	}
	state, err := tools.DeviceState(ctx, "virtual-switch-1")
	if err != nil || state.ID != "virtual-switch-1" || len(state.Properties) != 1 {
		t.Fatalf("MCP state = %#v, %v", state, err)
	}
}
