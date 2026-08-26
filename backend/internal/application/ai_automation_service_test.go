package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/aiautomation"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
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
	input     AIAutomationInvocation
	started   chan struct{}
}

type blockingAutomationRunner struct {
	started   chan struct{}
	approvals int
}

func (s *blockingAutomationRunner) StartAutomation(ctx context.Context, _ AIAutomationInvocation) (AIAutomationRun, error) {
	s.started <- struct{}{}
	<-ctx.Done()
	return AIAutomationRun{ID: "run-cancelled", Status: "awaiting_approval", Message: "等待批准"}, ctx.Err()
}

func (s *blockingAutomationRunner) ApproveAutomation(context.Context, string, bool) (AIAutomationRun, error) {
	s.approvals++
	return AIAutomationRun{ID: "run-cancelled", Status: "executed"}, nil
}

func (s *automationRunnerStub) StartAutomation(_ context.Context, input AIAutomationInvocation) (AIAutomationRun, error) {
	s.calls++
	s.last, s.input = input.Prompt, input
	if s.started != nil {
		s.started <- struct{}{}
	}
	return AIAutomationRun{ID: "run-1", Status: "awaiting_approval", Message: "等待批准", Action: &aiautomation.Action{PropertyPath: propertyPath(), Value: device.BoolValue(true), DeviceName: "传感器", PropertyName: "检测"}}, nil
}

