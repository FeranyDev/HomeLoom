package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/aiautomation"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
)

var (
	ErrAIAutomationNotFound         = errors.New("AI automation not found")
	ErrAIAutomationDisabled         = errors.New("AI automation is disabled")
	ErrAIAutomationRunNotFound      = errors.New("AI automation run not found")
	ErrAIAutomationRunNotApprovable = errors.New("AI automation run cannot be approved")
	ErrAIAutomationSuperseded       = errors.New("AI automation changed or stopped while running")
)

const aiAutomationRunTimeout = 6 * time.Minute

type AIAutomationStore interface {
	ListAIAutomations(context.Context) ([]aiautomation.Automation, error)
	GetAIAutomation(context.Context, string) (aiautomation.Automation, bool, error)
	SaveAIAutomation(context.Context, aiautomation.Automation) error
	DeleteAIAutomation(context.Context, string) error
}

// AIAutomationRunner is deliberately narrower than the Agent implementation.
// It returns only durable, task-safe results; credentials and raw model
// traffic always remain in the local Agent process.
type AIAutomationRunner interface {
	StartAutomation(context.Context, AIAutomationInvocation) (AIAutomationRun, error)
	ApproveAutomation(context.Context, string, bool) (AIAutomationRun, error)
}

// AIAutomationInvocation separates the user-authored task prompt from
// server-generated execution metadata. Trigger is present only for an exact
// state-triggered run and is forwarded as a read-only observation.
type AIAutomationInvocation struct {
	Prompt         string
	Source         string
	AutomationID   string
	AutomationName string
	Trigger        *domainstate.StateValue
}

// AIAutomationAccess makes a state trigger obey the same explicit AI
// authorization as an Agent tool call. A task may not turn an otherwise hidden
// device state into an automation input.
type AIAutomationAccess interface {
	EffectiveProperty(context.Context, domainmcp.PropertyPath) (domainmcp.EffectivePropertyConfig, error)
}

type AIAutomationRun struct {
	ID           string               `json:"id"`
	Source       string               `json:"source,omitempty"`
	Status       string               `json:"status"`
	Message      string               `json:"message"`
	AutoApproved bool                 `json:"autoApproved,omitempty"`
	CreatedAt    time.Time            `json:"createdAt"`
	ExpiresAt    time.Time            `json:"expiresAt,omitempty"`
	Action       *aiautomation.Action `json:"action,omitempty"`
}

type AIAutomationService struct {
	store    AIAutomationStore
	devices  *DeviceService
	runner   AIAutomationRunner
	access   AIAutomationAccess
	root     context.Context
	cancel   context.CancelFunc
	now      func() time.Time
	zoneMu   sync.RWMutex
	timeZone string
	location *time.Location

	mu      sync.Mutex
	running map[string]*automationExecution
	unsub   func()
	done    chan struct{}
}

// automationExecution is in-memory cancellation and revision state for a
// single task run. The durable Generation is checked again before an
// unattended approval and before writing a history result, so a disable,
// update, or delete becomes a reliable stop boundary.
type automationExecution struct {
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
}

func NewAIAutomationService(parent context.Context, store AIAutomationStore, devices *DeviceService, runner AIAutomationRunner, access AIAutomationAccess) (*AIAutomationService, error) {
	if store == nil || runner == nil {
		return nil, errors.New("AI automation store and runner are required")
	}
	ctx, cancel := context.WithCancel(parent)
	service := &AIAutomationService{store: store, devices: devices, runner: runner, access: access, root: ctx, cancel: cancel, now: func() time.Time { return time.Now().UTC() }, timeZone: time.Local.String(), location: time.Local, running: make(map[string]*automationExecution), done: make(chan struct{})}
	if devices != nil {
		service.unsub = devices.SubscribeStates(service.onState)
	} else {
		service.unsub = func() {}
	}
	go service.scheduleLoop()
	return service, nil
}

// SetHomeTimeZone updates the household zone used by cron schedules. Interval
// and trigger tasks remain timezone-independent. Invalid input is rejected so
// the scheduler always has a deterministic fallback.
func (s *AIAutomationService) SetHomeTimeZone(name string) error {
	location, err := time.LoadLocation(strings.TrimSpace(name))
	if err != nil {
		return fmt.Errorf("load home time zone: %w", err)
	}
	s.zoneMu.Lock()
	s.timeZone, s.location = strings.TrimSpace(name), location
	s.zoneMu.Unlock()
	return nil
}

