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

type blockingWriteProvider struct {
	inner   *virtual.Provider
	entered chan string
	release map[string]chan struct{}
}

type skewedSnapshotProvider struct{ inner *virtual.Provider }

type integerSnapshotProvider struct{}

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

func TestDeviceCommandsSerializePerDeviceAndRunAcrossDevices(t *testing.T) {
	provider := newBlockingWriteProvider(t)
	service := application.NewDeviceService(provider)
	defer service.Close()
	errors := make(chan error, 4)
	run := func(deviceID string, value bool) {
		go func() { _, _, err := service.ExecutePower(context.Background(), deviceID, value); errors <- err }()
	}

	run("switch-a", true)
	if entered := <-provider.entered; entered != "switch-a" {
		t.Fatalf("first entered = %q", entered)
	}
	run("switch-a", false)
	deadline := time.Now().Add(time.Second)
	for service.Metrics().CommandQueuePending != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if pending := service.Metrics().CommandQueuePending; pending != 1 {
		t.Fatalf("pending = %d", pending)
	}
	select {
	case entered := <-provider.entered:
		t.Fatalf("same device entered concurrently: %s", entered)
	case <-time.After(20 * time.Millisecond):
	}
	provider.release["switch-a"] <- struct{}{}
	if entered := <-provider.entered; entered != "switch-a" {
		t.Fatalf("second entered = %q", entered)
	}
	provider.release["switch-a"] <- struct{}{}
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
	if err := <-errors; err != nil {
		t.Fatal(err)
	}

	run("switch-a", true)
	run("switch-b", true)
	seen := map[string]bool{<-provider.entered: true, <-provider.entered: true}
	if !seen["switch-a"] || !seen["switch-b"] {
		t.Fatalf("cross-device entries = %#v", seen)
	}
	provider.release["switch-a"] <- struct{}{}
	provider.release["switch-b"] <- struct{}{}
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
	if service.Metrics().CommandQueueMaxPending < 1 {
		t.Fatalf("metrics = %#v", service.Metrics())
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
	if _, command, err := service.ExecutePower(ctx, "switch-a", false); !errors.Is(err, context.DeadlineExceeded) || command.ID != "" {
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
	_, command, err := service.ExecutePower(context.Background(), "virtual-switch-1", true)
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
		if len(states) == 1 && states[0].Quality == domainstate.QualityStale {
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
		if len(states) == 1 && states[0].Quality == domainstate.QualityReported && states[0].Version == staleVersion+1 {
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
	_, command, err := service.ExecutePower(context.Background(), "virtual-switch-1", true)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case state := <-events:
		if state.Quality != domainstate.QualityOptimistic || state.PendingCommandID != command.ID || state.Value.Bool == nil || !*state.Value.Bool {
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
