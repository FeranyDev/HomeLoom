package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/aiautomation"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
)

var (
	ErrAIAutomationNotFound = errors.New("AI automation not found")
	ErrAIAutomationDisabled = errors.New("AI automation is disabled")
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
	StartAutomation(context.Context, string) (AIAutomationRun, error)
	ApproveAutomation(context.Context, string) (AIAutomationRun, error)
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
	store   AIAutomationStore
	devices *DeviceService
	runner  AIAutomationRunner
	access  AIAutomationAccess
	root    context.Context
	cancel  context.CancelFunc
	now     func() time.Time

	mu      sync.Mutex
	running map[string]struct{}
	unsub   func()
	done    chan struct{}
}

func NewAIAutomationService(parent context.Context, store AIAutomationStore, devices *DeviceService, runner AIAutomationRunner, access AIAutomationAccess) (*AIAutomationService, error) {
	if store == nil || runner == nil {
		return nil, errors.New("AI automation store and runner are required")
	}
	ctx, cancel := context.WithCancel(parent)
	service := &AIAutomationService{store: store, devices: devices, runner: runner, access: access, root: ctx, cancel: cancel, now: func() time.Time { return time.Now().UTC() }, running: make(map[string]struct{}), done: make(chan struct{})}
	if devices != nil {
		service.unsub = devices.SubscribeStates(service.onState)
	} else {
		service.unsub = func() {}
	}
	go service.scheduleLoop()
	return service, nil
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
	if err := value.Validate(); err != nil {
		return aiautomation.Automation{}, err
	}
	if err := s.validateDeviceBinding(value); err != nil {
		return aiautomation.Automation{}, err
	}
	now := s.now()
	if found {
		value.CreatedAt, value.LastRunID, value.LastRunStatus, value.LastRunMessage, value.LastRunAt, value.RunHistory = existing.CreatedAt, existing.LastRunID, existing.LastRunStatus, existing.LastRunMessage, existing.LastRunAt, existing.RunHistory
	} else {
		value.CreatedAt = now
		value.LastRunID, value.LastRunStatus, value.LastRunMessage, value.LastRunAt = "", "", "", time.Time{}
		value.RunHistory = nil
	}
	value.UpdatedAt = now
	if err := s.store.SaveAIAutomation(ctx, value); err != nil {
		return aiautomation.Automation{}, err
	}
	return value, nil
}

func (s *AIAutomationService) Delete(ctx context.Context, id string) error {
	_, found, err := s.store.GetAIAutomation(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return ErrAIAutomationNotFound
	}
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
	if !s.begin(value.ID) {
		return aiautomation.Automation{}, AIAutomationRun{}, errors.New("AI automation is already running")
	}
	defer s.end(value.ID)
	return s.run(ctx, value, "manual")
}

// ApproveRun keeps the durable task history aligned when a task configured
// for manual execution is explicitly approved from the management page.
func (s *AIAutomationService) ApproveRun(ctx context.Context, automationID, runID string) (aiautomation.Automation, AIAutomationRun, error) {
	value, found, err := s.store.GetAIAutomation(ctx, automationID)
	if err != nil {
		return aiautomation.Automation{}, AIAutomationRun{}, err
	}
	if !found {
		return aiautomation.Automation{}, AIAutomationRun{}, ErrAIAutomationNotFound
	}
	run, err := s.runner.ApproveAutomation(ctx, runID)
	if err != nil {
		return aiautomation.Automation{}, AIAutomationRun{}, err
	}
	for index := range value.RunHistory {
		if value.RunHistory[index].ID != run.ID {
			continue
		}
		run.Source = value.RunHistory[index].Source
		run.AutoApproved = value.RunHistory[index].AutoApproved
		value.RunHistory[index].Status = run.Status
		value.RunHistory[index].Message = run.Message
		if run.Action != nil {
			value.RunHistory[index].Action = run.Action
		}
		break
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
		if !item.Enabled || item.Kind != aiautomation.KindSchedule || !scheduleDue(item, now) || !s.begin(item.ID) {
			continue
		}
		go func(value aiautomation.Automation) {
			defer s.end(value.ID)
			runCtx, cancel := context.WithTimeout(s.root, aiAutomationRunTimeout)
			defer cancel()
			_, _, _ = s.run(runCtx, value, "schedule")
		}(item)
	}
}

