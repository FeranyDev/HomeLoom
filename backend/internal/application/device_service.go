package application

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	commandtracker "github.com/feranydev/homeloom/backend/internal/command"
	domaincommand "github.com/feranydev/homeloom/backend/internal/domain/command"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
	"github.com/feranydev/homeloom/backend/internal/eventbus"
	"github.com/feranydev/homeloom/backend/internal/mapping"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/registry"
	statestore "github.com/feranydev/homeloom/backend/internal/state"
)

var (
	ErrDeviceNotFound      = providersdk.ErrDeviceNotFound
	ErrPropertyUnsupported = providersdk.ErrPropertyUnsupported
	ErrDeviceDisabled      = errors.New("device is disabled or removed")
	ErrCommandSuperseded   = errors.New("command superseded by a newer property write")
)

type DeviceService struct {
	provider                          providersdk.Provider
	discoverer                        providersdk.Discoverer
	cataloger                         providersdk.SourceCataloger
	reader                            providersdk.PropertyReader
	writer                            providersdk.PropertyWriter
	executor                          providersdk.CommandExecutor
	registry                          *registry.DeviceRegistry
	dispatcher                        *eventbus.Dispatcher
	states                            *statestore.Store
	commands                          *commandtracker.Tracker
	commandQueue                      *commandCoordinator
	unsubscribe                       func()
	unsubscribeDeviceEvents           func()
	unsubscribeCapabilityAvailability func()
	mu                                sync.RWMutex
	nextID                            uint64
	listeners                         map[uint64]*subscription[device.Device]
	nextStateID                       uint64
	stateListeners                    map[uint64]*subscription[domainstate.StateValue]
	nextDeviceEventID                 uint64
	deviceEventListeners              map[uint64]*subscription[providersdk.DeviceEvent]
	snapshotMu                        sync.Mutex
	snapshotSeq                       map[string]uint64
	refreshMu                         sync.Mutex
	staleCancel                       context.CancelFunc
	staleDone                         chan struct{}
	metrics                           deviceMetrics
	storageMetrics                    DatabaseMetricsProvider
	preferences                       DevicePreferenceStore
	disabledMu                        sync.RWMutex
	disabled                          map[string]struct{}
	propertyMu                        sync.Mutex
	propertyOps                       map[domainstate.Key]*propertyOperation
	propertyMapper                    PropertyMapper
}

type propertyOperation struct {
	expected  device.PropertyValue
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	command   domaincommand.Command
	item      device.Device
	err       error
	coalesced uint64
}

type DatabaseMetricsProvider interface {
	DatabaseOperationMetrics() (operations uint64, average time.Duration, maximum time.Duration)
}

type DatabaseHealthProvider interface {
	HealthCheck(context.Context) error
}

type DevicePreferenceStore interface {
	ListDisabledDeviceIDs(context.Context) ([]string, error)
	SetDeviceDisabled(context.Context, string, bool) error
}

type PropertyMapper interface {
	TransformProperty(providerID, deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue, direction mapping.Direction) (device.PropertyValue, string, bool, error)
	TransformPropertyDefinition(providerID, deviceID, endpointID, capabilityID, propertyID string, definition device.PropertyDefinition) (device.PropertyDefinition, string, bool, error)
}

type PropertyPathMapper interface {
	ResolvePropertyPath(providerID, deviceID, endpointID, capabilityID, propertyID string, direction mapping.Direction) (device.ParameterPath, string, bool, error)
}

type ProviderPropertyProjection struct {
	Path       device.ParameterPath
	Definition device.PropertyDefinition
	Value      device.PropertyValue
	BindingID  string
	Explicit   bool
}

type ProviderPropertyProjector interface {
	ProjectProviderProperty(providerID, deviceID, endpointID, capabilityID, propertyID string, definition device.PropertyDefinition, value device.PropertyValue) ([]ProviderPropertyProjection, error)
}

type ConsumerPropertyMapper interface {
	ProjectConsumerDevice(consumerID string, item device.Device) (device.Device, error)
	ResolveConsumerWrite(providerID, deviceID, consumerID string, deviceType device.Type, endpointID, capabilityID, propertyID string, value device.PropertyValue) (device.ParameterPath, device.PropertyValue, string, bool, error)
}

type ScopedConsumerPropertyMapper interface {
	ProjectConsumerDeviceInstance(consumerID, targetID, consumerDeviceID string, item device.Device) (device.Device, error)
	ResolveConsumerWriteInstance(providerID, deviceID, targetID, consumerDeviceID, consumerID string, deviceType device.Type, endpointID, capabilityID, propertyID string, value device.PropertyValue) (device.ParameterPath, device.PropertyValue, string, bool, error)
}

type ScopedConsumerPropertyComposer interface {
	ProjectConsumerDeviceSourcesInstance(consumerID, targetID, consumerDeviceID string, targetType device.Type, items []device.Device) (device.Device, error)
}

type ModelDefinitionMapper interface {
	ResolveModelDefinition(deviceType device.Type, path device.ParameterPath, fallback device.PropertyDefinition) (device.PropertyDefinition, bool)
}

type Readiness struct {
	Ready    bool   `json:"ready"`
	Database string `json:"database"`
	Error    string `json:"error,omitempty"`
}

type subscription[T any] struct {
	queue chan T
	done  chan struct{}
}

type deviceMetrics struct {
	eventsReceived        atomic.Uint64
	eventsProcessed       atomic.Uint64
	eventsDropped         atomic.Uint64
	targetEventsDropped   atomic.Uint64
	stateEventsDropped    atomic.Uint64
	deviceEventsReceived  atomic.Uint64
	deviceEventsDropped   atomic.Uint64
	statesMarkedStale     atomic.Uint64
	commandsStarted       atomic.Uint64
	commandsConfirmed     atomic.Uint64
	commandsRejected      atomic.Uint64
	commandsTimedOut      atomic.Uint64
	commandsSuperseded    atomic.Uint64
	commandsCoalesced     atomic.Uint64
	homeKitPushes         atomic.Uint64
	providerClockSkews    atomic.Uint64
	providerEventsIgnored atomic.Uint64
	mappingApplied        atomic.Uint64
	mappingErrors         atomic.Uint64
	maxClockSkewNanos     atomic.Uint64
}

type DeviceMetrics struct {
	EventsReceived            uint64  `json:"eventsReceived"`
	EventsProcessed           uint64  `json:"eventsProcessed"`
	EventsDropped             uint64  `json:"eventsDropped"`
	EventQueuePending         int     `json:"eventQueuePending"`
	EventQueueCapacity        int     `json:"eventQueueCapacity"`
	TargetEventsDropped       uint64  `json:"targetEventsDropped"`
	StateEventsDropped        uint64  `json:"stateEventsDropped"`
	DeviceEventsReceived      uint64  `json:"deviceEventsReceived"`
	DeviceEventsDropped       uint64  `json:"deviceEventsDropped"`
	StatesMarkedStale         uint64  `json:"statesMarkedStale"`
	CommandsStarted           uint64  `json:"commandsStarted"`
	CommandsConfirmed         uint64  `json:"commandsConfirmed"`
	CommandsRejected          uint64  `json:"commandsRejected"`
	CommandsTimedOut          uint64  `json:"commandsTimedOut"`
	CommandsSuperseded        uint64  `json:"commandsSuperseded"`
	CommandsCoalesced         uint64  `json:"commandsCoalesced"`
	CommandsOutcomeUnknown    uint64  `json:"commandsOutcomeUnknown"`
	HomeKitPushes             uint64  `json:"homeKitPushes"`
	OnlineDevices             int     `json:"onlineDevices"`
	OfflineDevices            int     `json:"offlineDevices"`
	UnknownDevices            int     `json:"unknownDevices"`
	DisabledDevices           int     `json:"disabledDevices"`
	RemovedDevices            int     `json:"removedDevices"`
	ProvidersRunning          int     `json:"providersRunning"`
	ProviderRetries           int     `json:"providerRetries"`
	DeviceSubscribers         int     `json:"deviceSubscribers"`
	StateSubscribers          int     `json:"stateSubscribers"`
	CommandAverageLatencyMS   float64 `json:"commandAverageLatencyMs"`
	CommandQueuePending       int64   `json:"commandQueuePending"`
	CommandQueueMaxPending    int64   `json:"commandQueueMaxPending"`
	EventAverageLatencyMS     float64 `json:"eventAverageLatencyMs"`
	EventMaxLatencyMS         float64 `json:"eventMaxLatencyMs"`
	SlowEventHandlers         uint64  `json:"slowEventHandlers"`
	DatabaseOperations        uint64  `json:"databaseOperations"`
	DatabaseAverageLatencyMS  float64 `json:"databaseAverageLatencyMs"`
	DatabaseMaxLatencyMS      float64 `json:"databaseMaxLatencyMs"`
	ProviderClockSkewEvents   uint64  `json:"providerClockSkewEvents"`
	ProviderMaxClockSkewMS    float64 `json:"providerMaxClockSkewMs"`
	ProviderEventsIgnored     uint64  `json:"providerEventsIgnored"`
	ProviderMessagesReceived  uint64  `json:"providerMessagesReceived"`
	ProviderMessagesInvalid   uint64  `json:"providerMessagesInvalid"`
	ProviderMessagesDropped   uint64  `json:"providerMessagesDropped"`
	ProviderCommandsPublished uint64  `json:"providerCommandsPublished"`
	MappingApplied            uint64  `json:"mappingApplied"`
	MappingErrors             uint64  `json:"mappingErrors"`
	Goroutines                int     `json:"goroutines"`
	HeapAllocBytes            uint64  `json:"heapAllocBytes"`
	HeapObjects               uint64  `json:"heapObjects"`
}

