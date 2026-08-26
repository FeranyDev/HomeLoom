// Package aiautomation contains durable, vendor-neutral AI task definitions.
package aiautomation

import (
	"errors"
	"fmt"
	"strconv"
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
	MaxConditions      = 16
	MaxCronExpression  = 120
)

var ErrInvalidAutomation = errors.New("invalid AI automation")

type Trigger struct {
	domainmcp.PropertyPath
	Value device.PropertyValue `json:"value"`
}

// ConditionOperator compares a current, known device state with the typed
// value configured on an automation.
type ConditionOperator string

const (
	ConditionEquals             ConditionOperator = "equals"
	ConditionNotEquals          ConditionOperator = "not_equals"
	ConditionGreaterThan        ConditionOperator = "greater_than"
	ConditionGreaterThanOrEqual ConditionOperator = "greater_than_or_equal"
	ConditionLessThan           ConditionOperator = "less_than"
	ConditionLessThanOrEqual    ConditionOperator = "less_than_or_equal"
)

// ConditionMatchMode selects how an automation combines its configured
// conditions. Existing task documents omit the field and therefore retain the
// safer all-conditions behavior.
type ConditionMatchMode string

const (
	ConditionMatchAll ConditionMatchMode = "all"
	ConditionMatchAny ConditionMatchMode = "any"
)

// Condition is evaluated by Core before a scheduled or state-triggered task
// starts an AI session. It never grants visibility to an unauthorized device.
type Condition struct {
	domainmcp.PropertyPath
	Operator ConditionOperator    `json:"operator"`
	Value    device.PropertyValue `json:"value"`
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
	ID                   string    `json:"id"`
	Source               string    `json:"source"`
	Status               string    `json:"status"`
	Message              string    `json:"message"`
	Action               *Action   `json:"action,omitempty"`
	AutoApproved         bool      `json:"autoApproved,omitempty"`
	AutomationGeneration uint64    `json:"automationGeneration"`
	CreatedAt            time.Time `json:"createdAt"`
}

type Automation struct {
	ID              string             `json:"id"`
	Generation      uint64             `json:"generation"`
	Name            string             `json:"name"`
	Enabled         bool               `json:"enabled"`
	Kind            Kind               `json:"kind"`
	Prompt          string             `json:"prompt"`
	ExecutionMode   ExecutionMode      `json:"executionMode"`
	IntervalSeconds int                `json:"intervalSeconds,omitempty"`
	CronExpression  string             `json:"cronExpression,omitempty"`
	CooldownSeconds int                `json:"cooldownSeconds,omitempty"`
	Trigger         *Trigger           `json:"trigger,omitempty"`
	Conditions      []Condition        `json:"conditions,omitempty"`
	ConditionMatch  ConditionMatchMode `json:"conditionMatch,omitempty"`
	LastRunID       string             `json:"lastRunId,omitempty"`
	LastRunStatus   string             `json:"lastRunStatus,omitempty"`
	LastRunMessage  string             `json:"lastRunMessage,omitempty"`
	LastRunAt       time.Time          `json:"lastRunAt,omitzero"`
	RunHistory      []RunRecord        `json:"runHistory,omitempty"`
	CreatedAt       time.Time          `json:"createdAt"`
	UpdatedAt       time.Time          `json:"updatedAt"`
}

