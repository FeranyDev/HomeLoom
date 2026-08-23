package gormstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/aiautomation"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) ListAIAutomations(ctx context.Context) ([]aiautomation.Automation, error) {
	defer s.observe(time.Now())
	var rows []aiAutomationRow
	if err := s.orm.WithContext(ctx).Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list AI automations: %w", err)
	}
	result := make([]aiautomation.Automation, 0, len(rows))
	for _, row := range rows {
		value, err := aiAutomation(row)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *Store) GetAIAutomation(ctx context.Context, id string) (aiautomation.Automation, bool, error) {
	defer s.observe(time.Now())
	var row aiAutomationRow
	if err := s.orm.WithContext(ctx).Where("id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return aiautomation.Automation{}, false, nil
		}
		return aiautomation.Automation{}, false, fmt.Errorf("get AI automation: %w", err)
	}
	value, err := aiAutomation(row)
	return value, err == nil, err
}

func (s *Store) SaveAIAutomation(ctx context.Context, value aiautomation.Automation) error {
	defer s.observe(time.Now())
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode AI automation: %w", err)
	}
	row := aiAutomationRow{ID: value.ID, DocumentJSON: jsonDocument(string(encoded)), CreatedAt: value.CreatedAt.UnixMilli(), UpdatedAt: value.UpdatedAt.UnixMilli()}
	if err := s.orm.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"document_json", "updated_at"}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("save AI automation: %w", err)
	}
	return nil
}

func (s *Store) DeleteAIAutomation(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	if err := s.orm.WithContext(ctx).Where("id = ?", id).Delete(&aiAutomationRow{}).Error; err != nil {
		return fmt.Errorf("delete AI automation: %w", err)
	}
	return nil
}

func aiAutomation(row aiAutomationRow) (aiautomation.Automation, error) {
	var value aiautomation.Automation
	if err := json.Unmarshal([]byte(row.DocumentJSON), &value); err != nil {
		return aiautomation.Automation{}, fmt.Errorf("decode AI automation %q: %w", row.ID, err)
	}
	value.ID = row.ID
	value.CreatedAt = time.UnixMilli(row.CreatedAt).UTC()
	value.UpdatedAt = time.UnixMilli(row.UpdatedAt).UTC()
	return value.Normalize(), nil
}
