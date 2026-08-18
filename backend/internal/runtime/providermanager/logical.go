package providermanager

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/logicaldevice"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

// SetLogicalDevices replaces the complete durable configuration already
// validated by the application layer. It does not discover or merge anything
// by name; only an explicit Binding can hide a concrete source device.
func (m *Manager) SetLogicalDevices(items []logicaldevice.Config) error {
	configs := make(map[string]logicaldevice.Config, len(items))
	bySource := make(map[string]map[string]struct{})
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("invalid logical device %q: %w", item.ID, err)
		}
		if _, exists := configs[item.ID]; exists {
			return fmt.Errorf("duplicate logical device id %q", item.ID)
		}
		configs[item.ID] = item.Clone()
		for _, binding := range item.Bindings {
			key := providerDeviceKey(binding.ProviderID, binding.DeviceID)
			if bound := bySource[key]; bound != nil {
				return fmt.Errorf("source %q/%q is already linked to another logical device", binding.ProviderID, binding.DeviceID)
			}
			bySource[key] = map[string]struct{}{item.ID: {}}
		}
	}
	m.mu.Lock()
	for id := range configs {
		if owner := m.routes[id]; owner != "" && owner != logicaldevice.ProviderID {
			m.mu.Unlock()
			return fmt.Errorf("logical device id %q conflicts with concrete provider %q", id, owner)
		}
	}
	m.logicalDevices, m.logicalSourceIDs = configs, bySource
	for id := range m.logicalSnapshots {
		if _, exists := configs[id]; !exists {
			delete(m.logicalSnapshots, id)
			delete(m.logicalExplanations, id)
			delete(m.routes, id)
		}
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) LogicalDevices() []logicaldevice.Config {
	m.mu.RLock()
	result := make([]logicaldevice.Config, 0, len(m.logicalDevices))
	for _, item := range m.logicalDevices {
		result = append(result, item.Clone())
	}
	m.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (m *Manager) LogicalDeviceExplanations(id string) ([]logicaldevice.RouteExplanation, bool) {
	m.mu.RLock()
	if _, exists := m.logicalDevices[id]; !exists {
		m.mu.RUnlock()
		return nil, false
	}
	items := append([]logicaldevice.RouteExplanation(nil), m.logicalExplanations[id]...)
	m.mu.RUnlock()
	return items, true
}

// LogicalDeviceCandidates returns review-only suggestions. It deliberately
// requires type, normalized name, and a shared source location; a name alone
// can never be returned as enough evidence to merge devices.
func (m *Manager) LogicalDeviceCandidates(ctx context.Context) ([]logicaldevice.MatchCandidate, error) {
	if _, err := m.DiscoverDevices(ctx); err != nil {
		return nil, err
	}
	m.mu.RLock()
	items := make([]logicaldevice.CandidateStatus, 0, len(m.logicalSourceSnapshots))
	for key, item := range m.logicalSourceSnapshots {
		if _, linked := m.logicalSourceIDs[key]; linked {
			continue
		}
		items = append(items, logicaldevice.CandidateStatus{SourceRef: logicaldevice.SourceRef{ProviderID: item.ProviderID, DeviceID: item.ID}, Name: item.Name, Type: item.Type, HomeID: sourceHomeID(item), RoomID: sourceRoomID(item)})
	}
	m.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool {
		if items[i].ProviderID != items[j].ProviderID {
			return items[i].ProviderID < items[j].ProviderID
		}
		return items[i].DeviceID < items[j].DeviceID
	})
	result := make([]logicaldevice.MatchCandidate, 0)
	for left := 0; left < len(items); left++ {
		for right := left + 1; right < len(items); right++ {
			if items[left].ProviderID == items[right].ProviderID || items[left].Type != items[right].Type || normalizedName(items[left].Name) == "" || normalizedName(items[left].Name) != normalizedName(items[right].Name) {
				continue
			}
			reasons := []string{"same_type", "same_normalized_name"}
			sharedLocation := false
			if items[left].RoomID != "" && items[left].RoomID == items[right].RoomID {
				reasons, sharedLocation = append(reasons, "same_source_room"), true
			} else if items[left].HomeID != "" && items[left].HomeID == items[right].HomeID {
				reasons, sharedLocation = append(reasons, "same_source_home"), true
			}
			if sharedLocation {
				result = append(result, logicaldevice.MatchCandidate{Left: items[left], Right: items[right], Reasons: reasons})
			}
		}
	}
	return result, nil
}

