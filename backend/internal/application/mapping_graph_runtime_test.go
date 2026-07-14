package application_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/mapping"
	"github.com/feranydev/homeloom/backend/internal/persistence/sqlite"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type rawMappingProvider struct {
	item      device.Device
	lastWrite providersdk.PropertyWriteRequest
}

func newRawMappingProvider() *rawMappingProvider {
	return &rawMappingProvider{item: device.Device{
		SchemaVersion: 1, ID: "raw-switch-1", ProviderID: "raw-main", Name: "Raw Switch", Type: device.TypeSwitch,
		Availability: device.AvailabilityOnline, Online: true, LastUpdateAt: time.Now().UTC(),
		Endpoints: []device.Endpoint{{ID: "main", Name: "Main", Type: "main", Capabilities: []device.Capability{{ID: "vendor", Type: "vendor", Properties: []device.Property{{
			Definition: device.PropertyDefinition{ID: "raw-power", Name: "Raw power", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true}, Value: device.BoolValue(false),
		}}}}}},
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
	return []device.Device{p.item}, nil
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
	return p.item, nil
}

func TestProviderRouteRelocatesRawPathAndResolvesReverseWrite(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "mapping.db"))
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
	if _, _, err := service.ExecuteProperty(ctx, "raw-switch-1", "main", "switch", "power", device.BoolValue(true)); err != nil {
		t.Fatal(err)
	}
	if provider.lastWrite.CapabilityID != "vendor" || provider.lastWrite.PropertyID != "raw-power" || provider.lastWrite.Value.Bool == nil || !*provider.lastWrite.Value.Bool {
		t.Fatalf("raw write = %#v", provider.lastWrite)
	}
}

func TestConsumerRouteProjectsAndReversesConversion(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "consumer.db"))
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

func TestCustomUnifiedPropertyPersistsInModelCatalog(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "custom-model.db"))
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
