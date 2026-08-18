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
	ErrQueueFull            = errors.New("event queue is full")
	ErrDispatcherClosed     = errors.New("dispatcher is closed")
	ErrLowPriorityDuplicate = errors.New("low-priority duplicate event dropped")
)

// Priority controls how a queued event competes with other events in the
// same shard. The zero value deliberately remains Normal for compatibility
// with callers that construct Event literals.
type Priority string

const (
	PriorityLow      Priority = "low"
	PriorityNormal   Priority = "normal"
	PriorityHigh     Priority = "high"
	PriorityCritical Priority = "critical"
)

// Config configures a Dispatcher. QueueSize is the capacity of each shard,
// not the aggregate capacity. Zero and invalid values are normalized to the
// long-standing defaults so a partial configuration remains safe.
type Config struct {
	ShardCount int
	QueueSize  int
}

const (
	DefaultShardCount = 8
	DefaultQueueSize  = 128
)

func (c Config) normalized() Config {
	if c.ShardCount < 1 {
		c.ShardCount = DefaultShardCount
	}
	if c.QueueSize < 1 {
		c.QueueSize = DefaultQueueSize
	}
	return c
}

type Event struct {
	DeviceID string
	TraceID  string
	Payload  any
	Priority Priority

	// CoalesceKey identifies a state/property update where only the newest
	// queued value matters. Matching keys are merged last-write-wins before a
	// handler begins processing. A useful key is a canonical property path
	// such as "device\x00endpoint\x00capability\x00property".
	CoalesceKey string
	// DeduplicationKey is only applied to low-priority events. If an identical
	// low-priority event is already waiting in the shard, the new event is
	// rejected with ErrLowPriorityDuplicate instead of consuming capacity.
	DeduplicationKey string

	enqueuedAt time.Time
}

var traceSequence atomic.Uint64

type Stats struct {
	Handled                      uint64
	AverageLatency               time.Duration
	MaxLatency                   time.Duration
	SlowHandlers                 uint64
	Merged                       uint64
	LowPriorityDuplicatesDropped uint64
}

type Handler func(Event)

type queuedEvent struct {
	event Event
	order uint64
}

// dispatcherShard holds a bounded, priority-aware queue. A custom queue is
// used instead of channels so a later update can replace a queued property
// event without ever blocking a producer.
type dispatcherShard struct {
	mu        sync.Mutex
	notEmpty  *sync.Cond
	closed    bool
	capacity  int
	pending   int
	queues    map[Priority][]*queuedEvent
	coalesce  map[string]*queuedEvent
	dedupe    map[string]struct{}
	nextOrder uint64
}

func newDispatcherShard(capacity int) *dispatcherShard {
	queue := &dispatcherShard{
		capacity: capacity,
		queues: map[Priority][]*queuedEvent{
			PriorityCritical: nil,
			PriorityHigh:     nil,
			PriorityNormal:   nil,
			PriorityLow:      nil,
		},
		coalesce: make(map[string]*queuedEvent),
		dedupe:   make(map[string]struct{}),
	}
	queue.notEmpty = sync.NewCond(&queue.mu)
	return queue
}

func normalizePriority(priority Priority) Priority {
	switch priority {
	case PriorityLow, PriorityHigh, PriorityCritical:
		return priority
	case PriorityNormal, "":
		return PriorityNormal
	default:
		return PriorityNormal
	}
}

func priorityGreater(left, right Priority) bool {
	rank := func(value Priority) int {
		switch value {
		case PriorityCritical:
			return 4
		case PriorityHigh:
			return 3
		case PriorityNormal:
			return 2
		case PriorityLow:
			return 1
		default:
			return 2
		}
	}
	return rank(left) > rank(right)
}

// enqueue returns whether the event was accepted, whether it merged an
// existing queued event, and an error for a deliberately dropped duplicate or
// full queue.
func (q *dispatcherShard) enqueue(event Event) (bool, bool, error) {
	event.Priority = normalizePriority(event.Priority)
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false, false, ErrDispatcherClosed
	}
	if event.Priority == PriorityLow && event.DeduplicationKey != "" {
		if _, exists := q.dedupe[event.DeduplicationKey]; exists {
			return false, false, ErrLowPriorityDuplicate
		}
	}
	if event.CoalesceKey != "" {
		if current := q.coalesce[event.CoalesceKey]; current != nil {
			previousPriority := current.event.Priority
			previousDedupeKey := current.event.DeduplicationKey
			if priorityGreater(event.Priority, previousPriority) {
				q.removeLocked(current)
				current.event = event
				q.queues[event.Priority] = append(q.queues[event.Priority], current)
			} else {
				current.event = event
				current.event.Priority = previousPriority
			}
			if previousPriority == PriorityLow && previousDedupeKey != "" {
				delete(q.dedupe, previousDedupeKey)
			}
			if current.event.Priority == PriorityLow && current.event.DeduplicationKey != "" {
				q.dedupe[current.event.DeduplicationKey] = struct{}{}
			}
			return true, true, nil
		}
	}
	if q.pending >= q.capacity {
		return false, false, ErrQueueFull
	}
	q.nextOrder++
	queued := &queuedEvent{event: event, order: q.nextOrder}
	q.queues[event.Priority] = append(q.queues[event.Priority], queued)
	q.pending++
	if event.CoalesceKey != "" {
		q.coalesce[event.CoalesceKey] = queued
	}
	if event.Priority == PriorityLow && event.DeduplicationKey != "" {
		q.dedupe[event.DeduplicationKey] = struct{}{}
	}
	q.notEmpty.Signal()
	return true, false, nil
}

