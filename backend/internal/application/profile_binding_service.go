package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func (s *ProfileService) SetChangeHandler(handler func(context.Context)) {
	s.mu.Lock()
	s.changeHandler = handler
	s.mu.Unlock()
}

func (s *ProfileService) ListBindings() []mapping.Binding {
	s.mu.RLock()
	result := make([]mapping.Binding, 0, len(s.bindings))
	for _, item := range s.bindings {
		result = append(result, item)
	}
	s.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].DeviceID == result[j].DeviceID {
			return result[i].Key() < result[j].Key()
		}
		return result[i].DeviceID < result[j].DeviceID
	})
	return result
}

func (s *ProfileService) GetBinding(id string) (mapping.Binding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.bindings[id]
	if !ok {
		return mapping.Binding{}, ErrBindingNotFound
	}
	return item, nil
}

func (s *ProfileService) CreateBinding(ctx context.Context, item mapping.Binding) (mapping.Binding, error) {
	if item.ID == "" {
		id, err := newBindingID()
		if err != nil {
			return mapping.Binding{}, err
		}
		item.ID = id
	}
	item = canonicalBinding(item)
	s.mu.Lock()
	if err := s.validateBindingLocked(item); err != nil {
		s.mu.Unlock()
		return mapping.Binding{}, err
	}
	if _, exists := s.bindings[item.ID]; exists {
		s.mu.Unlock()
		return mapping.Binding{}, ErrBindingExists
	}
	if _, exists := s.bindingsByKey[item.Key()]; exists {
		s.mu.Unlock()
		return mapping.Binding{}, ErrBindingExists
	}
	if item.EffectiveStage() == mapping.StageProvider {
		if _, exists := s.bindingsByModel[item.ModelKey()]; exists {
			s.mu.Unlock()
			return mapping.Binding{}, ErrBindingExists
		}
	}
	if err := s.store.SaveMappingBinding(ctx, item); err != nil {
		s.mu.Unlock()
		return mapping.Binding{}, err
	}
	s.bindings[item.ID] = item
	s.bindingsByKey[item.Key()] = item.ID
	if item.EffectiveStage() == mapping.StageProvider {
		s.bindingsByModel[item.ModelKey()] = item.ID
	}
	s.mu.Unlock()
	s.notifyChanged(ctx)
	return item, nil
}

func (s *ProfileService) UpdateBinding(ctx context.Context, id string, item mapping.Binding) (mapping.Binding, error) {
	item.ID = id
	item = canonicalBinding(item)
	s.mu.Lock()
	current, exists := s.bindings[id]
	if !exists {
		s.mu.Unlock()
		return mapping.Binding{}, ErrBindingNotFound
	}
	if err := s.validateBindingLocked(item); err != nil {
		s.mu.Unlock()
		return mapping.Binding{}, err
	}
	if owner, exists := s.bindingsByKey[item.Key()]; exists && owner != id {
		s.mu.Unlock()
		return mapping.Binding{}, ErrBindingExists
	}
	if item.EffectiveStage() == mapping.StageProvider {
		if owner, exists := s.bindingsByModel[item.ModelKey()]; exists && owner != id {
			s.mu.Unlock()
			return mapping.Binding{}, ErrBindingExists
		}
	}
	if err := s.store.SaveMappingBinding(ctx, item); err != nil {
		s.mu.Unlock()
		return mapping.Binding{}, err
	}
	delete(s.bindingsByKey, current.Key())
	if current.EffectiveStage() == mapping.StageProvider {
		delete(s.bindingsByModel, current.ModelKey())
	}
	s.bindings[id] = item
	s.bindingsByKey[item.Key()] = id
	if item.EffectiveStage() == mapping.StageProvider {
		s.bindingsByModel[item.ModelKey()] = id
	}
	s.mu.Unlock()
	s.notifyChanged(ctx)
	return item, nil
}

func canonicalBinding(item mapping.Binding) mapping.Binding {
	item.Stage = item.EffectiveStage()
	model := item.ModelPath()
	item.ModelEndpointID, item.ModelCapabilityID, item.ModelPropertyID = model.EndpointID, model.CapabilityID, model.PropertyID
	return item
}

func (s *ProfileService) DeleteBinding(ctx context.Context, id string) error {
	s.mu.Lock()
	item, exists := s.bindings[id]
	if !exists {
		s.mu.Unlock()
		return ErrBindingNotFound
	}
	if err := s.store.DeleteMappingBinding(ctx, id); err != nil {
		s.mu.Unlock()
		return err
	}
	delete(s.bindings, id)
	delete(s.bindingsByKey, item.Key())
	if item.EffectiveStage() == mapping.StageProvider {
		delete(s.bindingsByModel, item.ModelKey())
	}
	s.mu.Unlock()
	s.notifyChanged(ctx)
	return nil
}

