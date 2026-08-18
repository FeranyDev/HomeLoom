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
	*created.Transforms[0].Factor = 99
	stored, _ := service.Get(item.ID)
	if *stored.Transforms[0].Factor != 2 {
		t.Fatal("caller mutated service snapshot")
	}
	if _, err := service.Update(ctx, item.ID, item); err == nil {
		t.Fatal("same profile version accepted")
	}
	item.Version = 2
	if _, err := service.Update(ctx, item.ID, item); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, "builtin-active-low"); !errors.Is(err, application.ErrProfileBuiltIn) {
		t.Fatalf("error = %v", err)
	}

	reloaded, err := application.NewProfileService(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reloaded.Get(item.ID)
	if err != nil || loaded.Version != 2 {
		t.Fatalf("loaded = %#v, error = %v", loaded, err)
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
