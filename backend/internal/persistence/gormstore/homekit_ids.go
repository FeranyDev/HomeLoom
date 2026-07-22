package gormstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

func (s *Store) HomeKitAccessoryAID(ctx context.Context, targetID, deviceID string) (uint64, error) {
	defer s.observe(time.Now())
	var aid uint64
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row homeKitAccessoryIDRow
		err := tx.Select("aid").Where("target_id = ? AND device_id = ?", targetID, deviceID).Take(&row).Error
		if err == nil {
			aid = row.AID
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read accessory id: %w", err)
		}
		if err := tx.Model(&homeKitAccessoryIDRow{}).Select("COALESCE(MAX(aid), 1) + 1").Where("target_id = ?", targetID).Scan(&aid).Error; err != nil {
			return fmt.Errorf("allocate accessory id: %w", err)
		}
		if err := tx.Create(&homeKitAccessoryIDRow{TargetID: targetID, DeviceID: deviceID, AID: aid, CreatedAt: time.Now().UTC().UnixMilli()}).Error; err != nil {
			return fmt.Errorf("save accessory id: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return aid, nil
}

func (s *Store) HomeKitIID(ctx context.Context, targetID, deviceID, resourceKey string) (uint64, error) {
	defer s.observe(time.Now())
	var iid uint64
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row homeKitIIDRow
		err := tx.Select("iid").Where("target_id = ? AND device_id = ? AND resource_key = ?", targetID, deviceID, resourceKey).Take(&row).Error
		if err == nil {
			iid = row.IID
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read IID: %w", err)
		}
		if err := tx.Model(&homeKitIIDRow{}).Select("COALESCE(MAX(iid), 0) + 1").Where("target_id = ? AND device_id = ?", targetID, deviceID).Scan(&iid).Error; err != nil {
			return fmt.Errorf("allocate IID: %w", err)
		}
		if err := tx.Create(&homeKitIIDRow{TargetID: targetID, DeviceID: deviceID, ResourceKey: resourceKey, IID: iid, CreatedAt: time.Now().UTC().UnixMilli()}).Error; err != nil {
			return fmt.Errorf("save IID: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return iid, nil
}
