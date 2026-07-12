package virtual

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type Provider struct {
	mu        sync.RWMutex
	devices   map[string]device.Device
	nextID    uint64
	listeners map[uint64]func(device.Device)
}

var _ providersdk.Provider = (*Provider)(nil)
var _ providersdk.Discoverer = (*Provider)(nil)
var _ providersdk.PropertyWriter = (*Provider)(nil)
var _ providersdk.EventSubscriber = (*Provider)(nil)

func (p *Provider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: "virtual-main", Type: "virtual", Name: "Virtual Provider", Version: "0.1.0"}
}

func (p *Provider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyRead: true, PropertyWrite: true, Events: true}
}

func (p *Provider) Initialize(context.Context) error { return nil }
func (p *Provider) Close(context.Context) error      { return nil }

func (p *Provider) DiscoverDevices(ctx context.Context) ([]device.Device, error) { return p.List(ctx) }

func (p *Provider) WriteProperty(ctx context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	if request.EndpointID != "main" || request.CapabilityID != "switch" || request.PropertyID != "power" ||
		request.Value.Type != device.ValueTypeBool || request.Value.Bool == nil {
		return device.Device{}, application.ErrPropertyUnsupported
	}
	return p.SetPower(ctx, request.DeviceID, *request.Value.Bool)
}

func NewProvider() *Provider {
	now := time.Now().UTC()
	off := false
	temperature := 23.6
	return &Provider{devices: map[string]device.Device{
		"virtual-switch-1": {
			ID: "virtual-switch-1", ProviderID: "virtual-main", Name: "客厅开关",
			Type: device.TypeSwitch, Online: true, State: device.State{Power: &off}, LastUpdateAt: now,
			Endpoints: []device.Endpoint{{
				ID: "main", Name: "主端点", Type: "switch",
				Capabilities: []device.Capability{{ID: "switch", Type: "switch", Properties: []device.Property{{
					Definition: device.PropertyDefinition{ID: "power", Name: "开关", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 15},
					Value:      device.BoolValue(false),
				}}}},
			}},
		},
		"virtual-temperature-1": {
			ID: "virtual-temperature-1", ProviderID: "virtual-main", Name: "客厅温度",
			Type: device.TypeTemperatureSensor, Online: true,
			State: device.State{Temperature: &temperature}, LastUpdateAt: now,
			Endpoints: []device.Endpoint{{
				ID: "main", Name: "主端点", Type: "sensor",
				Capabilities: []device.Capability{{ID: "temperature", Type: "temperature-sensor", Properties: []device.Property{{
					Definition: device.PropertyDefinition{ID: "current-temperature", Name: "当前温度", Type: device.ValueTypeNumber, Unit: "celsius", Readable: true, Notifiable: true, StaleAfterSeconds: 30},
					Value:      device.NumberValue(temperature),
				}}}},
			}},
		},
	}, listeners: make(map[uint64]func(device.Device))}
}

func (p *Provider) List(context.Context) ([]device.Device, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]device.Device, 0, len(p.devices))
	for _, item := range p.devices {
		result = append(result, item.Clone())
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
	item.SetProperty("main", "switch", "power", device.BoolValue(power))
	item.LastUpdateAt = time.Now().UTC()
	p.devices[id] = item
	listeners := make([]func(device.Device), 0, len(p.listeners))
	for _, listener := range p.listeners {
		listeners = append(listeners, listener)
	}
	p.mu.Unlock()

	for _, listener := range listeners {
		listener(item.Clone())
	}
	return item.Clone(), nil
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
