package providermanager_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/feranydev/homeloom/backend/internal/providers/xiaomi"
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
)

type failingProvider struct{ id string }

type authRequiredProvider struct {
	initializations atomic.Int32
	closes          atomic.Int32
}

func (*authRequiredProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: "auth-required", Type: xiaomi.XiaomiMIoTCloudProviderType, Name: "Cloud"}
}
func (*authRequiredProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{}
}
func (p *authRequiredProvider) Initialize(context.Context) error {
	p.initializations.Add(1)
	return &xiaomi.IdentityVerificationRequiredError{URL: "https://account.xiaomi.com/identity/authStart"}
}
func (p *authRequiredProvider) Close(context.Context) error {
	p.closes.Add(1)
	return nil
}

type authenticationRequiredProvider struct {
	id              string
	initializations atomic.Int32
	closes          atomic.Int32
}

type authenticationRequiredError struct{}

func (authenticationRequiredError) Error() string                { return "identity verification required" }
func (authenticationRequiredError) AuthenticationRequired() bool { return true }

func (p *authenticationRequiredProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "xiaomi-miot-cloud", Name: p.id}
}
func (*authenticationRequiredProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{}
}
func (p *authenticationRequiredProvider) Initialize(context.Context) error {
	p.initializations.Add(1)
	return authenticationRequiredError{}
}
func (p *authenticationRequiredProvider) Close(context.Context) error {
	p.closes.Add(1)
	return nil
}

type bindingProvider struct {
	id       string
	devices  []device.Device
	bindings []providersdk.CapabilityBinding
	hidden   []string
}

func (p *bindingProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "test-camera", Name: p.id}
}
func (*bindingProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true}
}
func (*bindingProvider) Initialize(context.Context) error { return nil }
func (*bindingProvider) Close(context.Context) error      { return nil }
func (p *bindingProvider) DiscoverDevices(context.Context) ([]device.Device, error) {
	result := make([]device.Device, len(p.devices))
	for index := range p.devices {
		result[index] = p.devices[index].Clone()
	}
	return result, nil
}
func (p *bindingProvider) CapabilityBindings() []providersdk.CapabilityBinding {
	return append([]providersdk.CapabilityBinding(nil), p.bindings...)
}
func (p *bindingProvider) HiddenDeviceIDs() []string {
	return append([]string(nil), p.hidden...)
}

type controlProvider struct {
	bindingProvider
	lastWrite   providersdk.PropertyWriteRequest
	lastCommand providersdk.CommandRequest
	handler     func(device.Device)
}

type catalogControlProvider struct {
	controlProvider
	catalog []providersdk.SourceCatalogDevice
}

func (p *catalogControlProvider) SourceCatalog(context.Context) ([]providersdk.SourceCatalogDevice, error) {
	result := make([]providersdk.SourceCatalogDevice, len(p.catalog))
	for index := range p.catalog {
		result[index] = p.catalog[index]
		result[index].Device = p.catalog[index].Device.Clone()
	}
	return result, nil
}

type catalogProvider struct {
	bindingProvider
	catalog []providersdk.SourceCatalogDevice
}

func (p *catalogProvider) SourceCatalog(context.Context) ([]providersdk.SourceCatalogDevice, error) {
	result := make([]providersdk.SourceCatalogDevice, len(p.catalog))
	for index := range p.catalog {
		result[index] = p.catalog[index]
		result[index].Device = p.catalog[index].Device.Clone()
	}
	return result, nil
}

func (p *controlProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "xiaomi", Name: p.id}
}

