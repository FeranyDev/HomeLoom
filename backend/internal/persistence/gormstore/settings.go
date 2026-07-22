package gormstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	defer s.observe(time.Now())
	var row systemSettingRow
	if err := s.orm.WithContext(ctx).Select("value").Where("key = ?", key).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get system setting %q: %w", key, err)
	}
	return row.Value, true, nil
}

func (s *Store) SaveSetting(ctx context.Context, key, value string) error {
	return s.SaveSettings(ctx, map[string]string{key: value})
}

func (s *Store) SaveSettings(ctx context.Context, values map[string]string) error {
	defer s.observe(time.Now())
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for key, value := range values {
			row := systemSettingRow{Key: key, Value: value, UpdatedAt: time.Now().UTC().UnixMilli()}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"})}).Create(&row).Error; err != nil {
				return fmt.Errorf("save system setting %q: %w", key, err)
			}
		}
		return nil
	})
}
