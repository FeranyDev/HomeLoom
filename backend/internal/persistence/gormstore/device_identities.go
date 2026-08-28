package gormstore

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const StableIdentityRetention = 30 * 24 * time.Hour

var ErrProviderDeviceIdentityConflict = errors.New("provider device identity conflicts with an existing internal device id")

// EnsureProviderDeviceIdentity records the Provider-native key that produced
// a canonical Device.ID. The Device.ID remains the public routing key, so this
// registry is intentionally observational: it prevents a configured Provider
// from silently repointing an established identity. When an operator deletes
// that Provider and recreates it with a new ID, the orphaned binding may move
// to the replacement while preserving the canonical Device.ID.
func (s *Store) EnsureProviderDeviceIdentity(ctx context.Context, providerID, providerDeviceID, deviceID string) error {
	defer s.observe(time.Now())
	if !device.ValidStableID(providerID) || !device.ValidStableID(deviceID) {
		return fmt.Errorf("provider and internal device ids must be stable ids")
	}
	if providerDeviceID = strings.TrimSpace(providerDeviceID); providerDeviceID == "" || len(providerDeviceID) > 512 {
		return fmt.Errorf("provider device id is required and must be at most 512 characters")
	}
	now := time.Now().UTC().UnixMilli()
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current providerDeviceIdentityRow
		err := tx.Where("provider_id = ? AND provider_device_id = ?", providerID, providerDeviceID).Take(&current).Error
		if err == nil {
			if current.DeviceID != deviceID {
				return fmt.Errorf("%w: %s/%s is already bound to %s", ErrProviderDeviceIdentityConflict, providerID, providerDeviceID, current.DeviceID)
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read provider device identity: %w", err)
		}
		var byDevice providerDeviceIdentityRow
		err = tx.Where("device_id = ?", deviceID).Take(&byDevice).Error
		if err == nil {
			// Older HomeLoom releases stored the already-normalized Device.ID as
			// ProviderDeviceID. Once a Provider exposes its true upstream ID,
			// migrate that observational key in place when ownership is unchanged.
			if byDevice.ProviderID == providerID {
				result := tx.Model(&providerDeviceIdentityRow{}).
					Where("provider_id = ? AND provider_device_id = ? AND device_id = ?", providerID, byDevice.ProviderDeviceID, deviceID).
					Updates(map[string]any{"provider_device_id": providerDeviceID, "updated_at": now})
				if result.Error != nil {
					return fmt.Errorf("migrate provider device identity: %w", result.Error)
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf("%w: internal device %s binding changed while migrating provider identity", ErrProviderDeviceIdentityConflict, deviceID)
				}
				return nil
			}
			var previousProvider providerRow
			err = tx.Select("id").Where("id = ?", byDevice.ProviderID).Take(&previousProvider).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				result := tx.Model(&providerDeviceIdentityRow{}).
					Where("provider_id = ? AND provider_device_id = ? AND device_id = ?", byDevice.ProviderID, byDevice.ProviderDeviceID, deviceID).
					Updates(map[string]any{"provider_id": providerID, "provider_device_id": providerDeviceID, "updated_at": now})
				if result.Error != nil {
					return fmt.Errorf("rebind orphaned provider device identity: %w", result.Error)
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf("%w: internal device %s binding changed while replacing provider", ErrProviderDeviceIdentityConflict, deviceID)
				}
				return nil
			}
			if err != nil {
				return fmt.Errorf("read provider for internal device identity: %w", err)
			}
			return fmt.Errorf("%w: internal device %s is already bound to %s/%s", ErrProviderDeviceIdentityConflict, deviceID, byDevice.ProviderID, byDevice.ProviderDeviceID)
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read internal device identity: %w", err)
		}
		if err := tx.Create(&providerDeviceIdentityRow{ProviderID: providerID, ProviderDeviceID: providerDeviceID, DeviceID: deviceID, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return fmt.Errorf("save provider device identity: %w", err)
		}
		return nil
	})
}