func (s *DeviceService) Readiness(ctx context.Context) Readiness {
	result := Readiness{Ready: true, Database: "ok"}
	if checker, ok := s.storageMetrics.(DatabaseHealthProvider); ok {
		if err := checker.HealthCheck(ctx); err != nil {
			result.Ready, result.Database, result.Error = false, "unavailable", "database health check failed"
		}
	}
	return result
}

func (s *DeviceService) RecordHomeKitPushes(count uint64) {
	s.metrics.homeKitPushes.Add(count)
}

func (s *DeviceService) SetCommandTimeout(timeout time.Duration) { s.commands.SetTimeout(timeout) }

func (s *DeviceService) SetCommandHistoryLimit(limit int) { s.commands.SetMaxItems(limit) }

func NewDeviceService(provider providersdk.Provider, dependencies ...any) *DeviceService {
	discoverer, _ := provider.(providersdk.Discoverer)
	cataloger, _ := provider.(providersdk.SourceCataloger)
	reader, _ := provider.(providersdk.PropertyReader)
	writer, _ := provider.(providersdk.PropertyWriter)
	executor, _ := provider.(providersdk.CommandExecutor)
	service := &DeviceService{
		provider: provider, discoverer: discoverer, cataloger: cataloger, reader: reader, writer: writer, executor: executor,
		registry: registry.NewDeviceRegistry(nil),
		states:   statestore.NewStore(), commands: commandtracker.NewTracker(5 * time.Second), commandQueue: newCommandCoordinator(),
		listeners: make(map[uint64]*subscription[device.Device]), stateListeners: make(map[uint64]*subscription[domainstate.StateValue]), deviceEventListeners: make(map[uint64]*subscription[providersdk.DeviceEvent]), snapshotSeq: make(map[string]uint64), disabled: make(map[string]struct{}), propertyOps: make(map[domainstate.Key]*propertyOperation),
	}
	for _, dependency := range dependencies {
		if metrics, ok := dependency.(DatabaseMetricsProvider); ok && service.storageMetrics == nil {
			service.storageMetrics = metrics
		}
		if preferences, ok := dependency.(DevicePreferenceStore); ok && service.preferences == nil {
			service.preferences = preferences
		}
		if mapper, ok := dependency.(PropertyMapper); ok && service.propertyMapper == nil {
			service.propertyMapper = mapper
		}
	}
	items := make([]device.Device, 0)
	if discoverer != nil {
		items, _ = discoverer.DiscoverDevices(context.Background())
	}
	publicItems := make(map[string]device.Device, len(items))
	for _, item := range items {
		publicItems[item.ID] = item.Clone()
	}
	items = service.sourceSnapshots(context.Background(), items)
	for _, source := range items {
		item, err := service.mapSnapshot(source)
		if err != nil {
			// A SourceCatalog snapshot can contain every Provider-native field.
			// Never put it in the public registry when a binding or conversion is
			// invalid. Fall back to the Provider's deliberately narrow discovery
			// snapshot, which can still expose its valid built-in model mapping.
			public, exists := publicItems[source.ID]
			if !exists {
				continue
			}
			item, err = service.mapSnapshot(public)
			if err != nil {
				continue
			}
		}
		item.NormalizeAvailability()
		service.registry.Upsert(item)
		if item.IsOnline() {
			service.acceptSnapshotSequence(item)
			service.applySnapshot(item, "")
		} else {
			service.ensureUnknownStates(item, unavailableReason(item), "")
		}
	}
	if reporter, ok := provider.(providersdk.CapabilityAvailabilityReporter); ok {
		for _, availability := range reporter.CapabilityAvailabilities() {
			if availability.Available {
				continue
			}
			service.states.MarkCapabilityUnavailable(
				availability.DeviceID, availability.EndpointID, availability.CapabilityID,
				domainstate.UnavailableControlProviderOffline,
			)
		}
	}
	service.dispatcher = eventbus.NewDispatcher(8, 128, service.handleEvent)
	staleCtx, staleCancel := context.WithCancel(context.Background())
	service.staleCancel, service.staleDone = staleCancel, make(chan struct{})
	go service.runStaleScanner(staleCtx)
	if subscriber, ok := provider.(providersdk.EventSubscriber); ok {
		service.unsubscribe = subscriber.Subscribe(func(item device.Device) {
			service.metrics.eventsReceived.Add(1)
			if err := service.dispatcher.Publish(eventbus.Event{DeviceID: item.ID, Payload: item}); err != nil {
				service.metrics.eventsDropped.Add(1)
			}
		})
	} else {
		service.unsubscribe = func() {}
	}
	if subscriber, ok := provider.(providersdk.DeviceEventSubscriber); ok {
		service.unsubscribeDeviceEvents = subscriber.SubscribeDeviceEvents(service.publishDeviceEvent)
	} else {
		service.unsubscribeDeviceEvents = func() {}
	}
	if subscriber, ok := provider.(providersdk.CapabilityAvailabilitySubscriber); ok {
		service.unsubscribeCapabilityAvailability = subscriber.SubscribeCapabilityAvailability(func(availability providersdk.CapabilityAvailability) {
			if err := service.dispatcher.Publish(eventbus.Event{DeviceID: availability.DeviceID, Payload: availability}); err != nil {
				service.metrics.eventsDropped.Add(1)
			}
		})
	} else {
		service.unsubscribeCapabilityAvailability = func() {}
	}
	return service
}

func (s *DeviceService) LoadDevicePreferences(ctx context.Context) error {
	if s.preferences == nil {
		return nil
	}
	ids, err := s.preferences.ListDisabledDeviceIDs(ctx)
	if err != nil {
		return err
	}
	s.disabledMu.Lock()
	for _, id := range ids {
		s.disabled[id] = struct{}{}
	}
	s.disabledMu.Unlock()
	for _, id := range ids {
		if item, ok := s.registry.Get(id); ok {
			item.Disabled = true
			item.SetOnline(false)
			s.registry.Upsert(item)
			s.states.MarkDeviceUnavailable(id, domainstate.UnavailableDisabled)
			s.resetSnapshotSequence(id)
		}
	}
	return nil
}

func (s *DeviceService) SetDeviceEnabled(ctx context.Context, id string, enabled bool) (device.Device, error) {
	item, ok := s.registry.Get(id)
	if !ok {
		return device.Device{}, ErrDeviceNotFound
	}
	if s.preferences == nil {
		return device.Device{}, errors.New("device preferences are unavailable")
	}
	s.disabledMu.Lock()
	_, wasDisabled := s.disabled[id]
	if enabled {
		delete(s.disabled, id)
	} else {
		s.disabled[id] = struct{}{}
	}
	if err := s.preferences.SetDeviceDisabled(ctx, id, !enabled); err != nil {
		if wasDisabled {
			s.disabled[id] = struct{}{}
		} else {
			delete(s.disabled, id)
		}
		s.disabledMu.Unlock()
		return device.Device{}, err
	}
	s.disabledMu.Unlock()
	if !enabled {
		item.Disabled = true
		item.SetOnline(false)
		s.resetSnapshotSequence(id)
		s.registry.Upsert(item)
		stale := s.states.MarkDeviceUnavailable(id, domainstate.UnavailableDisabled)
		s.metrics.statesMarkedStale.Add(uint64(len(stale)))
		for _, state := range stale {
			s.publishState(state)
		}
		s.publishDevice(item)
		return item, nil
	}
	item.Disabled = false
	s.resetSnapshotSequence(id)
	if s.discoverer != nil {
		if snapshots, err := s.discoverer.DiscoverDevices(ctx); err == nil {
			for _, snapshot := range snapshots {
				if snapshot.ID == id {
					item = snapshot
					if projected, projectErr := s.projectLatestSnapshot(ctx, item); projectErr == nil {
						item = projected
					}
					item.Disabled, item.Removed = false, false
					item.NormalizeAvailability()
					break
				}
			}
		}
	}
	if item.Removed {
		item.SetOnline(false)
	}
	s.registry.Upsert(item)
	if item.IsOnline() {
		s.acceptSnapshotSequence(item)
		s.applySnapshot(item, "")
	} else {
		s.ensureUnknownStates(item, unavailableReason(item), "")
		stale := s.states.MarkDeviceUnavailable(id, unavailableReason(item))
		for _, state := range stale {
			s.publishState(state)
		}
	}
	s.publishDevice(item)
	return item, nil
}

func (s *DeviceService) isDeviceDisabled(id string) bool {
	s.disabledMu.RLock()
	_, disabled := s.disabled[id]
	s.disabledMu.RUnlock()
	return disabled
}

func (s *DeviceService) States(deviceID string) []domainstate.StateValue {
	s.states.MarkStale(time.Now().UTC())
	return s.states.Device(deviceID)
}

func (s *DeviceService) ProviderInfos() []providersdk.RuntimeInfo {
	if inspector, ok := s.provider.(providersdk.Inspector); ok {
		return inspector.ProviderInfos()
	}
	return []providersdk.RuntimeInfo{{Manifest: s.provider.Manifest(), Capabilities: s.provider.Capabilities(), Status: "running"}}
}

