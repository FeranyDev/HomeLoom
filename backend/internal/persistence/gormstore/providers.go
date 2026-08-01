package gormstore

import (
	"context"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) ListProviders(ctx context.Context) ([]providerconfig.Config, error) {
	defer s.observe(time.Now())
	var rows []providerRow
	if err := s.orm.WithContext(ctx).Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	result := make([]providerconfig.Config, 0, len(rows))
	for _, row := range rows {
		decoded, _, err := s.transformProviderConfigSecrets(row.ID, []byte(row.ConfigJSON), false)
		if err != nil {
			return nil, err
		}
		result = append(result, providerconfig.Config{ID: row.ID, Type: row.Type, Name: row.Name, Enabled: row.Enabled, Config: decoded})
	}
	return result, nil
}

func (s *Store) SaveProvider(ctx context.Context, item providerconfig.Config) error {
	defer s.observe(time.Now())
	return s.saveProvider(s.orm.WithContext(ctx), item)
}

// SaveProvidersAtomically persists a related set of provider configurations in
// one transaction. It is used by ownership migrations where publishing only
// one side would temporarily give two providers ownership of the same device.
func (s *Store) SaveProvidersAtomically(ctx context.Context, items ...providerconfig.Config) error {
	defer s.observe(time.Now())
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range items {
			if err := s.saveProvider(tx, item); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) saveProvider(db *gorm.DB, item providerconfig.Config) error {
	configJSON := item.Config
	if len(configJSON) == 0 {
		configJSON = []byte("{}")
	}
	encrypted, _, err := s.transformProviderConfigSecrets(item.ID, configJSON, true)
	if err != nil {
		return err
	}
	configJSON = encrypted
	now := time.Now().UTC().UnixMilli()
	row := providerRow{ID: item.ID, Type: item.Type, Name: item.Name, Enabled: item.Enabled, ConfigJSON: jsonDocument(configJSON), CreatedAt: now, UpdatedAt: now}
	err = db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"type", "name", "enabled", "config_json", "updated_at"}),
	}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("save provider: %w", err)
	}
	return nil
}

func (s *Store) DeleteProvider(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	result := s.orm.WithContext(ctx).Where("id = ?", id).Delete(&providerRow{})
	if result.Error != nil {
		return fmt.Errorf("delete provider: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("provider %q not found", id)
	}
	return nil
}
