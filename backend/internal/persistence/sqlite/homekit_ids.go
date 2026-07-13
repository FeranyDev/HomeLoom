package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (s *Store) HomeKitAccessoryAID(ctx context.Context, targetID, deviceID string) (uint64, error) {
	defer s.observe(time.Now())
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin accessory id transaction: %w", err)
	}
	defer tx.Rollback()
	var aid uint64
	err = tx.QueryRowContext(ctx, "SELECT aid FROM homekit_accessory_ids WHERE target_id = ? AND device_id = ?", targetID, deviceID).Scan(&aid)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return aid, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("read accessory id: %w", err)
	}
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(aid), 1) + 1 FROM homekit_accessory_ids WHERE target_id = ?", targetID).Scan(&aid); err != nil {
		return 0, fmt.Errorf("allocate accessory id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO homekit_accessory_ids(target_id, device_id, aid, created_at) VALUES (?, ?, ?, ?)", targetID, deviceID, aid, time.Now().UTC().UnixMilli()); err != nil {
		return 0, fmt.Errorf("save accessory id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit accessory id: %w", err)
	}
	return aid, nil
}

func (s *Store) HomeKitIID(ctx context.Context, targetID, deviceID, resourceKey string) (uint64, error) {
	defer s.observe(time.Now())
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin IID transaction: %w", err)
	}
	defer tx.Rollback()
	var iid uint64
	err = tx.QueryRowContext(ctx, "SELECT iid FROM homekit_iids WHERE target_id = ? AND device_id = ? AND resource_key = ?", targetID, deviceID, resourceKey).Scan(&iid)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return iid, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("read IID: %w", err)
	}
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(iid), 0) + 1 FROM homekit_iids WHERE target_id = ? AND device_id = ?", targetID, deviceID).Scan(&iid); err != nil {
		return 0, fmt.Errorf("allocate IID: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO homekit_iids(target_id, device_id, resource_key, iid, created_at) VALUES (?, ?, ?, ?, ?)", targetID, deviceID, resourceKey, iid, time.Now().UTC().UnixMilli()); err != nil {
		return 0, fmt.Errorf("save IID: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit IID: %w", err)
	}
	return iid, nil
}