func (s *AIAutomationService) homeLocation() *time.Location {
	s.zoneMu.RLock()
	defer s.zoneMu.RUnlock()
	if s.location != nil {
		return s.location
	}
	return time.Local
}

func (s *AIAutomationService) List(ctx context.Context) ([]aiautomation.Automation, error) {
	return s.store.ListAIAutomations(ctx)
}

func (s *AIAutomationService) Create(ctx context.Context, value aiautomation.Automation) (aiautomation.Automation, error) {
	if value.ID != "" {
		return aiautomation.Automation{}, fmt.Errorf("%w: ID is assigned by HomeLoom", aiautomation.ErrInvalidAutomation)
	}
	value.ID = newAIAutomationID()
	return s.save(ctx, value, false)
}

func (s *AIAutomationService) Update(ctx context.Context, id string, value aiautomation.Automation) (aiautomation.Automation, error) {
	value.ID = id
	return s.save(ctx, value, true)
}

func (s *AIAutomationService) save(ctx context.Context, value aiautomation.Automation, requireExisting bool) (aiautomation.Automation, error) {
	value = value.Normalize()
	if err := value.Validate(); err != nil {
		return aiautomation.Automation{}, err
	}
	if err := s.validateDeviceBinding(value); err != nil {
		return aiautomation.Automation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, found, err := s.store.GetAIAutomation(ctx, value.ID)
	if err != nil {
		return aiautomation.Automation{}, err
	}
	if requireExisting && !found {
		return aiautomation.Automation{}, ErrAIAutomationNotFound
	}
	if !requireExisting && found {
		return aiautomation.Automation{}, errors.New("AI automation ID already exists")
	}
	now := s.now()
	if found {
		value.CreatedAt, value.LastRunID, value.LastRunStatus, value.LastRunMessage, value.LastRunAt, value.RunHistory = existing.CreatedAt, existing.LastRunID, existing.LastRunStatus, existing.LastRunMessage, existing.LastRunAt, existing.RunHistory
		value.Generation = existing.Generation + 1
		s.cancelRunningLocked(value.ID)
	} else {
		value.CreatedAt = now
		value.LastRunID, value.LastRunStatus, value.LastRunMessage, value.LastRunAt = "", "", "", time.Time{}
		value.RunHistory = nil
		value.Generation = 1
	}
	value.UpdatedAt = now
	if err := s.store.SaveAIAutomation(ctx, value); err != nil {
		return aiautomation.Automation{}, err
	}
	return value, nil
}

func (s *AIAutomationService) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, found, err := s.store.GetAIAutomation(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return ErrAIAutomationNotFound
	}
	s.cancelRunningLocked(id)
	return s.store.DeleteAIAutomation(ctx, id)
}

func (s *AIAutomationService) RunNow(ctx context.Context, id string) (aiautomation.Automation, AIAutomationRun, error) {
	value, found, err := s.store.GetAIAutomation(ctx, id)
	if err != nil {
		return aiautomation.Automation{}, AIAutomationRun{}, err
	}
	if !found {
		return aiautomation.Automation{}, AIAutomationRun{}, ErrAIAutomationNotFound
	}
	if !value.Enabled {
		return aiautomation.Automation{}, AIAutomationRun{}, ErrAIAutomationDisabled
	}
	runCtx, cancel := context.WithTimeout(ctx, aiAutomationRunTimeout)
	execution, started, beginErr := s.begin(runCtx, value)
	if beginErr != nil {
		cancel()
		return aiautomation.Automation{}, AIAutomationRun{}, beginErr
	}
	if !started {
		cancel()
		return aiautomation.Automation{}, AIAutomationRun{}, errors.New("AI automation is already running")
	}
	defer cancel()
	defer s.end(value.ID, execution)
	return s.run(execution.ctx, value, "manual", nil, execution)
}

