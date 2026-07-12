package providermanager

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type managedProvider struct {
	provider    providersdk.Provider
	status      string
	err         string
	unsubscribe func()
	deviceIDs   map[string]struct{}
}

type Manager struct {
	mu           sync.RWMutex
	providers    map[string]*managedProvider
	order        []string
	routes       map[string]string
	listeners    map[uint64]func(device.Device)
	nextListener uint64
	initialized  bool
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
		m.providers[id] = &managedProvider{provider: item, status: "created", deviceIDs: make(map[string]struct{})}
		m.order = append(m.order, id)
	}
	return m, nil
}

func (m *Manager) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: "provider-manager", Type: "manager", Name: "Provider Manager", Version: "0.2.0"}
}
func (m *Manager) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyRead: true, PropertyWrite: true, Events: true}
}

func (m *Manager) Initialize(ctx context.Context) error {
	m.mu.RLock()
	ids := append([]string(nil), m.order...)
	m.mu.RUnlock()
	for _, id := range ids {
		m.mu.RLock()
		current := m.providers[id]
		m.mu.RUnlock()
		if err := current.provider.Initialize(ctx); err != nil {
			m.mu.Lock()
			current.status, current.err = "error", err.Error()
			m.mu.Unlock()
			return fmt.Errorf("initialize provider %q: %w", id, err)
		}
		m.mu.Lock()
		current.status, current.err = "running", ""
		m.mu.Unlock()
		m.attach(id, current)
	}
	m.mu.Lock()
	m.initialized = true
	m.mu.Unlock()
	return nil
}

func (m *Manager) DiscoverDevices(ctx context.Context) ([]device.Device, error) {
	m.mu.RLock()
	ids := append([]string(nil), m.order...)
	m.mu.RUnlock()
	result := make([]device.Device, 0)
	routes := make(map[string]string)
	for _, id := range ids {
		m.mu.RLock()
		current := m.providers[id]
		m.mu.RUnlock()
		discoverer, ok := current.provider.(providersdk.Discoverer)
		if !ok {
			continue
		}
		items, err := discoverer.DiscoverDevices(ctx)
		if err != nil {
			return nil, fmt.Errorf("discover provider %q: %w", id, err)
		}
		currentIDs := make(map[string]struct{}, len(items))
		for _, item := range items {
			if owner, exists := routes[item.ID]; exists {
				return nil, fmt.Errorf("device id %q is provided by both %q and %q", item.ID, owner, id)
			}
			item.ProviderID = id
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
	if err := item.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize provider %q: %w", id, err)
	}
	created := &managedProvider{provider: item, status: "running", deviceIDs: make(map[string]struct{})}
	var discovered []device.Device
	if source, ok := item.(providersdk.Discoverer); ok {
		items, err := source.DiscoverDevices(ctx)
		if err != nil {
			_ = item.Close(ctx)
			return fmt.Errorf("discover provider %q: %w", id, err)
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
				item.Online = false
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

func (m *Manager) ProviderInfos() []providersdk.RuntimeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]providersdk.RuntimeInfo, 0, len(m.order))
	for _, id := range m.order {
		current := m.providers[id]
		result = append(result, providersdk.RuntimeInfo{Manifest: current.provider.Manifest(), Capabilities: current.provider.Capabilities(), Status: current.status, Error: current.err})
	}
	return result
}

func (m *Manager) Close(ctx context.Context) error {
	m.mu.RLock()
	ids := append([]string(nil), m.order...)
	m.mu.RUnlock()
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
		current.status = "stopped"
		current.unsubscribe = nil
		m.mu.Unlock()
	}
	return first
}

var _ providersdk.Provider = (*Manager)(nil)
var _ providersdk.Discoverer = (*Manager)(nil)
var _ providersdk.PropertyWriter = (*Manager)(nil)
var _ providersdk.EventSubscriber = (*Manager)(nil)
var _ providersdk.Inspector = (*Manager)(nil)
