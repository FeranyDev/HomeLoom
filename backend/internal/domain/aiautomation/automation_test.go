package aiautomation

import (
	"testing"
	"time"

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

func TestCronExpressionValidationAndMatching(t *testing.T) {
	cron, err := ParseCronExpression("*/15 9-17 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
	if !cron.Matches(time.Date(2026, 8, 24, 9, 30, 0, 0, time.UTC)) || cron.Matches(time.Date(2026, 8, 24, 9, 31, 0, 0, time.UTC)) || cron.Matches(time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("cron matching was incorrect")
	}
	if _, err := ParseCronExpression("0 9 * *"); err == nil {
		t.Fatal("accepted a four-field cron expression")
	}
	if err := (Automation{Name: "每日检查", Enabled: true, Kind: KindSchedule, Prompt: "检查状态", CronExpression: "0 9 * * *"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Automation{Name: "冲突计划", Enabled: true, Kind: KindSchedule, Prompt: "检查状态", IntervalSeconds: 60, CronExpression: "0 9 * * *"}).Validate(); err == nil {
		t.Fatal("accepted interval and cron in the same schedule")
	}
}

func TestAutomationValidationAcceptsTypedConditionsAndRejectsInvalidComparisons(t *testing.T) {
	conditionPath := domainmcp.PropertyPath{DeviceID: "sensor-1", EndpointID: "main", CapabilityID: "temperature", PropertyID: "current"}
	valid := Automation{Name: "温度检查", Enabled: true, Kind: KindSchedule, Prompt: "检查状态", IntervalSeconds: 60, ConditionMatch: ConditionMatchAny, Conditions: []Condition{{PropertyPath: conditionPath, Operator: ConditionGreaterThan, Value: device.NumberValue(26)}}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	if valid.Normalize().ConditionMatch != ConditionMatchAny {
		t.Fatalf("condition match mode = %q", valid.Normalize().ConditionMatch)
	}
	for _, invalid := range []Automation{
		{Name: "布尔比较", Enabled: true, Kind: KindSchedule, Prompt: "检查状态", IntervalSeconds: 60, Conditions: []Condition{{PropertyPath: conditionPath, Operator: ConditionGreaterThan, Value: device.BoolValue(true)}}},
		{Name: "重复属性", Enabled: true, Kind: KindSchedule, Prompt: "检查状态", IntervalSeconds: 60, Conditions: []Condition{{PropertyPath: conditionPath, Operator: ConditionEquals, Value: device.NumberValue(26)}, {PropertyPath: conditionPath, Operator: ConditionNotEquals, Value: device.NumberValue(30)}}},
		{Name: "无效条件关系", Enabled: true, Kind: KindSchedule, Prompt: "检查状态", IntervalSeconds: 60, ConditionMatch: "some", Conditions: []Condition{{PropertyPath: conditionPath, Operator: ConditionEquals, Value: device.NumberValue(26)}}},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("accepted invalid condition %#v", invalid)
		}
	}
}

func structPath() domainmcp.PropertyPath {
	return domainmcp.PropertyPath{DeviceID: "sensor-1", EndpointID: "main", CapabilityID: "motion", PropertyID: "detected"}
}
