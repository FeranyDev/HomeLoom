package aiautomation

import (
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
)

func TestAutomationValidationRequiresBoundedScheduleOrTypedTrigger(t *testing.T) {
	scheduled := Automation{ID: "morning-check", Name: "晨间检查", Enabled: true, Kind: KindSchedule, Prompt: "检查家中状态", IntervalSeconds: 300}
	if err := scheduled.Validate(); err != nil {
		t.Fatal(err)
	}
	triggered := Automation{ID: "motion-check", Name: "移动提醒", Enabled: true, Kind: KindTrigger, Prompt: "检查客厅情况", CooldownSeconds: 120, Trigger: &Trigger{PropertyPath: structPath(), Value: device.BoolValue(true)}}
	if err := triggered.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Automation{
		{ID: "invalid", Name: "", Kind: KindSchedule, Prompt: "x", IntervalSeconds: 60},
		{ID: "invalid", Name: "任务", Kind: KindSchedule, Prompt: "x", IntervalSeconds: 30},
		{ID: "invalid", Name: "任务", Kind: KindTrigger, Prompt: "x", CooldownSeconds: 60, Trigger: &Trigger{PropertyPath: structPath(), Value: device.PropertyValue{Type: device.ValueTypeBool}}},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("accepted invalid automation %#v", invalid)
		}
	}
}

func structPath() domainmcp.PropertyPath {
	return domainmcp.PropertyPath{DeviceID: "sensor-1", EndpointID: "main", CapabilityID: "motion", PropertyID: "detected"}
}
