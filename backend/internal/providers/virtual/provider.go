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
var _ providersdk.PropertyReader = (*Provider)(nil)
var _ providersdk.PropertyWriter = (*Provider)(nil)
var _ providersdk.CommandExecutor = (*Provider)(nil)
var _ providersdk.EventSubscriber = (*Provider)(nil)
var _ providersdk.Simulator = (*Provider)(nil)

func (p *Provider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "virtual", Name: p.name, Version: "0.1.0"}
}

func (p *Provider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyRead: true, PropertyWrite: true, Commands: true, Events: true}
}

func (p *Provider) Initialize(context.Context) error { return nil }
func (p *Provider) Close(context.Context) error      { return nil }

func (p *Provider) DiscoverDevices(ctx context.Context) ([]device.Device, error) { return p.List(ctx) }

func (p *Provider) ReadProperty(ctx context.Context, request providersdk.PropertyReadRequest) (device.Property, error) {
	select {
	case <-ctx.Done():
		return device.Property{}, ctx.Err()
	default:
	}
	p.mu.RLock()
	item, ok := p.devices[request.DeviceID]
	p.mu.RUnlock()
	if !ok {
		return device.Property{}, providersdk.ErrDeviceNotFound
	}
	if !item.IsOnline() {
		return device.Property{}, providersdk.ErrProviderUnavailable
	}
	property, ok := item.Clone().Property(request.EndpointID, request.CapabilityID, request.PropertyID)
	if !ok || !property.Definition.Readable {
		return device.Property{}, providersdk.ErrPropertyUnsupported
	}
	return property, nil
}

func (p *Provider) WriteProperty(ctx context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	if err := p.waitForWrite(ctx); err != nil {
		return device.Device{}, err
	}
	p.mu.Lock()
	item, ok := p.devices[request.DeviceID]
	if !ok {
		p.mu.Unlock()
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	if !item.IsOnline() {
		p.mu.Unlock()
		return device.Device{}, providersdk.ErrProviderUnavailable
	}
	property, ok := item.Property(request.EndpointID, request.CapabilityID, request.PropertyID)
	if !ok || !property.Definition.Writable {
		p.mu.Unlock()
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	property.Value = request.Value
	if err := property.Validate(); err != nil {
		p.mu.Unlock()
		return device.Device{}, providersdk.ErrPropertyInvalid
	}
	item.SetProperty(request.EndpointID, request.CapabilityID, request.PropertyID, request.Value)
	reconcileVirtualState(&item, request.CapabilityID, request.PropertyID)
	return p.commitLocked(item), nil
}

func (p *Provider) ExecuteCommand(ctx context.Context, request providersdk.CommandRequest) (device.Device, error) {
	if request.EndpointID == "main" && request.CapabilityID == "filter" && request.CommandID == "reset-filter" {
		if len(request.Parameters) != 0 {
			return device.Device{}, providersdk.ErrCommandInvalid
		}
		return p.resetFilter(ctx, request.DeviceID)
	}
	if request.EndpointID != "main" || request.CapabilityID != "switch" {
		return device.Device{}, providersdk.ErrCommandUnsupported
	}
	switch request.CommandID {
	case "toggle":
		if len(request.Parameters) != 0 {
			return device.Device{}, providersdk.ErrCommandInvalid
		}
		property, err := p.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: request.DeviceID, EndpointID: "main", CapabilityID: "switch", PropertyID: "power"})
		if err != nil {
			return device.Device{}, err
		}
		return p.SetPower(ctx, request.DeviceID, !*property.Value.Bool)
	case "set-power":
		value, ok := request.Parameters["value"]
		if !ok || len(request.Parameters) != 1 || value.Type != device.ValueTypeBool || value.Bool == nil {
			return device.Device{}, providersdk.ErrCommandInvalid
		}
		return p.SetPower(ctx, request.DeviceID, *value.Bool)
	default:
		return device.Device{}, providersdk.ErrCommandUnsupported
	}
}

func NewProvider() *Provider {
	return NewProviderWithIdentity("virtual-main", "Virtual Provider")
}

func NewProviderWithIdentity(id, name string) *Provider {
	return newProvider(id, name, Config{}, defaultDevices(id))
}

func newProvider(id, name string, config Config, devices map[string]device.Device) *Provider {
	for deviceID, item := range devices {
		_ = item.NormalizeModelParameters()
		devices[deviceID] = item
	}
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
		switchID:      poweredDevice(switchID, id, "客厅开关", device.TypeSwitch, true, false),
		temperatureID: temperatureDevice(temperatureID, id, "客厅温度", true, 23.6),
	}
}

