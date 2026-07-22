package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/mapping"
	"github.com/feranydev/homeloom/backend/internal/persistence/gormstore"
)

func TestCustomUnifiedModelPersistsAndOwnsThreeLevelProperties(t *testing.T) {
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
	model := mapping.CustomModel{DeviceType: "air-quality-monitor", Name: "空气质量监测器", Version: 1}
	if _, err := service.CreateCustomModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	property := mapping.CustomModelProperty{
		ID: "air-quality-monitor-pm25", DeviceType: model.DeviceType,
		EndpointID: "main", EndpointName: "主端点", EndpointType: "sensor",
		CapabilityID: "air-quality", CapabilityType: "air-quality",
		Definition: device.PropertyDefinition{ID: "pm2.5", Name: "PM2.5", Type: device.ValueTypeNumber, Unit: "microgram-per-cubic-meter", Readable: true, Notifiable: true},
	}
	if _, err := service.CreateCustomModelProperty(ctx, property); err != nil {
		t.Fatal(err)
	}
	contracts := service.ModelContracts()
	found := false
	for _, contract := range contracts {
		if contract.DeviceType == model.DeviceType {
			found = contract.Name == model.Name && !contract.BuiltIn && len(contract.Parameters) == 1 && contract.Parameters[0].Path == property.Path()
		}
	}
	if !found {
		t.Fatalf("custom contract not projected: %#v", contracts)
	}
	if err := service.DeleteCustomModel(ctx, model.DeviceType); !errors.Is(err, application.ErrProfileInUse) {
		t.Fatalf("delete model with property = %v", err)
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
	if len(service.ListCustomModels()) != 1 || len(service.ListCustomModelProperties()) != 1 {
		t.Fatalf("reloaded custom model = %#v, properties = %#v", service.ListCustomModels(), service.ListCustomModelProperties())
	}
	if err := service.DeleteCustomModelProperty(ctx, property.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteCustomModel(ctx, model.DeviceType); err != nil {
		t.Fatal(err)
	}
}
