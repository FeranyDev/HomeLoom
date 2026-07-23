package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/mapping"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

func TestPropertyBindingHotReloadsAndMapsBothDirections(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles, err := application.NewProfileService(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	provider := virtual.NewProvider()
	service := application.NewDeviceService(provider, profiles)
	defer service.Close()
	profiles.SetChangeHandler(func(changeCtx context.Context) {
		if err := service.RefreshDevices(changeCtx); err != nil {
			t.Errorf("RefreshDevices() error = %v", err)
		}
	})

	initial, _ := service.List(ctx)
	assertPower(t, initial, false)
	binding, err := profiles.CreateBinding(ctx, mapping.Binding{
		ProfileID: "builtin-active-low", ProviderID: "virtual-main", DeviceID: "virtual-switch-1",
		EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if binding.ID == "" {
		t.Fatal("binding id was not generated")
	}
	mapped, _ := service.List(ctx)
	assertPower(t, mapped, true)

	result, err := service.SetPower(ctx, "virtual-switch-1", false)
	if err != nil {
		t.Fatal(err)
	}
	property, _ := result.Property("main", "switch", "power")
	if property.Value.Bool == nil || *property.Value.Bool {
		t.Fatalf("normalized write result = %#v", property.Value)
	}
	raw, err := provider.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"})
	if err != nil || raw.Value.Bool == nil || !*raw.Value.Bool {
		t.Fatalf("provider raw value = %#v, error = %v", raw.Value, err)
	}
	read, err := service.ReadProperty(ctx, "virtual-switch-1", "main", "switch", "power")
	if err != nil || read.Value.Bool == nil || *read.Value.Bool {
		t.Fatalf("normalized read = %#v, error = %v", read.Value, err)
	}
	metrics := service.Metrics()
	if metrics.MappingApplied < 3 || metrics.MappingErrors != 0 {
		t.Fatalf("mapping metrics = %#v", metrics)
	}

	if err := profiles.Delete(ctx, "custom-in-use"); !errors.Is(err, application.ErrProfileNotFound) {
		t.Fatalf("unexpected profile delete error = %v", err)
	}
	if err := profiles.DeleteBinding(ctx, binding.ID); err != nil {
		t.Fatal(err)
	}
	withoutBinding, _ := service.List(ctx)
	assertPower(t, withoutBinding, true)

	reloaded, err := application.NewProfileService(ctx, store)
	if err != nil || len(reloaded.ListBindings()) != 0 {
		t.Fatalf("reloaded bindings = %#v, error = %v", reloaded.ListBindings(), err)
	}
}

func TestProfileInUseCannotBeDeletedOrMadeNonReversible(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles, _ := application.NewProfileService(ctx, store)
	custom := mapping.Profile{SchemaVersion: 1, ID: "runtime-invert", Version: 1, Kind: mapping.KindProvider, InputType: device.ValueTypeBool, OutputType: device.ValueTypeBool, Transforms: []mapping.Transform{{Type: mapping.TransformInvert}}}
	if _, err := profiles.Create(ctx, custom); err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.CreateBinding(ctx, mapping.Binding{ID: "binding-one", ProfileID: custom.ID, ProviderID: "virtual-main", DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := profiles.Delete(ctx, custom.ID); !errors.Is(err, application.ErrProfileInUse) {
		t.Fatalf("delete error = %v", err)
	}
	minimum := 0.0
	custom.Version = 2
	custom.InputType, custom.OutputType = device.ValueTypeNumber, device.ValueTypeNumber
	custom.Transforms = []mapping.Transform{{Type: mapping.TransformClamp, Min: &minimum}}
	if _, err := profiles.Update(ctx, custom.ID, custom); err == nil {
		t.Fatal("non-reversible bound profile update accepted")
	}
}

func TestPropertyBindingTransformsNumericDefinition(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles, _ := application.NewProfileService(ctx, store)
	profile := mapping.Profile{SchemaVersion: 1, ID: "temperature-unit", Version: 1, Kind: mapping.KindProvider, InputType: device.ValueTypeNumber, OutputType: device.ValueTypeNumber, Transforms: []mapping.Transform{{Type: mapping.TransformUnit, FromUnit: "celsius", ToUnit: "fahrenheit"}}}
	if _, err := profiles.Create(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.CreateBinding(ctx, mapping.Binding{ID: "temperature-binding", ProfileID: profile.ID, ProviderID: "virtual-main", DeviceID: "virtual-temperature-1", EndpointID: "main", CapabilityID: "temperature", PropertyID: "current-temperature", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	minimum, maximum, step := -40.0, 100.0, 0.5
	definition := device.PropertyDefinition{ID: "current-temperature", Name: "Temperature", Type: device.ValueTypeNumber, Unit: "celsius", Min: &minimum, Max: &maximum, Step: &step, Readable: true}
	mapped, bindingID, applied, err := profiles.TransformPropertyDefinition("virtual-main", "virtual-temperature-1", "main", "temperature", "current-temperature", definition)
	if err != nil || !applied || bindingID != "temperature-binding" {
		t.Fatalf("transform = %#v, %q, %v, %v", mapped, bindingID, applied, err)
	}
	if mapped.Unit != "fahrenheit" || mapped.Min == nil || *mapped.Min != -40 || mapped.Max == nil || *mapped.Max != 212 || mapped.Step == nil || *mapped.Step < 0.899999 || *mapped.Step > 0.900001 {
		t.Fatalf("mapped definition = %#v", mapped)
	}
}

func TestPropertyBindingTransformsKelvinDefinitionToMired(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles, _ := application.NewProfileService(ctx, store)
	profile := mapping.Profile{
		SchemaVersion: 1, ID: "kelvin-mired", Version: 1, Kind: mapping.KindProvider,
		InputType: device.ValueTypeInt, OutputType: device.ValueTypeInt,
		Transforms: []mapping.Transform{
			{Type: mapping.TransformUnit, FromUnit: "kelvin", ToUnit: "mired"},
			{Type: mapping.TransformRound, Mode: "nearest"},
		},
	}
	if _, err := profiles.Create(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.CreateBinding(ctx, mapping.Binding{
		ID: "color-temperature-binding", ProfileID: profile.ID,
		ProviderID: "xiaomi-main", DeviceID: "monitor-light",
		EndpointID: "miot-2", CapabilityID: "service-2", PropertyID: "property-3",
		ModelEndpointID: "main", ModelCapabilityID: "light", ModelPropertyID: "color-temperature",
		Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	minimum, maximum, step := 2700.0, 6500.0, 100.0
	definition := device.PropertyDefinition{
		ID: "property-3", Name: "Color temperature", Type: device.ValueTypeInt,
		Unit: "kelvin", Min: &minimum, Max: &maximum, Step: &step,
		Readable: true, Writable: true, Notifiable: true,
	}
	mapped, bindingID, applied, err := profiles.TransformPropertyDefinition(
		"xiaomi-main", "monitor-light", "miot-2", "service-2", "property-3", definition,
	)
	if err != nil || !applied || bindingID != "color-temperature-binding" {
		t.Fatalf("transform = %#v, %q, %v, %v", mapped, bindingID, applied, err)
	}
	if mapped.Unit != "mired" || mapped.Min == nil || *mapped.Min != 154 || mapped.Max == nil || *mapped.Max != 370 || mapped.Step != nil {
		t.Fatalf("mapped definition = %#v", mapped)
	}
	resolved, found := profiles.ResolveModelDefinition(
		device.TypeLightbulb,
		device.ParameterPath{EndpointID: "main", CapabilityID: "light", PropertyID: "color-temperature"},
		mapped,
	)
	if !found || resolved.Min == nil || *resolved.Min != 154 || resolved.Max == nil || *resolved.Max != 370 || resolved.Step == nil || *resolved.Step != 1 {
		t.Fatalf("resolved model definition = %#v, found = %v", resolved, found)
	}
}

func TestPropertyBindingTransformsNumericDefinitionToEnumBands(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles, _ := application.NewProfileService(ctx, store)
	maximumCold, maximumComfortable := 18.0, 28.0
	profile := mapping.Profile{SchemaVersion: 1, ID: "comfort-band", Version: 1, Kind: mapping.KindProvider, InputType: device.ValueTypeNumber, OutputType: device.ValueTypeEnum, Transforms: []mapping.Transform{{Type: mapping.TransformRangeEnum, Bands: []mapping.RangeBand{
		{Max: &maximumCold, Value: "cold", Reverse: 10},
		{Max: &maximumComfortable, Value: "comfortable", Reverse: 24},
		{Value: "hot", Reverse: 32},
	}}}}
	if _, err := profiles.Create(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.CreateBinding(ctx, mapping.Binding{ID: "comfort-binding", ProfileID: profile.ID, ProviderID: "virtual-main", DeviceID: "room-temperature", EndpointID: "main", CapabilityID: "sensor", PropertyID: "value", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	minimum, maximum, step := -40.0, 100.0, 0.1
	definition := device.PropertyDefinition{ID: "value", Name: "Temperature", Type: device.ValueTypeNumber, Unit: "celsius", Min: &minimum, Max: &maximum, Step: &step, Readable: true}
	mapped, bindingID, applied, err := profiles.TransformPropertyDefinition("virtual-main", "room-temperature", "main", "sensor", "value", definition)
	if err != nil || !applied || bindingID != "comfort-binding" {
		t.Fatalf("transform = %#v, %q, %v, %v", mapped, bindingID, applied, err)
	}
	if mapped.Type != device.ValueTypeEnum || mapped.Unit != "" || mapped.Min != nil || mapped.Max != nil || mapped.Step != nil || len(mapped.Enum) != 3 || mapped.Enum[0] != "cold" || mapped.Enum[1] != "comfortable" || mapped.Enum[2] != "hot" {
		t.Fatalf("mapped definition = %#v", mapped)
	}
}

func assertPower(t *testing.T, items []device.Device, expected bool) {
	t.Helper()
	for _, item := range items {
		if item.ID != "virtual-switch-1" {
			continue
		}
		property, ok := item.Property("main", "switch", "power")
		if !ok || property.Value.Bool == nil || *property.Value.Bool != expected {
			t.Fatalf("power = %#v, expected %v", property.Value, expected)
		}
		return
	}
	t.Fatal("virtual switch not found")
}