func (p *controlProvider) ReadProperty(_ context.Context, request providersdk.PropertyReadRequest) (device.Property, error) {
	for _, endpoint := range p.devices[0].Endpoints {
		if endpoint.ID != request.EndpointID {
			continue
		}
		for _, capability := range endpoint.Capabilities {
			if capability.ID != request.CapabilityID {
				continue
			}
			for _, property := range capability.Properties {
				if property.Definition.ID == request.PropertyID {
					return property, nil
				}
			}
		}
	}
	return device.Property{}, providersdk.ErrPropertyUnsupported
}
func (p *controlProvider) WriteProperty(_ context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	p.lastWrite = request
	result := p.devices[0].Clone()
	if !result.SetProperty(request.EndpointID, request.CapabilityID, request.PropertyID, request.Value) {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	p.devices[0] = result
	return result, nil
}
func (p *controlProvider) ExecuteCommand(_ context.Context, request providersdk.CommandRequest) (device.Device, error) {
	p.lastCommand = request
	return p.devices[0].Clone(), nil
}
func (p *controlProvider) Subscribe(handler func(device.Device)) func() {
	p.handler = handler
	return func() { p.handler = nil }
}
func (p *controlProvider) emit(item device.Device) {
	if p.handler != nil {
		p.handler(item)
	}
}

func cameraSnapshot(id, providerID string, online bool, capabilities ...device.Capability) device.Device {
	item := device.Device{
		SchemaVersion: device.SchemaVersion, ID: id, ProviderID: providerID, Name: id,
		Type: device.TypeCamera, Endpoints: []device.Endpoint{{
			ID: "main", Name: "Camera", Type: string(device.TypeCamera), Capabilities: capabilities,
		}}, LastUpdateAt: time.Now().UTC(),
	}
	item.SetOnline(online)
	return item
}

type interestedProvider struct {
	id        string
	interests []providersdk.PropertyInterest
}

func TestManagerMergesAndDelegatesBoundCameraControls(t *testing.T) {
	mediaCapability := device.Capability{ID: "media", Type: "media", Properties: []device.Property{{
		Definition: device.PropertyDefinition{ID: "live-stream", Name: "Live", Type: device.ValueTypeBool, Readable: true},
		Value:      device.BoolValue(true),
	}}}
	privacyCapability := device.Capability{
		ID: "privacy", Type: "privacy",
		Properties: []device.Property{{
			Definition: device.PropertyDefinition{ID: "enabled", Name: "Privacy", Type: device.ValueTypeBool, Readable: true, Writable: true},
			Value:      device.BoolValue(false),
		}},
		Commands: []device.CommandDefinition{{ID: "toggle", Name: "Toggle", Idempotent: false}},
	}
	camera := &bindingProvider{
		id:      "camera-main",
		devices: []device.Device{cameraSnapshot("living-camera", "camera-main", true, mediaCapability)},
		bindings: []providersdk.CapabilityBinding{{
			DeviceID: "living-camera", ProviderID: "xiaomi-hub", SourceDeviceID: "xiaomi-camera",
		}},
	}
	control := &controlProvider{bindingProvider: bindingProvider{
		id:      "xiaomi-hub",
		devices: []device.Device{cameraSnapshot("xiaomi-camera", "xiaomi-hub", true, mediaCapability, privacyCapability)},
	}}
	manager, err := providermanager.New(camera, control)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	items, err := manager.DiscoverDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "living-camera" || items[0].ProviderID != "camera-main" ||
		len(items[0].Endpoints) != 1 || len(items[0].Endpoints[0].Capabilities) != 2 {
		t.Fatalf("merged cameras = %#v", items)
	}
	value := device.BoolValue(true)
	updated, err := manager.WriteProperty(ctx, providersdk.PropertyWriteRequest{
		DeviceID: "living-camera", EndpointID: "main", CapabilityID: "privacy", PropertyID: "enabled", Value: value,
	})
	if err != nil {
		t.Fatal(err)
	}
	if control.lastWrite.DeviceID != "xiaomi-camera" || updated.ID != "living-camera" ||
		updated.ProviderID != "camera-main" || !updated.IsOnline() {
		t.Fatalf("delegated write = %#v, request=%#v", updated, control.lastWrite)
	}
	if _, err := manager.ExecuteCommand(ctx, providersdk.CommandRequest{
		DeviceID: "living-camera", EndpointID: "main", CapabilityID: "privacy", CommandID: "toggle",
	}); err != nil {
		t.Fatal(err)
	}
	if control.lastCommand.DeviceID != "xiaomi-camera" {
		t.Fatalf("delegated command = %#v", control.lastCommand)
	}
}

func TestManagerMarksAuthenticationRequiredWithoutSchedulingRetries(t *testing.T) {
	provider := &authRequiredProvider{}
	manager, err := providermanager.New(provider)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	infos := manager.ProviderInfos()
	if len(infos) != 1 || infos[0].Status != "auth_required" || infos[0].NextRetryAt != nil {
		t.Fatalf("provider info=%#v", infos)
	}
	if got, ok := manager.ProviderAny(provider.Manifest().ID); !ok || got != provider {
		t.Fatalf("ProviderAny()=%#v/%v", got, ok)
	}
	// An auth gate must remain paused rather than repeatedly re-posting the
	// account password while the administrator is entering the code.
	time.Sleep(20 * time.Millisecond)
	if count := provider.initializations.Load(); count != 1 {
		t.Fatalf("authentication retries=%d", count)
	}
}

func TestManagerUsesNativeCatalogForControlOnlyCameraCapabilities(t *testing.T) {
	privacy := device.Capability{
		ID: "privacy", Type: "privacy",
		Properties: []device.Property{{
			Definition: device.PropertyDefinition{ID: "enabled", Name: "Privacy", Type: device.ValueTypeBool, Readable: true, Writable: true},
			Value:      device.BoolValue(false),
		}},
		Commands: []device.CommandDefinition{
			{ID: "move-left", Name: "向左转动"},
			{ID: "goto-preset", Name: "转到记忆点", Parameters: []device.CommandParameter{
				{ID: "preset-id", Name: "记忆点", Type: device.ValueTypeString, Required: true},
			}},
		},
	}
	camera := &bindingProvider{
		id: "camera-main", devices: []device.Device{cameraSnapshot("camera-one", "camera-main", true)},
		bindings: []providersdk.CapabilityBinding{{
			DeviceID: "camera-one", ProviderID: "xiaomi-hub", SourceDeviceID: "source-camera",
		}},
	}
	publicSource := cameraSnapshot("source-camera", "xiaomi-hub", true)
	nativeSource := cameraSnapshot("source-camera", "xiaomi-hub", true, privacy)
	control := &catalogControlProvider{
		controlProvider: controlProvider{bindingProvider: bindingProvider{
			id: "xiaomi-hub", devices: []device.Device{publicSource},
		}},
		catalog: []providersdk.SourceCatalogDevice{{
			Device: nativeSource,
			Catalog: providersdk.SourceCatalogMetadata{
				Complete: true, Source: "miot-spec.org", Model: "vendor.camera.v1",
				SpecType: "urn:miot-spec-v2:device:camera:vendor-v1:1",
				Values: map[string]providersdk.SourceValueStatus{
					providersdk.SourceValueKey("main", "privacy", "enabled"): {Known: true, Available: true},
				},
			},
		}},
	}
	manager, err := providermanager.New(camera, control)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)

	items, err := manager.DiscoverDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "camera-one" {
		t.Fatalf("devices = %#v", items)
	}
	if _, ok := items[0].Property("main", "privacy", "enabled"); !ok {
		t.Fatalf("native catalog capability was not merged: %#v", items[0])
	}
	commands := items[0].Endpoints[0].Capabilities[0].Commands
	if len(commands) != 2 || commands[0].ID != "move-left" || commands[1].ID != "goto-preset" {
		t.Fatalf("native camera commands were not merged: %#v", commands)
	}
	catalog, err := manager.SourceCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 1 || catalog[0].ID != "camera-one" || catalog[0].ProviderID != "camera-main" {
		t.Fatalf("composed source catalog identities = %#v", catalog)
	}
	if _, ok := catalog[0].Property("main", "privacy", "enabled"); !ok {
		t.Fatalf("composed source catalog omitted control properties: %#v", catalog[0])
	}
	if !catalog[0].Catalog.Complete || catalog[0].Catalog.Model != "vendor.camera.v1" ||
		catalog[0].Catalog.Values[providersdk.SourceValueKey("main", "privacy", "enabled")].Known != true {
		t.Fatalf("composed source catalog metadata = %#v", catalog[0].Catalog)
	}
}

