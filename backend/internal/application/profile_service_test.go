package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func TestProfileServicePersistsVersionsAndProtectsBuiltIns(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service, err := application.NewProfileService(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(service.List()) != len(application.BuiltInProfiles()) {
		t.Fatalf("built-ins = %#v", service.List())
	}
	factor := 2.0
	item := mapping.Profile{SchemaVersion: 1, ID: "custom-scale", Version: 1, Kind: mapping.KindCapability, InputType: device.ValueTypeNumber, OutputType: device.ValueTypeNumber, Transforms: []mapping.Transform{{Type: mapping.TransformScale, Factor: &factor}}}
	created, err := service.Create(ctx, item)
	if err != nil || created.BuiltIn {
		t.Fatalf("created = %#v, error = %v", created, err)
	}
	if !mapping.IsUUIDv7(created.ID) || created.Identifier != "custom-scale" {
		t.Fatalf("new identity = %#v", created.Profile)
	}
	*created.Transforms[0].Factor = 99
	stored, _ := service.Get(item.ID)
	if stored.ID != created.ID || *stored.Transforms[0].Factor != 2 {
		t.Fatal("caller mutated service snapshot")
	}
	if _, err := service.Update(ctx, item.ID, item); err == nil {
		t.Fatal("same profile version accepted")
	}
	item.Version = 2
	item.ID = "not-the-permanent-id"
	item.Identifier = "renamed-scale"
	updated, err := service.Update(ctx, "custom-scale", item)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != created.ID || updated.Identifier != "renamed-scale" {
		t.Fatalf("updated identity = %#v", updated.Profile)
	}
	if err := service.Delete(ctx, "builtin-active-low"); !errors.Is(err, application.ErrProfileBuiltIn) {
		t.Fatalf("error = %v", err)
	}

	reloaded, err := application.NewProfileService(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reloaded.Get("renamed-scale")
	if err != nil || loaded.Version != 2 || loaded.ID != created.ID {
		t.Fatalf("loaded = %#v, error = %v", loaded, err)
	}
}

func TestProfileServiceMigratesLegacyIDsAndBindingReferences(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	legacy := mapping.Profile{SchemaVersion: 1, ID: "legacy-invert", Version: 1, Kind: mapping.KindProvider, InputType: device.ValueTypeBool, OutputType: device.ValueTypeBool, Transforms: []mapping.Transform{{Type: mapping.TransformInvert}}}
	if err := store.SaveMappingProfile(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMappingBinding(ctx, mapping.Binding{ID: "legacy-binding", ProfileID: legacy.ID, ProviderID: "virtual-main", DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	service, err := application.NewProfileService(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := service.Get("legacy-invert")
	if err != nil || !mapping.IsUUIDv7(migrated.ID) || migrated.Identifier != "legacy-invert" {
		t.Fatalf("migrated = %#v, error = %v", migrated, err)
	}
	binding, err := service.GetBinding("legacy-binding")
	if err != nil || binding.ProfileID != migrated.ID {
		t.Fatalf("binding = %#v, error = %v", binding, err)
	}

	updated := migrated.Profile
	updated.Version = 2
	updated.Identifier = "renamed-legacy-invert"
	if _, err := service.Update(ctx, migrated.ID, updated); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get("renamed-legacy-invert"); err != nil {
		t.Fatalf("renamed identifier did not resolve: %v", err)
	}
	if binding, err := service.GetBinding("legacy-binding"); err != nil || binding.ProfileID != migrated.ID {
		t.Fatalf("binding changed after rename = %#v, error = %v", binding, err)
	}

	reloaded, err := application.NewProfileService(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	persistedBinding, err := reloaded.GetBinding("legacy-binding")
	if err != nil || persistedBinding.ProfileID != migrated.ID {
		t.Fatalf("persisted binding = %#v, error = %v", persistedBinding, err)
	}
}

func TestProfileServiceImportIsValidatedBeforePersistence(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service, _ := application.NewProfileService(ctx, store)
	valid := mapping.Profile{SchemaVersion: 1, ID: "valid-map", Version: 1, Kind: mapping.KindProvider, InputType: device.ValueTypeBool, OutputType: device.ValueTypeBool, Transforms: []mapping.Transform{{Type: mapping.TransformInvert}}}
	invalid := valid
	invalid.ID = "Invalid ID"
	if _, err := service.Import(ctx, []mapping.Profile{valid, invalid}); err == nil {
		t.Fatal("invalid import accepted")
	}
	if _, err := service.Get(valid.ID); !errors.Is(err, application.ErrProfileNotFound) {
		t.Fatalf("partial import reached memory: %v", err)
	}
	items, err := store.ListMappingProfiles(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("stored = %#v, error = %v", items, err)
	}
}
