package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

func (s *Store) ListProviders(ctx context.Context) ([]providerconfig.Config, error) {
	defer s.observe(time.Now())
	rows, err := s.database.QueryContext(ctx, `
        SELECT id, type, name, enabled, config_json
        FROM providers ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()
	result := make([]providerconfig.Config, 0)
	for rows.Next() {
		var item providerconfig.Config
		var raw string
		if err := rows.Scan(&item.ID, &item.Type, &item.Name, &item.Enabled, &raw); err != nil {
			return nil, fmt.Errorf("scan provider: %w", err)
		}
		item.Config = []byte(raw)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate providers: %w", err)
	}
	return result, nil
}

func (s *Store) SaveProvider(ctx context.Context, item providerconfig.Config) error {
	defer s.observe(time.Now())
	configJSON := item.Config
	if len(configJSON) == 0 {
		configJSON = []byte("{}")
	}
	now := time.Now().UTC().UnixMilli()
	_, err := s.database.ExecContext(ctx, `
        INSERT INTO providers(id, type, name, enabled, config_json, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
          type = excluded.type, name = excluded.name, enabled = excluded.enabled,
          config_json = excluded.config_json, updated_at = excluded.updated_at`,
		item.ID, item.Type, item.Name, item.Enabled, string(configJSON), now, now,
	)
	if err != nil {
		return fmt.Errorf("save provider: %w", err)
	}
	return nil
}

func (s *Store) DeleteProvider(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	result, err := s.database.ExecContext(ctx, "DELETE FROM providers WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete provider: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted provider count: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("provider %q not found", id)
	}
	return nil
}
