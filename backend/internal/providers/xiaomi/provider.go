package xiaomi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type clientFactory func() hubClient

type Provider struct {
	id      string
	name    string
	config  Config
	factory clientFactory

	mu        sync.RWMutex
	client    hubClient
	devices   map[string]device.Device
	byDID     map[string]string
	listeners map[uint64]func(device.Device)
	next      uint64
	sequence  uint64
	cancel    context.CancelFunc
	lifecycle context.Context
	done      chan struct{}

	requests atomic.Uint64
	events   atomic.Uint64
	errors   atomic.Uint64
}

func NewProviderFromConfig(item providerconfig.Config) (*Provider, error) {
	config, brokerURL, tlsConfig, err := decodeConfig(item)
	if err != nil {
		return nil, err
	}
	factory := func() hubClient { return newMIPSClient(config, brokerURL, tlsConfig) }
	return newProvider(item.ID, item.Name, config, factory)
}

func newProvider(id, name string, config Config, factory clientFactory) (*Provider, error) {
	provider := &Provider{id: id, name: name, config: config, factory: factory, devices: make(map[string]device.Device), byDID: make(map[string]string), listeners: make(map[uint64]func(device.Device))}
	for _, configured := range config.Devices {
		item := buildDevice(id, configured)
		if err := item.NormalizeModelParameters(); err != nil {
			return nil, fmt.Errorf("Xiaomi device %q model mapping: %w", configured.ID, err)
		}
		provider.devices[item.ID] = item
		provider.byDID[configured.DID] = item.ID
	}
	return provider, nil
}

func (p *Provider) Manifest() providersdk.Manifest {
	p.mu.RLock()
	name := p.name
	p.mu.RUnlock()
	return providersdk.Manifest{ID: p.id, Type: "xiaomi", Name: name, Version: "0.1.0"}
}

func (p *Provider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyRead: true, PropertyWrite: true, Commands: true, Events: true}
}

func (p *Provider) Initialize(ctx context.Context) error {
	p.mu.Lock()
	if p.client != nil {
		p.mu.Unlock()
		return errors.New("Xiaomi provider is already initialized")
	}
	lifecycle, cancel := context.WithCancel(context.WithoutCancel(ctx))
	client := p.factory()
	client.SetIncomingHandler(p.handleIncoming)
	p.client, p.cancel, p.lifecycle, p.done = client, cancel, lifecycle, make(chan struct{})
	p.mu.Unlock()
	if err := client.Connect(lifecycle, ctx); err != nil {
		cancel()
		p.mu.Lock()
		p.client, p.cancel, p.lifecycle = nil, nil, nil
		p.mu.Unlock()
		return fmt.Errorf("connect Xiaomi central hub: %w", err)
	}
	requestCtx, requestCancel := context.WithTimeout(ctx, p.config.requestTimeout())
	deviceList, err := client.DeviceList(requestCtx)
	requestCancel()
	if err == nil {
		err = responseOK(deviceList)
	}
	if err != nil {
		_ = client.Close(context.Background())
		cancel()
		p.mu.Lock()
		p.client, p.cancel, p.lifecycle = nil, nil, nil
		p.mu.Unlock()
		return fmt.Errorf("request Xiaomi device list: %w", err)
	}
	// Initial reads run concurrently with a bounded worker count. Individual
	// unavailable properties remain at their typed zero value and are retried by
	// the calibration loop instead of preventing the provider from starting.
	p.refreshAll(ctx)
	go p.pollLoop(lifecycle)
	return nil
}

func (p *Provider) Close(ctx context.Context) error {
	p.mu.Lock()
	client, cancel, done := p.client, p.cancel, p.done
	p.client, p.cancel, p.lifecycle = nil, nil, nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	var err error
	if client != nil {
		err = client.Close(ctx)
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			if err == nil {
				err = ctx.Err()
			}
		}
	}
	p.mu.Lock()
	for id, item := range p.devices {
		item.SetOnline(false)
		p.devices[id] = item
	}
	p.mu.Unlock()
	return err
}