func (s *DeviceService) Simulate(ctx context.Context, request providersdk.SimulationRequest) (device.Device, error) {
	simulator, ok := s.provider.(providersdk.Simulator)
	if !ok {
		return device.Device{}, ErrPropertyUnsupported
	}
	item, err := simulator.Simulate(ctx, request)
	if errors.Is(err, providersdk.ErrDeviceNotFound) {
		return device.Device{}, ErrDeviceNotFound
	}
	if errors.Is(err, providersdk.ErrSimulationInvalid) {
		return device.Device{}, ErrPropertyUnsupported
	}
	if err != nil {
		return item, err
	}
	mapped, mapErr := s.projectLatestSnapshot(ctx, item)
	if mapErr != nil {
		return device.Device{}, mapErr
	}
	return mapped, nil
}

// RefreshDevices reconciles current raw provider snapshots through the latest
// mapping bindings. It is safe to call after configuration changes and does not
// restart providers or target bridges.
func (s *DeviceService) RefreshDevices(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if s.discoverer == nil {
		return nil
	}
	items, err := s.discoverer.DiscoverDevices(ctx)
	if err != nil {
		return err
	}
	for _, item := range s.sourceSnapshots(ctx, items) {
		s.resetSnapshotSequence(item.ID)
		s.handleEvent(eventbus.Event{DeviceID: item.ID, Payload: item, TraceID: CorrelationID(ctx)})
	}
	return nil
}