func sourceHomeID(item device.Device) string {
	if item.SourceHomeID != "" {
		return item.SourceHomeID
	}
	return item.HomeID
}
func sourceRoomID(item device.Device) string {
	if item.SourceRoomID != "" {
		return item.SourceRoomID
	}
	return item.RoomID
}
func normalizedName(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

// replaceLogicalSourceSnapshotsLocked installs freshly discovered concrete
// snapshots and returns all currently materializable logical snapshots.
func (m *Manager) replaceLogicalSourceSnapshotsLocked(items map[string]device.Device) []device.Device {
	m.logicalSourceSnapshots = make(map[string]device.Device, len(items))
	for key, item := range items {
		m.logicalSourceSnapshots[key] = item.Clone()
	}
	return m.rebuildLogicalSnapshotsLocked()
}

func (m *Manager) updateLogicalSourceSnapshotLocked(providerID string, item device.Device) ([]device.Device, bool) {
	key := providerDeviceKey(providerID, item.ID)
	if _, linked := m.logicalSourceIDs[key]; !linked {
		return nil, false
	}
	m.logicalSourceSnapshots[key] = item.Clone()
	return m.rebuildLogicalSnapshotsLocked(), true
}

func (m *Manager) rebuildLogicalSnapshotsLocked() []device.Device {
	result := make([]device.Device, 0, len(m.logicalDevices))
	m.logicalExplanations = make(map[string][]logicaldevice.RouteExplanation, len(m.logicalDevices))
	for _, config := range m.logicalDevices {
		item, explanations, ok := composeLogicalSnapshot(config, m.logicalSourceSnapshots)
		if !ok {
			continue
		}
		if previous, exists := m.logicalSnapshots[config.ID]; exists && previous.Sequence >= item.Sequence {
			item.Sequence = previous.Sequence + 1
		}
		m.logicalSnapshots[config.ID] = item.Clone()
		m.logicalExplanations[config.ID] = explanations
		m.routes[config.ID] = logicaldevice.ProviderID
		result = append(result, item)
	}
	for id := range m.logicalSnapshots {
		if _, configured := m.logicalDevices[id]; !configured {
			delete(m.logicalSnapshots, id)
		}
	}
	return result
}

type logicalPropertyChoice struct {
	candidate  logicaldevice.PropertyCandidate
	property   device.Property
	endpoint   device.Endpoint
	capability device.Capability
	available  bool
}

type logicalCommandChoice struct {
	candidate logicaldevice.CommandCandidate
	command   device.CommandDefinition
	available bool
}

func composeLogicalSnapshot(config logicaldevice.Config, sources map[string]device.Device) (device.Device, []logicaldevice.RouteExplanation, bool) {
	bindings := append([]logicaldevice.Binding(nil), config.Bindings...)
	logicaldevice.SortBindings(bindings)
	present := make(map[string]device.Device, len(bindings))
	availability, online, unknown := device.AvailabilityOffline, 0, 0
	var lastUpdate time.Time
	var location device.Device
	for _, binding := range bindings {
		item, exists := sources[binding.Key()]
		if !exists {
			unknown++
			continue
		}
		if item.Type != config.Type {
			unknown++
			continue
		}
		present[binding.Key()] = item
		if location.ID == "" {
			location = item
		}
		if item.LastUpdateAt.After(lastUpdate) {
			lastUpdate = item.LastUpdateAt
		}
		switch item.EffectiveAvailability() {
		case device.AvailabilityOnline:
			online++
		case device.AvailabilityUnknown:
			unknown++
		}
	}
	if len(present) == 0 {
		return device.Device{}, nil, false
	}
	if online > 0 {
		availability = device.AvailabilityOnline
	} else if unknown > 0 {
		availability = device.AvailabilityUnknown
	}
	item := device.Device{SchemaVersion: device.SchemaVersion, ID: config.ID, ProviderID: logicaldevice.ProviderID, Name: config.Name, Type: config.Type, Availability: availability, Online: availability == device.AvailabilityOnline, LastUpdateAt: lastUpdate}
	if item.LastUpdateAt.IsZero() {
		item.LastUpdateAt = time.Now().UTC()
	}
	item.HomeID, item.HomeName, item.RoomID, item.RoomName = location.HomeID, location.HomeName, location.RoomID, location.RoomName
	item.SourceHomeID, item.SourceHomeName, item.SourceRoomID, item.SourceRoomName = location.SourceHomeID, location.SourceHomeName, location.SourceRoomID, location.SourceRoomName
	item.LocationMode = location.LocationMode
	explanations := make([]logicaldevice.RouteExplanation, 0)
	properties := logicalPropertyPaths(config, present)
	for _, path := range properties {
		choices := propertyChoices(config, path, present, bindings)
		selected, ok := selectPropertyChoice(choices)
		if !ok {
			continue
		}
		property := selected.property
		property.Definition.ID = path.PropertyID
		addLogicalProperty(&item, path, selected.endpoint, selected.capability, property)
		if len(choices) > 1 {
			explanations = append(explanations, explainProperty(config.ID, path, choices, selected))
		}
	}
	commands := logicalCommandPaths(config, present)
	for _, path := range commands {
		choices := commandChoices(config, path, present, bindings)
		selected, ok := selectCommandChoice(choices)
		if !ok {
			continue
		}
		command := selected.command
		command.ID = path.CommandID
		addLogicalCommand(&item, path, command)
		if len(choices) > 1 {
			explanations = append(explanations, explainCommand(config.ID, path, choices, selected))
		}
	}
	if err := item.ValidateStructure(); err != nil {
		return device.Device{}, nil, false
	}
	sort.Slice(explanations, func(i, j int) bool {
		if explanations[i].Kind != explanations[j].Kind {
			return explanations[i].Kind < explanations[j].Kind
		}
		return explanations[i].Path < explanations[j].Path
	})
	return item, explanations, true
}

func logicalPropertyPaths(config logicaldevice.Config, sources map[string]device.Device) []logicaldevice.PropertyPath {
	set := map[string]logicaldevice.PropertyPath{}
	for _, route := range config.PropertyRoutes {
		set[route.Path.Key()] = route.Path
	}
	for _, source := range sources {
		for _, endpoint := range source.Endpoints {
			for _, capability := range endpoint.Capabilities {
				for _, property := range capability.Properties {
					path := logicaldevice.PropertyPath{EndpointID: endpoint.ID, CapabilityID: capability.ID, PropertyID: property.Definition.ID}
					if _, explicit := explicitPropertyRoute(config, path); !explicit {
						set[path.Key()] = path
					}
				}
			}
		}
	}
	return sortedPropertyPaths(set)
}

func logicalCommandPaths(config logicaldevice.Config, sources map[string]device.Device) []logicaldevice.CommandPath {
	set := map[string]logicaldevice.CommandPath{}
	for _, route := range config.CommandRoutes {
		set[route.Path.Key()] = route.Path
	}
	for _, source := range sources {
		for _, endpoint := range source.Endpoints {
			for _, capability := range endpoint.Capabilities {
				for _, command := range capability.Commands {
					path := logicaldevice.CommandPath{EndpointID: endpoint.ID, CapabilityID: capability.ID, CommandID: command.ID}
					if _, explicit := explicitCommandRoute(config, path); !explicit {
						set[path.Key()] = path
					}
				}
			}
		}
	}
	return sortedCommandPaths(set)
}

func propertyChoices(config logicaldevice.Config, path logicaldevice.PropertyPath, sources map[string]device.Device, bindings []logicaldevice.Binding) []logicalPropertyChoice {
	candidates := propertyCandidates(config, path, bindings)
	result := make([]logicalPropertyChoice, 0, len(candidates))
	for _, candidate := range candidates {
		source, exists := sources[candidate.SourceRef.Key()]
		if !exists {
			continue
		}
		property, endpoint, capability, exists := sourceProperty(source, candidate.Path)
		if !exists {
			continue
		}
		result = append(result, logicalPropertyChoice{candidate: candidate, property: property, endpoint: endpoint, capability: capability, available: source.IsOnline()})
	}
	return result
}

func commandChoices(config logicaldevice.Config, path logicaldevice.CommandPath, sources map[string]device.Device, bindings []logicaldevice.Binding) []logicalCommandChoice {
	candidates := commandCandidates(config, path, bindings)
	result := make([]logicalCommandChoice, 0, len(candidates))
	for _, candidate := range candidates {
		source, exists := sources[candidate.SourceRef.Key()]
		if !exists {
			continue
		}
		command, exists := sourceCommand(source, candidate.Path)
		if !exists {
			continue
		}
		result = append(result, logicalCommandChoice{candidate: candidate, command: command, available: source.IsOnline()})
	}
	return result
}

func propertyCandidates(config logicaldevice.Config, path logicaldevice.PropertyPath, bindings []logicaldevice.Binding) []logicaldevice.PropertyCandidate {
	if route, explicit := explicitPropertyRoute(config, path); explicit {
		result := append([]logicaldevice.PropertyCandidate(nil), route.Candidates...)
		sort.SliceStable(result, func(i, j int) bool { return result[i].Priority < result[j].Priority })
		return result
	}
	result := make([]logicaldevice.PropertyCandidate, 0, len(bindings))
	for index, binding := range bindings {
		result = append(result, logicaldevice.PropertyCandidate{SourceRef: binding.SourceRef, Path: path, Priority: binding.Priority, AllowFallback: index < len(bindings)-1})
	}
	return result
}

func commandCandidates(config logicaldevice.Config, path logicaldevice.CommandPath, bindings []logicaldevice.Binding) []logicaldevice.CommandCandidate {
	if route, explicit := explicitCommandRoute(config, path); explicit {
		result := append([]logicaldevice.CommandCandidate(nil), route.Candidates...)
		sort.SliceStable(result, func(i, j int) bool { return result[i].Priority < result[j].Priority })
		return result
	}
	result := make([]logicaldevice.CommandCandidate, 0, len(bindings))
	for index, binding := range bindings {
		result = append(result, logicaldevice.CommandCandidate{SourceRef: binding.SourceRef, Path: path, Priority: binding.Priority, AllowFallback: index < len(bindings)-1})
	}
	return result
}

func explicitPropertyRoute(config logicaldevice.Config, path logicaldevice.PropertyPath) (logicaldevice.PropertyRoute, bool) {
	for _, route := range config.PropertyRoutes {
		if route.Path == path {
			return route, true
		}
	}
	return logicaldevice.PropertyRoute{}, false
}
func explicitCommandRoute(config logicaldevice.Config, path logicaldevice.CommandPath) (logicaldevice.CommandRoute, bool) {
	for _, route := range config.CommandRoutes {
		if route.Path == path {
			return route, true
		}
	}
	return logicaldevice.CommandRoute{}, false
}

func selectPropertyChoice(items []logicalPropertyChoice) (logicalPropertyChoice, bool) {
	for _, item := range items {
		if item.available {
			return item, true
		}
	}
	if len(items) == 0 {
		return logicalPropertyChoice{}, false
	}
	return items[0], true
}
func selectCommandChoice(items []logicalCommandChoice) (logicalCommandChoice, bool) {
	for _, item := range items {
		if item.available {
			return item, true
		}
	}
	if len(items) == 0 {
		return logicalCommandChoice{}, false
	}
	return items[0], true
}

func sourceProperty(item device.Device, path logicaldevice.PropertyPath) (device.Property, device.Endpoint, device.Capability, bool) {
	for _, endpoint := range item.Endpoints {
		if endpoint.ID != path.EndpointID {
			continue
		}
		for _, capability := range endpoint.Capabilities {
			if capability.ID != path.CapabilityID {
				continue
			}
			for _, property := range capability.Properties {
				if property.Definition.ID == path.PropertyID {
					return property, endpoint, capability, true
				}
			}
		}
	}
	return device.Property{}, device.Endpoint{}, device.Capability{}, false
}
func sourceCommand(item device.Device, path logicaldevice.CommandPath) (device.CommandDefinition, bool) {
	for _, endpoint := range item.Endpoints {
		if endpoint.ID != path.EndpointID {
			continue
		}
		for _, capability := range endpoint.Capabilities {
			if capability.ID != path.CapabilityID {
				continue
			}
			for _, command := range capability.Commands {
				if command.ID == path.CommandID {
					return command, true
				}
			}
		}
	}
	return device.CommandDefinition{}, false
}

func addLogicalProperty(item *device.Device, path logicaldevice.PropertyPath, sourceEndpoint device.Endpoint, sourceCapability device.Capability, property device.Property) {
	endpoint := logicalEndpoint(item, path.EndpointID, sourceEndpoint.Name, sourceEndpoint.Type)
	capability := logicalCapability(endpoint, path.CapabilityID, sourceCapability.Type)
	for _, existing := range capability.Properties {
		if existing.Definition.ID == property.Definition.ID {
			return
		}
	}
	capability.Properties = append(capability.Properties, property)
}
func addLogicalCommand(item *device.Device, path logicaldevice.CommandPath, command device.CommandDefinition) {
	endpoint := logicalEndpoint(item, path.EndpointID, path.EndpointID, path.EndpointID)
	capability := logicalCapability(endpoint, path.CapabilityID, path.CapabilityID)
	for _, existing := range capability.Commands {
		if existing.ID == command.ID {
			return
		}
	}
	capability.Commands = append(capability.Commands, command)
}
func logicalEndpoint(item *device.Device, id, name, kind string) *device.Endpoint {
	for index := range item.Endpoints {
		if item.Endpoints[index].ID == id {
			return &item.Endpoints[index]
		}
	}
	item.Endpoints = append(item.Endpoints, device.Endpoint{ID: id, Name: name, Type: kind})
	return &item.Endpoints[len(item.Endpoints)-1]
}
func logicalCapability(endpoint *device.Endpoint, id, kind string) *device.Capability {
	for index := range endpoint.Capabilities {
		if endpoint.Capabilities[index].ID == id {
			return &endpoint.Capabilities[index]
		}
	}
	endpoint.Capabilities = append(endpoint.Capabilities, device.Capability{ID: id, Type: kind})
	return &endpoint.Capabilities[len(endpoint.Capabilities)-1]
}

func explainProperty(id string, path logicaldevice.PropertyPath, choices []logicalPropertyChoice, selected logicalPropertyChoice) logicaldevice.RouteExplanation {
	candidates := make([]logicaldevice.CandidateSelection, 0, len(choices))
	reason := "provider_priority"
	if !selected.available {
		reason = "all_sources_unavailable"
	} else if choices[0].candidate.SourceRef != selected.candidate.SourceRef {
		reason = "safe_fallback_provider_unavailable"
	}
	for _, choice := range choices {
		candidates = append(candidates, logicaldevice.CandidateSelection{SourceRef: choice.candidate.SourceRef, Path: choice.candidate.Path.Key(), Priority: choice.candidate.Priority, Available: choice.available, Selected: choice.candidate.SourceRef == selected.candidate.SourceRef && choice.candidate.Path == selected.candidate.Path})
	}
	return logicaldevice.RouteExplanation{LogicalDeviceID: id, Kind: "property", Path: path.Key(), Reason: reason, Selected: candidates[indexSelection(candidates)], Candidates: candidates}
}
func explainCommand(id string, path logicaldevice.CommandPath, choices []logicalCommandChoice, selected logicalCommandChoice) logicaldevice.RouteExplanation {
	candidates := make([]logicaldevice.CandidateSelection, 0, len(choices))
	reason := "provider_priority"
	if !selected.available {
		reason = "all_sources_unavailable"
	} else if choices[0].candidate.SourceRef != selected.candidate.SourceRef {
		reason = "safe_fallback_provider_unavailable"
	}
	for _, choice := range choices {
		candidates = append(candidates, logicaldevice.CandidateSelection{SourceRef: choice.candidate.SourceRef, Path: choice.candidate.Path.Key(), Priority: choice.candidate.Priority, Available: choice.available, Selected: choice.candidate.SourceRef == selected.candidate.SourceRef && choice.candidate.Path == selected.candidate.Path})
	}
	return logicaldevice.RouteExplanation{LogicalDeviceID: id, Kind: "command", Path: path.Key(), Reason: reason, Selected: candidates[indexSelection(candidates)], Candidates: candidates}
}
func indexSelection(items []logicaldevice.CandidateSelection) int {
	for index, item := range items {
		if item.Selected {
			return index
		}
	}
	return 0
}
func sortedPropertyPaths(items map[string]logicaldevice.PropertyPath) []logicaldevice.PropertyPath {
	result := make([]logicaldevice.PropertyPath, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key() < result[j].Key() })
	return result
}
func sortedCommandPaths(items map[string]logicaldevice.CommandPath) []logicaldevice.CommandPath {
	result := make([]logicaldevice.CommandPath, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key() < result[j].Key() })
	return result
}

