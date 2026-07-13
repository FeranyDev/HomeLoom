package mqtt

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

const inboundQueueCapacity = 1024

type Provider struct {
	id               string
	name             string
	config           Config
	brokerURL        *url.URL
	tlsConfig        *tls.Config
	transportFactory transportFactory

	mu              sync.RWMutex
	devices         map[string]device.Device
	remoteSequences map[string]uint64
	nextSequence    uint64
	listeners       map[uint64]func(device.Device)
	nextListener    uint64
	transport       mqttTransport
	cancel          context.CancelFunc
	workerDone      chan struct{}
	running         bool
	connected       bool
	lastError       string
	inbound         chan inboundMessage

	messagesReceived  atomic.Uint64
	messagesInvalid   atomic.Uint64
	messagesDropped   atomic.Uint64
	commandsPublished atomic.Uint64
}

type Stats struct {
	MessagesReceived  uint64 `json:"messagesReceived"`
	MessagesInvalid   uint64 `json:"messagesInvalid"`
	MessagesDropped   uint64 `json:"messagesDropped"`
	CommandsPublished uint64 `json:"commandsPublished"`
}

var (
	_ providersdk.Provider        = (*Provider)(nil)
	_ providersdk.Discoverer      = (*Provider)(nil)
	_ providersdk.PropertyReader  = (*Provider)(nil)
	_ providersdk.PropertyWriter  = (*Provider)(nil)
	_ providersdk.CommandExecutor = (*Provider)(nil)
	_ providersdk.EventSubscriber = (*Provider)(nil)
	_ providersdk.MetricsReporter = (*Provider)(nil)
)

func NewProviderFromConfig(item providerconfig.Config) (*Provider, error) {
	return newProviderFromConfig(item, newPahoTransport)
}

func newProviderFromConfig(item providerconfig.Config, factory transportFactory) (*Provider, error) {
	config, brokerURL, tlsConfig, err := decodeConfig(item)
	if err != nil {
		return nil, err
	}
	return &Provider{
		id: item.ID, name: item.Name, config: config, brokerURL: brokerURL, tlsConfig: tlsConfig, transportFactory: factory,
		devices: make(map[string]device.Device), remoteSequences: make(map[string]uint64), listeners: make(map[uint64]func(device.Device)), inbound: make(chan inboundMessage, inboundQueueCapacity),
	}, nil
}

func (p *Provider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "mqtt", Name: p.name, Version: "0.1.0"}
}

func (p *Provider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyRead: true, PropertyWrite: true, Commands: true, Events: true}
}

func (p *Provider) Initialize(ctx context.Context) error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return nil
	}
	for {
		select {
		case <-p.inbound:
		default:
			goto queueDrained
		}
	}
queueDrained:
	lifecycle, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	handlers := transportHandlers{
		onMessage: p.enqueue,
		onConnectionUp: func() {
			p.mu.Lock()
			p.connected, p.lastError = true, ""
			p.mu.Unlock()
		},
		onConnectionDown: p.markConnectionDown,
		onError: func(err error) {
			p.mu.Lock()
			p.lastError = err.Error()
			p.mu.Unlock()
		},
	}
	transport := p.transportFactory(p.config, p.brokerURL, p.tlsConfig, handlers)
	p.transport, p.cancel, p.workerDone = transport, cancel, done
	p.mu.Unlock()
	go p.run(lifecycle, done)
	if err := transport.Start(lifecycle, ctx, p.config.connectTimeout()); err != nil {
		cancel()
		<-done
		closeContext, closeCancel := context.WithTimeout(context.Background(), p.config.connectTimeout())
		_ = transport.Close(closeContext)
		closeCancel()
		p.mu.Lock()
		if p.transport == transport {
			p.transport, p.cancel, p.workerDone, p.running, p.connected, p.lastError = nil, nil, nil, false, false, err.Error()
		}
		p.mu.Unlock()
		return fmt.Errorf("connect mqtt provider %q: %w", p.id, err)
	}
	p.mu.Lock()
	if p.transport == transport {
		p.running = true
	}
	p.mu.Unlock()
	return nil
}

