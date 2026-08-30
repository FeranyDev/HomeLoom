package gormstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/mapping"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) ListMappingProfiles(ctx context.Context) ([]mapping.Profile, error) {
	defer s.observe(time.Now())
	var rows []mappingProfileRow
	if err := s.orm.WithContext(ctx).Select("document_json").Order("kind, id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list mapping profiles: %w", err)
	}
	result := make([]mapping.Profile, 0, len(rows))
	for _, row := range rows {
		var item mapping.Profile
		if err := json.Unmarshal([]byte(row.DocumentJSON), &item); err != nil {
			return nil, fmt.Errorf("decode mapping profile: %w", err)
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Store) SaveMappingProfile(ctx context.Context, item mapping.Profile) error {
	return s.SaveMappingProfiles(ctx, []mapping.Profile{item})
}

func (s *Store) SaveMappingProfiles(ctx context.Context, items []mapping.Profile) error {
	defer s.observe(time.Now())
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC().UnixMilli()
		for _, item := range items {
			document, err := json.Marshal(item)
			if err != nil {
				return fmt.Errorf("encode mapping profile %q: %w", item.ID, err)
			}
			row := mappingProfileRow{ID: item.ID, Identifier: item.Identifier, Kind: string(item.Kind), Version: item.Version, DocumentJSON: jsonDocument(document), CreatedAt: now, UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"identifier", "kind", "version", "document_json", "updated_at"})}).Create(&row).Error; err != nil {
				return fmt.Errorf("save mapping profile %q: %w", item.ID, err)
			}
		}
		return nil
	})
}

// MigrateMappingProfileIdentities atomically replaces legacy identifier-backed
// Profile IDs and every Binding reference. It is intentionally separate from
// normal upsert behavior because changing a primary key must never briefly
// leave a Binding pointing at a non-existent Profile.
func (s *Store) MigrateMappingProfileIdentities(ctx context.Context, migrations []mapping.ProfileIdentityMigration, bindingProfileIDs map[string]string) error {
	if len(migrations) == 0 && len(bindingProfileIDs) == 0 {
		return nil
	}
	defer s.observe(time.Now())
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC().UnixMilli()
		for _, migration := range migrations {
			item := migration.Profile
			document, err := json.Marshal(item)
			if err != nil {
				return fmt.Errorf("encode migrated mapping profile %q: %w", migration.PreviousID, err)
			}
			values := map[string]any{
				"id":            item.ID,
				"identifier":    item.Identifier,
				"kind":          string(item.Kind),
				"version":       item.Version,
				"document_json": jsonDocument(document),
				"updated_at":    now,
			}
			result := tx.Model(&mappingProfileRow{}).Where("id = ?", migration.PreviousID).Updates(values)
			if result.Error != nil {
				return fmt.Errorf("migrate mapping profile %q: %w", migration.PreviousID, result.Error)
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("mapping profile %q not found during identity migration", migration.PreviousID)
			}
		}
		for previousID, id := range bindingProfileIDs {
			if previousID == id {
				continue
			}
			if err := tx.Model(&mappingBindingRow{}).Where("profile_id = ?", previousID).Update("profile_id", id).Error; err != nil {
				return fmt.Errorf("migrate mapping bindings from profile %q: %w", previousID, err)
			}
		}
		return nil
	})
}

func (s *Store) DeleteMappingProfile(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	result := s.orm.WithContext(ctx).Where("id = ?", id).Delete(&mappingProfileRow{})
	if result.Error != nil {
		return fmt.Errorf("delete mapping profile: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("mapping profile %q not found", id)
	}
	return nil
}