// EnsureDeviceTopologyIdentity stores endpoint/capability paths without
// deleting historical rows. A Provider restart, an offline device, or a
// temporary capability omission must never change a published identity.
func (s *Store) EnsureDeviceTopologyIdentity(ctx context.Context, item device.Device) error {
	defer s.observe(time.Now())
	if !device.ValidStableID(item.ID) {
		return fmt.Errorf("device id must be a stable id")
	}
	now := time.Now().UTC().UnixMilli()
	return s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, endpoint := range item.Endpoints {
			if !device.ValidStableID(endpoint.ID) {
				return fmt.Errorf("endpoint id %q must be a stable id", endpoint.ID)
			}
			for _, capability := range endpoint.Capabilities {
				if !device.ValidStableID(capability.ID) {
					return fmt.Errorf("capability id %q must be a stable id", capability.ID)
				}
				row := deviceCapabilityIdentityRow{DeviceID: item.ID, EndpointID: endpoint.ID, CapabilityID: capability.ID, CreatedAt: now, UpdatedAt: now}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
					return fmt.Errorf("save endpoint/capability identity: %w", err)
				}
			}
		}
		return nil
	})
}

func ensureLogicalDeviceIdentity(tx *gorm.DB, id string, now int64) error {
	if !device.ValidStableID(id) {
		return fmt.Errorf("logical device id must be a stable id")
	}
	row := logicalDeviceIdentityRow{LogicalDeviceID: id, CreatedAt: now, UpdatedAt: now}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "logical_device_id"}},
		DoUpdates: clause.Assignments(map[string]any{"deleted_at": 0, "purge_after": 0, "updated_at": now}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("save logical device identity: %w", err)
	}
	return nil
}

func markLogicalDeviceIdentityDeleted(tx *gorm.DB, id string, now int64, retention time.Duration) error {
	if !device.ValidStableID(id) {
		return fmt.Errorf("logical device id must be a stable id")
	}
	if retention <= 0 {
		return fmt.Errorf("identity retention must be positive")
	}
	row := logicalDeviceIdentityRow{LogicalDeviceID: id, DeletedAt: now, PurgeAfter: now + retention.Milliseconds(), CreatedAt: now, UpdatedAt: now}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "logical_device_id"}},
		DoUpdates: clause.Assignments(map[string]any{"deleted_at": now, "purge_after": row.PurgeAfter, "updated_at": now}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("mark logical device identity deleted: %w", err)
	}
	return nil
}

// PruneExpiredStableIdentities removes only IDs that were explicitly deleted
// and have reached their retention deadline. It is deliberately opt-in; a
// Provider outage or a normal discovery refresh never triggers purging.
func (s *Store) PruneExpiredStableIdentities(ctx context.Context, now time.Time) (int64, error) {
	defer s.observe(time.Now())
	result := s.orm.WithContext(ctx).
		Where("deleted_at > 0 AND purge_after > 0 AND purge_after <= ?", now.UTC().UnixMilli()).
		Delete(&logicalDeviceIdentityRow{})
	if result.Error != nil {
		return 0, fmt.Errorf("prune expired logical device identities: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// HomeKitAccessoryUUID returns a random, durable opaque UUID for one Target
// accessory. HAP still owns wire-level AID/IID allocation; this mapping gives
// the application a stable Target/Device association for reconciliation and
// diagnostics without deriving identity from display names.
func (s *Store) HomeKitAccessoryUUID(ctx context.Context, targetID, deviceID string) (string, error) {
	defer s.observe(time.Now())
	if !device.ValidStableID(targetID) || !device.ValidStableID(deviceID) {
		return "", fmt.Errorf("target and device ids must be stable ids")
	}
	var value string
	err := s.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row homeKitAccessoryUUIDRow
		err := tx.Select("uuid").Where("target_id = ? AND device_id = ?", targetID, deviceID).Take(&row).Error
		if err == nil {
			value = row.UUID
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read HomeKit accessory UUID: %w", err)
		}
		value, err = randomUUIDv4()
		if err != nil {
			return err
		}
		if err := tx.Create(&homeKitAccessoryUUIDRow{TargetID: targetID, DeviceID: deviceID, UUID: value, CreatedAt: time.Now().UTC().UnixMilli()}).Error; err != nil {
			return fmt.Errorf("save HomeKit accessory UUID: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return value, nil
}

func randomUUIDv4() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate accessory UUID: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}
