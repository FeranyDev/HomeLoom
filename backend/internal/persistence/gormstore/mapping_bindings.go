package gormstore

import (
	"context"
	"fmt"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/mapping"
	"gorm.io/gorm/clause"
)

func (s *Store) ListMappingBindings(ctx context.Context) ([]mapping.Binding, error) {
	defer s.observe(time.Now())
	var rows []mappingBindingRow
	if err := s.orm.WithContext(ctx).Order("stage, provider_id, device_id, target_id, consumer_device_id, consumer_id, device_type, model_endpoint_id, model_capability_id, model_property_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list mapping bindings: %w", err)
	}
	result := make([]mapping.Binding, 0, len(rows))
	for _, row := range rows {
		result = append(result, mapping.Binding{ID: row.ID, Stage: mapping.BindingStage(row.Stage), ProfileID: row.ProfileID, ProviderID: row.ProviderID, DeviceID: row.DeviceID, EndpointID: row.EndpointID, CapabilityID: row.CapabilityID, PropertyID: row.PropertyID, DeviceType: device.Type(row.DeviceType), ConsumerDeviceType: device.Type(row.ConsumerDeviceType), ModelEndpointID: row.ModelEndpointID, ModelCapabilityID: row.ModelCapabilityID, ModelPropertyID: row.ModelPropertyID, ConsumerID: row.ConsumerID, TargetID: row.TargetID, ConsumerDeviceID: row.ConsumerDeviceID, ConsumerProperty: row.ConsumerProperty, Enabled: row.Enabled})
	}
	return result, nil
}

func (s *Store) SaveMappingBinding(ctx context.Context, item mapping.Binding) error {
	defer s.observe(time.Now())
	now := time.Now().UTC().UnixMilli()
	stage := item.EffectiveStage()
	model := item.ModelPath()
	row := mappingBindingRow{ID: item.ID, Stage: string(stage), ProfileID: item.ProfileID, ProviderID: item.ProviderID, DeviceID: item.DeviceID, EndpointID: item.EndpointID, CapabilityID: item.CapabilityID, PropertyID: item.PropertyID, DeviceType: string(item.DeviceType), ConsumerDeviceType: string(item.ConsumerDeviceType), ModelEndpointID: model.EndpointID, ModelCapabilityID: model.CapabilityID, ModelPropertyID: model.PropertyID, ConsumerID: item.ConsumerID, TargetID: item.TargetID, ConsumerDeviceID: item.ConsumerDeviceID, ConsumerProperty: item.ConsumerProperty, Enabled: item.Enabled, CreatedAt: now, UpdatedAt: now}
	err := s.orm.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"stage", "profile_id", "provider_id", "device_id", "endpoint_id", "capability_id", "property_id", "device_type", "consumer_device_type", "model_endpoint_id", "model_capability_id", "model_property_id", "consumer_id", "target_id", "consumer_device_id", "consumer_property", "enabled", "updated_at"})}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("save mapping binding %q: %w", item.ID, err)
	}
	return nil
}

func (s *Store) DeleteMappingBinding(ctx context.Context, id string) error {
	defer s.observe(time.Now())
	result := s.orm.WithContext(ctx).Where("id = ?", id).Delete(&mappingBindingRow{})
	if result.Error != nil {
		return fmt.Errorf("delete mapping binding: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("mapping binding %q not found", id)
	}
	return nil
}