func (p *Provider) Close(ctx context.Context) error {
	p.mu.Lock()
	transport, cancel, done := p.transport, p.cancel, p.workerDone
	p.transport, p.cancel, p.workerDone, p.running, p.connected = nil, nil, nil, false, false
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	var closeErr error
	if transport != nil {
		closeErr = transport.Close(ctx)
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			if closeErr == nil {
				closeErr = ctx.Err()
			}
		}
	}
	return closeErr
}

func (p *Provider) DiscoverDevices(context.Context) ([]device.Device, error) {
	p.mu.RLock()
	result := make([]device.Device, 0, len(p.devices))
	for _, item := range p.devices {
		result = append(result, item.Clone())
	}
	p.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (p *Provider) ReadProperty(_ context.Context, request providersdk.PropertyReadRequest) (device.Property, error) {
	p.mu.RLock()
	item, exists := p.devices[request.DeviceID]
	connected := p.connected
	p.mu.RUnlock()
	if !exists {
		return device.Property{}, providersdk.ErrDeviceNotFound
	}
	if !connected || !item.IsOnline() {
		return device.Property{}, providersdk.ErrProviderUnavailable
	}
	property, exists := item.Property(request.EndpointID, request.CapabilityID, request.PropertyID)
	if !exists || !property.Definition.Readable {
		return device.Property{}, providersdk.ErrPropertyUnsupported
	}
	return property, nil
}

func (p *Provider) WriteProperty(ctx context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	p.mu.RLock()
	item, exists := p.devices[request.DeviceID]
	connected := p.connected
	p.mu.RUnlock()
	if !exists {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	if !connected || !item.IsOnline() {
		return device.Device{}, providersdk.ErrProviderUnavailable
	}
	property, exists := item.Property(request.EndpointID, request.CapabilityID, request.PropertyID)
	if !exists || !property.Definition.Writable {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	property.Value = request.Value
	if err := property.Validate(); err != nil {
		return device.Device{}, fmt.Errorf("%w: %v", providersdk.ErrPropertyInvalid, err)
	}
	message := commandMessage{SchemaVersion: protocolSchemaVersion, Kind: "property", CorrelationID: newCorrelationID(), Value: &request.Value, CreatedAt: time.Now().UTC()}
	if err := p.publishCommand(ctx, commandTopic(p.config.TopicPrefix, request.DeviceID, request.EndpointID, request.CapabilityID, request.PropertyID), message); err != nil {
		return device.Device{}, err
	}
	candidate := item.Clone()
	candidate.SetProperty(request.EndpointID, request.CapabilityID, request.PropertyID, request.Value)
	return candidate, nil
}

func (p *Provider) ExecuteCommand(ctx context.Context, request providersdk.CommandRequest) (device.Device, error) {
	p.mu.RLock()
	item, exists := p.devices[request.DeviceID]
	connected := p.connected
	p.mu.RUnlock()
	if !exists {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	if !connected || !item.IsOnline() {
		return device.Device{}, providersdk.ErrProviderUnavailable
	}
	definition, exists := commandDefinition(item, request.EndpointID, request.CapabilityID, request.CommandID)
	if !exists {
		return device.Device{}, providersdk.ErrCommandUnsupported
	}
	if err := validateCommandParameters(definition, request.Parameters); err != nil {
		return device.Device{}, fmt.Errorf("%w: %v", providersdk.ErrCommandInvalid, err)
	}
	message := commandMessage{SchemaVersion: protocolSchemaVersion, Kind: "action", CorrelationID: newCorrelationID(), IdempotencyKey: request.IdempotencyKey, Parameters: request.Parameters, CreatedAt: time.Now().UTC()}
	if err := p.publishCommand(ctx, commandTopic(p.config.TopicPrefix, request.DeviceID, request.EndpointID, request.CapabilityID, request.CommandID), message); err != nil {
		return device.Device{}, err
	}
	return item.Clone(), nil
}

func (p *Provider) Subscribe(handler func(device.Device)) func() {
	if handler == nil {
		return func() {}
	}
	p.mu.Lock()
	p.nextListener++
	id := p.nextListener
	p.listeners[id] = handler
	p.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { p.mu.Lock(); delete(p.listeners, id); p.mu.Unlock() }) }
}

func (p *Provider) Stats() Stats {
	return Stats{MessagesReceived: p.messagesReceived.Load(), MessagesInvalid: p.messagesInvalid.Load(), MessagesDropped: p.messagesDropped.Load(), CommandsPublished: p.commandsPublished.Load()}
}

func (p *Provider) ProviderMetrics() map[string]uint64 {
	stats := p.Stats()
	return map[string]uint64{"messagesReceived": stats.MessagesReceived, "messagesInvalid": stats.MessagesInvalid, "messagesDropped": stats.MessagesDropped, "commandsPublished": stats.CommandsPublished}
}

func (p *Provider) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-p.inbound:
			p.messagesReceived.Add(1)
			if err := p.handleMessage(message); err != nil {
				p.messagesInvalid.Add(1)
				p.mu.Lock()
				p.lastError = err.Error()
				p.mu.Unlock()
			}
		}
	}
}

