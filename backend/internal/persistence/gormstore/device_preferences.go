package gormstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"gorm.io/gorm"
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

func (s *Store) ListDeviceLocationPreferences(ctx context.Context) ([]device.LocationPreference, error) {
	defer s.observe(time.Now())
	var rows []deviceLocationPreferenceRow
	if err := s.orm.WithContext(ctx).Order("device_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list device location preferences: %w", err)
	}
	result := make([]device.LocationPreference, 0, len(rows))
	for _, row := range rows {
		result = append(result, device.LocationPreference{
			DeviceID: row.DeviceID, HomeID: row.HomeID, HomeName: row.HomeName,
			RoomID: row.RoomID, RoomName: row.RoomName,
		})
	}
	return result, nil
}

func (s *Store) SetDeviceLocationPreference(ctx context.Context, preference device.LocationPreference) error {
	defer s.observe(time.Now())
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureLocationPreferenceCatalog(tx, preference); err != nil {
			return err
		}
		now := time.Now().UTC().UnixMilli()
		row := deviceLocationPreferenceRow{
			DeviceID: preference.DeviceID, HomeID: preference.HomeID, HomeName: preference.HomeName,
			RoomID: preference.RoomID, RoomName: preference.RoomName, CreatedAt: now, UpdatedAt: now,
		}
		updates := []string{"home_id", "home_name", "room_id", "room_name", "updated_at"}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "device_id"}}, DoUpdates: clause.AssignmentColumns(updates),
		}).Create(&row).Error
	})
	if err != nil {
		return fmt.Errorf("set device location preference: %w", err)
	}
	return nil
}

func (s *Store) ClearDeviceLocationPreference(ctx context.Context, deviceID string) error {
	defer s.observe(time.Now())
	if err := s.orm.WithContext(ctx).Where("device_id = ?", deviceID).Delete(&deviceLocationPreferenceRow{}).Error; err != nil {
		return fmt.Errorf("clear device location preference: %w", err)
	}
	return nil
}

func (s *Store) ListDeviceLocationHomes(ctx context.Context) ([]device.LocationHome, error) {
	defer s.observe(time.Now())
	var homes []deviceLocationHomeRow
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := importLegacyLocationPreferences(tx); err != nil {
			return err
		}
		return tx.Order("name, id").Preload("Rooms", func(db *gorm.DB) *gorm.DB {
			return db.Order("name, id")
		}).Find(&homes).Error
	})
	if err != nil {
		return nil, fmt.Errorf("list device location homes: %w", err)
	}
	result := make([]device.LocationHome, 0, len(homes))
	for _, row := range homes {
		home := device.LocationHome{ID: row.ID, Name: row.Name, Rooms: make([]device.LocationRoom, 0, len(row.Rooms))}
		for _, room := range row.Rooms {
			home.Rooms = append(home.Rooms, device.LocationRoom{ID: room.ID, HomeID: room.HomeID, Name: room.Name})
		}
		result = append(result, home)
	}
	return result, nil
}

func (s *Store) SaveDeviceLocationHome(ctx context.Context, home device.LocationHome) error {
	defer s.observe(time.Now())
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var duplicate int64
		if err := tx.Model(&deviceLocationHomeRow{}).Where("name = ? AND id <> ?", home.Name, home.ID).Count(&duplicate).Error; err != nil {
			return err
		}
		if duplicate > 0 {
			return device.ErrLocationConflict
		}
		var existing deviceLocationHomeRow
		err := tx.Where("id = ?", home.ID).First(&existing).Error
		now := time.Now().UTC().UnixMilli()
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			return tx.Create(&deviceLocationHomeRow{ID: home.ID, Name: home.Name, CreatedAt: now, UpdatedAt: now}).Error
		case err != nil:
			return err
		}
		if err := tx.Model(&deviceLocationHomeRow{}).Where("id = ?", home.ID).Updates(map[string]any{"name": home.Name, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&deviceLocationPreferenceRow{}).Where("home_id = ?", home.ID).Updates(map[string]any{"home_name": home.Name, "updated_at": now}).Error
	})
	if err != nil {
		return fmt.Errorf("save device location home: %w", err)
	}
	return nil
}

func (s *Store) DeleteDeviceLocationHome(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&deviceLocationHomeRow{}).Where("id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return device.ErrLocationNotFound
		}
		if err := tx.Model(&deviceLocationPreferenceRow{}).Where("home_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return device.ErrLocationInUse
		}
		if err := tx.Where("home_id = ?", id).Delete(&deviceLocationRoomRow{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&deviceLocationHomeRow{}).Error
	})
	if err != nil {
		return fmt.Errorf("delete device location home: %w", err)
	}
	return nil
}

func (s *Store) SaveDeviceLocationRoom(ctx context.Context, room device.LocationRoom) error {
	defer s.observe(time.Now())
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&deviceLocationHomeRow{}).Where("id = ?", room.HomeID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return device.ErrLocationNotFound
		}
		if err := tx.Model(&deviceLocationRoomRow{}).Where("home_id = ? AND name = ? AND id <> ?", room.HomeID, room.Name, room.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return device.ErrLocationConflict
		}
		var existing deviceLocationRoomRow
		err := tx.Where("id = ?", room.ID).First(&existing).Error
		now := time.Now().UTC().UnixMilli()
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			return tx.Create(&deviceLocationRoomRow{ID: room.ID, HomeID: room.HomeID, Name: room.Name, CreatedAt: now, UpdatedAt: now}).Error
		case err != nil:
			return err
		case existing.HomeID != room.HomeID:
			return device.ErrLocationConflict
		}
		if err := tx.Model(&deviceLocationRoomRow{}).Where("id = ?", room.ID).Updates(map[string]any{"name": room.Name, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&deviceLocationPreferenceRow{}).Where("room_id = ?", room.ID).Updates(map[string]any{"room_name": room.Name, "updated_at": now}).Error
	})
	if err != nil {
		return fmt.Errorf("save device location room: %w", err)
	}
	return nil
}

func (s *Store) DeleteDeviceLocationRoom(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&deviceLocationRoomRow{}).Where("id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return device.ErrLocationNotFound
		}
		if err := tx.Model(&deviceLocationPreferenceRow{}).Where("room_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return device.ErrLocationInUse
		}
		return tx.Where("id = ?", id).Delete(&deviceLocationRoomRow{}).Error
	})
	if err != nil {
		return fmt.Errorf("delete device location room: %w", err)
	}
	return nil
}

func importLegacyLocationPreferences(tx *gorm.DB) error {
	var preferences []deviceLocationPreferenceRow
	if err := tx.Find(&preferences).Error; err != nil {
		return err
	}
	for _, row := range preferences {
		if err := ensureLocationPreferenceCatalog(tx, device.LocationPreference{
			DeviceID: row.DeviceID, HomeID: row.HomeID, HomeName: row.HomeName,
			RoomID: row.RoomID, RoomName: row.RoomName,
		}); err != nil {
			return err
		}
	}
	return nil
}

func ensureLocationPreferenceCatalog(tx *gorm.DB, preference device.LocationPreference) error {
	now := time.Now().UTC().UnixMilli()
	home := deviceLocationHomeRow{ID: preference.HomeID, Name: preference.HomeName, CreatedAt: now, UpdatedAt: now}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&home).Error; err != nil {
		return err
	}
	if preference.RoomID == "" {
		return nil
	}
	room := deviceLocationRoomRow{ID: preference.RoomID, HomeID: preference.HomeID, Name: preference.RoomName, CreatedAt: now, UpdatedAt: now}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&room).Error
}
