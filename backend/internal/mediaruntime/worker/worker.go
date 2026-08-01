// Package worker owns Core's in-memory desired media stream state.
package worker

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/feranydev/homeloom/backend/internal/mediaruntime/contract"
)

var (
	ErrStaleGeneration = errors.New("stale stream generation")
	ErrReplayRequired  = errors.New("stream replay required for new generation")
	ErrStaleRevision   = errors.New("stale stream revision")
	ErrRevisionGap     = errors.New("stream revision gap")
	ErrInvalidStream   = errors.New("invalid stream specification")
)

// Adapter is intentionally small so stage 0 can exercise lifecycle semantics
// without loading a media engine.
type Adapter interface {
	Replace([]contract.StreamSpec) error
	Upsert(contract.StreamSpec) error
	Delete(streamID string) error
}

type Manager struct {
	mu         sync.RWMutex
	adapter    Adapter
	generation uint64
	revision   uint64
	streams    map[string]contract.StreamSpec
}

func NewManager(adapter Adapter) *Manager {
	return &Manager{adapter: adapter, streams: make(map[string]contract.StreamSpec)}
}

func (m *Manager) Replay(params contract.ReplayParams) (contract.ApplyResult, error) {
	if params.SchemaVersion != 1 || params.Generation == 0 || params.Revision == 0 {
		return m.result(false), fmt.Errorf("%w: invalid replay envelope", ErrInvalidStream)
	}
	streams, err := indexStreams(params.Streams)
	if err != nil {
		return m.result(false), err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if params.Generation < m.generation {
		return m.resultLocked(false), ErrStaleGeneration
	}
	if params.Generation == m.generation && params.Revision < m.revision {
		return m.resultLocked(false), ErrStaleRevision
	}
	if err := m.adapter.Replace(cloneSlice(params.Streams)); err != nil {
		return m.resultLocked(false), err
	}
	m.streams = streams
	m.generation = params.Generation
	m.revision = params.Revision
	return m.resultLocked(true), nil
}

func (m *Manager) Upsert(params contract.UpsertParams) (contract.ApplyResult, error) {
	if params.SchemaVersion != 1 || params.Generation == 0 || params.Revision == 0 {
		return m.result(false), fmt.Errorf("%w: invalid upsert envelope", ErrInvalidStream)
	}
	if err := validateStream(params.Stream); err != nil {
		return m.result(false), err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if result, done, err := m.validateMutationLocked(params.Generation, params.Revision); done {
		return result, err
	}
	if err := m.adapter.Upsert(cloneStream(params.Stream)); err != nil {
		return m.resultLocked(false), err
	}
	m.streams[params.Stream.ID] = cloneStream(params.Stream)
	m.revision = params.Revision
	return m.resultLocked(true), nil
}

func (m *Manager) Delete(params contract.DeleteParams) (contract.ApplyResult, error) {
	if params.SchemaVersion != 1 || params.Generation == 0 || params.Revision == 0 || params.StreamID == "" {
		return m.result(false), fmt.Errorf("%w: invalid delete envelope", ErrInvalidStream)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if result, done, err := m.validateMutationLocked(params.Generation, params.Revision); done {
		return result, err
	}
	if err := m.adapter.Delete(params.StreamID); err != nil {
		return m.resultLocked(false), err
	}
	delete(m.streams, params.StreamID)
	m.revision = params.Revision
	return m.resultLocked(true), nil
}

func (m *Manager) validateMutationLocked(generation, revision uint64) (contract.ApplyResult, bool, error) {
	if generation < m.generation {
		return m.resultLocked(false), true, ErrStaleGeneration
	}
	if generation > m.generation {
		return m.resultLocked(false), true, ErrReplayRequired
	}
	if revision < m.revision {
		return m.resultLocked(false), true, ErrStaleRevision
	}
	if revision == m.revision {
		return m.resultLocked(false), true, nil // idempotent duplicate
	}
	if revision != m.revision+1 {
		return m.resultLocked(false), true, ErrRevisionGap
	}
	return contract.ApplyResult{}, false, nil
}

func (m *Manager) Snapshot() (generation, revision uint64, streams []contract.StreamSpec) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	streams = make([]contract.StreamSpec, 0, len(m.streams))
	for _, stream := range m.streams {
		streams = append(streams, cloneStream(stream))
	}
	sort.Slice(streams, func(i, j int) bool { return streams[i].ID < streams[j].ID })
	return m.generation, m.revision, streams
}

func (m *Manager) result(applied bool) contract.ApplyResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.resultLocked(applied)
}

func (m *Manager) resultLocked(applied bool) contract.ApplyResult {
	return contract.ApplyResult{Applied: applied, Generation: m.generation, Revision: m.revision}
}

func indexStreams(streams []contract.StreamSpec) (map[string]contract.StreamSpec, error) {
	result := make(map[string]contract.StreamSpec, len(streams))
	for _, stream := range streams {
		if err := validateStream(stream); err != nil {
			return nil, err
		}
		if _, exists := result[stream.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate stream %q", ErrInvalidStream, stream.ID)
		}
		result[stream.ID] = cloneStream(stream)
	}
	return result, nil
}

func validateStream(stream contract.StreamSpec) error {
	if stream.SchemaVersion != 1 || stream.ID == "" || stream.DeviceID == "" || stream.Protocol == "" || stream.Profile == "" || stream.Mode == "" {
		return fmt.Errorf("%w: schemaVersion, id, deviceId, protocol, profile, and mode are required", ErrInvalidStream)
	}
	if stream.Talkback && !stream.Audio {
		return fmt.Errorf("%w: talkback requires audio", ErrInvalidStream)
	}
	return nil
}

func cloneStream(stream contract.StreamSpec) contract.StreamSpec {
	stream.Options = append([]byte(nil), stream.Options...)
	return stream
}

func cloneSlice(streams []contract.StreamSpec) []contract.StreamSpec {
	result := make([]contract.StreamSpec, len(streams))
	for i, stream := range streams {
		result[i] = cloneStream(stream)
	}
	return result
}