func (a Automation) Normalize() Automation {
	a.ID = strings.TrimSpace(a.ID)
	a.Name = strings.TrimSpace(a.Name)
	a.Prompt = strings.TrimSpace(a.Prompt)
	a.CronExpression = strings.TrimSpace(a.CronExpression)
	a.ExecutionMode = ExecutionMode(strings.TrimSpace(string(a.ExecutionMode)))
	// Existing task documents predate executionMode. Their migration path is
	// unattended execution, which is the behavior selected for automations.
	if a.ExecutionMode == "" {
		a.ExecutionMode = ExecutionModeUnattended
	}
	a.ConditionMatch = ConditionMatchMode(strings.TrimSpace(string(a.ConditionMatch)))
	if a.ConditionMatch == "" {
		a.ConditionMatch = ConditionMatchAll
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
	if len(a.Conditions) > 0 {
		a.Conditions = append([]Condition(nil), a.Conditions...)
	}
	for index := range a.Conditions {
		condition := &a.Conditions[index]
		condition.DeviceID = strings.TrimSpace(condition.DeviceID)
		condition.EndpointID = strings.TrimSpace(condition.EndpointID)
		condition.CapabilityID = strings.TrimSpace(condition.CapabilityID)
		condition.PropertyID = strings.TrimSpace(condition.PropertyID)
		condition.Operator = ConditionOperator(strings.TrimSpace(string(condition.Operator)))
		if condition.Operator == "" {
			condition.Operator = ConditionEquals
		}
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
	if a.ConditionMatch != ConditionMatchAll && a.ConditionMatch != ConditionMatchAny {
		return fmt.Errorf("%w: condition match must be all or any", ErrInvalidAutomation)
	}
	if len(a.Conditions) > MaxConditions {
		return fmt.Errorf("%w: no more than %d conditions are allowed", ErrInvalidAutomation, MaxConditions)
	}
	seenConditions := make(map[domainmcp.PropertyPath]struct{}, len(a.Conditions))
	for _, condition := range a.Conditions {
		if condition.PropertyPath.Validate() != nil || !condition.Value.HasSinglePayload() || !conditionOperatorAllowed(condition.Operator, condition.Value.Type) {
			return fmt.Errorf("%w: condition must include a valid property path, compatible operator, and typed value", ErrInvalidAutomation)
		}
		if _, found := seenConditions[condition.PropertyPath]; found {
			return fmt.Errorf("%w: condition properties must be unique", ErrInvalidAutomation)
		}
		seenConditions[condition.PropertyPath] = struct{}{}
	}
	switch a.Kind {
	case KindSchedule:
		if a.CronExpression == "" && (a.IntervalSeconds < MinIntervalSeconds || a.IntervalSeconds > MaxIntervalSeconds) {
			return fmt.Errorf("%w: schedule interval must be between %d and %d seconds", ErrInvalidAutomation, MinIntervalSeconds, MaxIntervalSeconds)
		}
		if a.CronExpression != "" {
			if a.IntervalSeconds != 0 {
				return fmt.Errorf("%w: schedule must use either interval or cron expression", ErrInvalidAutomation)
			}
			if _, err := ParseCronExpression(a.CronExpression); err != nil {
				return fmt.Errorf("%w: invalid cron expression: %v", ErrInvalidAutomation, err)
			}
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

// CronExpression is a five-field cron schedule in the household time zone:
// minute hour day-of-month month day-of-week. Each field accepts *, a number,
// comma-separated values, ranges, and */step or range/step. Day-of-week uses
// 0 or 7 for Sunday. Fields are combined with AND so the result is explicit.
type CronExpression struct{ fields [5]cronField }

type cronField struct {
	min, max int
	values   map[int]struct{}
}

func ParseCronExpression(expression string) (CronExpression, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" || len(expression) > MaxCronExpression {
		return CronExpression{}, errors.New("expression must be 1 to 120 characters")
	}
	parts := strings.Fields(expression)
	if len(parts) != 5 {
		return CronExpression{}, errors.New("expression must contain five fields")
	}
	limits := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	result := CronExpression{}
	for index, part := range parts {
		field, err := parseCronField(part, limits[index][0], limits[index][1])
		if err != nil {
			return CronExpression{}, err
		}
		if index == 4 {
			if _, sunday := field.values[7]; sunday {
				delete(field.values, 7)
				field.values[0] = struct{}{}
			}
			field.max = 6
		}
		result.fields[index] = field
	}
	return result, nil
}

func (c CronExpression) Matches(value time.Time) bool {
	return c.fields[0].matches(value.Minute()) && c.fields[1].matches(value.Hour()) && c.fields[2].matches(value.Day()) && c.fields[3].matches(int(value.Month())) && c.fields[4].matches(int(value.Weekday()))
}

func CronMatches(expression string, value time.Time) bool {
	parsed, err := ParseCronExpression(expression)
	return err == nil && parsed.Matches(value)
}

func parseCronField(raw string, min, max int) (cronField, error) {
	field := cronField{min: min, max: max, values: make(map[int]struct{})}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return cronField{}, errors.New("empty cron list item")
		}
		base, step := item, 1
		if before, after, found := strings.Cut(item, "/"); found {
			base = before
			parsed, err := strconv.Atoi(after)
			if err != nil || parsed < 1 || parsed > max-min+1 {
				return cronField{}, errors.New("invalid cron step")
			}
			step = parsed
		}
		start, end := min, max
		switch {
		case base == "*":
		case strings.Contains(base, "-"):
			left, right, ok := strings.Cut(base, "-")
			if !ok {
				return cronField{}, errors.New("invalid cron range")
			}
			var err error
			start, err = strconv.Atoi(left)
			if err != nil {
				return cronField{}, errors.New("invalid cron range")
			}
			end, err = strconv.Atoi(right)
			if err != nil {
				return cronField{}, errors.New("invalid cron range")
			}
		default:
			parsed, err := strconv.Atoi(base)
			if err != nil {
				return cronField{}, errors.New("invalid cron value")
			}
			start, end = parsed, parsed
		}
		if start < min || end > max || start > end {
			return cronField{}, errors.New("cron value outside its field range")
		}
		for value := start; value <= end; value += step {
			field.values[value] = struct{}{}
		}
	}
	return field, nil
}

func (f cronField) matches(value int) bool {
	_, found := f.values[value]
	return found
}

func conditionOperatorAllowed(operator ConditionOperator, valueType device.ValueType) bool {
	switch operator {
	case ConditionEquals, ConditionNotEquals:
		return true
	case ConditionGreaterThan, ConditionGreaterThanOrEqual, ConditionLessThan, ConditionLessThanOrEqual:
		return valueType == device.ValueTypeInt || valueType == device.ValueTypeNumber
	default:
		return false
	}
}
