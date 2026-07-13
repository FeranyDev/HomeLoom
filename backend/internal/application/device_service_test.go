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
