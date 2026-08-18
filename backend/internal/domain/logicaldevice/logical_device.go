// Package logicaldevice defines durable, protocol-neutral device aggregation
// configuration. A logical device never imports credentials or native IDs into
// its own identity; it only keeps references to concrete Provider devices.
package logicaldevice

import (
	"fmt"
	"slices"
	"strings"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

// ProviderID is the synthetic owner exposed by the Provider Manager. It is a
// stable public contract rather than a real, configurable Provider instance.
const ProviderID = "logical"

type SourceRef struct {
	ProviderID string `json:"providerId"`
	DeviceID   string `json:"deviceId"`
}

func (r SourceRef) Key() string { return r.ProviderID + "\x00" + r.DeviceID }

type Binding struct {
	SourceRef
	// Priority is ascending: 0 is preferred. It is used for implicit identity
	// routes and as the tie-breaker for explicit routes.
	Priority int `json:"priority"`
}

type PropertyPath struct {
	EndpointID   string `json:"endpointId"`
	CapabilityID string `json:"capabilityId"`
	PropertyID   string `json:"propertyId"`
}

func (p PropertyPath) Key() string {
	return p.EndpointID + "\x00" + p.CapabilityID + "\x00" + p.PropertyID
}

type CommandPath struct {
	EndpointID   string `json:"endpointId"`
	CapabilityID string `json:"capabilityId"`
	CommandID    string `json:"commandId"`
}

func (p CommandPath) Key() string {
	return p.EndpointID + "\x00" + p.CapabilityID + "\x00" + p.CommandID
}

type PropertyCandidate struct {
	SourceRef
	Path          PropertyPath `json:"path"`
	Priority      int          `json:"priority"`
	AllowFallback bool         `json:"allowFallback,omitempty"`
}

type CommandCandidate struct {
	SourceRef
	Path     CommandPath `json:"path"`
	Priority int         `json:"priority"`
	// AllowFallback only permits retrying the next candidate when the selected
	// concrete command is idempotent and the previous Provider was unavailable.
	// It never permits retry after a timeout, rejection, or an arbitrary error.
	AllowFallback bool `json:"allowFallback,omitempty"`
}

type PropertyRoute struct {
	Path       PropertyPath        `json:"path"`
	Candidates []PropertyCandidate `json:"candidates"`
}

type CommandRoute struct {
	Path       CommandPath        `json:"path"`
	Candidates []CommandCandidate `json:"candidates"`
}

// CandidateStatus is returned for review in the linking UI. Candidates are
// suggestions only; no endpoint in this package can turn one into a binding.
type CandidateStatus struct {
	SourceRef
	Name   string      `json:"name"`
	Type   device.Type `json:"type"`
	HomeID string      `json:"homeId,omitempty"`
	RoomID string      `json:"roomId,omitempty"`
}

type MatchCandidate struct {
	Left    CandidateStatus `json:"left"`
	Right   CandidateStatus `json:"right"`
	Reasons []string        `json:"reasons"`
}

// RouteExplanation makes a selected logical value or control route auditable.
// Values are intentionally summarized as availability and selection metadata;
// the State Store remains the authoritative value history.
type RouteExplanation struct {
	LogicalDeviceID string               `json:"logicalDeviceId"`
	Kind            string               `json:"kind"`
	Path            string               `json:"path"`
	Reason          string               `json:"reason"`
	Selected        CandidateSelection   `json:"selected"`
	Candidates      []CandidateSelection `json:"candidates"`
}

type CandidateSelection struct {
	SourceRef
	Path      string `json:"path"`
	Priority  int    `json:"priority"`
	Available bool   `json:"available"`
	Selected  bool   `json:"selected"`
}

// Config is a manually created aggregate. A runtime never creates one from a
// candidate automatically, even when names happen to match.
type Config struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Type           device.Type     `json:"type"`
	Bindings       []Binding       `json:"bindings"`
	PropertyRoutes []PropertyRoute `json:"propertyRoutes,omitempty"`
	CommandRoutes  []CommandRoute  `json:"commandRoutes,omitempty"`
}

func (c Config) Clone() Config {
	copy := c
	copy.Bindings = append([]Binding(nil), c.Bindings...)
	copy.PropertyRoutes = make([]PropertyRoute, len(c.PropertyRoutes))
	for index, route := range c.PropertyRoutes {
		copy.PropertyRoutes[index] = route
		copy.PropertyRoutes[index].Candidates = append([]PropertyCandidate(nil), route.Candidates...)
	}
	copy.CommandRoutes = make([]CommandRoute, len(c.CommandRoutes))
	for index, route := range c.CommandRoutes {
		copy.CommandRoutes[index] = route
		copy.CommandRoutes[index].Candidates = append([]CommandCandidate(nil), route.Candidates...)
	}
	return copy
}

