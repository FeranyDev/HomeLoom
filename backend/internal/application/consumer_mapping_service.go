package application

import (
	"fmt"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/mapping"
)

// ProjectConsumerDevice creates a Consumer-specific view without mutating the
// Core registry. Explicit Consumer bindings can source any standard or custom
// unified-model property and place it at the Consumer's default adapter path.
func (s *ProfileService) ProjectConsumerDevice(consumerID string, item device.Device) (device.Device, error) {
	return s.ProjectConsumerDeviceInstance(consumerID, "", "", item)
}

func (s *ProfileService) ProjectConsumerDeviceInstance(consumerID, targetID, consumerDeviceID string, item device.Device) (device.Device, error) {
	result := cloneDevice(item)
	contract, supported := consumerContract(consumerID, item.Type)
	if !supported {
		return result, nil
	}
	for _, parameter := range contract.Parameters {
		binding, profile, applied, err := s.resolveConsumerBinding(item.ProviderID, item.ID, targetID, consumerDeviceID, consumerID, item.Type, parameter.Target)
		if err != nil {
			return device.Device{}, err
		}
		if !applied {
			continue
		}
		property, exists := item.Property(binding.ModelPath().EndpointID, binding.ModelPath().CapabilityID, binding.ModelPath().PropertyID)
		if !exists {
			continue
		}
		if binding.ProfileID != "" {
			preview, previewErr := mapping.Preview(mapping.PreviewRequest{Profile: profile, Direction: mapping.DirectionForward, Value: &property.Value})
			if previewErr != nil {
				return device.Device{}, fmt.Errorf("consumer binding %q: %w", binding.ID, previewErr)
			}
			property.Value = preview.Value
			property.Definition.Type = profile.OutputType
		}
		property.Definition.ID = parameter.Source.PropertyID
		property.Definition.ParameterLevel = parameter.Level
		upsertConsumerProperty(&result, parameter.Source, property)
	}
	if _, err := device.ProjectForConsumer(result, contract); err != nil {
		return device.Device{}, err
	}
	return result, nil
}

// ProjectConsumerDeviceSourcesInstance composes one Consumer-owned device from
// an ordered set of unified devices. Explicit per-source bindings are applied
// first (primary source first), then compatible primary-source identity values
// fill any still-unmapped properties.
func (s *ProfileService) ProjectConsumerDeviceSourcesInstance(consumerID, targetID, consumerDeviceID string, targetType device.Type, items []device.Device) (device.Device, error) {
	if len(items) == 0 {
		return device.Device{}, fmt.Errorf("consumer device %q has no source devices", consumerDeviceID)
	}
	contract, supported := consumerContract(consumerID, targetType)
	if !supported {
		return device.Device{}, fmt.Errorf("consumer %q does not support device type %q", consumerID, targetType)
	}
	primary := items[0]
	result := cloneDevice(primary)
	result.Type = targetType
	if primary.Type != targetType {
		result.Endpoints = nil
	}
	for _, current := range items[1:] {
		if current.LastUpdateAt.After(result.LastUpdateAt) {
			result.LastUpdateAt = current.LastUpdateAt
		}
		if current.IsOnline() {
			result.SetAvailability(device.AvailabilityOnline)
		}
	}

	for _, parameter := range contract.Parameters {
		for _, source := range items {
			binding, profile, applied, err := s.resolveConsumerBinding(source.ProviderID, source.ID, targetID, consumerDeviceID, consumerID, targetType, parameter.Target)
			if err != nil {
				return device.Device{}, err
			}
			if !applied {
				continue
			}
			path := binding.ModelPath()
			property, exists := source.Property(path.EndpointID, path.CapabilityID, path.PropertyID)
			if !exists {
				continue
			}
			if binding.ProfileID != "" {
				preview, previewErr := mapping.Preview(mapping.PreviewRequest{Profile: profile, Direction: mapping.DirectionForward, Value: &property.Value})
				if previewErr != nil {
					return device.Device{}, fmt.Errorf("consumer binding %q: %w", binding.ID, previewErr)
				}
				property.Value = preview.Value
				property.Definition.Type = profile.OutputType
			}
			property.Definition.ID = parameter.Source.PropertyID
			property.Definition.ParameterLevel = parameter.Level
			upsertConsumerProperty(&result, parameter.Source, property)
			break
		}
	}
	if _, err := device.ProjectForConsumer(result, contract); err != nil {
		return device.Device{}, err
	}
	return result, nil
}

