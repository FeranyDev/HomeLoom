package gormstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/mapping"
	"gorm.io/gorm/clause"
)

func (s *Store) ListModelEnumOverrides(ctx context.Context) ([]mapping.ModelEnumOverride, error) {
	defer s.observe(time.Now())
	var rows []modelEnumOverrideRow
	if err := s.orm.WithContext(ctx).Select("document_json").Order("device_type, endpoint_id, capability_id, property_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list model enum overrides: %w", err)
	}
	result := make([]mapping.ModelEnumOverride, 0, len(rows))
	for _, row := range rows {
		var item mapping.ModelEnumOverride
		if err := json.Unmarshal([]byte(row.DocumentJSON), &item); err != nil {
			return nil, fmt.Errorf("decode model enum override: %w", err)
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Store) SaveModelEnumOverride(ctx context.Context, item mapping.ModelEnumOverride) error {
	defer s.observe(time.Now())
	document, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode model enum override %q: %w", item.ID, err)
	}
	now := time.Now().UTC().UnixMilli()
	row := modelEnumOverrideRow{
		ID: item.ID, DeviceType: string(item.DeviceType), EndpointID: item.EndpointID,
		CapabilityID: item.CapabilityID, PropertyID: item.PropertyID,
		DocumentJSON: jsonDocument(document), CreatedAt: now, UpdatedAt: now,
	}
	err = s.orm.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"device_type", "endpoint_id", "capability_id", "property_id", "document_json", "updated_at"}),
	}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("save model enum override %q: %w", item.ID, err)
	}
	return nil
}

func (s *Store) DeleteModelEnumOverride(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	result := s.orm.WithContext(ctx).Delete(&modelEnumOverrideRow{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("delete model enum override %q: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("model enum override %q not found", id)
	}
	return nil
}
