package gormstore

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxMatterEndpointID = uint64(65534)

func matterRuntimeSecretScope(targetID, key string) string {
	return "matter-runtime-kv:" + targetID + ":" + key
}

func validateMatterRuntimeKey(targetID, key string) error {
	if strings.TrimSpace(targetID) == "" {
		return errors.New("Matter target ID is required")
	}
	if key == "" || len(key) > 1024 || strings.ContainsRune(key, '\x00') {
		return errors.New("Matter runtime key must contain 1 to 1024 bytes and no NUL")
	}
	return nil
}

func (s *Store) requireMatterTarget(tx *gorm.DB, targetID string, lock bool) error {
	var row targetRow
	query := tx.Select("id", "type").Where("id = ?", targetID)
	if lock && s.databaseKind == databasePostgreSQL {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("Matter target %q not found", targetID)
		}
		return fmt.Errorf("read Matter target %q: %w", targetID, err)
	}
	if row.Type != "matter" {
		return fmt.Errorf("target %q is not a Matter target", targetID)
	}
	return nil
}

// PutMatterRuntimeValue stores a runtime value encrypted with associated data
// bound to both target and key. All values are encrypted because callers do not
// always know whether an SDK blob embeds credentials.
func (s *Store) PutMatterRuntimeValue(ctx context.Context, targetID, key string, value []byte) error {
	defer s.observe(time.Now())
	if err := validateMatterRuntimeKey(targetID, key); err != nil {
		return err
	}
	encoded := base64.RawStdEncoding.EncodeToString(value)
	encrypted, err := s.secrets.encrypt(matterRuntimeSecretScope(targetID, key), encoded)
	if err != nil {
		return fmt.Errorf("encrypt Matter runtime value: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.requireMatterTarget(tx, targetID, true); err != nil {
			return err
		}
		row := matterRuntimeKVRow{TargetID: targetID, Key: key, Value: encrypted, Sensitive: true, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "target_id"}, {Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "sensitive", "updated_at"}),
		}).Create(&row).Error; err != nil {
			return fmt.Errorf("save Matter runtime value: %w", err)
		}
		return nil
	})
}

func (s *Store) GetMatterRuntimeValue(ctx context.Context, targetID, key string) ([]byte, bool, error) {
	defer s.observe(time.Now())
	if err := validateMatterRuntimeKey(targetID, key); err != nil {
		return nil, false, err
	}
	if err := s.requireMatterTarget(s.orm.WithContext(ctx), targetID, false); err != nil {
		return nil, false, err
	}
	var row matterRuntimeKVRow
	err := s.orm.WithContext(ctx).Where("target_id = ? AND key = ?", targetID, key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read Matter runtime value: %w", err)
	}
	decrypted, err := s.secrets.decrypt(matterRuntimeSecretScope(targetID, key), row.Value)
	if err != nil {
		return nil, false, fmt.Errorf("decrypt Matter runtime value: %w", err)
	}
	value, err := base64.RawStdEncoding.DecodeString(decrypted)
	if err != nil {
		return nil, false, errors.New("Matter runtime value is malformed")
	}
	return value, true, nil
}

func (s *Store) ListMatterRuntimeValues(ctx context.Context, targetID string) ([]target.MatterRuntimeValue, error) {
	defer s.observe(time.Now())
	if strings.TrimSpace(targetID) == "" {
		return nil, errors.New("Matter target ID is required")
	}
	if err := s.requireMatterTarget(s.orm.WithContext(ctx), targetID, false); err != nil {
		return nil, err
	}
	var rows []matterRuntimeKVRow
	if err := s.orm.WithContext(ctx).Where("target_id = ?", targetID).Order("key").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list Matter runtime values: %w", err)
	}
	result := make([]target.MatterRuntimeValue, 0, len(rows))
	for _, row := range rows {
		decrypted, err := s.secrets.decrypt(matterRuntimeSecretScope(targetID, row.Key), row.Value)
		if err != nil {
			return nil, fmt.Errorf("decrypt Matter runtime key %q: %w", row.Key, err)
		}
		value, err := base64.RawStdEncoding.DecodeString(decrypted)
		if err != nil {
			return nil, fmt.Errorf("Matter runtime key %q is malformed", row.Key)
		}
		result = append(result, target.MatterRuntimeValue{
			TargetID: targetID, Key: row.Key, Value: value, Sensitive: row.Sensitive,
			UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
		})
	}
	return result, nil
}

