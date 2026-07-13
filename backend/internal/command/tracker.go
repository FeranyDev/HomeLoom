package command

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	domaincommand "github.com/feranydev/homeloom/backend/internal/domain/command"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

var ErrIdempotencyConflict = errors.New("idempotency key reused with different command parameters")

type Tracker struct {
	mu           sync.RWMutex
	timeoutNanos atomic.Int64
	commands     map[string]domaincommand.Command
	pending      map[string]string
	idempotency  map[string]string
	maxItems     int
	listeners    map[uint64]func(domaincommand.Command)
	nextListener uint64
}

func NewTracker(timeout time.Duration) *Tracker {
	tracker := &Tracker{commands: make(map[string]domaincommand.Command), pending: make(map[string]string), idempotency: make(map[string]string), maxItems: 1000, listeners: make(map[uint64]func(domaincommand.Command))}
	tracker.SetTimeout(timeout)
	return tracker
}

func (t *Tracker) SetTimeout(timeout time.Duration) { t.timeoutNanos.Store(int64(timeout)) }

func (t *Tracker) Timeout() time.Duration { return time.Duration(t.timeoutNanos.Load()) }

func (t *Tracker) SetMaxItems(limit int) {
	if limit < 1 {
		limit = 1
	}
	t.mu.Lock()
	t.maxItems = limit
	for len(t.commands) > t.maxItems && t.removeOldestTerminalLocked() {
	}
	t.mu.Unlock()
}

func (t *Tracker) MaxItems() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.maxItems
}

func (t *Tracker) Begin(deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue) domaincommand.Command {
	command, _ := t.BeginReplacing(deviceID, endpointID, capabilityID, propertyID, value)
	return command
}

func (t *Tracker) BeginReplacing(deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue) (domaincommand.Command, *domaincommand.Command) {
	return t.BeginReplacingCorrelated(deviceID, endpointID, capabilityID, propertyID, value, "")
}

func (t *Tracker) BeginReplacingCorrelated(deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue, correlationID string) (domaincommand.Command, *domaincommand.Command) {
	now := time.Now().UTC()
	key := pendingKey(deviceID, endpointID, capabilityID, propertyID)
	command := domaincommand.Command{
		ID: commandID(), DeviceID: deviceID, EndpointID: endpointID, CapabilityID: capabilityID,
		Kind: domaincommand.KindProperty, PropertyID: propertyID, Expected: value,
		CorrelationID: correlationID,
		Status:        domaincommand.StatusQueued, CreatedAt: now, UpdatedAt: now, Deadline: now.Add(t.Timeout()),
	}
	t.mu.Lock()
	t.pruneLocked()
	var superseded *domaincommand.Command
	if previousID, exists := t.pending[key]; exists {
		previous := t.commands[previousID]
		if !terminal(previous.Status) {
			previous.Status, previous.Outcome, previous.Error, previous.UpdatedAt = domaincommand.StatusSuperseded, domaincommand.OutcomeUnknown, "replaced by a newer command", now
			t.commands[previousID] = previous
			copy := previous
			superseded = &copy
		}
	}
	t.commands[command.ID] = command
	t.pending[key] = command.ID
	t.mu.Unlock()
	if superseded != nil {
		t.notify(*superseded)
	}
	t.notify(command)
	return command, superseded
}

func (t *Tracker) BeginAction(deviceID, endpointID, capabilityID, actionID string, parameters map[string]device.PropertyValue) domaincommand.Command {
	command, _, _ := t.BeginActionIdempotent(deviceID, endpointID, capabilityID, actionID, parameters, "")
	return command
}

func (t *Tracker) BeginActionIdempotent(deviceID, endpointID, capabilityID, actionID string, parameters map[string]device.PropertyValue, idempotencyKey string) (domaincommand.Command, bool, error) {
	return t.BeginActionIdempotentCorrelated(deviceID, endpointID, capabilityID, actionID, parameters, idempotencyKey, "")
}

