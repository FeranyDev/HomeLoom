package sqlite

import (
	"context"
	"fmt"
	"time"
)

func (s *Store) ListDisabledDeviceIDs(ctx context.Context) ([]string, error) {
	defer s.observe(time.Now())
	rows, err := s.database.QueryContext(ctx, "SELECT device_id FROM device_preferences WHERE disabled = 1 ORDER BY device_id")
	if err != nil {
		return nil, fmt.Errorf("list disabled devices: %w", err)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan disabled device: %w", err)
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func (s *Store) SetDeviceDisabled(ctx context.Context, deviceID string, disabled bool) error {
	defer s.observe(time.Now())
	if disabled {
		_, err := s.database.ExecContext(ctx, `
            INSERT INTO device_preferences(device_id, disabled, updated_at) VALUES (?, 1, ?)
            ON CONFLICT(device_id) DO UPDATE SET disabled = 1, updated_at = excluded.updated_at`,
			deviceID, time.Now().UTC().UnixMilli())
		if err != nil {
			return fmt.Errorf("disable device: %w", err)
		}
		return nil
	}
	if _, err := s.database.ExecContext(ctx, "DELETE FROM device_preferences WHERE device_id = ?", deviceID); err != nil {
		return fmt.Errorf("enable device: %w", err)
	}
	return nil
}