func (s *ProfileService) TransformProperty(providerID, deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue, direction mapping.Direction) (device.PropertyValue, string, bool, error) {
	binding, profile, applied, err := s.resolveProviderBinding(providerID, deviceID, endpointID, capabilityID, propertyID, direction)
	if err != nil || !applied {
		return value, binding.ID, applied, err
	}
	if binding.ProfileID == "" {
		return value, binding.ID, true, nil
	}
	result, err := mapping.Preview(mapping.PreviewRequest{Profile: profile, Direction: direction, Value: &value})
	if err != nil {
		return device.PropertyValue{}, binding.ID, true, fmt.Errorf("binding %q (%s): %w", binding.ID, mapping.BindingPath(binding), err)
	}
	return result.Value, binding.ID, true, nil
}

func (s *ProfileService) TransformPropertyDefinition(providerID, deviceID, endpointID, capabilityID, propertyID string, definition device.PropertyDefinition) (device.PropertyDefinition, string, bool, error) {
	binding, profile, applied, err := s.resolveProviderBinding(providerID, deviceID, endpointID, capabilityID, propertyID, mapping.DirectionForward)
	if err != nil || !applied {
		return definition, binding.ID, applied, err
	}
	if binding.ProfileID == "" {
		return definition, binding.ID, true, nil
	}
	result := definition
	result.Type = profile.OutputType
	mapNumber := func(value float64) (float64, error) {
		input := device.NumberValue(value)
		if profile.InputType == device.ValueTypeInt {
			input = device.IntValue(int64(value))
		}
		preview, previewErr := mapping.Preview(mapping.PreviewRequest{Profile: profile, Direction: mapping.DirectionForward, Value: &input})
		if previewErr != nil || (preview.Value.Number == nil && preview.Value.Int == nil) {
			if previewErr == nil {
				previewErr = fmt.Errorf("numeric definition transform did not produce a number")
			}
			return 0, previewErr
		}
		if preview.Value.Int != nil {
			return float64(*preview.Value.Int), nil
		}
		return *preview.Value.Number, nil
	}
	if profile.InputType == device.ValueTypeNumber || profile.InputType == device.ValueTypeInt {
		var minimum, maximum *float64
		if definition.Min != nil {
			value, mapErr := mapNumber(*definition.Min)
			if mapErr != nil {
				return device.PropertyDefinition{}, binding.ID, true, fmt.Errorf("binding %q minimum: %w", binding.ID, mapErr)
			}
			minimum = &value
		}
		if definition.Max != nil {
			value, mapErr := mapNumber(*definition.Max)
			if mapErr != nil {
				return device.PropertyDefinition{}, binding.ID, true, fmt.Errorf("binding %q maximum: %w", binding.ID, mapErr)
			}
			maximum = &value
		}
		if minimum != nil && maximum != nil && *minimum > *maximum {
			minimum, maximum = maximum, minimum
		}
		result.Min, result.Max = minimum, maximum
		if definition.Step != nil {
			zero, zeroErr := mapNumber(0)
			stepped, stepErr := mapNumber(*definition.Step)
			if zeroErr != nil || stepErr != nil {
				if zeroErr != nil {
					stepErr = zeroErr
				}
				return device.PropertyDefinition{}, binding.ID, true, fmt.Errorf("binding %q step: %w", binding.ID, stepErr)
			}
			step := math.Abs(stepped - zero)
			result.Step = &step
		}
	}
	if profile.InputType == device.ValueTypeEnum && len(definition.Enum) > 0 {
		result.Enum = make([]string, 0, len(definition.Enum))
		for _, raw := range definition.Enum {
			input := device.EnumValue(raw)
			preview, previewErr := mapping.Preview(mapping.PreviewRequest{Profile: profile, Direction: mapping.DirectionForward, Value: &input})
			if previewErr != nil || preview.Value.String == nil {
				if previewErr == nil {
					previewErr = fmt.Errorf("enum definition transform did not produce a string")
				}
				return device.PropertyDefinition{}, binding.ID, true, fmt.Errorf("binding %q enum option %q: %w", binding.ID, raw, previewErr)
			}
			result.Enum = append(result.Enum, *preview.Value.String)
		}
	}
	for _, transform := range profile.Transforms {
		if transform.Type == mapping.TransformUnit {
			result.Unit = transform.ToUnit
		}
	}
	return result, binding.ID, true, nil
}

func (s *ProfileService) ResolvePropertyPath(providerID, deviceID, endpointID, capabilityID, propertyID string, direction mapping.Direction) (device.ParameterPath, string, bool, error) {
	binding, _, applied, err := s.resolveProviderBinding(providerID, deviceID, endpointID, capabilityID, propertyID, direction)
	if err != nil || !applied {
		return device.ParameterPath{EndpointID: endpointID, CapabilityID: capabilityID, PropertyID: propertyID}, binding.ID, applied, err
	}
	if direction == mapping.DirectionReverse {
		return binding.SourcePath(), binding.ID, true, nil
	}
	return binding.ModelPath(), binding.ID, true, nil
}