func TestManagerSourceCatalogPreservesNativePropertiesAlongsidePublicProjection(t *testing.T) {
	public := device.Capability{ID: "switch", Type: "switch", Properties: []device.Property{{
		Definition: device.PropertyDefinition{ID: "power", Name: "Power", Type: device.ValueTypeBool, Readable: true},
		Value:      device.BoolValue(false),
	}}}
	native := device.Capability{ID: "native", Type: "vendor", Properties: []device.Property{{
		Definition: device.PropertyDefinition{ID: "firmware-channel", Name: "Firmware Channel", Type: device.ValueTypeString, Readable: true},
		Value:      device.StringValue("stable"),
	}}}
	provider := &catalogProvider{
		bindingProvider: bindingProvider{id: "xiaomi-main", devices: []device.Device{cameraSnapshot("camera-one", "xiaomi-main", true, public)}},
		catalog: []providersdk.SourceCatalogDevice{{
			Device:  cameraSnapshot("camera-one", "xiaomi-main", true, native),
			Catalog: providersdk.SourceCatalogMetadata{Complete: true, Source: "miot-spec-cache"},
		}},
	}
	manager, err := providermanager.New(provider)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)

	catalog, err := manager.SourceCatalog(ctx)
	if err != nil || len(catalog) != 1 {
		t.Fatalf("source catalog = %#v, err=%v", catalog, err)
	}
	if _, ok := catalog[0].Property("main", "native", "firmware-channel"); !ok {
		t.Fatalf("native source property was discarded: %#v", catalog[0].Device)
	}
	if _, ok := catalog[0].Property("main", "switch", "power"); !ok {
		t.Fatalf("public projection property was not retained: %#v", catalog[0].Device)
	}
}

func TestManagerNeverPublishesProviderHiddenControlSource(t *testing.T) {
	control := &controlProvider{bindingProvider: bindingProvider{
		id:      "xiaomi-hub",
		devices: []device.Device{cameraSnapshot("source-camera", "xiaomi-hub", true)},
		hidden:  []string{"source-camera"},
	}}
	manager, err := providermanager.New(control)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	items, err := manager.DiscoverDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("hidden control source was published: %#v", items)
	}
	events := make(chan device.Device, 1)
	unsubscribe := manager.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()
	control.emit(control.devices[0])
	select {
	case item := <-events:
		t.Fatalf("hidden control source emitted a snapshot: %#v", item)
	default:
	}
}

