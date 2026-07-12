package virtual

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

type Provider struct {
	mu        sync.RWMutex
	devices   map[string]device.Device
	nextID    uint64
	listeners map[uint64]func(device.Device)
}

func NewProvider() *Provider {
	now := time.Now().UTC()
	off := false
	temperature := 23.6
	return &Provider{devices: map[string]device.Device{
		"virtual-switch-1": {
			ID: "virtual-switch-1", ProviderID: "virtual-main", Name: "客厅开关",
			Type: device.TypeSwitch, Online: true, State: device.State{Power: &off}, LastUpdateAt: now,
		},
		"virtual-temperature-1": {
			ID: "virtual-temperature-1", ProviderID: "virtual-main", Name: "客厅温度",
			Type: device.TypeTemperatureSensor, Online: true,
			State: device.State{Temperature: &temperature}, LastUpdateAt: now,
		},
	}, listeners: make(map[uint64]func(device.Device))}
}

func (p *Provider) List(context.Context) ([]device.Device, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]device.Device, 0, len(p.devices))
	for _, item := range p.devices {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (p *Provider) SetPower(_ context.Context, id string, power bool) (device.Device, error) {
	p.mu.Lock()
	item, ok := p.devices[id]
	if !ok {
		p.mu.Unlock()
		return device.Device{}, application.ErrDeviceNotFound
	}
	if item.Type != device.TypeSwitch {
		p.mu.Unlock()
		return device.Device{}, application.ErrPropertyUnsupported
	}
	item.State.Power = &power
	item.LastUpdateAt = time.Now().UTC()
	p.devices[id] = item
	listeners := make([]func(device.Device), 0, len(p.listeners))
	for _, listener := range p.listeners {
		listeners = append(listeners, listener)
	}
	p.mu.Unlock()

	for _, listener := range listeners {
		listener(item)
	}
	return item, nil
}

func (p *Provider) Subscribe(handler func(device.Device)) func() {
	p.mu.Lock()
	p.nextID++
	id := p.nextID
	p.listeners[id] = handler
	p.mu.Unlock()

	return func() {
		p.mu.Lock()
		delete(p.listeners, id)
		p.mu.Unlock()
	}
}
