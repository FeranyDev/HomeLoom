package providermanager_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/logicaldevice"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
)

type logicalTestProvider struct {
	id          string
	item        device.Device
	nativeID    string
	writeErr    error
	commandErr  error
	writes      int
	commands    int
	lastWrite   providersdk.PropertyWriteRequest
	lastCommand providersdk.CommandRequest
}

func (p *logicalTestProvider) ProviderDeviceID(deviceID string) (string, bool) {
	if p.nativeID == "" || deviceID != p.item.ID {
		return "", false
	}
	return p.nativeID, true
}

type identityStoreStub struct {
	providerBindings [][3]string
	topologies       []device.Device
	err              error
}

func (s *identityStoreStub) EnsureProviderDeviceIdentity(_ context.Context, providerID, providerDeviceID, deviceID string) error {
	if s.err != nil {
		return s.err
	}
	s.providerBindings = append(s.providerBindings, [3]string{providerID, providerDeviceID, deviceID})
	return nil
}

func (s *identityStoreStub) EnsureDeviceTopologyIdentity(_ context.Context, item device.Device) error {
	if s.err != nil {
		return s.err
	}
	s.topologies = append(s.topologies, item.Clone())
	return nil
}

func (p *logicalTestProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "test", Name: p.id}
}
func (*logicalTestProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyRead: true, PropertyWrite: true, Commands: true}
}
func (*logicalTestProvider) Initialize(context.Context) error { return nil }
func (*logicalTestProvider) Close(context.Context) error      { return nil }
func (p *logicalTestProvider) DiscoverDevices(context.Context) ([]device.Device, error) {
	return []device.Device{p.item.Clone()}, nil
}
func (p *logicalTestProvider) ReadProperty(_ context.Context, request providersdk.PropertyReadRequest) (device.Property, error) {
	if p.writeErr != nil {
		return device.Property{}, p.writeErr
	}
	property, ok := p.item.Property(request.EndpointID, request.CapabilityID, request.PropertyID)
	if !ok {
		return device.Property{}, providersdk.ErrPropertyUnsupported
	}
	return property, nil
}
func (p *logicalTestProvider) WriteProperty(_ context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	p.writes++
	p.lastWrite = request
	if p.writeErr != nil {
		return device.Device{}, p.writeErr
	}
	if !p.item.SetProperty(request.EndpointID, request.CapabilityID, request.PropertyID, request.Value) {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	p.item.LastUpdateAt = time.Now().UTC()
	return p.item.Clone(), nil
}
func (p *logicalTestProvider) ExecuteCommand(_ context.Context, request providersdk.CommandRequest) (device.Device, error) {
	p.commands++
	p.lastCommand = request
	if p.commandErr != nil {
		return device.Device{}, p.commandErr
	}
	return p.item.Clone(), nil
}

func logicalSwitch(id, providerID string, online bool, idempotentCommand bool) device.Device {
	item := device.Device{SchemaVersion: device.SchemaVersion, ID: id, ProviderID: providerID, Name: "客厅主灯", Type: device.TypeSwitch, HomeID: "home-main", RoomID: "room-living", LastUpdateAt: time.Now().UTC(), Endpoints: []device.Endpoint{{ID: "main", Name: "Main", Type: "main", Capabilities: []device.Capability{{ID: "switch", Type: "switch", Properties: []device.Property{{Definition: device.PropertyDefinition{ID: "power", Name: "Power", Type: device.ValueTypeBool, Readable: true, Writable: true}, Value: device.BoolValue(false)}}, Commands: []device.CommandDefinition{{ID: "identify", Name: "Identify", Idempotent: idempotentCommand}}}}}}}
	item.SetOnline(online)
	return item
}

func logicalSwitchConfig() logicaldevice.Config {
	return logicaldevice.Config{ID: "living-switch", Name: "客厅主灯", Type: device.TypeSwitch, Bindings: []logicaldevice.Binding{
		{SourceRef: logicaldevice.SourceRef{ProviderID: "local", DeviceID: "local-switch"}},
		{SourceRef: logicaldevice.SourceRef{ProviderID: "cloud", DeviceID: "cloud-switch"}, Priority: 10},
	}}
}

func TestLogicalDeviceHidesConcreteSourcesAndSafelyFallsBackPropertyWrites(t *testing.T) {
	local := &logicalTestProvider{id: "local", item: logicalSwitch("local-switch", "local", true, true)}
	cloud := &logicalTestProvider{id: "cloud", item: logicalSwitch("cloud-switch", "cloud", true, true)}
	manager, err := providermanager.New(local, cloud)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	if err := manager.SetLogicalDevices([]logicaldevice.Config{logicalSwitchConfig()}); err != nil {
		t.Fatal(err)
	}
	items, err := manager.DiscoverDevices(ctx)
	if err != nil || len(items) != 1 || items[0].ID != "living-switch" || items[0].ProviderID != logicaldevice.ProviderID {
		t.Fatalf("logical discovery = %#v, %v", items, err)
	}
	if _, err := manager.WriteProperty(ctx, providersdk.PropertyWriteRequest{DeviceID: "living-switch", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)}); err != nil {
		t.Fatal(err)
	}
	if local.writes != 1 || cloud.writes != 0 || local.lastWrite.DeviceID != "local-switch" {
		t.Fatalf("primary logical write local=%#v cloud=%#v", local, cloud)
	}
	local.writeErr = providersdk.ErrProviderUnavailable
	if _, err := manager.WriteProperty(ctx, providersdk.PropertyWriteRequest{DeviceID: "living-switch", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(false)}); err != nil {
		t.Fatal(err)
	}
	if cloud.writes != 1 || cloud.lastWrite.DeviceID != "cloud-switch" {
		t.Fatalf("fallback write local=%#v cloud=%#v", local, cloud)
	}
	local.item.SetOnline(false)
	if _, err := manager.DiscoverDevices(ctx); err != nil {
		t.Fatal(err)
	}
	explanations, exists := manager.LogicalDeviceExplanations("living-switch")
	if !exists || len(explanations) == 0 || explanations[0].Reason != "safe_fallback_provider_unavailable" {
		t.Fatalf("logical conflict explanation = %#v", explanations)
	}
}

