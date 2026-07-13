package eventbus

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatcherPreservesPerDeviceOrder(t *testing.T) {
	var mu sync.Mutex
	values := make(map[string][]int)
	dispatcher := NewDispatcher(4, 32, func(event Event) {
		mu.Lock()
		values[event.DeviceID] = append(values[event.DeviceID], event.Payload.(int))
		mu.Unlock()
	})
	for index := 0; index < 20; index++ {
		if err := dispatcher.Publish(Event{DeviceID: "switch-1", Payload: index}); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := dispatcher.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	want := make([]int, 20)
	for index := range want {
		want[index] = index
	}
	if !reflect.DeepEqual(values["switch-1"], want) {
		t.Fatalf("values = %v, want %v", values["switch-1"], want)
	}
}

func TestDispatcherReportsFullAndClosed(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	dispatcher := NewDispatcher(1, 1, func(Event) { once.Do(func() { close(started) }); <-release })
	if err := dispatcher.Publish(Event{DeviceID: "a"}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := dispatcher.Publish(Event{DeviceID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Publish(Event{DeviceID: "a"}); err != ErrQueueFull {
		t.Fatalf("error = %v", err)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := dispatcher.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Publish(Event{DeviceID: "a"}); err != ErrDispatcherClosed {
		t.Fatalf("error = %v", err)
	}
}

func TestDispatcherReportsLatencyAndSlowHandlers(t *testing.T) {
	dispatcher := NewDispatcher(1, 2, func(Event) { time.Sleep(110 * time.Millisecond) })
	if err := dispatcher.Publish(Event{DeviceID: "slow"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := dispatcher.Close(ctx); err != nil {
		t.Fatal(err)
	}
	stats := dispatcher.Stats()
	if stats.Handled != 1 || stats.AverageLatency < 100*time.Millisecond || stats.MaxLatency < stats.AverageLatency || stats.SlowHandlers != 1 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestDispatcherCloseHonorsTimeout(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	dispatcher := NewDispatcher(1, 1, func(Event) {
		close(started)
		<-release
	})
	if err := dispatcher.Publish(Event{DeviceID: "blocked"}); err != nil {
		t.Fatal(err)
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := dispatcher.Close(ctx); err != context.DeadlineExceeded {
		t.Fatalf("Close() error = %v, want deadline exceeded", err)
	}
	close(release)
	drainCtx, drainCancel := context.WithTimeout(context.Background(), time.Second)
	defer drainCancel()
	if err := dispatcher.Close(drainCtx); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestDispatcherProcessesOneHundredEventsPerSecondBaseline(t *testing.T) {
	var handled atomic.Uint64
	dispatcher := NewDispatcher(8, 128, func(Event) { handled.Add(1) })
	started := time.Now()
	for index := 0; index < 100; index++ {
		if err := dispatcher.Publish(Event{DeviceID: fmt.Sprintf("device-%d", index%20), Payload: index}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := dispatcher.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if handled.Load() != 100 || time.Since(started) >= time.Second {
		t.Fatalf("handled %d events in %s", handled.Load(), time.Since(started))
	}
}

func BenchmarkDispatcherOneHundredEvents(b *testing.B) {
	for iteration := 0; iteration < b.N; iteration++ {
		dispatcher := NewDispatcher(8, 128, func(Event) {})
		for index := 0; index < 100; index++ {
			if err := dispatcher.Publish(Event{DeviceID: fmt.Sprintf("device-%d", index%20)}); err != nil {
				b.Fatal(err)
			}
		}
		if err := dispatcher.Close(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