// ApproveRun keeps the durable task history aligned when a task configured
// for manual execution is explicitly approved from the management page.
func (s *AIAutomationService) ApproveRun(ctx context.Context, automationID, runID string) (aiautomation.Automation, AIAutomationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, found, err := s.store.GetAIAutomation(ctx, automationID)
	if err != nil {
		return aiautomation.Automation{}, AIAutomationRun{}, err
	}
	if !found {
		return aiautomation.Automation{}, AIAutomationRun{}, ErrAIAutomationNotFound
	}
	if !value.Enabled {
		return aiautomation.Automation{}, AIAutomationRun{}, ErrAIAutomationDisabled
	}
	historyIndex := -1
	for index := range value.RunHistory {
		if value.RunHistory[index].ID == runID {
			historyIndex = index
			break
		}
	}
	if historyIndex < 0 {
		return aiautomation.Automation{}, AIAutomationRun{}, ErrAIAutomationRunNotFound
	}
	record := value.RunHistory[historyIndex]
	if record.Status != "awaiting_approval" || record.AutomationGeneration != value.Generation {
		return aiautomation.Automation{}, AIAutomationRun{}, ErrAIAutomationRunNotApprovable
	}
	run, err := s.runner.ApproveAutomation(ctx, runID, false)
	if err != nil {
		return aiautomation.Automation{}, AIAutomationRun{}, err
	}
	run.Source = record.Source
	run.AutoApproved = record.AutoApproved
	value.RunHistory[historyIndex].Status = run.Status
	value.RunHistory[historyIndex].Message = run.Message
	if run.Action != nil {
		value.RunHistory[historyIndex].Action = run.Action
	}
	if value.LastRunID == run.ID {
		value.LastRunStatus, value.LastRunMessage = run.Status, run.Message
	}
	value.UpdatedAt = s.now()
	if err := s.store.SaveAIAutomation(context.WithoutCancel(ctx), value); err != nil {
		return aiautomation.Automation{}, AIAutomationRun{}, err
	}
	return value, run, nil
}

func (s *AIAutomationService) RunDue(ctx context.Context) {
	items, err := s.store.ListAIAutomations(ctx)
	if err != nil {
		return
	}
	now := s.now()
	for _, item := range items {
		if !item.Enabled || item.Kind != aiautomation.KindSchedule || !scheduleDue(item, now, s.homeLocation()) || !s.conditionsSatisfied(item.ConditionMatch, item.Conditions, nil) {
			continue
		}
		runCtx, cancel := context.WithTimeout(s.root, aiAutomationRunTimeout)
		execution, started, beginErr := s.begin(runCtx, item)
		if beginErr != nil || !started {
			cancel()
			continue
		}
		go func(value aiautomation.Automation, execution *automationExecution, cancel context.CancelFunc) {
			defer s.end(value.ID, execution)
			defer cancel()
			_, _, _ = s.run(execution.ctx, value, "schedule", nil, execution)
		}(item, execution, cancel)
	}
}

func (s *AIAutomationService) onState(state domainstate.StateValue) {
	if !stateUsableForAutomation(state, s.clock()) {
		return
	}
	items, err := s.store.ListAIAutomations(s.root)
	if err != nil {
		return
	}
	now := s.now()
	for _, item := range items {
		if !item.Enabled || item.Kind != aiautomation.KindTrigger || item.Trigger == nil || !triggerMatches(*item.Trigger, state) || !cooldownElapsed(item, now) || !s.conditionsSatisfied(item.ConditionMatch, item.Conditions, &state) {
			continue
		}
		trigger := state
		runCtx, cancel := context.WithTimeout(s.root, aiAutomationRunTimeout)
		execution, started, beginErr := s.begin(runCtx, item)
		if beginErr != nil || !started {
			cancel()
			continue
		}
		go func(value aiautomation.Automation, trigger domainstate.StateValue, execution *automationExecution, cancel context.CancelFunc) {
			defer s.end(value.ID, execution)
			defer cancel()
			_, _, _ = s.run(execution.ctx, value, "trigger", &trigger, execution)
		}(item, trigger, execution, cancel)
	}
}

