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
	provider       providersdk.Provider
	status         string
	err            string
	unsubscribe    func()
	deviceIDs      map[string]struct{}
	retryCount     int
	nextRetryAt    time.Time
	transitionedAt time.Time
}

type Manager struct {
	mu              sync.RWMutex
	providers       map[string]*managedProvider
	order           []string
	routes          map[string]string
	listeners       map[uint64]func(device.Device)
	nextListener    uint64
	initialized     bool
	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc
	retryRunning    bool
	retryDone       chan struct{}
	closed          bool
}

func New(items ...providersdk.Provider) (*Manager, error) {
	m := &Manager{providers: make(map[string]*managedProvider), routes: make(map[string]string), listeners: make(map[uint64]func(device.Device))}
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

func (m *Manager) Initialize(ctx context.Context) error {
	m.mu.Lock()
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
		if err := current.provider.Initialize(ctx); err != nil {
			m.mu.Lock()
			m.markFailureLocked(current, err)
			m.mu.Unlock()
			continue
		}
		m.mu.Lock()
		current.status, current.err, current.transitionedAt = "running", "", time.Now().UTC()
		m.mu.Unlock()
		m.attach(id, current)
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
			if err := item.NormalizeModelParameters(); err != nil {
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

// Apply starts a provider and atomically replaces an instance with the same id.
func (m *Manager) Apply(ctx context.Context, item providersdk.Provider) error {
	id := item.Manifest().ID
	if id == "" {
		return fmt.Errorf("provider id is required")
	}
	m.mu.RLock()
	current := m.providers[id]
	running := current != nil && current.status == "running"
	m.mu.RUnlock()
	if running {
		if reconfigurer, ok := current.provider.(providersdk.LiveReconfigurer); ok {
			var previous []device.Device
			if source, discoverable := current.provider.(providersdk.Discoverer); discoverable {
				previous, _ = source.DiscoverDevices(ctx)
			}
			handled, err := reconfigurer.Reconfigure(ctx, item)
			if err != nil {
				return fmt.Errorf("reconfigure provider %q: %w", id, err)
			}
			if handled {
				return m.reconcileReconfigured(ctx, id, current, previous)
			}
		}
	}
	if err := item.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize provider %q: %w", id, err)
	}
	created := &managedProvider{provider: item, status: "running", deviceIDs: make(map[string]struct{}), transitionedAt: time.Now().UTC()}
	var discovered []device.Device
	if source, ok := item.(providersdk.Discoverer); ok {
		items, err := source.DiscoverDevices(ctx)
		if err != nil {
			_ = item.Close(ctx)
			return fmt.Errorf("discover provider %q: %w", id, err)
		}
		for index := range items {
			items[index].ProviderID = id
			items[index].NormalizeAvailability()
			if err := items[index].NormalizeModelParameters(); err != nil {
				_ = item.Close(ctx)
				return fmt.Errorf("provider %q returned invalid device snapshot: %w", id, err)
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
			return fmt.Errorf("device id %q is already owned by %q", snapshot.ID, owner)
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
	if old != nil {
		if source, ok := old.provider.(providersdk.Discoverer); ok {
			if previous, err := source.DiscoverDevices(ctx); err == nil {
				for _, snapshot := range previous {
					if _, retained := created.deviceIDs[snapshot.ID]; retained {
						continue
					}
					snapshot.ProviderID = id
					snapshot.Removed = true
					snapshot.SetOnline(false)
					m.broadcast(snapshot)
				}
			}
		}
		if old.unsubscribe != nil {
			old.unsubscribe()
		}
		_ = old.provider.Close(ctx)
	}
	for _, snapshot := range discovered {
		snapshot.ProviderID = id
		m.broadcast(snapshot)
	}
	return nil
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
		if err := items[index].NormalizeModelParameters(); err != nil {
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
	current.err, current.nextRetryAt, current.transitionedAt = "", time.Time{}, time.Now().UTC()
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
	m.mu.Lock()
	current, exists := m.providers[id]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("provider %q not found", id)
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
	subscriber, ok := current.provider.(providersdk.EventSubscriber)
	if !ok {
		return
	}
	unsubscribe := subscriber.Subscribe(func(item device.Device) {
		item.ProviderID = id
		item.NormalizeAvailability()
		if err := item.NormalizeModelParameters(); err != nil {
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
	if err := item.NormalizeModelParameters(); err != nil {
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
	if err := item.NormalizeModelParameters(); err != nil {
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
	if err := item.NormalizeModelParameters(); err != nil {
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
	m.mu.Lock()
	if m.providers[id] != current || current.status != "error" {
		m.mu.Unlock()
		return
	}
	current.status, current.transitionedAt = "retrying", time.Now().UTC()
	current.retryCount++
	m.mu.Unlock()
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
		if current.unsubscribe != nil {
			current.unsubscribe()
		}
		if err := current.provider.Close(ctx); err != nil && first == nil {
			first = fmt.Errorf("close provider %q: %w", ids[i], err)
		}
		m.mu.Lock()
		current.status, current.transitionedAt = "stopped", time.Now().UTC()
		current.unsubscribe = nil
		m.mu.Unlock()
	}
	return first
}

var _ providersdk.Provider = (*Manager)(nil)
var _ providersdk.Discoverer = (*Manager)(nil)
var _ providersdk.PropertyReader = (*Manager)(nil)
var _ providersdk.PropertyWriter = (*Manager)(nil)
var _ providersdk.CommandExecutor = (*Manager)(nil)
var _ providersdk.EventSubscriber = (*Manager)(nil)
var _ providersdk.Inspector = (*Manager)(nil)
var _ providersdk.Simulator = (*Manager)(nil)