func (p *Provider) enqueue(message inboundMessage) {
	select {
	case p.inbound <- message:
	default:
		p.messagesDropped.Add(1)
	}
}

func (p *Provider) handleMessage(message inboundMessage) error {
	parts, ok := topicParts(message.topic, p.config.TopicPrefix)
	if !ok {
		return fmt.Errorf("mqtt topic %q is outside configured prefix", message.topic)
	}
	switch {
	case len(parts) == 2 && parts[0] == "discovery":
		return p.handleDiscovery(parts[1], message)
	case len(parts) == 5 && parts[0] == "state":
		return p.handleState(parts[1], parts[2], parts[3], parts[4], message)
	case len(parts) == 2 && parts[0] == "availability":
		return p.handleAvailability(parts[1], message)
	default:
		return fmt.Errorf("mqtt topic %q does not match a HomeLoom subscription", message.topic)
	}
}

func (p *Provider) handleDiscovery(deviceID string, message inboundMessage) error {
	if !device.ValidStableID(deviceID) {
		return fmt.Errorf("discovery topic has invalid device id %q", deviceID)
	}
	if len(message.payload) == 0 {
		p.mu.Lock()
		item, exists := p.devices[deviceID]
		if exists {
			delete(p.devices, deviceID)
			p.clearRemoteSequencesLocked(deviceID)
			p.nextSequence++
			item.Sequence, item.Removed, item.LastUpdateAt = p.nextSequence, true, time.Now().UTC()
			item.SetOnline(false)
		}
		p.mu.Unlock()
		if exists {
			p.broadcast(item)
		}
		return nil
	}
	var item device.Device
	if err := decodeJSON(message.payload, &item); err != nil {
		return fmt.Errorf("decode discovery %q: %w", deviceID, err)
	}
	if item.ID != deviceID {
		return fmt.Errorf("discovery topic device %q does not match payload device %q", deviceID, item.ID)
	}
	item.ProviderID, item.Disabled, item.Removed = p.id, false, false
	item.NormalizeAvailability()
	if message.retained {
		item.SetAvailability(device.AvailabilityUnknown)
	}
	if item.LastUpdateAt.IsZero() {
		item.LastUpdateAt = time.Now().UTC()
	}
	p.mu.Lock()
	if previous, exists := p.devices[deviceID]; exists {
		mergeReportedValues(&item, previous)
		item.SetAvailability(previous.EffectiveAvailability())
	}
	p.nextSequence++
	item.Sequence = p.nextSequence
	if err := item.NormalizeModelParameters(); err != nil {
		p.mu.Unlock()
		return fmt.Errorf("validate discovery %q: %w", deviceID, err)
	}
	p.devices[deviceID] = item.Clone()
	p.mu.Unlock()
	p.broadcast(item)
	return nil
}

