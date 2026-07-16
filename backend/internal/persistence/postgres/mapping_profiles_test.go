package postgres

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
