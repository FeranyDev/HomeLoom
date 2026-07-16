package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/mapping"
	"gorm.io/gorm/clause"
)

func (s *Store) ListCustomModelProperties(ctx context.Context) ([]mapping.CustomModelProperty, error) {
	defer s.observe(time.Now())
	var rows []customModelPropertyRow
	if err := s.orm.WithContext(ctx).Select("document_json").Order("device_type, endpoint_id, capability_id, property_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list custom model properties: %w", err)
	}
	result := make([]mapping.CustomModelProperty, 0, len(rows))
	for _, row := range rows {
		var item mapping.CustomModelProperty
		if err := json.Unmarshal([]byte(row.DocumentJSON), &item); err != nil {
			return nil, fmt.Errorf("decode custom model property: %w", err)
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Store) SaveCustomModelProperty(ctx context.Context, item mapping.CustomModelProperty) error {
	defer s.observe(time.Now())
	document, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode custom model property %q: %w", item.ID, err)
	}
	now := time.Now().UTC().UnixMilli()
	row := customModelPropertyRow{ID: item.ID, DeviceType: string(item.DeviceType), EndpointID: item.EndpointID, CapabilityID: item.CapabilityID, PropertyID: item.Definition.ID, DocumentJSON: string(document), CreatedAt: now, UpdatedAt: now}
	err = s.orm.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"device_type", "endpoint_id", "capability_id", "property_id", "document_json", "updated_at"})}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("save custom model property %q: %w", item.ID, err)
	}
	return nil
}

func (s *Store) DeleteCustomModelProperty(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	result := s.orm.WithContext(ctx).Where("id = ?", id).Delete(&customModelPropertyRow{})
	if result.Error != nil {
		return fmt.Errorf("delete custom model property: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("custom model property %q not found", id)
	}
	return nil
}
