package xiaomi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

type deviceRoute struct {
	local bool
	cloud bool
	push  bool
}

type Provider struct {
	id       string
	name     string
	config   Config
	factory  clientFactory
	resolver *SpecResolver

	mu            sync.RWMutex
	client        hubClient
	cloud         homeCloudClient
	devices       map[string]device.Device
	byDID         map[string]string
	routes        map[string]deviceRoute
	directory     []HubDevice
	rawProperties map[string]PropertyMapping
	rawActions    map[string]ActionMapping
	catalog       map[string]providersdk.SourceCatalogMetadata
	valueStatus   map[string]providersdk.SourceValueStatus
	listeners     map[uint64]func(device.Device)
	next          uint64
	sequence      uint64
	cancel        context.CancelFunc
	lifecycle     context.Context
	done          chan struct{}

	requests       atomic.Uint64
	events         atomic.Uint64
	errors         atomic.Uint64
	localRequests  atomic.Uint64
	localFailures  atomic.Uint64
	cloudRequests  atomic.Uint64
	cloudFallbacks atomic.Uint64
}

var (
	_ providersdk.Provider             = (*Provider)(nil)
	_ providersdk.LiveReconfigurer     = (*Provider)(nil)
	_ providersdk.CredentialMaintainer = (*Provider)(nil)
	_ providersdk.Discoverer           = (*Provider)(nil)
	_ providersdk.SourceCataloger      = (*Provider)(nil)
)

func NewProviderFromConfig(item providerconfig.Config) (*Provider, error) {
	return NewProviderFromConfigWithSpecResolver(item, NewSpecResolver(nil))
}

func NewProviderFromConfigWithSpecResolver(item providerconfig.Config, resolver *SpecResolver) (*Provider, error) {
	config, brokerURL, tlsConfig, err := decodeConfig(item)
	if err != nil {
		return nil, err
	}
	factory := func() hubClient { return newMIPSClient(config, brokerURL, tlsConfig) }
	provider, err := newProviderWithResolver(item.ID, item.Name, config, factory, resolver)
	if err != nil {
		return nil, err
	}
	if config.OAuth != nil && strings.TrimSpace(config.OAuth.AccessToken) != "" {
		provider.cloud = newHTTPHomeCloudClient(*config.OAuth, &http.Client{Timeout: config.requestTimeout()})
	}
	return provider, nil
}

func newProvider(id, name string, config Config, factory clientFactory) (*Provider, error) {
	return newProviderWithResolver(id, name, config, factory, nil)
}

