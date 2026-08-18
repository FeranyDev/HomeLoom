package gormstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/logicaldevice"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) ListLogicalDevices(ctx context.Context) ([]logicaldevice.Config, error) {
	defer s.observe(time.Now())
	var rows []logicalDeviceRow
	if err := s.orm.WithContext(ctx).Order("id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list logical devices: %w", err)
	}
	result := make([]logicaldevice.Config, 0, len(rows))
	for _, row := range rows {
		var item logicaldevice.Config
		if err := json.Unmarshal([]byte(row.DocumentJSON), &item); err != nil {
			return nil, fmt.Errorf("decode logical device %q: %w", row.ID, err)
		}
		if item.ID != row.ID {
			return nil, fmt.Errorf("logical device %q document id conflicts with row id", row.ID)
		}
		if err := item.Validate(); err != nil {
			return nil, fmt.Errorf("invalid logical device %q: %w", row.ID, err)
		}
		result = append(result, item)
	}
	// AutoMigrate does not manufacture rows for pre-existing document records.
	// Backfill active configurations here so an existing deployment gains the
	// stable-ID reservation on its first normal startup without changing the
	// public Logical Device configuration format.
	if len(result) != 0 {
		now := time.Now().UTC().UnixMilli()
		if err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			for _, item := range result {
				if err := ensureLogicalDeviceIdentity(tx, item.ID, now); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return nil, fmt.Errorf("backfill logical device identities: %w", err)
		}
	}
	return result, nil
}

func (s *Store) SaveLogicalDevice(ctx context.Context, item logicaldevice.Config) error {
	defer s.observe(time.Now())
	if err := item.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode logical device %q: %w", item.ID, err)
	}
	now := time.Now().UTC().UnixMilli()
	row := logicalDeviceRow{ID: item.ID, DocumentJSON: jsonDocument(payload), CreatedAt: now, UpdatedAt: now}
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"document_json", "updated_at"}),
		}).Create(&row).Error; err != nil {
			return fmt.Errorf("save logical device %q: %w", item.ID, err)
		}
		return ensureLogicalDeviceIdentity(tx, item.ID, now)
	})
}

func (s *Store) DeleteLogicalDevice(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ?", id).Delete(&logicalDeviceRow{})
		if result.Error != nil {
			return fmt.Errorf("delete logical device %q: %w", id, result.Error)
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return markLogicalDeviceIdentityDeleted(tx, id, time.Now().UTC().UnixMilli(), StableIdentityRetention)
	})
}