func (q *dispatcherShard) removeLocked(target *queuedEvent) {
	items := q.queues[target.event.Priority]
	for index, item := range items {
		if item != target {
			continue
		}
		copy(items[index:], items[index+1:])
		items[len(items)-1] = nil
		q.queues[target.event.Priority] = items[:len(items)-1]
		return
	}
}

func (q *dispatcherShard) next() (Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.pending == 0 && !q.closed {
		q.notEmpty.Wait()
	}
	if q.pending == 0 {
		return Event{}, false
	}
	for _, priority := range []Priority{PriorityCritical, PriorityHigh, PriorityNormal, PriorityLow} {
		items := q.queues[priority]
		for index, queued := range items {
			if q.hasEarlierForDeviceLocked(queued) {
				continue
			}
			q.removeAtLocked(priority, index)
			q.pending--
			if queued.event.CoalesceKey != "" && q.coalesce[queued.event.CoalesceKey] == queued {
				delete(q.coalesce, queued.event.CoalesceKey)
			}
			if queued.event.Priority == PriorityLow && queued.event.DeduplicationKey != "" {
				delete(q.dedupe, queued.event.DeduplicationKey)
			}
			return queued.event, true
		}
	}
	panic("eventbus: pending queue has no event")
}

// hasEarlierForDeviceLocked keeps the original per-device ordering contract
// even when a later event carries a higher priority. Priority only chooses
// between devices whose own earlier events have already drained.
func (q *dispatcherShard) hasEarlierForDeviceLocked(candidate *queuedEvent) bool {
	for _, priority := range []Priority{PriorityCritical, PriorityHigh, PriorityNormal, PriorityLow} {
		for _, other := range q.queues[priority] {
			if other != candidate && other.event.DeviceID == candidate.event.DeviceID && other.order < candidate.order {
				return true
			}
		}
	}
	return false
}

func (q *dispatcherShard) removeAtLocked(priority Priority, index int) {
	items := q.queues[priority]
	copy(items[index:], items[index+1:])
	items[len(items)-1] = nil
	q.queues[priority] = items[:len(items)-1]
}

func (q *dispatcherShard) close() {
	q.mu.Lock()
	q.closed = true
	q.notEmpty.Broadcast()
	q.mu.Unlock()
}

func (q *dispatcherShard) pendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pending
}

type Dispatcher struct {
	mu                sync.RWMutex
	shards            []*dispatcherShard
	handle            Handler
	closed            bool
	wg                sync.WaitGroup
	handled           atomic.Uint64
	totalLatencyNanos atomic.Uint64
	maxLatencyNanos   atomic.Uint64
	slowHandlers      atomic.Uint64
	merged            atomic.Uint64
	duplicatesDropped atomic.Uint64
}

// NewDispatcher preserves the original public constructor. New code that
// receives application configuration should use NewDispatcherWithConfig.
func NewDispatcher(shardCount, queueSize int, handler Handler) *Dispatcher {
	// Keep the legacy constructor's invalid-input behavior: it was documented
	// by implementation and used as a small, one-worker fallback. Config-based
	// construction uses the production defaults instead.
	if shardCount < 1 {
		shardCount = 1
	}
	if queueSize < 1 {
		queueSize = 1
	}
	return NewDispatcherWithConfig(Config{ShardCount: shardCount, QueueSize: queueSize}, handler)
}

func NewDispatcherWithConfig(config Config, handler Handler) *Dispatcher {
	config = config.normalized()
	dispatcher := &Dispatcher{shards: make([]*dispatcherShard, config.ShardCount), handle: handler}
	for index := range dispatcher.shards {
		dispatcher.shards[index] = newDispatcherShard(config.QueueSize)
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
	event.enqueuedAt = time.Now()
	_, merged, err := d.shards[shard(event.DeviceID, len(d.shards))].enqueue(event)
	if merged {
		d.merged.Add(1)
	}
	if errors.Is(err, ErrLowPriorityDuplicate) {
		d.duplicatesDropped.Add(1)
	}
	return err
}

func (d *Dispatcher) Stats() Stats {
	handled := d.handled.Load()
	average := time.Duration(0)
	if handled > 0 {
		average = time.Duration(d.totalLatencyNanos.Load() / handled)
	}
	return Stats{
		Handled: handled, AverageLatency: average, MaxLatency: time.Duration(d.maxLatencyNanos.Load()), SlowHandlers: d.slowHandlers.Load(),
		Merged: d.merged.Load(), LowPriorityDuplicatesDropped: d.duplicatesDropped.Load(),
	}
}

func (d *Dispatcher) Pending() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	total := 0
	for _, queue := range d.shards {
		total += queue.pendingCount()
	}
	return total
}

func (d *Dispatcher) Capacity() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	total := 0
	for _, queue := range d.shards {
		total += queue.capacity
	}
	return total
}

func (d *Dispatcher) Close(ctx context.Context) error {
	d.mu.Lock()
	if !d.closed {
		d.closed = true
		for _, queue := range d.shards {
			queue.close()
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

func (d *Dispatcher) run(queue *dispatcherShard) {
	defer d.wg.Done()
	for {
		event, ok := queue.next()
		if !ok {
			return
		}
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
