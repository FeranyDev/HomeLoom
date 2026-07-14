package application

import (
	"context"
	"errors"
	"sort"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/mapping"
)

var (
	ErrCustomModelPropertyNotFound = errors.New("custom model property not found")
	ErrCustomModelPropertyExists   = errors.New("custom model property path already exists")
)

func (s *ProfileService) ModelContracts() []device.ModelContract {
	contracts := device.ModelContracts()
	s.mu.RLock()
	for _, item := range s.customProperties {
		for index := range contracts {
			if contracts[index].DeviceType == item.DeviceType {
				contracts[index].Parameters = append(contracts[index].Parameters, mapping.CustomModelParameter(item))
				break
			}
		}
	}
	s.mu.RUnlock()
	for index := range contracts {
		sort.Slice(contracts[index].Parameters, func(i, j int) bool {
			return contracts[index].Parameters[i].Path.String() < contracts[index].Parameters[j].Path.String()
		})
	}
	return contracts
}

func (s *ProfileService) ResolveModelDefinition(deviceType device.Type, path device.ParameterPath, fallback device.PropertyDefinition) (device.PropertyDefinition, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if item, ok := s.customProperties[string(deviceType)+"\x00"+path.Key()]; ok {
		definition := cloneCustomModelProperty(item).Definition
		definition.ID = path.PropertyID
		definition.ParameterLevel = device.ParameterCustom
		return definition, true
	}
	parameter, ok := s.modelParameterLocked(deviceType, path)
	if !ok {
		return fallback, false
	}
	result := fallback
	result.ID, result.Name, result.Type, result.ParameterLevel = path.PropertyID, parameter.Name, parameter.Type, parameter.Level
	result.Unit, result.Readable, result.Writable, result.Notifiable = parameter.Unit, parameter.Readable, parameter.Writable, parameter.Notifiable
	result.Enum = append([]string(nil), parameter.Enum...)
	return result, true
}

func (s *ProfileService) ListCustomModelProperties() []mapping.CustomModelProperty {
	s.mu.RLock()
	result := make([]mapping.CustomModelProperty, 0, len(s.customProperties))
	for _, item := range s.customProperties {
		result = append(result, cloneCustomModelProperty(item))
	}
	s.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].DeviceType == result[j].DeviceType {
			return result[i].Path().String() < result[j].Path().String()
		}
		return result[i].DeviceType < result[j].DeviceType
	})
	return result
}

func (s *ProfileService) CreateCustomModelProperty(ctx context.Context, item mapping.CustomModelProperty) (mapping.CustomModelProperty, error) {
	item.Definition.ParameterLevel = device.ParameterCustom
	if err := mapping.ValidateCustomModelProperty(item); err != nil {
		return mapping.CustomModelProperty{}, customModelValidationError(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, standard := s.modelParameterLocked(item.DeviceType, item.Path()); standard {
		return mapping.CustomModelProperty{}, ErrCustomModelPropertyExists
	}
	if _, exists := s.customProperties[item.Key()]; exists {
		return mapping.CustomModelProperty{}, ErrCustomModelPropertyExists
	}
	for _, current := range s.customProperties {
		if current.ID == item.ID {
			return mapping.CustomModelProperty{}, ErrCustomModelPropertyExists
		}
	}
	if err := s.store.SaveCustomModelProperty(ctx, item); err != nil {
		return mapping.CustomModelProperty{}, err
	}
	s.customProperties[item.Key()] = cloneCustomModelProperty(item)
	return cloneCustomModelProperty(item), nil
}

func (s *ProfileService) UpdateCustomModelProperty(ctx context.Context, id string, item mapping.CustomModelProperty) (mapping.CustomModelProperty, error) {
	item.ID = id
	item.Definition.ParameterLevel = device.ParameterCustom
	if err := mapping.ValidateCustomModelProperty(item); err != nil {
		return mapping.CustomModelProperty{}, customModelValidationError(err)
	}
	s.mu.Lock()
	var current mapping.CustomModelProperty
	found := false
	for _, candidate := range s.customProperties {
		if candidate.ID == id {
			current, found = candidate, true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return mapping.CustomModelProperty{}, ErrCustomModelPropertyNotFound
	}
	if parameter, standard := s.modelParameterLocked(item.DeviceType, item.Path()); standard && parameter.Level != device.ParameterCustom {
		s.mu.Unlock()
		return mapping.CustomModelProperty{}, ErrCustomModelPropertyExists
	}
	if owner, exists := s.customProperties[item.Key()]; exists && owner.ID != id {
		s.mu.Unlock()
		return mapping.CustomModelProperty{}, ErrCustomModelPropertyExists
	}
	if err := s.store.SaveCustomModelProperty(ctx, item); err != nil {
		s.mu.Unlock()
		return mapping.CustomModelProperty{}, err
	}
	delete(s.customProperties, current.Key())
	s.customProperties[item.Key()] = cloneCustomModelProperty(item)
	s.mu.Unlock()
	s.notifyChanged(ctx)
	return cloneCustomModelProperty(item), nil
}

func (s *ProfileService) DeleteCustomModelProperty(ctx context.Context, id string) error {
	s.mu.Lock()
	var current mapping.CustomModelProperty
	found := false
	for _, candidate := range s.customProperties {
		if candidate.ID == id {
			current, found = candidate, true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return ErrCustomModelPropertyNotFound
	}
	for _, binding := range s.bindings {
		if binding.ModelPath().Key() == current.Path().Key() && (binding.DeviceType == current.DeviceType || binding.DeviceType == "") {
			s.mu.Unlock()
			return ErrProfileInUse
		}
	}
	if err := s.store.DeleteCustomModelProperty(ctx, id); err != nil {
		s.mu.Unlock()
		return err
	}
	delete(s.customProperties, current.Key())
	s.mu.Unlock()
	s.notifyChanged(ctx)
	return nil
}

func cloneCustomModelProperty(item mapping.CustomModelProperty) mapping.CustomModelProperty {
	result := item
	result.Definition.Enum = append([]string(nil), item.Definition.Enum...)
	if item.Definition.Min != nil {
		value := *item.Definition.Min
		result.Definition.Min = &value
	}
	if item.Definition.Max != nil {
		value := *item.Definition.Max
		result.Definition.Max = &value
	}
	if item.Definition.Step != nil {
		value := *item.Definition.Step
		result.Definition.Step = &value
	}
	return result
}

func customModelValidationError(err error) error {
	var validation *mapping.ValidationError
	if !errors.As(err, &validation) {
		return err
	}
	return NewValidationError("invalid custom model property", validation.Fields)
}
