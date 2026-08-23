// Package aiautomation contains durable, vendor-neutral AI task definitions.
package aiautomation

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
)

type Kind string

const (
	KindSchedule Kind = "schedule"
	KindTrigger  Kind = "trigger"

	ExecutionModeUnattended ExecutionMode = "unattended"
	ExecutionModeManual     ExecutionMode = "manual"

	MinIntervalSeconds = 60
	MaxIntervalSeconds = 7 * 24 * 60 * 60
	MaxRunHistory      = 50
)

var ErrInvalidAutomation = errors.New("invalid AI automation")

type Trigger struct {
	domainmcp.PropertyPath
	Value device.PropertyValue `json:"value"`
}

// ExecutionMode determines whether a task-created device operation is
// approved by the automation after the AI has explicitly prepared it. It is
// deliberately scoped to durable automations; interactive AI conversations
// always require a human approval.
type ExecutionMode string

// Action is the durable, display-safe representation of one AI-proposed
// device write. Core still rechecks every value when it is executed.
type Action struct {
	domainmcp.PropertyPath
	Value                device.PropertyValue `json:"value"`
	ExpectedStateVersion *uint64              `json:"expectedStateVersion,omitempty"`
	DeviceName           string               `json:"deviceName"`
	PropertyName         string               `json:"propertyName"`
	UsageNote            string               `json:"usageNote,omitempty"`
}

// RunRecord preserves the outcome returned by an independent AI session for
// one task. Histories are bounded on write so task documents remain compact.
type RunRecord struct {
	ID           string    `json:"id"`
	Source       string    `json:"source"`
	Status       string    `json:"status"`
	Message      string    `json:"message"`
	Action       *Action   `json:"action,omitempty"`
	AutoApproved bool      `json:"autoApproved,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Automation struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	Enabled         bool          `json:"enabled"`
	Kind            Kind          `json:"kind"`
	Prompt          string        `json:"prompt"`
	ExecutionMode   ExecutionMode `json:"executionMode"`
	IntervalSeconds int           `json:"intervalSeconds,omitempty"`
	CooldownSeconds int           `json:"cooldownSeconds,omitempty"`
	Trigger         *Trigger      `json:"trigger,omitempty"`
	LastRunID       string        `json:"lastRunId,omitempty"`
	LastRunStatus   string        `json:"lastRunStatus,omitempty"`
	LastRunMessage  string        `json:"lastRunMessage,omitempty"`
	LastRunAt       time.Time     `json:"lastRunAt,omitzero"`
	RunHistory      []RunRecord   `json:"runHistory,omitempty"`
	CreatedAt       time.Time     `json:"createdAt"`
	UpdatedAt       time.Time     `json:"updatedAt"`
}

func (a Automation) Normalize() Automation {
	a.ID = strings.TrimSpace(a.ID)
	a.Name = strings.TrimSpace(a.Name)
	a.Prompt = strings.TrimSpace(a.Prompt)
	a.ExecutionMode = ExecutionMode(strings.TrimSpace(string(a.ExecutionMode)))
	// Existing task documents predate executionMode. Their migration path is
	// unattended execution, which is the behavior selected for automations.
	if a.ExecutionMode == "" {
		a.ExecutionMode = ExecutionModeUnattended
	}
	a.LastRunID = strings.TrimSpace(a.LastRunID)
	a.LastRunStatus = strings.TrimSpace(a.LastRunStatus)
	a.LastRunMessage = strings.TrimSpace(a.LastRunMessage)
	if len(a.RunHistory) > MaxRunHistory {
		a.RunHistory = append([]RunRecord(nil), a.RunHistory[:MaxRunHistory]...)
	}
	for index := range a.RunHistory {
		a.RunHistory[index].ID = strings.TrimSpace(a.RunHistory[index].ID)
		a.RunHistory[index].Source = strings.TrimSpace(a.RunHistory[index].Source)
		a.RunHistory[index].Status = strings.TrimSpace(a.RunHistory[index].Status)
		a.RunHistory[index].Message = strings.TrimSpace(a.RunHistory[index].Message)
	}
	if a.Kind == KindTrigger && a.CooldownSeconds == 0 {
		a.CooldownSeconds = MinIntervalSeconds
	}
	if a.Trigger != nil {
		value := *a.Trigger
		value.DeviceID = strings.TrimSpace(value.DeviceID)
		value.EndpointID = strings.TrimSpace(value.EndpointID)
		value.CapabilityID = strings.TrimSpace(value.CapabilityID)
		value.PropertyID = strings.TrimSpace(value.PropertyID)
		a.Trigger = &value
	}
	return a
}

func (a Automation) Validate() error {
	a = a.Normalize()
	if a.ID != "" && !device.ValidStableID(a.ID) {
		return fmt.Errorf("%w: invalid id", ErrInvalidAutomation)
	}
	if utf8.RuneCountInString(a.Name) < 1 || utf8.RuneCountInString(a.Name) > 120 {
		return fmt.Errorf("%w: name must be 1 to 120 characters", ErrInvalidAutomation)
	}
	if utf8.RuneCountInString(a.Prompt) < 1 || utf8.RuneCountInString(a.Prompt) > 16<<10 {
		return fmt.Errorf("%w: prompt must be 1 to 16384 characters", ErrInvalidAutomation)
	}
	if a.ExecutionMode != ExecutionModeUnattended && a.ExecutionMode != ExecutionModeManual {
		return fmt.Errorf("%w: execution mode must be unattended or manual", ErrInvalidAutomation)
	}
	switch a.Kind {
	case KindSchedule:
		if a.IntervalSeconds < MinIntervalSeconds || a.IntervalSeconds > MaxIntervalSeconds {
			return fmt.Errorf("%w: schedule interval must be between %d and %d seconds", ErrInvalidAutomation, MinIntervalSeconds, MaxIntervalSeconds)
		}
		if a.Trigger != nil || a.CooldownSeconds != 0 {
			return fmt.Errorf("%w: scheduled task cannot include a trigger", ErrInvalidAutomation)
		}
	case KindTrigger:
		if a.Trigger == nil || a.Trigger.PropertyPath.Validate() != nil || !a.Trigger.Value.HasSinglePayload() {
			return fmt.Errorf("%w: trigger must include a valid property path and typed value", ErrInvalidAutomation)
		}
		if a.CooldownSeconds < MinIntervalSeconds || a.CooldownSeconds > MaxIntervalSeconds {
			return fmt.Errorf("%w: trigger cooldown must be between %d and %d seconds", ErrInvalidAutomation, MinIntervalSeconds, MaxIntervalSeconds)
		}
		if a.IntervalSeconds != 0 {
			return fmt.Errorf("%w: trigger task cannot include a schedule interval", ErrInvalidAutomation)
		}
	default:
		return fmt.Errorf("%w: kind must be schedule or trigger", ErrInvalidAutomation)
	}
	return nil
}