func (s *AIAutomationService) run(ctx context.Context, value aiautomation.Automation, source string, trigger *domainstate.StateValue, execution *automationExecution) (aiautomation.Automation, AIAutomationRun, error) {
	prompt := fmt.Sprintf("[HomeLoom 自动任务：%s；来源：%s]\n%s", value.Name, source, value.Prompt)
	run, err := s.runner.StartAutomation(ctx, AIAutomationInvocation{Prompt: prompt, Source: source, AutomationID: value.ID, AutomationName: value.Name, Trigger: trigger})
	if err == nil && run.Status == "awaiting_approval" && value.ExecutionMode == aiautomation.ExecutionModeUnattended {
		planned := run
		approved, approveErr := s.approveUnattended(ctx, value, execution, run.ID)
		if approveErr != nil {
			run = planned
			run.Status = "failed"
			if errors.Is(approveErr, ErrAIAutomationSuperseded) {
				run.Message = "自动任务已变更或停止，设备操作未执行"
			} else {
				run.Message = "AI 自动批准设备操作失败"
			}
			err = approveErr
		} else {
			run = approved
			if run.Action == nil {
				run.Action = planned.Action
			}
		}
	}
	now := s.now()
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	run.Source = source
	run.AutoApproved = value.ExecutionMode == aiautomation.ExecutionModeUnattended && run.Status == "executed"
	updated, saveErr := s.recordRun(ctx, value, execution, run, err, now)
	if saveErr != nil && err == nil {
		err = saveErr
	}
	return updated, run, err
}

func (s *AIAutomationService) approveUnattended(ctx context.Context, value aiautomation.Automation, execution *automationExecution, runID string) (AIAutomationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.executionCurrentLocked(value.ID, execution) {
		return AIAutomationRun{}, ErrAIAutomationSuperseded
	}
	current, found, err := s.store.GetAIAutomation(context.WithoutCancel(ctx), value.ID)
	if err != nil {
		return AIAutomationRun{}, err
	}
	if !found || !current.Enabled || current.Generation != execution.generation || current.ExecutionMode != aiautomation.ExecutionModeUnattended {
		return AIAutomationRun{}, ErrAIAutomationSuperseded
	}
	return s.runner.ApproveAutomation(ctx, runID, true)
}

func (s *AIAutomationService) recordRun(ctx context.Context, value aiautomation.Automation, execution *automationExecution, run AIAutomationRun, runErr error, now time.Time) (aiautomation.Automation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.executionCurrentLocked(value.ID, execution) {
		return aiautomation.Automation{}, ErrAIAutomationSuperseded
	}
	current, found, err := s.store.GetAIAutomation(context.WithoutCancel(ctx), value.ID)
	if err != nil {
		return aiautomation.Automation{}, err
	}
	if !found || current.Generation != execution.generation {
		return aiautomation.Automation{}, ErrAIAutomationSuperseded
	}
	current.LastRunAt = now
	if runErr != nil {
		if run.ID == "" {
			current.LastRunStatus = "failed"
			current.LastRunMessage = "AI Agent 请求失败"
			current.LastRunID = ""
		} else {
			current.LastRunID, current.LastRunStatus, current.LastRunMessage = run.ID, run.Status, run.Message
		}
	} else {
		current.LastRunID, current.LastRunStatus, current.LastRunMessage = run.ID, run.Status, run.Message
	}
	current.RunHistory = appendRunHistory(current.RunHistory, aiautomation.RunRecord{ID: run.ID, Source: run.Source, Status: current.LastRunStatus, Message: current.LastRunMessage, Action: run.Action, AutoApproved: run.AutoApproved, AutomationGeneration: current.Generation, CreatedAt: current.LastRunAt})
	current.UpdatedAt = current.LastRunAt
	if err := s.store.SaveAIAutomation(context.WithoutCancel(ctx), current); err != nil {
		return aiautomation.Automation{}, err
	}
	return current, nil
}

func appendRunHistory(history []aiautomation.RunRecord, record aiautomation.RunRecord) []aiautomation.RunRecord {
	result := make([]aiautomation.RunRecord, 0, min(len(history)+1, aiautomation.MaxRunHistory))
	result = append(result, record)
	result = append(result, history...)
	if len(result) > aiautomation.MaxRunHistory {
		result = result[:aiautomation.MaxRunHistory]
	}
	return result
}

func (s *AIAutomationService) validateDeviceBinding(value aiautomation.Automation) error {
	if s.devices == nil || (value.Trigger == nil && len(value.Conditions) == 0) {
		return nil
	}
	items, err := s.devices.List(context.Background())
	if err != nil {
		return err
	}
	if value.Trigger != nil {
		if err := s.validateAutomationProperty(items, value.Trigger.PropertyPath, value.Trigger.Value.Type, "trigger"); err != nil {
			return err
		}
	}
	for _, condition := range value.Conditions {
		if err := s.validateAutomationProperty(items, condition.PropertyPath, condition.Value.Type, "condition"); err != nil {
			return err
		}
	}
	return nil
}

