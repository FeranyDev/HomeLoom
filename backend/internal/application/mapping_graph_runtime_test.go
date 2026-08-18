package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/mapping"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

type rawMappingProvider struct {
	item      device.Device
	lastWrite providersdk.PropertyWriteRequest
	handler   func(device.Device)
}

type failingCatalogProvider struct {
	public device.Device
	source device.Device
}

func (p *failingCatalogProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: "failing-source", Type: "test", Name: "Failing source", Version: "1"}
}
func (p *failingCatalogProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true}
}
func (p *failingCatalogProvider) Initialize(context.Context) error { return nil }
func (p *failingCatalogProvider) Close(context.Context) error      { return nil }
func (p *failingCatalogProvider) DiscoverDevices(context.Context) ([]device.Device, error) {
	return []device.Device{p.public.Clone()}, nil
}
func (p *failingCatalogProvider) SourceCatalog(context.Context) ([]providersdk.SourceCatalogDevice, error) {
	return []providersdk.SourceCatalogDevice{{Device: p.source.Clone(), Catalog: providersdk.SourceCatalogMetadata{Complete: true, Source: "test-native-spec"}}}, nil
}

type rejectingRawMapper struct{}

func (rejectingRawMapper) TransformProperty(_, _, endpointID, _, _ string, value device.PropertyValue, _ mapping.Direction) (device.PropertyValue, string, bool, error) {
	if endpointID == "native" {
		return device.PropertyValue{}, "broken-binding", true, errors.New("invalid test conversion")
	}
	return value, "", false, nil
}
func (rejectingRawMapper) TransformPropertyDefinition(_, _, endpointID, _, _ string, definition device.PropertyDefinition) (device.PropertyDefinition, string, bool, error) {
	if endpointID == "native" {
		return device.PropertyDefinition{}, "broken-binding", true, errors.New("invalid test conversion")
	}
	return definition, "", false, nil
}

type airConditionerMappingProvider struct {
	public    device.Device
	source    device.Device
	lastWrite providersdk.PropertyWriteRequest
}

type windowMappingProvider struct {
	item      device.Device
	lastWrite providersdk.PropertyWriteRequest
}

func newWindowMappingProvider() *windowMappingProvider {
	return &windowMappingProvider{item: device.Device{
		SchemaVersion: 1, ID: "xiaomi-1207372895", ProviderID: "xiaomi-2231ed", Name: "窗帘", Type: device.TypeWindowCovering,
		Availability: device.AvailabilityOnline, Online: true, LastUpdateAt: time.Now().UTC(),
		Endpoints: []device.Endpoint{{
			ID: "main", Name: "Main", Type: "main",
			Capabilities: []device.Capability{{
				ID: "window-covering", Type: "window-covering",
				Properties: []device.Property{
					{Definition: device.PropertyDefinition{ID: "current-position", Name: "当前位置", Type: device.ValueTypeInt, Unit: "percent", Min: numberPointer(0), Max: numberPointer(100), Step: numberPointer(1), Readable: true, Notifiable: true}, Value: device.IntValue(20)},
					{Definition: device.PropertyDefinition{ID: "target-position", Name: "目标位置", Type: device.ValueTypeInt, Unit: "percent", Min: numberPointer(0), Max: numberPointer(100), Step: numberPointer(1), Readable: true, Writable: true, Notifiable: true}, Value: device.IntValue(80)},
					{Definition: device.PropertyDefinition{ID: "position-state", Name: "运动状态", Type: device.ValueTypeEnum, Enum: []string{"decreasing", "increasing", "stopped"}, Readable: true, Notifiable: true}, Value: device.EnumValue("stopped")},
				},
			}},
		}},
	}}
}

func (p *windowMappingProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: "xiaomi-2231ed", Type: "xiaomi", Name: "Xiaomi", Version: "1"}
}
func (p *windowMappingProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyWrite: true}
}
func (p *windowMappingProvider) Initialize(context.Context) error { return nil }
func (p *windowMappingProvider) Close(context.Context) error      { return nil }
func (p *windowMappingProvider) DiscoverDevices(context.Context) ([]device.Device, error) {
	return []device.Device{p.item.Clone()}, nil
}
func (p *windowMappingProvider) SourceCatalog(context.Context) ([]providersdk.SourceCatalogDevice, error) {
	return []providersdk.SourceCatalogDevice{{Device: p.item.Clone(), Catalog: providersdk.SourceCatalogMetadata{Complete: true, Source: "miot-spec-cache"}}}, nil
}
func (p *windowMappingProvider) WriteProperty(_ context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	p.lastWrite = request
	for endpointIndex := range p.item.Endpoints {
		for capabilityIndex := range p.item.Endpoints[endpointIndex].Capabilities {
			for propertyIndex := range p.item.Endpoints[endpointIndex].Capabilities[capabilityIndex].Properties {
				property := &p.item.Endpoints[endpointIndex].Capabilities[capabilityIndex].Properties[propertyIndex]
				if p.item.Endpoints[endpointIndex].ID == request.EndpointID && p.item.Endpoints[endpointIndex].Capabilities[capabilityIndex].ID == request.CapabilityID && property.Definition.ID == request.PropertyID {
					property.Value = request.Value
				}
			}
		}
	}
	p.item.Sequence++
	p.item.LastUpdateAt = time.Now().UTC()
	return p.item.Clone(), nil
}

