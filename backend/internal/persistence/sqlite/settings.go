package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	defer s.observe(time.Now())
	var value string
	if err := s.database.QueryRowContext(ctx, "SELECT value FROM system_settings WHERE key = ?", key).Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get system setting %q: %w", key, err)
	}
	return value, true, nil
}

func (s *Store) SaveSetting(ctx context.Context, key, value string) error {
	return s.SaveSettings(ctx, map[string]string{key: value})
}

func (s *Store) SaveSettings(ctx context.Context, values map[string]string) error {
	defer s.observe(time.Now())
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin system settings update: %w", err)
	}
	for key, value := range values {
		if _, err := transaction.ExecContext(ctx, `INSERT INTO system_settings(key, value, updated_at) VALUES (?, ?, ?)
            ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, value, time.Now().UTC().UnixMilli()); err != nil {
			transaction.Rollback()
			return fmt.Errorf("save system setting %q: %w", key, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit system settings update: %w", err)
	}
	return nil
}
