package xiaomi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

const (
	XiaomiMIoTCloudProviderType = "xiaomi-miot-cloud"
	// XiaomiHomeCloudProviderType is reserved for a future official Xiaomi
	// Home Cloud adapter. It is intentionally not registered by this package.
	XiaomiHomeCloudProviderType = "xiaomi-home-cloud"
)

type cloudClientFactory func() miotCloudClient

// CloudProvider supplements the local central-hub Provider for devices that
// are only reliably reachable through the legacy MIoT cloud APIs used by
// hass-xiaomi-miot. It is deliberately a separate Provider type and does not
// share MQTT lifecycle state with the central hub implementation.
type CloudProvider struct {
	id       string
	name     string
	config   CloudConfig
	factory  cloudClientFactory
	local    miotLocalClient
	resolver *SpecResolver

	mu            sync.RWMutex
	client        miotCloudClient
	directory     []HubDevice
	devices       map[string]device.Device
	sourceDevices map[string]device.Device
	rawProperties map[string]PropertyMapping
	rawActions    map[string]ActionMapping
	catalog       map[string]providersdk.SourceCatalogMetadata
	valueStatus   map[string]providersdk.SourceValueStatus
	listeners     map[uint64]func(device.Device)
	next          uint64
	sequence      uint64
	cancel        context.CancelFunc
	done          chan struct{}

	requests       atomic.Uint64
	events         atomic.Uint64
	errors         atomic.Uint64
	localRequests  atomic.Uint64
	localFailures  atomic.Uint64
	cloudFallbacks atomic.Uint64
}

var (
	_ providersdk.Provider         = (*CloudProvider)(nil)
	_ providersdk.LiveReconfigurer = (*CloudProvider)(nil)
	_ providersdk.Discoverer       = (*CloudProvider)(nil)
	_ providersdk.SourceCataloger  = (*CloudProvider)(nil)
	_ providersdk.PropertyReader   = (*CloudProvider)(nil)
	_ providersdk.PropertyWriter   = (*CloudProvider)(nil)
	_ providersdk.CommandExecutor  = (*CloudProvider)(nil)
	_ providersdk.EventSubscriber  = (*CloudProvider)(nil)
	_ providersdk.MetricsReporter  = (*CloudProvider)(nil)
)

func NewCloudProviderFromConfig(item providerconfig.Config) (*CloudProvider, error) {
	return NewCloudProviderFromConfigWithSpecResolver(item, NewSpecResolver(nil))
}

func NewCloudProviderFromConfigWithSpecResolver(item providerconfig.Config, resolver *SpecResolver) (*CloudProvider, error) {
	config, err := decodeCloudConfig(item)
	if err != nil {
		return nil, err
	}
	return newCloudProvider(item.ID, item.Name, config, func() miotCloudClient { return newHTTPMiotCloudClient(config) }, resolver)
}

func newCloudProvider(id, name string, config CloudConfig, factory cloudClientFactory, resolver *SpecResolver) (*CloudProvider, error) {
	provider := &CloudProvider{
		id: id, name: name, config: config, factory: factory, resolver: resolver,
		local:   newUDPMIoTLocalClient(localMIoTTimeout(config.requestTimeout())),
		devices: make(map[string]device.Device), sourceDevices: make(map[string]device.Device), rawProperties: make(map[string]PropertyMapping), rawActions: make(map[string]ActionMapping),
		catalog: make(map[string]providersdk.SourceCatalogMetadata), valueStatus: make(map[string]providersdk.SourceValueStatus), listeners: make(map[uint64]func(device.Device)),
	}
	for _, configured := range config.Devices {
		item := buildDevice(id, configured)
		item.RuntimeMode = device.RuntimeModePending
		if err := item.NormalizeModelParameters(); err != nil {
			return nil, fmt.Errorf("Xiaomi MIoT cloud device %q model mapping: %w", configured.ID, err)
		}
		provider.devices[item.ID] = item
		provider.sourceDevices[item.ID] = item.Clone()
		provider.catalog[item.ID] = providersdk.SourceCatalogMetadata{Complete: false, Source: "configured-mapping-fallback", Model: configured.Model, Error: "MIoT Spec has not been loaded"}
	}
	return provider, nil
}