func poweredDevice(id, providerID, name string, deviceType device.Type, online, power bool) device.Device {
	capabilities := []device.Capability{{ID: "switch", Type: "switch", Properties: []device.Property{{Definition: device.PropertyDefinition{ID: "power", Name: "开关", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true, StaleAfterSeconds: 15}, Value: device.BoolValue(power)}}, Commands: []device.CommandDefinition{{ID: "toggle", Name: "切换"}, {ID: "set-power", Name: "设置开关", Idempotent: true, Parameters: []device.CommandParameter{{ID: "value", Name: "开关值", Type: device.ValueTypeBool, Required: true}}}}}}
	minimum, maximum, step := 0.0, 100.0, 1.0
	if deviceType == device.TypeLightbulb {
		colorMinimum, colorMaximum := 140.0, 500.0
		hueMaximum := 360.0
		capabilities = append(capabilities, device.Capability{ID: "light", Type: "light", Properties: []device.Property{
			{Definition: device.PropertyDefinition{ID: "brightness", Name: "亮度", Type: device.ValueTypeNumber, Unit: "percent", Readable: true, Writable: true, Notifiable: true, Min: &minimum, Max: &maximum, Step: &step, StaleAfterSeconds: 15}, Value: device.NumberValue(100)},
			{Definition: device.PropertyDefinition{ID: "color-temperature", Name: "色温", Type: device.ValueTypeInt, Unit: "mired", Readable: true, Writable: true, Notifiable: true, Min: &colorMinimum, Max: &colorMaximum, Step: &step, StaleAfterSeconds: 15}, Value: device.IntValue(250)},
			{Definition: device.PropertyDefinition{ID: "hue", Name: "色相", Type: device.ValueTypeNumber, Unit: "degree", Readable: true, Writable: true, Notifiable: true, Min: &minimum, Max: &hueMaximum, Step: &step, StaleAfterSeconds: 15}, Value: device.NumberValue(0)},
			{Definition: device.PropertyDefinition{ID: "saturation", Name: "饱和度", Type: device.ValueTypeNumber, Unit: "percent", Readable: true, Writable: true, Notifiable: true, Min: &minimum, Max: &maximum, Step: &step, StaleAfterSeconds: 15}, Value: device.NumberValue(0)},
		}})
	}
	if deviceType == device.TypeOutlet {
		capabilities = append(capabilities,
			device.Capability{ID: "outlet", Type: "outlet", Properties: []device.Property{{Definition: device.PropertyDefinition{ID: "in-use", Name: "正在使用", Type: device.ValueTypeBool, Readable: true, Notifiable: true, StaleAfterSeconds: 15}, Value: device.BoolValue(power)}}},
			device.Capability{ID: "electrical", Type: "electrical-meter", Properties: []device.Property{
				{Definition: device.PropertyDefinition{ID: "current-power", Name: "当前功率", Type: device.ValueTypeNumber, Unit: "watt", Readable: true, Notifiable: true, Min: &minimum, StaleAfterSeconds: 30}, Value: device.NumberValue(0)},
				{Definition: device.PropertyDefinition{ID: "energy", Name: "累计电量", Type: device.ValueTypeNumber, Unit: "kilowatt-hour", Readable: true, Notifiable: true, Min: &minimum, StaleAfterSeconds: 300}, Value: device.NumberValue(0)},
			}},
		)
	}
	item := device.Device{SchemaVersion: device.SchemaVersion, ID: id, ProviderID: providerID, Name: name, Type: deviceType, Sequence: 1, LastUpdateAt: time.Now().UTC(), Endpoints: []device.Endpoint{{ID: "main", Name: "主端点", Type: string(deviceType), Capabilities: capabilities}}}
	item.SetOnline(online)
	return item
}

