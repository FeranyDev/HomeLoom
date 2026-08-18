package state

import (
	"sort"
	"sync"
	"time"

	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
)

// Decision explains why Apply selected or rejected an incoming observation.
// The original Apply API remains available for callers that only need a
// changed flag; ApplyWithResult exposes this information to diagnostics and
// future conflict UI/API consumers.
type Decision string

const (
	DecisionAccepted                 Decision = "accepted"
	DecisionAcceptedProviderReport   Decision = "accepted-provider-report"
	DecisionRejectedProviderPriority Decision = "rejected-provider-priority"
	DecisionRejectedSequence         Decision = "rejected-sequence"
	DecisionRejectedObservedAt       Decision = "rejected-observed-at"
	DecisionRejectedQuality          Decision = "rejected-quality"
	DecisionRejectedReceivedAt       Decision = "rejected-received-at"
	DecisionRejectedEquivalent       Decision = "rejected-equivalent"
)

// ApplyResult contains the winner and the comparison that selected it.
// CurrentPriority and IncomingPriority are populated when the observation
// crossed Provider identities, making a priority decision explainable without
// exposing Store internals.
type ApplyResult struct {
	Applied          bool                   `json:"applied"`
	Decision         Decision               `json:"decision"`
	Current          domainstate.StateValue `json:"current"`
	Incoming         domainstate.StateValue `json:"incoming"`
	CurrentPriority  int                    `json:"currentPriority"`
	IncomingPriority int                    `json:"incomingPriority"`
}

// PriorityResolver supplies a deterministic preference for a Provider's
// value at a canonical state key. Larger values win. A resolver is only used
// when two different Providers report the same Key, so the existing sequence
// and timestamp semantics for one Provider remain unchanged.
type PriorityResolver interface {
	Priority(domainstate.Key, string) int
}

// StaticPriorityResolver is a convenient in-memory policy. Per-property
// entries override the default Provider priority and intentionally fall back
// when a Provider is absent from the property-specific map.
type StaticPriorityResolver struct {
	Providers  map[string]int
	Properties map[domainstate.Key]map[string]int
}

func (r StaticPriorityResolver) Priority(key domainstate.Key, providerID string) int {
	if propertyPriorities, exists := r.Properties[key]; exists {
		if priority, exists := propertyPriorities[providerID]; exists {
			return priority
		}
	}
	return r.Providers[providerID]
}

type Options struct {
	PriorityResolver PriorityResolver
}

// Checkpoint is a versioned in-memory representation suitable for durable
// storage. Restored values are always forced stale: a checkpoint restores
// identity and last-known value, never a claim that a device is currently
// reachable.
type Checkpoint struct {
	Version int                      `json:"version"`
	Values  []domainstate.StateValue `json:"values"`
}

const CheckpointVersion = 1

type Store struct {
	mu       sync.RWMutex
	values   map[domainstate.Key]domainstate.StateValue
	priority PriorityResolver
}

func NewStore(options ...Options) *Store {
	store := &Store{values: make(map[domainstate.Key]domainstate.StateValue)}
	if len(options) > 0 {
		store.priority = options[0].PriorityResolver
	}
	return store
}

// SetPriorityResolver updates the selection policy for subsequent reports.
// It does not rewrite already selected state values.
func (s *Store) SetPriorityResolver(resolver PriorityResolver) {
	s.mu.Lock()
	s.priority = resolver
	s.mu.Unlock()
}

func (s *Store) Apply(incoming domainstate.StateValue) (domainstate.StateValue, bool) {
	value, result := s.ApplyWithResult(incoming)
	return value, result.Applied
}

func (s *Store) ApplyWithResult(incoming domainstate.StateValue) (domainstate.StateValue, ApplyResult) {
	incoming = normalize(incoming)
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.values[incoming.Key]
	currentPriority, incomingPriority := 0, 0
	decision := DecisionAccepted
	if exists {
		decision, currentPriority, incomingPriority = s.compareLocked(current, incoming)
		if decision != DecisionAccepted && decision != DecisionAcceptedProviderReport {
			return current, ApplyResult{
				Decision: decision, Current: current, Incoming: incoming,
				CurrentPriority: currentPriority, IncomingPriority: incomingPriority,
			}
		}
	}
	incoming.Version = current.Version + 1
	s.values[incoming.Key] = incoming
	return incoming, ApplyResult{
		Applied: true, Decision: decision, Current: current, Incoming: incoming,
		CurrentPriority: currentPriority, IncomingPriority: incomingPriority,
	}
}

