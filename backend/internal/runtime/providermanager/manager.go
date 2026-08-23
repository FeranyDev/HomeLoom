package providermanager

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/logicaldevice"
	domainmedia "github.com/feranydev/homeloom/backend/internal/domain/media"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type managedProvider struct {
	lifecycle         sync.Mutex
	provider          providersdk.Provider
	status            string
	err               string
	unsubscribe       func()
	unsubscribeEvents func()
	deviceIDs         map[string]struct{}
	retryCount        int
	nextRetryAt       time.Time
	transitionedAt    time.Time
}

type capabilityRoute struct {
	deviceID, endpointID, capabilityID string
}

type capabilityDelegate struct {
	canonicalProviderID string
	sourceProviderID    string
	sourceDeviceID      string
	sourceEndpointID    string
	sourceCapabilityID  string
	available           bool
}

type Manager struct {
	mu                                 sync.RWMutex
	providers                          map[string]*managedProvider
	order                              []string
	routes                             map[string]string
	mediaRoutes                        map[string]string
	capabilityRoutes                   map[capabilityRoute]capabilityDelegate
	boundSources                       map[string]string
	hiddenSources                      map[string]struct{}
	canonicalSnapshots                 map[string]device.Device
	logicalDevices                     map[string]logicaldevice.Config
	logicalSourceIDs                   map[string]map[string]struct{}
	logicalSourceSnapshots             map[string]device.Device
	logicalSnapshots                   map[string]device.Device
	logicalExplanations                map[string][]logicaldevice.RouteExplanation
	identityStore                      DeviceIdentityStore
	listeners                          map[uint64]func(device.Device)
	snapshotEventListeners             map[uint64]func(device.Device)
	snapshotRefreshListeners           map[uint64]func(device.Device)
	eventListeners                     map[uint64]func(providersdk.DeviceEvent)
	capabilityAvailabilityListeners    map[uint64]func(providersdk.CapabilityAvailability)
	propertyInterests                  map[string][]providersdk.PropertyInterest
	nextListener                       uint64
	nextSnapshotEventListener          uint64
	nextSnapshotRefreshListener        uint64
	nextEventListener                  uint64
	nextCapabilityAvailabilityListener uint64
	initialized                        bool
	lifecycleCtx                       context.Context
	lifecycleCancel                    context.CancelFunc
	retryRunning                       bool
	retryDone                          chan struct{}
	closed                             bool
}

func New(items ...providersdk.Provider) (*Manager, error) {
	m := &Manager{providers: make(map[string]*managedProvider), routes: make(map[string]string), mediaRoutes: make(map[string]string), capabilityRoutes: make(map[capabilityRoute]capabilityDelegate), boundSources: make(map[string]string), hiddenSources: make(map[string]struct{}), canonicalSnapshots: make(map[string]device.Device), logicalDevices: make(map[string]logicaldevice.Config), logicalSourceIDs: make(map[string]map[string]struct{}), logicalSourceSnapshots: make(map[string]device.Device), logicalSnapshots: make(map[string]device.Device), logicalExplanations: make(map[string][]logicaldevice.RouteExplanation), listeners: make(map[uint64]func(device.Device)), snapshotEventListeners: make(map[uint64]func(device.Device)), snapshotRefreshListeners: make(map[uint64]func(device.Device)), eventListeners: make(map[uint64]func(providersdk.DeviceEvent)), capabilityAvailabilityListeners: make(map[uint64]func(providersdk.CapabilityAvailability)), propertyInterests: make(map[string][]providersdk.PropertyInterest)}
	for _, item := range items {
		id := item.Manifest().ID
		if id == "" {
			return nil, fmt.Errorf("provider id is required")
		}
		if _, exists := m.providers[id]; exists {
			return nil, fmt.Errorf("duplicate provider id %q", id)
		}
		m.providers[id] = &managedProvider{provider: item, status: "created", deviceIDs: make(map[string]struct{}), transitionedAt: time.Now().UTC()}
		m.order = append(m.order, id)
	}
	return m, nil
}

func (m *Manager) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: "provider-manager", Type: "manager", Name: "Provider Manager", Version: "0.2.0"}
}
func (m *Manager) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyRead: true, PropertyWrite: true, Commands: true, Events: true}
}

// Provider returns a running provider instance for provider-specific,
// read-only operations that should reuse an established network connection.
func (m *Manager) Provider(id string) (providersdk.Provider, bool) {
	m.mu.RLock()
	current := m.providers[id]
	if current == nil || current.status != "running" {
		m.mu.RUnlock()
		return nil, false
	}
	provider := current.provider
	m.mu.RUnlock()
	return provider, true
}

// ProviderAny returns a configured Provider regardless of whether it is
// currently running. It is intentionally separate from Provider: callers that
// need a live transport must continue to use Provider, while authentication
// recovery can inspect a provider retained in auth_required state.
func (m *Manager) ProviderAny(id string) (providersdk.Provider, bool) {
	m.mu.RLock()
	current := m.providers[id]
	if current == nil {
		m.mu.RUnlock()
		return nil, false
	}
	provider := current.provider
	m.mu.RUnlock()
	return provider, true
}

// SetPropertyInterests routes the complete active mapping set to each concrete
// Provider and remembers it for later Provider replacement. Updating mapping
// interests never reconnects Provider transports.
func (m *Manager) SetPropertyInterests(interests []providersdk.PropertyInterest) {
	grouped := make(map[string][]providersdk.PropertyInterest)
	for _, interest := range interests {
		if interest.ProviderID == "" {
			continue
		}
		grouped[interest.ProviderID] = append(grouped[interest.ProviderID], interest)
	}
	m.mu.Lock()
	m.propertyInterests = grouped
	providers := make(map[string]providersdk.Provider, len(m.providers))
	for id, current := range m.providers {
		if current != nil {
			providers[id] = current.provider
		}
	}
	m.mu.Unlock()
	for id, provider := range providers {
		if setter, ok := provider.(providersdk.PropertyInterestSetter); ok {
			setter.SetPropertyInterests(append([]providersdk.PropertyInterest(nil), grouped[id]...))
		}
	}
}

func (m *Manager) Initialize(ctx context.Context) error {
	m.mu.Lock()
	if m.initialized {
		m.mu.Unlock()
		m.startRetryWorker()
		return nil
	}
	if m.lifecycleCancel == nil {
		m.lifecycleCtx, m.lifecycleCancel = context.WithCancel(ctx)
	}
	m.mu.Unlock()
	m.mu.RLock()
	ids := append([]string(nil), m.order...)
	m.mu.RUnlock()
	for _, id := range ids {
		m.mu.RLock()
		current := m.providers[id]
		m.mu.RUnlock()
		current.lifecycle.Lock()
		if err := current.provider.Initialize(ctx); err != nil {
			m.mu.Lock()
			m.markFailureLocked(current, err)
			m.mu.Unlock()
			current.lifecycle.Unlock()
			continue
		}
		m.mu.Lock()
		current.status, current.err, current.transitionedAt = "running", "", time.Now().UTC()
		m.mu.Unlock()
		m.attach(id, current)
		current.lifecycle.Unlock()
	}
	m.mu.Lock()
	m.initialized = true
	m.mu.Unlock()
	m.startRetryWorker()
	return nil
}

