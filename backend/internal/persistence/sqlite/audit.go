package sqlite

import (
	"context"
	"fmt"
	"time"

	domainaudit "github.com/feranydev/homeloom/backend/internal/domain/audit"
)

const auditRetentionLimit = 5000

func (s *Store) AppendAuditEvent(ctx context.Context, event domainaudit.Event) (domainaudit.Event, error) {
	defer s.observe(time.Now())
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return domainaudit.Event{}, fmt.Errorf("begin audit event: %w", err)
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `INSERT INTO audit_events(
        correlation_id, actor, action, resource_type, resource_id, method, route, status, outcome, created_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.CorrelationID, event.Actor, event.Action, event.ResourceType, event.ResourceID, event.Method, event.Route, event.Status, event.Outcome, event.CreatedAt.UnixMilli())
	if err != nil {
		return domainaudit.Event{}, fmt.Errorf("append audit event: %w", err)
	}
	event.ID, err = result.LastInsertId()
	if err != nil {
		return domainaudit.Event{}, fmt.Errorf("read audit event id: %w", err)
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM audit_events WHERE id NOT IN (
        SELECT id FROM audit_events ORDER BY id DESC LIMIT ?
    )`, auditRetentionLimit); err != nil {
		return domainaudit.Event{}, fmt.Errorf("prune audit events: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return domainaudit.Event{}, fmt.Errorf("commit audit event: %w", err)
	}
	return event, nil
}

func (s *Store) ListAuditEvents(ctx context.Context, limit int) ([]domainaudit.Event, error) {
	defer s.observe(time.Now())
	rows, err := s.database.QueryContext(ctx, `SELECT id, correlation_id, actor, action, resource_type,
        resource_id, method, route, status, outcome, created_at
        FROM audit_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	events := make([]domainaudit.Event, 0)
	for rows.Next() {
		var event domainaudit.Event
		var createdAt int64
		if err := rows.Scan(&event.ID, &event.CorrelationID, &event.Actor, &event.Action, &event.ResourceType, &event.ResourceID, &event.Method, &event.Route, &event.Status, &event.Outcome, &createdAt); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		event.CreatedAt = time.UnixMilli(createdAt).UTC()
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	return events, nil
}