func (m *Manager) writeLogicalProperty(ctx context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	m.mu.RLock()
	config, exists := m.logicalDevices[request.DeviceID]
	bindings := append([]logicaldevice.Binding(nil), config.Bindings...)
	snapshots := make(map[string]device.Device, len(m.logicalSourceSnapshots))
	for key, item := range m.logicalSourceSnapshots {
		snapshots[key] = item.Clone()
	}
	m.mu.RUnlock()
	if !exists {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	logicaldevice.SortBindings(bindings)
	path := logicaldevice.PropertyPath{EndpointID: request.EndpointID, CapabilityID: request.CapabilityID, PropertyID: request.PropertyID}
	candidates := propertyCandidates(config, path, bindings)
	if len(candidates) == 0 {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	for index, candidate := range candidates {
		source := snapshots[candidate.SourceRef.Key()]
		if _, _, _, exists := sourceProperty(source, candidate.Path); !exists {
			continue
		}
		if source.EffectiveAvailability() == device.AvailabilityOffline {
			if candidate.AllowFallback && index < len(candidates)-1 {
				continue
			}
			return device.Device{}, providersdk.ErrProviderUnavailable
		}
		updated, err := m.writeConcreteProperty(ctx, candidate, request.Value)
		if err == nil {
			return m.finalizeLogicalSourceSnapshot(candidate.SourceRef, updated)
		}
		if !canFallback(err) || !candidate.AllowFallback || index == len(candidates)-1 {
			return device.Device{}, err
		}
	}
	return device.Device{}, providersdk.ErrPropertyUnsupported
}

func (m *Manager) readLogicalProperty(ctx context.Context, request providersdk.PropertyReadRequest) (device.Property, error) {
	m.mu.RLock()
	config, exists := m.logicalDevices[request.DeviceID]
	bindings := append([]logicaldevice.Binding(nil), config.Bindings...)
	snapshots := make(map[string]device.Device, len(m.logicalSourceSnapshots))
	for key, item := range m.logicalSourceSnapshots {
		snapshots[key] = item.Clone()
	}
	m.mu.RUnlock()
	if !exists {
		return device.Property{}, providersdk.ErrDeviceNotFound
	}
	logicaldevice.SortBindings(bindings)
	path := logicaldevice.PropertyPath{EndpointID: request.EndpointID, CapabilityID: request.CapabilityID, PropertyID: request.PropertyID}
	candidates := propertyCandidates(config, path, bindings)
	for index, candidate := range candidates {
		source := snapshots[candidate.SourceRef.Key()]
		if _, _, _, exists := sourceProperty(source, candidate.Path); !exists {
			continue
		}
		if source.EffectiveAvailability() == device.AvailabilityOffline {
			if candidate.AllowFallback && index < len(candidates)-1 {
				continue
			}
			return device.Property{}, providersdk.ErrProviderUnavailable
		}
		property, err := m.readConcreteProperty(ctx, candidate)
		if err == nil {
			property.Definition.ID = request.PropertyID
			return property, nil
		}
		if !canFallback(err) || !candidate.AllowFallback || index == len(candidates)-1 {
			return device.Property{}, err
		}
	}
	return device.Property{}, providersdk.ErrPropertyUnsupported
}

func (m *Manager) executeLogicalCommand(ctx context.Context, request providersdk.CommandRequest) (device.Device, error) {
	m.mu.RLock()
	config, exists := m.logicalDevices[request.DeviceID]
	bindings := append([]logicaldevice.Binding(nil), config.Bindings...)
	snapshots := make(map[string]device.Device, len(m.logicalSourceSnapshots))
	for key, item := range m.logicalSourceSnapshots {
		snapshots[key] = item.Clone()
	}
	m.mu.RUnlock()
	if !exists {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	logicaldevice.SortBindings(bindings)
	path := logicaldevice.CommandPath{EndpointID: request.EndpointID, CapabilityID: request.CapabilityID, CommandID: request.CommandID}
	candidates := commandCandidates(config, path, bindings)
	for index, candidate := range candidates {
		source := snapshots[candidate.SourceRef.Key()]
		command, exists := sourceCommand(source, candidate.Path)
		if !exists {
			continue
		}
		if source.EffectiveAvailability() == device.AvailabilityOffline {
			if candidate.AllowFallback && command.Idempotent && index < len(candidates)-1 {
				continue
			}
			return device.Device{}, providersdk.ErrProviderUnavailable
		}
		updated, err := m.executeConcreteCommand(ctx, candidate, request.Parameters, request.IdempotencyKey)
		if err == nil {
			return m.finalizeLogicalSourceSnapshot(candidate.SourceRef, updated)
		}
		// Commands are only safe to retry after a definite availability failure
		// and only if the concrete command declares itself idempotent.
		if !canFallback(err) || !candidate.AllowFallback || !command.Idempotent || index == len(candidates)-1 {
			return device.Device{}, err
		}
	}
	return device.Device{}, providersdk.ErrCommandUnsupported
}

func canFallback(err error) bool {
	return errors.Is(err, providersdk.ErrProviderUnavailable) || errors.Is(err, providersdk.ErrDeviceNotFound)
}

func (m *Manager) writeConcreteProperty(ctx context.Context, candidate logicaldevice.PropertyCandidate, value device.PropertyValue) (device.Device, error) {
	m.mu.RLock()
	current := m.providers[candidate.ProviderID]
	running := current != nil && current.status == "running"
	m.mu.RUnlock()
	if current == nil {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	if !running {
		return device.Device{}, providersdk.ErrProviderUnavailable
	}
	writer, ok := current.provider.(providersdk.PropertyWriter)
	if !ok {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	return writer.WriteProperty(ctx, providersdk.PropertyWriteRequest{DeviceID: candidate.DeviceID, EndpointID: candidate.Path.EndpointID, CapabilityID: candidate.Path.CapabilityID, PropertyID: candidate.Path.PropertyID, Value: value})
}
func (m *Manager) readConcreteProperty(ctx context.Context, candidate logicaldevice.PropertyCandidate) (device.Property, error) {
	m.mu.RLock()
	current := m.providers[candidate.ProviderID]
	running := current != nil && current.status == "running"
	m.mu.RUnlock()
	if current == nil {
		return device.Property{}, providersdk.ErrDeviceNotFound
	}
	if !running {
		return device.Property{}, providersdk.ErrProviderUnavailable
	}
	reader, ok := current.provider.(providersdk.PropertyReader)
	if !ok {
		return device.Property{}, providersdk.ErrPropertyUnsupported
	}
	return reader.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: candidate.DeviceID, EndpointID: candidate.Path.EndpointID, CapabilityID: candidate.Path.CapabilityID, PropertyID: candidate.Path.PropertyID})
}
func (m *Manager) executeConcreteCommand(ctx context.Context, candidate logicaldevice.CommandCandidate, parameters map[string]device.PropertyValue, key string) (device.Device, error) {
	m.mu.RLock()
	current := m.providers[candidate.ProviderID]
	running := current != nil && current.status == "running"
	m.mu.RUnlock()
	if current == nil {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	if !running {
		return device.Device{}, providersdk.ErrProviderUnavailable
	}
	executor, ok := current.provider.(providersdk.CommandExecutor)
	if !ok {
		return device.Device{}, providersdk.ErrCommandUnsupported
	}
	return executor.ExecuteCommand(ctx, providersdk.CommandRequest{DeviceID: candidate.DeviceID, EndpointID: candidate.Path.EndpointID, CapabilityID: candidate.Path.CapabilityID, CommandID: candidate.Path.CommandID, Parameters: parameters, IdempotencyKey: key})
}

func (m *Manager) finalizeLogicalSourceSnapshot(source logicaldevice.SourceRef, item device.Device) (device.Device, error) {
	item.ProviderID = source.ProviderID
	item.NormalizeAvailability()
	if err := item.ValidateStructure(); err != nil {
		return device.Device{}, fmt.Errorf("provider %q returned invalid logical source snapshot: %w", source.ProviderID, err)
	}
	m.mu.Lock()
	m.logicalSourceSnapshots[source.Key()] = item.Clone()
	rebuilt := m.rebuildLogicalSnapshotsLocked()
	snapshot, exists := m.logicalSnapshotsForIDLocked(rebuilt, source)
	m.mu.Unlock()
	if !exists {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	m.broadcast(snapshot)
	return snapshot, nil
}

func (m *Manager) logicalSnapshotsForIDLocked(_ []device.Device, source logicaldevice.SourceRef) (device.Device, bool) {
	for id, config := range m.logicalDevices {
		for _, binding := range config.Bindings {
			if binding.SourceRef == source {
				item, exists := m.logicalSnapshots[id]
				return item.Clone(), exists
			}
		}
	}
	return device.Device{}, false
}
