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
	if err := s.store.SaveMappingBinding(ctx, item); err != nil {
		s.mu.Unlock()
		return mapping.Binding{}, err
	}
	s.bindings[item.ID] = item
	s.bindingsByKey[item.Key()] = item.ID
	s.mu.Unlock()
	s.notifyChanged(ctx)
	return item, nil
}

func (s *ProfileService) UpdateBinding(ctx context.Context, id string, item mapping.Binding) (mapping.Binding, error) {
	item.ID = id
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
	if err := s.store.SaveMappingBinding(ctx, item); err != nil {
		s.mu.Unlock()
		return mapping.Binding{}, err
	}
	delete(s.bindingsByKey, current.Key())
	s.bindings[id] = item
	s.bindingsByKey[item.Key()] = id
	s.mu.Unlock()
	s.notifyChanged(ctx)
	return item, nil
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
	s.mu.Unlock()
	s.notifyChanged(ctx)
	return nil
}

func (s *ProfileService) TransformProperty(providerID, deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue, direction mapping.Direction) (device.PropertyValue, string, bool, error) {
	binding, profile, applied, err := s.resolveBinding(providerID, deviceID, endpointID, capabilityID, propertyID)
	if err != nil || !applied {
		return value, binding.ID, applied, err
	}
	result, err := mapping.Preview(mapping.PreviewRequest{Profile: profile, Direction: direction, Value: &value})
	if err != nil {
		return device.PropertyValue{}, binding.ID, true, fmt.Errorf("binding %q (%s): %w", binding.ID, mapping.BindingPath(binding), err)
	}
	return result.Value, binding.ID, true, nil
}

func (s *ProfileService) TransformPropertyDefinition(providerID, deviceID, endpointID, capabilityID, propertyID string, definition device.PropertyDefinition) (device.PropertyDefinition, string, bool, error) {
	binding, profile, applied, err := s.resolveBinding(providerID, deviceID, endpointID, capabilityID, propertyID)
	if err != nil || !applied {
		return definition, binding.ID, applied, err
	}
	result := definition
	result.Type = profile.OutputType
	mapNumber := func(value float64) (float64, error) {
		input := device.NumberValue(value)
		preview, previewErr := mapping.Preview(mapping.PreviewRequest{Profile: profile, Direction: mapping.DirectionForward, Value: &input})
		if previewErr != nil || preview.Value.Number == nil {
			if previewErr == nil {
				previewErr = fmt.Errorf("numeric definition transform did not produce a number")
			}
			return 0, previewErr
		}
		return *preview.Value.Number, nil
	}
	if profile.InputType == device.ValueTypeNumber {
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

func (s *ProfileService) resolveBinding(providerID, deviceID, endpointID, capabilityID, propertyID string) (mapping.Binding, mapping.Profile, bool, error) {
	key := (mapping.Binding{ProviderID: providerID, DeviceID: deviceID, EndpointID: endpointID, CapabilityID: capabilityID, PropertyID: propertyID}).Key()
	s.mu.RLock()
	id, ok := s.bindingsByKey[key]
	if !ok || !s.bindings[id].Enabled {
		s.mu.RUnlock()
		return mapping.Binding{}, mapping.Profile{}, false, nil
	}
	binding := s.bindings[id]
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
	profile, ok := s.profileLocked(item.ProfileID)
	if !ok {
		return NewValidationError("invalid mapping binding", map[string]string{"profileId": "mapping profile not found"})
	}
	return validateRuntimeProfile(profile)
}

func validateRuntimeProfile(profile mapping.Profile) error {
	fields := make(map[string]string)
	if profile.Kind == mapping.KindTarget {
		fields["profileId"] = "target profiles cannot be attached to provider properties"
	}
	if profile.InputType != profile.OutputType {
		fields["profileId"] = "runtime property bindings currently require matching input and output types"
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
