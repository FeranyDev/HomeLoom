package gormstore

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm/clause"
)

func (s *Store) ListDisabledDeviceIDs(ctx context.Context) ([]string, error) {
	defer s.observe(time.Now())
	var result []string
	if err := s.orm.WithContext(ctx).Model(&devicePreferenceRow{}).Where("disabled = ?", true).Order("device_id").Pluck("device_id", &result).Error; err != nil {
		return nil, fmt.Errorf("list disabled devices: %w", err)
	}
	return result, nil
}

func (s *Store) SetDeviceDisabled(ctx context.Context, deviceID string, disabled bool) error {
	defer s.observe(time.Now())
	if disabled {
		row := devicePreferenceRow{DeviceID: deviceID, Disabled: true, UpdatedAt: time.Now().UTC().UnixMilli()}
		err := s.orm.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "device_id"}}, DoUpdates: clause.AssignmentColumns([]string{"disabled", "updated_at"})}).Create(&row).Error
		if err != nil {
			return fmt.Errorf("disable device: %w", err)
		}
		return nil
	}
	if err := s.orm.WithContext(ctx).Where("device_id = ?", deviceID).Delete(&devicePreferenceRow{}).Error; err != nil {
		return fmt.Errorf("enable device: %w", err)
	}
	return nil
}
