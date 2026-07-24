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
	ErrCustomModelNotFound         = errors.New("custom unified model not found")
	ErrCustomModelExists           = errors.New("unified model already exists")
	ErrModelEnumOverrideNotFound   = errors.New("model enum override not found")
	ErrModelEnumOverrideExists     = errors.New("model enum override path already exists")
)

func (s *ProfileService) ModelContracts() []device.ModelContract {
	contracts := device.ModelContracts()
	s.mu.RLock()
	for _, item := range s.customModels {
		contracts = append(contracts, device.ModelContract{
			DeviceType: item.DeviceType,
			Name:       item.Name,
			Version:    item.Version,
			Parameters: []device.ModelParameter{},
			Custom: device.CustomParameterPolicy{
				Publisher: device.ParameterRole{Level: device.ParameterCustom, Behavior: "preserve-and-mark-custom"},
				Consumer:  device.ParameterRole{Level: device.ParameterCustom, Behavior: "explicit-path-mapping-only"},
			},
		})
	}
	for _, item := range s.customProperties {
		for index := range contracts {
			if contracts[index].DeviceType == item.DeviceType {
				contracts[index].Parameters = append(contracts[index].Parameters, mapping.CustomModelParameter(item))
				break
			}
		}
	}
	overrides := make([]mapping.ModelEnumOverride, 0, len(s.enumOverrides))
	for _, item := range s.enumOverrides {
		overrides = append(overrides, cloneModelEnumOverride(item))
	}
	s.mu.RUnlock()
	applyModelEnumOverrides(contracts, overrides)
	for index := range contracts {
		sort.Slice(contracts[index].Parameters, func(i, j int) bool {
			return contracts[index].Parameters[i].Path.String() < contracts[index].Parameters[j].Path.String()
		})
	}
	sort.Slice(contracts, func(i, j int) bool { return contracts[i].DeviceType < contracts[j].DeviceType })
	return contracts
}

func (s *ProfileService) ListCustomModels() []mapping.CustomModel {
	s.mu.RLock()
	result := make([]mapping.CustomModel, 0, len(s.customModels))
	for _, item := range s.customModels {
		result = append(result, item)
	}
	s.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].DeviceType < result[j].DeviceType })
	return result
}

func (s *ProfileService) CreateCustomModel(ctx context.Context, item mapping.CustomModel) (mapping.CustomModel, error) {
	if err := mapping.ValidateCustomModel(item); err != nil {
		return mapping.CustomModel{}, customModelValidationError(err)
	}
	s.mu.Lock()
	if _, builtIn := device.ModelContractFor(item.DeviceType); builtIn {
		s.mu.Unlock()
		return mapping.CustomModel{}, ErrCustomModelExists
	}
	if _, exists := s.customModels[item.DeviceType]; exists {
		s.mu.Unlock()
		return mapping.CustomModel{}, ErrCustomModelExists
	}
	if err := s.store.SaveCustomModel(ctx, item); err != nil {
		s.mu.Unlock()
		return mapping.CustomModel{}, err
	}
	s.customModels[item.DeviceType] = item
	s.mu.Unlock()
	s.notifyChanged(ctx)
	return item, nil
}

func (s *ProfileService) DeleteCustomModel(ctx context.Context, deviceType device.Type) error {
	s.mu.Lock()
	if _, exists := s.customModels[deviceType]; !exists {
		s.mu.Unlock()
		return ErrCustomModelNotFound
	}
	for _, item := range s.customProperties {
		if item.DeviceType == deviceType {
			s.mu.Unlock()
			return ErrProfileInUse
		}
	}
	for _, binding := range s.bindings {
		if binding.DeviceType == deviceType {
			s.mu.Unlock()
			return ErrProfileInUse
		}
	}
	if err := s.store.DeleteCustomModel(ctx, string(deviceType)); err != nil {
		s.mu.Unlock()
		return err
	}
	delete(s.customModels, deviceType)
	s.mu.Unlock()
	s.notifyChanged(ctx)
	return nil
}

