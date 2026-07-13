package registry

import (
	"sort"
	"sync"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

type DeviceRegistry struct {
	mu      sync.RWMutex
	devices map[string]device.Device
}

func NewDeviceRegistry(items []device.Device) *DeviceRegistry {
	registry := &DeviceRegistry{devices: make(map[string]device.Device, len(items))}
	for _, item := range items {
		item.NormalizeAvailability()
		registry.devices[item.ID] = item
	}
	return registry
}

func (r *DeviceRegistry) Upsert(item device.Device) {
	item.NormalizeAvailability()
	r.mu.Lock()
	r.devices[item.ID] = item
	r.mu.Unlock()
}

func (r *DeviceRegistry) Get(id string) (device.Device, bool) {
	r.mu.RLock()
	item, ok := r.devices[id]
	r.mu.RUnlock()
	if !ok {
		return device.Device{}, false
	}
	return item.Clone(), true
}

func (r *DeviceRegistry) List() []device.Device {
	r.mu.RLock()
	result := make([]device.Device, 0, len(r.devices))
	for _, item := range r.devices {
		result = append(result, item)
	}
	r.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