func (p *CloudProvider) Manifest() providersdk.Manifest {
	p.mu.RLock()
	name := p.name
	p.mu.RUnlock()
	return providersdk.Manifest{ID: p.id, Type: XiaomiMIoTCloudProviderType, Name: name, Version: "0.1.0"}
}

func (p *CloudProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyRead: true, PropertyWrite: true, Commands: true, Events: false}
}

func (p *CloudProvider) Initialize(ctx context.Context) error {
	p.mu.Lock()
	if p.client != nil {
		p.mu.Unlock()
		return nil
	}
	client := p.factory()
	lifecycle, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	p.client, p.cancel, p.done = client, cancel, done
	p.mu.Unlock()
	if err := client.Login(ctx); err != nil {
		p.clearClient(client)
		cancel()
		return fmt.Errorf("login Xiaomi MIoT cloud: %w", err)
	}
	directory, err := client.DeviceList(ctx)
	if err != nil {
		p.clearClient(client)
		cancel()
		return fmt.Errorf("request Xiaomi MIoT cloud device list: %w", err)
	}
	p.mu.Lock()
	p.directory = append([]HubDevice(nil), directory...)
	p.mu.Unlock()
	p.loadCloudSpecs(ctx, directory)
	p.refreshAllCloud(ctx)
	go p.cloudPollLoop(lifecycle, done)
	return nil
}

func (p *CloudProvider) clearClient(expected miotCloudClient) {
	p.mu.Lock()
	if p.client == expected {
		p.client, p.cancel, p.done = nil, nil, nil
	}
	p.mu.Unlock()
}

func (p *CloudProvider) Close(ctx context.Context) error {
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.client, p.cancel, p.done = nil, nil, nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.mu.Lock()
	for id, item := range p.devices {
		item.SetOnline(false)
		p.devices[id] = item
	}
	for id, item := range p.sourceDevices {
		item.SetOnline(false)
		p.sourceDevices[id] = item
	}
	p.mu.Unlock()
	return nil
}

func (p *CloudProvider) Reconfigure(ctx context.Context, replacement providersdk.Provider) (bool, error) {
	next, ok := replacement.(*CloudProvider)
	if !ok || next.id != p.id {
		return false, nil
	}
	p.mu.RLock()
	compatible := equalCloudSessionConfig(p.config, next.config)
	client := p.client
	directory := append([]HubDevice(nil), p.directory...)
	p.mu.RUnlock()
	if !compatible || client == nil {
		return false, nil
	}
	p.mu.Lock()
	updated := make(map[string]device.Device, len(next.devices))
	updatedSources := make(map[string]device.Device, len(next.sourceDevices))
	for id, item := range next.devices {
		if previous, exists := p.devices[id]; exists {
			item = preserveDeviceState(item, previous)
		}
		updated[id] = item
	}
	for id, item := range next.sourceDevices {
		if previous, exists := p.sourceDevices[id]; exists {
			item = preserveDeviceState(item, previous)
		}
		updatedSources[id] = item
	}
	p.name, p.config, p.factory, p.devices, p.sourceDevices = next.name, next.config, next.factory, updated, updatedSources
	p.rawProperties, p.rawActions, p.catalog = next.rawProperties, next.rawActions, next.catalog
	p.mu.Unlock()
	go func() {
		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), next.config.requestTimeout())
		defer cancel()
		p.loadCloudSpecs(refreshCtx, directory)
		p.refreshAllCloud(refreshCtx)
	}()
	return true, nil
}

func equalCloudSessionConfig(left, right CloudConfig) bool {
	left.Devices, right.Devices = nil, nil
	return reflect.DeepEqual(left, right)
}