func (s *DeviceService) Metrics() DeviceMetrics {
	items := s.registry.List()
	online, offline, unknown, disabled, removed := 0, 0, 0, 0, 0
	for _, item := range items {
		if item.Disabled {
			disabled++
			continue
		}
		if item.Removed {
			removed++
			continue
		}
		switch item.EffectiveAvailability() {
		case device.AvailabilityOnline:
			online++
		case device.AvailabilityUnknown:
			unknown++
		default:
			offline++
		}
	}
	providersRunning, providerRetries := 0, 0
	var providerMessagesReceived, providerMessagesInvalid, providerMessagesDropped, providerCommandsPublished uint64
	for _, item := range s.ProviderInfos() {
		providerRetries += item.RetryCount
		if item.Status == "running" {
			providersRunning++
		}
		providerMessagesReceived += item.Metrics["messagesReceived"]
		providerMessagesInvalid += item.Metrics["messagesInvalid"]
		providerMessagesDropped += item.Metrics["messagesDropped"]
		providerCommandsPublished += item.Metrics["commandsPublished"]
	}
	s.mu.RLock()
	subscribers, stateSubscribers := len(s.listeners), len(s.stateListeners)
	s.mu.RUnlock()
	commands := s.commands.List()
	var latency time.Duration
	terminalCount := 0
	for _, command := range commands {
		if command.Status == domaincommand.StatusConfirmed || command.Status == domaincommand.StatusRejected || command.Status == domaincommand.StatusTimeout || command.Status == domaincommand.StatusSuperseded {
			latency += command.UpdatedAt.Sub(command.CreatedAt)
			terminalCount++
		}
	}
	averageLatency := float64(0)
	if terminalCount > 0 {
		averageLatency = float64(latency.Nanoseconds()) / float64(time.Millisecond) / float64(terminalCount)
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	eventStats := s.dispatcher.Stats()
	commandQueuePending, commandQueueMaxPending := s.commandQueue.stats()
	var databaseOperations uint64
	var databaseAverage, databaseMaximum time.Duration
	if s.storageMetrics != nil {
		databaseOperations, databaseAverage, databaseMaximum = s.storageMetrics.DatabaseOperationMetrics()
	}
	return DeviceMetrics{
		EventsReceived: s.metrics.eventsReceived.Load(), EventsProcessed: s.metrics.eventsProcessed.Load(),
		EventsDropped: s.metrics.eventsDropped.Load(), EventQueuePending: s.dispatcher.Pending(), EventQueueCapacity: s.dispatcher.Capacity(),
		TargetEventsDropped: s.metrics.targetEventsDropped.Load(), StateEventsDropped: s.metrics.stateEventsDropped.Load(), DeviceEventsReceived: s.metrics.deviceEventsReceived.Load(), DeviceEventsDropped: s.metrics.deviceEventsDropped.Load(), StatesMarkedStale: s.metrics.statesMarkedStale.Load(),
		CommandsStarted: s.metrics.commandsStarted.Load(), CommandsConfirmed: s.metrics.commandsConfirmed.Load(),
		CommandsRejected: s.metrics.commandsRejected.Load(), CommandsTimedOut: s.metrics.commandsTimedOut.Load(), CommandsSuperseded: s.metrics.commandsSuperseded.Load(), CommandsCoalesced: s.metrics.commandsCoalesced.Load(),
		CommandsOutcomeUnknown: s.metrics.commandsTimedOut.Load() + s.metrics.commandsSuperseded.Load(),
		HomeKitPushes:          s.metrics.homeKitPushes.Load(),
		OnlineDevices:          online, OfflineDevices: offline, UnknownDevices: unknown, DisabledDevices: disabled, RemovedDevices: removed, ProvidersRunning: providersRunning, ProviderRetries: providerRetries, DeviceSubscribers: subscribers, StateSubscribers: stateSubscribers,
		CommandAverageLatencyMS: averageLatency,
		CommandQueuePending:     commandQueuePending, CommandQueueMaxPending: commandQueueMaxPending,
		EventAverageLatencyMS: float64(eventStats.AverageLatency.Nanoseconds()) / float64(time.Millisecond), EventMaxLatencyMS: float64(eventStats.MaxLatency.Nanoseconds()) / float64(time.Millisecond), SlowEventHandlers: eventStats.SlowHandlers,
		DatabaseOperations: databaseOperations, DatabaseAverageLatencyMS: float64(databaseAverage.Nanoseconds()) / float64(time.Millisecond), DatabaseMaxLatencyMS: float64(databaseMaximum.Nanoseconds()) / float64(time.Millisecond),
		ProviderClockSkewEvents: s.metrics.providerClockSkews.Load(), ProviderMaxClockSkewMS: float64(s.metrics.maxClockSkewNanos.Load()) / float64(time.Millisecond), ProviderEventsIgnored: s.metrics.providerEventsIgnored.Load(),
		ProviderMessagesReceived: providerMessagesReceived, ProviderMessagesInvalid: providerMessagesInvalid, ProviderMessagesDropped: providerMessagesDropped, ProviderCommandsPublished: providerCommandsPublished,
		MappingApplied: s.metrics.mappingApplied.Load(), MappingErrors: s.metrics.mappingErrors.Load(),
		Goroutines: runtime.NumGoroutine(), HeapAllocBytes: memory.HeapAlloc, HeapObjects: memory.HeapObjects,
	}
}

func (s *DeviceService) List(ctx context.Context) ([]device.Device, error) {
	return s.registry.List(), nil
}

// ReportDeviceAvailability lets a protocol runtime feed observed reachability
// back into the unified Device without making the Camera Provider own media
// sessions. It is intentionally limited to availability and never mutates
// Provider configuration or media credentials.
func (s *DeviceService) ReportDeviceAvailability(id string, availability device.Availability) (device.Device, error) {
	if availability != device.AvailabilityOnline && availability != device.AvailabilityOffline && availability != device.AvailabilityUnknown {
		return device.Device{}, errors.New("invalid device availability")
	}
	item, ok := s.registry.Get(id)
	if !ok {
		return device.Device{}, ErrDeviceNotFound
	}
	if item.Disabled || item.Removed {
		return item, ErrDeviceDisabled
	}
	if item.EffectiveAvailability() == availability {
		return item, nil
	}
	item.SetAvailability(availability)
	item.LastUpdateAt = time.Now().UTC()
	s.registry.Upsert(item)
	switch availability {
	case device.AvailabilityOnline:
		s.applySnapshot(item, "")
	case device.AvailabilityOffline:
		stale := s.states.MarkDeviceUnavailable(id, domainstate.UnavailableDeviceOffline)
		s.metrics.statesMarkedStale.Add(uint64(len(stale)))
		for _, state := range stale {
			s.publishState(state)
		}
	}
	s.publishDevice(item)
	return item, nil
}

// ProviderCatalog returns the unprojected Provider snapshots for mapping
// configuration. It intentionally bypasses Provider → model routes so the UI
// can display every raw property address offered by the Provider.
func (s *DeviceService) ProviderCatalog(ctx context.Context) ([]providersdk.SourceCatalogDevice, error) {
	if cataloger, ok := s.provider.(providersdk.SourceCataloger); ok {
		items, err := cataloger.SourceCatalog(ctx)
		if err == nil {
			return items, nil
		}
		fallback := s.registry.List()
		if len(fallback) == 0 {
			return nil, err
		}
		return fallbackCatalog(fallback, err.Error()), nil
	}
	if s.discoverer == nil {
		return fallbackCatalog(s.registry.List(), "provider does not expose a native source catalog"), nil
	}
	items, err := s.discoverer.DiscoverDevices(ctx)
	if err != nil {
		// Existing registry data is still useful when a Provider is temporarily
		// offline; callers should only fail when no catalog can be shown.
		fallback := s.registry.List()
		if len(fallback) == 0 {
			return nil, err
		}
		return fallbackCatalog(fallback, err.Error()), nil
	}
	result := make([]providersdk.SourceCatalogDevice, 0, len(items))
	for _, item := range items {
		result = append(result, providersdk.SourceCatalogDevice{Device: item, Catalog: providersdk.SourceCatalogMetadata{Complete: true, Source: "provider-discovery", FetchedAt: time.Now().UTC(), Values: providersdk.SnapshotValueStatuses(item)}})
	}
	return result, nil
}

func fallbackCatalog(items []device.Device, reason string) []providersdk.SourceCatalogDevice {
	result := make([]providersdk.SourceCatalogDevice, 0, len(items))
	for _, item := range items {
		result = append(result, providersdk.SourceCatalogDevice{Device: item, Catalog: providersdk.SourceCatalogMetadata{Complete: false, Source: "unified-registry-fallback", Error: reason, Values: providersdk.SnapshotValueStatuses(item)}})
	}
	return result
}

func (s *DeviceService) SetPower(ctx context.Context, id string, power bool) (device.Device, error) {
	item, ok := s.registry.Get(id)
	if ok && (item.Disabled || item.Removed) {
		return device.Device{}, ErrDeviceDisabled
	}
	if !ok {
		return device.Device{}, ErrDeviceNotFound
	}
	if s.writer == nil {
		return device.Device{}, ErrPropertyUnsupported
	}
	providerPath, _, _, err := s.resolvePropertyPath(item.ProviderID, id, "main", "switch", "power", mapping.DirectionReverse)
	if err != nil {
		return device.Device{}, err
	}
	value, _, _, err := s.mapProperty(item.ProviderID, id, "main", "switch", "power", device.BoolValue(power), mapping.DirectionReverse)
	if err != nil {
		return device.Device{}, err
	}
	value = s.alignProviderEnumValue(ctx, id, providerPath, value)
	updated, err := s.writer.WriteProperty(ctx, providersdk.PropertyWriteRequest{
		DeviceID: id, EndpointID: providerPath.EndpointID, CapabilityID: providerPath.CapabilityID, PropertyID: providerPath.PropertyID, Value: value,
	})
	if err != nil {
		return device.Device{}, err
	}
	return s.projectLatestSnapshot(ctx, updated)
}

func (s *DeviceService) ReadProperty(ctx context.Context, deviceID, endpointID, capabilityID, propertyID string) (device.Property, error) {
	if item, ok := s.registry.Get(deviceID); ok && (item.Disabled || item.Removed) {
		return device.Property{}, ErrDeviceDisabled
	}
	if s.reader == nil {
		return device.Property{}, ErrPropertyUnsupported
	}
	item, ok := s.registry.Get(deviceID)
	if !ok {
		return device.Property{}, ErrDeviceNotFound
	}
	providerPath, _, _, err := s.resolvePropertyPath(item.ProviderID, deviceID, endpointID, capabilityID, propertyID, mapping.DirectionReverse)
	if err != nil {
		return device.Property{}, err
	}
	property, err := s.reader.ReadProperty(ctx, providersdk.PropertyReadRequest{
		DeviceID: deviceID, EndpointID: providerPath.EndpointID, CapabilityID: providerPath.CapabilityID, PropertyID: providerPath.PropertyID,
	})
	if err != nil {
		return device.Property{}, err
	}
	projections, err := s.providerPropertyProjections(item.ProviderID, deviceID, providerPath.EndpointID, providerPath.CapabilityID, providerPath.PropertyID, property)
	if err != nil {
		return device.Property{}, err
	}
	targetPath := device.ParameterPath{EndpointID: endpointID, CapabilityID: capabilityID, PropertyID: propertyID}
	found := false
	for _, projection := range projections {
		if projection.Path != targetPath {
			continue
		}
		property.Definition, property.Value, found = projection.Definition, projection.Value, true
		if projection.Explicit {
			break
		}
	}
	if !found {
		return device.Property{}, ErrPropertyUnsupported
	}
	property.Definition.ID = propertyID
	if resolver, ok := s.propertyMapper.(ModelDefinitionMapper); ok {
		path := device.ParameterPath{EndpointID: endpointID, CapabilityID: capabilityID, PropertyID: propertyID}
		if definition, found := resolver.ResolveModelDefinition(item.Type, path, property.Definition); found {
			property.Definition = definition
			property.Value = alignEnumValue(property.Value, definition.Enum)
		}
	}
	return property, nil
}

func (s *DeviceService) ExecutePower(ctx context.Context, id string, power bool) (device.Device, domaincommand.Command, error) {
	return s.ExecuteProperty(ctx, id, "main", "switch", "power", device.BoolValue(power))
}

func (s *DeviceService) ProjectForConsumer(consumerID string, item device.Device) (device.Device, error) {
	mapper, ok := s.propertyMapper.(ConsumerPropertyMapper)
	if !ok {
		return item, nil
	}
	return mapper.ProjectConsumerDevice(consumerID, item)
}

func (s *DeviceService) ProjectForConsumerInstance(consumerID, targetID, consumerDeviceID string, item device.Device) (device.Device, error) {
	mapper, ok := s.propertyMapper.(ScopedConsumerPropertyMapper)
	if !ok {
		return s.ProjectForConsumer(consumerID, item)
	}
	return mapper.ProjectConsumerDeviceInstance(consumerID, targetID, consumerDeviceID, item)
}

func (s *DeviceService) ProjectSourcesForConsumerInstance(consumerID, targetID, consumerDeviceID string, targetType device.Type, sourceDeviceIDs []string) (device.Device, error) {
	items := make([]device.Device, 0, len(sourceDeviceIDs))
	for _, sourceID := range sourceDeviceIDs {
		item, ok := s.registry.Get(sourceID)
		if !ok {
			return device.Device{}, fmt.Errorf("%w: %s", ErrDeviceNotFound, sourceID)
		}
		items = append(items, item)
	}
	composer, ok := s.propertyMapper.(ScopedConsumerPropertyComposer)
	if !ok {
		if len(items) == 0 {
			return device.Device{}, ErrDeviceNotFound
		}
		return s.ProjectForConsumerInstance(consumerID, targetID, consumerDeviceID, items[0])
	}
	return composer.ProjectConsumerDeviceSourcesInstance(consumerID, targetID, consumerDeviceID, targetType, items)
}

func (s *DeviceService) ExecuteConsumerProperty(ctx context.Context, consumerID, deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue) (device.Device, domaincommand.Command, error) {
	item, ok := s.registry.Get(deviceID)
	if !ok {
		return device.Device{}, domaincommand.Command{}, ErrDeviceNotFound
	}
	mapper, ok := s.propertyMapper.(ConsumerPropertyMapper)
	if !ok {
		return s.ExecuteProperty(ctx, deviceID, endpointID, capabilityID, propertyID, value)
	}
	path, mapped, _, _, err := mapper.ResolveConsumerWrite(item.ProviderID, item.ID, consumerID, item.Type, endpointID, capabilityID, propertyID, value)
	if err != nil {
		return device.Device{}, domaincommand.Command{}, err
	}
	return s.ExecuteProperty(ctx, deviceID, path.EndpointID, path.CapabilityID, path.PropertyID, mapped)
}

func (s *DeviceService) ExecuteConsumerPropertyInstance(ctx context.Context, consumerID, targetID, consumerDeviceID, deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue) (device.Device, domaincommand.Command, error) {
	item, ok := s.registry.Get(deviceID)
	if !ok {
		return device.Device{}, domaincommand.Command{}, ErrDeviceNotFound
	}
	mapper, ok := s.propertyMapper.(ScopedConsumerPropertyMapper)
	if !ok {
		return s.ExecuteConsumerProperty(ctx, consumerID, deviceID, endpointID, capabilityID, propertyID, value)
	}
	path, mapped, _, _, err := mapper.ResolveConsumerWriteInstance(item.ProviderID, item.ID, targetID, consumerDeviceID, consumerID, item.Type, endpointID, capabilityID, propertyID, value)
	if err != nil {
		return device.Device{}, domaincommand.Command{}, err
	}
	return s.ExecuteProperty(ctx, deviceID, path.EndpointID, path.CapabilityID, path.PropertyID, mapped)
}

// ExecuteConsumerPropertySourcesInstance resolves explicit mappings across all
// aggregate sources before falling back to identity routing on the primary
// source. This keeps manual mappings above automatic model-path matching.
func (s *DeviceService) ExecuteConsumerPropertySourcesInstance(ctx context.Context, consumerID, targetID, consumerDeviceID string, targetType device.Type, sourceDeviceIDs []string, endpointID, capabilityID, propertyID string, value device.PropertyValue) (device.Device, domaincommand.Command, error) {
	if len(sourceDeviceIDs) == 0 {
		return device.Device{}, domaincommand.Command{}, ErrDeviceNotFound
	}
	mapper, ok := s.propertyMapper.(ScopedConsumerPropertyMapper)
	if !ok {
		return s.ExecuteConsumerProperty(ctx, consumerID, sourceDeviceIDs[0], endpointID, capabilityID, propertyID, value)
	}
	items := make([]device.Device, 0, len(sourceDeviceIDs))
	for _, sourceID := range sourceDeviceIDs {
		item, exists := s.registry.Get(sourceID)
		if !exists {
			return device.Device{}, domaincommand.Command{}, fmt.Errorf("%w: %s", ErrDeviceNotFound, sourceID)
		}
		items = append(items, item)
		path, mapped, _, applied, err := mapper.ResolveConsumerWriteInstance(item.ProviderID, item.ID, targetID, consumerDeviceID, consumerID, targetType, endpointID, capabilityID, propertyID, value)
		if err != nil {
			return device.Device{}, domaincommand.Command{}, err
		}
		if applied {
			if _, exists := item.Property(path.EndpointID, path.CapabilityID, path.PropertyID); !exists {
				continue
			}
			return s.ExecuteProperty(ctx, item.ID, path.EndpointID, path.CapabilityID, path.PropertyID, mapped)
		}
	}
	primary := items[0]
	if primary.Type != targetType {
		return device.Device{}, domaincommand.Command{}, ErrPropertyUnsupported
	}
	return s.ExecuteProperty(ctx, primary.ID, endpointID, capabilityID, propertyID, value)
}

func (s *DeviceService) ExecuteProperty(ctx context.Context, deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue) (result device.Device, resultCommand domaincommand.Command, resultErr error) {
	if err := s.validatePropertyWrite(deviceID, endpointID, capabilityID, propertyID, value); err != nil {
		return device.Device{}, domaincommand.Command{}, err
	}
	key := domainstate.Key{DeviceID: deviceID, EndpointID: endpointID, CapabilityID: capabilityID, PropertyID: propertyID}
	operation, joined := s.beginPropertyOperation(ctx, key, value)
	if joined {
		select {
		case <-operation.done:
			return operation.item, operation.command, operation.err
		case <-ctx.Done():
			return device.Device{}, domaincommand.Command{}, ctx.Err()
		}
	}
	defer func() {
		result, resultCommand, resultErr = s.finishPropertyOperation(key, operation, result, resultCommand, resultErr)
	}()
	release, err := s.commandQueue.acquire(operation.ctx, deviceID)
	if err != nil {
		return device.Device{}, domaincommand.Command{}, err
	}
	defer release()
	previous, hadPrevious := s.states.Get(key)
	command, superseded, replayed := s.commands.BeginPropertyCorrelated(deviceID, endpointID, capabilityID, propertyID, value, CorrelationID(ctx))
	if !s.setPropertyOperationCommand(key, operation, command) {
		s.metrics.commandsStarted.Add(1)
		current, changed := s.commands.Supersede(command.ID, "replaced before provider execution")
		if changed {
			s.metrics.commandsSuperseded.Add(1)
		}
		return device.Device{}, current, ErrCommandSuperseded
	}
	if replayed {
		s.metrics.commandsCoalesced.Add(1)
		item, ok := s.registry.Get(deviceID)
		if !ok {
			return device.Device{}, command, ErrDeviceNotFound
		}
		return item, command, nil
	}
	if superseded != nil {
		s.metrics.commandsSuperseded.Add(1)
		if stale, changed := s.states.ResolveOptimistic(superseded.ID, nil); changed {
			s.publishState(stale)
		}
		previous, hadPrevious = s.states.Get(key)
	}
	s.metrics.commandsStarted.Add(1)
	s.applyOptimisticState(key, value, command, previous, hadPrevious)
	s.commands.Sent(command.ID)
	if s.writer == nil {
		return s.rejectPropertyWrite(command, previous, hadPrevious, ErrPropertyUnsupported)
	}
	registered, ok := s.registry.Get(deviceID)
	if !ok {
		return s.rejectPropertyWrite(command, previous, hadPrevious, ErrDeviceNotFound)
	}
	providerValue, _, _, mapErr := s.mapProperty(registered.ProviderID, deviceID, endpointID, capabilityID, propertyID, value, mapping.DirectionReverse)
	if mapErr != nil {
		return s.rejectPropertyWrite(command, previous, hadPrevious, mapErr)
	}
	providerPath, _, _, pathErr := s.resolvePropertyPath(registered.ProviderID, deviceID, endpointID, capabilityID, propertyID, mapping.DirectionReverse)
	if pathErr != nil {
		return s.rejectPropertyWrite(command, previous, hadPrevious, pathErr)
	}
	providerValue = s.alignProviderEnumValue(operation.ctx, deviceID, providerPath, providerValue)
	item, err := s.writer.WriteProperty(operation.ctx, providersdk.PropertyWriteRequest{
		DeviceID: deviceID, EndpointID: providerPath.EndpointID, CapabilityID: providerPath.CapabilityID, PropertyID: providerPath.PropertyID, Value: providerValue,
	})
	if err != nil {
		if current, ok := s.commands.Get(command.ID); ok && current.Status == domaincommand.StatusSuperseded {
			s.rollbackOptimistic(command.ID, previous, hadPrevious)
			return device.Device{}, current, ErrCommandSuperseded
		}
		return s.rejectPropertyWrite(command, previous, hadPrevious, err)
	}
	if current, ok := s.commands.Get(command.ID); ok && current.Status == domaincommand.StatusSuperseded {
		s.rollbackOptimistic(command.ID, previous, hadPrevious)
		return device.Device{}, current, ErrCommandSuperseded
	}
	s.commands.Accepted(command.ID)
	if mapped, mapErr := s.projectLatestSnapshot(operation.ctx, item); mapErr == nil {
		item = mapped
	}
	current, _ := s.commands.Get(command.ID)
	return item, current, nil
}

func (s *DeviceService) rejectPropertyWrite(command domaincommand.Command, previous domainstate.StateValue, hadPrevious bool, err error) (device.Device, domaincommand.Command, error) {
	s.commands.Rejected(command.ID, err)
	s.rollbackOptimistic(command.ID, previous, hadPrevious)
	s.metrics.commandsRejected.Add(1)
	current, _ := s.commands.Get(command.ID)
	return device.Device{}, current, err
}

func (s *DeviceService) beginPropertyOperation(ctx context.Context, key domainstate.Key, value device.PropertyValue) (*propertyOperation, bool) {
	s.propertyMu.Lock()
	var superseded *domaincommand.Command
	if existing := s.propertyOps[key]; existing != nil {
		if existing.expected.Equal(value) {
			existing.coalesced++
			commandID := existing.command.ID
			s.propertyMu.Unlock()
			s.metrics.commandsCoalesced.Add(1)
			if commandID != "" {
				s.commands.AddCoalesced(commandID, 1)
			}
			return existing, true
		}
		if existing.command.ID != "" {
			if current, changed := s.commands.Supersede(existing.command.ID, "replaced by a newer property write"); changed {
				superseded = &current
			}
		}
		// The terminal state must be recorded before cancellation wakes the
		// provider write; otherwise the old operation can race ahead and turn
		// a deliberate replacement into a generic context-canceled rejection.
		existing.cancel()
	}
	operationContext, cancel := context.WithCancel(ctx)
	operation := &propertyOperation{expected: value, ctx: operationContext, cancel: cancel, done: make(chan struct{})}
	s.propertyOps[key] = operation
	s.propertyMu.Unlock()
	if superseded != nil {
		s.metrics.commandsSuperseded.Add(1)
		if stale, stateChanged := s.states.ResolveOptimistic(superseded.ID, nil); stateChanged {
			s.publishState(stale)
		}
	}
	return operation, false
}

func (s *DeviceService) setPropertyOperationCommand(key domainstate.Key, operation *propertyOperation, command domaincommand.Command) bool {
	s.propertyMu.Lock()
	operation.command = command
	active := s.propertyOps[key] == operation
	coalesced := operation.coalesced
	s.propertyMu.Unlock()
	if coalesced > 0 {
		if updated, ok := s.commands.AddCoalesced(command.ID, coalesced); ok {
			s.propertyMu.Lock()
			operation.command = updated
			s.propertyMu.Unlock()
		}
	}
	return active
}

func (s *DeviceService) finishPropertyOperation(key domainstate.Key, operation *propertyOperation, item device.Device, command domaincommand.Command, err error) (device.Device, domaincommand.Command, error) {
	if command.ID != "" {
		if current, ok := s.commands.Get(command.ID); ok {
			command = current
			if current.Status == domaincommand.StatusSuperseded {
				err = ErrCommandSuperseded
			}
		}
	}
	s.propertyMu.Lock()
	operation.item, operation.command, operation.err = item, command, err
	if s.propertyOps[key] == operation {
		delete(s.propertyOps, key)
	}
	operation.cancel()
	close(operation.done)
	s.propertyMu.Unlock()
	return item, command, err
}

func (s *DeviceService) validatePropertyWrite(deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue) error {
	item, ok := s.registry.Get(deviceID)
	if !ok {
		return ErrDeviceNotFound
	}
	if item.Disabled || item.Removed {
		return ErrDeviceDisabled
	}
	property, ok := item.Property(endpointID, capabilityID, propertyID)
	if !ok || !property.Definition.Writable {
		return ErrPropertyUnsupported
	}
	property.Value = value
	if err := property.Validate(); err != nil {
		return fmt.Errorf("%w: %v", providersdk.ErrPropertyInvalid, err)
	}
	return nil
}

func (s *DeviceService) ExecuteCommand(ctx context.Context, request providersdk.CommandRequest) (device.Device, domaincommand.Command, error) {
	if len(request.IdempotencyKey) > 128 {
		return device.Device{}, domaincommand.Command{}, providersdk.ErrCommandInvalid
	}
	if err := s.validateCommand(request); err != nil {
		return device.Device{}, domaincommand.Command{}, err
	}
	release, err := s.commandQueue.acquire(ctx, request.DeviceID)
	if err != nil {
		return device.Device{}, domaincommand.Command{}, err
	}
	defer release()
	command, replayed, err := s.commands.BeginActionIdempotentCorrelated(request.DeviceID, request.EndpointID, request.CapabilityID, request.CommandID, request.Parameters, request.IdempotencyKey, CorrelationID(ctx))
	if err != nil {
		return device.Device{}, domaincommand.Command{}, err
	}
	if replayed {
		item, ok := s.registry.Get(request.DeviceID)
		if !ok {
			return device.Device{}, command, ErrDeviceNotFound
		}
		return item, command, nil
	}
	s.metrics.commandsStarted.Add(1)
	s.commands.Sent(command.ID)
	if s.executor == nil {
		return s.rejectCommand(command, providersdk.ErrCommandUnsupported)
	}
	item, err := s.executor.ExecuteCommand(ctx, request)
	if err != nil {
		return s.rejectCommand(command, err)
	}
	if mapped, mapErr := s.projectLatestSnapshot(ctx, item); mapErr == nil {
		item = mapped
	}
	s.registry.Upsert(item)
	s.commands.Confirmed(command.ID)
	s.metrics.commandsConfirmed.Add(1)
	current, _ := s.commands.Get(command.ID)
	return item, current, nil
}

func (s *DeviceService) rejectCommand(command domaincommand.Command, err error) (device.Device, domaincommand.Command, error) {
	s.commands.Rejected(command.ID, err)
	s.metrics.commandsRejected.Add(1)
	current, _ := s.commands.Get(command.ID)
	return device.Device{}, current, err
}

func (s *DeviceService) validateCommand(request providersdk.CommandRequest) error {
	item, ok := s.registry.Get(request.DeviceID)
	if !ok {
		return ErrDeviceNotFound
	}
	if item.Disabled || item.Removed {
		return ErrDeviceDisabled
	}
	for _, endpoint := range item.Endpoints {
		if endpoint.ID != request.EndpointID {
			continue
		}
		for _, capability := range endpoint.Capabilities {
			if capability.ID != request.CapabilityID {
				continue
			}
			for _, definition := range capability.Commands {
				if definition.ID != request.CommandID {
					continue
				}
				declared := make(map[string]device.CommandParameter, len(definition.Parameters))
				for _, parameter := range definition.Parameters {
					declared[parameter.ID] = parameter
					if parameter.Required {
						if _, exists := request.Parameters[parameter.ID]; !exists {
							return providersdk.ErrCommandInvalid
						}
					}
				}
				for id, value := range request.Parameters {
					parameter, exists := declared[id]
					if !exists || !valueMatchesType(value, parameter.Type) {
						return providersdk.ErrCommandInvalid
					}
				}
				return nil
			}
		}
	}
	return providersdk.ErrCommandUnsupported
}

func valueMatchesType(value device.PropertyValue, expected device.ValueType) bool {
	if value.Type != expected || !value.HasSinglePayload() {
		return false
	}
	return (expected == device.ValueTypeBool && value.Bool != nil) || (expected == device.ValueTypeInt && value.Int != nil) || (expected == device.ValueTypeNumber && value.Number != nil) || ((expected == device.ValueTypeString || expected == device.ValueTypeEnum) && value.String != nil)
}

func (s *DeviceService) applyOptimisticState(key domainstate.Key, value device.PropertyValue, command domaincommand.Command, previous domainstate.StateValue, hadPrevious bool) {
	traceID := command.CorrelationID
	if traceID == "" {
		traceID = command.ID
	}
	optimistic := domainstate.StateValue{Key: key, ProviderID: previous.ProviderID, Source: domainstate.SourceOptimistic, Quality: domainstate.QualityOptimistic, ObservedAt: command.CreatedAt, ReceivedAt: command.CreatedAt, ExpiresAt: command.Deadline, TraceID: traceID, PendingCommandID: command.ID}
	switch value.Type {
	case device.ValueTypeBool:
		if value.Bool == nil {
			return
		}
		optimistic.Value = domainstate.BoolValue(*value.Bool)
	case device.ValueTypeInt:
		if value.Int == nil {
			return
		}
		optimistic.Value = domainstate.IntValue(*value.Int)
	case device.ValueTypeNumber:
		if value.Number == nil {
			return
		}
		optimistic.Value = domainstate.NumberValue(*value.Number)
	case device.ValueTypeString:
		if value.String == nil {
			return
		}
		optimistic.Value = domainstate.StringValue(*value.String)
	case device.ValueTypeEnum:
		if value.String == nil {
			return
		}
		optimistic.Value = domainstate.EnumValue(*value.String)
	default:
		return
	}
	if !hadPrevious {
		optimistic.ProviderID = "command"
	}
	s.publishState(s.states.ApplyOptimistic(optimistic))
}

func (s *DeviceService) rollbackOptimistic(commandID string, previous domainstate.StateValue, hadPrevious bool) {
	var fallback *domainstate.StateValue
	if hadPrevious {
		fallback = &previous
	}
	if restored, changed := s.states.ResolveOptimistic(commandID, fallback); changed {
		s.publishState(restored)
	}
}

func (s *DeviceService) Commands() []domaincommand.Command { return s.commands.List() }

func (s *DeviceService) Command(id string) (domaincommand.Command, bool) { return s.commands.Get(id) }

func (s *DeviceService) SubscribeCommands(handler func(domaincommand.Command)) func() {
	return s.commands.Subscribe(handler)
}

func (s *DeviceService) Subscribe(handler func(device.Device)) func() {
	return subscribe(s, &s.nextID, s.listeners, handler)
}

func (s *DeviceService) SubscribeStates(handler func(domainstate.StateValue)) func() {
	return subscribe(s, &s.nextStateID, s.stateListeners, handler)
}

func (s *DeviceService) SubscribeDeviceEvents(handler func(providersdk.DeviceEvent)) func() {
	return subscribe(s, &s.nextDeviceEventID, s.deviceEventListeners, handler)
}

func subscribe[T any](s *DeviceService, nextID *uint64, listeners map[uint64]*subscription[T], handler func(T)) func() {
	s.mu.Lock()
	*nextID++
	id := *nextID
	sub := &subscription[T]{queue: make(chan T, 64), done: make(chan struct{})}
	listeners[id] = sub
	s.mu.Unlock()
	go func() {
		defer close(sub.done)
		for item := range sub.queue {
			handler(item)
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { removeSubscription(s, id, listeners) }) }
}

func (s *DeviceService) Close() error {
	s.unsubscribe()
	s.unsubscribeDeviceEvents()
	s.unsubscribeCapabilityAvailability()
	s.closeSubscriptions()
	s.staleCancel()
	<-s.staleDone
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.dispatcher.Close(ctx); err != nil {
		return err
	}
	return s.provider.Close(ctx)
}

func (s *DeviceService) handleEvent(event eventbus.Event) {
	if availability, ok := event.Payload.(providersdk.CapabilityAvailability); ok {
		s.handleCapabilityAvailability(availability, event.TraceID)
		return
	}
	item, ok := event.Payload.(device.Device)
	if !ok {
		return
	}
	s.metrics.eventsProcessed.Add(1)
	// Provider events are deliberately public/narrow snapshots. Pull the
	// Provider-native snapshot from SourceCatalog only inside the mapping
	// boundary so raw attributes can never escape through the event stream.
	mapped, err := s.projectLatestSnapshot(context.Background(), item)
	if err != nil {
		return
	}
	item = mapped
	if current, exists := s.registry.Get(item.ID); exists &&
		item.Type == device.TypeCamera && item.ProviderID == current.ProviderID &&
		item.EffectiveAvailability() == device.AvailabilityUnknown &&
		current.EffectiveAvailability() != device.AvailabilityUnknown &&
		!item.Disabled && !item.Removed {
		// Camera media reachability is reported independently by the media
		// runtime. A control-only Provider projection starts as unknown and
		// must not erase a newer online/offline media observation.
		item.SetAvailability(current.EffectiveAvailability())
	}
	item.NormalizeAvailability()
	if s.isDeviceDisabled(item.ID) {
		item.Disabled = true
		item.SetOnline(false)
	}
	if item.Removed {
		item.SetOnline(false)
	}
	if item.IsOnline() && !s.acceptSnapshotSequence(item) {
		s.metrics.providerEventsIgnored.Add(1)
		return
	}
	if !item.IsOnline() {
		s.resetSnapshotSequence(item.ID)
	}
	if item.Removed {
		// Removed snapshots are tombstones used to notify live consumers. Keeping
		// them in the registry makes deleted Provider mappings linger until the
		// process rebuilds the registry on restart.
		s.registry.Delete(item.ID)
	} else {
		s.registry.Upsert(item)
	}
	if item.IsOnline() {
		s.applySnapshot(item, event.TraceID)
	} else {
		s.ensureUnknownStates(item, unavailableReason(item), event.TraceID)
		stale := s.states.MarkDeviceUnavailable(item.ID, unavailableReason(item), event.TraceID)
		s.metrics.statesMarkedStale.Add(uint64(len(stale)))
		for _, value := range stale {
			s.publishState(value)
		}
	}
	if item.IsOnline() {
		for _, endpoint := range item.Endpoints {
			for _, capability := range endpoint.Capabilities {
				for _, property := range capability.Properties {
					if s.commands.Confirm(item.ID, endpoint.ID, capability.ID, property.Definition.ID, property.Value) {
						s.metrics.commandsConfirmed.Add(1)
					}
				}
			}
		}
	}
	s.publishDevice(item)
}

func (s *DeviceService) handleCapabilityAvailability(
	availability providersdk.CapabilityAvailability,
	traceID string,
) {
	if availability.Available {
		// The following projected provider snapshot is authoritative and will
		// restore these states through applySnapshot.
		return
	}
	item, exists := s.registry.Get(availability.DeviceID)
	if !exists || item.ProviderID != availability.ProviderID || item.Disabled || item.Removed {
		return
	}
	changed := s.states.MarkCapabilityUnavailable(
		availability.DeviceID, availability.EndpointID, availability.CapabilityID,
		domainstate.UnavailableControlProviderOffline, traceID,
	)
	s.metrics.statesMarkedStale.Add(uint64(len(changed)))
	for _, value := range changed {
		s.publishState(value)
	}
}

func (s *DeviceService) mapSnapshot(item device.Device) (device.Device, error) {
	if s.propertyMapper == nil {
		return item, nil
	}
	result := item
	result.Endpoints = nil
	type projectedProperty struct {
		path                                       device.ParameterPath
		property                                   device.Property
		endpointName, endpointType, capabilityType string
		explicit                                   bool
	}
	projected := make([]projectedProperty, 0)
	byTarget := make(map[string]int)
	for _, endpoint := range item.Endpoints {
		for _, capability := range endpoint.Capabilities {
			for _, sourceProperty := range capability.Properties {
				projections, err := s.providerPropertyProjections(item.ProviderID, item.ID, endpoint.ID, capability.ID, sourceProperty.Definition.ID, sourceProperty)
				if err != nil {
					return device.Device{}, err
				}
				for _, projection := range projections {
					definition := projection.Definition
					definition.ID = projection.Path.PropertyID
					if resolver, ok := s.propertyMapper.(ModelDefinitionMapper); ok {
						var modelProperty bool
						definition, modelProperty = resolver.ResolveModelDefinition(item.Type, projection.Path, definition)
						if !modelProperty {
							// Native Provider attributes stay in ProviderCatalog until an
							// explicit route places them in the unified model.
							continue
						}
					}
					property := sourceProperty
					property.Definition, property.Value = definition, alignEnumValue(projection.Value, definition.Enum)
					candidate := projectedProperty{path: projection.Path, property: property, endpointName: endpoint.Name, endpointType: endpoint.Type, capabilityType: capability.Type, explicit: projection.Explicit}
					key := projection.Path.Key()
					if index, exists := byTarget[key]; exists {
						current := projected[index]
						if current.explicit && !candidate.explicit {
							continue
						}
						if current.explicit == candidate.explicit {
							return device.Device{}, fmt.Errorf("mapping produces duplicate %s unified property %s for device %q", map[bool]string{true: "manual", false: "automatic"}[candidate.explicit], projection.Path, item.ID)
						}
						projected[index] = candidate
						continue
					}
					byTarget[key] = len(projected)
					projected = append(projected, candidate)
				}
			}
		}
	}
	for _, projection := range projected {
		target := ensureCapability(&result, projection.path, projection.endpointName, projection.endpointType, projection.capabilityType)
		target.Properties = append(target.Properties, projection.property)
	}
	// Commands are addressed by the Provider-native endpoint and capability;
	// mappings only transform properties. Retain declarations when that exact
	// capability survived projection so DeviceService can validate and route the
	// command without accidentally exposing an action on a relocated mapping.
	preserveIdentityCommands(item, &result)
	if err := result.NormalizeModelParameters(); err != nil {
		return device.Device{}, fmt.Errorf("normalize mapped device %q: %w", item.ID, err)
	}
	return result, nil
}

func preserveIdentityCommands(source device.Device, target *device.Device) {
	if target == nil {
		return
	}
	for _, sourceEndpoint := range source.Endpoints {
		for _, sourceCapability := range sourceEndpoint.Capabilities {
			if len(sourceCapability.Commands) == 0 {
				continue
			}
			var destination *device.Capability
			for endpointIndex := range target.Endpoints {
				if target.Endpoints[endpointIndex].ID != sourceEndpoint.ID {
					continue
				}
				for capabilityIndex := range target.Endpoints[endpointIndex].Capabilities {
					if target.Endpoints[endpointIndex].Capabilities[capabilityIndex].ID == sourceCapability.ID {
						destination = &target.Endpoints[endpointIndex].Capabilities[capabilityIndex]
						break
					}
				}
				break
			}
			if destination == nil {
				continue
			}
			seen := make(map[string]struct{}, len(destination.Commands))
			for _, command := range destination.Commands {
				seen[command.ID] = struct{}{}
			}
			for _, command := range sourceCapability.Commands {
				if _, exists := seen[command.ID]; exists {
					continue
				}
				destination.Commands = append(destination.Commands, command)
				seen[command.ID] = struct{}{}
			}
		}
	}
}

func (s *DeviceService) providerPropertyProjections(providerID, deviceID, endpointID, capabilityID, propertyID string, property device.Property) ([]ProviderPropertyProjection, error) {
	if s.propertyMapper == nil {
		return []ProviderPropertyProjection{{Path: device.ParameterPath{EndpointID: endpointID, CapabilityID: capabilityID, PropertyID: propertyID}, Definition: property.Definition, Value: property.Value}}, nil
	}
	if projector, ok := s.propertyMapper.(ProviderPropertyProjector); ok {
		result, err := projector.ProjectProviderProperty(providerID, deviceID, endpointID, capabilityID, propertyID, property.Definition, property.Value)
		if err != nil {
			s.metrics.mappingErrors.Add(1)
			return nil, err
		}
		for _, projection := range result {
			if projection.Explicit {
				s.metrics.mappingApplied.Add(1)
			}
		}
		return result, nil
	}
	definition, _, definitionApplied, err := s.propertyMapper.TransformPropertyDefinition(providerID, deviceID, endpointID, capabilityID, propertyID, property.Definition)
	if err != nil {
		s.metrics.mappingErrors.Add(1)
		return nil, err
	}
	value, _, valueApplied, err := s.mapProperty(providerID, deviceID, endpointID, capabilityID, propertyID, property.Value, mapping.DirectionForward)
	if err != nil {
		return nil, err
	}
	path, _, pathApplied, err := s.resolvePropertyPath(providerID, deviceID, endpointID, capabilityID, propertyID, mapping.DirectionForward)
	if err != nil {
		s.metrics.mappingErrors.Add(1)
		return nil, err
	}
	return []ProviderPropertyProjection{{Path: path, Definition: definition, Value: value, Explicit: definitionApplied || valueApplied || pathApplied}}, nil
}

// sourceSnapshots supplies the mapping engine with Provider-native fields
// while keeping those fields out of the public registry. Providers with a
// complete source catalog (for example Xiaomi MIoT) can therefore expose a
// narrow unified snapshot from DiscoverDevices without breaking explicit
// Provider -> model bindings.
func (s *DeviceService) sourceSnapshots(ctx context.Context, fallback []device.Device) []device.Device {
	if s.propertyMapper == nil || s.cataloger == nil {
		return fallback
	}
	catalog, err := s.cataloger.SourceCatalog(ctx)
	if err != nil || len(catalog) == 0 {
		return fallback
	}
	byID := make(map[string]device.Device, len(catalog))
	for _, item := range catalog {
		byID[item.ID] = item.Device.Clone()
	}
	result := make([]device.Device, 0, len(fallback))
	for _, item := range fallback {
		if source, exists := byID[item.ID]; exists {
			result = append(result, source)
		} else {
			result = append(result, item)
		}
	}
	return result
}

func (s *DeviceService) projectLatestSnapshot(ctx context.Context, fallback device.Device) (device.Device, error) {
	items := s.sourceSnapshots(ctx, []device.Device{fallback})
	if len(items) == 1 {
		fallback = items[0]
	}
	return s.mapSnapshot(fallback)
}

// alignProviderEnumValue preserves identity mappings across harmless enum
// spelling differences such as MIoT "High" and unified-model "high". A real
// semantic conversion still requires a Profile; this only accepts a unique
// case/space/hyphen-equivalent source option.
func (s *DeviceService) alignProviderEnumValue(ctx context.Context, deviceID string, path device.ParameterPath, value device.PropertyValue) device.PropertyValue {
	if value.Type != device.ValueTypeEnum || value.String == nil || s.cataloger == nil {
		return value
	}
	catalog, err := s.cataloger.SourceCatalog(ctx)
	if err != nil {
		return value
	}
	for _, item := range catalog {
		if item.ID != deviceID {
			continue
		}
		property, found := item.Property(path.EndpointID, path.CapabilityID, path.PropertyID)
		if found {
			return alignEnumValue(value, property.Definition.Enum)
		}
	}
	return value
}

func alignEnumValue(value device.PropertyValue, options []string) device.PropertyValue {
	if value.Type != device.ValueTypeEnum || value.String == nil || len(options) == 0 {
		return value
	}
	for _, option := range options {
		if option == *value.String {
			return value
		}
	}
	canonical := canonicalEnumToken(*value.String)
	match := ""
	for _, option := range options {
		if canonicalEnumToken(option) != canonical {
			continue
		}
		if match != "" {
			return value
		}
		match = option
	}
	if match == "" {
		return value
	}
	return device.EnumValue(match)
}

func canonicalEnumToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("_", "-", " ", "-").Replace(value)
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	return strings.Trim(value, "-")
}

