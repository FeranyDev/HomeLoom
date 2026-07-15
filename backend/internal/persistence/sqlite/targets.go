package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/target"
)

func (s *Store) SaveTarget(ctx context.Context, item target.Config) error {
	defer s.observe(time.Now())
	pin, err := s.secrets.encrypt("target-pin:"+item.ID, item.Pin)
	if err != nil {
		return fmt.Errorf("encrypt target pin: %w", err)
	}
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
		item.ID, item.Type, item.Name, item.Enabled, item.Address, pin, item.SetupID, item.StorePath, now, now,
	)
	if err != nil {
		return fmt.Errorf("save target: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM target_virtual_devices WHERE target_id = ?", item.ID); err != nil {
		return fmt.Errorf("clear target virtual devices: %w", err)
	}
	devices := item.Devices
	if len(devices) == 0 {
		for _, deviceID := range item.DeviceIDs {
			devices = append(devices, target.VirtualDevice{ID: deviceID, Name: deviceID, SourceDeviceID: deviceID, Enabled: true})
		}
	}
	for _, current := range devices {
		if _, err := transaction.ExecContext(ctx,
			"INSERT INTO target_virtual_devices(target_id, id, name, type, source_device_id, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			item.ID, current.ID, current.Name, current.Type, current.SourceDeviceID, current.Enabled, now, now,
		); err != nil {
			return fmt.Errorf("save target virtual device: %w", err)
		}
	}
	// Consumer routes belong to one concrete Target-owned Consumer device. Drop
	// routes whose Consumer device was removed or whose unified source changed;
	// otherwise they would remain hidden, but still persisted as orphaned state.
	if _, err := transaction.ExecContext(ctx, `
		DELETE FROM mapping_bindings
		WHERE stage = 'consumer' AND target_id = ?
		  AND NOT EXISTS (
			SELECT 1 FROM target_virtual_devices AS virtual
			WHERE virtual.target_id = mapping_bindings.target_id
			  AND virtual.id = mapping_bindings.consumer_device_id
			  AND virtual.source_device_id = mapping_bindings.device_id
		  )`, item.ID); err != nil {
		return fmt.Errorf("clear stale target consumer mappings: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit target save: %w", err)
	}
	return nil
}

func (s *Store) DeleteTarget(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin target delete: %w", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, "DELETE FROM mapping_bindings WHERE stage = 'consumer' AND target_id = ?", id); err != nil {
		return fmt.Errorf("delete target consumer mappings: %w", err)
	}
	result, err := transaction.ExecContext(ctx, "DELETE FROM targets WHERE id = ?", id)
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
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit target delete: %w", err)
	}
	return nil
}

func (s *Store) ListTargets(ctx context.Context) ([]target.Config, error) {
	defer s.observe(time.Now())
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
		item.Pin, err = s.secrets.decrypt("target-pin:"+item.ID, item.Pin)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("decrypt target %q pin: %w", item.ID, err)
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
		devices, err := s.targetVirtualDevices(ctx, result[index].ID)
		if err != nil {
			return nil, err
		}
		result[index].Devices = devices
		for _, current := range devices {
			if current.Enabled {
				result[index].DeviceIDs = append(result[index].DeviceIDs, current.SourceDeviceID)
			}
		}
	}
	return result, nil
}

func (s *Store) targetVirtualDevices(ctx context.Context, targetID string) ([]target.VirtualDevice, error) {
	rows, err := s.database.QueryContext(
		ctx,
		"SELECT id, name, type, source_device_id, enabled FROM target_virtual_devices WHERE target_id = ? ORDER BY created_at, id",
		targetID,
	)
	if err != nil {
		return nil, fmt.Errorf("list target bindings: %w", err)
	}
	defer rows.Close()
	result := make([]target.VirtualDevice, 0)
	for rows.Next() {
		var item target.VirtualDevice
		if err := rows.Scan(&item.ID, &item.Name, &item.Type, &item.SourceDeviceID, &item.Enabled); err != nil {
			return nil, fmt.Errorf("scan target virtual device: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