func (t *Tracker) BeginActionIdempotentCorrelated(deviceID, endpointID, capabilityID, actionID string, parameters map[string]device.PropertyValue, idempotencyKey, correlationID string) (domaincommand.Command, bool, error) {
	now := time.Now().UTC()
	command := domaincommand.Command{ID: commandID(), Kind: domaincommand.KindAction, DeviceID: deviceID, EndpointID: endpointID, CapabilityID: capabilityID, CommandID: actionID, Parameters: cloneParameters(parameters), IdempotencyKey: idempotencyKey, CorrelationID: correlationID, Status: domaincommand.StatusQueued, CreatedAt: now, UpdatedAt: now, Deadline: now.Add(t.Timeout())}
	t.mu.Lock()
	if idempotencyKey != "" {
		scope := actionIdempotencyScope(deviceID, endpointID, capabilityID, actionID, idempotencyKey)
		if existingID, found := t.idempotency[scope]; found {
			existing := t.commands[existingID]
			t.mu.Unlock()
			if !parameterMapsEqual(existing.Parameters, parameters) {
				return domaincommand.Command{}, false, ErrIdempotencyConflict
			}
			return existing, true, nil
		}
	}
	t.pruneLocked()
	t.commands[command.ID] = command
	if idempotencyKey != "" {
		t.idempotency[actionIdempotencyScope(deviceID, endpointID, capabilityID, actionID, idempotencyKey)] = command.ID
	}
	t.mu.Unlock()
	t.notify(command)
	return command, false, nil
}

func cloneParameters(parameters map[string]device.PropertyValue) map[string]device.PropertyValue {
	if len(parameters) == 0 {
		return nil
	}
	result := make(map[string]device.PropertyValue, len(parameters))
	for key, value := range parameters {
		result[key] = value
	}
	return result
}

func (t *Tracker) pruneLocked() {
	for len(t.commands) >= t.maxItems && t.removeOldestTerminalLocked() {
	}
}

func (t *Tracker) removeOldestTerminalLocked() bool {
	var oldestID string
	var oldest time.Time
	for id, command := range t.commands {
		if !terminal(command.Status) {
			continue
		}
		if oldestID == "" || command.CreatedAt.Before(oldest) {
			oldestID, oldest = id, command.CreatedAt
		}
	}
	if oldestID != "" {
		delete(t.commands, oldestID)
		for key, id := range t.idempotency {
			if id == oldestID {
				delete(t.idempotency, key)
			}
		}
		return true
	}
	return false
}

func actionIdempotencyScope(deviceID, endpointID, capabilityID, actionID, key string) string {
	return deviceID + "\x00" + endpointID + "\x00" + capabilityID + "\x00" + actionID + "\x00" + key
}

func parameterMapsEqual(left, right map[string]device.PropertyValue) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		other, ok := right[key]
		if !ok || !valuesEqual(value, other) {
			return false
		}
	}
	return true
}

func (t *Tracker) Sent(id string)      { t.transition(id, domaincommand.StatusSent, "") }
func (t *Tracker) Accepted(id string)  { t.transition(id, domaincommand.StatusAccepted, "") }
func (t *Tracker) Confirmed(id string) { t.transition(id, domaincommand.StatusConfirmed, "") }
func (t *Tracker) Rejected(id string, err error) {
	t.transition(id, domaincommand.StatusRejected, err.Error())
}

func (t *Tracker) Confirm(deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue) bool {
	t.mu.Lock()
	key := pendingKey(deviceID, endpointID, capabilityID, propertyID)
	id, ok := t.pending[key]
	if !ok {
		t.mu.Unlock()
		return false
	}
	command := t.commands[id]
	if !valuesEqual(command.Expected, value) {
		t.mu.Unlock()
		return false
	}
	command.Status = domaincommand.StatusConfirmed
	command.Outcome = domaincommand.OutcomeSucceeded
	command.UpdatedAt = time.Now().UTC()
	t.commands[id] = command
	delete(t.pending, key)
	for len(t.commands) > t.maxItems && t.removeOldestTerminalLocked() {
	}
	t.mu.Unlock()
	t.notify(command)
	return true
}

