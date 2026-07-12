package state

import (
	"sort"
	"sync"
	"time"

	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
)

type Store struct {
	mu     sync.RWMutex
	values map[domainstate.Key]domainstate.StateValue
}

func NewStore() *Store {
	return &Store{values: make(map[domainstate.Key]domainstate.StateValue)}
}

func (s *Store) Apply(incoming domainstate.StateValue) (domainstate.StateValue, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.values[incoming.Key]
	if exists && !preferIncoming(current, incoming) {
		return current, false
	}
	incoming.Version = current.Version + 1
	s.values[incoming.Key] = incoming
	return incoming, true
}

func (s *Store) Device(deviceID string) []domainstate.StateValue {
	s.mu.RLock()
	result := make([]domainstate.StateValue, 0)
	for key, value := range s.values {
		if key.DeviceID == deviceID {
			result = append(result, value)
		}
	}
	s.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].Key.PropertyID < result[j].Key.PropertyID })
	return result
}

func (s *Store) MarkStale(now time.Time) []domainstate.StateValue {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := make([]domainstate.StateValue, 0)
	for key, value := range s.values {
		if value.ExpiresAt.IsZero() || now.Before(value.ExpiresAt) || value.Quality == domainstate.QualityStale {
			continue
		}
		value.Quality = domainstate.QualityStale
		value.Version++
		s.values[key] = value
		changed = append(changed, value)
	}
	return changed
}

func preferIncoming(current, incoming domainstate.StateValue) bool {
	if current.ProviderID == incoming.ProviderID && current.Sequence > 0 && incoming.Sequence > 0 && current.Sequence != incoming.Sequence {
		return incoming.Sequence > current.Sequence
	}
	if !current.ObservedAt.Equal(incoming.ObservedAt) {
		return incoming.ObservedAt.After(current.ObservedAt)
	}
	currentQuality, incomingQuality := qualityRank(current.Quality), qualityRank(incoming.Quality)
	if currentQuality != incomingQuality {
		return incomingQuality > currentQuality
	}
	if !current.ReceivedAt.Equal(incoming.ReceivedAt) {
		return incoming.ReceivedAt.After(current.ReceivedAt)
	}
	return false
}

func qualityRank(quality domainstate.Quality) int {
	switch quality {
	case domainstate.QualityConfirmed:
		return 5
	case domainstate.QualityReported:
		return 4
	case domainstate.QualityPolled:
		return 3
	case domainstate.QualityOptimistic:
		return 2
	case domainstate.QualityStale:
		return 1
	default:
		return 0
	}
}