func (m *Manager) DiscoverDevices(ctx context.Context) ([]device.Device, error) {
	m.mu.RLock()
	ids := append([]string(nil), m.order...)
	m.mu.RUnlock()
	result := make([]device.Device, 0)
	routes := make(map[string]string)
	logicalSourceSnapshots := make(map[string]device.Device)
	capabilitySourceDevices := make(map[string]device.Device)
	bindings := make([]providersdk.CapabilityBinding, 0)
	hiddenSources := make(map[string]struct{})
providerLoop:
	for _, id := range ids {
		m.mu.RLock()
		current := m.providers[id]
		running := current != nil && current.status == "running"
		m.mu.RUnlock()
		if !running {
			continue
		}
		discoverer, ok := current.provider.(providersdk.Discoverer)
		if !ok {
			continue
		}
		items, err := discoverer.DiscoverDevices(ctx)
		if err != nil {
			m.mu.Lock()
			m.markFailureLocked(current, err)
			m.mu.Unlock()
			m.startRetryWorker()
			continue
		}
		currentIDs := make(map[string]struct{}, len(items))
		for _, item := range items {
			item.ProviderID = id
			item.NormalizeAvailability()
			if err := item.ValidateStructure(); err != nil {
				m.mu.Lock()
				m.markFailureLocked(current, fmt.Errorf("invalid device snapshot: %w", err))
				m.mu.Unlock()
				m.startRetryWorker()
				continue providerLoop
			}
			if owner, exists := routes[item.ID]; exists {
				return nil, fmt.Errorf("device id %q is provided by both %q and %q", item.ID, owner, id)
			}
			routes[item.ID] = id
			currentIDs[item.ID] = struct{}{}
			result = append(result, item)
			key := providerDeviceKey(id, item.ID)
			capabilitySourceDevices[key] = item
			logicalSourceSnapshots[key] = item.Clone()
		}
		// A Provider may expose a richer native MIoT catalog than its public
		// configured Device projection. Capability bindings consume that
		// catalog so a control-only Xiaomi camera can contribute privacy,
		// indicator, motion and PTZ controls without becoming a media owner.
		// The ordinary route must still exist; catalog-only identities are
		// never promoted into the Device registry.
		if cataloger, ok := current.provider.(providersdk.SourceCataloger); ok {
			if catalog, catalogErr := cataloger.SourceCatalog(ctx); catalogErr == nil {
				for _, candidate := range catalog {
					if _, routed := currentIDs[candidate.ID]; !routed {
						continue
					}
					candidate.ProviderID = id
					candidate.NormalizeAvailability()
					if candidate.ValidateStructure() == nil {
						capabilitySourceDevices[providerDeviceKey(id, candidate.ID)] = candidate.Device
					}
				}
			}
		}
		if source, ok := current.provider.(providersdk.CapabilityBindingSource); ok {
			for _, binding := range source.CapabilityBindings() {
				if binding.DeviceID == "" || binding.ProviderID == "" || binding.SourceDeviceID == "" {
					return nil, fmt.Errorf("provider %q returned an incomplete capability binding", id)
				}
				if binding.ProviderID == id {
					return nil, fmt.Errorf("device %q cannot bind capabilities from its own provider %q", binding.DeviceID, id)
				}
				bindings = append(bindings, binding)
			}
		}
		if source, ok := current.provider.(providersdk.HiddenDeviceSource); ok {
			for _, deviceID := range source.HiddenDeviceIDs() {
				if _, exists := currentIDs[deviceID]; !exists {
					return nil, fmt.Errorf("provider %q marked undiscovered device %q as hidden", id, deviceID)
				}
				hiddenSources[providerDeviceKey(id, deviceID)] = struct{}{}
			}
		}
		m.mu.Lock()
		if live := m.providers[id]; live == current {
			live.deviceIDs = currentIDs
		}
		m.mu.Unlock()
	}
	capabilityRoutes := make(map[capabilityRoute]capabilityDelegate)
	boundSources := make(map[string]string)
	sourceClaims := make(map[string]string)
	canonicalIndexes := make(map[string]int, len(result))
	for index := range result {
		canonicalIndexes[providerDeviceKey(result[index].ProviderID, result[index].ID)] = index
	}
	for _, binding := range bindings {
		canonicalProviderID := routes[binding.DeviceID]
		canonicalKey := providerDeviceKey(canonicalProviderID, binding.DeviceID)
		index, canonicalExists := canonicalIndexes[canonicalKey]
		if !canonicalExists {
			return nil, fmt.Errorf("capability binding target device %q was not discovered", binding.DeviceID)
		}
		sourceKey := providerDeviceKey(binding.ProviderID, binding.SourceDeviceID)
		if previous, duplicate := sourceClaims[sourceKey]; duplicate {
			return nil, fmt.Errorf("control device %q from provider %q is bound to both %q and %q", binding.SourceDeviceID, binding.ProviderID, previous, binding.DeviceID)
		}
		sourceClaims[sourceKey] = binding.DeviceID
		source, sourceExists := capabilitySourceDevices[sourceKey]
		if !sourceExists {
			// A missing or stopped control Provider degrades only controls.
			// The canonical Camera and its media source remain discoverable.
			continue
		}
		m.mu.RLock()
		sourceProvider := m.providers[binding.ProviderID]
		m.mu.RUnlock()
		sourceType := ""
		if sourceProvider != nil {
			sourceType = sourceProvider.provider.Manifest().Type
		}
		if sourceType != "xiaomi" && sourceType != "xiaomi-miot-cloud" {
			return nil, fmt.Errorf(
				"control device %q uses unsupported provider type %q",
				binding.SourceDeviceID, sourceType,
			)
		}
		boundSources[sourceKey] = binding.DeviceID
		merged, delegates, err := mergeControlCapabilities(result[index], source, canonicalProviderID, binding.ProviderID)
		if err != nil {
			return nil, fmt.Errorf("merge controls for camera %q: %w", binding.DeviceID, err)
		}
		result[index] = merged
		for route, delegate := range delegates {
			capabilityRoutes[route] = delegate
		}
	}
	// Preserve identities for every concrete snapshot before explicit source
	// hiding. Logical binding, camera composition, or a temporary Provider
	// absence must not make an endpoint/capability identity disappear.
	identitySources := cloneDeviceList(result)
	if len(boundSources) != 0 || len(hiddenSources) != 0 {
		filtered := result[:0]
		for _, item := range result {
			key := providerDeviceKey(item.ProviderID, item.ID)
			if _, hidden := boundSources[key]; hidden {
				continue
			}
			if _, hidden := hiddenSources[key]; hidden {
				continue
			}
			filtered = append(filtered, item)
		}
		result = filtered
	}
	m.mu.Lock()
	// Logical aggregation runs after specialized Camera capability composition
	// but before publishing the device list. Only explicit bindings hide their
	// concrete source cards; similar names are never considered here.
	for logicalID := range m.logicalDevices {
		if owner := routes[logicalID]; owner != "" {
			m.mu.Unlock()
			return nil, fmt.Errorf("logical device id %q conflicts with concrete provider %q", logicalID, owner)
		}
	}
	logicalItems := m.replaceLogicalSourceSnapshotsLocked(logicalSourceSnapshots)
	if len(m.logicalSourceIDs) != 0 {
		filtered := result[:0]
		for _, item := range result {
			if _, linked := m.logicalSourceIDs[providerDeviceKey(item.ProviderID, item.ID)]; linked {
				continue
			}
			filtered = append(filtered, item)
		}
		result = filtered
	}
	for _, item := range logicalItems {
		routes[item.ID] = logicaldevice.ProviderID
		result = append(result, item)
	}
	m.routes = routes
	m.capabilityRoutes = capabilityRoutes
	m.boundSources = boundSources
	m.hiddenSources = hiddenSources
	m.canonicalSnapshots = make(map[string]device.Device, len(result))
	for _, item := range result {
		m.canonicalSnapshots[providerDeviceKey(item.ProviderID, item.ID)] = item.Clone()
	}
	m.mu.Unlock()
	if err := m.persistDiscoveryIdentities(ctx, identitySources, logicalItems); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// DiscoverMediaSources aggregates optional camera media extensions without
// creating a second device registry. Provider and device identity remain
// authoritative in the normal Provider routes.
func (m *Manager) DiscoverMediaSources(ctx context.Context) ([]domainmedia.MediaSourceDescriptor, error) {
	m.mu.RLock()
	ids := append([]string(nil), m.order...)
	m.mu.RUnlock()
	result := make([]domainmedia.MediaSourceDescriptor, 0)
	routes := make(map[string]string)
	for _, id := range ids {
		m.mu.RLock()
		current := m.providers[id]
		running := current != nil && current.status == "running"
		m.mu.RUnlock()
		if !running {
			continue
		}
		// Media ownership is intentionally restricted to the independent
		// Camera Provider. Account/catalog Providers may expose similarly
		// shaped helper methods internally, but must never publish sources.
		if current.provider.Manifest().Type != "camera" {
			continue
		}
		discoverer, ok := current.provider.(providersdk.MediaSourceDiscoverer)
		if !ok {
			continue
		}
		items, err := discoverer.DiscoverMediaSources(ctx)
		if err != nil {
			return nil, fmt.Errorf("discover media sources from provider %q: %w", id, err)
		}
		for _, item := range items {
			if item.ProviderID != id {
				return nil, fmt.Errorf("provider %q returned media source %q with provider id %q", id, item.DeviceID, item.ProviderID)
			}
			if err := item.Validate(); err != nil {
				return nil, fmt.Errorf("provider %q returned invalid media source %q: %w", id, item.DeviceID, err)
			}
			if owner, exists := routes[item.DeviceID]; exists {
				return nil, fmt.Errorf("media source device id %q is provided by both %q and %q", item.DeviceID, owner, id)
			}
			routes[item.DeviceID] = id
			result = append(result, item)
		}
	}
	m.mu.Lock()
	m.mediaRoutes = routes
	m.mu.Unlock()
	sort.Slice(result, func(i, j int) bool { return result[i].DeviceID < result[j].DeviceID })
	return result, nil
}

// AcquireMediaAuthorization routes one short-lived Worker request to the
// Provider that owns the unified camera Device.
func (m *Manager) AcquireMediaAuthorization(ctx context.Context, request domainmedia.AuthorizationRequest) (*domainmedia.AuthorizationResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	providerID := m.mediaRoutes[request.DeviceID]
	if providerID == "" {
		// Compatibility fallback for Providers without MediaSource discovery.
		providerID = m.routes[request.DeviceID]
	}
	current := m.providers[providerID]
	running := current != nil && current.status == "running"
	m.mu.RUnlock()
	if providerID == "" || current == nil {
		return nil, providersdk.ErrDeviceNotFound
	}
	if !running {
		return nil, providersdk.ErrProviderUnavailable
	}
	authorizer, ok := current.provider.(providersdk.MediaAuthorizer)
	if !ok {
		return nil, providersdk.ErrCommandUnsupported
	}
	response, err := authorizer.AcquireMediaAuthorization(ctx, request)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("provider %q returned an empty media authorization", providerID)
	}
	if err := response.Validate(); err != nil {
		return nil, fmt.Errorf("provider %q returned invalid media authorization: %w", providerID, err)
	}
	return response, nil
}