func TestManagerRejectsDuplicateControlBindingEvenWhenSourceMissing(t *testing.T) {
	camera := &bindingProvider{
		id: "camera-main",
		devices: []device.Device{
			cameraSnapshot("camera-one", "camera-main", true),
			cameraSnapshot("camera-two", "camera-main", true),
		},
		bindings: []providersdk.CapabilityBinding{
			{DeviceID: "camera-one", ProviderID: "missing-provider", SourceDeviceID: "same-source"},
			{DeviceID: "camera-two", ProviderID: "missing-provider", SourceDeviceID: "same-source"},
		},
	}
	manager, err := providermanager.New(camera)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	if _, err := manager.DiscoverDevices(ctx); err == nil {
		t.Fatal("DiscoverDevices accepted duplicate control binding")
	}
}

func TestManagerKeepsCameraWhenControlSourceIsOffline(t *testing.T) {
	privacy := device.Capability{ID: "privacy", Type: "privacy", Properties: []device.Property{{
		Definition: device.PropertyDefinition{ID: "enabled", Name: "Privacy", Type: device.ValueTypeBool, Readable: true, Writable: true},
		Value:      device.BoolValue(false),
	}}}
	camera := &bindingProvider{
		id: "camera-main", devices: []device.Device{cameraSnapshot("camera-one", "camera-main", true)},
		bindings: []providersdk.CapabilityBinding{{
			DeviceID: "camera-one", ProviderID: "xiaomi-hub", SourceDeviceID: "source-camera",
		}},
	}
	control := &controlProvider{bindingProvider: bindingProvider{
		id: "xiaomi-hub", devices: []device.Device{cameraSnapshot("source-camera", "xiaomi-hub", false, privacy)},
	}}
	manager, err := providermanager.New(camera, control)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	items, err := manager.DiscoverDevices(ctx)
	if err != nil || len(items) != 1 || !items[0].IsOnline() {
		t.Fatalf("camera with offline controls = %#v, %v", items, err)
	}
	_, err = manager.WriteProperty(ctx, providersdk.PropertyWriteRequest{
		DeviceID: "camera-one", EndpointID: "main", CapabilityID: "privacy", PropertyID: "enabled", Value: device.BoolValue(true),
	})
	if !errors.Is(err, providersdk.ErrProviderUnavailable) {
		t.Fatalf("offline delegated write error = %v", err)
	}
}