func TestLogicalCommandFallbackRequiresIdempotencyAndDefiniteAvailabilityFailure(t *testing.T) {
	local := &logicalTestProvider{id: "local", item: logicalSwitch("local-switch", "local", true, false), commandErr: providersdk.ErrProviderUnavailable}
	cloud := &logicalTestProvider{id: "cloud", item: logicalSwitch("cloud-switch", "cloud", true, false)}
	manager, err := providermanager.New(local, cloud)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	if err := manager.SetLogicalDevices([]logicaldevice.Config{logicalSwitchConfig()}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DiscoverDevices(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = manager.ExecuteCommand(ctx, providersdk.CommandRequest{DeviceID: "living-switch", EndpointID: "main", CapabilityID: "switch", CommandID: "identify"})
	if !errors.Is(err, providersdk.ErrProviderUnavailable) {
		t.Fatalf("non-idempotent fallback error = %v", err)
	}
	if local.commands != 1 || cloud.commands != 0 {
		t.Fatalf("unsafe command fallback local=%d cloud=%d", local.commands, cloud.commands)
	}
}

func TestLogicalCandidatesRequireLocationAsWellAsName(t *testing.T) {
	local := &logicalTestProvider{id: "local", item: logicalSwitch("local-switch", "local", true, true)}
	cloud := &logicalTestProvider{id: "cloud", item: logicalSwitch("cloud-switch", "cloud", true, true)}
	cloud.item.RoomID, cloud.item.HomeID = "", ""
	manager, err := providermanager.New(local, cloud)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	candidates, err := manager.LogicalDeviceCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("name-only candidate = %#v", candidates)
	}
	cloud.item.HomeID, cloud.item.RoomID = "home-main", "room-living"
	candidates, err = manager.LogicalDeviceCandidates(ctx)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("location-backed candidates = %#v, %v", candidates, err)
	}
}

func TestLogicalDiscoveryPersistsConcreteAndLogicalStableIdentities(t *testing.T) {
	local := &logicalTestProvider{id: "local", item: logicalSwitch("local-switch", "local", true, true), nativeID: "native-local-1"}
	cloud := &logicalTestProvider{id: "cloud", item: logicalSwitch("cloud-switch", "cloud", true, true)}
	manager, err := providermanager.New(local, cloud)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	identities := &identityStoreStub{}
	manager.SetDeviceIdentityStore(identities)
	if err := manager.SetLogicalDevices([]logicaldevice.Config{logicalSwitchConfig()}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DiscoverDevices(ctx); err != nil {
		t.Fatal(err)
	}
	if len(identities.providerBindings) != 2 {
		t.Fatalf("provider identity bindings = %#v", identities.providerBindings)
	}
	if identities.providerBindings[0] != [3]string{"local", "native-local-1", "local-switch"} || identities.providerBindings[1] != [3]string{"cloud", "cloud-switch", "cloud-switch"} {
		t.Fatalf("provider identity bindings = %#v", identities.providerBindings)
	}
	if len(identities.topologies) != 3 {
		t.Fatalf("topology identities = %#v", identities.topologies)
	}
	logicalFound := false
	for _, item := range identities.topologies {
		if item.ID == "living-switch" && item.ProviderID == logicaldevice.ProviderID {
			logicalFound = true
		}
	}
	if !logicalFound {
		t.Fatalf("logical topology was not persisted: %#v", identities.topologies)
	}
}