func (s *AIAutomationService) validateAutomationProperty(items []device.Device, path domainmcp.PropertyPath, valueType device.ValueType, label string) error {
	for _, item := range items {
		if item.ID != path.DeviceID || item.Removed {
			continue
		}
		property, found := item.Property(path.EndpointID, path.CapabilityID, path.PropertyID)
		if !found || property.Definition.Type != valueType {
			return fmt.Errorf("%w: %s property is not available or has a different value type", aiautomation.ErrInvalidAutomation, label)
		}
		if s.access != nil {
			effective, accessErr := s.access.EffectiveProperty(context.Background(), path)
			if accessErr != nil || effective.EffectiveAccess == domainmcp.AccessHidden {
				return fmt.Errorf("%w: %s property is not authorized for AI", aiautomation.ErrInvalidAutomation, label)
			}
		}
		return nil
	}
	return fmt.Errorf("%w: %s device is not available", aiautomation.ErrInvalidAutomation, label)
}

func (s *AIAutomationService) scheduleLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer close(s.done)
	for {
		select {
		case <-s.root.Done():
			return
		case <-ticker.C:
			s.RunDue(s.root)
		}
	}
}

func (s *AIAutomationService) Close() error {
	if s == nil {
		return nil
	}
	s.unsub()
	s.cancel()
	s.mu.Lock()
	for _, execution := range s.running {
		execution.cancel()
	}
	s.mu.Unlock()
	<-s.done
	return nil
}

func (s *AIAutomationService) begin(ctx context.Context, value aiautomation.Automation) (*automationExecution, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, found, err := s.store.GetAIAutomation(context.WithoutCancel(ctx), value.ID)
	if err != nil {
		return nil, false, err
	}
	if !found || !current.Enabled || current.Generation != value.Generation {
		return nil, false, nil
	}
	if _, exists := s.running[value.ID]; exists {
		return nil, false, nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	execution := &automationExecution{generation: value.Generation, ctx: runCtx, cancel: cancel}
	s.running[value.ID] = execution
	return execution, true, nil
}

func (s *AIAutomationService) end(id string, execution *automationExecution) {
	s.mu.Lock()
	if s.running[id] == execution {
		delete(s.running, id)
	}
	s.mu.Unlock()
	execution.cancel()
}

func (s *AIAutomationService) cancelRunningLocked(id string) {
	if execution := s.running[id]; execution != nil {
		execution.cancel()
	}
}

func (s *AIAutomationService) executionCurrentLocked(id string, execution *automationExecution) bool {
	return execution != nil && s.running[id] == execution
}

func scheduleDue(value aiautomation.Automation, now time.Time, location *time.Location) bool {
	if value.CronExpression != "" {
		if location == nil {
			location = time.Local
		}
		localNow := now.In(location)
		if !aiautomation.CronMatches(value.CronExpression, localNow) {
			return false
		}
		if value.LastRunAt.IsZero() {
			return true
		}
		last := value.LastRunAt.In(location)
		return last.Year() != localNow.Year() || last.YearDay() != localNow.YearDay() || last.Hour() != localNow.Hour() || last.Minute() != localNow.Minute()
	}
	base := value.LastRunAt
	if base.IsZero() {
		base = value.UpdatedAt
	}
	return !base.IsZero() && !now.Before(base.Add(time.Duration(value.IntervalSeconds)*time.Second))
}

func cooldownElapsed(value aiautomation.Automation, now time.Time) bool {
	return value.LastRunAt.IsZero() || !now.Before(value.LastRunAt.Add(time.Duration(value.CooldownSeconds)*time.Second))
}

func triggerMatches(trigger aiautomation.Trigger, state domainstate.StateValue) bool {
	if trigger.DeviceID != state.Key.DeviceID || trigger.EndpointID != state.Key.EndpointID || trigger.CapabilityID != state.Key.CapabilityID || trigger.PropertyID != state.Key.PropertyID {
		return false
	}
	return stateToPropertyValue(state.Value).Equal(trigger.Value)
}

// conditionsSatisfied fails closed: a missing, stale, unknown, or unavailable
// state never counts as a matched condition. Trigger state is used directly so
// the matching event and condition check share the same observation.
func (s *AIAutomationService) conditionsSatisfied(matchMode aiautomation.ConditionMatchMode, conditions []aiautomation.Condition, trigger *domainstate.StateValue) bool {
	if len(conditions) == 0 {
		return true
	}
	if s.devices == nil {
		return false
	}
	if matchMode == "" {
		matchMode = aiautomation.ConditionMatchAll
	}
	statesByDevice := make(map[string]map[domainstate.Key]domainstate.StateValue)
	for _, condition := range conditions {
		key := domainstate.Key{DeviceID: condition.DeviceID, EndpointID: condition.EndpointID, CapabilityID: condition.CapabilityID, PropertyID: condition.PropertyID}
		var state domainstate.StateValue
		found := false
		if trigger != nil && trigger.Key == key {
			state, found = *trigger, true
		} else {
			states, cached := statesByDevice[condition.DeviceID]
			if !cached {
				states = make(map[domainstate.Key]domainstate.StateValue)
				for _, current := range s.devices.States(condition.DeviceID) {
					states[current.Key] = current
				}
				statesByDevice[condition.DeviceID] = states
			}
			state, found = states[key]
		}
		matched := found && stateUsableForAutomation(state, s.clock()) && conditionMatches(condition, state)
		if matchMode == aiautomation.ConditionMatchAny {
			if matched {
				return true
			}
			continue
		}
		if !matched {
			return false
		}
	}
	return matchMode == aiautomation.ConditionMatchAll
}

func (s *AIAutomationService) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now().UTC()
}

