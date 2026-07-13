package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func (s *Store) ListMappingBindings(ctx context.Context) ([]mapping.Binding, error) {
	defer s.observe(time.Now())
	rows, err := s.database.QueryContext(ctx, `SELECT id, profile_id, provider_id, device_id, endpoint_id, capability_id, property_id, enabled FROM mapping_bindings ORDER BY provider_id, device_id, endpoint_id, capability_id, property_id`)
	if err != nil {
		return nil, fmt.Errorf("list mapping bindings: %w", err)
	}
	defer rows.Close()
	result := make([]mapping.Binding, 0)
	for rows.Next() {
		var item mapping.Binding
		if err := rows.Scan(&item.ID, &item.ProfileID, &item.ProviderID, &item.DeviceID, &item.EndpointID, &item.CapabilityID, &item.PropertyID, &item.Enabled); err != nil {
			return nil, fmt.Errorf("scan mapping binding: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mapping bindings: %w", err)
	}
	return result, nil
}

func (s *Store) SaveMappingBinding(ctx context.Context, item mapping.Binding) error {
	defer s.observe(time.Now())
	now := time.Now().UTC().UnixMilli()
	_, err := s.database.ExecContext(ctx, `INSERT INTO mapping_bindings(id, profile_id, provider_id, device_id, endpoint_id, capability_id, property_id, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET profile_id = excluded.profile_id, provider_id = excluded.provider_id, device_id = excluded.device_id, endpoint_id = excluded.endpoint_id, capability_id = excluded.capability_id, property_id = excluded.property_id, enabled = excluded.enabled, updated_at = excluded.updated_at`, item.ID, item.ProfileID, item.ProviderID, item.DeviceID, item.EndpointID, item.CapabilityID, item.PropertyID, item.Enabled, now, now)
	if err != nil {
		return fmt.Errorf("save mapping binding %q: %w", item.ID, err)
	}
	return nil
}

func (s *Store) DeleteMappingBinding(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	result, err := s.database.ExecContext(ctx, "DELETE FROM mapping_bindings WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete mapping binding: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted mapping binding count: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("mapping binding %q not found", id)
	}
	return nil
}
