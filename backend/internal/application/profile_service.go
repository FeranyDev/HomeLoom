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
	MigrateMappingProfileIdentities(context.Context, []mapping.ProfileIdentityMigration, map[string]string) error
	DeleteMappingProfile(context.Context, string) error
	ListMappingBindings(context.Context) ([]mapping.Binding, error)
	SaveMappingBinding(context.Context, mapping.Binding) error
	DeleteMappingBinding(context.Context, string) error
	ListCustomModelProperties(context.Context) ([]mapping.CustomModelProperty, error)
	SaveCustomModelProperty(context.Context, mapping.CustomModelProperty) error
	DeleteCustomModelProperty(context.Context, string) error
	ListModelEnumOverrides(context.Context) ([]mapping.ModelEnumOverride, error)
	SaveModelEnumOverride(context.Context, mapping.ModelEnumOverride) error
	DeleteModelEnumOverride(context.Context, string) error
	ListCustomModels(context.Context) ([]mapping.CustomModel, error)
	SaveCustomModel(context.Context, mapping.CustomModel) error
	DeleteCustomModel(context.Context, string) error
}

type ProfileInfo struct {
	mapping.Profile
	BuiltIn bool `json:"builtIn"`
}

type ProfileService struct {
	mu                     sync.RWMutex
	profiles               map[string]mapping.Profile
	builtIns               map[string]mapping.Profile
	profileIDsByIdentifier map[string]string
	bindings               map[string]mapping.Binding
	bindingsByKey          map[string][]string
	bindingsByModel        map[string]string
	customProperties       map[string]mapping.CustomModelProperty
	enumOverrides          map[string]mapping.ModelEnumOverride
	customModels           map[device.Type]mapping.CustomModel
	store                  ProfileStore
	changeHandler          func(context.Context)
}

