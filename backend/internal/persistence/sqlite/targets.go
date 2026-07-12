package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/target"
)

func (s *Store) SaveTarget(ctx context.Context, item target.Config) error {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin target save: %w", err)
	}
	defer transaction.Rollback()
	now := time.Now().UTC().UnixMilli()
	_, err = transaction.ExecContext(ctx, `
        INSERT INTO targets(id, type, name, enabled, address, pin, setup_id, store_path, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
          type = excluded.type, name = excluded.name, enabled = excluded.enabled,
          address = excluded.address, pin = excluded.pin, setup_id = excluded.setup_id,
          store_path = excluded.store_path, updated_at = excluded.updated_at`,
		item.ID, item.Type, item.Name, item.Enabled, item.Address, item.Pin, item.SetupID, item.StorePath, now, now,
	)
	if err != nil {
		return fmt.Errorf("save target: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM target_device_bindings WHERE target_id = ?", item.ID); err != nil {
		return fmt.Errorf("clear target bindings: %w", err)
	}
	for _, deviceID := range item.DeviceIDs {
		if _, err := transaction.ExecContext(ctx,
			"INSERT INTO target_device_bindings(target_id, device_id) VALUES (?, ?)", item.ID, deviceID,
		); err != nil {
			return fmt.Errorf("save target binding: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit target save: %w", err)
	}
	return nil
}

func (s *Store) DeleteTarget(ctx context.Context, id string) error {
	result, err := s.database.ExecContext(ctx, "DELETE FROM targets WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete target: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted target count: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("target %q not found", id)
	}
	return nil
}

func (s *Store) ListTargets(ctx context.Context) ([]target.Config, error) {
	rows, err := s.database.QueryContext(ctx, `
        SELECT id, type, name, enabled, address, pin, setup_id, store_path
        FROM targets ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list targets: %w", err)
	}
	result := make([]target.Config, 0)
	for rows.Next() {
		var item target.Config
		if err := rows.Scan(
			&item.ID, &item.Type, &item.Name, &item.Enabled, &item.Address,
			&item.Pin, &item.SetupID, &item.StorePath,
		); err != nil {
			return nil, fmt.Errorf("scan target: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate targets: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close target rows: %w", err)
	}
	for index := range result {
		bindings, err := s.targetDeviceIDs(ctx, result[index].ID)
		if err != nil {
			return nil, err
		}
		result[index].DeviceIDs = bindings
	}
	return result, nil
}

func (s *Store) targetDeviceIDs(ctx context.Context, targetID string) ([]string, error) {
	rows, err := s.database.QueryContext(
		ctx,
		"SELECT device_id FROM target_device_bindings WHERE target_id = ? ORDER BY device_id",
		targetID,
	)
	if err != nil {
		return nil, fmt.Errorf("list target bindings: %w", err)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan target binding: %w", err)
		}
		result = append(result, id)
	}
	return result, rows.Err()
}
