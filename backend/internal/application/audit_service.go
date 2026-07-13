package application

import (
	"context"
	"sync"
	"time"

	domainaudit "github.com/feranydev/homeloom/backend/internal/domain/audit"
)

type AuditStore interface {
	AppendAuditEvent(context.Context, domainaudit.Event) (domainaudit.Event, error)
	ListAuditEvents(context.Context, int) ([]domainaudit.Event, error)
}

type AuditService struct {
	store        AuditStore
	mu           sync.RWMutex
	listeners    map[uint64]func(domainaudit.Event)
	nextListener uint64
}

func NewAuditService(store AuditStore) *AuditService {
	return &AuditService{store: store, listeners: make(map[uint64]func(domainaudit.Event))}
}

func (s *AuditService) Record(ctx context.Context, event domainaudit.Event) (domainaudit.Event, error) {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	stored, err := s.store.AppendAuditEvent(ctx, event)
	if err != nil {
		return domainaudit.Event{}, err
	}
	s.mu.RLock()
	listeners := make([]func(domainaudit.Event), 0, len(s.listeners))
	for _, listener := range s.listeners {
		listeners = append(listeners, listener)
	}
	s.mu.RUnlock()
	for _, listener := range listeners {
		listener(stored)
	}
	return stored, nil
}

func (s *AuditService) List(ctx context.Context, limit int) ([]domainaudit.Event, error) {
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	return s.store.ListAuditEvents(ctx, limit)
}

func (s *AuditService) Subscribe(handler func(domainaudit.Event)) func() {
	s.mu.Lock()
	s.nextListener++
	id := s.nextListener
	s.listeners[id] = handler
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.listeners, id)
			s.mu.Unlock()
		})
	}
}