func ensureCapability(item *device.Device, path device.ParameterPath, endpointName, endpointType, capabilityType string) *device.Capability {
	for endpointIndex := range item.Endpoints {
		if item.Endpoints[endpointIndex].ID != path.EndpointID {
			continue
		}
		for capabilityIndex := range item.Endpoints[endpointIndex].Capabilities {
			if item.Endpoints[endpointIndex].Capabilities[capabilityIndex].ID == path.CapabilityID {
				return &item.Endpoints[endpointIndex].Capabilities[capabilityIndex]
			}
		}
		item.Endpoints[endpointIndex].Capabilities = append(item.Endpoints[endpointIndex].Capabilities, device.Capability{ID: path.CapabilityID, Type: capabilityType})
		return &item.Endpoints[endpointIndex].Capabilities[len(item.Endpoints[endpointIndex].Capabilities)-1]
	}
	if endpointName == "" || path.EndpointID != "main" {
		endpointName = path.EndpointID
	}
	if endpointType == "" || path.EndpointID != "main" {
		endpointType = path.EndpointID
	}
	if capabilityType == "" || path.CapabilityID == "" {
		capabilityType = path.CapabilityID
	}
	item.Endpoints = append(item.Endpoints, device.Endpoint{ID: path.EndpointID, Name: endpointName, Type: endpointType, Capabilities: []device.Capability{{ID: path.CapabilityID, Type: capabilityType}}})
	return &item.Endpoints[len(item.Endpoints)-1].Capabilities[0]
}