func (p *CloudProvider) DiscoverDevices(context.Context) ([]device.Device, error) {
	p.mu.RLock()
	result := make([]device.Device, 0, len(p.devices))
	for _, item := range p.devices {
		result = append(result, item.Clone())
	}
	p.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// DiscoverCloudDevices refreshes the account directory through the active
// cloud session without creating another Provider or login session.
func (p *CloudProvider) DiscoverCloudDevices(ctx context.Context) ([]HubDevice, error) {
	client, err := p.currentCloudClient()
	if err != nil {
		return nil, err
	}
	p.requests.Add(1)
	items, err := client.DeviceList(ctx)
	if err != nil {
		p.errors.Add(1)
		return nil, err
	}
	p.mu.Lock()
	p.directory = append([]HubDevice(nil), items...)
	p.mu.Unlock()
	return items, nil
}

func (p *CloudProvider) SourceCatalog(context.Context) ([]providersdk.SourceCatalogDevice, error) {
	p.mu.RLock()
	result := make([]providersdk.SourceCatalogDevice, 0, len(p.sourceDevices))
	for id, item := range p.sourceDevices {
		metadata := p.catalog[id]
		metadata.Values = make(map[string]providersdk.SourceValueStatus)
		prefix := id + "\x00"
		for key, status := range p.valueStatus {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			parts := strings.Split(strings.TrimPrefix(key, prefix), "\x00")
			if len(parts) == 3 {
				metadata.Values[providersdk.SourceValueKey(parts[0], parts[1], parts[2])] = status
			}
		}
		result = append(result, providersdk.SourceCatalogDevice{Device: item.Clone(), Catalog: metadata})
	}
	p.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (p *CloudProvider) loadCloudSpecs(ctx context.Context, directory []HubDevice) {
	p.mu.RLock()
	configuredDevices := append([]DeviceConfig(nil), p.config.Devices...)
	resolver := p.resolver
	p.mu.RUnlock()
	byDID := make(map[string]HubDevice, len(directory))
	for _, item := range directory {
		byDID[item.DID] = item
	}
	jobs := make(chan DeviceConfig)
	results := make(chan specLoadResult, len(configuredDevices))
	workerCount := 4
	if len(configuredDevices) < workerCount {
		workerCount = len(configuredDevices)
	}
	var workers sync.WaitGroup
	for index := 0; index < workerCount; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for configured := range jobs {
				hub := byDID[configured.DID]
				model := hub.Model
				if model == "" {
					model = configured.Model
				}
				if resolver == nil {
					results <- specLoadResult{configured: configured, hub: hub, err: fmt.Errorf("MIoT Spec resolver is unavailable")}
					continue
				}
				document, fetchedAt, source, err := resolver.Resolve(ctx, hub.SpecType, model)
				results <- specLoadResult{configured: configured, hub: hub, document: document, fetchedAt: fetchedAt, source: source, err: err}
			}
		}()
	}
	go func() {
		for _, configured := range configuredDevices {
			jobs <- configured
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()
	loaded := make([]specLoadResult, 0, len(configuredDevices))
	for result := range results {
		loaded = append(loaded, result)
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].configured.ID < loaded[j].configured.ID })
	nextDevices := make(map[string]device.Device, len(configuredDevices))
	nextSourceDevices := make(map[string]device.Device, len(configuredDevices))
	nextProperties := make(map[string]PropertyMapping)
	nextActions := make(map[string]ActionMapping)
	nextCatalog := make(map[string]providersdk.SourceCatalogMetadata, len(configuredDevices))
	nextConfigured := make([]DeviceConfig, 0, len(configuredDevices))
	for _, loadedItem := range loaded {
		configured, hub := loadedItem.configured, loadedItem.hub
		automapped := false
		if loadedItem.err == nil {
			configured, automapped = autoMapCloudDevice(configured, loadedItem.document)
		}
		nextConfigured = append(nextConfigured, configured)
		model := hub.Model
		if model == "" {
			model = configured.Model
		}
		item := buildDevice(p.id, configured)
		item.RuntimeMode = device.RuntimeModePending
		sourceItem := item.Clone()
		metadata := providersdk.SourceCatalogMetadata{Complete: false, Source: "configured-mapping-fallback", Model: model, SpecType: hub.SpecType}
		if loadedItem.err != nil {
			metadata.Error = loadedItem.err.Error()
		} else {
			var properties map[string]PropertyMapping
			var actions map[string]ActionMapping
			sourceItem, properties, actions = mergeMIoTSpec(sourceItem, configured, loadedItem.document)
			for key, mapping := range properties {
				nextProperties[key] = mapping
			}
			for key, action := range actions {
				nextActions[key] = action
			}
			metadata.Complete, metadata.Source, metadata.SpecType, metadata.FetchedAt = true, loadedItem.source, loadedItem.document.Type, loadedItem.fetchedAt
		}
		if hub.Online != nil {
			item.SetOnline(*hub.Online)
			sourceItem.SetOnline(*hub.Online)
		}
		p.mu.RLock()
		previous, exists := p.devices[item.ID]
		p.mu.RUnlock()
		if exists {
			item.RuntimeMode = previous.RuntimeMode
		}
		if exists && !automapped {
			item = preserveDeviceState(item, previous)
		}
		p.mu.RLock()
		previousSource, sourceExists := p.sourceDevices[sourceItem.ID]
		p.mu.RUnlock()
		if sourceExists && !automapped {
			sourceItem = preserveDeviceState(sourceItem, previousSource)
		} else {
			sourceItem.Availability, sourceItem.Online = item.Availability, item.Online
			sourceItem.Sequence, sourceItem.LastUpdateAt, sourceItem.RuntimeMode = item.Sequence, item.LastUpdateAt, item.RuntimeMode
		}
		nextDevices[item.ID], nextSourceDevices[item.ID], nextCatalog[item.ID] = item, sourceItem, metadata
	}
	p.mu.Lock()
	p.config.Devices, p.devices, p.sourceDevices, p.rawProperties, p.rawActions, p.catalog = nextConfigured, nextDevices, nextSourceDevices, nextProperties, nextActions, nextCatalog
	p.mu.Unlock()
}