func (s *AIAutomationService) onState(state domainstate.StateValue) {
	if !state.Known || !state.Available {
		return
	}
	items, err := s.store.ListAIAutomations(s.root)
	if err != nil {
		return
	}
	now := s.now()
	for _, item := range items {
		if !item.Enabled || item.Kind != aiautomation.KindTrigger || item.Trigger == nil || !triggerMatches(*item.Trigger, state) || !cooldownElapsed(item, now) || !s.begin(item.ID) {
			continue
		}
		go func(value aiautomation.Automation) {
			defer s.end(value.ID)
			runCtx, cancel := context.WithTimeout(s.root, aiAutomationRunTimeout)
			defer cancel()
			_, _, _ = s.run(runCtx, value, "trigger")
		}(item)
	}
}

func (s *AIAutomationService) run(ctx context.Context, value aiautomation.Automation, source string) (aiautomation.Automation, AIAutomationRun, error) {
	prompt := fmt.Sprintf("[HomeLoom 自动任务：%s；来源：%s]\n%s", value.Name, source, value.Prompt)
	run, err := s.runner.StartAutomation(ctx, prompt)
	if err == nil && run.Status == "awaiting_approval" && value.ExecutionMode == aiautomation.ExecutionModeUnattended {
		planned := run
		approved, approveErr := s.runner.ApproveAutomation(ctx, run.ID)
		if approveErr != nil {
			run = planned
			run.Status = "failed"
			run.Message = "AI 自动批准设备操作失败"
			err = approveErr
		} else {
			run = approved
			if run.Action == nil {
				run.Action = planned.Action
			}
		}
	}
	value.LastRunAt = s.now()
	if run.CreatedAt.IsZero() {
		run.CreatedAt = value.LastRunAt
	}
	run.Source = source
	run.AutoApproved = value.ExecutionMode == aiautomation.ExecutionModeUnattended && run.Status == "executed"
	if err != nil {
		if run.ID == "" {
			value.LastRunStatus = "failed"
			value.LastRunMessage = "AI Agent 请求失败"
			value.LastRunID = ""
		} else {
			value.LastRunID, value.LastRunStatus, value.LastRunMessage = run.ID, run.Status, run.Message
		}
	} else {
		value.LastRunID, value.LastRunStatus, value.LastRunMessage = run.ID, run.Status, run.Message
	}
	value.RunHistory = appendRunHistory(value.RunHistory, aiautomation.RunRecord{ID: run.ID, Source: source, Status: value.LastRunStatus, Message: value.LastRunMessage, Action: run.Action, AutoApproved: run.AutoApproved, CreatedAt: value.LastRunAt})
	value.UpdatedAt = value.LastRunAt
	if saveErr := s.store.SaveAIAutomation(context.WithoutCancel(ctx), value); saveErr != nil && err == nil {
		err = saveErr
	}
	return value, run, err
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
	if value.Kind != aiautomation.KindTrigger || value.Trigger == nil || s.devices == nil {
		return nil
	}
	items, err := s.devices.List(context.Background())
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID != value.Trigger.DeviceID || item.Removed {
			continue
		}
		property, found := item.Property(value.Trigger.EndpointID, value.Trigger.CapabilityID, value.Trigger.PropertyID)
		if !found || property.Definition.Type != value.Trigger.Value.Type {
			return fmt.Errorf("%w: trigger property is not available or has a different value type", aiautomation.ErrInvalidAutomation)
		}
		if s.access != nil {
			effective, accessErr := s.access.EffectiveProperty(context.Background(), value.Trigger.PropertyPath)
			if accessErr != nil || effective.EffectiveAccess == domainmcp.AccessHidden {
				return fmt.Errorf("%w: trigger property is not authorized for AI", aiautomation.ErrInvalidAutomation)
			}
		}
		return nil
	}
	return fmt.Errorf("%w: trigger device is not available", aiautomation.ErrInvalidAutomation)
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
	<-s.done
	return nil
}

func (s *AIAutomationService) begin(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.running[id]; exists {
		return false
	}
	s.running[id] = struct{}{}
	return true
}

func (s *AIAutomationService) end(id string) {
	s.mu.Lock()
	delete(s.running, id)
	s.mu.Unlock()
}

func scheduleDue(value aiautomation.Automation, now time.Time) bool {
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
