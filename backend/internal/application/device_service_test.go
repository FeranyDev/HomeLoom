package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	domaincommand "github.com/feranydev/homeloom/backend/internal/domain/command"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

type silentProvider struct{ inner *virtual.Provider }

type removalEventProvider struct {
	inner   *virtual.Provider
	handler func(device.Device)
}

func (p *removalEventProvider) Manifest() providersdk.Manifest { return p.inner.Manifest() }
func (p *removalEventProvider) Capabilities() providersdk.Capabilities {
	return p.inner.Capabilities()
}
func (p *removalEventProvider) Initialize(ctx context.Context) error {
	return p.inner.Initialize(ctx)
}
func (p *removalEventProvider) Close(ctx context.Context) error { return p.inner.Close(ctx) }
func (p *removalEventProvider) DiscoverDevices(ctx context.Context) ([]device.Device, error) {
	return p.inner.DiscoverDevices(ctx)
}
func (p *removalEventProvider) Subscribe(handler func(device.Device)) func() {
	p.handler = handler
	return func() { p.handler = nil }
}
func (p *removalEventProvider) emit(item device.Device) { p.handler(item) }

type transientApplicationProvider struct {
	inner   *virtual.Provider
	handler func(providersdk.DeviceEvent)
}

func (p *transientApplicationProvider) Manifest() providersdk.Manifest { return p.inner.Manifest() }
func (p *transientApplicationProvider) Capabilities() providersdk.Capabilities {
	return p.inner.Capabilities()
}
func (p *transientApplicationProvider) Initialize(ctx context.Context) error {
	return p.inner.Initialize(ctx)
}
func (p *transientApplicationProvider) Close(ctx context.Context) error { return p.inner.Close(ctx) }
func (p *transientApplicationProvider) DiscoverDevices(ctx context.Context) ([]device.Device, error) {
	return p.inner.DiscoverDevices(ctx)
}
func (p *transientApplicationProvider) SubscribeDeviceEvents(handler func(providersdk.DeviceEvent)) func() {
	p.handler = handler
	return func() { p.handler = nil }
}
func (p *transientApplicationProvider) emit(event providersdk.DeviceEvent) {
	if p.handler != nil {
		p.handler(event)
	}
}

type blockingWriteProvider struct {
	inner   *virtual.Provider
	entered chan string
	release map[string]chan struct{}
}

type skewedSnapshotProvider struct{ inner *virtual.Provider }

type devicePreferenceMetrics struct {
	mu       sync.Mutex
	disabled map[string]bool
}

func (p *devicePreferenceMetrics) DatabaseOperationMetrics() (uint64, time.Duration, time.Duration) {
	return 0, 0, 0
}
func (p *devicePreferenceMetrics) ListDisabledDeviceIDs(context.Context) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]string, 0)
	for id, disabled := range p.disabled {
		if disabled {
			result = append(result, id)
		}
	}
	return result, nil
}
func (p *devicePreferenceMetrics) SetDeviceDisabled(_ context.Context, id string, disabled bool) error {
	p.mu.Lock()
	p.disabled[id] = disabled
	p.mu.Unlock()
	return nil
}

type integerSnapshotProvider struct{}
type polledSnapshotProvider struct{ integerSnapshotProvider }
type mixedTransportSnapshotProvider struct{ integerSnapshotProvider }

func (*integerSnapshotProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: "integer", Type: "test", Name: "Integer", Version: "1"}
}
func (*integerSnapshotProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true}
}
func (*integerSnapshotProvider) Initialize(context.Context) error { return nil }
func (*integerSnapshotProvider) Close(context.Context) error      { return nil }
func (*integerSnapshotProvider) DiscoverDevices(context.Context) ([]device.Device, error) {
	maximum := 10.0
	item := device.Device{SchemaVersion: device.SchemaVersion, ID: "counter", ProviderID: "integer", Name: "Counter", Type: device.Type("counter"), LastUpdateAt: time.Now().UTC(), Endpoints: []device.Endpoint{{ID: "main", Name: "Main", Type: "counter", Capabilities: []device.Capability{{ID: "counter", Type: "counter", Properties: []device.Property{{Definition: device.PropertyDefinition{ID: "value", Name: "Value", Type: device.ValueTypeInt, Readable: true, Writable: true, Max: &maximum}, Value: device.IntValue(7)}}}}}}}
	item.SetOnline(true)
	return []device.Device{item}, nil
}