func (s *DeviceService) mapProperty(providerID, deviceID, endpointID, capabilityID, propertyID string, value device.PropertyValue, direction mapping.Direction) (device.PropertyValue, string, bool, error) {
	if s.propertyMapper == nil {
		return value, "", false, nil
	}
	mapped, bindingID, applied, err := s.propertyMapper.TransformProperty(providerID, deviceID, endpointID, capabilityID, propertyID, value, direction)
	if err != nil {
		s.metrics.mappingErrors.Add(1)
		return device.PropertyValue{}, bindingID, applied, err
	}
	if applied {
		s.metrics.mappingApplied.Add(1)
	}
	return mapped, bindingID, applied, nil
}

func (s *DeviceService) resolvePropertyPath(providerID, deviceID, endpointID, capabilityID, propertyID string, direction mapping.Direction) (device.ParameterPath, string, bool, error) {
	identity := device.ParameterPath{EndpointID: endpointID, CapabilityID: capabilityID, PropertyID: propertyID}
	mapper, ok := s.propertyMapper.(PropertyPathMapper)
	if !ok {
		return identity, "", false, nil
	}
	return mapper.ResolvePropertyPath(providerID, deviceID, endpointID, capabilityID, propertyID, direction)
}

func (s *DeviceService) publishDevice(item device.Device) {
	s.mu.RLock()
	for _, subscription := range s.listeners {
		select {
		case subscription.queue <- item:
		default:
			s.metrics.targetEventsDropped.Add(1)
		}
	}
	s.mu.RUnlock()
}

