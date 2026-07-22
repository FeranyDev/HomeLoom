package gormstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/mapping"
	"gorm.io/gorm/clause"
)

func (s *Store) ListCustomModels(ctx context.Context) ([]mapping.CustomModel, error) {
	defer s.observe(time.Now())
	var rows []customUnifiedModelRow
	if err := s.orm.WithContext(ctx).Select("document_json").Order("device_type").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list custom unified models: %w", err)
	}
	result := make([]mapping.CustomModel, 0, len(rows))
	for _, row := range rows {
		var item mapping.CustomModel
		if err := json.Unmarshal([]byte(row.DocumentJSON), &item); err != nil {
			return nil, fmt.Errorf("decode custom unified model: %w", err)
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Store) SaveCustomModel(ctx context.Context, item mapping.CustomModel) error {
	defer s.observe(time.Now())
	document, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode custom unified model %q: %w", item.DeviceType, err)
	}
	now := time.Now().UTC().UnixMilli()
	row := customUnifiedModelRow{DeviceType: string(item.DeviceType), Name: item.Name, Version: item.Version, DocumentJSON: jsonDocument(document), CreatedAt: now, UpdatedAt: now}
	err = s.orm.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "device_type"}}, DoUpdates: clause.AssignmentColumns([]string{"name", "version", "document_json", "updated_at"})}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("save custom unified model %q: %w", item.DeviceType, err)
	}
	return nil
}

func (s *Store) DeleteCustomModel(ctx context.Context, deviceType string) error {
	defer s.observe(time.Now())
	result := s.orm.WithContext(ctx).Where("device_type = ?", deviceType).Delete(&customUnifiedModelRow{})
	if result.Error != nil {
		return fmt.Errorf("delete custom unified model: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("custom unified model %q not found", deviceType)
	}
	return nil
}
