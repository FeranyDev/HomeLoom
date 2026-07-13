package application

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	commandtracker "github.com/feranydev/homeloom/backend/internal/command"
	domaincommand "github.com/feranydev/homeloom/backend/internal/domain/command"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domainstate "github.com/feranydev/homeloom/backend/internal/domain/state"
	"github.com/feranydev/homeloom/backend/internal/eventbus"
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
	provider       providersdk.Provider
	discoverer     providersdk.Discoverer
	reader         providersdk.PropertyReader
	writer         providersdk.PropertyWriter
	executor       providersdk.CommandExecutor
	registry       *registry.DeviceRegistry
	dispatcher     *eventbus.Dispatcher
	states         *statestore.Store
	commands       *commandtracker.Tracker
	commandQueue   *commandCoordinator
	unsubscribe    func()
	mu             sync.RWMutex
	nextID         uint64
	listeners      map[uint64]*deviceSubscription
	nextStateID    uint64
	stateListeners map[uint64]*stateSubscription
	snapshotMu     sync.Mutex
	snapshotSeq    map[string]uint64
	staleCancel    context.CancelFunc
	staleDone      chan struct{}
	metrics        deviceMetrics
	storageMetrics DatabaseMetricsProvider
	preferences    DevicePreferenceStore
	disabledMu     sync.RWMutex
	disabled       map[string]struct{}
	propertyMu     sync.Mutex
	propertyOps    map[domainstate.Key]*propertyOperation
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

type Readiness struct {
	Ready    bool   `json:"ready"`
	Database string `json:"database"`
	Error    string `json:"error,omitempty"`
}

type deviceSubscription struct {
	queue chan device.Device
	done  chan struct{}
}

type stateSubscription struct {
	queue chan domainstate.StateValue
	done  chan struct{}
}

type deviceMetrics struct {
	eventsReceived        atomic.Uint64
	eventsProcessed       atomic.Uint64
	eventsDropped         atomic.Uint64
	targetEventsDropped   atomic.Uint64
	stateEventsDropped    atomic.Uint64
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
	maxClockSkewNanos     atomic.Uint64
}

