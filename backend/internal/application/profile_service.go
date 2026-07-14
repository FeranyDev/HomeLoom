package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/mapping"
)

var (
	ErrProfileNotFound = errors.New("mapping profile not found")
	ErrProfileExists   = errors.New("mapping profile already exists")
	ErrProfileBuiltIn  = errors.New("built-in mapping profile is read-only")
	ErrProfileInUse    = errors.New("mapping profile is used by a binding")
	ErrBindingNotFound = errors.New("mapping binding not found")
	ErrBindingExists   = errors.New("mapping binding already exists for this property")
)

type ProfileStore interface {
	ListMappingProfiles(context.Context) ([]mapping.Profile, error)
	SaveMappingProfile(context.Context, mapping.Profile) error
	SaveMappingProfiles(context.Context, []mapping.Profile) error
	DeleteMappingProfile(context.Context, string) error
	ListMappingBindings(context.Context) ([]mapping.Binding, error)
	SaveMappingBinding(context.Context, mapping.Binding) error
	DeleteMappingBinding(context.Context, string) error
	ListCustomModelProperties(context.Context) ([]mapping.CustomModelProperty, error)
	SaveCustomModelProperty(context.Context, mapping.CustomModelProperty) error
	DeleteCustomModelProperty(context.Context, string) error
}

type ProfileInfo struct {
	mapping.Profile
	BuiltIn bool `json:"builtIn"`
}

type ProfileService struct {
	mu               sync.RWMutex
	profiles         map[string]mapping.Profile
	builtIns         map[string]mapping.Profile
	bindings         map[string]mapping.Binding
	bindingsByKey    map[string]string
	bindingsByModel  map[string]string
	customProperties map[string]mapping.CustomModelProperty
	store            ProfileStore
	changeHandler    func(context.Context)
}

func NewProfileService(ctx context.Context, store ProfileStore) (*ProfileService, error) {
	service := &ProfileService{profiles: make(map[string]mapping.Profile), builtIns: make(map[string]mapping.Profile), bindings: make(map[string]mapping.Binding), bindingsByKey: make(map[string]string), bindingsByModel: make(map[string]string), customProperties: make(map[string]mapping.CustomModelProperty), store: store}
	for _, item := range BuiltInProfiles() {
		if err := mapping.Validate(item); err != nil {
			return nil, fmt.Errorf("validate built-in mapping profile %q: %w", item.ID, err)
		}
		service.builtIns[item.ID] = cloneProfile(item)
	}
	items, err := store.ListMappingProfiles(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if _, reserved := service.builtIns[item.ID]; reserved {
			return nil, fmt.Errorf("stored mapping profile %q conflicts with a built-in profile", item.ID)
		}
		if err := mapping.Validate(item); err != nil {
			return nil, fmt.Errorf("validate stored mapping profile %q: %w", item.ID, err)
		}
		service.profiles[item.ID] = cloneProfile(item)
	}
	customProperties, err := store.ListCustomModelProperties(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range customProperties {
		if err := mapping.ValidateCustomModelProperty(item); err != nil {
			return nil, fmt.Errorf("validate stored custom model property %q: %w", item.ID, err)
		}
		if _, duplicate := service.customProperties[item.Key()]; duplicate {
			return nil, fmt.Errorf("stored custom model property %q duplicates path %s", item.ID, item.Path())
		}
		service.customProperties[item.Key()] = item
	}
	bindings, err := store.ListMappingBindings(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range bindings {
		if err := service.validateBindingLocked(item); err != nil {
			return nil, fmt.Errorf("validate stored mapping binding %q: %w", item.ID, err)
		}
		if _, duplicate := service.bindingsByKey[item.Key()]; duplicate {
			return nil, fmt.Errorf("stored mapping binding %q duplicates property path %s", item.ID, mapping.BindingPath(item))
		}
		service.bindings[item.ID] = item
		service.bindingsByKey[item.Key()] = item.ID
		if item.EffectiveStage() == mapping.StageProvider {
			if _, duplicate := service.bindingsByModel[item.ModelKey()]; duplicate {
				return nil, fmt.Errorf("stored mapping binding %q duplicates unified model path %s", item.ID, item.ModelPath())
			}
			service.bindingsByModel[item.ModelKey()] = item.ID
		}
	}
	return service, nil
}

func BuiltInProfiles() []mapping.Profile {
	factor100, factor001 := 100.0, 0.01
	return []mapping.Profile{
		{SchemaVersion: 1, ID: "builtin-active-low", Version: 1, Kind: mapping.KindProvider, InputType: device.ValueTypeBool, OutputType: device.ValueTypeBool, Transforms: []mapping.Transform{{Type: mapping.TransformInvert}}},
		{SchemaVersion: 1, ID: "builtin-celsius-fahrenheit", Version: 1, Kind: mapping.KindTarget, InputType: device.ValueTypeNumber, OutputType: device.ValueTypeNumber, Transforms: []mapping.Transform{{Type: mapping.TransformUnit, FromUnit: "celsius", ToUnit: "fahrenheit"}}},
		{SchemaVersion: 1, ID: "builtin-ratio-percent", Version: 1, Kind: mapping.KindCapability, InputType: device.ValueTypeNumber, OutputType: device.ValueTypeNumber, Transforms: []mapping.Transform{{Type: mapping.TransformScale, Factor: &factor100}}},
		{SchemaVersion: 1, ID: "builtin-percent-ratio", Version: 1, Kind: mapping.KindCapability, InputType: device.ValueTypeNumber, OutputType: device.ValueTypeNumber, Transforms: []mapping.Transform{{Type: mapping.TransformScale, Factor: &factor001}}},
	}
}

func (s *ProfileService) List() []ProfileInfo {
	s.mu.RLock()
	result := make([]ProfileInfo, 0, len(s.builtIns)+len(s.profiles))
	for _, item := range s.builtIns {
		result = append(result, ProfileInfo{Profile: cloneProfile(item), BuiltIn: true})
	}
	for _, item := range s.profiles {
		result = append(result, ProfileInfo{Profile: cloneProfile(item)})
	}
	s.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind == result[j].Kind {
			return result[i].ID < result[j].ID
		}
		return result[i].Kind < result[j].Kind
	})
	return result
}