func (p *Provider) handleState(deviceID, endpointID, capabilityID, propertyID string, message inboundMessage) error {
	if !validPath(deviceID, endpointID, capabilityID, propertyID) {
		return fmt.Errorf("state topic contains an invalid stable id")
	}
	var state stateMessage
	if err := decodeJSON(message.payload, &state); err != nil {
		return fmt.Errorf("decode state %q: %w", message.topic, err)
	}
	if state.SchemaVersion != protocolSchemaVersion || state.Value == nil || state.Sequence == 0 || state.ObservedAt.IsZero() {
		return fmt.Errorf("state requires schemaVersion 1, value, positive sequence, and observedAt")
	}
	if message.retained && time.Since(state.ObservedAt) > p.config.retainedStateMaxAge() {
		return nil
	}
	sequenceKey := "state\x00" + deviceID + "\x00" + endpointID + "\x00" + capabilityID + "\x00" + propertyID
	p.mu.Lock()
	item, exists := p.devices[deviceID]
	if !exists {
		p.mu.Unlock()
		return fmt.Errorf("state references undiscovered device %q", deviceID)
	}
	if state.Sequence <= p.remoteSequences[sequenceKey] {
		p.mu.Unlock()
		return nil
	}
	property, exists := item.Property(endpointID, capabilityID, propertyID)
	if !exists {
		p.mu.Unlock()
		return fmt.Errorf("state references unknown property %s.%s.%s", endpointID, capabilityID, propertyID)
	}
	property.Value = *state.Value
	if err := property.Validate(); err != nil {
		p.mu.Unlock()
		return fmt.Errorf("validate state %q: %w", message.topic, err)
	}
	item.SetProperty(endpointID, capabilityID, propertyID, *state.Value)
	item.SetAvailability(device.AvailabilityOnline)
	item.LastUpdateAt = state.ObservedAt.UTC()
	p.nextSequence++
	item.Sequence = p.nextSequence
	p.remoteSequences[sequenceKey] = state.Sequence
	p.devices[deviceID] = item.Clone()
	p.mu.Unlock()
	p.broadcast(item)
	return nil
}

func (p *Provider) handleAvailability(deviceID string, message inboundMessage) error {
	if !device.ValidStableID(deviceID) {
		return fmt.Errorf("availability topic has invalid device id %q", deviceID)
	}
	var availability availabilityMessage
	if err := decodeJSON(message.payload, &availability); err != nil {
		return fmt.Errorf("decode availability %q: %w", deviceID, err)
	}
	if availability.SchemaVersion != protocolSchemaVersion || availability.Sequence == 0 || availability.ObservedAt.IsZero() {
		return fmt.Errorf("availability requires schemaVersion 1, positive sequence, and observedAt")
	}
	if availability.Availability != device.AvailabilityOnline && availability.Availability != device.AvailabilityOffline && availability.Availability != device.AvailabilityUnknown {
		return fmt.Errorf("invalid availability %q", availability.Availability)
	}
	if message.retained && time.Since(availability.ObservedAt) > p.config.retainedStateMaxAge() {
		return nil
	}
	sequenceKey := "availability\x00" + deviceID
	p.mu.Lock()
	item, exists := p.devices[deviceID]
	if !exists {
		p.mu.Unlock()
		return fmt.Errorf("availability references undiscovered device %q", deviceID)
	}
	if availability.Sequence <= p.remoteSequences[sequenceKey] {
		p.mu.Unlock()
		return nil
	}
	item.SetAvailability(availability.Availability)
	item.LastUpdateAt = availability.ObservedAt.UTC()
	p.nextSequence++
	item.Sequence = p.nextSequence
	p.remoteSequences[sequenceKey] = availability.Sequence
	p.devices[deviceID] = item.Clone()
	p.mu.Unlock()
	p.broadcast(item)
	return nil
}

