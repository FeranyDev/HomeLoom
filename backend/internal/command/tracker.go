package command

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"sync"
	"time"

	domaincommand "github.com/feranydev/homeloom/backend/internal/domain/command"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

type Tracker struct {
	mu       sync.RWMutex
	timeout  time.Duration
	commands map[string]domaincommand.Command
	pending  map[string]string
	maxItems int
}

func NewTracker(timeout time.Duration) *Tracker {
	return &Tracker{timeout: timeout, commands: make(map[string]domaincommand.Command), pending: make(map[string]string), maxItems: 1000}
}

func (t *Tracker) Begin(deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue) domaincommand.Command {
	now := time.Now().UTC()
	command := domaincommand.Command{
		ID: commandID(), DeviceID: deviceID, EndpointID: endpointID, CapabilityID: capabilityID,
		PropertyID: propertyID, Expected: value,
		Status: domaincommand.StatusQueued, CreatedAt: now, UpdatedAt: now, Deadline: now.Add(t.timeout),
	}
	t.mu.Lock()
	t.pruneLocked()
	t.commands[command.ID] = command
	t.pending[pendingKey(deviceID, endpointID, capabilityID, propertyID)] = command.ID
	t.mu.Unlock()
	return command
}

func (t *Tracker) pruneLocked() {
	if len(t.commands) < t.maxItems {
		return
	}
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
	}
}

func (t *Tracker) Sent(id string)     { t.transition(id, domaincommand.StatusSent, "") }
func (t *Tracker) Accepted(id string) { t.transition(id, domaincommand.StatusAccepted, "") }
func (t *Tracker) Rejected(id string, err error) {
	t.transition(id, domaincommand.StatusRejected, err.Error())
}

func (t *Tracker) Confirm(deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := pendingKey(deviceID, endpointID, capabilityID, propertyID)
	id, ok := t.pending[key]
	if !ok {
		return false
	}
	command := t.commands[id]
	if !valuesEqual(command.Expected, value) {
		return false
	}
	command.Status = domaincommand.StatusConfirmed
	command.UpdatedAt = time.Now().UTC()
	t.commands[id] = command
	delete(t.pending, key)
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
	defer t.mu.Unlock()
	command, ok := t.commands[id]
	if !ok || terminal(command.Status) {
		return
	}
	command.Status, command.Error, command.UpdatedAt = status, message, time.Now().UTC()
	t.commands[id] = command
	if terminal(status) {
		delete(t.pending, pendingKey(command.DeviceID, command.EndpointID, command.CapabilityID, command.PropertyID))
	}
}

func (t *Tracker) Expire(now time.Time) []domaincommand.Command {
	t.mu.Lock()
	defer t.mu.Unlock()
	expired := make([]domaincommand.Command, 0)
	for id, command := range t.commands {
		if terminal(command.Status) || now.Before(command.Deadline) {
			continue
		}
		command.Status, command.UpdatedAt = domaincommand.StatusTimeout, now
		t.commands[id] = command
		delete(t.pending, pendingKey(command.DeviceID, command.EndpointID, command.CapabilityID, command.PropertyID))
		expired = append(expired, command)
	}
	return expired
}

func terminal(status domaincommand.Status) bool {
	return status == domaincommand.StatusConfirmed || status == domaincommand.StatusRejected || status == domaincommand.StatusTimeout
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