func stateUsableForAutomation(state domainstate.StateValue, now time.Time) bool {
	return state.Known && state.Available && state.Quality != domainstate.QualityStale && (state.ExpiresAt.IsZero() || now.Before(state.ExpiresAt))
}

func conditionMatches(condition aiautomation.Condition, state domainstate.StateValue) bool {
	actual := stateToPropertyValue(state.Value)
	if actual.Type != condition.Value.Type || !actual.HasSinglePayload() {
		return false
	}
	switch condition.Operator {
	case aiautomation.ConditionEquals:
		return actual.Equal(condition.Value)
	case aiautomation.ConditionNotEquals:
		return !actual.Equal(condition.Value)
	case aiautomation.ConditionGreaterThan, aiautomation.ConditionGreaterThanOrEqual, aiautomation.ConditionLessThan, aiautomation.ConditionLessThanOrEqual:
		actualNumber, expectedNumber, ok := comparableNumbers(actual, condition.Value)
		if !ok {
			return false
		}
		switch condition.Operator {
		case aiautomation.ConditionGreaterThan:
			return actualNumber > expectedNumber
		case aiautomation.ConditionGreaterThanOrEqual:
			return actualNumber >= expectedNumber
		case aiautomation.ConditionLessThan:
			return actualNumber < expectedNumber
		default:
			return actualNumber <= expectedNumber
		}
	default:
		return false
	}
}

func comparableNumbers(left, right device.PropertyValue) (float64, float64, bool) {
	if left.Type != right.Type {
		return 0, 0, false
	}
	if left.Type == device.ValueTypeInt && left.Int != nil && right.Int != nil {
		return float64(*left.Int), float64(*right.Int), true
	}
	if left.Type == device.ValueTypeNumber && left.Number != nil && right.Number != nil {
		return *left.Number, *right.Number, true
	}
	return 0, 0, false
}

func stateToPropertyValue(value domainstate.Value) device.PropertyValue {
	switch value.Kind {
	case domainstate.KindBool:
		if value.Bool != nil {
			return device.BoolValue(*value.Bool)
		}
	case domainstate.KindInt:
		if value.Int != nil {
			return device.IntValue(*value.Int)
		}
	case domainstate.KindNumber:
		if value.Number != nil {
			return device.NumberValue(*value.Number)
		}
	case domainstate.KindString:
		if value.String != nil {
			return device.StringValue(*value.String)
		}
	case domainstate.KindEnum:
		if value.String != nil {
			return device.EnumValue(*value.String)
		}
	}
	return device.PropertyValue{}
}

func newAIAutomationID() string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return fmt.Sprintf("ai-task-%d", time.Now().UTC().UnixNano())
	}
	return "ai-task-" + hex.EncodeToString(value)
}
