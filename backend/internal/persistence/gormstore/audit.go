package gormstore

import (
	"context"
	"fmt"
	"time"

	domainaudit "github.com/feranydev/homeloom/backend/internal/domain/audit"
	"gorm.io/gorm"
)

const auditRetentionLimit = 5000

func (s *Store) AppendAuditEvent(ctx context.Context, event domainaudit.Event) (domainaudit.Event, error) {
	defer s.observe(time.Now())
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := auditEventRow{CorrelationID: event.CorrelationID, Actor: event.Actor, Action: event.Action, ResourceType: event.ResourceType, ResourceID: event.ResourceID, Method: event.Method, Route: event.Route, Status: event.Status, Outcome: string(event.Outcome), CreatedAt: event.CreatedAt.UnixMilli()}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("append audit event: %w", err)
		}
		event.ID = row.ID
		keep := tx.Model(&auditEventRow{}).Select("id").Order("id DESC").Limit(auditRetentionLimit)
		if err := tx.Where("id NOT IN (?)", keep).Delete(&auditEventRow{}).Error; err != nil {
			return fmt.Errorf("prune audit events: %w", err)
		}
		return nil
	})
	if err != nil {
		return domainaudit.Event{}, err
	}
	return event, nil
}

func (s *Store) ListAuditEvents(ctx context.Context, limit int) ([]domainaudit.Event, error) {
	defer s.observe(time.Now())
	var rows []auditEventRow
	if err := s.orm.WithContext(ctx).Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	events := make([]domainaudit.Event, 0, len(rows))
	for _, row := range rows {
		events = append(events, domainaudit.Event{ID: row.ID, CorrelationID: row.CorrelationID, Actor: row.Actor, Action: row.Action, ResourceType: row.ResourceType, ResourceID: row.ResourceID, Method: row.Method, Route: row.Route, Status: row.Status, Outcome: domainaudit.Outcome(row.Outcome), CreatedAt: time.UnixMilli(row.CreatedAt).UTC()})
	}
	return events, nil
}
