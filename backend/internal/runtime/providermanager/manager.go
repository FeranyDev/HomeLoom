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
	provider providersdk.Provider
	status   string
	err      string
}

type Manager struct {
	mu        sync.RWMutex
	providers map[string]*managedProvider
	order     []string
	routes    map[string]string
}

func New(providers ...providersdk.Provider) (*Manager, error) {
	manager := &Manager{providers: make(map[string]*managedProvider), routes: make(map[string]string)}
	for _, current := range providers {
		id := current.Manifest().ID
		if id == "" {
			return nil, fmt.Errorf("provider id is required")
		}
		if _, exists := manager.providers[id]; exists {
			return nil, fmt.Errorf("duplicate provider id %q", id)
		}
		manager.providers[id] = &managedProvider{provider: current, status: "created"}
		manager.order = append(manager.order, id)
	}
	return manager, nil
}

func (m *Manager) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: "provider-manager", Type: "manager", Name: "Provider Manager", Version: "0.1.0"}
}

func (m *Manager) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyRead: true, PropertyWrite: true, Events: true}
}

func (m *Manager) Initialize(ctx context.Context) error {
	for _, id := range m.order {
		managed := m.providers[id]
		if err := managed.provider.Initialize(ctx); err != nil {
			m.mu.Lock()
			managed.status, managed.err = "error", err.Error()
			m.mu.Unlock()
			return fmt.Errorf("initialize provider %q: %w", id, err)
		}
		m.mu.Lock()
		managed.status, managed.err = "running", ""
		m.mu.Unlock()
	}
	return nil
}

func (m *Manager) DiscoverDevices(ctx context.Context) ([]device.Device, error) {
	result := make([]device.Device, 0)
	routes := make(map[string]string)
	for _, id := range m.order {
		managed := m.providers[id]
		discoverer, ok := managed.provider.(providersdk.Discoverer)
		if !ok {
			continue
		}
		items, err := discoverer.DiscoverDevices(ctx)
		if err != nil {
			return nil, fmt.Errorf("discover provider %q: %w", id, err)
		}
		for _, item := range items {
			if owner, exists := routes[item.ID]; exists {
				return nil, fmt.Errorf("device id %q is provided by both %q and %q", item.ID, owner, id)
			}
			item.ProviderID = id
			routes[item.ID] = id
			result = append(result, item)
		}
	}
	m.mu.Lock()
	m.routes = routes
	m.mu.Unlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (m *Manager) WriteProperty(ctx context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	m.mu.RLock()
	providerID, ok := m.routes[request.DeviceID]
	managed := m.providers[providerID]
	m.mu.RUnlock()
	if !ok || managed == nil {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	writer, ok := managed.provider.(providersdk.PropertyWriter)
	if !ok {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	item, err := writer.WriteProperty(ctx, request)
	if err != nil {
		return device.Device{}, err
	}
	item.ProviderID = providerID
	return item, nil
}

func (m *Manager) Subscribe(handler func(device.Device)) func() {
	unsubscribes := make([]func(), 0)
	for _, id := range m.order {
		managed := m.providers[id]
		subscriber, ok := managed.provider.(providersdk.EventSubscriber)
		if !ok {
			continue
		}
		providerID := id
		unsubscribes = append(unsubscribes, subscriber.Subscribe(func(item device.Device) {
			item.ProviderID = providerID
			m.mu.Lock()
			owner, exists := m.routes[item.ID]
			if !exists || owner == providerID {
				m.routes[item.ID] = providerID
			}
			m.mu.Unlock()
			if exists && owner != providerID {
				return
			}
			handler(item)
		}))
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, unsubscribe := range unsubscribes {
				unsubscribe()
			}
		})
	}
}

func (m *Manager) ProviderInfos() []providersdk.RuntimeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]providersdk.RuntimeInfo, 0, len(m.order))
	for _, id := range m.order {
		managed := m.providers[id]
		result = append(result, providersdk.RuntimeInfo{
			Manifest: managed.provider.Manifest(), Capabilities: managed.provider.Capabilities(),
			Status: managed.status, Error: managed.err,
		})
	}
	return result
}

func (m *Manager) Close(ctx context.Context) error {
	var firstError error
	for index := len(m.order) - 1; index >= 0; index-- {
		id := m.order[index]
		managed := m.providers[id]
		if err := managed.provider.Close(ctx); err != nil && firstError == nil {
			firstError = fmt.Errorf("close provider %q: %w", id, err)
		}
		m.mu.Lock()
		managed.status = "stopped"
		m.mu.Unlock()
	}
	return firstError
}

var _ providersdk.Provider = (*Manager)(nil)
var _ providersdk.Discoverer = (*Manager)(nil)
var _ providersdk.PropertyWriter = (*Manager)(nil)
var _ providersdk.EventSubscriber = (*Manager)(nil)
var _ providersdk.Inspector = (*Manager)(nil)
