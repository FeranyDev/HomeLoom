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
	"time"

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
	item.ProfileID = s.profileIDForReferenceLocked(item.ProfileID)
	if err := s.validateBindingLocked(item); err != nil {
		s.mu.Unlock()
		return mapping.Binding{}, err
	}
	if _, exists := s.bindings[item.ID]; exists {
		s.mu.Unlock()
		return mapping.Binding{}, ErrBindingExists
	}
	if item.EffectiveStage() == mapping.StageConsumer && len(s.bindingsByKey[item.Key()]) > 0 {
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
	s.addBindingKeyLocked(item.Key(), item.ID)
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
	item.ProfileID = s.profileIDForReferenceLocked(item.ProfileID)
	current, exists := s.bindings[id]
	if !exists {
		s.mu.Unlock()
		return mapping.Binding{}, ErrBindingNotFound
	}
	if err := s.validateBindingLocked(item); err != nil {
		s.mu.Unlock()
		return mapping.Binding{}, err
	}
	if item.EffectiveStage() == mapping.StageConsumer && s.hasOtherBindingKeyLocked(item.Key(), id) {
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
	s.removeBindingKeyLocked(current.Key(), id)
	if current.EffectiveStage() == mapping.StageProvider {
		delete(s.bindingsByModel, current.ModelKey())
	}
	s.bindings[id] = item
	s.addBindingKeyLocked(item.Key(), id)
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
	if item.EffectiveStage() != mapping.StageProvider {
		item.ReadbackEnabled = false
		item.ReadbackDelaysMS = nil
		item.PresentationStep = nil
	}
	return item
}

// PropertyReadbackDelays resolves the source-property policy colocated with
// Provider mapping bindings. A Provider property may fan out to several model
// properties, so enabled schedules are merged and de-duplicated.
func (s *ProfileService) PropertyReadbackDelays(providerID, deviceID string, path device.ParameterPath) ([]time.Duration, bool) {
	key := (mapping.Binding{Stage: mapping.StageProvider, ProviderID: providerID, DeviceID: deviceID, EndpointID: path.EndpointID, CapabilityID: path.CapabilityID, PropertyID: path.PropertyID}).Key()
	s.mu.RLock()
	milliseconds := make(map[int]struct{})
	configured := false
	for _, id := range s.bindingsByKey[key] {
		binding := s.bindings[id]
		if !binding.Enabled || !binding.ReadbackEnabled {
			continue
		}
		configured = true
		for _, delay := range binding.ReadbackDelaysMS {
			milliseconds[delay] = struct{}{}
		}
	}
	s.mu.RUnlock()
	if !configured {
		return nil, false
	}
	result := make([]time.Duration, 0, len(milliseconds))
	for delay := range milliseconds {
		result = append(result, time.Duration(delay)*time.Millisecond)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, true
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
	s.removeBindingKeyLocked(item.Key(), id)
	if item.EffectiveStage() == mapping.StageProvider {
		delete(s.bindingsByModel, item.ModelKey())
	}
	s.mu.Unlock()
	s.notifyChanged(ctx)
	return nil
}

// PruneOrphanedBindings removes persisted routes whose source Provider was
// deleted or whose source Device was explicitly removed from an authoritative
// Provider configuration. Disabled bindings are removed as well: they must not
// reserve uniqueness slots after the device mapping itself no longer exists.
func (s *ProfileService) PruneOrphanedBindings(ctx context.Context, knownProviders map[string]struct{}, configuredDevices map[string]map[string]struct{}) (int, error) {
	return s.pruneBindings(ctx, func(item mapping.Binding) bool {
		_, providerExists := knownProviders[item.ProviderID]
		active, authoritative := configuredDevices[item.ProviderID]
		_, deviceExists := active[item.DeviceID]
		return !providerExists || (authoritative && !deviceExists)
	})
}

// PruneProviderBindings removes routes for devices no longer present in one
// authoritative Provider configuration. Passing an empty device set removes
// all routes owned by that Provider, as required when deleting the Provider.
func (s *ProfileService) PruneProviderBindings(ctx context.Context, providerID string, configuredDevices map[string]struct{}) (int, error) {
	return s.pruneBindings(ctx, func(item mapping.Binding) bool {
		if item.ProviderID != providerID {
			return false
		}
		_, exists := configuredDevices[item.DeviceID]
		return !exists
	})
}

func (s *ProfileService) pruneBindings(ctx context.Context, shouldDelete func(mapping.Binding) bool) (int, error) {
	s.mu.Lock()
	ids := make([]string, 0)
	for id, item := range s.bindings {
		if shouldDelete(item) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	deleted := 0
	for _, id := range ids {
		item := s.bindings[id]
		if err := s.store.DeleteMappingBinding(ctx, id); err != nil {
			s.mu.Unlock()
			if deleted > 0 {
				s.notifyChanged(ctx)
			}
			return deleted, err
		}
		delete(s.bindings, id)
		s.removeBindingKeyLocked(item.Key(), id)
		if item.EffectiveStage() == mapping.StageProvider {
			delete(s.bindingsByModel, item.ModelKey())
		}
		deleted++
	}
	s.mu.Unlock()
	if deleted > 0 {
		s.notifyChanged(ctx)
	}
	return deleted, nil
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
		return applyBindingPresentationStep(definition, binding), binding.ID, true, nil
	}
	result, err := transformProviderPropertyDefinition(binding, profile, definition)
	return applyBindingPresentationStep(result, binding), binding.ID, true, err
}

func applyBindingPresentationStep(definition device.PropertyDefinition, binding mapping.Binding) device.PropertyDefinition {
	if binding.PresentationStep == nil || definition.Step == nil || *definition.Step <= 0 {
		return definition
	}
	step := *binding.PresentationStep
	if !isWholeStepMultiple(step, *definition.Step) {
		return definition
	}
	definition.Step = &step
	return definition
}

func isWholeStepMultiple(value, sourceStep float64) bool {
	if value <= 0 || sourceStep <= 0 {
		return false
	}
	multiple := value / sourceStep
	return math.Abs(multiple-math.Round(multiple)) <= 1e-9*math.Max(1, math.Abs(multiple))
}

func transformProviderPropertyDefinition(binding mapping.Binding, profile mapping.Profile, definition device.PropertyDefinition) (device.PropertyDefinition, error) {
	result := definition
	result.Type = profile.OutputType
	if profile.OutputType != device.ValueTypeNumber && profile.OutputType != device.ValueTypeInt {
		result.Min, result.Max, result.Step, result.Unit = nil, nil, nil, ""
	}
	if profile.OutputType != device.ValueTypeEnum {
		result.Enum = nil
	}
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
	if (profile.InputType == device.ValueTypeNumber || profile.InputType == device.ValueTypeInt) && (profile.OutputType == device.ValueTypeNumber || profile.OutputType == device.ValueTypeInt) {
		var minimum, maximum *float64
		if definition.Min != nil {
			value, mapErr := mapNumber(*definition.Min)
			if mapErr != nil {
				return device.PropertyDefinition{}, fmt.Errorf("binding %q minimum: %w", binding.ID, mapErr)
			}
			minimum = &value
		}
		if definition.Max != nil {
			value, mapErr := mapNumber(*definition.Max)
			if mapErr != nil {
				return device.PropertyDefinition{}, fmt.Errorf("binding %q maximum: %w", binding.ID, mapErr)
			}
			maximum = &value
		}
		if minimum != nil && maximum != nil && *minimum > *maximum {
			minimum, maximum = maximum, minimum
		}
		result.Min, result.Max = minimum, maximum
		if definition.Step != nil && !hasNonlinearNumericTransform(profile) {
			zero, zeroErr := mapNumber(0)
			stepped, stepErr := mapNumber(*definition.Step)
			if zeroErr != nil || stepErr != nil {
				if zeroErr != nil {
					stepErr = zeroErr
				}
				return device.PropertyDefinition{}, fmt.Errorf("binding %q step: %w", binding.ID, stepErr)
			}
			step := math.Abs(stepped - zero)
			if step > 0 {
				result.Step = &step
			} else if profile.OutputType == device.ValueTypeInt {
				integerStep := 1.0
				result.Step = &integerStep
			}
		} else if definition.Step != nil {
			// Reciprocal conversions do not have a constant output increment.
			// Sampling f(step)-f(0) is both misleading and undefined for
			// Kelvin/mired, so let the unified-model definition supply its step.
			result.Step = nil
		}
	}
	if profile.InputType == device.ValueTypeEnum && profile.OutputType == device.ValueTypeEnum && len(definition.Enum) > 0 {
		result.Enum = make([]string, 0, len(definition.Enum))
		for _, raw := range definition.Enum {
			input := device.EnumValue(raw)
			preview, previewErr := mapping.Preview(mapping.PreviewRequest{Profile: profile, Direction: mapping.DirectionForward, Value: &input})
			if previewErr != nil || preview.Value.String == nil {
				if previewErr == nil {
					previewErr = fmt.Errorf("enum definition transform did not produce a string")
				}
				return device.PropertyDefinition{}, fmt.Errorf("binding %q enum option %q: %w", binding.ID, raw, previewErr)
			}
			result.Enum = append(result.Enum, *preview.Value.String)
		}
	}
	if profile.OutputType == device.ValueTypeEnum {
		values := append([]string(nil), definition.Enum...)
		for _, transform := range profile.Transforms {
			switch transform.Type {
			case mapping.TransformRangeEnum:
				values = values[:0]
				for _, band := range transform.Bands {
					values = append(values, band.Value)
				}
			case mapping.TransformBoolEnum:
				values = []string{transform.FalseValue, transform.TrueValue}
			case mapping.TransformEnum:
				mapped := make([]string, 0, len(values))
				for _, value := range values {
					if target, exists := transform.Values[value]; exists {
						mapped = append(mapped, target)
					}
				}
				values = mapped
			}
		}
		result.Enum = uniqueStrings(values)
	}
	for _, transform := range profile.Transforms {
		if transform.Type == mapping.TransformUnit {
			result.Unit = transform.ToUnit
		}
	}
	return result, nil
}

func hasNonlinearNumericTransform(profile mapping.Profile) bool {
	for _, transform := range profile.Transforms {
		if transform.Type == mapping.TransformReciprocal {
			return true
		}
		if transform.Type == mapping.TransformUnit &&
			((transform.FromUnit == "kelvin" && transform.ToUnit == "mired") ||
				(transform.FromUnit == "mired" && transform.ToUnit == "kelvin")) {
			return true
		}
	}
	return false
}

// ProjectProviderProperty returns the implicit identity route plus every
// enabled device-scoped manual route for one Provider source property. The
// DeviceService resolves target collisions after all source properties have
// been visited, with Explicit projections taking precedence over identity.
func (s *ProfileService) ProjectProviderProperty(providerID, deviceID, endpointID, capabilityID, propertyID string, definition device.PropertyDefinition, value device.PropertyValue) ([]ProviderPropertyProjection, error) {
	identity := ProviderPropertyProjection{
		Path:       device.ParameterPath{EndpointID: endpointID, CapabilityID: capabilityID, PropertyID: propertyID},
		Definition: definition,
		Value:      value,
	}
	key := (mapping.Binding{Stage: mapping.StageProvider, ProviderID: providerID, DeviceID: deviceID, EndpointID: endpointID, CapabilityID: capabilityID, PropertyID: propertyID}).Key()
	s.mu.RLock()
	bindings := make([]mapping.Binding, 0, len(s.bindingsByKey[key]))
	profiles := make(map[string]mapping.Profile)
	for _, id := range s.bindingsByKey[key] {
		binding := s.bindings[id]
		if binding.EffectiveStage() != mapping.StageProvider || !binding.Enabled {
			continue
		}
		bindings = append(bindings, binding)
		if binding.ProfileID != "" {
			if profile, exists := s.profileLocked(binding.ProfileID); exists {
				profiles[binding.ProfileID] = profile
			}
		}
	}
	s.mu.RUnlock()

	result := []ProviderPropertyProjection{identity}
	for _, binding := range bindings {
		projected := ProviderPropertyProjection{Path: binding.ModelPath(), Definition: definition, Value: value, BindingID: binding.ID, Explicit: true}
		if binding.ProfileID != "" {
			profile, exists := profiles[binding.ProfileID]
			if !exists {
				return nil, fmt.Errorf("mapping profile %q not found", binding.ProfileID)
			}
			mappedDefinition, err := transformProviderPropertyDefinition(binding, profile, definition)
			if err != nil {
				return nil, err
			}
			preview, err := mapping.Preview(mapping.PreviewRequest{Profile: profile, Direction: mapping.DirectionForward, Value: &value})
			if err != nil {
				return nil, fmt.Errorf("binding %q (%s): %w", binding.ID, mapping.BindingPath(binding), err)
			}
			projected.Definition, projected.Value = mappedDefinition, preview.Value
		}
		projected.Definition = applyBindingPresentationStep(projected.Definition, binding)
		result = append(result, projected)
	}
	return result, nil
}

// ProjectMissingProviderProperties applies an explicit Provider binding's
// default only when that binding's source path is absent from the Provider
// snapshot. A present source property always wins, including when its value is
// false or zero. Bindings without a Profile default remain absent rather than
// fabricating a value.
func (s *ProfileService) ProjectMissingProviderProperties(providerID, deviceID string, available []device.ParameterPath) ([]ProviderPropertyProjection, error) {
	availablePaths := make(map[string]struct{}, len(available))
	for _, path := range available {
		availablePaths[path.Key()] = struct{}{}
	}

	s.mu.RLock()
	bindings := make([]mapping.Binding, 0)
	profiles := make(map[string]mapping.Profile)
	for _, binding := range s.bindings {
		if binding.EffectiveStage() != mapping.StageProvider || !binding.Enabled || binding.ProviderID != providerID || binding.DeviceID != deviceID || binding.ProfileID == "" {
			continue
		}
		if _, exists := availablePaths[binding.SourcePath().Key()]; exists {
			continue
		}
		profile, exists := s.profileLocked(binding.ProfileID)
		if !exists {
			s.mu.RUnlock()
			return nil, fmt.Errorf("mapping profile %q not found", binding.ProfileID)
		}
		if profile.Default == nil {
			continue
		}
		bindings = append(bindings, binding)
		profiles[binding.ProfileID] = profile
	}
	s.mu.RUnlock()
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })

	result := make([]ProviderPropertyProjection, 0, len(bindings))
	for _, binding := range bindings {
		profile := profiles[binding.ProfileID]
		preview, err := mapping.Preview(mapping.PreviewRequest{Profile: profile, Direction: mapping.DirectionForward})
		if err != nil {
			return nil, fmt.Errorf("binding %q (%s) missing default: %w", binding.ID, mapping.BindingPath(binding), err)
		}
		path := binding.ModelPath()
		definition := device.PropertyDefinition{
			ID: path.PropertyID, Name: path.PropertyID, Type: profile.OutputType,
			Readable: true, Notifiable: true,
		}
		result = append(result, ProviderPropertyProjection{
			Path:       path,
			Definition: applyBindingPresentationStep(definition, binding),
			Value:      preview.Value, BindingID: binding.ID, Explicit: true,
		})
	}
	return result, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
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
		for _, candidate := range s.bindingsByKey[key] {
			if s.bindings[candidate].Enabled {
				id, ok = candidate, true
				break
			}
		}
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

func (s *ProfileService) addBindingKeyLocked(key, id string) {
	for _, current := range s.bindingsByKey[key] {
		if current == id {
			return
		}
	}
	s.bindingsByKey[key] = append(s.bindingsByKey[key], id)
	sort.Strings(s.bindingsByKey[key])
}

func (s *ProfileService) removeBindingKeyLocked(key, id string) {
	ids := s.bindingsByKey[key]
	for index, current := range ids {
		if current != id {
			continue
		}
		ids = append(ids[:index], ids[index+1:]...)
		break
	}
	if len(ids) == 0 {
		delete(s.bindingsByKey, key)
		return
	}
	s.bindingsByKey[key] = ids
}

func (s *ProfileService) hasOtherBindingKeyLocked(key, id string) bool {
	for _, current := range s.bindingsByKey[key] {
		if current != id {
			return true
		}
	}
	return false
}

func (s *ProfileService) validateBindingLocked(item mapping.Binding) error {
	if err := mapping.ValidateBinding(item); err != nil {
		return bindingValidationError(err)
	}
	modelParameter, modelExists := s.modelParameterLocked(item.DeviceType, item.ModelPath())
	if item.DeviceType != "" && !s.modelExistsLocked(item.DeviceType) {
		return NewValidationError("invalid mapping binding", map[string]string{"deviceType": "unified model not found"})
	}
	if item.DeviceType != "" && !modelExists {
		return NewValidationError("invalid mapping binding", map[string]string{"modelPropertyId": "unified model property not found"})
	}
	if item.PresentationStep != nil && modelExists && modelParameter.Type != device.ValueTypeNumber && modelParameter.Type != device.ValueTypeInt {
		return NewValidationError("invalid mapping binding", map[string]string{"presentationStep": "unified model property must be numeric"})
	}
	var consumerProperty *mapping.ConsumerProperty
	if item.EffectiveStage() == mapping.StageConsumer {
		if candidate, found := mapping.FindConsumerProperty(item.ConsumerID, item.EffectiveConsumerDeviceType(), item.ConsumerProperty); found {
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
	var parameter device.ModelParameter
	found := false
	contract, ok := device.ModelContractFor(deviceType)
	if ok {
		for _, candidate := range contract.Parameters {
			if candidate.Path == path {
				parameter, found = candidate, true
				break
			}
		}
	}
	if !found {
		if item, exists := s.customProperties[string(deviceType)+"\x00"+path.Key()]; exists {
			parameter, found = mapping.CustomModelParameter(item), true
		}
	}
	if !found {
		return device.ModelParameter{}, false
	}
	if override, exists := s.enumOverrides[string(deviceType)+"\x00"+path.Key()]; exists && parameter.Type == device.ValueTypeEnum {
		parameter.Enum = append([]string(nil), override.Enum...)
	}
	return parameter, true
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

// profileIDForReferenceLocked keeps historic Binding payloads working during
// the identifier-to-UUIDv7 transition. New clients always submit the UUIDv7
// returned by the Profile API; legacy readable identifiers resolve once here
// and are persisted back as the opaque ID.
func (s *ProfileService) profileIDForReferenceLocked(reference string) string {
	if reference == "" {
		return ""
	}
	if _, exists := s.profileLocked(reference); exists {
		return reference
	}
	if id, exists := s.profileIDsByIdentifier[reference]; exists {
		return id
	}
	return reference
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