func newProviderWithResolver(id, name string, config Config, factory clientFactory, resolver *SpecResolver) (*Provider, error) {
	provider := &Provider{id: id, name: name, config: config, factory: factory, resolver: resolver, devices: make(map[string]device.Device), byDID: make(map[string]string), routes: make(map[string]deviceRoute), rawProperties: make(map[string]PropertyMapping), rawActions: make(map[string]ActionMapping), catalog: make(map[string]providersdk.SourceCatalogMetadata), valueStatus: make(map[string]providersdk.SourceValueStatus), listeners: make(map[uint64]func(device.Device))}
	for _, configured := range config.Devices {
		item := buildDevice(id, configured)
		item.RuntimeMode = device.RuntimeModePending
		if err := item.NormalizeModelParameters(); err != nil {
			return nil, fmt.Errorf("Xiaomi device %q model mapping: %w", configured.ID, err)
		}
		provider.devices[item.ID] = item
		provider.byDID[configured.DID] = item.ID
		provider.catalog[item.ID] = providersdk.SourceCatalogMetadata{Complete: false, Source: "configured-mapping-fallback", Model: configured.Model, Error: "MIoT Spec has not been loaded"}
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
		// Initialize is a lifecycle assertion rather than a request to open a
		// second MQTT session. Runtime reconciliation and retry scheduling may
		// converge on an already healthy instance, so match the idempotent
		// contract used by the other long-running Providers.
		return nil
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
	directory, err := p.refreshDirectory(requestCtx)
	requestCancel()
	if err != nil {
		_ = client.Close(context.Background())
		cancel()
		p.mu.Lock()
		p.client, p.cancel, p.lifecycle = nil, nil, nil
		p.mu.Unlock()
		return fmt.Errorf("request Xiaomi device directory: %w", err)
	}
	p.loadSourceSpecs(ctx, directory)
	// Initial reads run concurrently with a bounded worker count. Individual
	// unavailable properties remain at their typed zero value and are retried by
	// the calibration loop instead of preventing the provider from starting.
	p.refreshAll(ctx, true)
	go p.pollLoop(lifecycle)
	return nil
}

// refreshDirectory merges the gateway directory with the account directory.
// A central Provider can therefore expose cloud-only devices while preserving
// gateway availability as the authoritative local-route signal.
func (p *Provider) refreshDirectory(ctx context.Context) ([]HubDevice, error) {
	client, localClientErr := p.currentClient()
	var local []HubDevice
	var localErr error
	if localClientErr == nil {
		p.requests.Add(1)
		p.localRequests.Add(1)
		raw, err := client.DeviceList(ctx)
		if err == nil {
			err = responseOK(raw)
		}
		if err == nil {
			local, err = parseHubDeviceList(raw)
		}
		localErr = err
		if err != nil {
			p.localFailures.Add(1)
		}
	} else {
		localErr = localClientErr
	}

	p.mu.RLock()
	cloud := p.cloud
	p.mu.RUnlock()
	var cloudItems []HubDevice
	var cloudErr error
	if cloud != nil {
		p.requests.Add(1)
		p.cloudRequests.Add(1)
		cloudItems, cloudErr = cloud.DeviceList(ctx)
	}
	if localErr != nil && (cloud == nil || cloudErr != nil) {
		p.errors.Add(1)
		if cloudErr != nil {
			return nil, fmt.Errorf("gateway directory: %v; cloud directory: %w", localErr, cloudErr)
		}
		return nil, localErr
	}
	if cloudErr != nil {
		p.errors.Add(1)
	}
	directory := mergeHubAndCloudDevices(local, cloudItems)
	p.mu.Lock()
	p.directory = append([]HubDevice(nil), directory...)
	p.routes = make(map[string]deviceRoute, len(directory))
	for _, item := range directory {
		p.routes[item.DID] = deviceRoute{local: item.GatewayAvailable && item.LocalControlAvailable, cloud: item.CloudAvailable, push: item.GatewayAvailable && item.PushAvailable}
	}
	p.mu.Unlock()
	return directory, nil
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
		// Nothing is live to reconfigure. Let the manager initialize the
		// replacement instance through its normal lifecycle instead.
		return false, nil
	}
	updated := make(map[string]device.Device, len(next.devices))
	for id, item := range next.devices {
		item = item.Clone()
		if previous, exists := p.devices[id]; exists {
			item = preserveDeviceState(item, previous)
		}
		updated[id] = item
	}
	p.name, p.config, p.factory, p.devices = next.name, next.config, next.factory, updated
	p.rawProperties, p.rawActions, p.catalog = next.rawProperties, next.rawActions, next.catalog
	p.byDID = make(map[string]string, len(next.byDID))
	for did, id := range next.byDID {
		p.byDID[did] = id
	}
	lifecycle := p.lifecycle
	timeout := p.config.requestTimeout()
	if p.cloud == nil && next.cloud != nil {
		p.cloud = next.cloud
	}
	cloud := p.cloud
	oauth := p.config.OAuth
	p.mu.Unlock()
	if cloud != nil && oauth != nil {
		cloud.UpdateOAuth(*oauth)
	}

	// Refresh in the provider lifecycle so saving a large mapping set is not
	// coupled to the HTTP request deadline. Each property update is broadcast as
	// it arrives; unavailable properties retain their previous in-memory value.
	if lifecycle != nil {
		go func() {
			refreshCtx, cancel := context.WithTimeout(lifecycle, timeout)
			defer cancel()
			p.refreshSourceCatalog(refreshCtx)
			p.refreshAll(refreshCtx, true)
		}()
	}
	return true, nil
}