func TestManagerApplyActivatesCameraControlBindingWithoutRestart(t *testing.T) {
	media := device.Capability{ID: "media", Type: "media"}
	privacy := device.Capability{ID: "privacy", Type: "privacy", Properties: []device.Property{{
		Definition: device.PropertyDefinition{ID: "enabled", Name: "Privacy", Type: device.ValueTypeBool, Readable: true, Writable: true},
		Value:      device.BoolValue(false),
	}}}
	camera := &bindingProvider{
		id: "camera-main", devices: []device.Device{cameraSnapshot("camera-one", "camera-main", true, media)},
	}
	control := &controlProvider{bindingProvider: bindingProvider{
		id: "xiaomi-hub", devices: []device.Device{cameraSnapshot("source-camera", "xiaomi-hub", true, privacy)},
	}}
	manager, err := providermanager.New(camera, control)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	if items, err := manager.DiscoverDevices(ctx); err != nil || len(items) != 2 {
		t.Fatalf("initial devices = %#v, %v", items, err)
	}
	replacement := &bindingProvider{
		id: "camera-main", devices: camera.devices,
		bindings: []providersdk.CapabilityBinding{{
			DeviceID: "camera-one", ProviderID: "xiaomi-hub", SourceDeviceID: "source-camera",
		}},
	}
	if err := manager.Apply(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.WriteProperty(ctx, providersdk.PropertyWriteRequest{
		DeviceID: "camera-one", EndpointID: "main", CapabilityID: "privacy", PropertyID: "enabled", Value: device.BoolValue(true),
	}); err != nil {
		t.Fatalf("hot-applied delegated write = %v", err)
	}
	if control.lastWrite.DeviceID != "source-camera" {
		t.Fatalf("hot-applied request = %#v", control.lastWrite)
	}
}

func TestManagerProjectsControlSnapshotWithoutPublishingHiddenSource(t *testing.T) {
	privacy := device.Capability{ID: "privacy", Type: "privacy", Properties: []device.Property{{
		Definition: device.PropertyDefinition{ID: "enabled", Name: "Privacy", Type: device.ValueTypeBool, Readable: true, Writable: true},
		Value:      device.BoolValue(false),
	}}}
	camera := &bindingProvider{
		id: "camera-main", devices: []device.Device{cameraSnapshot("camera-one", "camera-main", true)},
		bindings: []providersdk.CapabilityBinding{{
			DeviceID: "camera-one", ProviderID: "xiaomi-hub", SourceDeviceID: "source-camera",
		}},
	}
	control := &controlProvider{bindingProvider: bindingProvider{
		id: "xiaomi-hub", devices: []device.Device{cameraSnapshot("source-camera", "xiaomi-hub", true, privacy)},
	}}
	manager, err := providermanager.New(camera, control)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	if _, err := manager.DiscoverDevices(ctx); err != nil {
		t.Fatal(err)
	}
	events := make(chan device.Device, 1)
	unsubscribe := manager.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()
	availabilityEvents := make(chan providersdk.CapabilityAvailability, 1)
	unsubscribeAvailability := manager.SubscribeCapabilityAvailability(func(item providersdk.CapabilityAvailability) {
		availabilityEvents <- item
	})
	defer unsubscribeAvailability()
	offline := control.devices[0].Clone()
	offline.SetOnline(false)
	offline.SetProperty("main", "privacy", "enabled", device.BoolValue(true))
	control.emit(offline)
	select {
	case projected := <-events:
		property, ok := projected.Property("main", "privacy", "enabled")
		if projected.ID != "camera-one" || projected.ProviderID != "camera-main" || !projected.IsOnline() ||
			!ok || property.Value.Bool == nil || !*property.Value.Bool {
			t.Fatalf("projected control snapshot = %#v", projected)
		}
	default:
		t.Fatal("control update was not projected to the canonical camera")
	}
	select {
	case availability := <-availabilityEvents:
		if availability.DeviceID != "camera-one" || availability.ProviderID != "camera-main" ||
			availability.EndpointID != "main" || availability.CapabilityID != "privacy" || availability.Available {
			t.Fatalf("capability availability = %#v", availability)
		}
	default:
		t.Fatal("control capability availability was not published")
	}
	_, err = manager.WriteProperty(ctx, providersdk.PropertyWriteRequest{
		DeviceID: "camera-one", EndpointID: "main", CapabilityID: "privacy", PropertyID: "enabled", Value: device.BoolValue(true),
	})
	if !errors.Is(err, providersdk.ErrProviderUnavailable) {
		t.Fatalf("write after control source went offline = %v", err)
	}
}

func (p *interestedProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "interest-test", Name: p.id}
}
func (*interestedProvider) Capabilities() providersdk.Capabilities { return providersdk.Capabilities{} }
func (*interestedProvider) Initialize(context.Context) error       { return nil }
func (*interestedProvider) Close(context.Context) error            { return nil }
func (p *interestedProvider) SetPropertyInterests(interests []providersdk.PropertyInterest) {
	p.interests = append([]providersdk.PropertyInterest(nil), interests...)
}

type transientProvider struct {
	inner   *virtual.Provider
	handler func(providersdk.DeviceEvent)
}

func (p *transientProvider) Manifest() providersdk.Manifest         { return p.inner.Manifest() }
func (p *transientProvider) Capabilities() providersdk.Capabilities { return p.inner.Capabilities() }
func (p *transientProvider) Initialize(ctx context.Context) error   { return p.inner.Initialize(ctx) }
func (p *transientProvider) Close(ctx context.Context) error        { return p.inner.Close(ctx) }
func (p *transientProvider) DiscoverDevices(ctx context.Context) ([]device.Device, error) {
	return p.inner.DiscoverDevices(ctx)
}
func (p *transientProvider) SubscribeDeviceEvents(handler func(providersdk.DeviceEvent)) func() {
	p.handler = handler
	return func() { p.handler = nil }
}
func (*transientProvider) ProviderDiagnostics() map[string]string {
	return map[string]string{"cloudMqttState": "connected"}
}
func (p *transientProvider) emit(event providersdk.DeviceEvent) {
	if p.handler != nil {
		p.handler(event)
	}
}

func (p failingProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "test", Name: "Failing"}
}
func (failingProvider) Capabilities() providersdk.Capabilities { return providersdk.Capabilities{} }
func (failingProvider) Initialize(context.Context) error       { return errors.New("connection refused") }
func (failingProvider) Close(context.Context) error            { return nil }