func (c Config) Validate() error {
	if !device.ValidStableID(c.ID) || c.ID == ProviderID {
		return fmt.Errorf("invalid logical device id")
	}
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 160 {
		return fmt.Errorf("logical device name is required and must be at most 160 characters")
	}
	if c.Type == "" {
		return fmt.Errorf("logical device type is required")
	}
	if len(c.Bindings) < 2 {
		return fmt.Errorf("at least two provider bindings are required")
	}
	bindings := make(map[string]Binding, len(c.Bindings))
	for _, binding := range c.Bindings {
		if err := validateSource(binding.SourceRef); err != nil {
			return err
		}
		if binding.ProviderID == ProviderID {
			return fmt.Errorf("a logical device cannot bind another logical device")
		}
		if binding.Priority < 0 || binding.Priority > 1000 {
			return fmt.Errorf("binding priority must be between 0 and 1000")
		}
		if _, exists := bindings[binding.Key()]; exists {
			return fmt.Errorf("duplicate provider binding %q/%q", binding.ProviderID, binding.DeviceID)
		}
		bindings[binding.Key()] = binding
	}
	properties := make(map[string]struct{}, len(c.PropertyRoutes))
	for _, route := range c.PropertyRoutes {
		if err := validatePropertyPath(route.Path); err != nil {
			return err
		}
		if _, exists := properties[route.Path.Key()]; exists {
			return fmt.Errorf("duplicate property route %q", route.Path.Key())
		}
		properties[route.Path.Key()] = struct{}{}
		if len(route.Candidates) == 0 {
			return fmt.Errorf("property route %q has no candidates", route.Path.Key())
		}
		candidateKeys := make(map[string]struct{}, len(route.Candidates))
		for _, candidate := range route.Candidates {
			if _, exists := bindings[candidate.SourceRef.Key()]; !exists {
				return fmt.Errorf("property route %q references an unbound source", route.Path.Key())
			}
			if err := validatePropertyPath(candidate.Path); err != nil {
				return err
			}
			if candidate.Priority < 0 || candidate.Priority > 1000 {
				return fmt.Errorf("property route priority must be between 0 and 1000")
			}
			key := candidate.SourceRef.Key() + "\x00" + candidate.Path.Key()
			if _, exists := candidateKeys[key]; exists {
				return fmt.Errorf("duplicate property route candidate")
			}
			candidateKeys[key] = struct{}{}
		}
	}
	commands := make(map[string]struct{}, len(c.CommandRoutes))
	for _, route := range c.CommandRoutes {
		if err := validateCommandPath(route.Path); err != nil {
			return err
		}
		if _, exists := commands[route.Path.Key()]; exists {
			return fmt.Errorf("duplicate command route %q", route.Path.Key())
		}
		commands[route.Path.Key()] = struct{}{}
		if len(route.Candidates) == 0 {
			return fmt.Errorf("command route %q has no candidates", route.Path.Key())
		}
		candidateKeys := make(map[string]struct{}, len(route.Candidates))
		for _, candidate := range route.Candidates {
			if _, exists := bindings[candidate.SourceRef.Key()]; !exists {
				return fmt.Errorf("command route %q references an unbound source", route.Path.Key())
			}
			if err := validateCommandPath(candidate.Path); err != nil {
				return err
			}
			if candidate.Priority < 0 || candidate.Priority > 1000 {
				return fmt.Errorf("command route priority must be between 0 and 1000")
			}
			key := candidate.SourceRef.Key() + "\x00" + candidate.Path.Key()
			if _, exists := candidateKeys[key]; exists {
				return fmt.Errorf("duplicate command route candidate")
			}
			candidateKeys[key] = struct{}{}
		}
	}
	return nil
}

func validateSource(source SourceRef) error {
	if !device.ValidStableID(source.ProviderID) || !device.ValidStableID(source.DeviceID) {
		return fmt.Errorf("invalid provider binding source")
	}
	return nil
}

func validatePropertyPath(path PropertyPath) error {
	if !device.ValidStableID(path.EndpointID) || !device.ValidStableID(path.CapabilityID) || !device.ValidStableID(path.PropertyID) {
		return fmt.Errorf("invalid property path")
	}
	return nil
}

func validateCommandPath(path CommandPath) error {
	if !device.ValidStableID(path.EndpointID) || !device.ValidStableID(path.CapabilityID) || !device.ValidStableID(path.CommandID) {
		return fmt.Errorf("invalid command path")
	}
	return nil
}

func SortBindings(items []Binding) {
	slices.SortFunc(items, func(left, right Binding) int {
		if left.Priority != right.Priority {
			return left.Priority - right.Priority
		}
		if left.ProviderID != right.ProviderID {
			return strings.Compare(left.ProviderID, right.ProviderID)
		}
		return strings.Compare(left.DeviceID, right.DeviceID)
	})
}
