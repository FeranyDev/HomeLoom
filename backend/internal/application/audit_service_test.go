package application

import (
	"context"
	"testing"

	domainaudit "github.com/feranydev/homeloom/backend/internal/domain/audit"
)

type memoryAuditStore struct {
	events    []domainaudit.Event
	lastLimit int
}

func (s *memoryAuditStore) AppendAuditEvent(_ context.Context, event domainaudit.Event) (domainaudit.Event, error) {
	event.ID = int64(len(s.events) + 1)
	s.events = append(s.events, event)
	return event, nil
}

func (s *memoryAuditStore) ListAuditEvents(_ context.Context, limit int) ([]domainaudit.Event, error) {
	s.lastLimit = limit
	return append([]domainaudit.Event(nil), s.events...), nil
}

func TestAuditServicePersistsPublishesAndBoundsList(t *testing.T) {
	store := &memoryAuditStore{}
	service := NewAuditService(store)
	received := make(chan domainaudit.Event, 1)
	unsubscribe := service.Subscribe(func(event domainaudit.Event) { received <- event })
	stored, err := service.Record(context.Background(), domainaudit.Event{CorrelationID: "trace-1", Action: "put", Outcome: domainaudit.OutcomeSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID != 1 || stored.CreatedAt.IsZero() {
		t.Fatalf("stored event = %#v", stored)
	}
	if event := <-received; event.ID != stored.ID {
		t.Fatalf("published event = %#v", event)
	}
	unsubscribe()
	if _, err := service.List(context.Background(), 900); err != nil || store.lastLimit != 500 {
		t.Fatalf("list limit = %d, error = %v", store.lastLimit, err)
	}
}

func TestCorrelationIDContextIsBounded(t *testing.T) {
	ctx := WithCorrelationID(context.Background(), "  request-123  ")
	if got := CorrelationID(ctx); got != "request-123" {
		t.Fatalf("correlation id = %q", got)
	}
	long := make([]byte, 200)
	for index := range long {
		long[index] = 'x'
	}
	if got := CorrelationID(WithCorrelationID(context.Background(), string(long))); len(got) != 128 {
		t.Fatalf("bounded correlation id length = %d", len(got))
	}
}