func equalConnectionConfig(left, right Config) bool {
	left.Devices, right.Devices = nil, nil
	left.ClientCertificate, right.ClientCertificate = "", ""
	left.PrivateKey, right.PrivateKey = "", ""
	if left.OAuth != nil && right.OAuth != nil {
		leftOAuth, rightOAuth := *left.OAuth, *right.OAuth
		leftOAuth.AccessToken, rightOAuth.AccessToken = "", ""
		leftOAuth.RefreshToken, rightOAuth.RefreshToken = "", ""
		leftOAuth.RefreshAfter, rightOAuth.RefreshAfter = 0, 0
		leftOAuth.ExpiresAt, rightOAuth.ExpiresAt = 0, 0
		left.OAuth, right.OAuth = &leftOAuth, &rightOAuth
	}
	return reflect.DeepEqual(left, right)
}

func preserveDeviceState(next, previous device.Device) device.Device {
	next.Availability, next.Online = previous.Availability, previous.Online
	next.Sequence, next.LastUpdateAt = previous.Sequence, previous.LastUpdateAt
	next.RuntimeMode = previous.RuntimeMode
	for _, endpoint := range next.Endpoints {
		for _, capability := range endpoint.Capabilities {
			for _, property := range capability.Properties {
				old, ok := previous.Property(endpoint.ID, capability.ID, property.Definition.ID)
				if ok && old.Definition.Type == property.Definition.Type {
					setObservedProperty(&next, endpoint.ID, capability.ID, property.Definition.ID, old.Value)
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

// DiscoverHubDevices reads the active central gateway and merges its directory
// with the OAuth account directory. The name is kept for API compatibility.
func (p *Provider) DiscoverHubDevices(ctx context.Context) ([]HubDevice, error) {
	return p.refreshDirectory(ctx)
}

func (p *Provider) ReadProperty(ctx context.Context, request providersdk.PropertyReadRequest) (device.Property, error) {
	configured, mapping, err := p.mappingForProperty(request.DeviceID, request.EndpointID, request.CapabilityID, request.PropertyID)
	if err != nil {
		return device.Property{}, err
	}
	if mapping.Readable != nil && !*mapping.Readable {
		return device.Property{}, providersdk.ErrPropertyUnsupported
	}
	raw, err := p.readPropertyRaw(ctx, configured, mapping)
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
	// Observed MIoT values can use an out-of-range sentinel (for example a
	// target temperature of 0 while an air conditioner is off). The source
	// snapshot widens its displayed bounds to remain structurally valid, but
	// writes must still honor the original MIoT specification.
	property.Definition.Min = mapping.Min
	property.Definition.Max = mapping.Max
	property.Definition.Step = mapping.Step
	property.Value = request.Value
	if err := property.Validate(); err != nil {
		return device.Device{}, providersdk.ErrPropertyInvalid
	}
	raw, err := encodePropertyValue(mapping, request.Value)
	if err != nil {
		return device.Device{}, providersdk.ErrPropertyInvalid
	}
	if err := p.writePropertyRaw(ctx, configured, mapping, raw); err != nil {
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
	if err := p.executeActionRaw(ctx, configured, action, input); err != nil {
		p.errors.Add(1)
		return device.Device{}, providersdk.ErrWriteRejected
	}
	item, _ := p.snapshot(configured.ID)
	return item, nil
}

func (p *Provider) readPropertyRaw(ctx context.Context, configured DeviceConfig, mapping PropertyMapping) (any, error) {
	mode, route, local, cloud := p.routeFor(configured)
	var localErr error
	if mode != connectionModeCloud && route.local && local != nil {
		p.requests.Add(1)
		p.localRequests.Add(1)
		reply, err := local.GetProperty(ctx, configured.DID, mapping.SIID, mapping.PIID)
		if err == nil {
			var value any
			value, err = responseValue(reply)
			if err == nil {
				p.setRuntimeMode(configured.ID, device.RuntimeModeLocal)
				return value, nil
			}
		}
		localErr = err
		p.localFailures.Add(1)
		if mode == connectionModeLocal {
			return nil, err
		}
	} else if mode == connectionModeLocal {
		return nil, errors.New("device is not available through the Xiaomi central gateway")
	}
	if cloud == nil || !route.cloud {
		if localErr != nil {
			return nil, localErr
		}
		return nil, errors.New("Xiaomi Home cloud route is unavailable")
	}
	if mode == connectionModeAuto {
		p.cloudFallbacks.Add(1)
	}
	p.requests.Add(1)
	p.cloudRequests.Add(1)
	result, err := cloud.GetProperties(ctx, []cloudProperty{{DID: configured.DID, SIID: mapping.SIID, PIID: mapping.PIID}})
	if err != nil || len(result) != 1 || !miotResultOK(result[0].Code) {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("Xiaomi Home cloud property response was incomplete")
	}
	p.setRuntimeMode(configured.ID, device.RuntimeModeCloud)
	return result[0].Value, nil
}

func (p *Provider) writePropertyRaw(ctx context.Context, configured DeviceConfig, mapping PropertyMapping, value any) error {
	mode, route, local, cloud := p.routeFor(configured)
	var localErr error
	if mode != connectionModeCloud && route.local && local != nil {
		p.requests.Add(1)
		p.localRequests.Add(1)
		reply, err := local.SetProperty(ctx, configured.DID, mapping.SIID, mapping.PIID, value)
		if err == nil {
			err = responseOK(reply)
			if err == nil {
				p.setRuntimeMode(configured.ID, device.RuntimeModeLocal)
				return nil
			}
		}
		localErr = err
		p.localFailures.Add(1)
		if mode == connectionModeLocal {
			return err
		}
	} else if mode == connectionModeLocal {
		return errors.New("device is not available through the Xiaomi central gateway")
	}
	if cloud == nil || !route.cloud {
		if localErr != nil {
			return localErr
		}
		return errors.New("Xiaomi Home cloud route is unavailable")
	}
	if mode == connectionModeAuto {
		p.cloudFallbacks.Add(1)
	}
	p.requests.Add(1)
	p.cloudRequests.Add(1)
	result, err := cloud.SetProperties(ctx, []cloudProperty{{DID: configured.DID, SIID: mapping.SIID, PIID: mapping.PIID, Value: value}})
	if err != nil {
		return err
	}
	if len(result) != 1 || !miotResultOK(result[0].Code) {
		return errors.New("Xiaomi Home cloud rejected the property write")
	}
	p.setRuntimeMode(configured.ID, device.RuntimeModeCloud)
	return nil
}

func (p *Provider) executeActionRaw(ctx context.Context, configured DeviceConfig, action ActionMapping, input []any) error {
	mode, route, local, cloud := p.routeFor(configured)
	var localErr error
	if mode != connectionModeCloud && route.local && local != nil {
		p.requests.Add(1)
		p.localRequests.Add(1)
		reply, err := local.Action(ctx, configured.DID, action.SIID, action.AIID, input)
		if err == nil {
			err = responseOK(reply)
			if err == nil {
				p.setRuntimeMode(configured.ID, device.RuntimeModeLocal)
				return nil
			}
		}
		localErr = err
		p.localFailures.Add(1)
		if mode == connectionModeLocal {
			return err
		}
	} else if mode == connectionModeLocal {
		return errors.New("device is not available through the Xiaomi central gateway")
	}
	if cloud == nil || !route.cloud {
		if localErr != nil {
			return localErr
		}
		return errors.New("Xiaomi Home cloud route is unavailable")
	}
	if mode == connectionModeAuto {
		p.cloudFallbacks.Add(1)
	}
	p.requests.Add(1)
	p.cloudRequests.Add(1)
	if err := cloud.Action(ctx, cloudAction{DID: configured.DID, SIID: action.SIID, AIID: action.AIID, Input: input}); err != nil {
		return err
	}
	p.setRuntimeMode(configured.ID, device.RuntimeModeCloud)
	return nil
}

func (p *Provider) routeFor(configured DeviceConfig) (string, deviceRoute, hubClient, homeCloudClient) {
	p.mu.RLock()
	route, known := p.routes[configured.DID]
	local, cloud := p.client, p.cloud
	p.mu.RUnlock()
	if !known {
		// Preserve compatibility with gateways that omit route capability fields.
		route.local, route.cloud, route.push = local != nil, cloud != nil, true
	}
	// The account directory is useful discovery metadata, not an authorization
	// boundary. Some Wi-Fi and shared devices can be controlled through MIoT
	// HTTP even when a particular directory response temporarily omits them.
	// Keep auto fallback available whenever this Provider owns a valid OAuth
	// cloud client; an actual cloud rejection is still returned to the caller.
	if cloud != nil {
		route.cloud = true
	}
	mode := configured.ConnectionMode
	if mode == "" {
		mode = connectionModeAuto
	}
	if mode == connectionModeCloud {
		route.local = false
	}
	if mode == connectionModeLocal {
		route.cloud = false
	}
	return mode, route, local, cloud
}

func miotResultOK(code int) bool { return code == 0 || code == 1 }

func (p *Provider) setRuntimeMode(id string, mode device.RuntimeMode) {
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
	p.mu.Unlock()
	p.broadcast(item.Clone())
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
	return map[string]uint64{
		"requests": p.requests.Load(), "events": p.events.Load(), "errors": p.errors.Load(),
		"localRequests": p.localRequests.Load(), "localFailures": p.localFailures.Load(),
		"cloudRequests": p.cloudRequests.Load(), "cloudFallbacks": p.cloudFallbacks.Load(),
	}
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
			p.refreshAll(refreshCtx, false)
			cancel()
		}
	}
}

func (p *Provider) refreshAll(ctx context.Context, initial bool) {
	jobs := make(chan DeviceConfig)
	var workers sync.WaitGroup
	workerCount := 4
	for index := 0; index < workerCount; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for configured := range jobs {
				for _, mapping := range p.readableMappings(configured) {
					readable := mapping.Readable == nil || *mapping.Readable
					if !readable {
						continue
					}
					_, err := p.ReadProperty(ctx, providersdk.PropertyReadRequest{DeviceID: configured.ID, EndpointID: mapping.EndpointID, CapabilityID: mapping.CapabilityID, PropertyID: mapping.PropertyID})
					if err != nil {
						p.markValueError(configured.ID, mapping, err)
					}
				}
			}
		}()
	}
	p.mu.RLock()
	configuredDevices := append([]DeviceConfig(nil), p.config.Devices...)
	p.mu.RUnlock()
sendLoop:
	for _, configured := range configuredDevices {
		if !initial && !p.shouldCalibrate(configured) {
			continue
		}
		select {
		case jobs <- configured:
		case <-ctx.Done():
			break sendLoop
		}
	}
	close(jobs)
	workers.Wait()
}

func (p *Provider) readableMappings(configured DeviceConfig) []PropertyMapping {
	p.mu.RLock()
	rawMappings := make([]PropertyMapping, 0)
	prefix := configured.ID + "\x00"
	for key, mapping := range p.rawProperties {
		if strings.HasPrefix(key, prefix) {
			rawMappings = append(rawMappings, mapping)
		}
	}
	p.mu.RUnlock()
	result := append([]PropertyMapping(nil), configured.Properties...)
	result = append(result, rawMappings...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].SIID != result[j].SIID {
			return result[i].SIID < result[j].SIID
		}
		return result[i].PIID < result[j].PIID
	})
	return result
}