func newAirConditionerMappingProvider() *airConditionerMappingProvider {
	public := device.Device{
		SchemaVersion: 1, ID: "xiaomi-126772242", ProviderID: "xiaomi-2231ed", Name: "空调伴侣", Type: device.TypeAirConditioner,
		Availability: device.AvailabilityOnline, Online: true, LastUpdateAt: time.Now().UTC(),
		Endpoints: []device.Endpoint{{ID: "main", Name: "Main", Type: "main", Capabilities: []device.Capability{
			{ID: "air-conditioner", Type: "air-conditioner", Properties: []device.Property{
				{Definition: device.PropertyDefinition{ID: "active", Name: "启用", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true}, Value: device.BoolValue(true)},
				{Definition: device.PropertyDefinition{ID: "target-mode", Name: "运行模式", Type: device.ValueTypeEnum, Enum: []string{"auto", "cool", "dry", "heat", "fan"}, Readable: true, Writable: true, Notifiable: true}, Value: device.EnumValue("cool")},
			}},
			{ID: "temperature", Type: "temperature", Properties: []device.Property{
				{Definition: device.PropertyDefinition{ID: "target-temperature", Name: "目标温度", Type: device.ValueTypeNumber, Unit: "celsius", Min: numberPointer(16), Max: numberPointer(32), Step: numberPointer(0.5), Readable: true, Writable: true, Notifiable: true}, Value: device.NumberValue(24)},
			}},
		}}},
	}
	source := public.Clone()
	source.Endpoints = append(source.Endpoints, device.Endpoint{ID: "miot-3", Name: "Fan Control", Type: "fan-control", Capabilities: []device.Capability{{ID: "service-3", Type: "fan-control", Properties: []device.Property{{
		Definition: device.PropertyDefinition{ID: "property-1", Name: "Fan Level", Type: device.ValueTypeEnum, Enum: []string{"Auto", "Low", "Medium", "High"}, Readable: true, Writable: true, Notifiable: true},
		Value:      device.EnumValue("High"),
	}}}}})
	return &airConditionerMappingProvider{public: public, source: source}
}

func (p *airConditionerMappingProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: "xiaomi-2231ed", Type: "xiaomi", Name: "Xiaomi", Version: "1"}
}
func (p *airConditionerMappingProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyWrite: true}
}
func (p *airConditionerMappingProvider) Initialize(context.Context) error { return nil }
func (p *airConditionerMappingProvider) Close(context.Context) error      { return nil }
func (p *airConditionerMappingProvider) DiscoverDevices(context.Context) ([]device.Device, error) {
	return []device.Device{p.public.Clone()}, nil
}
func (p *airConditionerMappingProvider) SourceCatalog(context.Context) ([]providersdk.SourceCatalogDevice, error) {
	return []providersdk.SourceCatalogDevice{{Device: p.source.Clone(), Catalog: providersdk.SourceCatalogMetadata{Complete: true, Source: "miot-spec-cache"}}}, nil
}
func (p *airConditionerMappingProvider) WriteProperty(_ context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	p.lastWrite = request
	for endpointIndex := range p.source.Endpoints {
		for capabilityIndex := range p.source.Endpoints[endpointIndex].Capabilities {
			for propertyIndex := range p.source.Endpoints[endpointIndex].Capabilities[capabilityIndex].Properties {
				property := &p.source.Endpoints[endpointIndex].Capabilities[capabilityIndex].Properties[propertyIndex]
				if p.source.Endpoints[endpointIndex].ID == request.EndpointID && p.source.Endpoints[endpointIndex].Capabilities[capabilityIndex].ID == request.CapabilityID && property.Definition.ID == request.PropertyID {
					property.Value = request.Value
				}
			}
		}
	}
	p.source.Sequence++
	p.source.LastUpdateAt = time.Now().UTC()
	p.public.Sequence, p.public.LastUpdateAt = p.source.Sequence, p.source.LastUpdateAt
	return p.public.Clone(), nil
}

func numberPointer(value float64) *float64 { return &value }

func newRawMappingProvider() *rawMappingProvider {
	return &rawMappingProvider{item: device.Device{
		SchemaVersion: 1, ID: "raw-switch-1", ProviderID: "raw-main", Name: "Raw Switch", Type: device.TypeSwitch,
		Availability: device.AvailabilityOnline, Online: true, LastUpdateAt: time.Now().UTC(),
		Endpoints: []device.Endpoint{{ID: "main", Name: "Main", Type: "main", Capabilities: []device.Capability{{ID: "vendor", Type: "vendor", Properties: []device.Property{
			{Definition: device.PropertyDefinition{ID: "raw-power", Name: "Raw power", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true}, Value: device.BoolValue(false)},
			{Definition: device.PropertyDefinition{ID: "firmware-channel", Name: "Firmware channel", Type: device.ValueTypeString, Readable: true}, Value: device.StringValue("stable")},
		}}}}},
	}}
}

func (p *rawMappingProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: "raw-main", Type: "raw", Name: "Raw", Version: "1"}
}
func (p *rawMappingProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyRead: true, PropertyWrite: true}
}
func (p *rawMappingProvider) Initialize(context.Context) error { return nil }
func (p *rawMappingProvider) Close(context.Context) error      { return nil }
func (p *rawMappingProvider) DiscoverDevices(context.Context) ([]device.Device, error) {
	return []device.Device{p.publicSnapshot()}, nil
}
func (p *rawMappingProvider) SourceCatalog(context.Context) ([]providersdk.SourceCatalogDevice, error) {
	return []providersdk.SourceCatalogDevice{{
		Device: p.item.Clone(),
		Catalog: providersdk.SourceCatalogMetadata{
			Complete: true,
			Source:   "test-native-spec",
			Values:   providersdk.SnapshotValueStatuses(p.item),
		},
	}}, nil
}
func (p *rawMappingProvider) ReadProperty(_ context.Context, request providersdk.PropertyReadRequest) (device.Property, error) {
	property, _ := p.item.Property(request.EndpointID, request.CapabilityID, request.PropertyID)
	return property, nil
}
func (p *rawMappingProvider) WriteProperty(_ context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	p.lastWrite = request
	p.item.Endpoints[0].Capabilities[0].Properties[0].Value = request.Value
	p.item.Sequence++
	p.item.LastUpdateAt = time.Now().UTC()
	public := p.publicSnapshot()
	if p.handler != nil {
		p.handler(public)
	}
	return public, nil
}

