package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) SaveTarget(ctx context.Context, item target.Config) error {
	defer s.observe(time.Now())
	pin, err := s.secrets.encrypt("target-pin:"+item.ID, item.Pin)
	if err != nil {
		return fmt.Errorf("encrypt target pin: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := targetRow{ID: item.ID, Type: item.Type, Name: item.Name, Enabled: item.Enabled, Address: item.Address, PIN: pin, SetupID: item.SetupID, StorePath: item.StorePath, CreatedAt: now, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"type", "name", "enabled", "address", "pin", "setup_id", "store_path", "updated_at"})}).Create(&row).Error; err != nil {
			return fmt.Errorf("save target: %w", err)
		}
		if err := tx.Where("target_id = ?", item.ID).Delete(&targetVirtualDeviceRow{}).Error; err != nil {
			return fmt.Errorf("clear target virtual devices: %w", err)
		}
		devices := item.Devices
		if len(devices) == 0 {
			for _, deviceID := range item.DeviceIDs {
				devices = append(devices, target.VirtualDevice{ID: deviceID, Name: deviceID, SourceDeviceID: deviceID, Enabled: true})
			}
		}
		rows := make([]targetVirtualDeviceRow, 0, len(devices))
		for _, current := range devices {
			rows = append(rows, targetVirtualDeviceRow{TargetID: item.ID, ID: current.ID, Name: current.Name, Type: string(current.Type), SourceDeviceID: current.SourceDeviceID, Enabled: current.Enabled, CreatedAt: now, UpdatedAt: now})
		}
		if len(rows) > 0 {
			if err := tx.Create(&rows).Error; err != nil {
				return fmt.Errorf("save target virtual devices: %w", err)
			}
		}
		// This relational predicate is intentionally kept in SQL while GORM owns
		// the transaction and deletion, because it expresses orphan detection.
		if err := tx.Where("stage = 'consumer' AND target_id = ?", item.ID).
			Where(`NOT EXISTS (SELECT 1 FROM target_virtual_devices AS virtual
				WHERE virtual.target_id = mapping_bindings.target_id
				AND virtual.id = mapping_bindings.consumer_device_id
				AND virtual.source_device_id = mapping_bindings.device_id)`).
			Delete(&mappingBindingRow{}).Error; err != nil {
			return fmt.Errorf("clear stale target consumer mappings: %w", err)
		}
		return nil
	})
}

func (s *Store) DeleteTarget(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("stage = ? AND target_id = ?", "consumer", id).Delete(&mappingBindingRow{}).Error; err != nil {
			return fmt.Errorf("delete target consumer mappings: %w", err)
		}
		result := tx.Where("id = ?", id).Delete(&targetRow{})
		if result.Error != nil {
			return fmt.Errorf("delete target: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("target %q not found", id)
		}
		return nil
	})
}

func (s *Store) ListTargets(ctx context.Context) ([]target.Config, error) {
	defer s.observe(time.Now())
	var rows []targetRow
	if err := s.orm.WithContext(ctx).Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list targets: %w", err)
	}
	result := make([]target.Config, 0, len(rows))
	for _, row := range rows {
		item := target.Config{ID: row.ID, Type: row.Type, Name: row.Name, Enabled: row.Enabled, Address: row.Address, Pin: row.PIN, SetupID: row.SetupID, StorePath: row.StorePath}
		var err error
		item.Pin, err = s.secrets.decrypt("target-pin:"+item.ID, item.Pin)
		if err != nil {
			return nil, fmt.Errorf("decrypt target %q pin: %w", item.ID, err)
		}
		result = append(result, item)
	}
	for index := range result {
		devices, err := s.targetVirtualDevices(ctx, result[index].ID)
		if err != nil {
			return nil, err
		}
		result[index].Devices = devices
		for _, current := range devices {
			if current.Enabled {
				result[index].DeviceIDs = append(result[index].DeviceIDs, current.SourceDeviceID)
			}
		}
	}
	return result, nil
}

func (s *Store) targetVirtualDevices(ctx context.Context, targetID string) ([]target.VirtualDevice, error) {
	var rows []targetVirtualDeviceRow
	if err := s.orm.WithContext(ctx).Where("target_id = ?", targetID).Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list target bindings: %w", err)
	}
	result := make([]target.VirtualDevice, 0, len(rows))
	for _, row := range rows {
		result = append(result, target.VirtualDevice{ID: row.ID, Name: row.Name, Type: device.Type(row.Type), SourceDeviceID: row.SourceDeviceID, Enabled: row.Enabled})
	}
	return result, nil
}