// SourceCatalog exposes the same composed identities as DiscoverDevices while
// retaining Provider-native catalog metadata. Internal control identities are
// omitted, and their complete non-media catalogs are merged into the canonical
// Camera Provider device shown by Device Center and the mapping editor.
func (m *Manager) SourceCatalog(ctx context.Context) ([]providersdk.SourceCatalogDevice, error) {
	visible, err := m.DiscoverDevices(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	ids := append([]string(nil), m.order...)
	m.mu.RUnlock()
	native := make(map[string]providersdk.SourceCatalogDevice)
	for _, id := range ids {
		m.mu.RLock()
		current := m.providers[id]
		running := current != nil && current.status == "running"
		m.mu.RUnlock()
		if !running {
			continue
		}
		if cataloger, ok := current.provider.(providersdk.SourceCataloger); ok {
			items, err := cataloger.SourceCatalog(ctx)
			if err != nil {
				return nil, fmt.Errorf("source catalog for provider %q: %w", id, err)
			}
			for index := range items {
				items[index].ProviderID = id
				native[providerDeviceKey(id, items[index].ID)] = items[index]
			}
			continue
		}
		discoverer, ok := current.provider.(providersdk.Discoverer)
		if !ok {
			continue
		}
		items, err := discoverer.DiscoverDevices(ctx)
		if err != nil {
			return nil, fmt.Errorf("source catalog fallback for provider %q: %w", id, err)
		}
		for _, item := range items {
			item.ProviderID = id
			native[providerDeviceKey(id, item.ID)] = providersdk.SourceCatalogDevice{
				Device: item,
				Catalog: providersdk.SourceCatalogMetadata{
					Complete: true, Source: "provider-discovery", FetchedAt: time.Now().UTC(),
					Values: providersdk.SnapshotValueStatuses(item),
				},
			}
		}
	}
	m.mu.RLock()
	boundSources := make(map[string]string, len(m.boundSources))
	for sourceKey, canonicalDeviceID := range m.boundSources {
		boundSources[sourceKey] = canonicalDeviceID
	}
	m.mu.RUnlock()
	result := make([]providersdk.SourceCatalogDevice, 0, len(visible))
	for _, item := range visible {
		key := providerDeviceKey(item.ProviderID, item.ID)
		composed, exists := native[key]
		if !exists {
			composed = providersdk.SourceCatalogDevice{Catalog: providersdk.SourceCatalogMetadata{
				Complete: false, Source: "composed-discovery", Error: "canonical Provider source catalog is unavailable",
			}}
		}
		// Keep the native catalog as the source of truth. DiscoverDevices returns
		// the deliberately narrow public projection, so replacing the catalog
		// device with it would silently discard every unmapped native property.
		// The public projection can still carry composed control capabilities that
		// are not present in the canonical Provider catalog; merge those extras in.
		if exists {
			composed.Device = mergeSourceCatalogDevice(composed.Device, item)
		} else {
			composed.Device = item.Clone()
		}
		for sourceKey, canonicalDeviceID := range boundSources {
			if canonicalDeviceID != item.ID {
				continue
			}
			source, sourceExists := native[sourceKey]
			if !sourceExists {
				composed.Catalog.Complete = false
				composed.Catalog.Error = joinCatalogError(composed.Catalog.Error, "control Provider source catalog is unavailable")
				continue
			}
			composed.Catalog = mergeSourceCatalogMetadata(composed.Catalog, source.Catalog)
		}
		result = append(result, composed)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ProviderID != result[j].ProviderID {
			return result[i].ProviderID < result[j].ProviderID
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

// mergeSourceCatalogDevice preserves a Provider's complete native snapshot and
// adds structural capabilities from the composed public snapshot. The latter
// is needed for control-only sources (for example camera capabilities delegated
// from a Xiaomi hub), while the native snapshot must remain authoritative for
// source properties, values and definitions.
func mergeSourceCatalogDevice(native, composed device.Device) device.Device {
	merged := native.Clone()
	merged.SchemaVersion = composed.SchemaVersion
	merged.ID, merged.ProviderID = composed.ID, composed.ProviderID
	merged.Name, merged.Type = composed.Name, composed.Type
	merged.HomeID, merged.HomeName = composed.HomeID, composed.HomeName
	merged.RoomID, merged.RoomName = composed.RoomID, composed.RoomName
	merged.Availability, merged.Online = composed.Availability, composed.Online
	merged.Sequence, merged.Disabled, merged.Removed = composed.Sequence, composed.Disabled, composed.Removed
	merged.RuntimeMode, merged.StateTransport = composed.RuntimeMode, composed.StateTransport
	if composed.LastUpdateAt.After(merged.LastUpdateAt) {
		merged.LastUpdateAt = composed.LastUpdateAt
	}

	endpointIndexes := make(map[string]int, len(merged.Endpoints))
	for index, endpoint := range merged.Endpoints {
		endpointIndexes[endpoint.ID] = index
	}
	for _, composedEndpoint := range composed.Endpoints {
		endpointIndex, exists := endpointIndexes[composedEndpoint.ID]
		if !exists {
			merged.Endpoints = append(merged.Endpoints, composedEndpoint)
			endpointIndexes[composedEndpoint.ID] = len(merged.Endpoints) - 1
			continue
		}
		mergedEndpoint := &merged.Endpoints[endpointIndex]
		capabilityIndexes := make(map[string]int, len(mergedEndpoint.Capabilities))
		for index, capability := range mergedEndpoint.Capabilities {
			capabilityIndexes[capability.ID] = index
		}
		for _, composedCapability := range composedEndpoint.Capabilities {
			capabilityIndex, exists := capabilityIndexes[composedCapability.ID]
			if !exists {
				mergedEndpoint.Capabilities = append(mergedEndpoint.Capabilities, composedCapability)
				capabilityIndexes[composedCapability.ID] = len(mergedEndpoint.Capabilities) - 1
				continue
			}
			mergedCapability := &mergedEndpoint.Capabilities[capabilityIndex]
			propertyIDs := make(map[string]struct{}, len(mergedCapability.Properties))
			for _, property := range mergedCapability.Properties {
				propertyIDs[property.Definition.ID] = struct{}{}
			}
			for _, property := range composedCapability.Properties {
				if _, exists := propertyIDs[property.Definition.ID]; !exists {
					mergedCapability.Properties = append(mergedCapability.Properties, property)
					propertyIDs[property.Definition.ID] = struct{}{}
				}
			}
			commandIDs := make(map[string]struct{}, len(mergedCapability.Commands))
			for _, command := range mergedCapability.Commands {
				commandIDs[command.ID] = struct{}{}
			}
			for _, command := range composedCapability.Commands {
				if _, exists := commandIDs[command.ID]; !exists {
					mergedCapability.Commands = append(mergedCapability.Commands, command)
					commandIDs[command.ID] = struct{}{}
				}
			}
			eventIDs := make(map[string]struct{}, len(mergedCapability.Events))
			for _, event := range mergedCapability.Events {
				eventIDs[event.ID] = struct{}{}
			}
			for _, event := range composedCapability.Events {
				if _, exists := eventIDs[event.ID]; !exists {
					mergedCapability.Events = append(mergedCapability.Events, event)
					eventIDs[event.ID] = struct{}{}
				}
			}
		}
	}
	return merged
}

func mergeSourceCatalogMetadata(
	canonical, source providersdk.SourceCatalogMetadata,
) providersdk.SourceCatalogMetadata {
	canonical.Complete = canonical.Complete && source.Complete
	if source.Source != "" && source.Source != canonical.Source {
		if canonical.Source == "" {
			canonical.Source = source.Source
		} else {
			canonical.Source += "+" + source.Source
		}
	}
	if source.SpecType != "" {
		canonical.SpecType = source.SpecType
	}
	if source.Model != "" {
		canonical.Model = source.Model
	}
	if source.FetchedAt.After(canonical.FetchedAt) {
		canonical.FetchedAt = source.FetchedAt
	}
	canonical.Error = joinCatalogError(canonical.Error, source.Error)
	values := make(map[string]providersdk.SourceValueStatus, len(canonical.Values)+len(source.Values))
	for key, status := range canonical.Values {
		values[key] = status
	}
	for key, status := range source.Values {
		values[key] = status
	}
	canonical.Values = values
	return canonical
}

func joinCatalogError(current, addition string) string {
	current, addition = strings.TrimSpace(current), strings.TrimSpace(addition)
	if addition == "" || current == addition {
		return current
	}
	if current == "" {
		return addition
	}
	return current + "; " + addition
}

// Apply hot-reconfigures a compatible provider. If transport settings changed,
// it closes the old instance before opening the replacement so two connections
// with the same provider identity can never overlap.
func (m *Manager) Apply(ctx context.Context, item providersdk.Provider) error {
	id := item.Manifest().ID
	if id == "" {
		return fmt.Errorf("provider id is required")
	}
	m.mu.RLock()
	current := m.providers[id]
	m.mu.RUnlock()
	var previous []device.Device
	if current != nil {
		current.lifecycle.Lock()
		defer current.lifecycle.Unlock()
		m.mu.RLock()
		unchanged := m.providers[id] == current
		m.mu.RUnlock()
		if !unchanged {
			return fmt.Errorf("provider %q changed while applying configuration", id)
		}
		if source, ok := current.provider.(providersdk.Discoverer); ok {
			previous, _ = source.DiscoverDevices(ctx)
		}
		if reconfigurer, ok := current.provider.(providersdk.LiveReconfigurer); ok {
			handled, err := reconfigurer.Reconfigure(ctx, item)
			if err != nil {
				return fmt.Errorf("reconfigure provider %q: %w", id, err)
			}
			if handled {
				return m.reconcileReconfigured(ctx, id, current, previous)
			}
		}
		m.mu.Lock()
		current.status, current.err, current.nextRetryAt, current.transitionedAt = "reconfiguring", "", time.Time{}, time.Now().UTC()
		unsubscribe, unsubscribeEvents := current.unsubscribe, current.unsubscribeEvents
		current.unsubscribe, current.unsubscribeEvents = nil, nil
		m.mu.Unlock()
		if unsubscribe != nil {
			unsubscribe()
		}
		if unsubscribeEvents != nil {
			unsubscribeEvents()
		}
		if err := current.provider.Close(ctx); err != nil {
			m.mu.Lock()
			if m.providers[id] == current {
				m.markFailureLocked(current, fmt.Errorf("close provider for replacement: %w", err))
			}
			m.mu.Unlock()
			m.startRetryWorker()
			return fmt.Errorf("close provider %q before replacement: %w", id, err)
		}
	}
	m.mu.RLock()
	interests := append([]providersdk.PropertyInterest(nil), m.propertyInterests[id]...)
	m.mu.RUnlock()
	if setter, ok := item.(providersdk.PropertyInterestSetter); ok {
		setter.SetPropertyInterests(interests)
	}
	if err := item.Initialize(ctx); err != nil {
		return m.restoreAfterFailedReplacement(ctx, id, current, fmt.Errorf("initialize provider %q: %w", id, err))
	}
	created := &managedProvider{provider: item, status: "running", deviceIDs: make(map[string]struct{}), transitionedAt: time.Now().UTC()}
	var discovered []device.Device
	if source, ok := item.(providersdk.Discoverer); ok {
		items, err := source.DiscoverDevices(ctx)
		if err != nil {
			_ = item.Close(ctx)
			return m.restoreAfterFailedReplacement(ctx, id, current, fmt.Errorf("discover provider %q: %w", id, err))
		}
		for index := range items {
			items[index].ProviderID = id
			items[index].NormalizeAvailability()
			if err := items[index].ValidateStructure(); err != nil {
				_ = item.Close(ctx)
				return m.restoreAfterFailedReplacement(ctx, id, current, fmt.Errorf("provider %q returned invalid device snapshot: %w", id, err))
			}
		}
		discovered = items
	}
	m.mu.Lock()
	old := m.providers[id]
	for _, snapshot := range discovered {
		if owner, exists := m.routes[snapshot.ID]; exists && owner != id {
			m.mu.Unlock()
			_ = item.Close(ctx)
			return m.restoreAfterFailedReplacement(ctx, id, current, fmt.Errorf("device id %q is already owned by %q", snapshot.ID, owner))
		}
		created.deviceIDs[snapshot.ID] = struct{}{}
	}
	if old != nil {
		for deviceID := range old.deviceIDs {
			delete(m.routes, deviceID)
		}
	} else {
		m.order = append(m.order, id)
	}
	m.providers[id] = created
	for deviceID := range created.deviceIDs {
		m.routes[deviceID] = id
	}
	m.mu.Unlock()
	m.attach(id, created)
	for _, snapshot := range previous {
		if _, retained := created.deviceIDs[snapshot.ID]; retained {
			continue
		}
		snapshot.ProviderID = id
		snapshot.Removed = true
		snapshot.SetOnline(false)
		m.broadcastRefresh(snapshot)
	}
	return m.broadcastDiscovery(ctx)
}

func (m *Manager) restoreAfterFailedReplacement(ctx context.Context, id string, current *managedProvider, cause error) error {
	if current == nil {
		return cause
	}
	if err := current.provider.Initialize(ctx); err != nil {
		m.mu.Lock()
		if m.providers[id] == current {
			m.markFailureLocked(current, fmt.Errorf("restore previous provider: %w", err))
		}
		m.mu.Unlock()
		m.startRetryWorker()
		return fmt.Errorf("%w; restore previous provider: %v", cause, err)
	}
	m.mu.Lock()
	if m.providers[id] == current {
		current.status, current.err, current.nextRetryAt, current.transitionedAt = "running", "", time.Time{}, time.Now().UTC()
	}
	m.mu.Unlock()
	m.attach(id, current)
	return cause
}

func (m *Manager) reconcileReconfigured(ctx context.Context, id string, current *managedProvider, previous []device.Device) error {
	source, ok := current.provider.(providersdk.Discoverer)
	if !ok {
		return nil
	}
	items, err := source.DiscoverDevices(ctx)
	if err != nil {
		return fmt.Errorf("discover reconfigured provider %q: %w", id, err)
	}
	for index := range items {
		items[index].ProviderID = id
		items[index].NormalizeAvailability()
		if err := items[index].ValidateStructure(); err != nil {
			return fmt.Errorf("provider %q returned invalid device snapshot: %w", id, err)
		}
	}

	m.mu.Lock()
	if m.providers[id] != current {
		m.mu.Unlock()
		return fmt.Errorf("provider %q changed while applying live configuration", id)
	}
	for _, snapshot := range items {
		if owner, exists := m.routes[snapshot.ID]; exists && owner != id {
			m.mu.Unlock()
			return fmt.Errorf("device id %q is already owned by %q", snapshot.ID, owner)
		}
	}
	previousIDs := current.deviceIDs
	nextIDs := make(map[string]struct{}, len(items))
	current.deviceIDs = nextIDs
	for deviceID := range previousIDs {
		delete(m.routes, deviceID)
	}
	for _, snapshot := range items {
		nextIDs[snapshot.ID] = struct{}{}
		m.routes[snapshot.ID] = id
	}
	current.status, current.err, current.nextRetryAt, current.transitionedAt = "running", "", time.Time{}, time.Now().UTC()
	m.mu.Unlock()

	for _, snapshot := range previous {
		if _, retained := nextIDs[snapshot.ID]; retained {
			continue
		}
		snapshot.ProviderID = id
		snapshot.Removed = true
		snapshot.SetOnline(false)
		m.broadcastRefresh(snapshot)
	}
	return m.broadcastDiscovery(ctx)
}

// Remove stops a provider and emits offline snapshots for its known devices.
func (m *Manager) Remove(ctx context.Context, id string) error {
	m.mu.RLock()
	current, exists := m.providers[id]
	if !exists {
		m.mu.RUnlock()
		return fmt.Errorf("provider %q not found", id)
	}
	m.mu.RUnlock()
	current.lifecycle.Lock()
	defer current.lifecycle.Unlock()
	m.mu.Lock()
	if m.providers[id] != current {
		m.mu.Unlock()
		return fmt.Errorf("provider %q changed while removing it", id)
	}
	delete(m.providers, id)
	for i, value := range m.order {
		if value == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	for deviceID := range current.deviceIDs {
		delete(m.routes, deviceID)
	}
	m.mu.Unlock()
	if current.unsubscribe != nil {
		current.unsubscribe()
	}
	if current.unsubscribeEvents != nil {
		current.unsubscribeEvents()
	}
	if discoverer, ok := current.provider.(providersdk.Discoverer); ok {
		if items, err := discoverer.DiscoverDevices(ctx); err == nil {
			for _, item := range items {
				item.ProviderID = id
				item.Removed = true
				item.SetOnline(false)
				m.broadcastRefresh(item)
			}
		}
	}
	if err := current.provider.Close(ctx); err != nil {
		return fmt.Errorf("close provider %q: %w", id, err)
	}
	if err := m.broadcastDiscovery(ctx); err != nil {
		return fmt.Errorf("refresh devices after removing provider %q: %w", id, err)
	}
	return nil
}

func (m *Manager) attach(id string, current *managedProvider) {
	m.attachSnapshots(id, current)
	m.attachDeviceEvents(id, current)
}

func (m *Manager) attachSnapshots(id string, current *managedProvider) {
	subscriber, ok := current.provider.(providersdk.EventSubscriber)
	if !ok {
		return
	}
	unsubscribe := subscriber.Subscribe(func(item device.Device) {
		item.ProviderID = id
		item.NormalizeAvailability()
		if err := item.ValidateStructure(); err != nil {
			m.mu.Lock()
			if m.providers[id] == current {
				m.markFailureLocked(current, fmt.Errorf("invalid device event: %w", err))
			}
			m.mu.Unlock()
			m.startRetryWorker()
			return
		}
		m.mu.Lock()
		owner, exists := m.routes[item.ID]
		if !exists || owner == id {
			m.routes[item.ID] = id
			current.deviceIDs[item.ID] = struct{}{}
		}
		canonicalDeviceID, boundSource := m.boundSources[providerDeviceKey(id, item.ID)]
		_, hiddenSource := m.hiddenSources[providerDeviceKey(id, item.ID)]
		var projected device.Device
		projectedOK := false
		availabilityChanges := make([]providersdk.CapabilityAvailability, 0)
		logicalProjected, logicalSource := m.updateLogicalSourceSnapshotLocked(id, item)
		if boundSource {
			for route, delegate := range m.capabilityRoutes {
				if delegate.sourceProviderID == id && delegate.sourceDeviceID == item.ID {
					previous := delegate.available
					delegate.available = item.IsOnline()
					m.capabilityRoutes[route] = delegate
					if previous != delegate.available {
						availabilityChanges = append(availabilityChanges, providersdk.CapabilityAvailability{
							ProviderID: delegate.canonicalProviderID,
							DeviceID:   route.deviceID, EndpointID: route.endpointID, CapabilityID: route.capabilityID,
							Available: delegate.available,
						})
					}
				}
			}
			canonicalProviderID := m.routes[canonicalDeviceID]
			canonicalKey := providerDeviceKey(canonicalProviderID, canonicalDeviceID)
			if cached, exists := m.canonicalSnapshots[canonicalKey]; exists {
				projected = projectControlSnapshot(cached, item, m.capabilityRoutes)
				m.canonicalSnapshots[canonicalKey] = projected.Clone()
				projectedOK = true
			}
		}
		m.mu.Unlock()
		if exists && owner != id {
			return
		}
		if boundSource {
			if !item.IsOnline() && projectedOK {
				m.broadcastEvent(projected)
			}
			for _, availability := range availabilityChanges {
				m.broadcastCapabilityAvailability(availability)
			}
			if item.IsOnline() && projectedOK {
				m.broadcastEvent(projected)
			}
			for _, logical := range logicalProjected {
				m.broadcastEvent(logical)
			}
			return
		}
		if logicalSource {
			for _, logical := range logicalProjected {
				m.broadcastEvent(logical)
			}
			return
		}
		if hiddenSource {
			return
		}
		m.broadcastEvent(item)
	})
	m.mu.Lock()
	if m.providers[id] == current && current.unsubscribe == nil {
		current.unsubscribe = unsubscribe
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	unsubscribe()
}

func (m *Manager) attachDeviceEvents(id string, current *managedProvider) {
	subscriber, ok := current.provider.(providersdk.DeviceEventSubscriber)
	if !ok {
		return
	}
	unsubscribe := subscriber.SubscribeDeviceEvents(func(event providersdk.DeviceEvent) {
		event.ProviderID = id
		if event.DeviceID == "" || event.EndpointID == "" || event.CapabilityID == "" || event.EventID == "" || event.Sequence == 0 || event.ObservedAt.IsZero() {
			return
		}
		m.mu.RLock()
		owner, exists := m.routes[event.DeviceID]
		canonicalDeviceID, boundSource := m.boundSources[providerDeviceKey(id, event.DeviceID)]
		_, hiddenSource := m.hiddenSources[providerDeviceKey(id, event.DeviceID)]
		canonicalProviderID := m.routes[canonicalDeviceID]
		_, delegatedCapability := m.capabilityRoutes[capabilityRoute{canonicalDeviceID, event.EndpointID, event.CapabilityID}]
		m.mu.RUnlock()
		if boundSource {
			if !delegatedCapability || canonicalProviderID == "" {
				return
			}
			event.ProviderID = canonicalProviderID
			event.DeviceID = canonicalDeviceID
			m.broadcastDeviceEvent(event)
			return
		}
		if hiddenSource {
			return
		}
		if exists && owner == id {
			m.broadcastDeviceEvent(event)
		}
	})
	m.mu.Lock()
	if m.providers[id] == current && current.unsubscribeEvents == nil {
		current.unsubscribeEvents = unsubscribe
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	unsubscribe()
}

func (m *Manager) broadcast(item device.Device) {
	m.mu.RLock()
	handlers := make([]func(device.Device), 0, len(m.listeners))
	for _, h := range m.listeners {
		handlers = append(handlers, h)
	}
	m.mu.RUnlock()
	for _, h := range handlers {
		h(item.Clone())
	}
}

// broadcastEvent preserves the legacy aggregate subscription while also
// telling timestamp-aware consumers that this snapshot originated from a live
// Provider notification.
func (m *Manager) broadcastEvent(item device.Device) {
	m.broadcast(item)
	m.mu.RLock()
	handlers := make([]func(device.Device), 0, len(m.snapshotEventListeners))
	for _, handler := range m.snapshotEventListeners {
		handlers = append(handlers, handler)
	}
	m.mu.RUnlock()
	for _, handler := range handlers {
		handler(item.Clone())
	}
}

// broadcastRefresh preserves the legacy aggregate subscription while marking
// manager-driven catalog reconciliation as a refresh instead of a live event.
func (m *Manager) broadcastRefresh(item device.Device) {
	m.broadcast(item)
	m.mu.RLock()
	handlers := make([]func(device.Device), 0, len(m.snapshotRefreshListeners))
	for _, handler := range m.snapshotRefreshListeners {
		handlers = append(handlers, handler)
	}
	m.mu.RUnlock()
	for _, handler := range handlers {
		handler(item.Clone())
	}
}

func (m *Manager) broadcastDeviceEvent(event providersdk.DeviceEvent) {
	m.mu.RLock()
	handlers := make([]func(providersdk.DeviceEvent), 0, len(m.eventListeners))
	for _, handler := range m.eventListeners {
		handlers = append(handlers, handler)
	}
	m.mu.RUnlock()
	for _, handler := range handlers {
		copy := event
		copy.Payload = append([]byte(nil), event.Payload...)
		handler(copy)
	}
}

func (m *Manager) WriteProperty(ctx context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	m.mu.RLock()
	_, logical := m.logicalDevices[request.DeviceID]
	m.mu.RUnlock()
	if logical {
		return m.writeLogicalProperty(ctx, request)
	}
	m.mu.RLock()
	id, ok := m.routes[request.DeviceID]
	delegate, delegated := m.capabilityRoutes[capabilityRoute{request.DeviceID, request.EndpointID, request.CapabilityID}]
	if delegated {
		id = delegate.sourceProviderID
	}
	current := m.providers[id]
	running := current != nil && current.status == "running"
	m.mu.RUnlock()
	if !ok || current == nil {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	if !running || (delegated && !delegate.available) {
		return device.Device{}, providersdk.ErrProviderUnavailable
	}
	writer, ok := current.provider.(providersdk.PropertyWriter)
	if !ok {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	sourceRequest := request
	if delegated {
		sourceRequest.DeviceID = delegate.sourceDeviceID
		sourceRequest.EndpointID = delegate.sourceEndpointID
		sourceRequest.CapabilityID = delegate.sourceCapabilityID
	}
	item, err := writer.WriteProperty(ctx, sourceRequest)
	if err != nil {
		return device.Device{}, err
	}
	if delegated {
		return m.finalizeDelegatedSnapshot(ctx, request.DeviceID, delegate, item)
	}
	item.ProviderID = id
	item.NormalizeAvailability()
	if err := item.ValidateStructure(); err != nil {
		return device.Device{}, fmt.Errorf("provider %q returned invalid device snapshot: %w", id, err)
	}
	return item, nil
}

func (m *Manager) ReadProperty(ctx context.Context, request providersdk.PropertyReadRequest) (device.Property, error) {
	m.mu.RLock()
	_, logical := m.logicalDevices[request.DeviceID]
	m.mu.RUnlock()
	if logical {
		return m.readLogicalProperty(ctx, request)
	}
	m.mu.RLock()
	id, ok := m.routes[request.DeviceID]
	delegate, delegated := m.capabilityRoutes[capabilityRoute{request.DeviceID, request.EndpointID, request.CapabilityID}]
	if delegated {
		id = delegate.sourceProviderID
	}
	current := m.providers[id]
	running := current != nil && current.status == "running"
	m.mu.RUnlock()
	if !ok || current == nil {
		return device.Property{}, providersdk.ErrDeviceNotFound
	}
	if !running || (delegated && !delegate.available) {
		return device.Property{}, providersdk.ErrProviderUnavailable
	}
	reader, ok := current.provider.(providersdk.PropertyReader)
	if !ok {
		return device.Property{}, providersdk.ErrPropertyUnsupported
	}
	if delegated {
		request.DeviceID = delegate.sourceDeviceID
		request.EndpointID = delegate.sourceEndpointID
		request.CapabilityID = delegate.sourceCapabilityID
	}
	return reader.ReadProperty(ctx, request)
}

func (m *Manager) ExecuteCommand(ctx context.Context, request providersdk.CommandRequest) (device.Device, error) {
	m.mu.RLock()
	_, logical := m.logicalDevices[request.DeviceID]
	m.mu.RUnlock()
	if logical {
		return m.executeLogicalCommand(ctx, request)
	}
	m.mu.RLock()
	id, ok := m.routes[request.DeviceID]
	delegate, delegated := m.capabilityRoutes[capabilityRoute{request.DeviceID, request.EndpointID, request.CapabilityID}]
	if delegated {
		id = delegate.sourceProviderID
	}
	current := m.providers[id]
	running := current != nil && current.status == "running"
	m.mu.RUnlock()
	if !ok || current == nil {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	if !running || (delegated && !delegate.available) {
		return device.Device{}, providersdk.ErrProviderUnavailable
	}
	executor, ok := current.provider.(providersdk.CommandExecutor)
	if !ok {
		return device.Device{}, providersdk.ErrCommandUnsupported
	}
	sourceRequest := request
	if delegated {
		sourceRequest.DeviceID = delegate.sourceDeviceID
		sourceRequest.EndpointID = delegate.sourceEndpointID
		sourceRequest.CapabilityID = delegate.sourceCapabilityID
	}
	item, err := executor.ExecuteCommand(ctx, sourceRequest)
	if err != nil {
		return device.Device{}, err
	}
	if delegated {
		return m.finalizeDelegatedSnapshot(ctx, request.DeviceID, delegate, item)
	}
	item.ProviderID = id
	item.NormalizeAvailability()
	if err := item.ValidateStructure(); err != nil {
		return device.Device{}, fmt.Errorf("provider %q returned invalid device snapshot: %w", id, err)
	}
	return item, nil
}

func providerDeviceKey(providerID, deviceID string) string {
	return providerID + "\x00" + deviceID
}

func mergeControlCapabilities(
	canonical, source device.Device,
	canonicalProviderID, sourceProviderID string,
) (device.Device, map[capabilityRoute]capabilityDelegate, error) {
	if canonical.Type != device.TypeCamera {
		return device.Device{}, nil, fmt.Errorf("canonical device is not a camera")
	}
	if source.Type != device.TypeCamera {
		return device.Device{}, nil, fmt.Errorf("control source device %q is not a camera", source.ID)
	}
	merged := canonical.Clone()
	delegates := make(map[capabilityRoute]capabilityDelegate)
	endpointIndexes := make(map[string]int, len(merged.Endpoints))
	for index, endpoint := range merged.Endpoints {
		endpointIndexes[endpoint.ID] = index
	}
	for _, sourceEndpoint := range source.Endpoints {
		targetIndex, endpointExists := endpointIndexes[sourceEndpoint.ID]
		if !endpointExists {
			merged.Endpoints = append(merged.Endpoints, device.Endpoint{
				ID: sourceEndpoint.ID, Name: sourceEndpoint.Name, Type: sourceEndpoint.Type,
			})
			targetIndex = len(merged.Endpoints) - 1
			endpointIndexes[sourceEndpoint.ID] = targetIndex
		}
		target := &merged.Endpoints[targetIndex]
		existingCapabilities := make(map[string]struct{}, len(target.Capabilities))
		for _, capability := range target.Capabilities {
			existingCapabilities[capability.ID] = struct{}{}
		}
		for _, capability := range sourceEndpoint.Capabilities {
			if capability.ID == "media" || capability.Type == "media" {
				continue
			}
			if _, duplicate := existingCapabilities[capability.ID]; duplicate {
				return device.Device{}, nil, fmt.Errorf(
					"endpoint %q capability %q conflicts with the canonical camera",
					sourceEndpoint.ID, capability.ID,
				)
			}
			copiedEndpoint := device.Endpoint{Capabilities: []device.Capability{capability}}
			copiedDevice := device.Device{Endpoints: []device.Endpoint{copiedEndpoint}}.Clone()
			target.Capabilities = append(target.Capabilities, copiedDevice.Endpoints[0].Capabilities[0])
			existingCapabilities[capability.ID] = struct{}{}
			delegates[capabilityRoute{canonical.ID, sourceEndpoint.ID, capability.ID}] = capabilityDelegate{
				canonicalProviderID: canonicalProviderID,
				sourceProviderID:    sourceProviderID, sourceDeviceID: source.ID,
				sourceEndpointID: sourceEndpoint.ID, sourceCapabilityID: capability.ID,
				available: source.IsOnline(),
			}
		}
	}
	return merged, delegates, nil
}

func projectControlSnapshot(
	canonical, source device.Device,
	routes map[capabilityRoute]capabilityDelegate,
) device.Device {
	projected := canonical.Clone()
	for _, sourceEndpoint := range source.Endpoints {
		for _, sourceCapability := range sourceEndpoint.Capabilities {
			route := capabilityRoute{canonical.ID, sourceEndpoint.ID, sourceCapability.ID}
			delegate, delegated := routes[route]
			if !delegated || delegate.sourceDeviceID != source.ID {
				continue
			}
			for endpointIndex := range projected.Endpoints {
				if projected.Endpoints[endpointIndex].ID != sourceEndpoint.ID {
					continue
				}
				for capabilityIndex := range projected.Endpoints[endpointIndex].Capabilities {
					if projected.Endpoints[endpointIndex].Capabilities[capabilityIndex].ID != sourceCapability.ID {
						continue
					}
					copyHolder := device.Device{Endpoints: []device.Endpoint{{Capabilities: []device.Capability{sourceCapability}}}}.Clone()
					projected.Endpoints[endpointIndex].Capabilities[capabilityIndex] = copyHolder.Endpoints[0].Capabilities[0]
				}
			}
		}
	}
	if source.LastUpdateAt.After(projected.LastUpdateAt) {
		projected.LastUpdateAt = source.LastUpdateAt
	}
	if source.Sequence >= projected.Sequence {
		projected.Sequence = source.Sequence + 1
	} else {
		projected.Sequence++
	}
	// Availability belongs to the Camera/media identity, never to its control
	// transport. Keep the canonical projection exactly as it was.
	projected.NormalizeAvailability()
	return projected
}

func (m *Manager) finalizeDelegatedSnapshot(
	ctx context.Context,
	canonicalDeviceID string,
	delegate capabilityDelegate,
	source device.Device,
) (device.Device, error) {
	source.ProviderID = delegate.sourceProviderID
	source.NormalizeAvailability()
	if err := source.ValidateStructure(); err != nil {
		return device.Device{}, fmt.Errorf("provider %q returned invalid device snapshot: %w", delegate.sourceProviderID, err)
	}
	m.mu.RLock()
	current := m.providers[delegate.canonicalProviderID]
	running := current != nil && current.status == "running"
	m.mu.RUnlock()
	if !running {
		return device.Device{}, providersdk.ErrProviderUnavailable
	}
	discoverer, ok := current.provider.(providersdk.Discoverer)
	if !ok {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	items, err := discoverer.DiscoverDevices(ctx)
	if err != nil {
		return device.Device{}, err
	}
	for _, canonical := range items {
		if canonical.ID != canonicalDeviceID {
			continue
		}
		canonical.ProviderID = delegate.canonicalProviderID
		merged, _, err := mergeControlCapabilities(canonical, source, delegate.canonicalProviderID, delegate.sourceProviderID)
		if err != nil {
			return device.Device{}, err
		}
		merged.NormalizeAvailability()
		if err := merged.ValidateStructure(); err != nil {
			return device.Device{}, fmt.Errorf("invalid merged camera snapshot: %w", err)
		}
		return merged, nil
	}
	return device.Device{}, providersdk.ErrDeviceNotFound
}

func (m *Manager) Simulate(ctx context.Context, request providersdk.SimulationRequest) (device.Device, error) {
	m.mu.RLock()
	id, ok := m.routes[request.DeviceID]
	current := m.providers[id]
	m.mu.RUnlock()
	if !ok || current == nil {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	simulator, ok := current.provider.(providersdk.Simulator)
	if !ok {
		return device.Device{}, providersdk.ErrSimulationInvalid
	}
	item, err := simulator.Simulate(ctx, request)
	if err != nil {
		return device.Device{}, err
	}
	item.ProviderID = id
	item.NormalizeAvailability()
	if err := item.ValidateStructure(); err != nil {
		return device.Device{}, fmt.Errorf("provider %q returned invalid device snapshot: %w", id, err)
	}
	return item, nil
}

func (m *Manager) Subscribe(handler func(device.Device)) func() {
	m.mu.Lock()
	m.nextListener++
	id := m.nextListener
	m.listeners[id] = handler
	m.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { m.mu.Lock(); delete(m.listeners, id); m.mu.Unlock() }) }
}

// SubscribeSnapshotEvents receives only live snapshot notifications emitted by
// underlying Providers. It deliberately excludes manager catalog refreshes.
func (m *Manager) SubscribeSnapshotEvents(handler func(device.Device)) func() {
	if handler == nil {
		return func() {}
	}
	m.mu.Lock()
	m.nextSnapshotEventListener++
	id := m.nextSnapshotEventListener
	m.snapshotEventListeners[id] = handler
	m.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { m.mu.Lock(); delete(m.snapshotEventListeners, id); m.mu.Unlock() }) }
}

// SubscribeSnapshotRefreshes receives snapshots broadcast while the manager
// reconciles its Provider catalog after apply, remove, or retry operations.
func (m *Manager) SubscribeSnapshotRefreshes(handler func(device.Device)) func() {
	if handler == nil {
		return func() {}
	}
	m.mu.Lock()
	m.nextSnapshotRefreshListener++
	id := m.nextSnapshotRefreshListener
	m.snapshotRefreshListeners[id] = handler
	m.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { m.mu.Lock(); delete(m.snapshotRefreshListeners, id); m.mu.Unlock() }) }
}

func (m *Manager) SubscribeDeviceEvents(handler func(providersdk.DeviceEvent)) func() {
	m.mu.Lock()
	m.nextEventListener++
	id := m.nextEventListener
	m.eventListeners[id] = handler
	m.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { m.mu.Lock(); delete(m.eventListeners, id); m.mu.Unlock() }) }
}

func (m *Manager) CapabilityAvailabilities() []providersdk.CapabilityAvailability {
	m.mu.RLock()
	result := make([]providersdk.CapabilityAvailability, 0, len(m.capabilityRoutes))
	for route, delegate := range m.capabilityRoutes {
		result = append(result, providersdk.CapabilityAvailability{
			ProviderID: delegate.canonicalProviderID,
			DeviceID:   route.deviceID, EndpointID: route.endpointID, CapabilityID: route.capabilityID,
			Available: delegate.available,
		})
	}
	m.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].DeviceID != result[j].DeviceID {
			return result[i].DeviceID < result[j].DeviceID
		}
		if result[i].EndpointID != result[j].EndpointID {
			return result[i].EndpointID < result[j].EndpointID
		}
		return result[i].CapabilityID < result[j].CapabilityID
	})
	return result
}

func (m *Manager) SubscribeCapabilityAvailability(handler func(providersdk.CapabilityAvailability)) func() {
	m.mu.Lock()
	m.nextCapabilityAvailabilityListener++
	id := m.nextCapabilityAvailabilityListener
	m.capabilityAvailabilityListeners[id] = handler
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.capabilityAvailabilityListeners, id)
			m.mu.Unlock()
		})
	}
}