func (p *CloudProvider) ReadProperty(ctx context.Context, request providersdk.PropertyReadRequest) (device.Property, error) {
	configured, mapping, err := p.cloudMappingForProperty(request.DeviceID, request.EndpointID, request.CapabilityID, request.PropertyID)
	if err != nil {
		return device.Property{}, err
	}
	input := []cloudProperty{{DID: configured.DID, SIID: mapping.SIID, PIID: mapping.PIID}}
	result, err := p.getProperties(ctx, configured, input)
	if err != nil || len(result) != 1 || result[0].Code != 0 {
		p.errors.Add(1)
		return device.Property{}, providersdk.ErrProviderUnavailable
	}
	value, err := decodePropertyValue(mapping, result[0].Value)
	if err != nil {
		p.errors.Add(1)
		return device.Property{}, providersdk.ErrPropertyInvalid
	}
	updated := p.updateCloudProperty(configured.ID, mapping, value)
	p.broadcastCloud(updated)
	property, ok := updated.Property(mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)
	if !ok {
		p.mu.RLock()
		source := p.sourceDevices[configured.ID]
		p.mu.RUnlock()
		property, _ = source.Property(mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)
	}
	return property, nil
}

func (p *CloudProvider) WriteProperty(ctx context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	configured, mapping, err := p.cloudMappingForProperty(request.DeviceID, request.EndpointID, request.CapabilityID, request.PropertyID)
	if err != nil {
		return device.Device{}, err
	}
	if !mapping.Writable {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	item, ok := p.snapshotCloud(configured.ID)
	if !ok {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	property, ok := item.Property(mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)
	if !ok {
		p.mu.RLock()
		source := p.sourceDevices[configured.ID]
		p.mu.RUnlock()
		property, ok = source.Property(mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)
	}
	if !ok {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	property.Definition.Min, property.Definition.Max, property.Definition.Step = mapping.Min, mapping.Max, mapping.Step
	property.Value = request.Value
	if err := property.Validate(); err != nil {
		return device.Device{}, providersdk.ErrPropertyInvalid
	}
	raw, err := encodePropertyValue(mapping, request.Value)
	if err != nil {
		return device.Device{}, providersdk.ErrPropertyInvalid
	}
	input := []cloudProperty{{DID: configured.DID, SIID: mapping.SIID, PIID: mapping.PIID, Value: raw}}
	result, err := p.setProperties(ctx, configured, input)
	if err != nil || len(result) != 1 || result[0].Code != 0 {
		p.errors.Add(1)
		return device.Device{}, providersdk.ErrWriteRejected
	}
	updated := p.updateCloudProperty(configured.ID, mapping, request.Value)
	p.broadcastCloud(updated)
	return updated, nil
}

func (p *CloudProvider) ExecuteCommand(ctx context.Context, request providersdk.CommandRequest) (device.Device, error) {
	configured, action, err := p.cloudMappingForAction(request.DeviceID, request.EndpointID, request.CapabilityID, request.CommandID)
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
	if err := p.doAction(ctx, configured, cloudAction{DID: configured.DID, SIID: action.SIID, AIID: action.AIID, Input: input}); err != nil {
		p.errors.Add(1)
		return device.Device{}, providersdk.ErrWriteRejected
	}
	item, _ := p.snapshotCloud(configured.ID)
	return item, nil
}

func (p *CloudProvider) getProperties(ctx context.Context, configured DeviceConfig, input []cloudProperty) ([]cloudProperty, error) {
	access, mode, local := p.localAccess(configured)
	if local {
		p.requests.Add(1)
		p.localRequests.Add(1)
		result, err := p.local.GetProperties(ctx, access, input)
		if err == nil && propertyResultsSuccessful(result, len(input)) {
			p.setRuntimeMode(configured.ID, device.RuntimeModeLocal)
			return result, nil
		}
		p.localFailures.Add(1)
		if mode == cloudConnectionLocal {
			if err != nil {
				return nil, err
			}
			return result, errors.New("local MIoT property response was incomplete")
		}
	} else if mode == cloudConnectionLocal {
		return nil, errors.New("local MIoT address or token is unavailable")
	}
	if mode == cloudConnectionAuto {
		p.cloudFallbacks.Add(1)
	}
	client, err := p.currentCloudClient()
	if err != nil {
		return nil, err
	}
	p.requests.Add(1)
	result, err := client.GetProperties(ctx, input)
	if err == nil {
		p.setRuntimeMode(configured.ID, device.RuntimeModeCloud)
	}
	return result, err
}

func (p *CloudProvider) setRuntimeMode(id string, mode device.RuntimeMode) {
	p.mu.Lock()
	item, exists := p.devices[id]
	if !exists || item.RuntimeMode == mode {
		p.mu.Unlock()
		return
	}
	item.RuntimeMode = mode
	p.sequence++
	item.Sequence, item.LastUpdateAt = p.sequence, time.Now().UTC()
	p.devices[id] = item
	if source, ok := p.sourceDevices[id]; ok {
		source.RuntimeMode, source.Sequence, source.LastUpdateAt = mode, item.Sequence, item.LastUpdateAt
		p.sourceDevices[id] = source
	}
	p.mu.Unlock()
	p.broadcastCloud(item.Clone())
}

func (p *CloudProvider) setProperties(ctx context.Context, configured DeviceConfig, input []cloudProperty) ([]cloudProperty, error) {
	access, mode, local := p.localAccess(configured)
	if local {
		p.requests.Add(1)
		p.localRequests.Add(1)
		result, err := p.local.SetProperties(ctx, access, input)
		if err == nil && propertyResultsSuccessful(result, len(input)) {
			return result, nil
		}
		p.localFailures.Add(1)
		if mode == cloudConnectionLocal {
			if err != nil {
				return nil, err
			}
			return result, errors.New("local MIoT write response was incomplete")
		}
	} else if mode == cloudConnectionLocal {
		return nil, errors.New("local MIoT address or token is unavailable")
	}
	if mode == cloudConnectionAuto {
		p.cloudFallbacks.Add(1)
	}
	client, err := p.currentCloudClient()
	if err != nil {
		return nil, err
	}
	p.requests.Add(1)
	return client.SetProperties(ctx, input)
}

func (p *CloudProvider) doAction(ctx context.Context, configured DeviceConfig, input cloudAction) error {
	access, mode, local := p.localAccess(configured)
	if local {
		p.requests.Add(1)
		p.localRequests.Add(1)
		if err := p.local.Action(ctx, access, input); err == nil {
			return nil
		} else {
			p.localFailures.Add(1)
			if mode == cloudConnectionLocal {
				return err
			}
		}
	} else if mode == cloudConnectionLocal {
		return errors.New("local MIoT address or token is unavailable")
	}
	if mode == cloudConnectionAuto {
		p.cloudFallbacks.Add(1)
	}
	client, err := p.currentCloudClient()
	if err != nil {
		return err
	}
	p.requests.Add(1)
	return client.Action(ctx, input)
}

func (p *CloudProvider) localAccess(configured DeviceConfig) (miotLocalAccess, string, bool) {
	mode := configured.ConnectionMode
	if mode == "" {
		mode = cloudConnectionAuto
	}
	if mode == cloudConnectionCloud {
		return miotLocalAccess{}, mode, false
	}
	p.mu.RLock()
	directory := append([]HubDevice(nil), p.directory...)
	local := p.local
	p.mu.RUnlock()
	if local == nil {
		return miotLocalAccess{}, mode, false
	}
	for _, item := range directory {
		if item.DID == configured.DID && validLocalAccess(item.LocalIP, item.Token) {
			return miotLocalAccess{Host: item.LocalIP, Token: item.Token}, mode, true
		}
	}
	return miotLocalAccess{}, mode, false
}

func propertyResultsSuccessful(result []cloudProperty, expected int) bool {
	if len(result) != expected {
		return false
	}
	for _, item := range result {
		if item.Code != 0 {
			return false
		}
	}
	return true
}

func (p *CloudProvider) Subscribe(handler func(device.Device)) func() {
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

func (p *CloudProvider) ProviderMetrics() map[string]uint64 {
	return map[string]uint64{"requests": p.requests.Load(), "events": p.events.Load(), "errors": p.errors.Load(), "localRequests": p.localRequests.Load(), "localFailures": p.localFailures.Load(), "cloudFallbacks": p.cloudFallbacks.Load()}
}

func (p *CloudProvider) cloudPollLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
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
			p.refreshAllCloud(refreshCtx)
			cancel()
		}
	}
}

func (p *CloudProvider) refreshAllCloud(ctx context.Context) {
	type mappingRequest struct {
		configured DeviceConfig
		mapping    PropertyMapping
	}
	p.mu.RLock()
	configured := append([]DeviceConfig(nil), p.config.Devices...)
	raw := make(map[string]PropertyMapping, len(p.rawProperties))
	for key, value := range p.rawProperties {
		raw[key] = value
	}
	p.mu.RUnlock()
	requests := make(map[string][]mappingRequest, len(configured))
	for _, item := range configured {
		for _, mapping := range item.Properties {
			if mapping.Readable == nil || *mapping.Readable {
				requests[item.ID] = append(requests[item.ID], mappingRequest{item, mapping})
			}
		}
		for key, mapping := range raw {
			if strings.HasPrefix(key, item.ID+"\x00") && (mapping.Readable == nil || *mapping.Readable) {
				requests[item.ID] = append(requests[item.ID], mappingRequest{item, mapping})
			}
		}
	}
	deviceIDs := make([]string, 0, len(requests))
	for id := range requests {
		deviceIDs = append(deviceIDs, id)
	}
	sort.Strings(deviceIDs)
	const chunkSize = 10
	processDevice := func(deviceRequests []mappingRequest) {
		for start := 0; start < len(deviceRequests); start += chunkSize {
			end := start + chunkSize
			if end > len(deviceRequests) {
				end = len(deviceRequests)
			}
			chunk := deviceRequests[start:end]
			input := make([]cloudProperty, 0, len(chunk))
			for _, request := range chunk {
				input = append(input, cloudProperty{DID: request.configured.DID, SIID: request.mapping.SIID, PIID: request.mapping.PIID})
			}
			result, requestErr := p.getProperties(ctx, chunk[0].configured, input)
			if requestErr != nil {
				p.errors.Add(1)
				continue
			}
			requestByKey := make(map[string]mappingRequest, len(chunk))
			for _, request := range chunk {
				requestByKey[cloudResultKey(request.configured.DID, request.mapping.SIID, request.mapping.PIID)] = request
			}
			for index, response := range result {
				if response.Code != 0 {
					continue
				}
				request, found := requestByKey[cloudResultKey(response.DID, response.SIID, response.PIID)]
				if !found && index < len(chunk) {
					// Local and compatible cloud endpoints may omit identifiers but
					// retain request order.
					request = chunk[index]
					found = true
				}
				if !found {
					continue
				}
				value, decodeErr := decodePropertyValue(request.mapping, response.Value)
				if decodeErr != nil {
					p.errors.Add(1)
					continue
				}
				p.events.Add(1)
				p.broadcastCloud(p.updateCloudProperty(request.configured.ID, request.mapping, value))
			}
		}
	}
	workerCount := 4
	if len(deviceIDs) < workerCount {
		workerCount = len(deviceIDs)
	}
	jobs := make(chan []mappingRequest)
	var workers sync.WaitGroup
	for index := 0; index < workerCount; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for deviceRequests := range jobs {
				processDevice(deviceRequests)
			}
		}()
	}