// Reconfigure applies device-model mappings without opening a second MQTT
// session. Xiaomi central hubs may reject a concurrent login using the same
// virtual DID, so transport, TLS, OAuth and polling changes deliberately fall
// back to the normal replacement lifecycle.
func (p *Provider) Reconfigure(ctx context.Context, replacement providersdk.Provider) (bool, error) {
	next, ok := replacement.(*Provider)
	if !ok || next.id != p.id {
		return false, nil
	}
	p.mu.RLock()
	compatible := equalConnectionConfig(p.config, next.config)
	p.mu.RUnlock()
	if !compatible {
		return false, nil
	}

	p.mu.Lock()
	if p.client == nil {
		p.mu.Unlock()
		return true, providersdk.ErrProviderUnavailable
	}
	updated := make(map[string]device.Device, len(next.devices))
	for id, item := range next.devices {
		item = item.Clone()
		if previous, exists := p.devices[id]; exists {
			item = preserveDeviceState(item, previous)
		}
		updated[id] = item
	}
	p.name, p.config, p.devices = next.name, next.config, updated
	p.byDID = make(map[string]string, len(next.byDID))
	for did, id := range next.byDID {
		p.byDID[did] = id
	}
	lifecycle := p.lifecycle
	timeout := p.config.requestTimeout()
	p.mu.Unlock()

	// Refresh in the provider lifecycle so saving a large mapping set is not
	// coupled to the HTTP request deadline. Each property update is broadcast as
	// it arrives; unavailable properties retain their previous in-memory value.
	if lifecycle != nil {
		go func() {
			refreshCtx, cancel := context.WithTimeout(lifecycle, timeout)
			defer cancel()
			p.refreshAll(refreshCtx)
		}()
	}
	return true, nil
}

func equalConnectionConfig(left, right Config) bool {
	left.Devices, right.Devices = nil, nil
	return reflect.DeepEqual(left, right)
}