func (p *polledSnapshotProvider) DiscoverDevices(ctx context.Context) ([]device.Device, error) {
	items, err := p.integerSnapshotProvider.DiscoverDevices(ctx)
	if err == nil && len(items) > 0 {
		items[0].StateTransport = device.StateTransportCloudHTTP
	}
	return items, err
}

func (p *mixedTransportSnapshotProvider) DiscoverDevices(ctx context.Context) ([]device.Device, error) {
	items, err := p.integerSnapshotProvider.DiscoverDevices(ctx)
	if err == nil && len(items) > 0 {
		items[0].StateTransport = device.StateTransportCloudHTTP
		items[0].Endpoints[0].Capabilities[0].Properties[0].StateTransport = device.StateTransportCloudMQTT
		second := items[0].Endpoints[0].Capabilities[0].Properties[0]
		second.Definition.ID, second.StateTransport = "calibration", device.StateTransportCloudHTTP
		items[0].Endpoints[0].Capabilities[0].Properties = append(items[0].Endpoints[0].Capabilities[0].Properties, second)
	}
	return items, err
}

func (p *skewedSnapshotProvider) Manifest() providersdk.Manifest { return p.inner.Manifest() }
func (p *skewedSnapshotProvider) Capabilities() providersdk.Capabilities {
	return p.inner.Capabilities()
}
func (p *skewedSnapshotProvider) Initialize(ctx context.Context) error {
	return p.inner.Initialize(ctx)
}
func (p *skewedSnapshotProvider) Close(ctx context.Context) error { return p.inner.Close(ctx) }
func (p *skewedSnapshotProvider) DiscoverDevices(ctx context.Context) ([]device.Device, error) {
	items, err := p.inner.DiscoverDevices(ctx)
	if err == nil && len(items) > 0 {
		items[0].LastUpdateAt = time.Now().UTC().Add(24 * time.Hour)
	}
	return items, err
}

func (p *silentProvider) Manifest() providersdk.Manifest         { return p.inner.Manifest() }
func (p *silentProvider) Capabilities() providersdk.Capabilities { return p.inner.Capabilities() }
func (p *silentProvider) Initialize(ctx context.Context) error   { return p.inner.Initialize(ctx) }
func (p *silentProvider) Close(ctx context.Context) error        { return p.inner.Close(ctx) }
func (p *silentProvider) DiscoverDevices(ctx context.Context) ([]device.Device, error) {
	return p.inner.DiscoverDevices(ctx)
}
func (p *silentProvider) WriteProperty(ctx context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	return p.inner.WriteProperty(ctx, request)
}