func (m *Manager) broadcastCapabilityAvailability(event providersdk.CapabilityAvailability) {
	m.mu.RLock()
	handlers := make([]func(providersdk.CapabilityAvailability), 0, len(m.capabilityAvailabilityListeners))
	for _, handler := range m.capabilityAvailabilityListeners {
		handlers = append(handlers, handler)
	}
	m.mu.RUnlock()
	for _, handler := range handlers {
		handler(event)
	}
}

func (m *Manager) markFailureLocked(current *managedProvider, err error) {
	if isAuthenticationRequired(err) {
		current.status, current.err, current.nextRetryAt, current.transitionedAt = "auth_required", err.Error(), time.Time{}, time.Now().UTC()
		return
	}
	current.status, current.err, current.transitionedAt = "error", err.Error(), time.Now().UTC()
	delay := time.Second << min(current.retryCount, 5)
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	current.nextRetryAt = time.Now().UTC().Add(delay)
}

type authenticationRequiredError interface {
	AuthenticationRequired() bool
}

func isAuthenticationRequired(err error) bool {
	var marker authenticationRequiredError
	return errors.As(err, &marker) && marker.AuthenticationRequired()
}

func (m *Manager) startRetryWorker() {
	m.mu.Lock()
	if m.closed || m.retryRunning || m.lifecycleCtx == nil {
		m.mu.Unlock()
		return
	}
	hasFailure := false
	for _, current := range m.providers {
		if current.status == "error" {
			hasFailure = true
			break
		}
	}
	if !hasFailure {
		m.mu.Unlock()
		return
	}
	m.retryRunning, m.retryDone = true, make(chan struct{})
	ctx, done := m.lifecycleCtx, m.retryDone
	m.mu.Unlock()
	go m.retryLoop(ctx, done)
}