func (s *ProfileService) resolveProviderBinding(providerID, deviceID, endpointID, capabilityID, propertyID string, direction mapping.Direction) (mapping.Binding, mapping.Profile, bool, error) {
	probe := mapping.Binding{Stage: mapping.StageProvider, ProviderID: providerID, DeviceID: deviceID, EndpointID: endpointID, CapabilityID: capabilityID, PropertyID: propertyID}
	key := probe.Key()
	s.mu.RLock()
	var id string
	var ok bool
	if direction == mapping.DirectionReverse {
		probe.ModelEndpointID, probe.ModelCapabilityID, probe.ModelPropertyID = endpointID, capabilityID, propertyID
		id, ok = s.bindingsByModel[probe.ModelKey()]
	} else {
		id, ok = s.bindingsByKey[key]
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

func (s *ProfileService) validateBindingLocked(item mapping.Binding) error {
	if err := mapping.ValidateBinding(item); err != nil {
		return bindingValidationError(err)
	}
	modelParameter, modelExists := s.modelParameterLocked(item.DeviceType, item.ModelPath())
	if item.DeviceType != "" && !modelExists {
		return NewValidationError("invalid mapping binding", map[string]string{"modelPropertyId": "unified model property not found"})
	}
	var consumerProperty *mapping.ConsumerProperty
	if item.EffectiveStage() == mapping.StageConsumer {
		if candidate, found := mapping.FindConsumerProperty(item.ConsumerID, item.DeviceType, item.ConsumerProperty); found {
			consumerProperty = &candidate
		}
		if consumerProperty == nil {
			return NewValidationError("invalid mapping binding", map[string]string{"consumerProperty": "consumer property not found"})
		}
	}
	if item.ProfileID == "" {
		if consumerProperty != nil && modelParameter.Type != consumerProperty.Type {
			return NewValidationError("invalid mapping binding", map[string]string{"profileId": "a conversion profile is required because the model and consumer types differ"})
		}
		return nil
	}
	profile, ok := s.profileLocked(item.ProfileID)
	if !ok {
		return NewValidationError("invalid mapping binding", map[string]string{"profileId": "mapping profile not found"})
	}
	if modelExists {
		if item.EffectiveStage() == mapping.StageProvider && profile.OutputType != modelParameter.Type {
			return NewValidationError("invalid mapping binding", map[string]string{"profileId": "profile output type does not match the unified model property"})
		}
		if item.EffectiveStage() == mapping.StageConsumer && profile.InputType != modelParameter.Type {
			return NewValidationError("invalid mapping binding", map[string]string{"profileId": "profile input type does not match the unified model property"})
		}
	}
	if consumerProperty != nil && profile.OutputType != consumerProperty.Type {
		return NewValidationError("invalid mapping binding", map[string]string{"profileId": "profile output type does not match the consumer property"})
	}
	return validateRuntimeProfile(profile, item.EffectiveStage())
}

func (s *ProfileService) modelParameterLocked(deviceType device.Type, path device.ParameterPath) (device.ModelParameter, bool) {
	if deviceType == "" {
		return device.ModelParameter{}, false
	}
	contract, ok := device.ModelContractFor(deviceType)
	if ok {
		for _, parameter := range contract.Parameters {
			if parameter.Path == path {
				return parameter, true
			}
		}
	}
	if item, exists := s.customProperties[string(deviceType)+"\x00"+path.Key()]; exists {
		return mapping.CustomModelParameter(item), true
	}
	return device.ModelParameter{}, false
}

func validateRuntimeProfile(profile mapping.Profile, stages ...mapping.BindingStage) error {
	fields := make(map[string]string)
	stage := mapping.StageProvider
	if len(stages) > 0 {
		stage = stages[0]
	}
	if stage == mapping.StageProvider && profile.Kind == mapping.KindTarget {
		fields["profileId"] = "target profiles cannot be attached to provider properties"
	}
	if stage == mapping.StageConsumer && profile.Kind == mapping.KindProvider {
		fields["profileId"] = "provider profiles cannot be attached to consumer properties"
	}
	for _, transform := range profile.Transforms {
		if transform.Type == mapping.TransformClamp {
			fields["profileId"] = "runtime property bindings must be reversible; clamp is not reversible"
		}
	}
	if len(fields) > 0 {
		return NewValidationError("invalid mapping binding", fields)
	}
	return nil
}

func (s *ProfileService) profileLocked(id string) (mapping.Profile, bool) {
	if item, ok := s.builtIns[id]; ok {
		return cloneProfile(item), true
	}
	item, ok := s.profiles[id]
	return cloneProfile(item), ok
}

func bindingValidationError(err error) error {
	var validation *mapping.ValidationError
	if !errors.As(err, &validation) {
		return err
	}
	fields := make(map[string]string, len(validation.Fields))
	for key, message := range validation.Fields {
		fields[strings.TrimPrefix(key, "binding.")] = message
	}
	return NewValidationError("invalid mapping binding", fields)
}

func (s *ProfileService) notifyChanged(ctx context.Context) {
	s.mu.RLock()
	handler := s.changeHandler
	s.mu.RUnlock()
	if handler != nil {
		handler(ctx)
	}
}

func newBindingID() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate mapping binding id: %w", err)
	}
	return "binding-" + hex.EncodeToString(buffer), nil
}
