package gormstore

import (
	"context"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/mapping"
)

func TestSaveMappingProfilesRollsBackBatchOnConstraintFailure(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	valid := mapping.Profile{SchemaVersion: 1, ID: "first", Version: 1, Kind: mapping.KindProvider, InputType: device.ValueTypeBool, OutputType: device.ValueTypeBool, Transforms: []mapping.Transform{}}
	invalid := valid
	invalid.ID = "second"
	invalid.Kind = "invalid"
	if err := store.SaveMappingProfiles(ctx, []mapping.Profile{valid, invalid}); err == nil {
		t.Fatal("constraint failure was accepted")
	}
	items, err := store.ListMappingProfiles(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("items = %#v, error = %v", items, err)
	}
}

func TestMigrateMappingProfileIdentitiesUpdatesDocumentAndBindings(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	legacy := mapping.Profile{SchemaVersion: 1, ID: "legacy-profile", Version: 1, Kind: mapping.KindProvider, InputType: device.ValueTypeBool, OutputType: device.ValueTypeBool, Transforms: []mapping.Transform{{Type: mapping.TransformInvert}}}
	if err := store.SaveMappingProfile(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMappingBinding(ctx, mapping.Binding{ID: "legacy-profile-binding", ProfileID: legacy.ID, ProviderID: "virtual-main", DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	id, err := mapping.NewUUIDv7()
	if err != nil {
		t.Fatal(err)
	}
	migrated := legacy
	migrated.ID = id
	migrated.Identifier = "legacy-profile"
	if err := store.MigrateMappingProfileIdentities(ctx, []mapping.ProfileIdentityMigration{{PreviousID: legacy.ID, Profile: migrated}}, map[string]string{legacy.ID: migrated.ID}); err != nil {
		t.Fatal(err)
	}
	profiles, err := store.ListMappingProfiles(ctx)
	if err != nil || len(profiles) != 1 || profiles[0].ID != migrated.ID || profiles[0].Identifier != migrated.Identifier {
		t.Fatalf("profiles = %#v, error = %v", profiles, err)
	}
	bindings, err := store.ListMappingBindings(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].ProfileID != migrated.ID {
		t.Fatalf("bindings = %#v, error = %v", bindings, err)
	}
}
