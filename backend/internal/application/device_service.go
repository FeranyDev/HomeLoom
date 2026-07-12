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
	command := s.commands.BeginBool(id, "power", power)
	s.commands.Sent(command.ID)
	item, err := s.provider.SetPower(ctx, id, power)
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
	if item.State.Power != nil {
		s.commands.ConfirmBool(item.ID, "power", *item.State.Power)
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
	base := domainstate.StateValue{
		ProviderID: item.ProviderID, Source: domainstate.SourceReported,
		ObservedAt: item.LastUpdateAt, ReceivedAt: receivedAt, Quality: domainstate.QualityReported,
	}
	if item.State.Power != nil {
		value := base
		value.Key = domainstate.Key{DeviceID: item.ID, PropertyID: "power"}
		value.Value = domainstate.BoolValue(*item.State.Power)
		s.states.Apply(value)
	}
	if item.State.Temperature != nil {
		value := base
		value.Key = domainstate.Key{DeviceID: item.ID, PropertyID: "temperature"}
		value.Value = domainstate.NumberValue(*item.State.Temperature)
		s.states.Apply(value)
	}
}