func (s *ProfileService) ResolveModelDefinition(deviceType device.Type, path device.ParameterPath, fallback device.PropertyDefinition) (device.PropertyDefinition, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if item, ok := s.customProperties[string(deviceType)+"\x00"+path.Key()]; ok {
		definition := cloneCustomModelProperty(item).Definition
		definition.ID = path.PropertyID
		definition.ParameterLevel = device.ParameterCustom
		if override, hasOverride := s.enumOverrides[string(deviceType)+"\x00"+path.Key()]; hasOverride && definition.Type == device.ValueTypeEnum {
			definition.Enum = append([]string(nil), override.Enum...)
		}
		return definition, true
	}
	parameter, ok := s.modelParameterLocked(deviceType, path)
	if !ok {
		return fallback, false
	}
	result := fallback
	result.ID, result.Name, result.Type, result.ParameterLevel = path.PropertyID, parameter.Name, parameter.Type, parameter.Level
	if parameter.Unit != "" {
		result.Unit = parameter.Unit
	}
	result.Min = intersectMinimum(result.Min, parameter.Min)
	result.Max = intersectMaximum(result.Max, parameter.Max)
	result.Step = stricterStep(result.Step, parameter.Step)
	if parameter.StaleAfterSeconds > 0 {
		result.StaleAfterSeconds = parameter.StaleAfterSeconds
	}
	result.Readable, result.Writable, result.Notifiable = parameter.Readable, parameter.Writable, parameter.Notifiable
	result.Enum = append([]string(nil), parameter.Enum...)
	if override, ok := s.enumOverrides[string(deviceType)+"\x00"+path.Key()]; ok && result.Type == device.ValueTypeEnum {
		result.Enum = append([]string(nil), override.Enum...)
	}
	return result, true
}

func intersectMinimum(source, contract *float64) *float64 {
	if source == nil {
		return cloneFloat(contract)
	}
	if contract == nil || *source >= *contract {
		return cloneFloat(source)
	}
	return cloneFloat(contract)
}

func intersectMaximum(source, contract *float64) *float64 {
	if source == nil {
		return cloneFloat(contract)
	}
	if contract == nil || *source <= *contract {
		return cloneFloat(source)
	}
	return cloneFloat(contract)
}

func stricterStep(source, contract *float64) *float64 {
	if source == nil {
		return cloneFloat(contract)
	}
	if contract == nil || *source >= *contract {
		return cloneFloat(source)
	}
	return cloneFloat(contract)
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
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
	if !s.modelExistsLocked(item.DeviceType) {
		return mapping.CustomModelProperty{}, customModelValidationError(NewValidationError("invalid custom model property", map[string]string{"deviceType": "must reference an existing unified device model"}))
	}
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
	if !s.modelExistsLocked(item.DeviceType) {
		s.mu.Unlock()
		return mapping.CustomModelProperty{}, customModelValidationError(NewValidationError("invalid custom model property", map[string]string{"deviceType": "must reference an existing unified device model"}))
	}
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
	fields := errFields(err)
	if fields == nil {
		return err
	}
	return NewValidationError("invalid custom model property", fields)
}

func errFields(err error) map[string]string {
	var validation *mapping.ValidationError
	if !errors.As(err, &validation) {
		return nil
	}
	return validation.Fields
}

func (s *ProfileService) ListModelEnumOverrides() []mapping.ModelEnumOverride {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]mapping.ModelEnumOverride, 0, len(s.enumOverrides))
	for _, item := range s.enumOverrides {
		result = append(result, cloneModelEnumOverride(item))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].DeviceType != result[j].DeviceType {
			return result[i].DeviceType < result[j].DeviceType
		}
		return result[i].Path().String() < result[j].Path().String()
	})
	return result
}