func newBlockingWriteProvider(t *testing.T) *blockingWriteProvider {
	t.Helper()
	inner, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "ordered", Name: "Ordered", Config: []byte(`{"devices":[{"id":"switch-a","type":"switch"},{"id":"switch-b","type":"switch"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	return &blockingWriteProvider{inner: inner, entered: make(chan string, 8), release: map[string]chan struct{}{"switch-a": make(chan struct{}, 8), "switch-b": make(chan struct{}, 8)}}
}

func (p *blockingWriteProvider) Manifest() providersdk.Manifest { return p.inner.Manifest() }
func (p *blockingWriteProvider) Capabilities() providersdk.Capabilities {
	return p.inner.Capabilities()
}
func (p *blockingWriteProvider) Initialize(ctx context.Context) error { return p.inner.Initialize(ctx) }
func (p *blockingWriteProvider) Close(ctx context.Context) error      { return p.inner.Close(ctx) }
func (p *blockingWriteProvider) DiscoverDevices(ctx context.Context) ([]device.Device, error) {
	return p.inner.DiscoverDevices(ctx)
}
func (p *blockingWriteProvider) Subscribe(handler func(device.Device)) func() {
	return p.inner.Subscribe(handler)
}
func (p *blockingWriteProvider) WriteProperty(ctx context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	select {
	case p.entered <- request.DeviceID:
	case <-ctx.Done():
		return device.Device{}, ctx.Err()
	}
	select {
	case <-p.release[request.DeviceID]:
		return p.inner.WriteProperty(ctx, request)
	case <-ctx.Done():
		return device.Device{}, ctx.Err()
	}
}

func (p *blockingWriteProvider) ExecuteCommand(ctx context.Context, request providersdk.CommandRequest) (device.Device, error) {
	select {
	case p.entered <- request.DeviceID:
	case <-ctx.Done():
		return device.Device{}, ctx.Err()
	}
	select {
	case <-p.release[request.DeviceID]:
		return p.inner.ExecuteCommand(ctx, request)
	case <-ctx.Done():
		return device.Device{}, ctx.Err()
	}
}

func TestDeviceServiceRoutesProviderEvents(t *testing.T) {
	provider := virtual.NewProvider()
	service := application.NewDeviceService(provider)
	defer service.Close()
	notified := make(chan device.Device, 1)
	unsubscribe := service.Subscribe(func(item device.Device) { notified <- item })
	defer unsubscribe()
	if _, err := service.SetPower(context.Background(), "virtual-switch-1", true); err != nil {
		t.Fatalf("SetPower() error = %v", err)
	}
	select {
	case item := <-notified:
		property, ok := item.Property("main", "switch", "power")
		if !ok || property.Value.Bool == nil || !*property.Value.Bool {
			t.Fatal("subscriber received wrong state")
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber was not notified")
	}
	items, err := service.List(context.Background())
	if err != nil || len(items) != 2 {
		t.Fatalf("List() = %#v, %v", items, err)
	}
	states := service.States("virtual-switch-1")
	if len(states) != 1 || states[0].Version != 2 {
		t.Fatalf("States() = %#v", states)
	}
}

func TestDeviceServiceRemovesProviderTombstonesFromRegistry(t *testing.T) {
	provider := &removalEventProvider{inner: virtual.NewProvider()}
	service := application.NewDeviceService(provider)
	defer service.Close()
	notified := make(chan device.Device, 1)
	unsubscribe := service.Subscribe(func(item device.Device) { notified <- item })
	defer unsubscribe()

	items, err := service.List(context.Background())
	if err != nil || len(items) == 0 {
		t.Fatalf("List() = %#v, %v", items, err)
	}
	removed := items[0]
	removed.Removed = true
	removed.SetOnline(false)
	provider.emit(removed)

	select {
	case tombstone := <-notified:
		if !tombstone.Removed || tombstone.ID != removed.ID {
			t.Fatalf("notification = %#v", tombstone)
		}
	case <-time.After(time.Second):
		t.Fatal("removal tombstone was not published")
	}
	items, err = service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == removed.ID {
			t.Fatalf("removed device is still registered: %#v", item)
		}
	}
}

func TestDeviceServiceDeliversTransientEventsOutsideStateStore(t *testing.T) {
	provider := &transientApplicationProvider{inner: virtual.NewProvider()}
	service := application.NewDeviceService(provider)
	defer service.Close()
	events := make(chan providersdk.DeviceEvent, 1)
	unsubscribe := service.SubscribeDeviceEvents(func(event providersdk.DeviceEvent) { events <- event })
	defer unsubscribe()
	provider.emit(providersdk.DeviceEvent{ProviderID: "virtual-main", DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", EventID: "pressed", Payload: []byte(`{"value":true}`), ObservedAt: time.Now().UTC(), Sequence: 1})
	select {
	case event := <-events:
		if event.EventID != "pressed" || event.DeviceID != "virtual-switch-1" {
			t.Fatalf("event=%#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("transient event was not delivered")
	}
	if metrics := service.Metrics(); metrics.DeviceEventsReceived != 1 || metrics.DeviceEventsDropped != 0 {
		t.Fatalf("metrics=%#v", metrics)
	}
}

func TestDeviceServiceMarksCloudHTTPSnapshotsAsPolled(t *testing.T) {
	service := application.NewDeviceService(&polledSnapshotProvider{})
	defer service.Close()
	states := service.States("counter")
	if len(states) != 1 || states[0].Source != domainstate.SourcePolled || states[0].Quality != domainstate.QualityPolled {
		t.Fatalf("cloud HTTP states=%#v", states)
	}
}

func TestDeviceServiceUsesPerPropertyStateTransport(t *testing.T) {
	service := application.NewDeviceService(&mixedTransportSnapshotProvider{})
	defer service.Close()
	states := service.States("counter")
	if len(states) != 2 {
		t.Fatalf("states=%#v", states)
	}
	byProperty := map[string]domainstate.StateValue{}
	for _, state := range states {
		byProperty[state.Key.PropertyID] = state
	}
	if byProperty["value"].Source != domainstate.SourceReported || byProperty["calibration"].Source != domainstate.SourcePolled {
		t.Fatalf("mixed transport states=%#v", states)
	}
}

func TestDeviceServiceIgnoresRepeatedAndOutOfOrderProviderSnapshots(t *testing.T) {
	provider := virtual.NewProvider()
	service := application.NewDeviceService(provider)
	defer service.Close()
	newer := uint64(10)
	if _, err := service.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: "virtual-switch-1", Sequence: &newer, Properties: []providersdk.PropertyWriteRequest{{EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)}}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		states := service.States("virtual-switch-1")
		if len(states) == 1 && states[0].Sequence == newer {
			break
		}
		time.Sleep(time.Millisecond)
	}
	older := uint64(2)
	if _, err := service.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: "virtual-switch-1", Sequence: &older, Repeat: 2, Properties: []providersdk.PropertyWriteRequest{{EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(false)}}}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for service.Metrics().ProviderEventsIgnored < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	states := service.States("virtual-switch-1")
	items, _ := service.List(context.Background())
	property, _ := items[0].Property("main", "switch", "power")
	if len(states) != 1 || states[0].Sequence != newer || states[0].Value.Bool == nil || !*states[0].Value.Bool || property.Value.Bool == nil || !*property.Value.Bool || service.Metrics().ProviderEventsIgnored != 2 {
		t.Fatalf("states = %#v, registry = %#v, metrics = %#v", states, property, service.Metrics())
	}
}

func TestDeviceDisablePersistsAndProviderEventsCannotReviveIt(t *testing.T) {
	provider := virtual.NewProvider()
	preferences := &devicePreferenceMetrics{disabled: map[string]bool{"virtual-switch-1": true}}
	service := application.NewDeviceService(provider, preferences)
	defer service.Close()
	if err := service.LoadDevicePreferences(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, _ := service.List(context.Background())
	var current device.Device
	for _, item := range items {
		if item.ID == "virtual-switch-1" {
			current = item
		}
	}
	if !current.Disabled || current.Online {
		t.Fatalf("disabled device = %#v", current)
	}
	if _, err := provider.SetPower(context.Background(), current.ID, true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	items, _ = service.List(context.Background())
	for _, item := range items {
		if item.ID == current.ID && (!item.Disabled || item.Online) {
			t.Fatalf("provider revived disabled device: %#v", item)
		}
	}
	enabled, err := service.SetDeviceEnabled(context.Background(), current.ID, true)
	if err != nil || enabled.Disabled || !enabled.Online || preferences.disabled[current.ID] {
		t.Fatalf("enabled device = %#v, preferences = %#v, %v", enabled, preferences.disabled, err)
	}
	disabled, err := service.SetDeviceEnabled(context.Background(), current.ID, false)
	if err != nil || !disabled.Disabled || disabled.Online || !preferences.disabled[current.ID] {
		t.Fatalf("disabled device = %#v, preferences = %#v, %v", disabled, preferences.disabled, err)
	}
	if _, _, err := service.ExecutePower(context.Background(), current.ID, false); !errors.Is(err, application.ErrDeviceDisabled) {
		t.Fatalf("disabled write error = %v", err)
	}
}

func TestDeviceServiceDetectsAndClampsProviderClockSkew(t *testing.T) {
	service := application.NewDeviceService(&skewedSnapshotProvider{inner: virtual.NewProvider()})
	defer service.Close()
	metrics := service.Metrics()
	if metrics.ProviderClockSkewEvents != 1 || metrics.ProviderMaxClockSkewMS < float64((23*time.Hour)/time.Millisecond) {
		t.Fatalf("clock metrics = %#v", metrics)
	}
	states := service.States("virtual-switch-1")
	if len(states) != 1 || states[0].ObservedAt.After(time.Now().Add(time.Minute)) {
		t.Fatalf("clamped states = %#v", states)
	}
}

func TestDeviceServicePublishesIntegerState(t *testing.T) {
	service := application.NewDeviceService(&integerSnapshotProvider{})
	defer service.Close()
	states := service.States("counter")
	if len(states) != 1 || states[0].Value.Kind != domainstate.KindInt || states[0].Value.Int == nil || *states[0].Value.Int != 7 {
		t.Fatalf("states = %#v", states)
	}
}

func TestUnknownDeviceStartsWithoutInventedValueAndRecovers(t *testing.T) {
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "unknown-provider", Name: "Unknown", Config: []byte(`{"devices":[{"id":"pending","type":"switch","availability":"unknown"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(provider)
	defer service.Close()
	states := service.States("pending")
	powerState := func(items []domainstate.StateValue) (domainstate.StateValue, bool) {
		for _, item := range items {
			if item.Key.CapabilityID == "switch" && item.Key.PropertyID == "power" {
				return item, true
			}
		}
		return domainstate.StateValue{}, false
	}
	initial, found := powerState(states)
	if !found || initial.Known || initial.Available || initial.Quality != domainstate.QualityUnknown || initial.UnavailableReason != domainstate.UnavailableAvailabilityUnknown || initial.Value.Kind != "" {
		t.Fatalf("initial states = %#v", states)
	}
	online := device.AvailabilityOnline
	if _, err := service.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: "pending", Availability: &online}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		states = service.States("pending")
		current, found := powerState(states)
		if found && current.Known && current.Available && current.Quality == domainstate.QualityReported && current.Value.Bool != nil && current.TraceID != "" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("recovered states = %#v", states)
}

func TestDeviceServiceRejectsInvalidTypedPropertyBeforeCreatingCommand(t *testing.T) {
	service := application.NewDeviceService(&integerSnapshotProvider{})
	defer service.Close()
	_, _, err := service.ExecuteProperty(context.Background(), "counter", "main", "counter", "value", device.NumberValue(7))
	if !errors.Is(err, providersdk.ErrPropertyInvalid) || len(service.Commands()) != 0 {
		t.Fatalf("type error = %v, commands = %#v", err, service.Commands())
	}
	_, _, err = service.ExecuteProperty(context.Background(), "counter", "main", "counter", "value", device.IntValue(11))
	if !errors.Is(err, providersdk.ErrPropertyInvalid) || len(service.Commands()) != 0 {
		t.Fatalf("range error = %v, commands = %#v", err, service.Commands())
	}
}

func TestNewPropertyValueSupersedesActiveWriteAndDevicesStillRunConcurrently(t *testing.T) {
	provider := newBlockingWriteProvider(t)
	service := application.NewDeviceService(provider)
	defer service.Close()
	type result struct {
		command domaincommand.Command
		err     error
	}
	results := make(chan result, 4)
	run := func(deviceID string, value bool) {
		go func() {
			_, command, err := service.ExecutePower(context.Background(), deviceID, value)
			results <- result{command: command, err: err}
		}()
	}

	run("switch-a", true)
	if entered := <-provider.entered; entered != "switch-a" {
		t.Fatalf("first entered = %q", entered)
	}
	run("switch-a", false)
	if entered := <-provider.entered; entered != "switch-a" {
		t.Fatalf("replacement entered = %q", entered)
	}
	provider.release["switch-a"] <- struct{}{}
	first, second := <-results, <-results
	if errors.Is(second.err, application.ErrCommandSuperseded) {
		first, second = second, first
	}
	if !errors.Is(first.err, application.ErrCommandSuperseded) || first.command.Status != domaincommand.StatusSuperseded || second.err != nil {
		t.Fatalf("replacement results = %#v, %#v", first, second)
	}
	if metrics := service.Metrics(); metrics.CommandsSuperseded != 1 || metrics.CommandsStarted != 2 {
		t.Fatalf("replacement metrics = %#v", metrics)
	}

	run("switch-a", true)
	run("switch-b", true)
	seen := map[string]bool{<-provider.entered: true, <-provider.entered: true}
	if !seen["switch-a"] || !seen["switch-b"] {
		t.Fatalf("cross-device entries = %#v", seen)
	}
	provider.release["switch-a"] <- struct{}{}
	provider.release["switch-b"] <- struct{}{}
	if result := <-results; result.err != nil {
		t.Fatal(result.err)
	}
	if result := <-results; result.err != nil {
		t.Fatal(result.err)
	}
	if service.Metrics().CommandQueuePending != 0 {
		t.Fatalf("queue metrics = %#v", service.Metrics())
	}
}

func TestConcurrentIdenticalPropertyWritesShareProviderCall(t *testing.T) {
	provider := newBlockingWriteProvider(t)
	service := application.NewDeviceService(provider)
	defer service.Close()
	type result struct {
		command domaincommand.Command
		err     error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			_, command, err := service.ExecutePower(context.Background(), "switch-a", true)
			results <- result{command: command, err: err}
		}()
		if len(service.Commands()) == 0 {
			<-provider.entered
		}
	}
	select {
	case entered := <-provider.entered:
		t.Fatalf("duplicate reached provider: %s", entered)
	case <-time.After(20 * time.Millisecond):
	}
	provider.release["switch-a"] <- struct{}{}
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.command.ID != second.command.ID || first.command.Coalesced != 1 || second.command.Coalesced != 1 || service.Metrics().CommandsStarted != 1 || service.Metrics().CommandsCoalesced != 1 {
		t.Fatalf("deduplicated results = %#v, %#v, metrics = %#v", first, second, service.Metrics())
	}
}

func TestAcceptedIdenticalPropertyWriteIsReplayed(t *testing.T) {
	service := application.NewDeviceService(&silentProvider{inner: virtual.NewProvider()})
	defer service.Close()
	_, first, err := service.ExecutePower(context.Background(), "virtual-switch-1", true)
	if err != nil {
		t.Fatal(err)
	}
	_, replay, err := service.ExecutePower(context.Background(), "virtual-switch-1", true)
	if err != nil || replay.ID != first.ID || replay.Coalesced != 1 || len(service.Commands()) != 1 || service.Metrics().CommandsStarted != 1 || service.Metrics().CommandsCoalesced != 1 {
		t.Fatalf("replay = %#v, %v, commands = %#v", replay, err, service.Commands())
	}
}

func TestDeviceCommandQueueHonorsContextCancellation(t *testing.T) {
	provider := newBlockingWriteProvider(t)
	service := application.NewDeviceService(provider)
	defer service.Close()
	firstDone := make(chan error, 1)
	go func() { _, _, err := service.ExecutePower(context.Background(), "switch-a", true); firstDone <- err }()
	<-provider.entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, command, err := service.ExecuteCommand(ctx, providersdk.CommandRequest{DeviceID: "switch-a", EndpointID: "main", CapabilityID: "switch", CommandID: "toggle", IdempotencyKey: "queued-toggle"}); !errors.Is(err, context.DeadlineExceeded) || command.ID != "" {
		t.Fatalf("queued cancellation = %#v, %v", command, err)
	}
	select {
	case entered := <-provider.entered:
		t.Fatalf("cancelled command reached provider: %s", entered)
	default:
	}
	provider.release["switch-a"] <- struct{}{}
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestDeviceServiceReadsProviderProperty(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider())
	defer service.Close()
	property, err := service.ReadProperty(context.Background(), "virtual-switch-1", "main", "switch", "power")
	if err != nil || property.Value.Bool == nil || *property.Value.Bool {
		t.Fatalf("ReadProperty() = %#v, %v", property, err)
	}
	withoutReader := application.NewDeviceService(&silentProvider{inner: virtual.NewProvider()})
	defer withoutReader.Close()
	if _, err := withoutReader.ReadProperty(context.Background(), "virtual-switch-1", "main", "switch", "power"); !errors.Is(err, application.ErrPropertyUnsupported) {
		t.Fatalf("unsupported reader error = %v", err)
	}
}

func TestDeviceServiceExecutesActionCommand(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider())
	defer service.Close()
	updated, command, err := service.ExecuteCommand(context.Background(), providersdk.CommandRequest{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", CommandID: "set-power", Parameters: map[string]device.PropertyValue{"value": device.BoolValue(true)}})
	property, _ := updated.Property("main", "switch", "power")
	if err != nil || command.Kind != domaincommand.KindAction || command.CommandID != "set-power" || command.Status != domaincommand.StatusConfirmed || property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatalf("ExecuteCommand() = %#v, %#v, %v", updated, command, err)
	}
	if _, _, err := service.ExecuteCommand(context.Background(), providersdk.CommandRequest{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", CommandID: "set-power"}); !errors.Is(err, providersdk.ErrCommandInvalid) {
		t.Fatalf("invalid error = %v", err)
	}
	request := providersdk.CommandRequest{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", CommandID: "toggle", IdempotencyKey: "toggle-once"}
	_, first, err := service.ExecuteCommand(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	_, replay, err := service.ExecuteCommand(context.Background(), request)
	if err != nil || replay.ID != first.ID || service.Metrics().CommandsStarted != 2 {
		t.Fatalf("replay = %#v, %v, metrics = %#v", replay, err, service.Metrics())
	}
}

func TestConcurrentIdempotentActionExecutesOnce(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider())
	defer service.Close()
	request := providersdk.CommandRequest{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", CommandID: "toggle", IdempotencyKey: "concurrent-toggle"}
	ids := make(chan string, 20)
	errs := make(chan error, 20)
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, command, err := service.ExecuteCommand(context.Background(), request)
			if err != nil {
				errs <- err
				return
			}
			ids <- command.ID
		}()
	}
	group.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	unique := make(map[string]struct{})
	for id := range ids {
		unique[id] = struct{}{}
	}
	if len(unique) != 1 || service.Metrics().CommandsStarted != 1 {
		t.Fatalf("ids = %#v, metrics = %#v", unique, service.Metrics())
	}
}

func TestSlowSubscriberDoesNotBlockCoreDispatcher(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider())
	blocked := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	unsubscribe := service.Subscribe(func(device.Device) { once.Do(func() { close(started) }); <-blocked })
	for index := 0; index < 100; index++ {
		if _, err := service.SetPower(context.Background(), "virtual-switch-1", index%2 == 0); err != nil {
			t.Fatal(err)
		}
	}
	<-started
	deadline := time.Now().Add(time.Second)
	for service.Metrics().EventsProcessed < 100 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	metrics := service.Metrics()
	if metrics.EventsProcessed != 100 {
		t.Fatalf("processed = %d", metrics.EventsProcessed)
	}
	if metrics.TargetEventsDropped == 0 {
		t.Fatal("slow subscriber queue never reported dropped events")
	}
	close(blocked)
	unsubscribe()
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceServiceCommandIsConfirmedByProviderEvent(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider())
	defer service.Close()
	ctx := application.WithCorrelationID(context.Background(), "trace-optimistic")
	_, command, err := service.ExecutePower(ctx, "virtual-switch-1", true)
	if err != nil {
		t.Fatalf("ExecutePower() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, ok := service.Command(command.ID)
		if ok && current.Status == domaincommand.StatusConfirmed {
			if service.Metrics().CommandAverageLatencyMS <= 0 {
				t.Fatal("average command latency was not recorded")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	current, _ := service.Command(command.ID)
	t.Fatalf("command status = %s", current.Status)
}

func TestDeviceOfflineImmediatelyMarksStateStaleAndOnlineRestoresIt(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider())
	defer service.Close()
	id := "virtual-switch-1"
	offline := false
	stateEvents := make(chan domainstate.StateValue, 3)
	unsubscribe := service.SubscribeStates(func(value domainstate.StateValue) { stateEvents <- value })
	defer unsubscribe()
	if _, err := service.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: id, Online: &offline}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	var staleVersion uint64
	for time.Now().Before(deadline) {
		states := service.States(id)
		if len(states) == 1 && states[0].Quality == domainstate.QualityStale && states[0].Known && !states[0].Available && states[0].UnavailableReason == domainstate.UnavailableDeviceOffline {
			staleVersion = states[0].Version
			break
		}
		time.Sleep(time.Millisecond)
	}
	if staleVersion == 0 {
		t.Fatalf("state did not become stale: %#v", service.States(id))
	}
	select {
	case event := <-stateEvents:
		if event.Quality != domainstate.QualityStale {
			t.Fatalf("offline event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("missing stale state event")
	}
	if _, err := service.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: id, Online: &offline}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if version := service.States(id)[0].Version; version != staleVersion {
		t.Fatalf("duplicate offline changed version from %d to %d", staleVersion, version)
	}
	select {
	case event := <-stateEvents:
		t.Fatalf("duplicate offline published %#v", event)
	default:
	}
	online := true
	if _, err := service.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: id, Online: &online}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		states := service.States(id)
		if len(states) == 1 && states[0].Quality == domainstate.QualityReported && states[0].Known && states[0].Available && states[0].Version == staleVersion+1 {
			select {
			case event := <-stateEvents:
				if event.Quality != domainstate.QualityReported {
					t.Fatalf("online event = %#v", event)
				}
			case <-time.After(time.Second):
				t.Fatal("missing recovered state event")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("state did not recover: %#v", service.States(id))
}

func TestRejectedCommandRollsBackOptimisticState(t *testing.T) {
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "reject", Name: "Reject", Config: []byte(`{"rejectWrites":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(provider)
	defer service.Close()
	id := "reject-switch-1"
	_, command, err := service.ExecutePower(context.Background(), id, true)
	if err == nil || command.Status != domaincommand.StatusRejected {
		t.Fatalf("command = %#v, %v", command, err)
	}
	states := service.States(id)
	if len(states) != 1 || states[0].Quality != domainstate.QualityReported || states[0].PendingCommandID != "" || states[0].Value.Bool == nil || *states[0].Value.Bool {
		t.Fatalf("rolled back states = %#v", states)
	}
}

func TestCommandPublishesOptimisticThenReportedState(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider())
	defer service.Close()
	events := make(chan domainstate.StateValue, 4)
	unsubscribe := service.SubscribeStates(func(value domainstate.StateValue) {
		if value.Key.DeviceID == "virtual-switch-1" {
			events <- value
		}
	})
	defer unsubscribe()
	ctx := application.WithCorrelationID(context.Background(), "trace-optimistic")
	_, command, err := service.ExecutePower(ctx, "virtual-switch-1", true)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case state := <-events:
		if state.Quality != domainstate.QualityOptimistic || state.PendingCommandID != command.ID || state.TraceID != "trace-optimistic" || state.Value.Bool == nil || !*state.Value.Bool {
			t.Fatalf("optimistic = %#v", state)
		}
	case <-time.After(time.Second):
		t.Fatal("missing optimistic state")
	}
	select {
	case state := <-events:
		if state.Quality != domainstate.QualityReported || state.PendingCommandID != "" || state.Version < 3 {
			t.Fatalf("reported = %#v", state)
		}
	case <-time.After(time.Second):
		t.Fatal("missing reported state")
	}
}

func TestNewPropertyCommandSupersedesOldOptimisticCommand(t *testing.T) {
	service := application.NewDeviceService(&silentProvider{inner: virtual.NewProvider()})
	defer service.Close()
	id := "virtual-switch-1"
	_, first, err := service.ExecutePower(context.Background(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := service.ExecutePower(context.Background(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := service.Command(first.ID)
	if old.Status != domaincommand.StatusSuperseded {
		t.Fatalf("old = %#v", old)
	}
	states := service.States(id)
	if len(states) != 1 || states[0].Quality != domainstate.QualityOptimistic || states[0].PendingCommandID != second.ID || states[0].Value.Bool == nil || *states[0].Value.Bool {
		t.Fatalf("latest optimistic state = %#v", states)
	}
	if service.Metrics().CommandsSuperseded != 1 {
		t.Fatalf("metrics = %#v", service.Metrics())
	}
}
