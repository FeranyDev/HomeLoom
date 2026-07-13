package application_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	domaincommand "github.com/feranydev/homeloom/backend/internal/domain/command"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

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
		if item.State.Power == nil || !*item.State.Power {
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
