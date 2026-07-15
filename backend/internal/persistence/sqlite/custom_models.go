package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func (s *Store) ListCustomModels(ctx context.Context) ([]mapping.CustomModel, error) {
	defer s.observe(time.Now())
	rows, err := s.database.QueryContext(ctx, "SELECT document_json FROM custom_unified_models ORDER BY device_type")
	if err != nil {
		return nil, fmt.Errorf("list custom unified models: %w", err)
	}
	defer rows.Close()
	result := make([]mapping.CustomModel, 0)
	for rows.Next() {
		var document string
		if err := rows.Scan(&document); err != nil {
			return nil, fmt.Errorf("scan custom unified model: %w", err)
		}
		var item mapping.CustomModel
		if err := json.Unmarshal([]byte(document), &item); err != nil {
			return nil, fmt.Errorf("decode custom unified model: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate custom unified models: %w", err)
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
	_, err = s.database.ExecContext(ctx, `INSERT INTO custom_unified_models(device_type, name, version, document_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(device_type) DO UPDATE SET name = excluded.name, version = excluded.version, document_json = excluded.document_json, updated_at = excluded.updated_at`, item.DeviceType, item.Name, item.Version, string(document), now, now)
	if err != nil {
		return fmt.Errorf("save custom unified model %q: %w", item.DeviceType, err)
	}
	return nil
}

func (s *Store) DeleteCustomModel(ctx context.Context, deviceType string) error {
	defer s.observe(time.Now())
	result, err := s.database.ExecContext(ctx, "DELETE FROM custom_unified_models WHERE device_type = ?", deviceType)
	if err != nil {
		return fmt.Errorf("delete custom unified model: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted custom unified model count: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("custom unified model %q not found", deviceType)
	}
	return nil
}