func NewProfileService(ctx context.Context, store ProfileStore) (*ProfileService, error) {
	service := &ProfileService{profiles: make(map[string]mapping.Profile), builtIns: make(map[string]mapping.Profile), profileIDsByIdentifier: make(map[string]string), bindings: make(map[string]mapping.Binding), bindingsByKey: make(map[string][]string), bindingsByModel: make(map[string]string), customProperties: make(map[string]mapping.CustomModelProperty), enumOverrides: make(map[string]mapping.ModelEnumOverride), customModels: make(map[device.Type]mapping.CustomModel), store: store}
	for _, item := range BuiltInProfiles() {
		if err := validateProfile(item); err != nil {
			return nil, fmt.Errorf("validate built-in mapping profile %q: %w", item.ID, err)
		}
		if existing, duplicate := service.profileIDsByIdentifier[item.Identifier]; duplicate {
			return nil, fmt.Errorf("built-in mapping profile identifiers %q and %q conflict", existing, item.ID)
		}
		service.builtIns[item.ID] = cloneProfile(item)
		service.profileIDsByIdentifier[item.Identifier] = item.ID
	}
	items, err := store.ListMappingProfiles(ctx)
	if err != nil {
		return nil, err
	}
	migratedItems, migrations, bindingProfileIDs, err := service.migrateStoredProfileIdentities(items)
	if err != nil {
		return nil, err
	}
	if err := store.MigrateMappingProfileIdentities(ctx, migrations, bindingProfileIDs); err != nil {
		return nil, err
	}
	for _, item := range migratedItems {
		if _, reserved := service.builtIns[item.ID]; reserved {
			return nil, fmt.Errorf("stored mapping profile %q conflicts with a built-in profile", item.ID)
		}
		if err := validateProfile(item); err != nil {
			return nil, fmt.Errorf("validate stored mapping profile %q: %w", item.ID, err)
		}
		if existing, duplicate := service.profileIDsByIdentifier[item.Identifier]; duplicate {
			return nil, fmt.Errorf("stored mapping profile %q uses identifier %q already owned by %q", item.ID, item.Identifier, existing)
		}
		service.profiles[item.ID] = cloneProfile(item)
		service.profileIDsByIdentifier[item.Identifier] = item.ID
	}
	customModels, err := store.ListCustomModels(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range customModels {
		if err := mapping.ValidateCustomModel(item); err != nil {
			return nil, fmt.Errorf("validate stored custom unified model %q: %w", item.DeviceType, err)
		}
		if _, builtIn := device.ModelContractFor(item.DeviceType); builtIn {
			return nil, fmt.Errorf("stored custom unified model %q conflicts with a built-in model", item.DeviceType)
		}
		service.customModels[item.DeviceType] = item
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
		if _, builtIn := device.ModelContractFor(item.DeviceType); !builtIn {
			if _, custom := service.customModels[item.DeviceType]; !custom {
				return nil, fmt.Errorf("stored custom model property %q references unknown model %q", item.ID, item.DeviceType)
			}
		}
		service.customProperties[item.Key()] = item
	}

	enumOverrides, err := store.ListModelEnumOverrides(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range enumOverrides {
		if err := mapping.ValidateModelEnumOverride(item); err != nil {
			return nil, fmt.Errorf("validate stored model enum override %q: %w", item.ID, err)
		}
		if _, duplicate := service.enumOverrides[item.Key()]; duplicate {
			return nil, fmt.Errorf("stored model enum override %q duplicates path %s", item.ID, item.Path())
		}
		if _, builtIn := device.ModelContractFor(item.DeviceType); !builtIn {
			if _, custom := service.customModels[item.DeviceType]; !custom {
				return nil, fmt.Errorf("stored model enum override %q references unknown model %q", item.ID, item.DeviceType)
			}
		}
		service.enumOverrides[item.Key()] = item
	}
	bindings, err := store.ListMappingBindings(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range bindings {
		item.ProfileID = service.profileIDForReferenceLocked(item.ProfileID)
		if err := service.validateBindingLocked(item); err != nil {
			return nil, fmt.Errorf("validate stored mapping binding %q: %w", item.ID, err)
		}
		if item.EffectiveStage() == mapping.StageConsumer && len(service.bindingsByKey[item.Key()]) > 0 {
			return nil, fmt.Errorf("stored mapping binding %q duplicates property path %s", item.ID, mapping.BindingPath(item))
		}
		service.bindings[item.ID] = item
		service.addBindingKeyLocked(item.Key(), item.ID)
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
	builtIn := func(identifier string, kind mapping.ProfileKind, inputType, outputType device.ValueType, transforms []mapping.Transform) mapping.Profile {
		return mapping.Profile{SchemaVersion: 1, ID: mapping.BuiltInProfileID(identifier), Identifier: identifier, Version: 1, Kind: kind, InputType: inputType, OutputType: outputType, Transforms: transforms}
	}
	profiles := []mapping.Profile{
		builtIn("builtin-active-low", mapping.KindProvider, device.ValueTypeBool, device.ValueTypeBool, []mapping.Transform{{Type: mapping.TransformInvert}}),
		builtIn("builtin-celsius-fahrenheit", mapping.KindTarget, device.ValueTypeNumber, device.ValueTypeNumber, []mapping.Transform{{Type: mapping.TransformUnit, FromUnit: "celsius", ToUnit: "fahrenheit"}}),
		builtIn("builtin-ratio-percent", mapping.KindCapability, device.ValueTypeNumber, device.ValueTypeNumber, []mapping.Transform{{Type: mapping.TransformScale, Factor: &factor100}}),
		builtIn("builtin-percent-ratio", mapping.KindCapability, device.ValueTypeNumber, device.ValueTypeNumber, []mapping.Transform{{Type: mapping.TransformScale, Factor: &factor001}}),
	}
	return append(profiles, mapping.AutoCapabilityProfiles()...)
}

// migrateStoredProfileIdentities upgrades Profile documents created before
// UUIDv7 IDs and brings their Binding references along in one database
// transaction. An old Profile ID becomes its editable identifier; a prior
// UUIDv7 document without identifier receives a stable readable fallback.
func (s *ProfileService) migrateStoredProfileIdentities(items []mapping.Profile) ([]mapping.Profile, []mapping.ProfileIdentityMigration, map[string]string, error) {
	result := make([]mapping.Profile, 0, len(items))
	migrations := make([]mapping.ProfileIdentityMigration, 0, len(items))
	bindingProfileIDs := make(map[string]string)
	for identifier, id := range s.profileIDsByIdentifier {
		bindingProfileIDs[identifier] = id
	}
	identifiers := make(map[string]string, len(s.profileIDsByIdentifier)+len(items))
	for identifier, id := range s.profileIDsByIdentifier {
		identifiers[identifier] = id
	}
	for _, item := range items {
		previousID := item.ID
		previousIdentifier := item.Identifier
		if item.Identifier == "" {
			if mapping.IsUUIDv7(item.ID) {
				item.Identifier = "profile-" + strings.ReplaceAll(item.ID, "-", "")
			} else {
				item.Identifier = item.ID
			}
		}
		if !mapping.IsUUIDv7(item.ID) {
			id, err := mapping.NewUUIDv7()
			if err != nil {
				return nil, nil, nil, err
			}
			item.ID = id
			bindingProfileIDs[previousID] = item.ID
		}
		bindingProfileIDs[item.Identifier] = item.ID
		if existing, duplicate := identifiers[item.Identifier]; duplicate {
			return nil, nil, nil, fmt.Errorf("stored mapping profile %q uses identifier %q already owned by %q", previousID, item.Identifier, existing)
		}
		identifiers[item.Identifier] = item.ID
		if item.ID != previousID || item.Identifier != previousIdentifier {
			migrations = append(migrations, mapping.ProfileIdentityMigration{PreviousID: previousID, Profile: item})
		}
		result = append(result, item)
	}
	return result, deduplicateProfileMigrations(migrations), bindingProfileIDs, nil
}

func deduplicateProfileMigrations(items []mapping.ProfileIdentityMigration) []mapping.ProfileIdentityMigration {
	seen := make(map[string]int, len(items))
	result := make([]mapping.ProfileIdentityMigration, 0, len(items))
	for _, item := range items {
		if index, exists := seen[item.PreviousID]; exists {
			result[index] = item
			continue
		}
		seen[item.PreviousID] = len(result)
		result = append(result, item)
	}
	return result
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
			return result[i].Identifier < result[j].Identifier
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
	sort.Slice(result, func(i, j int) bool { return result[i].Identifier < result[j].Identifier })
	return result
}

func (s *ProfileService) Get(id string) (ProfileInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id = s.profileIDForReferenceLocked(id)
	if item, ok := s.builtIns[id]; ok {
		return ProfileInfo{Profile: cloneProfile(item), BuiltIn: true}, nil
	}
	if item, ok := s.profiles[id]; ok {
		return ProfileInfo{Profile: cloneProfile(item)}, nil
	}
	return ProfileInfo{}, ErrProfileNotFound
}

func (s *ProfileService) Create(ctx context.Context, item mapping.Profile) (ProfileInfo, error) {
	var err error
	item, err = newProfileIdentity(item)
	if err != nil {
		return ProfileInfo{}, err
	}
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
	if existing, exists := s.profileIDsByIdentifier[item.Identifier]; exists {
		return ProfileInfo{}, NewValidationError("invalid mapping profile", map[string]string{"identifier": fmt.Sprintf("is already used by profile %q", existing)})
	}
	if err := s.store.SaveMappingProfile(ctx, item); err != nil {
		return ProfileInfo{}, err
	}
	s.profiles[item.ID] = cloneProfile(item)
	s.profileIDsByIdentifier[item.Identifier] = item.ID
	return ProfileInfo{Profile: cloneProfile(item)}, nil
}

func (s *ProfileService) Update(ctx context.Context, id string, item mapping.Profile) (ProfileInfo, error) {
	s.mu.Lock()
	id = s.profileIDForReferenceLocked(id)
	if _, exists := s.builtIns[id]; exists {
		s.mu.Unlock()
		return ProfileInfo{}, ErrProfileBuiltIn
	}
	current, exists := s.profiles[id]
	if !exists {
		s.mu.Unlock()
		return ProfileInfo{}, ErrProfileNotFound
	}
	item.ID = id
	if item.Identifier == "" {
		item.Identifier = current.Identifier
	}
	if err := validateProfile(item); err != nil {
		s.mu.Unlock()
		return ProfileInfo{}, err
	}
	if item.Version <= current.Version {
		s.mu.Unlock()
		return ProfileInfo{}, NewValidationError("invalid mapping profile", map[string]string{"version": fmt.Sprintf("must be greater than current version %d", current.Version)})
	}
	if owner, exists := s.profileIDsByIdentifier[item.Identifier]; exists && owner != id {
		s.mu.Unlock()
		return ProfileInfo{}, NewValidationError("invalid mapping profile", map[string]string{"identifier": fmt.Sprintf("is already used by profile %q", owner)})
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
	delete(s.profileIDsByIdentifier, current.Identifier)
	s.profiles[id] = cloneProfile(item)
	s.profileIDsByIdentifier[item.Identifier] = id
	s.mu.Unlock()
	if usedByBinding {
		s.notifyChanged(ctx)
	}
	return ProfileInfo{Profile: cloneProfile(item)}, nil
}

func (s *ProfileService) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = s.profileIDForReferenceLocked(id)
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
	delete(s.profileIDsByIdentifier, s.profiles[id].Identifier)
	delete(s.profiles, id)
	return nil
}

func (s *ProfileService) Import(ctx context.Context, items []mapping.Profile) ([]ProfileInfo, error) {
	if len(items) == 0 {
		return nil, NewValidationError("invalid mapping profile import", map[string]string{"profiles": "must not be empty"})
	}
	seen := make(map[string]struct{}, len(items))
	seenIdentifiers := make(map[string]struct{}, len(items))
	prepared := make([]mapping.Profile, 0, len(items))
	for index, item := range items {
		var err error
		item, err = importedProfileIdentity(item)
		if err != nil {
			return nil, profileValidationError(fmt.Sprintf("profiles.%d", index), err)
		}
		if err := validateProfile(item); err != nil {
			return nil, profileValidationError(fmt.Sprintf("profiles.%d", index), err)
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return nil, NewValidationError("invalid mapping profile import", map[string]string{fmt.Sprintf("profiles.%d.id", index): "duplicate profile id"})
		}
		seen[item.ID] = struct{}{}
		if _, duplicate := seenIdentifiers[item.Identifier]; duplicate {
			return nil, NewValidationError("invalid mapping profile import", map[string]string{fmt.Sprintf("profiles.%d.identifier", index): "duplicate profile identifier"})
		}
		seenIdentifiers[item.Identifier] = struct{}{}
		prepared = append(prepared, item)
	}
	s.mu.Lock()
	for index, item := range prepared {
		if _, exists := s.builtIns[item.ID]; exists {
			s.mu.Unlock()
			return nil, NewValidationError("invalid mapping profile import", map[string]string{fmt.Sprintf("profiles.%d.id", index): "conflicts with a built-in profile"})
		}
		if current, exists := s.profiles[item.ID]; exists && item.Version <= current.Version {
			s.mu.Unlock()
			return nil, NewValidationError("invalid mapping profile import", map[string]string{fmt.Sprintf("profiles.%d.version", index): fmt.Sprintf("must be greater than current version %d", current.Version)})
		}
		if owner, exists := s.profileIDsByIdentifier[item.Identifier]; exists && owner != item.ID {
			s.mu.Unlock()
			return nil, NewValidationError("invalid mapping profile import", map[string]string{fmt.Sprintf("profiles.%d.identifier", index): fmt.Sprintf("is already used by profile %q", owner)})
		}
		if err := validateRuntimeProfileUpdate(item, s.bindings, item.ID); err != nil {
			s.mu.Unlock()
			return nil, err
		}
	}
	if err := s.store.SaveMappingProfiles(ctx, prepared); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	result := make([]ProfileInfo, 0, len(prepared))
	refreshRuntime := false
	for _, item := range prepared {
		refreshRuntime = refreshRuntime || profileUsedByBinding(s.bindings, item.ID)
		if current, exists := s.profiles[item.ID]; exists {
			delete(s.profileIDsByIdentifier, current.Identifier)
		}
		s.profiles[item.ID] = cloneProfile(item)
		s.profileIDsByIdentifier[item.Identifier] = item.ID
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
	fields := make(map[string]string)
	if !mapping.IsUUIDv7(item.ID) {
		fields["id"] = "must be a canonical UUIDv7"
	}
	if !device.ValidStableID(item.Identifier) {
		fields["identifier"] = "must be a stable lowercase identifier"
	}
	if len(fields) > 0 {
		return NewValidationError("invalid mapping profile", fields)
	}
	return nil
}

func newProfileIdentity(item mapping.Profile) (mapping.Profile, error) {
	if item.Identifier == "" && item.ID != "" {
		// Accept old API clients during the transition: their former ID becomes
		// the editable identifier, while the server owns the permanent ID.
		item.Identifier = item.ID
	}
	if !device.ValidStableID(item.Identifier) {
		return mapping.Profile{}, NewValidationError("invalid mapping profile", map[string]string{"identifier": "must be a stable lowercase identifier"})
	}
	id, err := mapping.NewUUIDv7()
	if err != nil {
		return mapping.Profile{}, err
	}
	item.ID = id
	return item, nil
}

func importedProfileIdentity(item mapping.Profile) (mapping.Profile, error) {
	if item.Identifier == "" {
		if mapping.IsUUIDv7(item.ID) {
			item.Identifier = "profile-" + strings.ReplaceAll(item.ID, "-", "")
		} else {
			item.Identifier = item.ID
		}
	}
	if !mapping.IsUUIDv7(item.ID) {
		id, err := mapping.NewUUIDv7()
		if err != nil {
			return mapping.Profile{}, err
		}
		item.ID = id
	}
	return item, nil
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
		if transform.ReverseValues != nil {
			result.Transforms[index].ReverseValues = make(map[string]string, len(transform.ReverseValues))
			for key, value := range transform.ReverseValues {
				result.Transforms[index].ReverseValues[key] = value
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
