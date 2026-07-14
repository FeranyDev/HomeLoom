package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) LoadMIoTSpec(ctx context.Context, specType, model string) ([]byte, string, time.Time, bool, error) {
	defer s.observe(time.Now())
	query, value := "SELECT document_json, spec_type, fetched_at FROM miot_spec_cache WHERE spec_type = ?", specType
	if specType == "" {
		query, value = "SELECT document_json, spec_type, fetched_at FROM miot_spec_cache WHERE model = ? ORDER BY fetched_at DESC LIMIT 1", model
	}
	var document []byte
	var resolvedType string
	var fetchedAt int64
	err := s.database.QueryRowContext(ctx, query, value).Scan(&document, &resolvedType, &fetchedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", time.Time{}, false, nil
		}
		return nil, "", time.Time{}, false, fmt.Errorf("load MIoT spec cache: %w", err)
	}
	return append([]byte(nil), document...), resolvedType, time.UnixMilli(fetchedAt).UTC(), true, nil
}

func (s *Store) SaveMIoTSpec(ctx context.Context, specType, model string, document []byte, fetchedAt time.Time) error {
	defer s.observe(time.Now())
	_, err := s.database.ExecContext(ctx, `INSERT INTO miot_spec_cache(spec_type, model, document_json, fetched_at) VALUES (?, ?, ?, ?) ON CONFLICT(spec_type) DO UPDATE SET model = excluded.model, document_json = excluded.document_json, fetched_at = excluded.fetched_at`, specType, model, document, fetchedAt.UTC().UnixMilli())
	if err != nil {
		return fmt.Errorf("save MIoT spec cache: %w", err)
	}
	return nil
}