func (s *Store) compareLocked(current, incoming domainstate.StateValue) (Decision, int, int) {
	// A real device report confirms or replaces an optimistic command even if
	// its Provider has a lower configured priority. Otherwise an optimistic
	// command could survive until expiry merely because it was issued locally.
	if current.Quality == domainstate.QualityOptimistic &&
		(incoming.Quality == domainstate.QualityReported || incoming.Quality == domainstate.QualityConfirmed) {
		return DecisionAcceptedProviderReport, 0, 0
	}
	if current.ProviderID != "" && incoming.ProviderID != "" && current.ProviderID != incoming.ProviderID {
		currentPriority, incomingPriority := s.providerPriorityLocked(current.Key, current.ProviderID), s.providerPriorityLocked(incoming.Key, incoming.ProviderID)
		if currentPriority != incomingPriority {
			if incomingPriority > currentPriority {
				return DecisionAccepted, currentPriority, incomingPriority
			}
			return DecisionRejectedProviderPriority, currentPriority, incomingPriority
		}
	}
	if current.ProviderID == incoming.ProviderID && current.Sequence > 0 && incoming.Sequence > 0 {
		if current.Sequence != incoming.Sequence {
			if incoming.Sequence > current.Sequence {
				return DecisionAccepted, 0, 0
			}
			return DecisionRejectedSequence, 0, 0
		}
		return DecisionRejectedEquivalent, 0, 0
	}
	if !current.ObservedAt.Equal(incoming.ObservedAt) {
		if incoming.ObservedAt.After(current.ObservedAt) {
			return DecisionAccepted, 0, 0
		}
		return DecisionRejectedObservedAt, 0, 0
	}
	currentQuality, incomingQuality := qualityRank(current.Quality), qualityRank(incoming.Quality)
	if currentQuality != incomingQuality {
		if incomingQuality > currentQuality {
			return DecisionAccepted, 0, 0
		}
		return DecisionRejectedQuality, 0, 0
	}
	if !current.ReceivedAt.Equal(incoming.ReceivedAt) {
		if incoming.ReceivedAt.After(current.ReceivedAt) {
			return DecisionAccepted, 0, 0
		}
		return DecisionRejectedReceivedAt, 0, 0
	}
	return DecisionRejectedEquivalent, 0, 0
}

func (s *Store) providerPriorityLocked(key domainstate.Key, providerID string) int {
	if s.priority == nil {
		return 0
	}
	return s.priority.Priority(key, providerID)
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
	sortStateValues(result)
	return result
}

func (s *Store) Get(key domainstate.Key) (domainstate.StateValue, bool) {
	s.mu.RLock()
	value, ok := s.values[key]
	s.mu.RUnlock()
	return value, ok
}

// Checkpoint returns a deterministic snapshot of retained values. It does not
// modify the live store and callers may serialize the returned value outside
// its lock.
func (s *Store) Checkpoint() Checkpoint {
	s.mu.RLock()
	values := make([]domainstate.StateValue, 0, len(s.values))
	for _, value := range s.values {
		values = append(values, value)
	}
	s.mu.RUnlock()
	sortStateValues(values)
	return Checkpoint{Version: CheckpointVersion, Values: values}
}

// RestoreCheckpoint replaces the store with the checkpoint's retained state.
// Every restored value is made unavailable/stale and outstanding command
// ownership is discarded, preventing a restart from publishing cached data as
// a live report. It returns false for an unsupported checkpoint version.
func (s *Store) RestoreCheckpoint(checkpoint Checkpoint) bool {
	if checkpoint.Version != CheckpointVersion {
		return false
	}
	restored := make(map[domainstate.Key]domainstate.StateValue, len(checkpoint.Values))
	for _, value := range checkpoint.Values {
		if value.Key.DeviceID == "" || value.Key.EndpointID == "" || value.Key.CapabilityID == "" || value.Key.PropertyID == "" {
			continue
		}
		value.Quality = domainstate.QualityStale
		value.Source = domainstate.SourcePersistentCache
		value.Available = false
		value.UnavailableReason = domainstate.UnavailableStale
		value.PendingCommandID = ""
		value.ExpiresAt = time.Time{}
		value = normalize(value)
		if value.Version == 0 {
			value.Version = 1
		}
		restored[value.Key] = value
	}
	s.mu.Lock()
	s.values = restored
	s.mu.Unlock()
	return true
}

func sortStateValues(values []domainstate.StateValue) {
	sort.Slice(values, func(i, j int) bool {
		left, right := values[i].Key, values[j].Key
		if left.DeviceID != right.DeviceID {
			return left.DeviceID < right.DeviceID
		}
		if left.EndpointID != right.EndpointID {
			return left.EndpointID < right.EndpointID
		}
		if left.CapabilityID != right.CapabilityID {
			return left.CapabilityID < right.CapabilityID
		}
		return left.PropertyID < right.PropertyID
	})
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

func (s *Store) MarkCapabilityUnavailable(
	deviceID, endpointID, capabilityID string,
	reason domainstate.UnavailableReason,
	traceIDs ...string,
) []domainstate.StateValue {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := make([]domainstate.StateValue, 0)
	traceID := ""
	if len(traceIDs) > 0 {
		traceID = traceIDs[0]
	}
	for key, value := range s.values {
		if key.DeviceID != deviceID || key.EndpointID != endpointID || key.CapabilityID != capabilityID {
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