func temperatureDevice(id, providerID, name string, online bool, temperature float64) device.Device {
	minimum, maximum, step := -100.0, 200.0, 0.1
	item := device.Device{SchemaVersion: device.SchemaVersion, ID: id, ProviderID: providerID, Name: name,
		Type:         device.TypeTemperatureSensor,
		Sequence:     1,
		LastUpdateAt: time.Now().UTC(),
		Endpoints: []device.Endpoint{{
			ID: "main", Name: "主端点", Type: "sensor",
			Capabilities: []device.Capability{{ID: "temperature", Type: "temperature-sensor", Properties: []device.Property{{
				Definition: device.PropertyDefinition{ID: "current-temperature", Name: "当前温度", Type: device.ValueTypeNumber, Unit: "celsius", Readable: true, Notifiable: true, Min: &minimum, Max: &maximum, Step: &step, StaleAfterSeconds: 30},
				Value:      device.NumberValue(temperature),
			}}}},
		}},
	}
	item.SetOnline(online)
	item.Endpoints[0].Capabilities = append(item.Endpoints[0].Capabilities, sensorStatusCapabilities()...)
	return item
}

func humidityDevice(id, providerID, name string, online bool, humidity float64) device.Device {
	minimum, maximum, step := 0.0, 100.0, 0.1
	item := device.Device{SchemaVersion: device.SchemaVersion, ID: id, ProviderID: providerID, Name: name, Type: device.TypeHumiditySensor, Sequence: 1, LastUpdateAt: time.Now().UTC(), Endpoints: []device.Endpoint{{ID: "main", Name: "主端点", Type: "sensor", Capabilities: []device.Capability{{ID: "humidity", Type: "humidity-sensor", Properties: []device.Property{{Definition: device.PropertyDefinition{ID: "current-humidity", Name: "当前湿度", Type: device.ValueTypeNumber, Unit: "percent", Readable: true, Notifiable: true, Min: &minimum, Max: &maximum, Step: &step, StaleAfterSeconds: 30}, Value: device.NumberValue(humidity)}}}}}}}
	item.SetOnline(online)
	item.Endpoints[0].Capabilities = append(item.Endpoints[0].Capabilities, sensorStatusCapabilities()...)
	return item
}

func booleanSensorDevice(id, providerID, name string, deviceType device.Type, capabilityID, capabilityType, propertyID, propertyName string, online, value bool) device.Device {
	item := device.Device{SchemaVersion: device.SchemaVersion, ID: id, ProviderID: providerID, Name: name, Type: deviceType, Sequence: 1, LastUpdateAt: time.Now().UTC(), Endpoints: []device.Endpoint{{ID: "main", Name: "主端点", Type: "sensor", Capabilities: []device.Capability{{ID: capabilityID, Type: capabilityType, Properties: []device.Property{{Definition: device.PropertyDefinition{ID: propertyID, Name: propertyName, Type: device.ValueTypeBool, Readable: true, Notifiable: true, StaleAfterSeconds: 30}, Value: device.BoolValue(value)}}}}}}}
	item.SetOnline(online)
	item.Endpoints[0].Capabilities = append(item.Endpoints[0].Capabilities, sensorStatusCapabilities()...)
	return item
}