func (s *ProfileService) UpsertModelEnumOverride(ctx context.Context, item mapping.ModelEnumOverride) (mapping.ModelEnumOverride, error) {
	if item.ID == "" {
		item.ID = mapping.ModelEnumOverrideID(item.DeviceType, item.Path())
	}
	if err := mapping.ValidateModelEnumOverride(item); err != nil {
		return mapping.ModelEnumOverride{}, NewValidationError("invalid model enum override", errFields(err))
	}
	s.mu.Lock()
	if !s.modelExistsLocked(item.DeviceType) {
		s.mu.Unlock()
		return mapping.ModelEnumOverride{}, NewValidationError("invalid model enum override", map[string]string{"deviceType": "must reference an existing unified device model"})
	}
	parameter, ok := s.modelParameterLocked(item.DeviceType, item.Path())
	if !ok {
		s.mu.Unlock()
		return mapping.ModelEnumOverride{}, NewValidationError("invalid model enum override", map[string]string{"propertyId": "must reference an existing model property"})
	}
	if parameter.Type != device.ValueTypeEnum {
		s.mu.Unlock()
		return mapping.ModelEnumOverride{}, NewValidationError("invalid model enum override", map[string]string{"propertyId": "must reference an enum property"})
	}
	if owner, exists := s.enumOverrides[item.Key()]; exists && owner.ID != item.ID {
		s.mu.Unlock()
		return mapping.ModelEnumOverride{}, ErrModelEnumOverrideExists
	}
	for _, current := range s.enumOverrides {
		if current.ID == item.ID && current.Key() != item.Key() {
			s.mu.Unlock()
			return mapping.ModelEnumOverride{}, ErrModelEnumOverrideExists
		}
	}
	if err := s.store.SaveModelEnumOverride(ctx, item); err != nil {
		s.mu.Unlock()
		return mapping.ModelEnumOverride{}, err
	}
	s.enumOverrides[item.Key()] = cloneModelEnumOverride(item)
	s.mu.Unlock()
	s.notifyChanged(ctx)
	return cloneModelEnumOverride(item), nil
}

func (s *ProfileService) DeleteModelEnumOverride(ctx context.Context, id string) error {
	s.mu.Lock()
	var current mapping.ModelEnumOverride
	found := false
	for _, candidate := range s.enumOverrides {
		if candidate.ID == id {
			current, found = candidate, true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return ErrModelEnumOverrideNotFound
	}
	if err := s.store.DeleteModelEnumOverride(ctx, id); err != nil {
		s.mu.Unlock()
		return err
	}
	delete(s.enumOverrides, current.Key())
	s.mu.Unlock()
	s.notifyChanged(ctx)
	return nil
}

func applyModelEnumOverrides(contracts []device.ModelContract, overrides []mapping.ModelEnumOverride) {
	if len(overrides) == 0 {
		return
	}
	index := make(map[string]map[string]int, len(contracts))
	for contractIndex := range contracts {
		paths := make(map[string]int, len(contracts[contractIndex].Parameters))
		for parameterIndex, parameter := range contracts[contractIndex].Parameters {
			paths[parameter.Path.Key()] = parameterIndex
		}
		index[string(contracts[contractIndex].DeviceType)] = paths
	}
	for _, override := range overrides {
		paths, ok := index[string(override.DeviceType)]
		if !ok {
			continue
		}
		parameterIndex, ok := paths[override.Path().Key()]
		if !ok {
			continue
		}
		for contractIndex := range contracts {
			if contracts[contractIndex].DeviceType != override.DeviceType {
				continue
			}
			if contracts[contractIndex].Parameters[parameterIndex].Type != device.ValueTypeEnum {
				break
			}
			contracts[contractIndex].Parameters[parameterIndex].Enum = append([]string(nil), override.Enum...)
			break
		}
	}
}

func cloneModelEnumOverride(item mapping.ModelEnumOverride) mapping.ModelEnumOverride {
	result := item
	result.Enum = append([]string(nil), item.Enum...)
	return result
}

func (s *ProfileService) modelExistsLocked(deviceType device.Type) bool {
	if _, builtIn := device.ModelContractFor(deviceType); builtIn {
		return true
	}
	_, custom := s.customModels[deviceType]
	return custom
}
