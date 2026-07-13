package command

import (
	"errors"
	"testing"
	"time"

	domaincommand "github.com/feranydev/homeloom/backend/internal/domain/command"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

func TestCommandConfirmsOnlyMatchingState(t *testing.T) {
	tracker := NewTracker(time.Second)
	command := tracker.Begin("switch", "main", "switch", "power", device.BoolValue(true))
	tracker.Sent(command.ID)
	tracker.Accepted(command.ID)
	tracker.Confirm("switch", "main", "switch", "power", device.BoolValue(false))
	current, _ := tracker.Get(command.ID)
	if current.Status != domaincommand.StatusAccepted {
		t.Fatalf("status = %s", current.Status)
	}
	tracker.Confirm("switch", "main", "switch", "power", device.BoolValue(true))
	current, _ = tracker.Get(command.ID)
	if current.Status != domaincommand.StatusConfirmed {
		t.Fatalf("status = %s", current.Status)
	}
}

func TestCommandRejectAndTimeout(t *testing.T) {
	tracker := NewTracker(-time.Millisecond)
	rejected := tracker.Begin("a", "main", "switch", "power", device.BoolValue(true))
	tracker.Rejected(rejected.ID, errors.New("offline"))
	current, _ := tracker.Get(rejected.ID)
	if current.Status != domaincommand.StatusRejected || current.Error != "offline" {
		t.Fatalf("rejected = %#v", current)
	}
	timedOut := tracker.Begin("b", "main", "switch", "power", device.BoolValue(true))
	current, _ = tracker.Get(timedOut.ID)
	if current.Status != domaincommand.StatusTimeout {
		t.Fatalf("timeout = %#v", current)
	}
}

func TestCommandConfirmsTypedNumber(t *testing.T) {
	tracker := NewTracker(time.Second)
	command := tracker.Begin("sensor", "main", "temperature", "target", device.NumberValue(21.5))
	tracker.Accepted(command.ID)
	tracker.Confirm("sensor", "main", "temperature", "target", device.NumberValue(21.5))
	current, _ := tracker.Get(command.ID)
	if current.Status != domaincommand.StatusConfirmed {
		t.Fatalf("status = %s", current.Status)
	}
	text := "auto"
	if valuesEqual(device.PropertyValue{Type: device.ValueTypeString, String: &text}, device.PropertyValue{Type: device.ValueTypeEnum, String: &text}) {
		t.Fatal("values with different types compared equal")
	}
}

func TestTrackerBoundsTerminalHistory(t *testing.T) {
	tracker := NewTracker(time.Second)
	for index := 0; index < 1001; index++ {
		command := tracker.Begin("switch", "main", "switch", "power", device.BoolValue(true))
		tracker.Rejected(command.ID, errors.New("test"))
	}
	if items := tracker.List(); len(items) != 1000 {
		t.Fatalf("history length = %d", len(items))
	}
}

func TestTrackerPublishesLifecycleTransitions(t *testing.T) {
	tracker := NewTracker(time.Second)
	statuses := make(chan domaincommand.Status, 4)
	unsubscribe := tracker.Subscribe(func(command domaincommand.Command) { statuses <- command.Status })
	defer unsubscribe()
	command := tracker.Begin("switch", "main", "switch", "power", device.BoolValue(true))
	tracker.Sent(command.ID)
	tracker.Accepted(command.ID)
	tracker.Confirm("switch", "main", "switch", "power", device.BoolValue(true))
	for _, expected := range []domaincommand.Status{domaincommand.StatusQueued, domaincommand.StatusSent, domaincommand.StatusAccepted, domaincommand.StatusConfirmed} {
		select {
		case actual := <-statuses:
			if actual != expected {
				t.Fatalf("status = %s, want %s", actual, expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("missing %s", expected)
		}
	}
}

func TestNewCommandSupersedesPendingCommandForSameProperty(t *testing.T) {
	tracker := NewTracker(time.Second)
	first := tracker.Begin("switch", "main", "switch", "power", device.BoolValue(true))
	tracker.Accepted(first.ID)
	second, superseded := tracker.BeginReplacing("switch", "main", "switch", "power", device.BoolValue(false))
	if superseded == nil || superseded.ID != first.ID || superseded.Status != domaincommand.StatusSuperseded {
		t.Fatalf("superseded = %#v", superseded)
	}
	current, _ := tracker.Get(first.ID)
	if current.Status != domaincommand.StatusSuperseded {
		t.Fatalf("first = %#v", current)
	}
	tracker.Accepted(first.ID)
	current, _ = tracker.Get(first.ID)
	if current.Status != domaincommand.StatusSuperseded {
		t.Fatal("terminal command transitioned again")
	}
	tracker.Accepted(second.ID)
	if !tracker.Confirm("switch", "main", "switch", "power", device.BoolValue(false)) {
		t.Fatal("new command was not pending")
	}
}

func TestActionCommandLifecycle(t *testing.T) {
	tracker := NewTracker(time.Second)
	created := tracker.BeginAction("switch", "main", "switch", "set-power", map[string]device.PropertyValue{"value": device.BoolValue(true)})
	tracker.Sent(created.ID)
	tracker.Confirmed(created.ID)
	current, ok := tracker.Get(created.ID)
	if !ok || current.Kind != domaincommand.KindAction || current.CommandID != "set-power" || current.Status != domaincommand.StatusConfirmed || current.Parameters["value"].Bool == nil || !*current.Parameters["value"].Bool {
		t.Fatalf("command = %#v", current)
	}
}

func TestActionIdempotency(t *testing.T) {
	tracker := NewTracker(time.Second)
	parameters := map[string]device.PropertyValue{"value": device.BoolValue(true)}
	first, replayed, err := tracker.BeginActionIdempotent("switch", "main", "switch", "set-power", parameters, "key-1")
	if err != nil || replayed {
		t.Fatalf("first = %#v, %v, %v", first, replayed, err)
	}
	second, replayed, err := tracker.BeginActionIdempotent("switch", "main", "switch", "set-power", parameters, "key-1")
	if err != nil || !replayed || second.ID != first.ID {
		t.Fatalf("second = %#v, %v, %v", second, replayed, err)
	}
	if _, _, err := tracker.BeginActionIdempotent("switch", "main", "switch", "set-power", map[string]device.PropertyValue{"value": device.BoolValue(false)}, "key-1"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflict = %v", err)
	}
}