func sensorStatusCapabilities() []device.Capability {
	minimum, maximum, step := 0.0, 100.0, 1.0
	return []device.Capability{
		{ID: "battery", Type: "battery", Properties: []device.Property{
			{Definition: device.PropertyDefinition{ID: "level", Name: "电池电量", Type: device.ValueTypeInt, Unit: "percent", Readable: true, Notifiable: true, Min: &minimum, Max: &maximum, Step: &step, StaleAfterSeconds: 300}, Value: device.IntValue(100)},
			{Definition: device.PropertyDefinition{ID: "low", Name: "低电量", Type: device.ValueTypeBool, Readable: true, Notifiable: true, StaleAfterSeconds: 300}, Value: device.BoolValue(false)},
		}},
		{ID: "security", Type: "security-status", Properties: []device.Property{{Definition: device.PropertyDefinition{ID: "tampered", Name: "防拆状态", Type: device.ValueTypeBool, Readable: true, Notifiable: true, StaleAfterSeconds: 300}, Value: device.BoolValue(false)}}},
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
	return p.WriteProperty(ctx, providersdk.PropertyWriteRequest{DeviceID: id, EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(power)})
}

func (p *Provider) waitForWrite(ctx context.Context) error {
	if p.config.RejectWrites {
		return providersdk.ErrWriteRejected
	}
	if p.config.LatencyMS > 0 {
		timer := time.NewTimer(time.Duration(p.config.LatencyMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func (p *Provider) resetFilter(ctx context.Context, id string) (device.Device, error) {
	if err := p.waitForWrite(ctx); err != nil {
		return device.Device{}, err
	}
	p.mu.Lock()
	item, ok := p.devices[id]
	if !ok {
		p.mu.Unlock()
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	if !item.IsOnline() {
		p.mu.Unlock()
		return device.Device{}, providersdk.ErrProviderUnavailable
	}
	if _, ok := item.Property("main", "filter", "life-level"); !ok {
		p.mu.Unlock()
		return device.Device{}, providersdk.ErrCommandUnsupported
	}
	item.SetProperty("main", "filter", "life-level", device.NumberValue(100))
	item.SetProperty("main", "filter", "change-indication", device.BoolValue(false))
	return p.commitLocked(item), nil
}

func (p *Provider) commitLocked(item device.Device) device.Device {
	item.Sequence++
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
	return item.Clone()
}

func (p *Provider) Simulate(_ context.Context, request providersdk.SimulationRequest) (device.Device, error) {
	repeat := request.Repeat
	if repeat == 0 {
		repeat = 1
	}
	if repeat < 1 || repeat > 10 {
		return device.Device{}, providersdk.ErrSimulationInvalid
	}
	p.mu.Lock()
	item, ok := p.devices[request.DeviceID]
	if !ok {
		p.mu.Unlock()
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	if request.Online != nil {
		item.SetOnline(*request.Online)
	}
	if request.Availability != nil {
		if *request.Availability != device.AvailabilityOnline && *request.Availability != device.AvailabilityOffline && *request.Availability != device.AvailabilityUnknown {
			p.mu.Unlock()
			return device.Device{}, providersdk.ErrSimulationInvalid
		}
		item.SetAvailability(*request.Availability)
	}
	for _, change := range request.Properties {
		property, exists := item.Property(change.EndpointID, change.CapabilityID, change.PropertyID)
		if !exists {
			p.mu.Unlock()
			return device.Device{}, providersdk.ErrSimulationInvalid
		}
		property.Value = change.Value
		if err := property.Validate(); err != nil {
			p.mu.Unlock()
			return device.Device{}, providersdk.ErrSimulationInvalid
		}
		item.SetProperty(change.EndpointID, change.CapabilityID, change.PropertyID, change.Value)
		reconcileVirtualState(&item, change.CapabilityID, change.PropertyID)
	}
	eventSequence := item.Sequence + 1
	if request.Sequence != nil {
		eventSequence = *request.Sequence
	}
	if eventSequence > item.Sequence {
		item.Sequence = eventSequence
	}
	item.LastUpdateAt = time.Now().UTC()
	p.devices[item.ID] = item
	event := item.Clone()
	event.Sequence = eventSequence
	listeners := make([]func(device.Device), 0, len(p.listeners))
	for _, listener := range p.listeners {
		listeners = append(listeners, listener)
	}
	p.mu.Unlock()
	for index := 0; index < repeat; index++ {
		for _, listener := range listeners {
			listener(event.Clone())
		}
	}
	return event, nil
}

func reconcileVirtualState(item *device.Device, capabilityID, propertyID string) {
	switch capabilityID {
	case "switch":
		if item.Type == device.TypeOutlet && propertyID == "power" {
			item.SetProperty("main", "outlet", "in-use", device.BoolValue(boolValue(*item, "switch", "power")))
		}
	case "fan":
		active, speed := boolValue(*item, "fan", "active"), numberValue(*item, "fan", "rotation-speed")
		item.SetProperty("main", "fan", "current-state", device.EnumValue(fanState(active, speed)))
	case "air-purifier":
		active, speed := boolValue(*item, "air-purifier", "active"), numberValue(*item, "air-purifier", "rotation-speed")
		item.SetProperty("main", "air-purifier", "current-state", device.EnumValue(airPurifierState(active, speed)))
	case "window-covering":
		if propertyID == "target-position" {
			property, _ := item.Property("main", "window-covering", "target-position")
			item.SetProperty("main", "window-covering", "current-position", property.Value)
			item.SetProperty("main", "window-covering", "position-state", device.EnumValue(positionStopped))
		}
	case "filter":
		if propertyID == "life-level" {
			item.SetProperty("main", "filter", "change-indication", device.BoolValue(numberValue(*item, "filter", "life-level") <= 10))
		}
	}
}

func boolValue(item device.Device, capabilityID, propertyID string) bool {
	property, ok := item.Property("main", capabilityID, propertyID)
	return ok && property.Value.Bool != nil && *property.Value.Bool
}

func numberValue(item device.Device, capabilityID, propertyID string) float64 {
	property, ok := item.Property("main", capabilityID, propertyID)
	if !ok || property.Value.Number == nil {
		return 0
	}
	return *property.Value.Number
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