type DeviceMetrics struct {
	EventsReceived           uint64  `json:"eventsReceived"`
	EventsProcessed          uint64  `json:"eventsProcessed"`
	EventsDropped            uint64  `json:"eventsDropped"`
	EventQueuePending        int     `json:"eventQueuePending"`
	EventQueueCapacity       int     `json:"eventQueueCapacity"`
	TargetEventsDropped      uint64  `json:"targetEventsDropped"`
	StateEventsDropped       uint64  `json:"stateEventsDropped"`
	StatesMarkedStale        uint64  `json:"statesMarkedStale"`
	CommandsStarted          uint64  `json:"commandsStarted"`
	CommandsConfirmed        uint64  `json:"commandsConfirmed"`
	CommandsRejected         uint64  `json:"commandsRejected"`
	CommandsTimedOut         uint64  `json:"commandsTimedOut"`
	CommandsSuperseded       uint64  `json:"commandsSuperseded"`
	CommandsCoalesced        uint64  `json:"commandsCoalesced"`
	CommandsOutcomeUnknown   uint64  `json:"commandsOutcomeUnknown"`
	HomeKitPushes            uint64  `json:"homeKitPushes"`
	OnlineDevices            int     `json:"onlineDevices"`
	OfflineDevices           int     `json:"offlineDevices"`
	UnknownDevices           int     `json:"unknownDevices"`
	DisabledDevices          int     `json:"disabledDevices"`
	RemovedDevices           int     `json:"removedDevices"`
	ProvidersRunning         int     `json:"providersRunning"`
	ProviderRetries          int     `json:"providerRetries"`
	DeviceSubscribers        int     `json:"deviceSubscribers"`
	StateSubscribers         int     `json:"stateSubscribers"`
	CommandAverageLatencyMS  float64 `json:"commandAverageLatencyMs"`
	CommandQueuePending      int64   `json:"commandQueuePending"`
	CommandQueueMaxPending   int64   `json:"commandQueueMaxPending"`
	EventAverageLatencyMS    float64 `json:"eventAverageLatencyMs"`
	EventMaxLatencyMS        float64 `json:"eventMaxLatencyMs"`
	SlowEventHandlers        uint64  `json:"slowEventHandlers"`
	DatabaseOperations       uint64  `json:"databaseOperations"`
	DatabaseAverageLatencyMS float64 `json:"databaseAverageLatencyMs"`
	DatabaseMaxLatencyMS     float64 `json:"databaseMaxLatencyMs"`
	ProviderClockSkewEvents  uint64  `json:"providerClockSkewEvents"`
	ProviderMaxClockSkewMS   float64 `json:"providerMaxClockSkewMs"`
	ProviderEventsIgnored    uint64  `json:"providerEventsIgnored"`
	Goroutines               int     `json:"goroutines"`
	HeapAllocBytes           uint64  `json:"heapAllocBytes"`
	HeapObjects              uint64  `json:"heapObjects"`
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

func NewDeviceService(provider providersdk.Provider, storageMetrics ...DatabaseMetricsProvider) *DeviceService {
	discoverer, _ := provider.(providersdk.Discoverer)
	reader, _ := provider.(providersdk.PropertyReader)
	writer, _ := provider.(providersdk.PropertyWriter)
	executor, _ := provider.(providersdk.CommandExecutor)
	items := make([]device.Device, 0)
	if discoverer != nil {
		items, _ = discoverer.DiscoverDevices(context.Background())
	}
	service := &DeviceService{
		provider: provider, discoverer: discoverer, reader: reader, writer: writer, executor: executor,
		registry: registry.NewDeviceRegistry(items),
		states:   statestore.NewStore(), commands: commandtracker.NewTracker(5 * time.Second), commandQueue: newCommandCoordinator(),
		listeners: make(map[uint64]*deviceSubscription), stateListeners: make(map[uint64]*stateSubscription), snapshotSeq: make(map[string]uint64), disabled: make(map[string]struct{}), propertyOps: make(map[domainstate.Key]*propertyOperation),
	}
	if len(storageMetrics) > 0 {
		service.storageMetrics = storageMetrics[0]
		service.preferences, _ = any(storageMetrics[0]).(DevicePreferenceStore)
	}
	for _, item := range items {
		item.NormalizeAvailability()
		if item.IsOnline() {
			service.acceptSnapshotSequence(item)
			service.applySnapshot(item, "")
		} else {
			service.ensureUnknownStates(item, unavailableReason(item), "")
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
	return item, err
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
	for _, item := range s.ProviderInfos() {
		providerRetries += item.RetryCount
		if item.Status == "running" {
			providersRunning++
		}
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
		TargetEventsDropped: s.metrics.targetEventsDropped.Load(), StateEventsDropped: s.metrics.stateEventsDropped.Load(), StatesMarkedStale: s.metrics.statesMarkedStale.Load(),
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
		Goroutines: runtime.NumGoroutine(), HeapAllocBytes: memory.HeapAlloc, HeapObjects: memory.HeapObjects,
	}
}

func (s *DeviceService) List(ctx context.Context) ([]device.Device, error) {
	return s.registry.List(), nil
}

func (s *DeviceService) SetPower(ctx context.Context, id string, power bool) (device.Device, error) {
	if item, ok := s.registry.Get(id); ok && (item.Disabled || item.Removed) {
		return device.Device{}, ErrDeviceDisabled
	}
	if s.writer == nil {
		return device.Device{}, ErrPropertyUnsupported
	}
	return s.writer.WriteProperty(ctx, providersdk.PropertyWriteRequest{
		DeviceID: id, EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(power),
	})
}

func (s *DeviceService) ReadProperty(ctx context.Context, deviceID, endpointID, capabilityID, propertyID string) (device.Property, error) {
	if item, ok := s.registry.Get(deviceID); ok && (item.Disabled || item.Removed) {
		return device.Property{}, ErrDeviceDisabled
	}
	if s.reader == nil {
		return device.Property{}, ErrPropertyUnsupported
	}
	return s.reader.ReadProperty(ctx, providersdk.PropertyReadRequest{
		DeviceID: deviceID, EndpointID: endpointID, CapabilityID: capabilityID, PropertyID: propertyID,
	})
}

func (s *DeviceService) ExecutePower(ctx context.Context, id string, power bool) (device.Device, domaincommand.Command, error) {
	return s.ExecuteProperty(ctx, id, "main", "switch", "power", device.BoolValue(power))
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
		err := ErrPropertyUnsupported
		s.commands.Rejected(command.ID, err)
		s.rollbackOptimistic(command.ID, previous, hadPrevious)
		s.metrics.commandsRejected.Add(1)
		current, _ := s.commands.Get(command.ID)
		return device.Device{}, current, err
	}
	item, err := s.writer.WriteProperty(operation.ctx, providersdk.PropertyWriteRequest{
		DeviceID: deviceID, EndpointID: endpointID, CapabilityID: capabilityID, PropertyID: propertyID, Value: value,
	})
	if err != nil {
		if current, ok := s.commands.Get(command.ID); ok && current.Status == domaincommand.StatusSuperseded {
			s.rollbackOptimistic(command.ID, previous, hadPrevious)
			return device.Device{}, current, ErrCommandSuperseded
		}
		s.commands.Rejected(command.ID, err)
		s.rollbackOptimistic(command.ID, previous, hadPrevious)
		s.metrics.commandsRejected.Add(1)
		current, _ := s.commands.Get(command.ID)
		return device.Device{}, current, err
	}
	if current, ok := s.commands.Get(command.ID); ok && current.Status == domaincommand.StatusSuperseded {
		s.rollbackOptimistic(command.ID, previous, hadPrevious)
		return device.Device{}, current, ErrCommandSuperseded
	}
	s.commands.Accepted(command.ID)
	current, _ := s.commands.Get(command.ID)
	return item, current, nil
}

func (s *DeviceService) beginPropertyOperation(ctx context.Context, key domainstate.Key, value device.PropertyValue) (*propertyOperation, bool) {
	s.propertyMu.Lock()
	var supersededCommandID string
	if existing := s.propertyOps[key]; existing != nil {
		if propertyValuesEqual(existing.expected, value) {
			existing.coalesced++
			commandID := existing.command.ID
			s.propertyMu.Unlock()
			s.metrics.commandsCoalesced.Add(1)
			if commandID != "" {
				s.commands.AddCoalesced(commandID, 1)
			}
			return existing, true
		}
		supersededCommandID = existing.command.ID
		existing.cancel()
	}
	operationContext, cancel := context.WithCancel(ctx)
	operation := &propertyOperation{expected: value, ctx: operationContext, cancel: cancel, done: make(chan struct{})}
	s.propertyOps[key] = operation
	s.propertyMu.Unlock()
	if supersededCommandID != "" {
		if superseded, changed := s.commands.Supersede(supersededCommandID, "replaced by a newer property write"); changed {
			s.metrics.commandsSuperseded.Add(1)
			if stale, stateChanged := s.states.ResolveOptimistic(superseded.ID, nil); stateChanged {
				s.publishState(stale)
			}
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

func propertyValuesEqual(left, right device.PropertyValue) bool {
	if left.Type != right.Type {
		return false
	}
	switch left.Type {
	case device.ValueTypeBool:
		return left.Bool != nil && right.Bool != nil && *left.Bool == *right.Bool
	case device.ValueTypeInt:
		return left.Int != nil && right.Int != nil && *left.Int == *right.Int
	case device.ValueTypeNumber:
		return left.Number != nil && right.Number != nil && *left.Number == *right.Number
	case device.ValueTypeString, device.ValueTypeEnum:
		return left.String != nil && right.String != nil && *left.String == *right.String
	default:
		return false
	}
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
		err := providersdk.ErrCommandUnsupported
		s.commands.Rejected(command.ID, err)
		s.metrics.commandsRejected.Add(1)
		current, _ := s.commands.Get(command.ID)
		return device.Device{}, current, err
	}
	item, err := s.executor.ExecuteCommand(ctx, request)
	if err != nil {
		s.commands.Rejected(command.ID, err)
		s.metrics.commandsRejected.Add(1)
		current, _ := s.commands.Get(command.ID)
		return device.Device{}, current, err
	}
	s.registry.Upsert(item)
	s.commands.Confirmed(command.ID)
	s.metrics.commandsConfirmed.Add(1)
	current, _ := s.commands.Get(command.ID)
	return item, current, nil
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
	if value.Type != expected {
		return false
	}
	payloads := 0
	if value.Bool != nil {
		payloads++
	}
	if value.Int != nil {
		payloads++
	}
	if value.Number != nil {
		payloads++
	}
	if value.String != nil {
		payloads++
	}
	if payloads != 1 {
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
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	subscription := &deviceSubscription{queue: make(chan device.Device, 64), done: make(chan struct{})}
	s.listeners[id] = subscription
	s.mu.Unlock()
	go func() {
		defer close(subscription.done)
		for item := range subscription.queue {
			handler(item)
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { s.removeSubscription(id) }) }
}

func (s *DeviceService) SubscribeStates(handler func(domainstate.StateValue)) func() {
	s.mu.Lock()
	s.nextStateID++
	id := s.nextStateID
	subscription := &stateSubscription{queue: make(chan domainstate.StateValue, 64), done: make(chan struct{})}
	s.stateListeners[id] = subscription
	s.mu.Unlock()
	go func() {
		defer close(subscription.done)
		for item := range subscription.queue {
			handler(item)
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { s.removeStateSubscription(id) }) }
}

func (s *DeviceService) Close() error {
	s.unsubscribe()
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
	item, ok := event.Payload.(device.Device)
	if !ok {
		return
	}
	s.metrics.eventsProcessed.Add(1)
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
	s.registry.Upsert(item)
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
				value := domainstate.StateValue{
					Key:        domainstate.Key{DeviceID: item.ID, EndpointID: endpoint.ID, CapabilityID: capability.ID, PropertyID: property.Definition.ID},
					ProviderID: item.ProviderID, Source: domainstate.SourceReported,
					ObservedAt: observedAt, ReceivedAt: receivedAt, Sequence: item.Sequence, Quality: domainstate.QualityReported, TraceID: traceID,
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

func (s *DeviceService) removeSubscription(id uint64) {
	s.mu.Lock()
	subscription, ok := s.listeners[id]
	if ok {
		delete(s.listeners, id)
		close(subscription.queue)
	}
	s.mu.Unlock()
	if ok {
		<-subscription.done
	}
}

func (s *DeviceService) removeStateSubscription(id uint64) {
	s.mu.Lock()
	subscription, ok := s.stateListeners[id]
	if ok {
		delete(s.stateListeners, id)
		close(subscription.queue)
	}
	s.mu.Unlock()
	if ok {
		<-subscription.done
	}
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
	subscriptions := make([]*deviceSubscription, 0, len(s.listeners))
	for id, subscription := range s.listeners {
		delete(s.listeners, id)
		close(subscription.queue)
		subscriptions = append(subscriptions, subscription)
	}
	stateSubscriptions := make([]*stateSubscription, 0, len(s.stateListeners))
	for id, subscription := range s.stateListeners {
		delete(s.stateListeners, id)
		close(subscription.queue)
		stateSubscriptions = append(stateSubscriptions, subscription)
	}
	s.mu.Unlock()
	for _, subscription := range subscriptions {
		<-subscription.done
	}
	for _, subscription := range stateSubscriptions {
		<-subscription.done
	}
}