sendDevices:
	for _, deviceID := range deviceIDs {
		select {
		case jobs <- requests[deviceID]:
		case <-ctx.Done():
			break sendDevices
		}
	}
	close(jobs)
	workers.Wait()
}

func (p *CloudProvider) currentCloudClient() (miotCloudClient, error) {
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return nil, providersdk.ErrProviderUnavailable
	}
	return client, nil
}

func (p *CloudProvider) updateCloudProperty(id string, mapping PropertyMapping, value device.PropertyValue) device.Device {
	p.mu.Lock()
	item := p.devices[id]
	setObservedProperty(&item, mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID, value)
	source := p.sourceDevices[id]
	setObservedProperty(&source, mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID, value)
	p.valueStatus[sourcePropertyKey(id, mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)] = providersdk.SourceValueStatus{Known: true, Available: true, ObservedAt: time.Now().UTC()}
	p.sequence++
	item.Sequence, item.LastUpdateAt = p.sequence, time.Now().UTC()
	item.SetOnline(true)
	p.devices[id] = item
	source.Sequence, source.LastUpdateAt, source.RuntimeMode = item.Sequence, item.LastUpdateAt, item.RuntimeMode
	source.SetOnline(true)
	p.sourceDevices[id] = source
	p.mu.Unlock()
	return item.Clone()
}