func (s *Store) DeleteMatterRuntimeValue(ctx context.Context, targetID, key string) error {
	defer s.observe(time.Now())
	if err := validateMatterRuntimeKey(targetID, key); err != nil {
		return err
	}
	if err := s.requireMatterTarget(s.orm.WithContext(ctx), targetID, false); err != nil {
		return err
	}
	if err := s.orm.WithContext(ctx).Where("target_id = ? AND key = ?", targetID, key).Delete(&matterRuntimeKVRow{}).Error; err != nil {
		return fmt.Errorf("delete Matter runtime value: %w", err)
	}
	return nil
}

func (s *Store) ClearMatterRuntimeValues(ctx context.Context, targetID string) error {
	defer s.observe(time.Now())
	if strings.TrimSpace(targetID) == "" {
		return errors.New("Matter target ID is required")
	}
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.requireMatterTarget(tx, targetID, true); err != nil {
			return err
		}
		if err := tx.Where("target_id = ?", targetID).Delete(&matterRuntimeKVRow{}).Error; err != nil {
			return fmt.Errorf("clear Matter runtime values: %w", err)
		}
		return nil
	})
}

func (s *Store) AllocateMatterEndpoint(ctx context.Context, targetID, consumerDeviceID string, deviceType device.Type) (uint16, error) {
	defer s.observe(time.Now())
	if strings.TrimSpace(targetID) == "" || strings.TrimSpace(consumerDeviceID) == "" {
		return 0, errors.New("Matter target ID and consumer device ID are required")
	}
	if _, supported := device.ModelContractFor(deviceType); deviceType == "" || !supported {
		return 0, fmt.Errorf("Matter endpoint device type %q is unsupported", deviceType)
	}
	var endpointID uint16
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The target row is the per-target allocation mutex on PostgreSQL.
		// SQLite transactions are already IMMEDIATE through the configured DSN.
		if err := s.requireMatterTarget(tx, targetID, true); err != nil {
			return err
		}
		var existing matterEndpointIdentityRow
		err := tx.Where("target_id = ? AND consumer_device_id = ?", targetID, consumerDeviceID).Take(&existing).Error
		if err == nil {
			if existing.DeviceType != string(deviceType) {
				return target.ErrMatterDeviceTypeChange
			}
			endpointID = uint16(existing.EndpointID)
			if existing.Tombstone {
				if err := tx.Model(&matterEndpointIdentityRow{}).
					Where("target_id = ? AND consumer_device_id = ?", targetID, consumerDeviceID).
					Updates(map[string]any{"tombstone": false, "updated_at": time.Now().UTC().UnixMilli()}).Error; err != nil {
					return fmt.Errorf("restore Matter endpoint identity: %w", err)
				}
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read Matter endpoint identity: %w", err)
		}
		var maxID uint64
		if err := tx.Model(&matterEndpointIdentityRow{}).Select("COALESCE(MAX(endpoint_id), 1)").Where("target_id = ?", targetID).Scan(&maxID).Error; err != nil {
			return fmt.Errorf("inspect Matter endpoint IDs: %w", err)
		}
		if maxID >= maxMatterEndpointID {
			return target.ErrMatterEndpointIDsExhausted
		}
		endpointID = uint16(maxID + 1)
		now := time.Now().UTC().UnixMilli()
		row := matterEndpointIdentityRow{
			TargetID: targetID, ConsumerDeviceID: consumerDeviceID, EndpointID: uint32(endpointID),
			DeviceType: string(deviceType), CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("save Matter endpoint identity: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return endpointID, nil
}

func (s *Store) TombstoneMatterEndpoint(ctx context.Context, targetID, consumerDeviceID string) error {
	defer s.observe(time.Now())
	if strings.TrimSpace(targetID) == "" || strings.TrimSpace(consumerDeviceID) == "" {
		return errors.New("Matter target ID and consumer device ID are required")
	}
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.requireMatterTarget(tx, targetID, true); err != nil {
			return err
		}
		result := tx.Model(&matterEndpointIdentityRow{}).
			Where("target_id = ? AND consumer_device_id = ?", targetID, consumerDeviceID).
			Updates(map[string]any{"tombstone": true, "updated_at": time.Now().UTC().UnixMilli()})
		if result.Error != nil {
			return fmt.Errorf("tombstone Matter endpoint identity: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("Matter endpoint identity %q not found", consumerDeviceID)
		}
		return nil
	})
}

func (s *Store) ConfirmMatterEndpointDeviceType(ctx context.Context, targetID, consumerDeviceID string, deviceType device.Type, confirmed bool) error {
	defer s.observe(time.Now())
	if !confirmed {
		return target.ErrMatterDeviceTypeChange
	}
	if _, supported := device.ModelContractFor(deviceType); deviceType == "" || !supported {
		return fmt.Errorf("Matter endpoint device type %q is unsupported", deviceType)
	}
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.requireMatterTarget(tx, targetID, true); err != nil {
			return err
		}
		result := tx.Model(&matterEndpointIdentityRow{}).
			Where("target_id = ? AND consumer_device_id = ?", targetID, consumerDeviceID).
			Updates(map[string]any{"device_type": string(deviceType), "updated_at": time.Now().UTC().UnixMilli()})
		if result.Error != nil {
			return fmt.Errorf("change Matter endpoint device type: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("Matter endpoint identity %q not found", consumerDeviceID)
		}
		return nil
	})
}

func (s *Store) MatterEndpointIdentity(ctx context.Context, targetID, consumerDeviceID string) (target.MatterEndpointIdentity, bool, error) {
	defer s.observe(time.Now())
	if strings.TrimSpace(targetID) == "" || strings.TrimSpace(consumerDeviceID) == "" {
		return target.MatterEndpointIdentity{}, false, errors.New("Matter target ID and consumer device ID are required")
	}
	if err := s.requireMatterTarget(s.orm.WithContext(ctx), targetID, false); err != nil {
		return target.MatterEndpointIdentity{}, false, err
	}
	var row matterEndpointIdentityRow
	err := s.orm.WithContext(ctx).Where("target_id = ? AND consumer_device_id = ?", targetID, consumerDeviceID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return target.MatterEndpointIdentity{}, false, nil
	}
	if err != nil {
		return target.MatterEndpointIdentity{}, false, fmt.Errorf("read Matter endpoint identity: %w", err)
	}
	return matterEndpointIdentityFromRow(row), true, nil
}

func (s *Store) ListMatterEndpointIdentities(ctx context.Context, targetID string) ([]target.MatterEndpointIdentity, error) {
	defer s.observe(time.Now())
	if strings.TrimSpace(targetID) == "" {
		return nil, errors.New("Matter target ID is required")
	}
	if err := s.requireMatterTarget(s.orm.WithContext(ctx), targetID, false); err != nil {
		return nil, err
	}
	var rows []matterEndpointIdentityRow
	if err := s.orm.WithContext(ctx).Where("target_id = ?", targetID).Order("endpoint_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list Matter endpoint identities: %w", err)
	}
	result := make([]target.MatterEndpointIdentity, 0, len(rows))
	for _, row := range rows {
		result = append(result, matterEndpointIdentityFromRow(row))
	}
	return result, nil
}

func matterEndpointIdentityFromRow(row matterEndpointIdentityRow) target.MatterEndpointIdentity {
	return target.MatterEndpointIdentity{
		TargetID: row.TargetID, ConsumerDeviceID: row.ConsumerDeviceID, EndpointID: uint16(row.EndpointID),
		DeviceType: device.Type(row.DeviceType), Tombstone: row.Tombstone,
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
	}
}

// Short aliases keep the storage boundary ergonomic for the JSON-RPC runtime.
func (s *Store) MatterRuntimePut(ctx context.Context, targetID, key string, value []byte) error {
	return s.PutMatterRuntimeValue(ctx, targetID, key, value)
}

func (s *Store) MatterRuntimeGet(ctx context.Context, targetID, key string) ([]byte, bool, error) {
	return s.GetMatterRuntimeValue(ctx, targetID, key)
}

func (s *Store) MatterRuntimeDelete(ctx context.Context, targetID, key string) error {
	return s.DeleteMatterRuntimeValue(ctx, targetID, key)
}

func (s *Store) MatterRuntimeClear(ctx context.Context, targetID string) error {
	return s.ClearMatterRuntimeValues(ctx, targetID)
}