func (p *Provider) shouldCalibrate(configured DeviceConfig) bool {
	mode, route, _, _ := p.routeFor(configured)
	if mode == connectionModeCloud || !route.local || !route.push {
		return true
	}
	p.mu.RLock()
	item, exists := p.devices[configured.ID]
	p.mu.RUnlock()
	return !exists || item.RuntimeMode != device.RuntimeModeLocal
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
		p.setRuntimeMode(configured.ID, device.RuntimeModeLocal)
		p.broadcast(p.updateProperty(configured.ID, mapping, value))
	}
}

func (p *Provider) updateProperty(id string, mapping PropertyMapping, value device.PropertyValue) device.Device {
	p.mu.Lock()
	item := p.devices[id]
	setObservedProperty(&item, mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID, value)
	p.valueStatus[sourcePropertyKey(id, mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)] = providersdk.SourceValueStatus{Known: true, Available: true, ObservedAt: time.Now().UTC()}
	p.sequence++
	item.Sequence = p.sequence
	item.LastUpdateAt = time.Now().UTC()
	item.SetOnline(true)
	p.devices[id] = item
	p.mu.Unlock()
	return item.Clone()
}

func (p *Provider) markValueError(id string, mapping PropertyMapping, cause error) {
	p.mu.Lock()
	key := sourcePropertyKey(id, mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)
	status := p.valueStatus[key]
	status.Available, status.Error = false, cause.Error()
	p.valueStatus[key] = status
	p.mu.Unlock()
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
	raw, rawExists := p.rawProperties[sourcePropertyKey(id, endpoint, capability, property)]
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
		if rawExists {
			return configured, raw, nil
		}
		return DeviceConfig{}, PropertyMapping{}, providersdk.ErrPropertyUnsupported
	}
	return DeviceConfig{}, PropertyMapping{}, providersdk.ErrDeviceNotFound
}