func preserveDeviceState(next, previous device.Device) device.Device {
	next.Availability, next.Online = previous.Availability, previous.Online
	next.Sequence, next.LastUpdateAt = previous.Sequence, previous.LastUpdateAt
	for _, endpoint := range next.Endpoints {
		for _, capability := range endpoint.Capabilities {
			for _, property := range capability.Properties {
				old, ok := previous.Property(endpoint.ID, capability.ID, property.Definition.ID)
				if ok && old.Definition.Type == property.Definition.Type {
					next.SetProperty(endpoint.ID, capability.ID, property.Definition.ID, old.Value)
				}
			}
		}
	}
	return next
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

// MatchesGateway reports whether a directory request can safely reuse this
// provider's active MQTT connection.
func (p *Provider) MatchesGateway(host string, port int) bool {
	if port == 0 {
		port = defaultPort
	}
	p.mu.RLock()
	matches := strings.EqualFold(strings.TrimSpace(host), p.config.Host) && port == p.config.Port
	p.mu.RUnlock()
	return matches
}

// DiscoverHubDevices reads the central gateway directory through the active
// connection without changing configured unified-model mappings.
func (p *Provider) DiscoverHubDevices(ctx context.Context) ([]HubDevice, error) {
	client, err := p.currentClient()
	if err != nil {
		return nil, err
	}
	p.requests.Add(1)
	raw, err := client.DeviceList(ctx)
	if err != nil {
		p.errors.Add(1)
		return nil, err
	}
	items, err := parseHubDeviceList(raw)
	if err != nil {
		p.errors.Add(1)
	}
	return items, err
}

func (p *Provider) ReadProperty(ctx context.Context, request providersdk.PropertyReadRequest) (device.Property, error) {
	configured, mapping, err := p.mappingForProperty(request.DeviceID, request.EndpointID, request.CapabilityID, request.PropertyID)
	if err != nil {
		return device.Property{}, err
	}
	if mapping.Readable != nil && !*mapping.Readable {
		return device.Property{}, providersdk.ErrPropertyUnsupported
	}
	client, err := p.currentClient()
	if err != nil {
		return device.Property{}, err
	}
	p.requests.Add(1)
	reply, err := client.GetProperty(ctx, configured.DID, mapping.SIID, mapping.PIID)
	if err != nil {
		p.errors.Add(1)
		return device.Property{}, err
	}
	raw, err := responseValue(reply)
	if err != nil {
		p.errors.Add(1)
		return device.Property{}, err
	}
	value, err := decodePropertyValue(mapping, raw)
	if err != nil {
		return device.Property{}, providersdk.ErrPropertyInvalid
	}
	updated := p.updateProperty(configured.ID, mapping, value)
	p.broadcast(updated)
	property, _ := updated.Property(mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)
	return property, nil
}

func (p *Provider) WriteProperty(ctx context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	configured, mapping, err := p.mappingForProperty(request.DeviceID, request.EndpointID, request.CapabilityID, request.PropertyID)
	if err != nil {
		return device.Device{}, err
	}
	if !mapping.Writable {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	current, ok := p.snapshot(request.DeviceID)
	if !ok {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	property, ok := current.Property(mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)
	if !ok || property.Definition.Type != request.Value.Type {
		return device.Device{}, providersdk.ErrPropertyInvalid
	}
	property.Value = request.Value
	if err := property.Validate(); err != nil {
		return device.Device{}, providersdk.ErrPropertyInvalid
	}
	raw, err := encodePropertyValue(mapping, request.Value)
	if err != nil {
		return device.Device{}, providersdk.ErrPropertyInvalid
	}
	client, err := p.currentClient()
	if err != nil {
		return device.Device{}, err
	}
	p.requests.Add(1)
	reply, err := client.SetProperty(ctx, configured.DID, mapping.SIID, mapping.PIID, raw)
	if err != nil {
		p.errors.Add(1)
		return device.Device{}, providersdk.ErrWriteRejected
	}
	if err := responseOK(reply); err != nil {
		p.errors.Add(1)
		return device.Device{}, providersdk.ErrWriteRejected
	}
	updated := p.updateProperty(configured.ID, mapping, request.Value)
	p.broadcast(updated)
	return updated, nil
}

func (p *Provider) ExecuteCommand(ctx context.Context, request providersdk.CommandRequest) (device.Device, error) {
	configured, action, err := p.mappingForAction(request.DeviceID, request.EndpointID, request.CapabilityID, request.CommandID)
	if err != nil {
		return device.Device{}, err
	}
	input := make([]any, 0, len(action.Parameters))
	for _, name := range action.Parameters {
		value, exists := request.Parameters[name]
		if !exists {
			return device.Device{}, providersdk.ErrCommandInvalid
		}
		input = append(input, plainPropertyValue(value))
	}
	client, err := p.currentClient()
	if err != nil {
		return device.Device{}, err
	}
	p.requests.Add(1)
	reply, err := client.Action(ctx, configured.DID, action.SIID, action.AIID, input)
	if err != nil || responseOK(reply) != nil {
		p.errors.Add(1)
		return device.Device{}, providersdk.ErrWriteRejected
	}
	item, _ := p.snapshot(configured.ID)
	return item, nil
}

func (p *Provider) Subscribe(handler func(device.Device)) func() {
	p.mu.Lock()
	p.next++
	id := p.next
	p.listeners[id] = handler
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		delete(p.listeners, id)
		p.mu.Unlock()
	}
}

func (p *Provider) ProviderMetrics() map[string]uint64 {
	return map[string]uint64{"requests": p.requests.Load(), "events": p.events.Load(), "errors": p.errors.Load()}
}

func (p *Provider) pollLoop(ctx context.Context) {
	defer func() {
		p.mu.RLock()
		done := p.done
		p.mu.RUnlock()
		if done != nil {
			close(done)
		}
	}()
	p.mu.RLock()
	interval := p.config.pollInterval()
	p.mu.RUnlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.mu.RLock()
			timeout := p.config.requestTimeout()
			p.mu.RUnlock()
			refreshCtx, cancel := context.WithTimeout(ctx, timeout)
			p.refreshAll(refreshCtx)
			cancel()
		}
	}
}

func (p *Provider) refreshAll(ctx context.Context) {
	type job struct {
		device DeviceConfig
		mapTo  PropertyMapping
	}
	jobs := make(chan job)
	var workers sync.WaitGroup
	workerCount := 8
	for index := 0; index < workerCount; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for current := range jobs {
				readable := current.mapTo.Readable == nil || *current.mapTo.Readable
				if !readable {
					continue
				}
				_, _ = p.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: current.device.ID, EndpointID: current.mapTo.EndpointID, CapabilityID: current.mapTo.CapabilityID, PropertyID: current.mapTo.PropertyID})
			}
		}()
	}
	p.mu.RLock()
	configuredDevices := append([]DeviceConfig(nil), p.config.Devices...)
	p.mu.RUnlock()
