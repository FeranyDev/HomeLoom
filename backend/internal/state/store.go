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
	incoming = normalize(incoming)
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

func (s *Store) ApplyOptimistic(incoming domainstate.StateValue) domainstate.StateValue {
	incoming = normalize(incoming)
	s.mu.Lock()
	current := s.values[incoming.Key]
	incoming.Version = current.Version + 1
	s.values[incoming.Key] = incoming
	s.mu.Unlock()
	return incoming
}

// EnsureUnknown creates a state identity only when no value has ever been
// observed. It never replaces a retained last-known value.
func (s *Store) EnsureUnknown(incoming domainstate.StateValue) (domainstate.StateValue, bool) {
	incoming.Quality = domainstate.QualityUnknown
	incoming = normalize(incoming)
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, exists := s.values[incoming.Key]; exists {
		return current, false
	}
	incoming.Version = 1
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

func (s *Store) Get(key domainstate.Key) (domainstate.StateValue, bool) {
	s.mu.RLock()
	value, ok := s.values[key]
	s.mu.RUnlock()
	return value, ok
}

// ResolveOptimistic rolls back only while the same command still owns the
// optimistic value. A provider report that arrived first is never overwritten.
func (s *Store) ResolveOptimistic(commandID string, fallback *domainstate.StateValue) (domainstate.StateValue, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, current := range s.values {
		if current.PendingCommandID != commandID {
			continue
		}
		if fallback != nil {
			restored := *fallback
			restored.Version = current.Version + 1
			restored.PendingCommandID = ""
			s.values[key] = restored
			return restored, true
		}
		current.Quality, current.PendingCommandID, current.Version = domainstate.QualityStale, "", current.Version+1
		current.Available, current.UnavailableReason = false, domainstate.UnavailableCommandUnconfirmed
		s.values[key] = current
		return current, true
	}
	return domainstate.StateValue{}, false
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
		value.Available = false
		value.UnavailableReason = domainstate.UnavailableExpired
		value.PendingCommandID = ""
		value.Version++
		s.values[key] = value
		changed = append(changed, value)
	}
	return changed
}

// MarkDeviceStale invalidates the last known values without deleting them.
// Repeated offline events are idempotent and do not inflate state versions.
func (s *Store) MarkDeviceStale(deviceID string) []domainstate.StateValue {
	return s.MarkDeviceUnavailable(deviceID, domainstate.UnavailableDeviceOffline)
}

func (s *Store) MarkDeviceUnavailable(deviceID string, reason domainstate.UnavailableReason, traceIDs ...string) []domainstate.StateValue {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := make([]domainstate.StateValue, 0)
	traceID := ""
	if len(traceIDs) > 0 {
		traceID = traceIDs[0]
	}
	for key, value := range s.values {
		if key.DeviceID != deviceID {
			continue
		}
		quality := domainstate.QualityStale
		if !value.Known {
			quality = domainstate.QualityUnknown
		}
		if !value.Available && value.Quality == quality && value.UnavailableReason == reason {
			continue
		}
		value.Quality = quality
		value.Available = false
		value.UnavailableReason = reason
		value.TraceID = traceID
		value.PendingCommandID = ""
		value.Version++
		s.values[key] = value
		changed = append(changed, value)
	}
	return changed
}

func normalize(value domainstate.StateValue) domainstate.StateValue {
	if value.Value.Kind != "" {
		value.Known = true
	}
	switch value.Quality {
	case domainstate.QualityConfirmed, domainstate.QualityReported, domainstate.QualityPolled, domainstate.QualityOptimistic:
		value.Available = value.Known
		if value.Available {
			value.UnavailableReason = ""
		} else if value.UnavailableReason == "" {
			value.UnavailableReason = domainstate.UnavailableNeverReported
		}
	case domainstate.QualityUnknown:
		value.Available = false
		if value.UnavailableReason == "" {
			value.UnavailableReason = domainstate.UnavailableNeverReported
		}
	case domainstate.QualityStale:
		value.Available = false
		if value.UnavailableReason == "" {
			value.UnavailableReason = domainstate.UnavailableStale
		}
	}
	return value
}

func preferIncoming(current, incoming domainstate.StateValue) bool {
	if current.Quality == domainstate.QualityOptimistic && (incoming.Quality == domainstate.QualityReported || incoming.Quality == domainstate.QualityConfirmed) {
		return true
	}
	if current.ProviderID == incoming.ProviderID && current.Sequence > 0 && incoming.Sequence > 0 {
		if current.Sequence != incoming.Sequence {
			return incoming.Sequence > current.Sequence
		}
		return false
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
