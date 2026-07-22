package providermanager

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
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

type Manager struct {
	mu                sync.RWMutex
	providers         map[string]*managedProvider
	order             []string
	routes            map[string]string
	listeners         map[uint64]func(device.Device)
	eventListeners    map[uint64]func(providersdk.DeviceEvent)
	propertyInterests map[string][]providersdk.PropertyInterest
	nextListener      uint64
	nextEventListener uint64
	initialized       bool
	lifecycleCtx      context.Context
	lifecycleCancel   context.CancelFunc
	retryRunning      bool
	retryDone         chan struct{}
	closed            bool
}

func New(items ...providersdk.Provider) (*Manager, error) {
	m := &Manager{providers: make(map[string]*managedProvider), routes: make(map[string]string), listeners: make(map[uint64]func(device.Device)), eventListeners: make(map[uint64]func(providersdk.DeviceEvent)), propertyInterests: make(map[string][]providersdk.PropertyInterest)}
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
		}
		m.mu.Lock()
		if live := m.providers[id]; live == current {
			live.deviceIDs = currentIDs
		}
		m.mu.Unlock()
	}
	m.mu.Lock()
	m.routes = routes
	m.mu.Unlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// SourceCatalog aggregates native catalogs from running providers. Providers
// without a specialized catalog contract fall back to their discovery
// snapshot, which is complete relative to that Provider's published contract.
func (m *Manager) SourceCatalog(ctx context.Context) ([]providersdk.SourceCatalogDevice, error) {
	m.mu.RLock()
	ids := append([]string(nil), m.order...)
	m.mu.RUnlock()
	result := make([]providersdk.SourceCatalogDevice, 0)
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
			}
			result = append(result, items...)
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
			result = append(result, providersdk.SourceCatalogDevice{Device: item, Catalog: providersdk.SourceCatalogMetadata{Complete: true, Source: "provider-discovery", FetchedAt: time.Now().UTC(), Values: providersdk.SnapshotValueStatuses(item)}})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ProviderID != result[j].ProviderID {
			return result[i].ProviderID < result[j].ProviderID
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
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
		m.broadcast(snapshot)
	}
	for _, snapshot := range discovered {
		snapshot.ProviderID = id
		m.broadcast(snapshot)
	}
	return nil
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
		m.broadcast(snapshot)
	}
	for _, snapshot := range items {
		m.broadcast(snapshot)
	}
	return nil
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
				m.broadcast(item)
			}
		}
	}
	if err := current.provider.Close(ctx); err != nil {
		return fmt.Errorf("close provider %q: %w", id, err)
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
		m.mu.Unlock()
		if exists && owner != id {
			return
		}
		m.broadcast(item)
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
		m.mu.RUnlock()
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
	id, ok := m.routes[request.DeviceID]
	current := m.providers[id]
	m.mu.RUnlock()
	if !ok || current == nil {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	writer, ok := current.provider.(providersdk.PropertyWriter)
	if !ok {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	item, err := writer.WriteProperty(ctx, request)
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

func (m *Manager) ReadProperty(ctx context.Context, request providersdk.PropertyReadRequest) (device.Property, error) {
	m.mu.RLock()
	id, ok := m.routes[request.DeviceID]
	current := m.providers[id]
	m.mu.RUnlock()
	if !ok || current == nil {
		return device.Property{}, providersdk.ErrDeviceNotFound
	}
	reader, ok := current.provider.(providersdk.PropertyReader)
	if !ok {
		return device.Property{}, providersdk.ErrPropertyUnsupported
	}
	return reader.ReadProperty(ctx, request)
}

func (m *Manager) ExecuteCommand(ctx context.Context, request providersdk.CommandRequest) (device.Device, error) {
	m.mu.RLock()
	id, ok := m.routes[request.DeviceID]
	current := m.providers[id]
	m.mu.RUnlock()
	if !ok || current == nil {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	executor, ok := current.provider.(providersdk.CommandExecutor)
	if !ok {
		return device.Device{}, providersdk.ErrCommandUnsupported
	}
	item, err := executor.ExecuteCommand(ctx, request)
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

func (m *Manager) SubscribeDeviceEvents(handler func(providersdk.DeviceEvent)) func() {
	m.mu.Lock()
	m.nextEventListener++
	id := m.nextEventListener
	m.eventListeners[id] = handler
	m.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { m.mu.Lock(); delete(m.eventListeners, id); m.mu.Unlock() }) }
}

func (m *Manager) markFailureLocked(current *managedProvider, err error) {
	current.status, current.err, current.transitionedAt = "error", err.Error(), time.Now().UTC()
	delay := time.Second << min(current.retryCount, 5)
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	current.nextRetryAt = time.Now().UTC().Add(delay)
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
	for _, item := range items {
		item.ProviderID = id
		m.broadcast(item)
	}
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
var _ providersdk.Inspector = (*Manager)(nil)
var _ providersdk.Simulator = (*Manager)(nil)