func (s *DeviceService) acceptSnapshotSequence(item device.Device) bool {
	if item.Sequence == 0 {
		return true
	}
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()
	current := s.snapshotSeq[item.ID]
	if current > 0 && item.Sequence <= current {
		return false
	}
	s.snapshotSeq[item.ID] = item.Sequence
	return true
}

func (s *DeviceService) resetSnapshotSequence(deviceID string) {
	s.snapshotMu.Lock()
	delete(s.snapshotSeq, deviceID)
	s.snapshotMu.Unlock()
}

func (s *DeviceService) applySnapshot(item device.Device, traceID string) {
	receivedAt := time.Now().UTC()
	observedAt := s.safeObservedAt(item.LastUpdateAt, receivedAt)
	for _, endpoint := range item.Endpoints {
		for _, capability := range endpoint.Capabilities {
			for _, property := range capability.Properties {
				source, quality := domainstate.SourceReported, domainstate.QualityReported
				transport := property.StateTransport
				if transport == "" {
					transport = item.StateTransport
				}
				if transport == device.StateTransportCloudHTTP {
					source, quality = domainstate.SourcePolled, domainstate.QualityPolled
				}
				value := domainstate.StateValue{
					Key:        domainstate.Key{DeviceID: item.ID, EndpointID: endpoint.ID, CapabilityID: capability.ID, PropertyID: property.Definition.ID},
					ProviderID: item.ProviderID, Source: source,
					ObservedAt: observedAt, ReceivedAt: receivedAt, Sequence: item.Sequence, Quality: quality, TraceID: traceID,
				}
				if property.Definition.StaleAfterSeconds > 0 {
					value.ExpiresAt = receivedAt.Add(time.Duration(property.Definition.StaleAfterSeconds) * time.Second)
				}
				switch property.Value.Type {
				case device.ValueTypeBool:
					if property.Value.Bool == nil {
						continue
					}
					value.Value = domainstate.BoolValue(*property.Value.Bool)
				case device.ValueTypeInt:
					if property.Value.Int == nil {
						continue
					}
					value.Value = domainstate.IntValue(*property.Value.Int)
				case device.ValueTypeNumber:
					if property.Value.Number == nil {
						continue
					}
					value.Value = domainstate.NumberValue(*property.Value.Number)
				case device.ValueTypeString:
					if property.Value.String == nil {
						continue
					}
					value.Value = domainstate.StringValue(*property.Value.String)
				case device.ValueTypeEnum:
					if property.Value.String == nil {
						continue
					}
					value.Value = domainstate.EnumValue(*property.Value.String)
				default:
					continue
				}
				if applied, changed := s.states.Apply(value); changed {
					s.publishState(applied)
				}
			}
		}
	}
}