// ResolveConsumerWrite maps the adapter's stable default path back to the
// configured unified-model source and reverses the Consumer conversion.
func (s *ProfileService) ResolveConsumerWrite(providerID, deviceID, consumerID string, deviceType device.Type, endpointID, capabilityID, propertyID string, value device.PropertyValue) (device.ParameterPath, device.PropertyValue, string, bool, error) {
	return s.ResolveConsumerWriteInstance(providerID, deviceID, "", "", consumerID, deviceType, endpointID, capabilityID, propertyID, value)
}

func (s *ProfileService) ResolveConsumerWriteInstance(providerID, deviceID, targetID, consumerDeviceID, consumerID string, deviceType device.Type, endpointID, capabilityID, propertyID string, value device.PropertyValue) (device.ParameterPath, device.PropertyValue, string, bool, error) {
	identity := device.ParameterPath{EndpointID: endpointID, CapabilityID: capabilityID, PropertyID: propertyID}
	contract, supported := consumerContract(consumerID, deviceType)
	if !supported {
		return identity, value, "", false, nil
	}
	for _, parameter := range contract.Parameters {
		if parameter.Source != identity {
			continue
		}
		binding, profile, applied, err := s.resolveConsumerBinding(providerID, deviceID, targetID, consumerDeviceID, consumerID, deviceType, parameter.Target)
		if err != nil || !applied {
			return identity, value, binding.ID, applied, err
		}
		if binding.ProfileID == "" {
			return binding.ModelPath(), value, binding.ID, true, nil
		}
		preview, previewErr := mapping.Preview(mapping.PreviewRequest{Profile: profile, Direction: mapping.DirectionReverse, Value: &value})
		if previewErr != nil {
			return device.ParameterPath{}, device.PropertyValue{}, binding.ID, true, fmt.Errorf("consumer binding %q: %w", binding.ID, previewErr)
		}
		return binding.ModelPath(), preview.Value, binding.ID, true, nil
	}
	return identity, value, "", false, nil
}

func (s *ProfileService) resolveConsumerBinding(providerID, deviceID, targetID, consumerDeviceID, consumerID string, deviceType device.Type, target string) (mapping.Binding, mapping.Profile, bool, error) {
	key := (mapping.Binding{Stage: mapping.StageConsumer, ProviderID: providerID, DeviceID: deviceID, TargetID: targetID, ConsumerDeviceID: consumerDeviceID, ConsumerID: consumerID, DeviceType: deviceType, ConsumerProperty: target}).Key()
	s.mu.RLock()
	ids := s.bindingsByKey[key]
	id, ok := "", false
	if len(ids) > 0 {
		id, ok = ids[0], true
	}
	if !ok || !s.bindings[id].Enabled {
		s.mu.RUnlock()
		return mapping.Binding{}, mapping.Profile{}, false, nil
	}
	binding := s.bindings[id]
	if binding.ProfileID == "" {
		s.mu.RUnlock()
		return binding, mapping.Profile{}, true, nil
	}
	profile, ok := s.profileLocked(binding.ProfileID)
	s.mu.RUnlock()
	if !ok {
		return binding, mapping.Profile{}, true, fmt.Errorf("mapping profile %q not found", binding.ProfileID)
	}
	return binding, profile, true, nil
}

func consumerContract(consumerID string, deviceType device.Type) (device.ConsumerModelContract, bool) {
	return mapping.ConsumerContract(consumerID, deviceType)
}

func cloneDevice(item device.Device) device.Device {
	result := item
	result.Endpoints = make([]device.Endpoint, len(item.Endpoints))
	for endpointIndex, endpoint := range item.Endpoints {
		result.Endpoints[endpointIndex] = endpoint
		result.Endpoints[endpointIndex].Capabilities = make([]device.Capability, len(endpoint.Capabilities))
		for capabilityIndex, capability := range endpoint.Capabilities {
			result.Endpoints[endpointIndex].Capabilities[capabilityIndex] = capability
			result.Endpoints[endpointIndex].Capabilities[capabilityIndex].Properties = append([]device.Property(nil), capability.Properties...)
			result.Endpoints[endpointIndex].Capabilities[capabilityIndex].Commands = append([]device.CommandDefinition(nil), capability.Commands...)
			result.Endpoints[endpointIndex].Capabilities[capabilityIndex].Events = append([]device.EventDefinition(nil), capability.Events...)
		}
	}
	return result
}

func upsertConsumerProperty(item *device.Device, path device.ParameterPath, property device.Property) {
	capability := ensureCapability(item, path, path.EndpointID, path.EndpointID, path.CapabilityID)
	for index := range capability.Properties {
		if capability.Properties[index].Definition.ID == path.PropertyID {
			capability.Properties[index] = property
			return
		}
	}
	capability.Properties = append(capability.Properties, property)
}