sendLoop:
	for _, configured := range configuredDevices {
		for _, mapping := range configured.Properties {
			select {
			case jobs <- job{device: configured, mapTo: mapping}:
			case <-ctx.Done():
				break sendLoop
			}
		}
	}
	close(jobs)
	workers.Wait()
}

func (p *Provider) handleIncoming(incoming hubIncoming) {
	for _, notification := range parseNotifications(incoming.Topic, incoming.Payload) {
		p.mu.RLock()
		deviceID, ok := p.byDID[notification.DID]
		p.mu.RUnlock()
		if !ok {
			continue
		}
		configured, mapping, err := p.mappingForMIoT(deviceID, notification.SIID, notification.PIID)
		if err != nil {
			continue
		}
		value, err := decodePropertyValue(mapping, notification.Value)
		if err != nil {
			p.errors.Add(1)
			continue
		}
		p.events.Add(1)
		p.broadcast(p.updateProperty(configured.ID, mapping, value))
	}
}

func (p *Provider) updateProperty(id string, mapping PropertyMapping, value device.PropertyValue) device.Device {
	p.mu.Lock()
	item := p.devices[id]
	item.SetProperty(mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID, value)
	p.sequence++
	item.Sequence = p.sequence
	item.LastUpdateAt = time.Now().UTC()
	item.SetOnline(true)
	p.devices[id] = item
	p.mu.Unlock()
	return item.Clone()
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

func (p *Provider) currentClient() (hubClient, error) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return nil, providersdk.ErrProviderUnavailable
	}
	return client, nil
}

func (p *Provider) snapshot(id string) (device.Device, bool) {
	p.mu.RLock()
	item, ok := p.devices[id]
	p.mu.RUnlock()
	return item.Clone(), ok
}

func (p *Provider) mappingForProperty(id, endpoint, capability, property string) (DeviceConfig, PropertyMapping, error) {
	p.mu.RLock()
	configuredDevices := append([]DeviceConfig(nil), p.config.Devices...)
	p.mu.RUnlock()
	for _, configured := range configuredDevices {
		if configured.ID != id {
			continue
		}
		for _, mapping := range configured.Properties {
			if mapping.EndpointID == endpoint && mapping.CapabilityID == capability && mapping.PropertyID == property {
				return configured, mapping, nil
			}
		}
		return DeviceConfig{}, PropertyMapping{}, providersdk.ErrPropertyUnsupported
	}
	return DeviceConfig{}, PropertyMapping{}, providersdk.ErrDeviceNotFound
}

func (p *Provider) mappingForMIoT(id string, siid, piid int) (DeviceConfig, PropertyMapping, error) {
	p.mu.RLock()
	configuredDevices := append([]DeviceConfig(nil), p.config.Devices...)
	p.mu.RUnlock()
	for _, configured := range configuredDevices {
		if configured.ID == id {
			for _, mapping := range configured.Properties {
				if mapping.SIID == siid && mapping.PIID == piid {
					return configured, mapping, nil
				}
			}
		}
	}
	return DeviceConfig{}, PropertyMapping{}, providersdk.ErrPropertyUnsupported
}

func (p *Provider) mappingForAction(id, endpoint, capability, command string) (DeviceConfig, ActionMapping, error) {
	p.mu.RLock()
	configuredDevices := append([]DeviceConfig(nil), p.config.Devices...)
	p.mu.RUnlock()
	for _, configured := range configuredDevices {
		if configured.ID != id {
			continue
		}
		for _, mapping := range configured.Actions {
			if mapping.EndpointID == endpoint && mapping.CapabilityID == capability && mapping.CommandID == command {
				return configured, mapping, nil
			}
		}
		return DeviceConfig{}, ActionMapping{}, providersdk.ErrCommandUnsupported
	}
	return DeviceConfig{}, ActionMapping{}, providersdk.ErrDeviceNotFound
}

