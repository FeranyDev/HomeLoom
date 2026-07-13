package application

import (
	"context"
	"sync"
	"sync/atomic"
)

type commandGate struct {
	token chan struct{}
	refs  int
}

// commandCoordinator serializes provider mutations per device while allowing
// unrelated devices to execute concurrently. Waiting honors the caller context.
type commandCoordinator struct {
	mu         sync.Mutex
	gates      map[string]*commandGate
	pending    atomic.Int64
	maxPending atomic.Int64
}

func newCommandCoordinator() *commandCoordinator {
	return &commandCoordinator{gates: make(map[string]*commandGate)}
}

func (c *commandCoordinator) acquire(ctx context.Context, deviceID string) (func(), error) {
	c.mu.Lock()
	gate := c.gates[deviceID]
	if gate == nil {
		gate = &commandGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		c.gates[deviceID] = gate
	}
	gate.refs++
	c.mu.Unlock()

	select {
	case <-gate.token:
		return c.release(deviceID, gate), nil
	default:
	}

	pending := c.pending.Add(1)
	for {
		maximum := c.maxPending.Load()
		if pending <= maximum || c.maxPending.CompareAndSwap(maximum, pending) {
			break
		}
	}
	select {
	case <-ctx.Done():
		c.pending.Add(-1)
		c.releaseReference(deviceID, gate)
		return nil, ctx.Err()
	case <-gate.token:
		c.pending.Add(-1)
		return c.release(deviceID, gate), nil
	}
}

func (c *commandCoordinator) release(deviceID string, gate *commandGate) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			gate.token <- struct{}{}
			c.releaseReference(deviceID, gate)
		})
	}
}

func (c *commandCoordinator) releaseReference(deviceID string, gate *commandGate) {
	c.mu.Lock()
	gate.refs--
	if gate.refs == 0 && c.gates[deviceID] == gate {
		delete(c.gates, deviceID)
	}
	c.mu.Unlock()
}

func (c *commandCoordinator) stats() (pending, maximum int64) {
	return c.pending.Load(), c.maxPending.Load()
}
