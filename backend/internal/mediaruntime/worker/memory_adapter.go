package worker

import (
	"sort"
	"sync"

	"github.com/feranydev/homeloom/backend/internal/mediaruntime/contract"
)

// MemoryAdapter is a non-persistent test adapter. It must not be used to
// represent a connected camera or to hold credential material.
type MemoryAdapter struct {
	mu      sync.RWMutex
	streams map[string]contract.StreamSpec
}

func NewMemoryAdapter() *MemoryAdapter {
	return &MemoryAdapter{streams: make(map[string]contract.StreamSpec)}
}

func (a *MemoryAdapter) Replace(streams []contract.StreamSpec) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.streams = make(map[string]contract.StreamSpec, len(streams))
	for _, stream := range streams {
		a.streams[stream.ID] = cloneStream(stream)
	}
	return nil
}

func (a *MemoryAdapter) Upsert(stream contract.StreamSpec) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.streams[stream.ID] = cloneStream(stream)
	return nil
}

func (a *MemoryAdapter) Delete(streamID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.streams, streamID)
	return nil
}

func (a *MemoryAdapter) Snapshot() []contract.StreamSpec {
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := make([]contract.StreamSpec, 0, len(a.streams))
	for _, stream := range a.streams {
		result = append(result, cloneStream(stream))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