func buildDevice(providerID string, configured DeviceConfig) device.Device {
	endpoints := make(map[string]*device.Endpoint)
	for _, mapping := range configured.Properties {
		endpoint := endpoints[mapping.EndpointID]
		if endpoint == nil {
			endpoint = &device.Endpoint{ID: mapping.EndpointID, Name: mapping.EndpointID, Type: string(configured.Type)}
			endpoints[mapping.EndpointID] = endpoint
		}
		capabilityIndex := -1
		for index := range endpoint.Capabilities {
			if endpoint.Capabilities[index].ID == mapping.CapabilityID {
				capabilityIndex = index
				break
			}
		}
		if capabilityIndex < 0 {
			endpoint.Capabilities = append(endpoint.Capabilities, device.Capability{ID: mapping.CapabilityID, Type: mapping.CapabilityType})
			capabilityIndex = len(endpoint.Capabilities) - 1
		}
		readable, notifiable := true, true
		if mapping.Readable != nil {
			readable = *mapping.Readable
		}
		if mapping.Notifiable != nil {
			notifiable = *mapping.Notifiable
		}
		enum := make([]string, 0, len(mapping.Enum))
		for value := range mapping.Enum {
			enum = append(enum, value)
		}
		sort.Strings(enum)
		definition := device.PropertyDefinition{ID: mapping.PropertyID, Name: mapping.Name, Type: mapping.ValueType, Unit: mapping.Unit, Readable: readable, Writable: mapping.Writable, Notifiable: notifiable, Min: mapping.Min, Max: mapping.Max, Step: mapping.Step, Enum: enum, StaleAfterSeconds: defaultPollInterval * 2}
		endpoint.Capabilities[capabilityIndex].Properties = append(endpoint.Capabilities[capabilityIndex].Properties, device.Property{Definition: definition, Value: zeroValue(mapping)})
	}
	for _, action := range configured.Actions {
		endpoint := endpoints[action.EndpointID]
		if endpoint == nil {
			continue
		}
		for index := range endpoint.Capabilities {
			if endpoint.Capabilities[index].ID == action.CapabilityID {
				parameters := make([]device.CommandParameter, 0, len(action.Parameters))
				for _, name := range action.Parameters {
					parameters = append(parameters, device.CommandParameter{ID: name, Name: name, Type: device.ValueTypeString, Required: true})
				}
				endpoint.Capabilities[index].Commands = append(endpoint.Capabilities[index].Commands, device.CommandDefinition{ID: action.CommandID, Name: action.Name, Parameters: parameters})
			}
		}
	}
	endpointList := make([]device.Endpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		endpointList = append(endpointList, *endpoint)
	}
	sort.Slice(endpointList, func(i, j int) bool { return endpointList[i].ID < endpointList[j].ID })
	item := device.Device{SchemaVersion: device.SchemaVersion, ID: configured.ID, ProviderID: providerID, Name: configured.Name, Type: configured.Type, Endpoints: endpointList, LastUpdateAt: time.Now().UTC()}
	item.SetAvailability(device.AvailabilityUnknown)
	return item
}

func zeroValue(mapping PropertyMapping) device.PropertyValue {
	switch mapping.ValueType {
	case device.ValueTypeBool:
		return device.BoolValue(false)
	case device.ValueTypeInt:
		return device.IntValue(0)
	case device.ValueTypeNumber:
		return device.NumberValue(0)
	case device.ValueTypeEnum:
		values := make([]string, 0, len(mapping.Enum))
		for value := range mapping.Enum {
			values = append(values, value)
		}
		sort.Strings(values)
		if len(values) > 0 {
			return device.EnumValue(values[0])
		}
		return device.EnumValue("")
	default:
		return device.StringValue("")
	}
}

func decodePropertyValue(mapping PropertyMapping, raw any) (device.PropertyValue, error) {
	if mapping.ValueType == device.ValueTypeEnum {
		for name, remote := range mapping.Enum {
			if reflect.DeepEqual(normalizeNumber(remote), normalizeNumber(raw)) || fmt.Sprint(remote) == fmt.Sprint(raw) {
				return device.EnumValue(name), nil
			}
		}
		return device.PropertyValue{}, errors.New("unknown MIoT enum value")
	}
	switch mapping.ValueType {
	case device.ValueTypeBool:
		if value, ok := raw.(bool); ok {
			return device.BoolValue(value), nil
		}
		if number, ok := numberValue(raw); ok {
			return device.BoolValue(number != 0), nil
		}
	case device.ValueTypeInt:
		if number, ok := numberValue(raw); ok {
			return device.IntValue(int64(number)), nil
		}
	case device.ValueTypeNumber:
		if number, ok := numberValue(raw); ok {
			return device.NumberValue(number), nil
		}
	case device.ValueTypeString:
		if value, ok := raw.(string); ok {
			return device.StringValue(value), nil
		}
	}
	return device.PropertyValue{}, fmt.Errorf("cannot convert %T to %s", raw, mapping.ValueType)
}