func (p *Provider) markConnectionDown() {
	p.mu.Lock()
	if !p.connected {
		p.mu.Unlock()
		return
	}
	p.connected = false
	items := make([]device.Device, 0, len(p.devices))
	for id, item := range p.devices {
		item.SetAvailability(device.AvailabilityUnknown)
		item.LastUpdateAt = time.Now().UTC()
		p.nextSequence++
		item.Sequence = p.nextSequence
		p.devices[id] = item.Clone()
		items = append(items, item)
	}
	p.mu.Unlock()
	for _, item := range items {
		p.broadcast(item)
	}
}

func (p *Provider) publishCommand(ctx context.Context, topic string, message commandMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode mqtt command: %w", err)
	}
	p.mu.RLock()
	transport, connected := p.transport, p.connected
	p.mu.RUnlock()
	if transport == nil || !connected {
		return providersdk.ErrProviderUnavailable
	}
	if err := transport.Publish(ctx, topic, p.config.QoS, false, payload); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("%w: %v", providersdk.ErrWriteRejected, err)
	}
	p.commandsPublished.Add(1)
	return nil
}

func (p *Provider) broadcast(item device.Device) {
	p.mu.RLock()
	handlers := make([]func(device.Device), 0, len(p.listeners))
	for _, handler := range p.listeners {
		handlers = append(handlers, handler)
	}
	p.mu.RUnlock()
	for _, handler := range handlers {
		handler(item.Clone())
	}
}

func (p *Provider) clearRemoteSequencesLocked(deviceID string) {
	needle := "\x00" + deviceID
	for key := range p.remoteSequences {
		if key == "availability"+needle || strings.HasPrefix(key, "state"+needle+"\x00") {
			delete(p.remoteSequences, key)
		}
	}
}

func mergeReportedValues(target *device.Device, previous device.Device) {
	for _, endpoint := range target.Endpoints {
		for _, capability := range endpoint.Capabilities {
			for _, property := range capability.Properties {
				current, exists := previous.Property(endpoint.ID, capability.ID, property.Definition.ID)
				if exists && current.Definition.Type == property.Definition.Type {
					target.SetProperty(endpoint.ID, capability.ID, property.Definition.ID, current.Value)
				}
			}
		}
	}
}

func validPath(parts ...string) bool {
	for _, part := range parts {
		if !device.ValidStableID(part) {
			return false
		}
	}
	return true
}

func commandDefinition(item device.Device, endpointID, capabilityID, commandID string) (device.CommandDefinition, bool) {
	for _, endpoint := range item.Endpoints {
		if endpoint.ID != endpointID {
			continue
		}
		for _, capability := range endpoint.Capabilities {
			if capability.ID != capabilityID {
				continue
			}
			for _, command := range capability.Commands {
				if command.ID == commandID {
					return command, true
				}
			}
		}
	}
	return device.CommandDefinition{}, false
}

func validateCommandParameters(definition device.CommandDefinition, parameters map[string]device.PropertyValue) error {
	allowed := make(map[string]device.CommandParameter, len(definition.Parameters))
	for _, parameter := range definition.Parameters {
		allowed[parameter.ID] = parameter
		if parameter.Required {
			if _, exists := parameters[parameter.ID]; !exists {
				return fmt.Errorf("required parameter %q is missing", parameter.ID)
			}
		}
	}
	for id, value := range parameters {
		parameter, exists := allowed[id]
		if !exists {
			return fmt.Errorf("unknown parameter %q", id)
		}
		if value.Type != parameter.Type || !valueHasSinglePayload(value) {
			return fmt.Errorf("parameter %q does not contain a valid %s value", id, parameter.Type)
		}
	}
	return nil
}

func valueHasSinglePayload(value device.PropertyValue) bool {
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
	switch value.Type {
	case device.ValueTypeBool:
		return value.Bool != nil
	case device.ValueTypeInt:
		return value.Int != nil
	case device.ValueTypeNumber:
		return value.Number != nil
	case device.ValueTypeString, device.ValueTypeEnum:
		return value.String != nil
	default:
		return false
	}
}

func newCorrelationID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("mqtt-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}