func TestManagerRoutesAndRetainsPropertyInterestsAcrossReplacement(t *testing.T) {
	ctx := context.Background()
	first := &interestedProvider{id: "source-main"}
	manager, err := providermanager.New(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	interest := providersdk.PropertyInterest{ProviderID: "source-main", DeviceID: "device-1", EndpointID: "raw", CapabilityID: "service", PropertyID: "value"}
	manager.SetPropertyInterests([]providersdk.PropertyInterest{interest, {ProviderID: "other", DeviceID: "ignored", EndpointID: "raw", CapabilityID: "service", PropertyID: "value"}})
	if len(first.interests) != 1 || first.interests[0] != interest {
		t.Fatalf("first interests=%#v", first.interests)
	}

	replacement := &interestedProvider{id: "source-main"}
	if err := manager.Apply(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	if len(replacement.interests) != 1 || replacement.interests[0] != interest {
		t.Fatalf("replacement interests=%#v", replacement.interests)
	}

	manager.SetPropertyInterests(nil)
	if len(replacement.interests) != 0 {
		t.Fatalf("cleared interests=%#v", replacement.interests)
	}
}

type flakyProvider struct{ attempts atomic.Int32 }

type exclusiveConnection struct{ active atomic.Int32 }

type exclusiveProvider struct {
	id         string
	connection *exclusiveConnection
	fail       bool
}

func (p *exclusiveProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "exclusive-test", Name: p.id}
}
func (*exclusiveProvider) Capabilities() providersdk.Capabilities { return providersdk.Capabilities{} }
func (p *exclusiveProvider) Initialize(context.Context) error {
	if p.fail {
		return errors.New("replacement connection failed")
	}
	if p.connection.active.Add(1) != 1 {
		p.connection.active.Add(-1)
		return errors.New("duplicate provider connection")
	}
	return nil
}
func (p *exclusiveProvider) Close(context.Context) error {
	p.connection.active.CompareAndSwap(1, 0)
	return nil
}

type strictRetryProvider struct {
	active            atomic.Bool
	initializations   atomic.Int32
	closes            atomic.Int32
	discoveryFailures atomic.Int32
}

func (*strictRetryProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: "strict-retry", Type: "test", Name: "Strict retry"}
}
func (*strictRetryProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true}
}
func (p *strictRetryProvider) Initialize(context.Context) error {
	if !p.active.CompareAndSwap(false, true) {
		return errors.New("provider is already initialized")
	}
	p.initializations.Add(1)
	return nil
}
func (p *strictRetryProvider) Close(context.Context) error {
	p.active.Store(false)
	p.closes.Add(1)
	return nil
}
func (p *strictRetryProvider) DiscoverDevices(context.Context) ([]device.Device, error) {
	if p.discoveryFailures.CompareAndSwap(1, 0) {
		return nil, errors.New("temporary discovery failure")
	}
	return nil, nil
}

type liveProvider struct {
	id           string
	name         string
	items        []device.Device
	discoveryErr error
	initialized  atomic.Int32
	reconfigured atomic.Int32
}

func (p *liveProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "live-test", Name: p.name}
}
func (*liveProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true}
}
func (p *liveProvider) Initialize(context.Context) error {
	p.initialized.Add(1)
	return nil
}
func (*liveProvider) Close(context.Context) error { return nil }
func (p *liveProvider) DiscoverDevices(context.Context) ([]device.Device, error) {
	if p.discoveryErr != nil {
		return nil, p.discoveryErr
	}
	result := make([]device.Device, len(p.items))
	for index := range p.items {
		result[index] = p.items[index].Clone()
	}
	return result, nil
}
func (p *liveProvider) Reconfigure(_ context.Context, replacement providersdk.Provider) (bool, error) {
	next, ok := replacement.(*liveProvider)
	if !ok {
		return false, nil
	}
	p.name, p.items, p.discoveryErr = next.name, next.items, next.discoveryErr
	p.reconfigured.Add(1)
	return true, nil
}

func (*flakyProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: "flaky", Type: "test", Name: "Flaky"}
}
func (*flakyProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true}
}
func (p *flakyProvider) Initialize(context.Context) error {
	if p.attempts.Add(1) == 1 {
		return errors.New("temporary failure")
	}
	return nil
}
func (*flakyProvider) Close(context.Context) error                              { return nil }
func (*flakyProvider) DiscoverDevices(context.Context) ([]device.Device, error) { return nil, nil }

