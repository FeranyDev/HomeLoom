package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func (s *Store) ListMappingProfiles(ctx context.Context) ([]mapping.Profile, error) {
	defer s.observe(time.Now())
	rows, err := s.database.QueryContext(ctx, "SELECT document_json FROM mapping_profiles ORDER BY kind, id")
	if err != nil {
		return nil, fmt.Errorf("list mapping profiles: %w", err)
	}
	defer rows.Close()
	result := make([]mapping.Profile, 0)
	for rows.Next() {
		var document string
		if err := rows.Scan(&document); err != nil {
			return nil, fmt.Errorf("scan mapping profile: %w", err)
		}
		var item mapping.Profile
		if err := json.Unmarshal([]byte(document), &item); err != nil {
			return nil, fmt.Errorf("decode mapping profile: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mapping profiles: %w", err)
	}
	return result, nil
}

func (s *Store) SaveMappingProfile(ctx context.Context, item mapping.Profile) error {
	return s.SaveMappingProfiles(ctx, []mapping.Profile{item})
}

func (s *Store) SaveMappingProfiles(ctx context.Context, items []mapping.Profile) error {
	defer s.observe(time.Now())
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mapping profile save: %w", err)
	}
	defer transaction.Rollback()
	now := time.Now().UTC().UnixMilli()
	for _, item := range items {
		document, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("encode mapping profile %q: %w", item.ID, err)
		}
		_, err = transaction.ExecContext(ctx, "INSERT INTO mapping_profiles(id, kind, version, document_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, version = excluded.version, document_json = excluded.document_json, updated_at = excluded.updated_at", item.ID, item.Kind, item.Version, string(document), now, now)
		if err != nil {
			return fmt.Errorf("save mapping profile %q: %w", item.ID, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit mapping profile save: %w", err)
	}
	return nil
}

func (s *Store) DeleteMappingProfile(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	result, err := s.database.ExecContext(ctx, "DELETE FROM mapping_profiles WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete mapping profile: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted mapping profile count: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("mapping profile %q not found", id)
	}
	return nil
}
