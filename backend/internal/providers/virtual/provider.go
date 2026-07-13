package virtual

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type Provider struct {
	id        string
	name      string
	mu        sync.RWMutex
	devices   map[string]device.Device
	nextID    uint64
	listeners map[uint64]func(device.Device)
	config    Config
}

var _ providersdk.Provider = (*Provider)(nil)
var _ providersdk.Discoverer = (*Provider)(nil)
var _ providersdk.PropertyWriter = (*Provider)(nil)
var _ providersdk.EventSubscriber = (*Provider)(nil)
var _ providersdk.Simulator = (*Provider)(nil)

func (p *Provider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "virtual", Name: p.name, Version: "0.1.0"}
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
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	return p.SetPower(ctx, request.DeviceID, *request.Value.Bool)
}

func NewProvider() *Provider {
	return NewProviderWithIdentity("virtual-main", "Virtual Provider")
}

func NewProviderWithIdentity(id, name string) *Provider {
	return newProvider(id, name, Config{}, defaultDevices(id))
}

func newProvider(id, name string, config Config, devices map[string]device.Device) *Provider {
	return &Provider{id: id, name: name, devices: devices, listeners: make(map[uint64]func(device.Device)), config: config}
}

func defaultDevices(id string) map[string]device.Device {
	prefix := "virtual"
	if id != "virtual-main" {
		prefix = strings.TrimSuffix(id, "-")
	}
	switchID := prefix + "-switch-1"
	temperatureID := prefix + "-temperature-1"
	return map[string]device.Device{
		switchID:      switchDevice(switchID, id, "客厅开关", true, false),
		temperatureID: temperatureDevice(temperatureID, id, "客厅温度", true, 23.6),
	}
}

func switchDevice(id, providerID, name string, online, power bool) device.Device {
	return device.Device{ID: id, ProviderID: providerID, Name: name,
		Type: device.TypeSwitch, Online: online, State: device.State{Power: &power}, LastUpdateAt: time.Now().UTC(),
		Endpoints: []device.Endpoint{{
			ID: "main", Name: "主端点", Type: "switch",
			Capabilities: []device.Capability{{ID: "switch", Type: "switch", Properties: []device.Property{{
				Definition: device.PropertyDefinition{ID: "power", Name: "开关", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 15},
				Value:      device.BoolValue(power),
			}}}},
		}},
	}
}

func temperatureDevice(id, providerID, name string, online bool, temperature float64) device.Device {
	return device.Device{ID: id, ProviderID: providerID, Name: name,
		Type: device.TypeTemperatureSensor, Online: online,
		State: device.State{Temperature: &temperature}, LastUpdateAt: time.Now().UTC(),
		Endpoints: []device.Endpoint{{
			ID: "main", Name: "主端点", Type: "sensor",
			Capabilities: []device.Capability{{ID: "temperature", Type: "temperature-sensor", Properties: []device.Property{{
				Definition: device.PropertyDefinition{ID: "current-temperature", Name: "当前温度", Type: device.ValueTypeNumber, Unit: "celsius", Readable: true, Notifiable: true, StaleAfterSeconds: 30},
				Value:      device.NumberValue(temperature),
			}}}},
		}},
	}
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

func (p *Provider) SetPower(ctx context.Context, id string, power bool) (device.Device, error) {
	if p.config.RejectWrites {
		return device.Device{}, providersdk.ErrWriteRejected
	}
	if p.config.LatencyMS > 0 {
		timer := time.NewTimer(time.Duration(p.config.LatencyMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return device.Device{}, ctx.Err()
		case <-timer.C:
		}
	}
	p.mu.Lock()
	item, ok := p.devices[id]
	if !ok {
		p.mu.Unlock()
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	if item.Type != device.TypeSwitch {
		p.mu.Unlock()
		return device.Device{}, providersdk.ErrPropertyUnsupported
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

func (p *Provider) Simulate(_ context.Context, request providersdk.SimulationRequest) (device.Device, error) {
	p.mu.Lock()
	item, ok := p.devices[request.DeviceID]
	if !ok {
		p.mu.Unlock()
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	if request.Online != nil {
		item.Online = *request.Online
	}
	for _, change := range request.Properties {
		switch {
		case change.EndpointID == "main" && change.CapabilityID == "switch" && change.PropertyID == "power" && change.Value.Type == device.ValueTypeBool && change.Value.Bool != nil && item.Type == device.TypeSwitch:
			value := *change.Value.Bool
			item.State.Power = &value
			item.SetProperty("main", "switch", "power", device.BoolValue(value))
		case change.EndpointID == "main" && change.CapabilityID == "temperature" && change.PropertyID == "current-temperature" && change.Value.Type == device.ValueTypeNumber && change.Value.Number != nil && item.Type == device.TypeTemperatureSensor:
			value := *change.Value.Number
			if value < -100 || value > 200 {
				p.mu.Unlock()
				return device.Device{}, providersdk.ErrSimulationInvalid
			}
			item.State.Temperature = &value
			item.SetProperty("main", "temperature", "current-temperature", device.NumberValue(value))
		default:
			p.mu.Unlock()
			return device.Device{}, providersdk.ErrSimulationInvalid
		}
	}
	item.LastUpdateAt = time.Now().UTC()
	p.devices[item.ID] = item
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
