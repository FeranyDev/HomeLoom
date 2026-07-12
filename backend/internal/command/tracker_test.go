package command

import (
	"errors"
	"testing"
	"time"

	domaincommand "github.com/feranydev/homeloom/backend/internal/domain/command"
)

func TestCommandConfirmsOnlyMatchingState(t *testing.T) {
	tracker := NewTracker(time.Second)
	command := tracker.BeginBool("switch", "power", true)
	tracker.Sent(command.ID)
	tracker.Accepted(command.ID)
	tracker.ConfirmBool("switch", "power", false)
	current, _ := tracker.Get(command.ID)
	if current.Status != domaincommand.StatusAccepted {
		t.Fatalf("status = %s", current.Status)
	}
	tracker.ConfirmBool("switch", "power", true)
	current, _ = tracker.Get(command.ID)
	if current.Status != domaincommand.StatusConfirmed {
		t.Fatalf("status = %s", current.Status)
	}
}

func TestCommandRejectAndTimeout(t *testing.T) {
	tracker := NewTracker(-time.Millisecond)
	rejected := tracker.BeginBool("a", "power", true)
	tracker.Rejected(rejected.ID, errors.New("offline"))
	current, _ := tracker.Get(rejected.ID)
	if current.Status != domaincommand.StatusRejected || current.Error != "offline" {
		t.Fatalf("rejected = %#v", current)
	}
	timedOut := tracker.BeginBool("b", "power", true)
	current, _ = tracker.Get(timedOut.ID)
	if current.Status != domaincommand.StatusTimeout {
		t.Fatalf("timeout = %#v", current)
	}
}
