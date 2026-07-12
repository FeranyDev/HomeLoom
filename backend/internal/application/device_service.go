package application

import (
	"context"
	"errors"
	"sync"
	"time"

	commandtracker "github.com/feranydev/homeloom/backend/internal/command"
	domaincommand "github.com/feranydev/homeloom/backend/internal/domain/command"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
	"github.com/feranydev/homeloom/backend/internal/eventbus"
	"github.com/feranydev/homeloom/backend/internal/registry"
	statestore "github.com/feranydev/homeloom/backend/internal/state"
)

var (
	ErrDeviceNotFound      = errors.New("device not found")
	ErrPropertyUnsupported = errors.New("property unsupported")
)

type DeviceProvider interface {
	List(context.Context) ([]device.Device, error)
	SetPower(context.Context, string, bool) (device.Device, error)
}

type DeviceSubscriber interface {
	Subscribe(func(device.Device)) func()
}

type DeviceService struct {
	provider    DeviceProvider
	registry    *registry.DeviceRegistry
	dispatcher  *eventbus.Dispatcher
	states      *statestore.Store
	commands    *commandtracker.Tracker
	unsubscribe func()
	mu          sync.RWMutex
	nextID      uint64
	listeners   map[uint64]func(device.Device)
}

func NewDeviceService(provider DeviceProvider) *DeviceService {
	items, _ := provider.List(context.Background())
	service := &DeviceService{
		provider: provider, registry: registry.NewDeviceRegistry(items),
		states: statestore.NewStore(), commands: commandtracker.NewTracker(5 * time.Second),
		listeners: make(map[uint64]func(device.Device)),
	}
	for _, item := range items {
		service.applySnapshot(item)
	}
	service.dispatcher = eventbus.NewDispatcher(8, 128, service.handleEvent)
	if subscriber, ok := provider.(DeviceSubscriber); ok {
		service.unsubscribe = subscriber.Subscribe(func(item device.Device) {
			_ = service.dispatcher.Publish(eventbus.Event{DeviceID: item.ID, Payload: item})
		})
	} else {
		service.unsubscribe = func() {}
	}
	return service
}

func (s *DeviceService) States(deviceID string) []domainstate.StateValue {
	return s.states.Device(deviceID)
}

func (s *DeviceService) List(ctx context.Context) ([]device.Device, error) {
	return s.registry.List(), nil
}

func (s *DeviceService) SetPower(ctx context.Context, id string, power bool) (device.Device, error) {
	return s.provider.SetPower(ctx, id, power)
}

func (s *DeviceService) ExecutePower(ctx context.Context, id string, power bool) (device.Device, domaincommand.Command, error) {
	return s.ExecuteProperty(ctx, id, "main", "switch", "power", device.BoolValue(power))
}

func (s *DeviceService) ExecuteProperty(ctx context.Context, deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue) (device.Device, domaincommand.Command, error) {
	command := s.commands.Begin(deviceID, endpointID, capabilityID, propertyID, value)
	s.commands.Sent(command.ID)
	if endpointID != "main" || capabilityID != "switch" || propertyID != "power" || value.Type != device.ValueTypeBool || value.Bool == nil {
		err := ErrPropertyUnsupported
		s.commands.Rejected(command.ID, err)
		current, _ := s.commands.Get(command.ID)
		return device.Device{}, current, err
	}
	item, err := s.provider.SetPower(ctx, deviceID, *value.Bool)
	if err != nil {
		s.commands.Rejected(command.ID, err)
		current, _ := s.commands.Get(command.ID)
		return device.Device{}, current, err
	}
	s.commands.Accepted(command.ID)
	current, _ := s.commands.Get(command.ID)
	return item, current, nil
}

func (s *DeviceService) Commands() []domaincommand.Command { return s.commands.List() }

func (s *DeviceService) Command(id string) (domaincommand.Command, bool) { return s.commands.Get(id) }

func (s *DeviceService) Subscribe(handler func(device.Device)) func() {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.listeners[id] = handler
	s.mu.Unlock()
	return func() { s.mu.Lock(); delete(s.listeners, id); s.mu.Unlock() }
}

func (s *DeviceService) Close() error {
	s.unsubscribe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.dispatcher.Close(ctx)
}

func (s *DeviceService) handleEvent(event eventbus.Event) {
	item, ok := event.Payload.(device.Device)
	if !ok {
		return
	}
	s.registry.Upsert(item)
	s.applySnapshot(item)
	for _, endpoint := range item.Endpoints {
		for _, capability := range endpoint.Capabilities {
			for _, property := range capability.Properties {
				s.commands.Confirm(item.ID, endpoint.ID, capability.ID, property.Definition.ID, property.Value)
			}
		}
	}
	s.mu.RLock()
	listeners := make([]func(device.Device), 0, len(s.listeners))
	for _, listener := range s.listeners {
		listeners = append(listeners, listener)
	}
	s.mu.RUnlock()
	for _, listener := range listeners {
		listener(item)
	}
}

func (s *DeviceService) applySnapshot(item device.Device) {
	receivedAt := time.Now().UTC()
	for _, endpoint := range item.Endpoints {
		for _, capability := range endpoint.Capabilities {
			for _, property := range capability.Properties {
				value := domainstate.StateValue{
					Key:        domainstate.Key{DeviceID: item.ID, EndpointID: endpoint.ID, CapabilityID: capability.ID, PropertyID: property.Definition.ID},
					ProviderID: item.ProviderID, Source: domainstate.SourceReported,
					ObservedAt: item.LastUpdateAt, ReceivedAt: receivedAt, Quality: domainstate.QualityReported,
				}
				switch property.Value.Type {
				case device.ValueTypeBool:
					if property.Value.Bool == nil {
						continue
					}
					value.Value = domainstate.BoolValue(*property.Value.Bool)
				case device.ValueTypeNumber:
					if property.Value.Number == nil {
						continue
					}
					value.Value = domainstate.NumberValue(*property.Value.Number)
				default:
					continue
				}
				s.states.Apply(value)
			}
		}
	}
}
