package command

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"sync"
	"time"

	domaincommand "github.com/feranydev/homeloom/backend/internal/domain/command"
)

type Tracker struct {
	mu       sync.RWMutex
	timeout  time.Duration
	commands map[string]domaincommand.Command
	pending  map[string]string
}

func NewTracker(timeout time.Duration) *Tracker {
	return &Tracker{timeout: timeout, commands: make(map[string]domaincommand.Command), pending: make(map[string]string)}
}

func (t *Tracker) BeginBool(deviceID, propertyID string, value bool) domaincommand.Command {
	now := time.Now().UTC()
	command := domaincommand.Command{
		ID: commandID(), DeviceID: deviceID, PropertyID: propertyID, BoolValue: &value,
		Status: domaincommand.StatusQueued, CreatedAt: now, UpdatedAt: now, Deadline: now.Add(t.timeout),
	}
	t.mu.Lock()
	t.commands[command.ID] = command
	t.pending[pendingKey(deviceID, propertyID)] = command.ID
	t.mu.Unlock()
	return command
}

func (t *Tracker) Sent(id string)     { t.transition(id, domaincommand.StatusSent, "") }
func (t *Tracker) Accepted(id string) { t.transition(id, domaincommand.StatusAccepted, "") }
func (t *Tracker) Rejected(id string, err error) {
	t.transition(id, domaincommand.StatusRejected, err.Error())
}

func (t *Tracker) ConfirmBool(deviceID, propertyID string, value bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := pendingKey(deviceID, propertyID)
	id, ok := t.pending[key]
	if !ok {
		return
	}
	command := t.commands[id]
	if command.BoolValue == nil || *command.BoolValue != value {
		return
	}
	command.Status = domaincommand.StatusConfirmed
	command.UpdatedAt = time.Now().UTC()
	t.commands[id] = command
	delete(t.pending, key)
}

func (t *Tracker) Get(id string) (domaincommand.Command, bool) {
	t.expire()
	t.mu.RLock()
	defer t.mu.RUnlock()
	command, ok := t.commands[id]
	return command, ok
}

func (t *Tracker) List() []domaincommand.Command {
	t.expire()
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
		delete(t.pending, pendingKey(command.DeviceID, command.PropertyID))
	}
}

func (t *Tracker) expire() {
	now := time.Now().UTC()
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, command := range t.commands {
		if terminal(command.Status) || now.Before(command.Deadline) {
			continue
		}
		command.Status, command.UpdatedAt = domaincommand.StatusTimeout, now
		t.commands[id] = command
		delete(t.pending, pendingKey(command.DeviceID, command.PropertyID))
	}
}

func terminal(status domaincommand.Status) bool {
	return status == domaincommand.StatusConfirmed || status == domaincommand.StatusRejected || status == domaincommand.StatusTimeout
}

func pendingKey(deviceID, propertyID string) string { return deviceID + "\x00" + propertyID }

func commandID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(bytes)
}