func TestManagerDiscoversRoutesEventsAndWrites(t *testing.T) {
	ctx := context.Background()
	manager, err := providermanager.New(virtual.NewProvider())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := manager.DiscoverDevices(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("DiscoverDevices() = %#v, %v", items, err)
	}
	events := make(chan device.Device, 1)
	unsubscribe := manager.Subscribe(func(item device.Device) { events <- item })
	updated, err := manager.WriteProperty(ctx, providersdk.PropertyWriteRequest{
		DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProviderID != "virtual-main" {
		t.Fatalf("provider id = %q", updated.ProviderID)
	}
	property, err := manager.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", PropertyID: "power"})
	if err != nil || property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatalf("ReadProperty() = %#v, %v", property, err)
	}
	select {
	case event := <-events:
		if event.ProviderID != "virtual-main" {
			t.Fatalf("event provider id = %q", event.ProviderID)
		}
	default:
		t.Fatal("provider event was not forwarded")
	}
	unsubscribe()
	infos := manager.ProviderInfos()
	if len(infos) != 1 || infos[0].Status != "running" {
		t.Fatalf("ProviderInfos() = %#v", infos)
	}
	if err := manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if manager.ProviderInfos()[0].Status != "stopped" {
		t.Fatal("provider was not marked stopped")
	}
}

func TestManagerRoutesTransientEventsOnlyForOwnedDevices(t *testing.T) {
	provider := &transientProvider{inner: virtual.NewProvider()}
	manager, err := providermanager.New(provider)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	if _, err := manager.DiscoverDevices(ctx); err != nil {
		t.Fatal(err)
	}
	if infos := manager.ProviderInfos(); len(infos) != 1 || infos[0].Diagnostics["cloudMqttState"] != "connected" {
		t.Fatalf("runtime diagnostics=%#v", infos)
	}
	events := make(chan providersdk.DeviceEvent, 1)
	unsubscribe := manager.SubscribeDeviceEvents(func(event providersdk.DeviceEvent) { events <- event })
	defer unsubscribe()
	provider.emit(providersdk.DeviceEvent{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", EventID: "pressed", Payload: []byte(`{"value":true}`), ObservedAt: time.Now().UTC(), Sequence: 1})
	select {
	case event := <-events:
		if event.ProviderID != "virtual-main" || event.DeviceID != "virtual-switch-1" {
			t.Fatalf("event=%#v", event)
		}
	default:
		t.Fatal("owned transient event was not forwarded")
	}
	provider.emit(providersdk.DeviceEvent{DeviceID: "unknown", EndpointID: "main", CapabilityID: "switch", EventID: "pressed", Payload: []byte(`{}`), ObservedAt: time.Now().UTC(), Sequence: 2})
	select {
	case event := <-events:
		t.Fatalf("unknown device event was forwarded: %#v", event)
	default:
	}
}

func TestManagerRejectsDuplicateProviderAndUnknownDevice(t *testing.T) {
	if _, err := providermanager.New(virtual.NewProvider(), virtual.NewProvider()); err == nil {
		t.Fatal("duplicate provider id was accepted")
	}
	manager, _ := providermanager.New(virtual.NewProvider())
	manager.Initialize(context.Background())
	manager.DiscoverDevices(context.Background())
	if _, err := manager.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: "missing"}); err != providersdk.ErrDeviceNotFound {
		t.Fatalf("unknown device error = %v", err)
	}
}

func TestManagerHotAppliesAndRemovesProvider(t *testing.T) {
	ctx := context.Background()
	manager, _ := providermanager.New()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	events := make(chan device.Device, 4)
	unsubscribe := manager.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()
	if err := manager.Apply(ctx, virtual.NewProviderWithIdentity("virtual-lab", "Lab")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case item := <-events:
			if !item.Online || item.ProviderID != "virtual-lab" {
				t.Fatalf("apply event = %#v", item)
			}
		case <-time.After(time.Second):
			t.Fatal("missing apply event")
		}
	}
	if err := manager.Remove(ctx, "virtual-lab"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case item := <-events:
			if item.Online || !item.Removed {
				t.Fatalf("remove event is not a tombstone = %#v", item)
			}
		case <-time.After(time.Second):
			t.Fatal("missing offline event")
		}
	}
	if len(manager.ProviderInfos()) != 0 {
		t.Fatal("provider still registered")
	}
}