func (t *Tracker) Get(id string) (domaincommand.Command, bool) {
	t.Expire(time.Now().UTC())
	t.mu.RLock()
	defer t.mu.RUnlock()
	command, ok := t.commands[id]
	return command, ok
}

func (t *Tracker) List() []domaincommand.Command {
	t.Expire(time.Now().UTC())
	t.mu.RLock()
	result := make([]domaincommand.Command, 0, len(t.commands))
	for _, command := range t.commands {
		result = append(result, command)
	}
	t.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

func (t *Tracker) transition(id string, status domaincommand.Status, message string) {
	t.mu.Lock()
	command, ok := t.commands[id]
	if !ok || terminal(command.Status) {
		t.mu.Unlock()
		return
	}
	command.Status, command.Error, command.UpdatedAt = status, message, time.Now().UTC()
	command.Outcome = outcomeForStatus(status)
	t.commands[id] = command
	if terminal(status) {
		delete(t.pending, pendingKey(command.DeviceID, command.EndpointID, command.CapabilityID, command.PropertyID))
		for len(t.commands) > t.maxItems && t.removeOldestTerminalLocked() {
		}
	}
	t.mu.Unlock()
	t.notify(command)
}

func (t *Tracker) Expire(now time.Time) []domaincommand.Command {
	t.mu.Lock()
	expired := make([]domaincommand.Command, 0)
	for id, command := range t.commands {
		if terminal(command.Status) || now.Before(command.Deadline) {
			continue
		}
		command.Status, command.Outcome, command.UpdatedAt = domaincommand.StatusTimeout, domaincommand.OutcomeUnknown, now
		t.commands[id] = command
		delete(t.pending, pendingKey(command.DeviceID, command.EndpointID, command.CapabilityID, command.PropertyID))
		expired = append(expired, command)
	}
	for len(t.commands) > t.maxItems && t.removeOldestTerminalLocked() {
	}
	t.mu.Unlock()
	for _, command := range expired {
		t.notify(command)
	}
	return expired
}

func outcomeForStatus(status domaincommand.Status) domaincommand.Outcome {
	switch status {
	case domaincommand.StatusConfirmed:
		return domaincommand.OutcomeSucceeded
	case domaincommand.StatusRejected:
		return domaincommand.OutcomeFailed
	case domaincommand.StatusTimeout, domaincommand.StatusSuperseded:
		return domaincommand.OutcomeUnknown
	default:
		return ""
	}
}

func (t *Tracker) Subscribe(handler func(domaincommand.Command)) func() {
	t.mu.Lock()
	t.nextListener++
	id := t.nextListener
	t.listeners[id] = handler
	t.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { t.mu.Lock(); delete(t.listeners, id); t.mu.Unlock() }) }
}

func (t *Tracker) notify(command domaincommand.Command) {
	t.mu.RLock()
	listeners := make([]func(domaincommand.Command), 0, len(t.listeners))
	for _, listener := range t.listeners {
		listeners = append(listeners, listener)
	}
	t.mu.RUnlock()
	for _, listener := range listeners {
		listener(command)
	}
}

func terminal(status domaincommand.Status) bool {
	return status == domaincommand.StatusConfirmed || status == domaincommand.StatusRejected || status == domaincommand.StatusTimeout || status == domaincommand.StatusSuperseded
}

func pendingKey(deviceID, endpointID, capabilityID, propertyID string) string {
	return deviceID + "\x00" + endpointID + "\x00" + capabilityID + "\x00" + propertyID
}

func valuesEqual(left, right device.PropertyValue) bool {
	if left.Type != right.Type {
		return false
	}
	switch left.Type {
	case device.ValueTypeBool:
		return left.Bool != nil && right.Bool != nil && *left.Bool == *right.Bool
	case device.ValueTypeInt:
		return left.Int != nil && right.Int != nil && *left.Int == *right.Int
	case device.ValueTypeNumber:
		return left.Number != nil && right.Number != nil && *left.Number == *right.Number
	case device.ValueTypeString, device.ValueTypeEnum:
		return left.String != nil && right.String != nil && *left.String == *right.String
	default:
		return false
	}
}

func commandID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(bytes)
}
