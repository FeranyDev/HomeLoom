package eventbus

import (
	"context"
	"errors"
	"hash/fnv"
	"sync"
)

var (
	ErrQueueFull        = errors.New("event queue is full")
	ErrDispatcherClosed = errors.New("dispatcher is closed")
)

type Event struct {
	DeviceID string
	Payload  any
}

type Handler func(Event)

type Dispatcher struct {
	mu     sync.RWMutex
	shards []chan Event
	handle Handler
	closed bool
	wg     sync.WaitGroup
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
	queue := d.shards[shard(event.DeviceID, len(d.shards))]
	select {
	case queue <- event:
		return nil
	default:
		return ErrQueueFull
	}
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
		d.handle(event)
	}
}

func shard(id string, count int) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(id))
	return int(hash.Sum32() % uint32(count))
}
