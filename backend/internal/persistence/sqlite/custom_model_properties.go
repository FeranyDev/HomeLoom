package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func (s *Store) ListCustomModelProperties(ctx context.Context) ([]mapping.CustomModelProperty, error) {
	defer s.observe(time.Now())
	rows, err := s.database.QueryContext(ctx, "SELECT document_json FROM custom_model_properties ORDER BY device_type, endpoint_id, capability_id, property_id")
	if err != nil {
		return nil, fmt.Errorf("list custom model properties: %w", err)
	}
	defer rows.Close()
	result := make([]mapping.CustomModelProperty, 0)
	for rows.Next() {
		var document string
		if err := rows.Scan(&document); err != nil {
			return nil, fmt.Errorf("scan custom model property: %w", err)
		}
		var item mapping.CustomModelProperty
		if err := json.Unmarshal([]byte(document), &item); err != nil {
			return nil, fmt.Errorf("decode custom model property: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate custom model properties: %w", err)
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
	_, err = s.database.ExecContext(ctx, `INSERT INTO custom_model_properties(id, device_type, endpoint_id, capability_id, property_id, document_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET device_type = excluded.device_type, endpoint_id = excluded.endpoint_id, capability_id = excluded.capability_id, property_id = excluded.property_id, document_json = excluded.document_json, updated_at = excluded.updated_at`, item.ID, item.DeviceType, item.EndpointID, item.CapabilityID, item.Definition.ID, string(document), now, now)
	if err != nil {
		return fmt.Errorf("save custom model property %q: %w", item.ID, err)
	}
	return nil
}

func (s *Store) DeleteCustomModelProperty(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	result, err := s.database.ExecContext(ctx, "DELETE FROM custom_model_properties WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete custom model property: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted custom model property count: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("custom model property %q not found", id)
	}
	return nil
}