func (p *CloudProvider) broadcastCloud(item device.Device) {
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

func (p *CloudProvider) snapshotCloud(id string) (device.Device, bool) {
	p.mu.RLock()
	item, ok := p.devices[id]
	p.mu.RUnlock()
	return item.Clone(), ok
}

func (p *CloudProvider) cloudMappingForProperty(id, endpoint, capability, property string) (DeviceConfig, PropertyMapping, error) {
	p.mu.RLock()
	configured := append([]DeviceConfig(nil), p.config.Devices...)
	raw, rawExists := p.rawProperties[sourcePropertyKey(id, endpoint, capability, property)]
	p.mu.RUnlock()
	for _, item := range configured {
		if item.ID != id {
			continue
		}
		for _, mapping := range item.Properties {
			if mapping.EndpointID == endpoint && mapping.CapabilityID == capability && mapping.PropertyID == property {
				return item, mapping, nil
			}
		}
		if rawExists {
			return item, raw, nil
		}
		return DeviceConfig{}, PropertyMapping{}, providersdk.ErrPropertyUnsupported
	}
	return DeviceConfig{}, PropertyMapping{}, providersdk.ErrDeviceNotFound
}

func (p *CloudProvider) cloudMappingForAction(id, endpoint, capability, command string) (DeviceConfig, ActionMapping, error) {
	p.mu.RLock()
	configured := append([]DeviceConfig(nil), p.config.Devices...)
	raw, rawExists := p.rawActions[sourceActionKey(id, endpoint, capability, command)]
	p.mu.RUnlock()
	for _, item := range configured {
		if item.ID != id {
			continue
		}
		for _, mapping := range item.Actions {
			if mapping.EndpointID == endpoint && mapping.CapabilityID == capability && mapping.CommandID == command {
				return item, mapping, nil
			}
		}
		if rawExists {
			return item, raw, nil
		}
		return DeviceConfig{}, ActionMapping{}, providersdk.ErrCommandUnsupported
	}
	return DeviceConfig{}, ActionMapping{}, providersdk.ErrDeviceNotFound
}

func (p *CloudProvider) MarshalConfig() (json.RawMessage, error) { return json.Marshal(p.config) }

func cloudResultKey(did string, siid, piid int) string {
	return did + "\x00" + miotKey(siid, piid)
}
