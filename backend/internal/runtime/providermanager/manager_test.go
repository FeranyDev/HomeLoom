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
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
)

type failingProvider struct{ id string }

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