func (s *DeviceService) ensureUnknownStates(item device.Device, reason domainstate.UnavailableReason, traceID string) {
	now := time.Now().UTC()
	for _, endpoint := range item.Endpoints {
		for _, capability := range endpoint.Capabilities {
			for _, property := range capability.Properties {
				value := domainstate.StateValue{
					Key:        domainstate.Key{DeviceID: item.ID, EndpointID: endpoint.ID, CapabilityID: capability.ID, PropertyID: property.Definition.ID},
					ProviderID: item.ProviderID, Source: domainstate.SourceUnknown, Quality: domainstate.QualityUnknown,
					ObservedAt: now, ReceivedAt: now, UnavailableReason: reason, TraceID: traceID,
				}
				if created, changed := s.states.EnsureUnknown(value); changed {
					s.publishState(created)
				}
			}
		}
	}
}

func unavailableReason(item device.Device) domainstate.UnavailableReason {
	if item.Removed {
		return domainstate.UnavailableRemoved
	}
	if item.Disabled {
		return domainstate.UnavailableDisabled
	}
	if item.EffectiveAvailability() == device.AvailabilityUnknown {
		return domainstate.UnavailableAvailabilityUnknown
	}
	return domainstate.UnavailableDeviceOffline
}

func (s *DeviceService) safeObservedAt(observedAt, receivedAt time.Time) time.Time {
	if observedAt.IsZero() {
		return receivedAt
	}
	skew := receivedAt.Sub(observedAt)
	if skew < 0 {
		skew = -skew
	}
	if skew <= 5*time.Minute {
		return observedAt
	}
	s.metrics.providerClockSkews.Add(1)
	nanos := uint64(skew)
	for {
		current := s.metrics.maxClockSkewNanos.Load()
		if nanos <= current || s.metrics.maxClockSkewNanos.CompareAndSwap(current, nanos) {
			break
		}
	}
	return receivedAt
}

func (s *DeviceService) runStaleScanner(ctx context.Context) {
	defer close(s.staleDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			stale := s.states.MarkStale(now.UTC())
			s.metrics.statesMarkedStale.Add(uint64(len(stale)))
			for _, value := range stale {
				s.publishState(value)
			}
			expired := s.commands.Expire(now.UTC())
			s.metrics.commandsTimedOut.Add(uint64(len(expired)))
		case <-ctx.Done():
			return
		}
	}
}

func removeSubscription[T any](s *DeviceService, id uint64, listeners map[uint64]*subscription[T]) {
	s.mu.Lock()
	subscription, ok := listeners[id]
	if ok {
		delete(listeners, id)
		close(subscription.queue)
	}
	s.mu.Unlock()
	if ok {
		<-subscription.done
	}
}

func (s *DeviceService) publishDeviceEvent(event providersdk.DeviceEvent) {
	s.metrics.deviceEventsReceived.Add(1)
	if _, exists := s.registry.Get(event.DeviceID); !exists || s.isDeviceDisabled(event.DeviceID) {
		return
	}
	s.mu.RLock()
	for _, subscription := range s.deviceEventListeners {
		copy := event
		copy.Payload = append([]byte(nil), event.Payload...)
		select {
		case subscription.queue <- copy:
		default:
			s.metrics.deviceEventsDropped.Add(1)
		}
	}
	s.mu.RUnlock()
}

func (s *DeviceService) publishState(value domainstate.StateValue) {
	s.mu.RLock()
	for _, subscription := range s.stateListeners {
		select {
		case subscription.queue <- value:
		default:
			s.metrics.stateEventsDropped.Add(1)
		}
	}
	s.mu.RUnlock()
}

func (s *DeviceService) closeSubscriptions() {
	s.mu.Lock()
	subscriptions := make([]*subscription[device.Device], 0, len(s.listeners))
	for id, subscription := range s.listeners {
		delete(s.listeners, id)
		close(subscription.queue)
		subscriptions = append(subscriptions, subscription)
	}
	stateSubscriptions := make([]*subscription[domainstate.StateValue], 0, len(s.stateListeners))
	for id, subscription := range s.stateListeners {
		delete(s.stateListeners, id)
		close(subscription.queue)
		stateSubscriptions = append(stateSubscriptions, subscription)
	}
	deviceEventSubscriptions := make([]*subscription[providersdk.DeviceEvent], 0, len(s.deviceEventListeners))
	for id, subscription := range s.deviceEventListeners {
		delete(s.deviceEventListeners, id)
		close(subscription.queue)
		deviceEventSubscriptions = append(deviceEventSubscriptions, subscription)
	}
	s.mu.Unlock()
	for _, subscription := range subscriptions {
		<-subscription.done
	}
	for _, subscription := range stateSubscriptions {
		<-subscription.done
	}
	for _, subscription := range deviceEventSubscriptions {
		<-subscription.done
	}
}
