package gormstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	domainmcp "github.com/feranydev/homeloom/backend/internal/domain/mcp"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) GetMCPDeviceConfig(ctx context.Context, deviceID string) (domainmcp.DeviceConfig, bool, error) {
	defer s.observe(time.Now())
	var row mcpDeviceConfigRow
	if err := s.orm.WithContext(ctx).Where("device_id = ?", deviceID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domainmcp.DeviceConfig{}, false, nil
		}
		return domainmcp.DeviceConfig{}, false, fmt.Errorf("get MCP device config: %w", err)
	}
	return domainmcp.DeviceConfig{DeviceID: row.DeviceID, Enabled: row.Enabled, UsageNote: row.UsageNote, DefaultAccess: domainmcp.Access(row.DefaultAccess)}.Normalize(), true, nil
}

func (s *Store) SaveMCPDeviceConfig(ctx context.Context, config domainmcp.DeviceConfig) error {
	defer s.observe(time.Now())
	config = config.Normalize()
	now := time.Now().UTC().UnixMilli()
	row := mcpDeviceConfigRow{DeviceID: config.DeviceID, Enabled: config.Enabled, UsageNote: config.UsageNote, DefaultAccess: string(config.DefaultAccess), CreatedAt: now, UpdatedAt: now}
	if err := s.orm.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "device_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "usage_note", "default_access", "updated_at"}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("save MCP device config: %w", err)
	}
	return nil
}

func (s *Store) ListMCPPropertyConfigs(ctx context.Context, deviceID string) ([]domainmcp.PropertyConfig, error) {
	defer s.observe(time.Now())
	var rows []mcpPropertyConfigRow
	if err := s.orm.WithContext(ctx).Where("device_id = ?", deviceID).Order("endpoint_id, capability_id, property_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list MCP property configs: %w", err)
	}
	configs := make([]domainmcp.PropertyConfig, 0, len(rows))
	for _, row := range rows {
		configs = append(configs, mcpPropertyConfig(row))
	}
	return configs, nil
}

func (s *Store) GetMCPPropertyConfig(ctx context.Context, path domainmcp.PropertyPath) (domainmcp.PropertyConfig, bool, error) {
	defer s.observe(time.Now())
	var row mcpPropertyConfigRow
	if err := s.orm.WithContext(ctx).Where("device_id = ? AND endpoint_id = ? AND capability_id = ? AND property_id = ?", path.DeviceID, path.EndpointID, path.CapabilityID, path.PropertyID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domainmcp.PropertyConfig{}, false, nil
		}
		return domainmcp.PropertyConfig{}, false, fmt.Errorf("get MCP property config: %w", err)
	}
	return mcpPropertyConfig(row), true, nil
}

func (s *Store) SaveMCPPropertyConfig(ctx context.Context, config domainmcp.PropertyConfig) error {
	defer s.observe(time.Now())
	config = config.Normalize()
	now := time.Now().UTC().UnixMilli()
	row := mcpPropertyConfigRow{
		DeviceID: config.DeviceID, EndpointID: config.EndpointID, CapabilityID: config.CapabilityID, PropertyID: config.PropertyID,
		UsageNote: config.UsageNote, Access: string(config.Access), CreatedAt: now, UpdatedAt: now,
	}
	if err := s.orm.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "device_id"}, {Name: "endpoint_id"}, {Name: "capability_id"}, {Name: "property_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"usage_note", "access", "updated_at"}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("save MCP property config: %w", err)
	}
	return nil
}

func (s *Store) DeleteMCPPropertyConfig(ctx context.Context, path domainmcp.PropertyPath) error {
	defer s.observe(time.Now())
	if err := s.orm.WithContext(ctx).Where("device_id = ? AND endpoint_id = ? AND capability_id = ? AND property_id = ?", path.DeviceID, path.EndpointID, path.CapabilityID, path.PropertyID).Delete(&mcpPropertyConfigRow{}).Error; err != nil {
		return fmt.Errorf("delete MCP property config: %w", err)
	}
	return nil
}

func mcpPropertyConfig(row mcpPropertyConfigRow) domainmcp.PropertyConfig {
	return domainmcp.PropertyConfig{
		PropertyPath: domainmcp.PropertyPath{DeviceID: row.DeviceID, EndpointID: row.EndpointID, CapabilityID: row.CapabilityID, PropertyID: row.PropertyID},
		UsageNote:    row.UsageNote, Access: domainmcp.Access(row.Access),
	}.Normalize()
}
