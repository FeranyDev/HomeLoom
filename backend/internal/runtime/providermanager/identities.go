package providermanager

import (
	"context"
	"fmt"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

// DeviceIdentityStore is the narrow persistence contract needed by the
// Provider Manager. It records already-canonical Device IDs but never changes
// routing: Provider calls continue to use their native IDs, while Device.ID is
// the established internal/public identity.
type DeviceIdentityStore interface {
	EnsureProviderDeviceIdentity(context.Context, string, string, string) error
	EnsureDeviceTopologyIdentity(context.Context, device.Device) error
}

// SetDeviceIdentityStore installs the optional stable identity registry. It is
// intentionally separate from Provider initialization so in-memory tests and
// embedded consumers can run without a database.
func (m *Manager) SetDeviceIdentityStore(store DeviceIdentityStore) {
	m.mu.Lock()
	m.identityStore = store
	m.mu.Unlock()
}

func (m *Manager) persistDiscoveryIdentities(ctx context.Context, sources, logical []device.Device) error {
	m.mu.RLock()
	store := m.identityStore
	resolvers := make(map[string]providersdk.DeviceIdentityResolver, len(m.providers))
	for providerID, current := range m.providers {
		if resolver, ok := current.provider.(providersdk.DeviceIdentityResolver); ok {
			resolvers[providerID] = resolver
		}
	}
	m.mu.RUnlock()
	if store == nil {
		return nil
	}
	for _, item := range sources {
		providerDeviceID := item.ID
		if resolver := resolvers[item.ProviderID]; resolver != nil {
			if resolved, ok := resolver.ProviderDeviceID(item.ID); ok {
				providerDeviceID = resolved
			}
		}
		if err := store.EnsureProviderDeviceIdentity(ctx, item.ProviderID, providerDeviceID, item.ID); err != nil {
			return fmt.Errorf("persist provider device identity %q/%q: %w", item.ProviderID, providerDeviceID, err)
		}
		if err := store.EnsureDeviceTopologyIdentity(ctx, item); err != nil {
			return fmt.Errorf("persist device topology identity %q: %w", item.ID, err)
		}
	}
	for _, item := range logical {
		if err := store.EnsureDeviceTopologyIdentity(ctx, item); err != nil {
			return fmt.Errorf("persist logical device topology identity %q: %w", item.ID, err)
		}
	}
	return nil
}

func cloneDeviceList(items []device.Device) []device.Device {
	result := make([]device.Device, 0, len(items))
	for _, item := range items {
		result = append(result, item.Clone())
	}
	return result
}