func TestManagerUsesLiveReconfigurationWithoutInitializingReplacement(t *testing.T) {
	ctx := context.Background()
	virtualDevices, err := virtual.NewProvider().DiscoverDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current := &liveProvider{id: "live-main", name: "Current", items: virtualDevices[:1]}
	manager, err := providermanager.New(current)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DiscoverDevices(ctx); err != nil {
		t.Fatal(err)
	}
	events := make(chan device.Device, 2)
	unsubscribe := manager.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()
	replacement := &liveProvider{id: "live-main", name: "Updated", items: nil}
	if err := manager.Apply(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	if current.initialized.Load() != 1 || replacement.initialized.Load() != 0 || current.reconfigured.Load() != 1 {
		t.Fatalf("initialize/reconfigure counts current=%d replacement=%d reconfigured=%d", current.initialized.Load(), replacement.initialized.Load(), current.reconfigured.Load())
	}
	select {
	case item := <-events:
		if !item.Removed || item.Online {
			t.Fatalf("removed event = %#v", item)
		}
	case <-time.After(time.Second):
		t.Fatal("missing removed-device event")
	}
	info := manager.ProviderInfos()[0]
	if info.Manifest.Name != "Updated" || info.Status != "running" {
		t.Fatalf("runtime info = %#v", info)
	}
}

func TestManagerNeverOverlapsConnectionsWhenReplacingProvider(t *testing.T) {
	ctx := context.Background()
	connection := &exclusiveConnection{}
	current := &exclusiveProvider{id: "exclusive", connection: connection}
	manager, err := providermanager.New(current)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	replacement := &exclusiveProvider{id: "exclusive", connection: connection}
	if err := manager.Apply(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	if connection.active.Load() != 1 {
		t.Fatalf("active connections = %d", connection.active.Load())
	}
}

func TestManagerRestoresPreviousConnectionWhenReplacementFails(t *testing.T) {
	ctx := context.Background()
	connection := &exclusiveConnection{}
	current := &exclusiveProvider{id: "exclusive", connection: connection}
	manager, err := providermanager.New(current)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	replacement := &exclusiveProvider{id: "exclusive", connection: connection, fail: true}
	if err := manager.Apply(ctx, replacement); err == nil {
		t.Fatal("failed replacement was accepted")
	}
	info := manager.ProviderInfos()[0]
	if connection.active.Load() != 1 || info.Status != "running" {
		t.Fatalf("active=%d info=%#v", connection.active.Load(), info)
	}
}

func TestManagerRecoversErroredLiveProviderThroughReconfiguration(t *testing.T) {
	ctx := context.Background()
	current := &liveProvider{id: "live-main", name: "Current", discoveryErr: errors.New("temporary discovery failure")}
	manager, err := providermanager.New(current)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DiscoverDevices(ctx); err != nil {
		t.Fatal(err)
	}
	if status := manager.ProviderInfos()[0].Status; status != "error" {
		t.Fatalf("status after discovery failure = %q", status)
	}
	replacement := &liveProvider{id: "live-main", name: "Recovered"}
	if err := manager.Apply(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	info := manager.ProviderInfos()[0]
	if info.Status != "running" || info.Manifest.Name != "Recovered" {
		t.Fatalf("runtime info = %#v", info)
	}
	if current.reconfigured.Load() != 1 || replacement.initialized.Load() != 0 {
		t.Fatalf("reconfigured=%d replacement initializes=%d", current.reconfigured.Load(), replacement.initialized.Load())
	}
}

func TestManagerIsolatesProviderInitializationFailure(t *testing.T) {
	manager, err := providermanager.New(failingProvider{id: "broken"}, virtual.NewProvider())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatalf("manager initialization should isolate provider failure: %v", err)
	}
	infos := manager.ProviderInfos()
	if len(infos) != 2 || infos[0].Status != "error" || infos[0].Error != "connection refused" || infos[1].Status != "running" {
		t.Fatalf("infos = %#v", infos)
	}
	items, err := manager.DiscoverDevices(context.Background())
	if err != nil || len(items) != 2 {
		t.Fatalf("healthy discovery = %#v, %v", items, err)
	}
}

func TestManagerPausesAuthenticationRequiredProviderWithoutRetry(t *testing.T) {
	provider := &authenticationRequiredProvider{id: "cloud-auth"}
	manager, err := providermanager.New(provider, virtual.NewProvider())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	infos := manager.ProviderInfos()
	if len(infos) != 2 || infos[0].Status != "auth_required" || infos[0].NextRetryAt != nil {
		t.Fatalf("infos = %#v", infos)
	}
	initializations := provider.initializations.Load()
	time.Sleep(1200 * time.Millisecond)
	if provider.initializations.Load() != initializations {
		t.Fatalf("authentication challenge was retried: initializations=%d -> %d", initializations, provider.initializations.Load())
	}
	if _, ok := manager.ProviderAny(provider.id); !ok {
		t.Fatal("auth-required provider was not retained for challenge recovery")
	}
}

func TestManagerAutomaticallyRetriesFailedProvider(t *testing.T) {
	provider := &flakyProvider{}
	manager, _ := providermanager.New(provider)
	defer manager.Close(context.Background())
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	initial := manager.ProviderInfos()[0]
	if initial.Status != "error" || initial.NextRetryAt == nil {
		t.Fatalf("initial = %#v", initial)
	}
	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		info := manager.ProviderInfos()[0]
		if info.Status == "running" {
			if info.RetryCount != 1 || info.NextRetryAt != nil {
				t.Fatalf("recovered = %#v", info)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("provider did not recover: %#v", manager.ProviderInfos()[0])
}

func TestManagerClosesActiveProviderBeforeRetryInitialization(t *testing.T) {
	provider := &strictRetryProvider{}
	provider.discoveryFailures.Store(1)
	manager, err := providermanager.New(provider)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DiscoverDevices(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		info := manager.ProviderInfos()[0]
		if info.Status == "running" && provider.initializations.Load() == 2 {
			if provider.closes.Load() != 1 {
				t.Fatalf("close count = %d", provider.closes.Load())
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("provider did not recover: info=%#v initializations=%d closes=%d", manager.ProviderInfos()[0], provider.initializations.Load(), provider.closes.Load())
}