func encodePropertyValue(mapping PropertyMapping, value device.PropertyValue) (any, error) {
	if mapping.ValueType == device.ValueTypeEnum {
		if value.String == nil {
			return nil, errors.New("enum value missing")
		}
		remote, ok := mapping.Enum[*value.String]
		if !ok {
			return nil, errors.New("enum mapping missing")
		}
		return remote, nil
	}
	return plainPropertyValue(value), nil
}

func plainPropertyValue(value device.PropertyValue) any {
	if value.Bool != nil {
		return *value.Bool
	}
	if value.Int != nil {
		return *value.Int
	}
	if value.Number != nil {
		return *value.Number
	}
	if value.String != nil {
		return *value.String
	}
	return nil
}

func numberValue(raw any) (float64, bool) {
	switch value := raw.(type) {
	case float64:
		return value, true
	case json.Number:
		number, err := value.Float64()
		return number, err == nil
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case string:
		number, err := strconv.ParseFloat(value, 64)
		return number, err == nil
	}
	return 0, false
}

func normalizeNumber(value any) any {
	if number, ok := numberValue(value); ok {
		return number
	}
	return value
}

func responseOK(raw json.RawMessage) error {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	return walkResponseError(value)
}

func walkResponseError(value any) error {
	switch current := value.(type) {
	case map[string]any:
		if code, ok := current["code"]; ok {
			if number, numeric := numberValue(code); numeric && number != 0 {
				return fmt.Errorf("Xiaomi gateway returned code %v", code)
			}
		}
		for _, child := range current {
			if err := walkResponseError(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range current {
			if err := walkResponseError(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func responseValue(raw json.RawMessage) (any, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := walkResponseError(value); err != nil {
		return nil, err
	}
	if found, ok := findNamedValue(value); ok {
		return found, nil
	}
	return nil, errors.New("Xiaomi property response has no value")
}

func findNamedValue(value any) (any, bool) {
	switch current := value.(type) {
	case map[string]any:
		if found, ok := current["value"]; ok {
			return found, true
		}
		for _, key := range []string{"result", "params", "data"} {
			if child, ok := current[key]; ok {
				if found, ok := findNamedValue(child); ok {
					return found, true
				}
			}
		}
	case []any:
		for _, child := range current {
			if found, ok := findNamedValue(child); ok {
				return found, true
			}
		}
	}
	return nil, false
}

type propertyNotification struct {
	DID   string
	SIID  int
	PIID  int
	Value any
}

func parseNotifications(topic string, raw json.RawMessage) []propertyNotification {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil
	}
	result := make([]propertyNotification, 0)
	collectNotifications(value, &result)
	if len(result) == 0 {
		parts := strings.Split(topic, "/")
		for index, part := range parts {
			if part != "property" || index < 2 || index+1 >= len(parts) {
				continue
			}
			ids := strings.Split(parts[index+1], ".")
			if len(ids) != 2 {
				continue
			}
			siid, err1 := strconv.Atoi(ids[0])
			piid, err2 := strconv.Atoi(ids[1])
			if err1 == nil && err2 == nil {
				result = append(result, propertyNotification{DID: parts[index-1], SIID: siid, PIID: piid, Value: value})
			}
		}
	}
	return result
}

func collectNotifications(value any, output *[]propertyNotification) {
	switch current := value.(type) {
	case map[string]any:
		did, _ := current["did"].(string)
		siid, siidOK := numberValue(current["siid"])
		piid, piidOK := numberValue(current["piid"])
		propertyValue, valueOK := current["value"]
		if did != "" && siidOK && piidOK && valueOK {
			*output = append(*output, propertyNotification{DID: did, SIID: int(siid), PIID: int(piid), Value: propertyValue})
			return
		}
		for _, child := range current {
			collectNotifications(child, output)
		}
	case []any:
		for _, child := range current {
			collectNotifications(child, output)
		}
	}
}