func (s *automationRunnerStub) ApproveAutomation(_ context.Context, _ string, unattended bool) (AIAutomationRun, error) {
	s.approvals++
	if !unattended && s.input.Source != "manual" {
		return AIAutomationRun{}, fmt.Errorf("unexpected manual approval")
	}
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
	if runner.input.Source != "manual" || runner.input.Trigger != nil {
		t.Fatalf("automation invocation = %#v", runner.input)
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

func TestAIAutomationServiceStopsInFlightRunWhenTaskIsDisabled(t *testing.T) {
	store := &memoryAIAutomations{items: map[string]aiautomation.Automation{}}
	runner := &blockingAutomationRunner{started: make(chan struct{}, 1)}
	service, err := NewAIAutomationService(context.Background(), store, nil, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	task, err := service.Create(context.Background(), aiautomation.Automation{Name: "执行前检查", Enabled: true, Kind: aiautomation.KindSchedule, Prompt: "检查后操作", IntervalSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, _, runErr := service.RunNow(context.Background(), task.ID)
		result <- runErr
	}()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("automation did not begin")
	}
	task.Enabled = false
	updated, err := service.Update(context.Background(), task.ID, task)
	if err != nil || updated.Enabled || updated.Generation != 2 {
		t.Fatalf("disabled task = %#v, err=%v", updated, err)
	}
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("in-flight automation was not cancelled")
	}
	stored, found, err := store.GetAIAutomation(context.Background(), task.ID)
	if err != nil || !found || stored.Enabled || len(stored.RunHistory) != 0 || runner.approvals != 0 {
		t.Fatalf("stale run changed disabled task: stored=%#v approvals=%d err=%v", stored, runner.approvals, err)
	}
}

func TestAIAutomationServiceRejectsApprovalAfterTaskRevision(t *testing.T) {
	store := &memoryAIAutomations{items: map[string]aiautomation.Automation{}}
	runner := &automationRunnerStub{}
	service, err := NewAIAutomationService(context.Background(), store, nil, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	task, err := service.Create(context.Background(), aiautomation.Automation{Name: "待确认", Enabled: true, Kind: aiautomation.KindSchedule, Prompt: "检查", ExecutionMode: aiautomation.ExecutionModeManual, IntervalSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := service.RunNow(context.Background(), task.ID)
	if err != nil || run.Status != "awaiting_approval" {
		t.Fatalf("planned run = %#v, %v", run, err)
	}
	task.Prompt = "已修改的任务"
	if _, err := service.Update(context.Background(), task.ID, task); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ApproveRun(context.Background(), task.ID, run.ID); !errors.Is(err, ErrAIAutomationRunNotApprovable) || runner.approvals != 0 {
		t.Fatalf("revised approval err=%v approvals=%d", err, runner.approvals)
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

func TestScheduleDueUsesHomeTimeZoneAndRunsCronOnlyOncePerMinute(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	item := aiautomation.Automation{Kind: aiautomation.KindSchedule, CronExpression: "0 9 * * *"}
	now := time.Date(2026, 8, 24, 1, 0, 20, 0, time.UTC) // 09:00 in Shanghai
	if !scheduleDue(item, now, location) {
		t.Fatal("cron schedule was not due in its household timezone")
	}
	item.LastRunAt = now.Add(-10 * time.Second)
	if scheduleDue(item, now, location) {
		t.Fatal("cron schedule ran twice in the same household minute")
	}
	if scheduleDue(item, now.Add(time.Minute), location) {
		t.Fatal("cron schedule ran outside its matching minute")
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

func TestAIAutomationServicePassesExactStateObservationToTriggeredRun(t *testing.T) {
	store := &memoryAIAutomations{items: map[string]aiautomation.Automation{}}
	runner := &automationRunnerStub{started: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewAIAutomationService(ctx, store, nil, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	value := true
	observedAt := time.Date(2026, 8, 24, 9, 30, 0, 0, time.UTC)
	task, err := service.Create(context.Background(), aiautomation.Automation{
		Name: "检测到运动", Enabled: true, Kind: aiautomation.KindTrigger, Prompt: "检查客厅", Trigger: &aiautomation.Trigger{PropertyPath: propertyPath(), Value: device.BoolValue(true)}, CooldownSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := domainstate.StateValue{Key: domainstate.Key{DeviceID: "sensor-1", EndpointID: "main", CapabilityID: "motion", PropertyID: "detected"}, Value: domainstate.Value{Kind: domainstate.KindBool, Bool: &value}, Known: true, Available: true, ObservedAt: observedAt, ReceivedAt: observedAt.Add(time.Second), Version: 12, Quality: domainstate.QualityReported}
	service.onState(state)

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatalf("triggered task %q was not started", task.ID)
	}
	if runner.input.Source != "trigger" || runner.input.Trigger == nil || runner.input.Trigger.Key != state.Key || runner.input.Trigger.Value.Bool == nil || !*runner.input.Trigger.Value.Bool || !runner.input.Trigger.ObservedAt.Equal(observedAt) || runner.input.Trigger.Version != 12 || runner.input.Trigger.Quality != domainstate.QualityReported {
		t.Fatalf("trigger invocation = %#v", runner.input)
	}
}

func TestAIAutomationServiceStartsScheduledTaskOnlyWhenConditionsAreSatisfied(t *testing.T) {
	store := &memoryAIAutomations{items: map[string]aiautomation.Automation{}}
	runner := &automationRunnerStub{started: make(chan struct{}, 1)}
	devices := NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := NewAIAutomationService(ctx, store, devices, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	path := domainmcp.PropertyPath{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}
	task, err := service.Create(context.Background(), aiautomation.Automation{Name: "夜间检查", Enabled: true, Kind: aiautomation.KindSchedule, Prompt: "检查状态", IntervalSeconds: 60, Conditions: []aiautomation.Condition{{PropertyPath: path, Operator: aiautomation.ConditionEquals, Value: device.BoolValue(true)}}})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	service.RunDue(context.Background())
	select {
	case <-runner.started:
		t.Fatal("condition-failing scheduled task started")
	case <-time.After(75 * time.Millisecond):
	}

	task.Conditions[0].Value = device.BoolValue(false)
	if _, err := service.Update(context.Background(), task.ID, task); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	service.RunDue(context.Background())
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("condition-satisfying scheduled task did not start")
	}
	if runner.input.Source != "schedule" {
		t.Fatalf("scheduled invocation = %#v", runner.input)
	}
}

func TestConditionMatchesTypedComparableState(t *testing.T) {
	value := 26.5
	state := domainstate.StateValue{Value: domainstate.NumberValue(value), Known: true, Available: true}
	condition := aiautomation.Condition{Operator: aiautomation.ConditionGreaterThanOrEqual, Value: device.NumberValue(26.5)}
	if !conditionMatches(condition, state) {
		t.Fatal("matching numeric condition was rejected")
	}
	condition.Operator, condition.Value = aiautomation.ConditionLessThan, device.NumberValue(20)
	if conditionMatches(condition, state) {
		t.Fatal("non-matching numeric condition was accepted")
	}
}

func TestAIAutomationConditionsCanMatchAnyCurrentState(t *testing.T) {
	devices := NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	service := &AIAutomationService{devices: devices}
	conditions := []aiautomation.Condition{
		{PropertyPath: domainmcp.PropertyPath{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}, Operator: aiautomation.ConditionEquals, Value: device.BoolValue(true)},
		{PropertyPath: domainmcp.PropertyPath{DeviceID: "virtual-lightbulb-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}, Operator: aiautomation.ConditionEquals, Value: device.BoolValue(true)},
	}
	matched := domainstate.StateValue{Key: domainstate.Key{DeviceID: "virtual-lightbulb-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}, Value: domainstate.BoolValue(true), Known: true, Available: true}
	if service.conditionsSatisfied(aiautomation.ConditionMatchAll, conditions, &matched) {
		t.Fatal("all condition mode accepted a failing condition")
	}
	if !service.conditionsSatisfied(aiautomation.ConditionMatchAny, conditions, &matched) {
		t.Fatal("any condition mode rejected a matching condition")
	}
}

func propertyPath() domainmcp.PropertyPath {
	return domainmcp.PropertyPath{DeviceID: "sensor-1", EndpointID: "main", CapabilityID: "motion", PropertyID: "detected"}
}
