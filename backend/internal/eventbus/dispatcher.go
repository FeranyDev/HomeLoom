package eventbus

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrQueueFull        = errors.New("event queue is full")
	ErrDispatcherClosed = errors.New("dispatcher is closed")
)

type Event struct {
	DeviceID   string
	TraceID    string
	Payload    any
	enqueuedAt time.Time
}

var traceSequence atomic.Uint64

type Stats struct {
	Handled        uint64
	AverageLatency time.Duration
	MaxLatency     time.Duration
	SlowHandlers   uint64
}

type Handler func(Event)

type Dispatcher struct {
	mu                sync.RWMutex
	shards            []chan Event
	handle            Handler
	closed            bool
	wg                sync.WaitGroup
	handled           atomic.Uint64
	totalLatencyNanos atomic.Uint64
	maxLatencyNanos   atomic.Uint64
	slowHandlers      atomic.Uint64
}

func NewDispatcher(shardCount, queueSize int, handler Handler) *Dispatcher {
	if shardCount < 1 {
		shardCount = 1
	}
	if queueSize < 1 {
		queueSize = 1
	}
	dispatcher := &Dispatcher{shards: make([]chan Event, shardCount), handle: handler}
	for index := range dispatcher.shards {
		dispatcher.shards[index] = make(chan Event, queueSize)
		dispatcher.wg.Add(1)
		go dispatcher.run(dispatcher.shards[index])
	}
	return dispatcher
}

func (d *Dispatcher) Publish(event Event) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return ErrDispatcherClosed
	}
	if event.TraceID == "" {
		event.TraceID = fmt.Sprintf("event-%x-%x", time.Now().UnixNano(), traceSequence.Add(1))
	}
	queue := d.shards[shard(event.DeviceID, len(d.shards))]
	event.enqueuedAt = time.Now()
	select {
	case queue <- event:
		return nil
	default:
		return ErrQueueFull
	}
}

func (d *Dispatcher) Stats() Stats {
	handled := d.handled.Load()
	average := time.Duration(0)
	if handled > 0 {
		average = time.Duration(d.totalLatencyNanos.Load() / handled)
	}
	return Stats{Handled: handled, AverageLatency: average, MaxLatency: time.Duration(d.maxLatencyNanos.Load()), SlowHandlers: d.slowHandlers.Load()}
}

func (d *Dispatcher) Pending() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	total := 0
	for _, queue := range d.shards {
		total += len(queue)
	}
	return total
}

func (d *Dispatcher) Capacity() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	total := 0
	for _, queue := range d.shards {
		total += cap(queue)
	}
	return total
}

func (d *Dispatcher) Close(ctx context.Context) error {
	d.mu.Lock()
	if !d.closed {
		d.closed = true
		for _, queue := range d.shards {
			close(queue)
		}
	}
	d.mu.Unlock()
	done := make(chan struct{})
	go func() { d.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Dispatcher) run(queue <-chan Event) {
	defer d.wg.Done()
	for event := range queue {
		started := time.Now()
		d.handle(event)
		finished := time.Now()
		latency := finished.Sub(event.enqueuedAt)
		handlerDuration := finished.Sub(started)
		d.handled.Add(1)
		d.totalLatencyNanos.Add(uint64(latency))
		for {
			current := d.maxLatencyNanos.Load()
			if uint64(latency) <= current || d.maxLatencyNanos.CompareAndSwap(current, uint64(latency)) {
				break
			}
		}
		if handlerDuration >= 100*time.Millisecond {
			d.slowHandlers.Add(1)
		}
	}
}

func shard(id string, count int) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(id))
	return int(hash.Sum32() % uint32(count))
}