func (m *Manager) retryLoop(ctx context.Context, done chan struct{}) {
	defer func() {
		m.mu.Lock()
		m.retryRunning = false
		retryAgain := false
		if !m.closed {
			for _, current := range m.providers {
				if current.status == "error" {
					retryAgain = true
					break
				}
			}
		}
		m.mu.Unlock()
		close(done)
		if retryAgain {
			m.startRetryWorker()
		}
	}()
	for {
		m.mu.RLock()
		var selectedID string
		var selected *managedProvider
		var retryAt time.Time
		for _, id := range m.order {
			current := m.providers[id]
			if current.status != "error" {
				continue
			}
			if selected == nil || current.nextRetryAt.Before(retryAt) {
				selectedID, selected, retryAt = id, current, current.nextRetryAt
			}
		}
		m.mu.RUnlock()
		if selected == nil {
			return
		}
		delay := time.Until(retryAt)
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		m.retryProvider(ctx, selectedID, selected)
	}
}

func (m *Manager) retryProvider(ctx context.Context, id string, current *managedProvider) {
	current.lifecycle.Lock()
	defer current.lifecycle.Unlock()
	m.mu.Lock()
	if m.providers[id] != current || current.status != "error" {
		m.mu.Unlock()
		return
	}
	current.status, current.transitionedAt = "retrying", time.Now().UTC()
	current.retryCount++
	m.mu.Unlock()
	// An operational failure can mark a Provider as errored while its transport
	// is still allocated. Always tear that lifecycle down before Initialize;
	// otherwise strict Providers (notably Xiaomi's single MQTT session) reject
	// the retry as a duplicate initialization.
	if err := current.provider.Close(ctx); err != nil {
		m.mu.Lock()
		if m.providers[id] == current {
			m.markFailureLocked(current, fmt.Errorf("close provider before retry: %w", err))
		}
		m.mu.Unlock()
		return
	}
	if err := current.provider.Initialize(ctx); err != nil {
		m.mu.Lock()
		if m.providers[id] == current {
			m.markFailureLocked(current, err)
		}
		m.mu.Unlock()
		return
	}
	items := make([]device.Device, 0)
	if discoverer, ok := current.provider.(providersdk.Discoverer); ok {
		discovered, err := discoverer.DiscoverDevices(ctx)
		if err != nil {
			m.mu.Lock()
			if m.providers[id] == current {
				m.markFailureLocked(current, err)
			}
			m.mu.Unlock()
			return
		}
		items = discovered
	}
	m.mu.Lock()
	if m.providers[id] != current {
		m.mu.Unlock()
		return
	}
	for _, item := range items {
		if owner, exists := m.routes[item.ID]; exists && owner != id {
			m.markFailureLocked(current, fmt.Errorf("device id %q is already owned by %q", item.ID, owner))
			m.mu.Unlock()
			return
		}
	}
	for deviceID := range current.deviceIDs {
		delete(m.routes, deviceID)
	}
	current.deviceIDs = make(map[string]struct{}, len(items))
	for _, item := range items {
		current.deviceIDs[item.ID] = struct{}{}
		m.routes[item.ID] = id
	}
	current.status, current.err, current.nextRetryAt, current.transitionedAt = "running", "", time.Time{}, time.Now().UTC()
	m.mu.Unlock()
	m.attach(id, current)
	_ = m.broadcastDiscovery(ctx)
}

