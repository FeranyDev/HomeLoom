package application

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/aiautomation"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
)

type memoryAIAutomations struct {
	mu    sync.Mutex
	items map[string]aiautomation.Automation
}

func (s *memoryAIAutomations) ListAIAutomations(context.Context) ([]aiautomation.Automation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]aiautomation.Automation, 0, len(s.items))
	for _, item := range s.items {
		result = append(result, item)
	}
	return result, nil
}
func (s *memoryAIAutomations) GetAIAutomation(_ context.Context, id string) (aiautomation.Automation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, found := s.items[id]
	return item, found, nil
}
func (s *memoryAIAutomations) SaveAIAutomation(_ context.Context, item aiautomation.Automation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[item.ID] = item
	return nil
}
func (s *memoryAIAutomations) DeleteAIAutomation(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
	return nil
}

type automationRunnerStub struct {
	calls     int
	approvals int
	last      string
}

func (s *automationRunnerStub) StartAutomation(_ context.Context, prompt string) (AIAutomationRun, error) {
	s.calls++
	s.last = prompt
	return AIAutomationRun{ID: "run-1", Status: "awaiting_approval", Message: "等待批准", Action: &aiautomation.Action{PropertyPath: propertyPath(), Value: device.BoolValue(true), DeviceName: "传感器", PropertyName: "检测"}}, nil
}

func (s *automationRunnerStub) ApproveAutomation(context.Context, string) (AIAutomationRun, error) {
	s.approvals++
	return AIAutomationRun{ID: "run-1", Status: "executed", Message: "设备操作已提交"}, nil
}

func TestAIAutomationServiceRunsUnattendedTaskAndPersistsAIResult(t *testing.T) {
	store := &memoryAIAutomations{items: map[string]aiautomation.Automation{}}
	runner := &automationRunnerStub{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewAIAutomationService(ctx, store, nil, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	task, err := service.Create(context.Background(), aiautomation.Automation{Name: "状态检查", Enabled: true, Kind: aiautomation.KindSchedule, Prompt: "检查状态", IntervalSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	updated, run, err := service.RunNow(context.Background(), task.ID)
	if err != nil || run.Status != "executed" || updated.LastRunID != "run-1" || runner.calls != 1 || runner.approvals != 1 {
		t.Fatalf("run = %#v task=%#v err=%v", run, updated, err)
	}
	if updated.LastRunStatus != "executed" || len(updated.RunHistory) != 1 || updated.RunHistory[0].Message != "设备操作已提交" || !updated.RunHistory[0].AutoApproved || updated.RunHistory[0].Source != "manual" || updated.RunHistory[0].Action == nil {
		t.Fatalf("unattended history = %#v", updated.RunHistory)
	}
}

func TestAIAutomationServiceManualTaskDoesNotAutoApproveAndRetainsHistory(t *testing.T) {
	store := &memoryAIAutomations{items: map[string]aiautomation.Automation{}}
	runner := &automationRunnerStub{}
	service, err := NewAIAutomationService(context.Background(), store, nil, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	task, err := service.Create(context.Background(), aiautomation.Automation{Name: "需确认的任务", Enabled: true, Kind: aiautomation.KindSchedule, Prompt: "检查状态", ExecutionMode: aiautomation.ExecutionModeManual, IntervalSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	updated, run, err := service.RunNow(context.Background(), task.ID)
	if err != nil || run.Status != "awaiting_approval" || runner.approvals != 0 || len(updated.RunHistory) != 1 || updated.RunHistory[0].AutoApproved {
		t.Fatalf("manual run = %#v task=%#v err=%v", run, updated, err)
	}
	updated, run, err = service.ApproveRun(context.Background(), task.ID, run.ID)
	if err != nil || run.Status != "executed" || runner.approvals != 1 || updated.LastRunStatus != "executed" || updated.RunHistory[0].Status != "executed" {
		t.Fatalf("approved task = %#v run=%#v err=%v", updated, run, err)
	}
}

func TestAIAutomationServiceKeepsMostRecentFiftyRunRecords(t *testing.T) {
	history := make([]aiautomation.RunRecord, 0, aiautomation.MaxRunHistory)
	for index := 0; index < aiautomation.MaxRunHistory+1; index++ {
		history = appendRunHistory(history, aiautomation.RunRecord{ID: fmt.Sprintf("run-%d", index)})
	}
	if len(history) != aiautomation.MaxRunHistory || history[0].ID != "run-50" || history[len(history)-1].ID != "run-1" {
		t.Fatalf("history retention = %#v", history)
	}
}

func TestTriggerMatchesOnlyItsTypedPropertyValue(t *testing.T) {
	value := true
	trigger := aiautomation.Trigger{PropertyPath: propertyPath(), Value: device.BoolValue(true)}
	state := domainstate.StateValue{Known: true, Available: true, Key: domainstate.Key{DeviceID: "sensor-1", EndpointID: "main", CapabilityID: "motion", PropertyID: "detected"}, Value: domainstate.Value{Kind: domainstate.KindBool, Bool: &value}}
	if !triggerMatches(trigger, state) {
		t.Fatal("matching state did not trigger task")
	}
	value = false
	state.Value.Bool = &value
	if triggerMatches(trigger, state) {
		t.Fatal("different typed value triggered task")
	}
}

func propertyPath() domainmcp.PropertyPath {
	return domainmcp.PropertyPath{DeviceID: "sensor-1", EndpointID: "main", CapabilityID: "motion", PropertyID: "detected"}
}
