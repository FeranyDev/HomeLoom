package application_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/mapping"
	"github.com/feranydev/homeloom/backend/internal/persistence/gormstore"
)

func TestModelEnumOverrideUpdatesBuiltInContractAndReloads(t *testing.T) {
	ctx := context.Background()
	databaseURL, keyPath := testStoreCredentials(t)
	store, err := gormstore.Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewProfileService(ctx, store)
	if err != nil {
		t.Fatal(err)
	}

	path := device.ParameterPath{EndpointID: "main", CapabilityID: "fan", PropertyID: "current-state"}
	contract, ok := device.ModelContractFor(device.Type("fan"))
	if !ok {
		t.Fatal("fan contract missing")
	}
	var baseline []string
	for _, parameter := range contract.Parameters {
		if parameter.Path == path {
			baseline = append([]string(nil), parameter.Enum...)
			break
		}
	}
	if len(baseline) == 0 {
		t.Fatal("fan current-state enum missing")
	}
	updated := append(append([]string{}, baseline...), "sleep")
	item := mapping.ModelEnumOverride{
		DeviceType: device.Type("fan"), EndpointID: path.EndpointID, CapabilityID: path.CapabilityID, PropertyID: path.PropertyID,
		Enum: updated,
	}
	saved, err := service.UpsertModelEnumOverride(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" {
		t.Fatal("expected generated override id")
	}
	found := false
	for _, model := range service.ModelContracts() {
		if model.DeviceType != device.Type("fan") {
			continue
		}
		for _, parameter := range model.Parameters {
			if parameter.Path == path {
				found = reflect.DeepEqual(parameter.Enum, updated)
			}
		}
	}
	if !found {
		t.Fatalf("contract enum not overridden: %#v", service.ModelContracts())
	}
	definition, ok := service.ResolveModelDefinition(device.Type("fan"), path, device.PropertyDefinition{Type: device.ValueTypeEnum})
	if !ok || !reflect.DeepEqual(definition.Enum, updated) {
		t.Fatalf("resolved definition = %#v", definition)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = gormstore.Open(ctx, databaseURL, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service, err = application.NewProfileService(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	overrides := service.ListModelEnumOverrides()
	if len(overrides) != 1 || !reflect.DeepEqual(overrides[0].Enum, updated) {
		t.Fatalf("reloaded overrides = %#v", overrides)
	}
	if err := service.DeleteModelEnumOverride(ctx, overrides[0].ID); err != nil {
		t.Fatal(err)
	}
	definition, ok = service.ResolveModelDefinition(device.Type("fan"), path, device.PropertyDefinition{Type: device.ValueTypeEnum})
	if !ok || !reflect.DeepEqual(definition.Enum, baseline) {
		t.Fatalf("restored definition = %#v want %#v", definition.Enum, baseline)
	}
}