func (m *Manager) broadcastDiscovery(ctx context.Context) error {
	items, err := m.DiscoverDevices(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		m.broadcastRefresh(item)
	}
	return nil
}

func (m *Manager) ProviderInfos() []providersdk.RuntimeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]providersdk.RuntimeInfo, 0, len(m.order))
	for _, id := range m.order {
		current := m.providers[id]
		var nextRetryAt *time.Time
		if !current.nextRetryAt.IsZero() {
			value := current.nextRetryAt
			nextRetryAt = &value
		}
		info := providersdk.RuntimeInfo{Manifest: current.provider.Manifest(), Capabilities: current.provider.Capabilities(), Status: current.status, Error: current.err, RetryCount: current.retryCount, NextRetryAt: nextRetryAt, TransitionedAt: current.transitionedAt}
		if reporter, ok := current.provider.(providersdk.MetricsReporter); ok {
			info.Metrics = reporter.ProviderMetrics()
		}
		if reporter, ok := current.provider.(providersdk.DiagnosticsReporter); ok {
			info.Diagnostics = reporter.ProviderDiagnostics()
		}
		result = append(result, info)
	}
	return result
}

func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	if m.lifecycleCancel != nil {
		m.lifecycleCancel()
	}
	retryDone := m.retryDone
	ids := append([]string(nil), m.order...)
	m.mu.Unlock()
	if retryDone != nil {
		select {
		case <-retryDone:
		case <-ctx.Done():
		}
	}
	var first error
	for i := len(ids) - 1; i >= 0; i-- {
		m.mu.RLock()
		current := m.providers[ids[i]]
		m.mu.RUnlock()
		if current == nil {
			continue
		}
		current.lifecycle.Lock()
		if current.unsubscribe != nil {
			current.unsubscribe()
		}
		if current.unsubscribeEvents != nil {
			current.unsubscribeEvents()
		}
		if err := current.provider.Close(ctx); err != nil && first == nil {
			first = fmt.Errorf("close provider %q: %w", ids[i], err)
		}
		m.mu.Lock()
		current.status, current.transitionedAt = "stopped", time.Now().UTC()
		current.unsubscribe, current.unsubscribeEvents = nil, nil
		m.mu.Unlock()
		current.lifecycle.Unlock()
	}
	return first
}

var _ providersdk.Provider = (*Manager)(nil)
var _ providersdk.Discoverer = (*Manager)(nil)
var _ providersdk.SourceCataloger = (*Manager)(nil)
var _ providersdk.PropertyReader = (*Manager)(nil)
var _ providersdk.PropertyWriter = (*Manager)(nil)
var _ providersdk.CommandExecutor = (*Manager)(nil)
var _ providersdk.EventSubscriber = (*Manager)(nil)
var _ providersdk.CapabilityAvailabilityReporter = (*Manager)(nil)
var _ providersdk.CapabilityAvailabilitySubscriber = (*Manager)(nil)
var _ providersdk.Inspector = (*Manager)(nil)
var _ providersdk.Simulator = (*Manager)(nil)