func (p *rawMappingProvider) Subscribe(handler func(device.Device)) func() {
	p.handler = handler
	return func() { p.handler = nil }
}

func (p *rawMappingProvider) publicSnapshot() device.Device {
	item := p.item.Clone()
	item.Endpoints = nil
	return item
}

func TestProviderRouteRelocatesRawPathAndResolvesReverseWrite(t *testing.T) {
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
	_, err = profiles.CreateBinding(ctx, mapping.Binding{
		ID: "route-raw-power", Stage: mapping.StageProvider, DeviceType: device.TypeSwitch,
		ProviderID: "raw-main", DeviceID: "raw-switch-1", EndpointID: "main", CapabilityID: "vendor", PropertyID: "raw-power",
		ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := newRawMappingProvider()
	service := application.NewDeviceService(provider, profiles)
	defer service.Close()
	items, _ := service.List(ctx)
	property, ok := items[0].Property("main", "switch", "power")
	if !ok || property.Value.Bool == nil || *property.Value.Bool {
		t.Fatalf("mapped property = %#v", property)
	}
	if _, ok := items[0].Property("main", "vendor", "raw-power"); ok {
		t.Fatal("raw Provider property leaked into the unified model projection")
	}
	if _, ok := items[0].Property("main", "vendor", "firmware-channel"); ok {
		t.Fatal("unmapped Provider attribute leaked into the unified model projection")
	}
	catalog, err := service.ProviderCatalog(ctx)
	if err != nil || len(catalog) != 1 {
		t.Fatalf("Provider catalog = %#v, %v", catalog, err)
	}
	if _, ok := catalog[0].Property("main", "vendor", "firmware-channel"); !ok {
		t.Fatal("unmapped Provider attribute missing from mapping catalog")
	}
	updated, _, err := service.ExecuteProperty(ctx, "raw-switch-1", "main", "switch", "power", device.BoolValue(true))
	if err != nil {
		t.Fatal(err)
	}
	updatedPower, ok := updated.Property("main", "switch", "power")
	if !ok || updatedPower.Value.Bool == nil || !*updatedPower.Value.Bool {
		t.Fatalf("mapped write response = %#v", updatedPower)
	}
	if provider.lastWrite.CapabilityID != "vendor" || provider.lastWrite.PropertyID != "raw-power" || provider.lastWrite.Value.Bool == nil || !*provider.lastWrite.Value.Bool {
		t.Fatalf("raw write = %#v", provider.lastWrite)
	}
	if err := service.RefreshDevices(ctx); err != nil {
		t.Fatal(err)
	}
	refreshed, err := service.List(ctx)
	if err != nil || len(refreshed) != 1 {
		t.Fatalf("refresh = %#v, %v", refreshed, err)
	} else if refreshedPower, found := refreshed[0].Property("main", "switch", "power"); !found || refreshedPower.Value.Bool == nil || !*refreshedPower.Value.Bool {
		t.Fatalf("mapped refreshed property = %#v, found=%v", refreshedPower, found)
	}
}

func TestProviderBindingProjectsProfileDefaultWhenRawSourceIsMissing(t *testing.T) {
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
	defaultValue := device.BoolValue(true)
	profile := mapping.Profile{
		SchemaVersion: 1, ID: "missing-in-use-default", Version: 1, Kind: mapping.KindProvider,
		InputType: device.ValueTypeBool, OutputType: device.ValueTypeBool, Default: &defaultValue,
	}
	if _, err := profiles.Create(ctx, profile); err != nil {
		t.Fatal(err)
	}
	for _, binding := range []mapping.Binding{
		{
			ID: "raw-power-required", Stage: mapping.StageProvider, ProviderID: "raw-main", DeviceID: "raw-switch-1", DeviceType: device.TypeSwitch,
			EndpointID: "main", CapabilityID: "vendor", PropertyID: "raw-power",
			ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power", Enabled: true,
		},
		{
			ID: "missing-in-use", Stage: mapping.StageProvider, ProfileID: profile.ID, ProviderID: "raw-main", DeviceID: "raw-switch-1", DeviceType: device.TypeSwitch,
			EndpointID: "main", CapabilityID: "vendor", PropertyID: "missing-in-use",
			ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "in-use", Enabled: true,
		},
	} {
		if _, err := profiles.CreateBinding(ctx, binding); err != nil {
			t.Fatal(err)
		}
	}
	service := application.NewDeviceService(newRawMappingProvider(), profiles)
	defer service.Close()
	items, err := service.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %#v, error = %v", items, err)
	}
	inUse, found := items[0].Property("main", "switch", "in-use")
	if !found || inUse.Value.Bool == nil || !*inUse.Value.Bool || inUse.Definition.ParameterLevel != device.ParameterOptional {
		t.Fatalf("missing-source default projection = %#v, found=%v", inUse, found)
	}
}

func TestPublicProviderEventUsesCompleteCatalogOnlyInsideMappingBoundary(t *testing.T) {
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
	if _, err := profiles.CreateBinding(ctx, mapping.Binding{
		ID: "event-route-raw-power", Stage: mapping.StageProvider, DeviceType: device.TypeSwitch,
		ProviderID: "raw-main", DeviceID: "raw-switch-1", EndpointID: "main", CapabilityID: "vendor", PropertyID: "raw-power",
		ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	provider := newRawMappingProvider()
	service := application.NewDeviceService(provider, profiles)
	defer service.Close()
	events := make(chan device.Device, 1)
	unsubscribe := service.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()
	provider.item.Endpoints[0].Capabilities[0].Properties[0].Value = device.BoolValue(true)
	provider.item.Sequence++
	provider.item.LastUpdateAt = time.Now().UTC()
	provider.handler(provider.publicSnapshot())
	select {
	case item := <-events:
		power, found := item.Property("main", "switch", "power")
		if !found || power.Value.Bool == nil || !*power.Value.Bool {
			t.Fatalf("manual mapping missing from public event: %#v", item)
		}
		if _, leaked := item.Property("main", "vendor", "firmware-channel"); leaked {
			t.Fatalf("complete source property leaked into public event: %#v", item)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for mapped public event")
	}
}

func TestInvalidSourceMappingNeverLeaksCompleteCatalogIntoDeviceRegistry(t *testing.T) {
	public := device.Device{
		SchemaVersion: 1, ID: "safe-switch", ProviderID: "failing-source", Name: "Safe Switch", Type: device.TypeSwitch,
		Availability: device.AvailabilityOnline, Online: true, LastUpdateAt: time.Now().UTC(),
		Endpoints: []device.Endpoint{{ID: "main", Name: "Main", Type: "main", Capabilities: []device.Capability{{ID: "switch", Type: "switch", Properties: []device.Property{{Definition: device.PropertyDefinition{ID: "power", Name: "Power", Type: device.ValueTypeBool, Readable: true}, Value: device.BoolValue(false)}}}}}},
	}
	source := public.Clone()
	source.Endpoints = append(source.Endpoints, device.Endpoint{ID: "native", Name: "Native", Type: "vendor", Capabilities: []device.Capability{{ID: "miot", Type: "vendor", Properties: []device.Property{{Definition: device.PropertyDefinition{ID: "property-99", Name: "Native secret", Type: device.ValueTypeInt, Readable: true}, Value: device.IntValue(7)}}}}})
	service := application.NewDeviceService(&failingCatalogProvider{public: public, source: source}, rejectingRawMapper{})
	defer service.Close()
	items, err := service.List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("devices = %#v, %v", items, err)
	}
	if _, found := items[0].Property("main", "switch", "power"); !found {
		t.Fatal("safe public unified property was lost during mapping fallback")
	}
	if _, leaked := items[0].Property("native", "miot", "property-99"); leaked {
		t.Fatalf("complete source catalog leaked after mapping failure: %#v", items[0])
	}
	if items[0].MappingError != "属性映射失败：invalid test conversion" {
		t.Fatalf("mapping error = %q", items[0].MappingError)
	}
}

func TestAirConditionerIdentityEnumBindingShowsFourthPropertyAndWritesBack(t *testing.T) {
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
	if _, err := profiles.CreateBinding(ctx, mapping.Binding{
		ID: "binding-fan-speed", Stage: mapping.StageProvider, DeviceType: device.TypeAirConditioner,
		ProviderID: "xiaomi-2231ed", DeviceID: "xiaomi-126772242", EndpointID: "miot-3", CapabilityID: "service-3", PropertyID: "property-1",
		ModelEndpointID: "main", ModelCapabilityID: "air-conditioner", ModelPropertyID: "fan-speed", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	provider := newAirConditionerMappingProvider()
	service := application.NewDeviceService(provider, profiles)
	defer service.Close()
	items, err := service.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("devices = %#v, %v", items, err)
	}
	property, found := items[0].Property("main", "air-conditioner", "fan-speed")
	if !found || property.Value.String == nil || *property.Value.String != "high" {
		t.Fatalf("fourth mapped fan-speed property = %#v, found=%v", property, found)
	}
	updated, _, err := service.ExecuteProperty(ctx, items[0].ID, "main", "air-conditioner", "fan-speed", device.EnumValue("low"))
	if err != nil {
		t.Fatal(err)
	}
	if provider.lastWrite.EndpointID != "miot-3" || provider.lastWrite.CapabilityID != "service-3" || provider.lastWrite.PropertyID != "property-1" || provider.lastWrite.Value.String == nil || *provider.lastWrite.Value.String != "Low" {
		t.Fatalf("reverse fan-speed write = %#v", provider.lastWrite)
	}
	property, found = updated.Property("main", "air-conditioner", "fan-speed")
	if !found || property.Value.String == nil || *property.Value.String != "low" {
		t.Fatalf("updated fan-speed property = %#v, found=%v", property, found)
	}
}

func TestAirConditionerBrokenFanSpeedProfileIsVisibleOnFallbackDevice(t *testing.T) {
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
	maximumAuto, maximumLow, maximumMedium := 0.0, 30.0, 60.0
	profile := mapping.Profile{
		SchemaVersion: 1, ID: "fan-enum-to-number", Version: 1, Kind: mapping.KindProvider,
		InputType: device.ValueTypeEnum, OutputType: device.ValueTypeNumber,
		Transforms: []mapping.Transform{{Type: mapping.TransformEnumNumber, Bands: []mapping.RangeBand{
			{Max: &maximumAuto, Value: "auto", Reverse: 0},
			{Max: &maximumLow, Value: "low", Reverse: 30},
			{Max: &maximumMedium, Value: "medium", Reverse: 60},
			{Value: "high", Reverse: 90},
		}}},
	}
	if _, err := profiles.Create(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.CreateBinding(ctx, mapping.Binding{
		ID: "binding-fan-speed", Stage: mapping.StageProvider, ProfileID: profile.ID, DeviceType: device.TypeAirConditioner,
		ProviderID: "xiaomi-2231ed", DeviceID: "xiaomi-126772242", EndpointID: "miot-3", CapabilityID: "service-3", PropertyID: "property-1",
		ModelEndpointID: "main", ModelCapabilityID: "air-conditioner", ModelPropertyID: "rotation-speed", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(newAirConditionerMappingProvider(), profiles)
	defer service.Close()
	items, err := service.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("devices = %#v, %v", items, err)
	}
	if _, found := items[0].Property("main", "air-conditioner", "rotation-speed"); found {
		t.Fatalf("failed mapping should fall back to the narrow snapshot: %#v", items[0])
	}
	if !strings.Contains(items[0].MappingError, `binding "binding-fan-speed"`) || !strings.Contains(items[0].MappingError, `enum value "High" has no numeric mapping`) {
		t.Fatalf("mapping error = %q", items[0].MappingError)
	}
}

func TestManualProviderRouteOverridesConflictingAutomaticRouteWithoutDroppingDevice(t *testing.T) {
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
	if _, err := profiles.CreateBinding(ctx, mapping.Binding{
		ID: "curtain-target-as-current", Stage: mapping.StageProvider, DeviceType: device.TypeWindowCovering,
		ProviderID: "xiaomi-2231ed", DeviceID: "xiaomi-1207372895",
		EndpointID: "main", CapabilityID: "window-covering", PropertyID: "target-position",
		ModelEndpointID: "main", ModelCapabilityID: "window-covering", ModelPropertyID: "current-position", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	provider := newWindowMappingProvider()
	service := application.NewDeviceService(provider, profiles)
	defer service.Close()
	items, err := service.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("devices = %#v, %v", items, err)
	}
	current, currentFound := items[0].Property("main", "window-covering", "current-position")
	target, targetFound := items[0].Property("main", "window-covering", "target-position")
	state, stateFound := items[0].Property("main", "window-covering", "position-state")
	if !currentFound || !targetFound || !stateFound || current.Value.Int == nil || *current.Value.Int != 80 || target.Value.Int == nil || *target.Value.Int != 80 || state.Value.String == nil {
		t.Fatalf("manual-over-default projection = current %#v, target %#v, state %#v", current, target, state)
	}
}

func TestOneProviderSourcePropertyFansOutToMultipleUnifiedProperties(t *testing.T) {
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
	if _, err := profiles.CreateCustomModelProperty(ctx, mapping.CustomModelProperty{
		ID: "switch-mirrored-power", DeviceType: device.TypeSwitch,
		EndpointID: "main", EndpointName: "Main", EndpointType: "main", CapabilityID: "aux", CapabilityType: "aux",
		Definition: device.PropertyDefinition{ID: "mirrored-power", Name: "镜像开关", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true},
	}); err != nil {
		t.Fatal(err)
	}
	for _, binding := range []mapping.Binding{
		{ID: "raw-power-primary", Stage: mapping.StageProvider, DeviceType: device.TypeSwitch, ProviderID: "raw-main", DeviceID: "raw-switch-1", EndpointID: "main", CapabilityID: "vendor", PropertyID: "raw-power", ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power", Enabled: true},
		{ID: "raw-power-mirror", Stage: mapping.StageProvider, DeviceType: device.TypeSwitch, ProviderID: "raw-main", DeviceID: "raw-switch-1", EndpointID: "main", CapabilityID: "vendor", PropertyID: "raw-power", ModelEndpointID: "main", ModelCapabilityID: "aux", ModelPropertyID: "mirrored-power", Enabled: true},
	} {
		if _, err := profiles.CreateBinding(ctx, binding); err != nil {
			t.Fatal(err)
		}
	}
	provider := newRawMappingProvider()
	service := application.NewDeviceService(provider, profiles)
	defer service.Close()
	items, err := service.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("devices = %#v, %v", items, err)
	}
	primary, primaryFound := items[0].Property("main", "switch", "power")
	mirror, mirrorFound := items[0].Property("main", "aux", "mirrored-power")
	if !primaryFound || !mirrorFound || primary.Value.Bool == nil || mirror.Value.Bool == nil || *primary.Value.Bool != *mirror.Value.Bool {
		t.Fatalf("fan-out projection = primary %#v, mirror %#v", primary, mirror)
	}
	if _, _, err := service.ExecuteProperty(ctx, items[0].ID, "main", "aux", "mirrored-power", device.BoolValue(true)); err != nil {
		t.Fatal(err)
	}
	if provider.lastWrite.CapabilityID != "vendor" || provider.lastWrite.PropertyID != "raw-power" || provider.lastWrite.Value.Bool == nil || !*provider.lastWrite.Value.Bool {
		t.Fatalf("fan-out reverse route = %#v", provider.lastWrite)
	}
}

func TestRegisteredCustomPropertyEntersUnifiedProjection(t *testing.T) {
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
	custom := mapping.CustomModelProperty{
		ID: "switch-firmware-channel", DeviceType: device.TypeSwitch,
		EndpointID: "main", EndpointName: "Main", EndpointType: "main",
		CapabilityID: "device-info", CapabilityType: "device-info",
		Definition: device.PropertyDefinition{ID: "firmware-channel", Name: "Firmware channel", Type: device.ValueTypeString, Readable: true},
	}
	if _, err := profiles.CreateCustomModelProperty(ctx, custom); err != nil {
		t.Fatal(err)
	}
	for _, binding := range []mapping.Binding{
		{ID: "route-custom-power", Stage: mapping.StageProvider, DeviceType: device.TypeSwitch, ProviderID: "raw-main", DeviceID: "raw-switch-1", EndpointID: "main", CapabilityID: "vendor", PropertyID: "raw-power", ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power", Enabled: true},
		{ID: "route-custom-firmware", Stage: mapping.StageProvider, DeviceType: device.TypeSwitch, ProviderID: "raw-main", DeviceID: "raw-switch-1", EndpointID: "main", CapabilityID: "vendor", PropertyID: "firmware-channel", ModelEndpointID: "main", ModelCapabilityID: "device-info", ModelPropertyID: "firmware-channel", Enabled: true},
	} {
		if _, err := profiles.CreateBinding(ctx, binding); err != nil {
			t.Fatal(err)
		}
	}
	service := application.NewDeviceService(newRawMappingProvider(), profiles)
	defer service.Close()
	items, err := service.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("devices = %#v, %v", items, err)
	}
	property, ok := items[0].Property("main", "device-info", "firmware-channel")
	if !ok || property.Definition.ParameterLevel != device.ParameterCustom || property.Value.String == nil || *property.Value.String != "stable" {
		t.Fatalf("custom unified property = %#v", property)
	}
}

func TestConsumerRouteProjectsAndReversesConversion(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles, _ := application.NewProfileService(ctx, store)
	profile := mapping.Profile{SchemaVersion: 1, ID: "homekit-invert", Version: 1, Kind: mapping.KindTarget, InputType: device.ValueTypeBool, OutputType: device.ValueTypeBool, Transforms: []mapping.Transform{{Type: mapping.TransformInvert}}}
	if _, err := profiles.Create(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.CreateBinding(ctx, mapping.Binding{
		ID: "homekit-switch-on", Stage: mapping.StageConsumer, ProfileID: profile.ID, DeviceType: device.TypeSwitch,
		ProviderID: "provider-1", DeviceID: "switch-1",
		ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power",
		ConsumerID: "homekit", ConsumerProperty: "Switch.On", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	item := device.Device{SchemaVersion: 1, ID: "switch-1", ProviderID: "provider-1", Name: "Switch", Type: device.TypeSwitch, Availability: device.AvailabilityOnline, Online: true, LastUpdateAt: time.Now().UTC(), Endpoints: []device.Endpoint{{ID: "main", Name: "Main", Type: "main", Capabilities: []device.Capability{{ID: "switch", Type: "switch", Properties: []device.Property{{Definition: device.PropertyDefinition{ID: "power", Name: "Power", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true}, Value: device.BoolValue(false)}}}}}}}
	projected, err := profiles.ProjectConsumerDevice("homekit", item)
	if err != nil {
		t.Fatal(err)
	}
	property, _ := projected.Property("main", "switch", "power")
	if property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatalf("projected value = %#v", property.Value)
	}
	path, value, bindingID, applied, err := profiles.ResolveConsumerWrite("provider-1", "switch-1", "homekit", device.TypeSwitch, "main", "switch", "power", device.BoolValue(false))
	if err != nil || !applied || bindingID != "homekit-switch-on" || path.String() != "main/switch/power" || value.Bool == nil || !*value.Bool {
		t.Fatalf("reverse = %s %#v %q %v %v", path, value, bindingID, applied, err)
	}
	other := item
	other.ID = "switch-2"
	otherProjected, err := profiles.ProjectConsumerDevice("homekit", other)
	if err != nil {
		t.Fatal(err)
	}
	otherPower, _ := otherProjected.Property("main", "switch", "power")
	if otherPower.Value.Bool == nil || *otherPower.Value.Bool {
		t.Fatalf("device-scoped route leaked to another switch: %#v", otherPower.Value)
	}
}

func TestConsumerBindingProjectsProfileDefaultWhenModelPropertyIsMissing(t *testing.T) {
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
	defaultValue := device.BoolValue(false)
	profile := mapping.Profile{
		SchemaVersion: 1, ID: "missing-homekit-default", Version: 1, Kind: mapping.KindTarget,
		InputType: device.ValueTypeBool, OutputType: device.ValueTypeBool, Default: &defaultValue,
	}
	if _, err := profiles.Create(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.CreateBinding(ctx, mapping.Binding{
		ID: "missing-homekit-switch", Stage: mapping.StageConsumer, ProfileID: profile.ID,
		ProviderID: "provider-1", DeviceID: "switch-1", DeviceType: device.TypeSwitch,
		ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power",
		ConsumerID: "homekit", ConsumerProperty: "Switch.On", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	item := device.Device{SchemaVersion: 1, ID: "switch-1", ProviderID: "provider-1", Name: "Switch", Type: device.TypeSwitch, Availability: device.AvailabilityOnline, Online: true, LastUpdateAt: time.Now().UTC()}
	projected, err := profiles.ProjectConsumerDevice("homekit", item)
	if err != nil {
		t.Fatal(err)
	}
	power, found := projected.Property("main", "switch", "power")
	if !found || power.Value.Bool == nil || *power.Value.Bool {
		t.Fatalf("missing-model default projection = %#v, found=%v", power, found)
	}
}

func TestConsumerRouteIsScopedToTargetVirtualDevice(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles, _ := application.NewProfileService(ctx, store)
	profile := mapping.Profile{SchemaVersion: 1, ID: "target-invert", Version: 1, Kind: mapping.KindTarget, InputType: device.ValueTypeBool, OutputType: device.ValueTypeBool, Transforms: []mapping.Transform{{Type: mapping.TransformInvert}}}
	if _, err := profiles.Create(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.CreateBinding(ctx, mapping.Binding{
		ID: "bridge-switch-on", Stage: mapping.StageConsumer, ProfileID: profile.ID,
		ProviderID: "provider-1", DeviceID: "switch-1", DeviceType: device.TypeSwitch,
		ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power",
		TargetID: "apple-main", ConsumerDeviceID: "virtual-switch",
		ConsumerID: "homekit", ConsumerProperty: "Switch.On", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	item := device.Device{SchemaVersion: 1, ID: "switch-1", ProviderID: "provider-1", Name: "Switch", Type: device.TypeSwitch, Availability: device.AvailabilityOnline, Online: true, LastUpdateAt: time.Now().UTC(), Endpoints: []device.Endpoint{{ID: "main", Name: "Main", Type: "main", Capabilities: []device.Capability{{ID: "switch", Type: "switch", Properties: []device.Property{{Definition: device.PropertyDefinition{ID: "power", Name: "Power", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true}, Value: device.BoolValue(false)}}}}}}}
	projected, err := profiles.ProjectConsumerDeviceInstance("homekit", "apple-main", "virtual-switch", item)
	if err != nil {
		t.Fatal(err)
	}
	power, _ := projected.Property("main", "switch", "power")
	if power.Value.Bool == nil || !*power.Value.Bool {
		t.Fatalf("scoped projected value = %#v", power.Value)
	}
	other, err := profiles.ProjectConsumerDeviceInstance("homekit", "apple-main", "other-switch", item)
	if err != nil {
		t.Fatal(err)
	}
	otherPower, _ := other.Property("main", "switch", "power")
	if otherPower.Value.Bool == nil || *otherPower.Value.Bool {
		t.Fatalf("target virtual-device route leaked: %#v", otherPower.Value)
	}
	path, value, bindingID, applied, err := profiles.ResolveConsumerWriteInstance("provider-1", "switch-1", "apple-main", "virtual-switch", "homekit", device.TypeSwitch, "main", "switch", "power", device.BoolValue(false))
	if err != nil || !applied || bindingID != "bridge-switch-on" || path.String() != "main/switch/power" || value.Bool == nil || !*value.Bool {
		t.Fatalf("scoped reverse = %s %#v %q %v %v", path, value, bindingID, applied, err)
	}
}

func TestConsumerDeviceComposesAuxiliarySourcesAndRoutesWritesByPriority(t *testing.T) {
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
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "aggregate", Name: "Aggregate", Config: []byte(`{"devices":[{"id":"primary-switch","name":"Primary","type":"switch","power":false},{"id":"aux-switch","name":"Auxiliary","type":"switch","power":true}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(provider, profiles)
	defer service.Close()
	auxiliaryBinding := mapping.Binding{
		ID: "aggregate-aux-outlet-on", Stage: mapping.StageConsumer,
		ProviderID: "aggregate", DeviceID: "aux-switch", DeviceType: device.TypeSwitch, ConsumerDeviceType: device.TypeOutlet,
		ModelEndpointID: "main", ModelCapabilityID: "switch", ModelPropertyID: "power",
		TargetID: "apple-main", ConsumerDeviceID: "aggregate-outlet", ConsumerID: "homekit", ConsumerProperty: "Outlet.On", Enabled: true,
	}
	if _, err := profiles.CreateBinding(ctx, auxiliaryBinding); err != nil {
		t.Fatal(err)
	}
	if _, _, bindingID, applied, err := profiles.ResolveConsumerWriteInstance("aggregate", "aux-switch", "apple-main", "aggregate-outlet", "homekit", device.TypeOutlet, "main", "switch", "power", device.BoolValue(false)); err != nil || !applied || bindingID != auxiliaryBinding.ID {
		t.Fatalf("auxiliary binding resolution = %q, applied=%v, err=%v", bindingID, applied, err)
	}
	sourceIDs := []string{"primary-switch", "aux-switch"}
	projected, err := service.ProjectSourcesForConsumerInstance("homekit", "apple-main", "aggregate-outlet", device.TypeOutlet, sourceIDs)
	if err != nil {
		t.Fatal(err)
	}
	power, found := projected.Property("main", "switch", "power")
	if !found || power.Value.Bool == nil || !*power.Value.Bool || projected.Type != device.TypeOutlet {
		t.Fatalf("auxiliary projection = %#v, found=%v", projected, found)
	}
	primaryBinding := auxiliaryBinding
	primaryBinding.ID, primaryBinding.DeviceID = "aggregate-primary-outlet-on", "primary-switch"
	if _, err := profiles.CreateBinding(ctx, primaryBinding); err != nil {
		t.Fatal(err)
	}
	projected, err = service.ProjectSourcesForConsumerInstance("homekit", "apple-main", "aggregate-outlet", device.TypeOutlet, sourceIDs)
	if err != nil {
		t.Fatal(err)
	}
	power, _ = projected.Property("main", "switch", "power")
	if power.Value.Bool == nil || *power.Value.Bool {
		t.Fatalf("primary source did not win mapping conflict: %#v", power.Value)
	}
	if _, _, err := service.ExecuteProperty(ctx, "aux-switch", "main", "switch", "power", device.BoolValue(false)); err != nil {
		t.Fatal(err)
	}
	if err := service.RefreshDevices(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ExecuteConsumerPropertySourcesInstance(ctx, "homekit", "apple-main", "aggregate-outlet", device.TypeOutlet, sourceIDs, "main", "switch", "power", device.BoolValue(true)); err != nil {
		t.Fatal(err)
	}
	if err := service.RefreshDevices(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]bool)
	for _, item := range items {
		property, _ := item.Property("main", "switch", "power")
		if property.Value.Bool != nil {
			values[item.ID] = *property.Value.Bool
		}
	}
	if !values["primary-switch"] || values["aux-switch"] {
		t.Fatalf("write routing updated wrong source: %#v", values)
	}
}

func TestConsumerAirConditionerKeepsRotationSpeedWhenAuxiliaryClimateFieldsAreMapped(t *testing.T) {
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
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "aggregate-climate", Name: "Aggregate climate", Config: []byte(`{"devices":[{"id":"primary-air-conditioner","name":"Primary air conditioner","type":"air-conditioner"},{"id":"aux-climate","name":"Auxiliary climate","type":"temperature-humidity-sensor","temperature":21.8,"humidity":54}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(provider, profiles)
	defer service.Close()

	newClimateBinding := func(id, capabilityID, propertyID, consumerProperty string) mapping.Binding {
		return mapping.Binding{
			ID: id, Stage: mapping.StageConsumer,
			ProviderID: "aggregate-climate", DeviceID: "aux-climate", DeviceType: device.TypeTemperatureHumiditySensor, ConsumerDeviceType: device.TypeAirConditioner,
			ModelEndpointID: "main", ModelCapabilityID: capabilityID, ModelPropertyID: propertyID,
			TargetID: "apple-main", ConsumerDeviceID: "living-room-ac", ConsumerID: "homekit", ConsumerProperty: consumerProperty, Enabled: true,
		}
	}
	if _, err := profiles.CreateBinding(ctx, newClimateBinding("auxiliary-current-temperature", "temperature", "current-temperature", "HeaterCooler.CurrentTemperature")); err != nil {
		t.Fatal(err)
	}

	sourceIDs := []string{"primary-air-conditioner", "aux-climate"}
	projected, err := service.ProjectSourcesForConsumerInstance("homekit", "apple-main", "living-room-ac", device.TypeAirConditioner, sourceIDs)
	if err != nil {
		t.Fatal(err)
	}
	rotationSpeed, found := projected.Property("main", "air-conditioner", "rotation-speed")
	if !found || rotationSpeed.Value.Number == nil || *rotationSpeed.Value.Number != 40 {
		t.Fatalf("temperature mapping removed primary rotation speed: %#v, found=%v", rotationSpeed, found)
	}
	currentTemperature, found := projected.Property("main", "temperature", "current-temperature")
	if !found || currentTemperature.Value.Number == nil || *currentTemperature.Value.Number != 21.8 {
		t.Fatalf("auxiliary temperature projection = %#v, found=%v", currentTemperature, found)
	}

	if _, err := profiles.CreateBinding(ctx, newClimateBinding("auxiliary-current-humidity", "humidity", "current-humidity", "HumiditySensor.CurrentRelativeHumidity")); err != nil {
		t.Fatal(err)
	}
	projected, err = service.ProjectSourcesForConsumerInstance("homekit", "apple-main", "living-room-ac", device.TypeAirConditioner, sourceIDs)
	if err != nil {
		t.Fatal(err)
	}
	rotationSpeed, found = projected.Property("main", "air-conditioner", "rotation-speed")
	if !found || rotationSpeed.Value.Number == nil || *rotationSpeed.Value.Number != 40 {
		t.Fatalf("humidity mapping removed primary rotation speed: %#v, found=%v", rotationSpeed, found)
	}
	currentHumidity, found := projected.Property("main", "humidity", "current-humidity")
	if !found || currentHumidity.Value.Number == nil || *currentHumidity.Value.Number != 54 {
		t.Fatalf("auxiliary humidity projection = %#v, found=%v", currentHumidity, found)
	}
}

func TestTemperatureSensorConsumerRouteKeepsExplicitSemantic(t *testing.T) {
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
	if _, err := profiles.CreateBinding(ctx, mapping.Binding{
		ID: "temperature-sensor-homekit", Stage: mapping.StageConsumer,
		ProviderID: "provider-1", DeviceID: "sensor-1", DeviceType: device.TypeTemperatureSensor,
		ModelEndpointID: "main", ModelCapabilityID: "temperature", ModelPropertyID: "current-temperature",
		TargetID: "apple-main", ConsumerDeviceID: "room-sensor",
		ConsumerID: "homekit", ConsumerProperty: "TemperatureSensor.CurrentTemperature", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	item := device.Device{SchemaVersion: 1, ID: "sensor-1", ProviderID: "provider-1", Name: "Room Sensor", Type: device.TypeTemperatureSensor, Availability: device.AvailabilityOnline, Online: true, LastUpdateAt: time.Now().UTC(), Endpoints: []device.Endpoint{{ID: "main", Name: "Main", Type: "sensor", Capabilities: []device.Capability{{ID: "temperature", Type: "temperature", Properties: []device.Property{{Definition: device.PropertyDefinition{ID: "current-temperature", Name: "当前温度", Type: device.ValueTypeNumber, Unit: "celsius", Readable: true, Notifiable: true}, Value: device.NumberValue(22.5)}}}}}}}
	if err := item.NormalizeModelParameters(); err != nil {
		t.Fatal(err)
	}
	projected, err := profiles.ProjectConsumerDeviceInstance("homekit", "apple-main", "room-sensor", item)
	if err != nil {
		t.Fatal(err)
	}
	temperature, found := projected.Property("main", "temperature", "current-temperature")
	if !found || temperature.Value.Number == nil || *temperature.Value.Number != 22.5 {
		t.Fatalf("temperature projection = %#v, found=%v", temperature, found)
	}
}

func TestCustomUnifiedPropertyPersistsInModelCatalog(t *testing.T) {
	ctx := context.Background()
	store, err := openTestStore(t, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles, _ := application.NewProfileService(ctx, store)
	custom := mapping.CustomModelProperty{ID: "switch-led-pattern", DeviceType: device.TypeSwitch, EndpointID: "main", EndpointName: "Main", EndpointType: "main", CapabilityID: "vendor-acme", CapabilityType: "vendor-acme", Definition: device.PropertyDefinition{ID: "led-pattern", Name: "LED Pattern", Type: device.ValueTypeEnum, Enum: []string{"off", "pulse"}, Readable: true, Writable: true, Notifiable: true}}
	if _, err := profiles.CreateCustomModelProperty(ctx, custom); err != nil {
		t.Fatal(err)
	}
	reloaded, err := application.NewProfileService(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, contract := range reloaded.ModelContracts() {
		for _, parameter := range contract.Parameters {
			found = found || (contract.DeviceType == device.TypeSwitch && parameter.Path.String() == "main/vendor-acme/led-pattern" && parameter.Level == device.ParameterCustom)
		}
	}
	if !found {
		t.Fatal("custom property missing from reloaded model catalog")
	}
}