func (p *Provider) mappingForMIoT(id string, siid, piid int) (DeviceConfig, PropertyMapping, error) {
	p.mu.RLock()
	configuredDevices := append([]DeviceConfig(nil), p.config.Devices...)
	rawMappings := make([]PropertyMapping, 0, len(p.rawProperties))
	for key, mapping := range p.rawProperties {
		if strings.HasPrefix(key, id+"\x00") {
			rawMappings = append(rawMappings, mapping)
		}
	}
	p.mu.RUnlock()
	for _, configured := range configuredDevices {
		if configured.ID == id {
			for _, mapping := range configured.Properties {
				if mapping.SIID == siid && mapping.PIID == piid {
					return configured, mapping, nil
				}
			}
			for _, mapping := range rawMappings {
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
	raw, rawExists := p.rawActions[sourceActionKey(id, endpoint, capability, command)]
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
		if rawExists {
			return configured, raw, nil
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
		return device.IntValue(int64(boundedZero(mapping.Min, mapping.Max)))
	case device.ValueTypeNumber:
		return device.NumberValue(boundedZero(mapping.Min, mapping.Max))
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

func boundedZero(minimum, maximum *float64) float64 {
	value := float64(0)
	if minimum != nil && value < *minimum {
		value = *minimum
	}
	if maximum != nil && value > *maximum {
		value = *maximum
	}
	return value
}

// setObservedProperty preserves the exact native value while keeping the
// transport model internally consistent. Some MIoT devices publish sentinel
// values outside the value-range declared by their public specification.
func setObservedProperty(item *device.Device, endpointID, capabilityID, propertyID string, value device.PropertyValue) bool {
	for endpointIndex := range item.Endpoints {
		endpoint := &item.Endpoints[endpointIndex]
		if endpoint.ID != endpointID {
			continue
		}
		for capabilityIndex := range endpoint.Capabilities {
			capability := &endpoint.Capabilities[capabilityIndex]
			if capability.ID != capabilityID {
				continue
			}
			for propertyIndex := range capability.Properties {
				property := &capability.Properties[propertyIndex]
				if property.Definition.ID != propertyID {
					continue
				}
				property.Value = value
				switch value.Type {
				case device.ValueTypeInt:
					if value.Int != nil {
						widenObservedRange(&property.Definition, float64(*value.Int))
					}
				case device.ValueTypeNumber:
					if value.Number != nil {
						widenObservedRange(&property.Definition, *value.Number)
					}
				}
				return true
			}
		}
	}
	return false
}

func widenObservedRange(definition *device.PropertyDefinition, value float64) {
	if definition.Min != nil && value < *definition.Min {
		minimum := value
		definition.Min = &minimum
	}
	if definition.Max != nil && value > *definition.Max {
		maximum := value
		definition.Max = &maximum
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