func (s *ProfileService) Export() []mapping.Profile {
	s.mu.RLock()
	result := make([]mapping.Profile, 0, len(s.profiles))
	for _, item := range s.profiles {
		result = append(result, cloneProfile(item))
	}
	s.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (s *ProfileService) Get(id string) (ProfileInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if item, ok := s.builtIns[id]; ok {
		return ProfileInfo{Profile: cloneProfile(item), BuiltIn: true}, nil
	}
	if item, ok := s.profiles[id]; ok {
		return ProfileInfo{Profile: cloneProfile(item)}, nil
	}
	return ProfileInfo{}, ErrProfileNotFound
}

func (s *ProfileService) Create(ctx context.Context, item mapping.Profile) (ProfileInfo, error) {
	if err := validateProfile(item); err != nil {
		return ProfileInfo{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.builtIns[item.ID]; exists {
		return ProfileInfo{}, ErrProfileBuiltIn
	}
	if _, exists := s.profiles[item.ID]; exists {
		return ProfileInfo{}, ErrProfileExists
	}
	if err := s.store.SaveMappingProfile(ctx, item); err != nil {
		return ProfileInfo{}, err
	}
	s.profiles[item.ID] = cloneProfile(item)
	return ProfileInfo{Profile: cloneProfile(item)}, nil
}

func (s *ProfileService) Update(ctx context.Context, id string, item mapping.Profile) (ProfileInfo, error) {
	item.ID = id
	if err := validateProfile(item); err != nil {
		return ProfileInfo{}, err
	}
	s.mu.Lock()
	if _, exists := s.builtIns[id]; exists {
		s.mu.Unlock()
		return ProfileInfo{}, ErrProfileBuiltIn
	}
	current, exists := s.profiles[id]
	if !exists {
		s.mu.Unlock()
		return ProfileInfo{}, ErrProfileNotFound
	}
	if item.Version <= current.Version {
		s.mu.Unlock()
		return ProfileInfo{}, NewValidationError("invalid mapping profile", map[string]string{"version": fmt.Sprintf("must be greater than current version %d", current.Version)})
	}
	if err := validateRuntimeProfileUpdate(item, s.bindings, id); err != nil {
		s.mu.Unlock()
		return ProfileInfo{}, err
	}
	if err := s.store.SaveMappingProfile(ctx, item); err != nil {
		s.mu.Unlock()
		return ProfileInfo{}, err
	}
	usedByBinding := profileUsedByBinding(s.bindings, id)
	s.profiles[id] = cloneProfile(item)
	s.mu.Unlock()
	if usedByBinding {
		s.notifyChanged(ctx)
	}
	return ProfileInfo{Profile: cloneProfile(item)}, nil
}

func (s *ProfileService) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.builtIns[id]; exists {
		return ErrProfileBuiltIn
	}
	if _, exists := s.profiles[id]; !exists {
		return ErrProfileNotFound
	}
	for _, binding := range s.bindings {
		if binding.ProfileID == id {
			return ErrProfileInUse
		}
	}
	if err := s.store.DeleteMappingProfile(ctx, id); err != nil {
		return err
	}
	delete(s.profiles, id)
	return nil
}

func (s *ProfileService) Import(ctx context.Context, items []mapping.Profile) ([]ProfileInfo, error) {
	if len(items) == 0 {
		return nil, NewValidationError("invalid mapping profile import", map[string]string{"profiles": "must not be empty"})
	}
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		if err := mapping.Validate(item); err != nil {
			return nil, profileValidationError(fmt.Sprintf("profiles.%d", index), err)
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return nil, NewValidationError("invalid mapping profile import", map[string]string{fmt.Sprintf("profiles.%d.id", index): "duplicate profile id"})
		}
		seen[item.ID] = struct{}{}
	}
	s.mu.Lock()
	for index, item := range items {
		if _, exists := s.builtIns[item.ID]; exists {
			s.mu.Unlock()
			return nil, NewValidationError("invalid mapping profile import", map[string]string{fmt.Sprintf("profiles.%d.id", index): "conflicts with a built-in profile"})
		}
		if current, exists := s.profiles[item.ID]; exists && item.Version <= current.Version {
			s.mu.Unlock()
			return nil, NewValidationError("invalid mapping profile import", map[string]string{fmt.Sprintf("profiles.%d.version", index): fmt.Sprintf("must be greater than current version %d", current.Version)})
		}
		if err := validateRuntimeProfileUpdate(item, s.bindings, item.ID); err != nil {
			s.mu.Unlock()
			return nil, err
		}
	}
	if err := s.store.SaveMappingProfiles(ctx, items); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	result := make([]ProfileInfo, 0, len(items))
	refreshRuntime := false
	for _, item := range items {
		refreshRuntime = refreshRuntime || profileUsedByBinding(s.bindings, item.ID)
		s.profiles[item.ID] = cloneProfile(item)
		result = append(result, ProfileInfo{Profile: cloneProfile(item)})
	}
	s.mu.Unlock()
	if refreshRuntime {
		s.notifyChanged(ctx)
	}
	return result, nil
}

func validateRuntimeProfileUpdate(item mapping.Profile, bindings map[string]mapping.Binding, id string) error {
	for _, binding := range bindings {
		if binding.ProfileID == id {
			if err := validateRuntimeProfile(item, binding.EffectiveStage()); err != nil {
				return err
			}
		}
	}
	return nil
}

func profileUsedByBinding(bindings map[string]mapping.Binding, id string) bool {
	for _, binding := range bindings {
		if binding.ProfileID == id && binding.Enabled {
			return true
		}
	}
	return false
}

func validateProfile(item mapping.Profile) error {
	if err := mapping.Validate(item); err != nil {
		return profileValidationError("", err)
	}
	return nil
}

func profileValidationError(prefix string, err error) error {
	var validation *mapping.ValidationError
	if !errors.As(err, &validation) {
		return err
	}
	fields := make(map[string]string, len(validation.Fields))
	for key, message := range validation.Fields {
		if prefix != "" {
			key = prefix + "." + strings.TrimPrefix(key, "profile.")
		}
		fields[key] = message
	}
	return NewValidationError("invalid mapping profile", fields)
}

func cloneProfile(item mapping.Profile) mapping.Profile {
	result := item
	result.Transforms = make([]mapping.Transform, len(item.Transforms))
	for index, transform := range item.Transforms {
		result.Transforms[index] = transform
		if transform.Factor != nil {
			value := *transform.Factor
			result.Transforms[index].Factor = &value
		}
		if transform.Offset != nil {
			value := *transform.Offset
			result.Transforms[index].Offset = &value
		}
		if transform.Min != nil {
			value := *transform.Min
			result.Transforms[index].Min = &value
		}
		if transform.Max != nil {
			value := *transform.Max
			result.Transforms[index].Max = &value
		}
		if transform.Values != nil {
			result.Transforms[index].Values = make(map[string]string, len(transform.Values))
			for key, value := range transform.Values {
				result.Transforms[index].Values[key] = value
			}
		}
	}
	if item.Default != nil {
		value := clonePropertyValue(*item.Default)
		result.Default = &value
	}
	return result
}

func clonePropertyValue(value device.PropertyValue) device.PropertyValue {
	switch value.Type {
	case device.ValueTypeBool:
		if value.Bool != nil {
			return device.BoolValue(*value.Bool)
		}
	case device.ValueTypeInt:
		if value.Int != nil {
			return device.IntValue(*value.Int)
		}
	case device.ValueTypeNumber:
		if value.Number != nil {
			return device.NumberValue(*value.Number)
		}
	case device.ValueTypeString:
		if value.String != nil {
			return device.StringValue(*value.String)
		}
	case device.ValueTypeEnum:
		if value.String != nil {
			return device.EnumValue(*value.String)
		}
	}
	return value
}
