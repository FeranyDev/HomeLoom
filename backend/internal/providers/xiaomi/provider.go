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
type recoveryClientFactory func(Config) (hubClient, error)

type runtimeConfigChange struct {
	previous    json.RawMessage
	replacement json.RawMessage
}

type deviceRoute struct {
	local bool
	cloud bool
	push  bool
}

type observationSource uint8

const (
	observationUnknown observationSource = iota
	observationCloudHTTP
	observationCloudMQTT
	observationLocalMQTT
)

type propertyObservation struct {
	Source     observationSource
	ObservedAt time.Time
	ReceivedAt time.Time
	Value      string
}

type propertyReadFailure struct {
	Count       uint
	NextRetryAt time.Time
}

const (
	defaultCloudDisconnectGrace      = 30 * time.Second
	defaultCloudDirectoryReconcile   = 30 * time.Minute
	maximumPropertyReadRetryInterval = 15 * time.Minute
)

type Provider struct {
	id                  string
	name                string
	config              Config
	factory             clientFactory
	recoveryFactory     recoveryClientFactory
	discoverGateways    func(context.Context) ([]Gateway, error)
	resolver            *SpecResolver
	configDocument      json.RawMessage
	pendingConfigChange *runtimeConfigChange
	configChangeHandler func(previous, replacement json.RawMessage)
	remoteTokenRevoker  oauthTokenRevoker

	mu                       sync.RWMutex
	client                   hubClient
	cloud                    homeCloudClient
	cloudMIPS                cloudMIPSClient
	devices                  map[string]device.Device
	sourceDevices            map[string]device.Device
	byDID                    map[string]string
	routes                   map[string]deviceRoute
	directory                []HubDevice
	rawProperties            map[string]PropertyMapping
	rawActions               map[string]ActionMapping
	propertyInterests        map[string]struct{}
	catalog                  map[string]providersdk.SourceCatalogMetadata
	valueStatus              map[string]providersdk.SourceValueStatus
	observations             map[string]propertyObservation
	propertyFailures         map[string]propertyReadFailure
	listeners                map[uint64]func(device.Device)
	eventListeners           map[uint64]func(providersdk.DeviceEvent)
	next                     uint64
	nextEventListener        uint64
	sequence                 uint64
	eventSequence            uint64
	cancel                   context.CancelFunc
	lifecycle                context.Context
	done                     chan struct{}
	cloudEvents              chan cloudMIPSMessage
	cloudConnections         chan cloudMIPSConnectionEvent
	directoryChanges         chan struct{}
	pollIntervalChanges      chan time.Duration
	cloudDone                chan struct{}
	backgroundWG             sync.WaitGroup
	cloudDisconnectGrace     time.Duration
	cloudDirectoryInterval   time.Duration
	directoryRefreshDebounce time.Duration
	cloudConnectionEpoch     atomic.Uint64

	requests                 atomic.Uint64
	events                   atomic.Uint64
	errors                   atomic.Uint64
	localRequests            atomic.Uint64
	localFailures            atomic.Uint64
	cloudRequests            atomic.Uint64
	cloudFallbacks           atomic.Uint64
	cloudDropped             atomic.Uint64
	cloudDuplicates          atomic.Uint64
	cloudHTTPInitialReads    atomic.Uint64
	cloudHTTPReconcileReads  atomic.Uint64
	cloudHTTPReadFailures    atomic.Uint64
	cloudReconciling         atomic.Bool
	directoryRefreshing      atomic.Bool
	directoryRefreshes       atomic.Uint64
	directoryRefreshFailures atomic.Uint64
	propertyReadBackoffs     atomic.Uint64
	cloudDisconnectExpiries  atomic.Uint64
}

var (
	_ providersdk.Provider              = (*Provider)(nil)
	_ providersdk.LiveReconfigurer      = (*Provider)(nil)
	_ providersdk.CredentialMaintainer  = (*Provider)(nil)
	_ providersdk.CredentialRevoker     = (*Provider)(nil)
	_ providersdk.Discoverer            = (*Provider)(nil)
	_ providersdk.MediaSourceDiscoverer = (*Provider)(nil)
	_ providersdk.MediaSourceRefresher  = (*Provider)(nil)
	_ providersdk.MediaAuthorizer       = (*Provider)(nil)
	_ providersdk.SourceCataloger       = (*Provider)(nil)
	_ providersdk.HiddenDeviceSource    = (*Provider)(nil)
	_ providersdk.DeviceEventSubscriber = (*Provider)(nil)
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
	provider.configDocument = append(json.RawMessage(nil), item.Config...)
	provider.recoveryFactory = newRecoveryMIPSClient
	if config.OAuth != nil && strings.TrimSpace(config.OAuth.AccessToken) != "" {
		provider.cloud = newHTTPHomeCloudClient(*config.OAuth, &http.Client{Timeout: config.requestTimeout()})
		provider.cloudMIPS, err = newCloudMIPSClient(*config.OAuth, config.requestTimeout())
		if err != nil {
			return nil, fmt.Errorf("configure Xiaomi cloud MQTT: %w", err)
		}
	}
	return provider, nil
}

func newProvider(id, name string, config Config, factory clientFactory) (*Provider, error) {
	return newProviderWithResolver(id, name, config, factory, nil)
}

func newProviderWithResolver(id, name string, config Config, factory clientFactory, resolver *SpecResolver) (*Provider, error) {
	configDocument, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	provider := &Provider{id: id, name: name, config: config, factory: factory, resolver: resolver, discoverGateways: DiscoverGateways, configDocument: configDocument, remoteTokenRevoker: revokeOAuthTokens, devices: make(map[string]device.Device), sourceDevices: make(map[string]device.Device), byDID: make(map[string]string), routes: make(map[string]deviceRoute), rawProperties: make(map[string]PropertyMapping), rawActions: make(map[string]ActionMapping), propertyInterests: make(map[string]struct{}), catalog: make(map[string]providersdk.SourceCatalogMetadata), valueStatus: make(map[string]providersdk.SourceValueStatus), observations: make(map[string]propertyObservation), propertyFailures: make(map[string]propertyReadFailure), listeners: make(map[uint64]func(device.Device)), eventListeners: make(map[uint64]func(providersdk.DeviceEvent)), cloudDisconnectGrace: defaultCloudDisconnectGrace, cloudDirectoryInterval: defaultCloudDirectoryReconcile, directoryRefreshDebounce: 500 * time.Millisecond}
	for _, configured := range config.Devices {
		item := buildDevice(id, configured)
		applyCentralStalePolicy(&item, config.pollInterval())
		item.RuntimeMode = device.RuntimeModePending
		item.StateTransport = device.StateTransportPending
		if err := item.NormalizeModelParameters(); err != nil {
			return nil, fmt.Errorf("Xiaomi device %q model mapping: %w", configured.ID, err)
		}
		provider.devices[item.ID] = item
		provider.sourceDevices[item.ID] = item.Clone()
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
	if p.config.CredentialsRevoked {
		p.mu.Unlock()
		return ErrCredentialsRevoked
	}
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
	p.cloudEvents, p.cloudDone = make(chan cloudMIPSMessage, cloudMIPSEventQueue), make(chan struct{})
	p.cloudConnections = make(chan cloudMIPSConnectionEvent, 16)
	p.directoryChanges = make(chan struct{}, 1)
	p.pollIntervalChanges = make(chan time.Duration, 1)
	p.mu.Unlock()
	if err := client.Connect(lifecycle, ctx); err != nil {
		if recovered, recoveredConfig, recoveredFactory, recoveredOK := p.recoverGatewayAddress(ctx, lifecycle, client, err); recoveredOK {
			client = recovered
			p.mu.Lock()
			p.client, p.config, p.factory = recovered, recoveredConfig, recoveredFactory
			handler, change := p.recordGatewayAddressRecoveryLocked(recoveredConfig)
			p.mu.Unlock()
			if handler != nil && change != nil {
				go handler(change.previous, change.replacement)
			}
		} else {
			cancel()
			p.mu.Lock()
			p.client, p.cancel, p.lifecycle = nil, nil, nil
			p.mu.Unlock()
			return fmt.Errorf("connect Xiaomi central hub: %w", err)
		}
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
	go p.cloudEventLoop(lifecycle)
	p.mu.RLock()
	cloudMIPS := p.cloudMIPS
	p.mu.RUnlock()
	if cloudMIPS != nil {
		cloudMIPS.SetIncomingHandler(p.enqueueCloudMIPS)
		cloudMIPS.SetConnectionHandler(p.enqueueCloudConnection)
		if err := cloudMIPS.ReplaceDevices(ctx, p.cloudSubscriptionDIDs()); err != nil {
			p.errors.Add(1)
		}
		// Cloud push is an optimization over the still-available HTTP route.
		// A temporary broker outage must not prevent cloud-only devices from
		// initializing and being controlled while autopaho reconnects.
		if err := cloudMIPS.Connect(lifecycle, ctx); err != nil {
			p.errors.Add(1)
		}
	}
	// Initial reads run concurrently with a bounded worker count. Individual
	// unavailable properties remain at their typed zero value and are retried by
	// the calibration loop instead of preventing the provider from starting.
	p.refreshAll(lifecycle, true)
	go p.pollLoop(lifecycle)
	return nil
}

const gatewayAddressRecoveryTimeout = 5 * time.Second

// recoverGatewayAddress retries a timed-out local connection once using the
// mDNS record of the already-selected gateway DID. It intentionally does not
// match by name, role alone, or the first scan result: replacing an address
// with a different central hub would also replace the mTLS/MIPS trust target.
func (p *Provider) recoverGatewayAddress(ctx, lifecycle context.Context, current hubClient, cause error) (hubClient, Config, clientFactory, bool) {
	if !errors.Is(cause, ErrGatewayInitialConnectionTimeout) {
		return nil, Config{}, nil, false
	}
	p.mu.RLock()
	config, recoveryFactory, discover := p.config, p.recoveryFactory, p.discoverGateways
	p.mu.RUnlock()
	if strings.TrimSpace(config.GatewayDID) == "" || recoveryFactory == nil || discover == nil {
		return nil, Config{}, nil, false
	}

	closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), gatewayAddressRecoveryTimeout)
	_ = current.Close(closeCtx)
	closeCancel()

	discoveryCtx, discoveryCancel := context.WithTimeout(ctx, gatewayAddressRecoveryTimeout)
	gateways, err := discover(discoveryCtx)
	discoveryCancel()
	if err != nil {
		return nil, Config{}, nil, false
	}
	for _, gateway := range gateways {
		if gateway.DID != config.GatewayDID || !gateway.MQTTEnabled {
			continue
		}
		host := strings.TrimSpace(gateway.PreferredAddress())
		if host == "" || (host == config.Host && (gateway.Port == 0 || gateway.Port == config.Port)) {
			continue
		}
		recovered := config
		recovered.Host = host
		if gateway.Port > 0 {
			recovered.Port = gateway.Port
		}
		candidate, candidateErr := recoveryFactory(recovered)
		if candidateErr != nil {
			continue
		}
		candidate.SetIncomingHandler(p.handleIncoming)
		if candidateErr = candidate.Connect(lifecycle, ctx); candidateErr != nil {
			candidateCloseCtx, candidateCloseCancel := context.WithTimeout(context.WithoutCancel(ctx), gatewayAddressRecoveryTimeout)
			_ = candidate.Close(candidateCloseCtx)
			candidateCloseCancel()
			continue
		}
		return candidate, recovered, newRecoveryClientFactory(recovered, recoveryFactory), true
	}
	return nil, Config{}, nil, false
}

func newRecoveryMIPSClient(config Config) (hubClient, error) {
	brokerURL, err := config.validate()
	if err != nil {
		return nil, err
	}
	tlsConfig, err := config.tlsConfig()
	if err != nil {
		return nil, err
	}
	return newMIPSClient(config, brokerURL, tlsConfig), nil
}

func newRecoveryClientFactory(config Config, factory recoveryClientFactory) clientFactory {
	return func() hubClient {
		client, err := factory(config)
		if err != nil {
			return failedHubClient{err: err}
		}
		return client
	}
}

type failedHubClient struct{ err error }

func (c failedHubClient) Connect(context.Context, context.Context) error { return c.err }
func (failedHubClient) Close(context.Context) error                      { return nil }
func (failedHubClient) DeviceList(context.Context) (json.RawMessage, error) {
	return nil, errors.New("Xiaomi gateway client was not created")
}
func (failedHubClient) GetProperty(context.Context, string, int, int) (json.RawMessage, error) {
	return nil, errors.New("Xiaomi gateway client was not created")
}
func (failedHubClient) SetProperty(context.Context, string, int, int, any) (json.RawMessage, error) {
	return nil, errors.New("Xiaomi gateway client was not created")
}
func (failedHubClient) Action(context.Context, string, int, int, []any) (json.RawMessage, error) {
	return nil, errors.New("Xiaomi gateway client was not created")
}
func (failedHubClient) SetIncomingHandler(func(hubIncoming)) {}

// SetRuntimeConfigChangeHandler lets the application persist an address
// recovered by this running Provider. If recovery happened during process
// startup before ProviderService existed, the latest update is delivered as
// soon as the handler is attached.
func (p *Provider) SetRuntimeConfigChangeHandler(handler func(previous, replacement json.RawMessage)) {
	p.mu.Lock()
	p.configChangeHandler = handler
	pending := p.pendingConfigChange
	if handler != nil {
		p.pendingConfigChange = nil
	}
	p.mu.Unlock()
	if handler != nil && pending != nil {
		go handler(pending.previous, pending.replacement)
	}
}

func (p *Provider) recordGatewayAddressRecoveryLocked(recovered Config) (func(previous, replacement json.RawMessage), *runtimeConfigChange) {
	previous := append(json.RawMessage(nil), p.configDocument...)
	var document map[string]json.RawMessage
	if err := json.Unmarshal(previous, &document); err != nil || document == nil {
		return nil, nil
	}
	host, err := json.Marshal(recovered.Host)
	if err != nil {
		return nil, nil
	}
	port, err := json.Marshal(recovered.Port)
	if err != nil {
		return nil, nil
	}
	document["host"], document["port"] = host, port
	replacement, err := json.Marshal(document)
	if err != nil {
		return nil, nil
	}
	p.configDocument = replacement
	change := &runtimeConfigChange{previous: previous, replacement: replacement}
	if p.configChangeHandler == nil {
		p.pendingConfigChange = change
		return nil, change
	}
	return p.configChangeHandler, change
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
	p.cloudConnectionEpoch.Add(1)
	p.mu.Lock()
	client, cloudMIPS, cancel, done, cloudDone := p.client, p.cloudMIPS, p.cancel, p.done, p.cloudDone
	p.client, p.cancel, p.lifecycle = nil, nil, nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	var err error
	if cloudMIPS != nil {
		if cloudErr := cloudMIPS.Close(ctx); cloudErr != nil {
			err = cloudErr
		}
	}
	if client != nil {
		if localErr := client.Close(ctx); localErr != nil && err == nil {
			err = localErr
		}
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
	if cloudDone != nil {
		select {
		case <-cloudDone:
		case <-ctx.Done():
			if err == nil {
				err = ctx.Err()
			}
		}
	}
	backgroundDone := make(chan struct{})
	go func() {
		p.backgroundWG.Wait()
		close(backgroundDone)
	}()
	select {
	case <-backgroundDone:
	case <-ctx.Done():
		if err == nil {
			err = ctx.Err()
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
	updatedSources := make(map[string]device.Device, len(next.sourceDevices))
	for id, item := range next.devices {
		item = item.Clone()
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
	p.name, p.config, p.factory, p.configDocument, p.devices, p.sourceDevices = next.name, next.config, next.factory, append(json.RawMessage(nil), next.configDocument...), updated, updatedSources
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
	cloud, cloudMIPS, pollIntervalChanges := p.cloud, p.cloudMIPS, p.pollIntervalChanges
	oauth := p.config.OAuth
	p.mu.Unlock()
	if pollIntervalChanges != nil {
		select {
		case pollIntervalChanges <- next.config.pollInterval():
		default:
			select {
			case <-pollIntervalChanges:
			default:
			}
			pollIntervalChanges <- next.config.pollInterval()
		}
	}
	if cloud != nil && oauth != nil {
		cloud.UpdateOAuth(*oauth)
	}
	if cloudMIPS != nil && oauth != nil {
		cloudMIPS.UpdateOAuth(*oauth)
		if err := cloudMIPS.ReplaceDevices(ctx, p.cloudSubscriptionDIDs()); err != nil {
			p.errors.Add(1)
		}
	}

	// Refresh in the provider lifecycle so saving a large mapping set is not
	// coupled to the HTTP request deadline. Each property update is broadcast as
	// it arrives; unavailable properties retain their previous in-memory value.
	if lifecycle != nil {
		go func() {
			catalogCtx, cancel := context.WithTimeout(lifecycle, timeout)
			p.refreshSourceCatalog(catalogCtx)
			cancel()
			p.refreshAll(lifecycle, true)
		}()
	}
	return true, nil
}

func equalConnectionConfig(left, right Config) bool {
	left.Devices, right.Devices = nil, nil
	left.PollIntervalSec, right.PollIntervalSec = 0, 0
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
	next.StateTransport = previous.StateTransport
	for _, endpoint := range next.Endpoints {
		for _, capability := range endpoint.Capabilities {
			for _, property := range capability.Properties {
				old, ok := previous.Property(endpoint.ID, capability.ID, property.Definition.ID)
				if ok && old.Definition.Type == property.Definition.Type {
					setObservedProperty(&next, endpoint.ID, capability.ID, property.Definition.ID, old.Value)
					setPropertyStateTransport(&next, endpoint.ID, capability.ID, property.Definition.ID, old.StateTransport)
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

// HiddenDeviceIDs returns central-hub camera identities. Xiaomi account and
// hub Providers only supply catalog, credentials and controls; the independent
// Camera Provider is always the sole user-visible media device owner.
func (p *Provider) HiddenDeviceIDs() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]string, 0)
	for _, item := range p.config.Devices {
		if item.Type == device.TypeCamera {
			result = append(result, item.ID)
		}
	}
	sort.Strings(result)
	return result
}

// SetPropertyInterests hot-applies the Provider-native fields used by explicit
// Provider -> unified-model bindings. Complete MIoT source catalogs remain
// available to the UI, but their otherwise unused non-notifiable properties no
// longer participate in the frequent state-compensation loop.
func (p *Provider) SetPropertyInterests(interests []providersdk.PropertyInterest) {
	next := make(map[string]struct{})
	for _, interest := range interests {
		if interest.ProviderID != "" && interest.ProviderID != p.id {
			continue
		}
		if interest.DeviceID == "" || interest.EndpointID == "" || interest.CapabilityID == "" || interest.PropertyID == "" {
			continue
		}
		next[sourcePropertyKey(interest.DeviceID, interest.EndpointID, interest.CapabilityID, interest.PropertyID)] = struct{}{}
	}
	p.mu.Lock()
	p.propertyInterests = next
	p.mu.Unlock()
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
	readStartedAt := time.Now().UTC()
	raw, err := p.readPropertyRaw(ctx, configured, mapping)
	if err != nil {
		p.errors.Add(1)
		return device.Property{}, err
	}
	value, err := decodePropertyValue(mapping, raw)
	if err != nil {
		return device.Property{}, providersdk.ErrPropertyInvalid
	}
	p.mu.RLock()
	runtimeMode := p.devices[configured.ID].RuntimeMode
	p.mu.RUnlock()
	source := observationCloudHTTP
	if runtimeMode == device.RuntimeModeLocal {
		source = observationLocalMQTT
	}
	updated, changed := p.applyObservedProperty(configured.ID, mapping, value, source, readStartedAt, runtimeMode)
	if changed {
		p.broadcast(updated)
	}
	property, ok := updated.Property(mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)
	if !ok {
		p.mu.RLock()
		source := p.sourceDevices[configured.ID].Clone()
		p.mu.RUnlock()
		property, _ = source.Property(mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)
	}
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
	if !ok {
		p.mu.RLock()
		source := p.sourceDevices[request.DeviceID].Clone()
		p.mu.RUnlock()
		property, ok = source.Property(mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)
	}
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
	if source, ok := p.sourceDevices[id]; ok {
		source.RuntimeMode, source.Sequence, source.LastUpdateAt = mode, item.Sequence, item.LastUpdateAt
		p.sourceDevices[id] = source
	}
	snapshot := item.Clone()
	p.mu.Unlock()
	p.broadcast(snapshot)
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

func (p *Provider) SubscribeDeviceEvents(handler func(providersdk.DeviceEvent)) func() {
	p.mu.Lock()
	p.nextEventListener++
	id := p.nextEventListener
	p.eventListeners[id] = handler
	p.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			delete(p.eventListeners, id)
			p.mu.Unlock()
		})
	}
}

func (p *Provider) ProviderMetrics() map[string]uint64 {
	result := map[string]uint64{
		"requests": p.requests.Load(), "events": p.events.Load(), "errors": p.errors.Load(),
		"localRequests": p.localRequests.Load(), "localFailures": p.localFailures.Load(),
		"cloudRequests": p.cloudRequests.Load(), "cloudFallbacks": p.cloudFallbacks.Load(),
		"cloudMqttMessagesDropped": p.cloudDropped.Load(), "cloudMqttDuplicateMessages": p.cloudDuplicates.Load(),
		"cloudHttpInitialReads": p.cloudHTTPInitialReads.Load(), "cloudHttpReconcileReads": p.cloudHTTPReconcileReads.Load(), "cloudHttpReadFailures": p.cloudHTTPReadFailures.Load(),
		"directoryRefreshes": p.directoryRefreshes.Load(), "directoryRefreshFailures": p.directoryRefreshFailures.Load(),
		"propertyReadBackoffs": p.propertyReadBackoffs.Load(), "cloudDisconnectExpiries": p.cloudDisconnectExpiries.Load(),
	}
	p.mu.RLock()
	client := p.cloudMIPS
	p.mu.RUnlock()
	if client != nil {
		stats := client.Stats()
		result["cloudMqttConfigured"] = 1
		if stats.Connected {
			result["cloudMqttConnected"] = 1
		}
		result["cloudMqttMessagesReceived"] = stats.MessagesReceived
		result["cloudMqttMessagesInvalid"] = stats.MessagesInvalid
		result["cloudMqttReconnects"] = stats.Reconnects
		result["cloudMqttSubscriptionFailures"] = stats.SubscriptionFailures
		if !stats.LastConnectedAt.IsZero() {
			result["cloudMqttLastConnectedAt"] = uint64(stats.LastConnectedAt.Unix())
		}
		if !stats.LastDisconnectedAt.IsZero() {
			result["cloudMqttLastDisconnectedAt"] = uint64(stats.LastDisconnectedAt.Unix())
		}
		if !stats.LastConnectErrorAt.IsZero() {
			result["cloudMqttLastConnectErrorAt"] = uint64(stats.LastConnectErrorAt.Unix())
		}
		if !stats.NextRetryAt.IsZero() {
			result["cloudMqttNextRetryAt"] = uint64(stats.NextRetryAt.Unix())
		}
	}
	return result
}

func (p *Provider) ProviderDiagnostics() map[string]string {
	p.mu.RLock()
	client := p.cloudMIPS
	p.mu.RUnlock()
	if client == nil {
		return nil
	}
	stats := client.Stats()
	result := map[string]string{"cloudMqttState": "reconnecting"}
	if stats.Connected {
		result["cloudMqttState"] = "connected"
	}
	if stats.LastConnectError != "" {
		result["cloudMqttLastError"] = stats.LastConnectError
	}
	if !stats.LastConnectedAt.IsZero() {
		result["cloudMqttLastConnectedAt"] = stats.LastConnectedAt.Format(time.RFC3339)
	}
	if !stats.LastDisconnectedAt.IsZero() {
		result["cloudMqttLastDisconnectedAt"] = stats.LastDisconnectedAt.Format(time.RFC3339)
	}
	if !stats.NextRetryAt.IsZero() {
		result["cloudMqttNextRetryAt"] = stats.NextRetryAt.Format(time.RFC3339)
	}
	return result
}

func (p *Provider) cloudSubscriptionDIDs() []string {
	p.mu.RLock()
	configuredDevices := append([]DeviceConfig(nil), p.config.Devices...)
	p.mu.RUnlock()
	result := make([]string, 0, len(configuredDevices))
	for _, configured := range configuredDevices {
		mode := strings.ToLower(strings.TrimSpace(configured.ConnectionMode))
		if mode == "" {
			mode = connectionModeAuto
		}
		if mode != connectionModeLocal {
			result = append(result, configured.DID)
		}
	}
	sort.Strings(result)
	return result
}

func (p *Provider) enqueueCloudMIPS(message cloudMIPSMessage) {
	p.mu.RLock()
	queue := p.cloudEvents
	lifecycle := p.lifecycle
	p.mu.RUnlock()
	if queue == nil || lifecycle == nil {
		return
	}
	select {
	case queue <- message:
	case <-lifecycle.Done():
	default:
		p.cloudDropped.Add(1)
		p.errors.Add(1)
	}
}

func (p *Provider) enqueueCloudConnection(event cloudMIPSConnectionEvent) {
	p.mu.RLock()
	queue := p.cloudConnections
	lifecycle := p.lifecycle
	p.mu.RUnlock()
	if queue == nil || lifecycle == nil {
		return
	}
	select {
	case queue <- event:
	case <-lifecycle.Done():
	default:
		p.cloudDropped.Add(1)
	}
}

func (p *Provider) cloudEventLoop(ctx context.Context) {
	defer func() {
		p.mu.RLock()
		done := p.cloudDone
		p.mu.RUnlock()
		if done != nil {
			close(done)
		}
	}()
	p.mu.RLock()
	directoryInterval, cloud := p.cloudDirectoryInterval, p.cloud
	p.mu.RUnlock()
	var directoryTicker *time.Ticker
	var directoryTicks <-chan time.Time
	if cloud != nil && directoryInterval > 0 {
		directoryTicker = time.NewTicker(directoryInterval)
		directoryTicks = directoryTicker.C
		defer directoryTicker.Stop()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-p.cloudEvents:
			p.applyCloudMIPS(message)
		case connection := <-p.cloudConnections:
			epoch := p.cloudConnectionEpoch.Add(1)
			if connection.Connected {
				if connection.Reconnected {
					p.scheduleCloudReconciliation(ctx)
				}
			} else {
				p.scheduleCloudDisconnectExpiry(ctx, epoch)
			}
		case <-p.directoryChanges:
			p.scheduleDirectoryRefresh(ctx)
		case <-directoryTicks:
			p.scheduleDirectoryRefresh(ctx)
		}
	}
}

func (p *Provider) scheduleDirectoryRefresh(ctx context.Context) {
	if !p.directoryRefreshing.CompareAndSwap(false, true) {
		return
	}
	p.backgroundWG.Add(1)
	go func() {
		defer p.backgroundWG.Done()
		defer p.directoryRefreshing.Store(false)
		p.mu.RLock()
		debounce := p.directoryRefreshDebounce
		p.mu.RUnlock()
		if debounce < 0 {
			debounce = 0
		}
		timer := time.NewTimer(debounce)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		p.mu.RLock()
		timeout := p.config.requestTimeout()
		p.mu.RUnlock()
		refreshCtx, cancel := context.WithTimeout(ctx, timeout)
		directory, err := p.refreshDirectory(refreshCtx)
		cancel()
		if err != nil {
			p.directoryRefreshFailures.Add(1)
			return
		}
		p.directoryRefreshes.Add(1)
		specCtx, specCancel := context.WithTimeout(ctx, timeout)
		p.loadSourceSpecs(specCtx, directory)
		specCancel()
	}()
}

func (p *Provider) scheduleCloudDisconnectExpiry(ctx context.Context, epoch uint64) {
	p.mu.RLock()
	grace := p.cloudDisconnectGrace
	p.mu.RUnlock()
	if grace < 0 {
		grace = 0
	}
	p.backgroundWG.Add(1)
	go func() {
		defer p.backgroundWG.Done()
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if p.cloudConnectionEpoch.Load() != epoch {
			return
		}
		p.expireCloudAvailability()
	}()
}

func (p *Provider) expireCloudAvailability() {
	now := time.Now().UTC()
	snapshots := make([]device.Device, 0)
	p.mu.Lock()
	for _, configured := range p.config.Devices {
		mode := strings.ToLower(strings.TrimSpace(configured.ConnectionMode))
		if mode == "" {
			mode = connectionModeAuto
		}
		if mode == connectionModeLocal {
			continue
		}
		item, exists := p.devices[configured.ID]
		if !exists {
			continue
		}
		route := p.routes[configured.DID]
		if mode == connectionModeAuto && route.local && item.RuntimeMode == device.RuntimeModeLocal {
			continue
		}
		if item.EffectiveAvailability() == device.AvailabilityUnknown && item.StateTransport == device.StateTransportPending {
			continue
		}
		item.SetAvailability(device.AvailabilityUnknown)
		item.StateTransport = device.StateTransportPending
		p.sequence++
		item.Sequence, item.LastUpdateAt = p.sequence, now
		p.devices[configured.ID] = item
		if source, ok := p.sourceDevices[configured.ID]; ok {
			source.SetAvailability(device.AvailabilityUnknown)
			source.StateTransport, source.Sequence, source.LastUpdateAt = device.StateTransportPending, item.Sequence, now
			p.sourceDevices[configured.ID] = source
		}
		snapshots = append(snapshots, item.Clone())
	}
	p.mu.Unlock()
	if len(snapshots) > 0 {
		p.cloudDisconnectExpiries.Add(uint64(len(snapshots)))
	}
	for _, snapshot := range snapshots {
		p.broadcast(snapshot)
	}
}

func (p *Provider) scheduleCloudReconciliation(ctx context.Context) {
	if !p.cloudReconciling.CompareAndSwap(false, true) {
		return
	}
	p.backgroundWG.Add(1)
	go func() {
		defer p.backgroundWG.Done()
		defer p.cloudReconciling.Store(false)
		p.mu.RLock()
		configuredDevices := append([]DeviceConfig(nil), p.config.Devices...)
		cloud := p.cloud
		p.mu.RUnlock()
		if cloud == nil {
			return
		}
		for _, configured := range configuredDevices {
			mode, route, _, _ := p.routeFor(configured)
			if mode == connectionModeLocal || !route.cloud {
				continue
			}
			p.refreshCloudDevice(ctx, configured, p.calibrationMappings(configured, true), cloud, false)
			if ctx.Err() != nil {
				return
			}
		}
	}()
}

func (p *Provider) applyCloudMIPS(message cloudMIPSMessage) {
	p.mu.RLock()
	deviceID, ok := p.byDID[message.DID]
	p.mu.RUnlock()
	if !ok {
		return
	}
	configured, ok := p.configuredDevice(deviceID)
	if !ok || configured.ConnectionMode == connectionModeLocal {
		return
	}
	switch message.Kind {
	case cloudMIPSProperty:
		_, mapping, err := p.mappingForMIoT(deviceID, message.SIID, message.PIID)
		if err != nil {
			return
		}
		value, err := decodePropertyValue(mapping, message.Value)
		if err != nil {
			p.errors.Add(1)
			return
		}
		updated, changed := p.applyObservedProperty(deviceID, mapping, value, observationCloudMQTT, message.ObservedAt, device.RuntimeModeCloud)
		if !changed {
			p.cloudDuplicates.Add(1)
			return
		}
		p.events.Add(1)
		p.broadcast(updated)
	case cloudMIPSEvent:
		payload, err := json.Marshal(message.Arguments)
		if err != nil {
			p.errors.Add(1)
			return
		}
		endpointID := "miot-" + strconv.Itoa(message.SIID)
		capabilityID := "service-" + strconv.Itoa(message.SIID)
		eventID := "event-" + strconv.Itoa(message.EIID)
		observedAt := message.ObservedAt
		if observedAt.IsZero() {
			observedAt = time.Now().UTC()
		}
		p.mu.Lock()
		p.eventSequence++
		occurrence := providersdk.DeviceEvent{ProviderID: p.id, DeviceID: deviceID, EndpointID: endpointID, CapabilityID: capabilityID, EventID: eventID, Name: p.eventNameLocked(deviceID, endpointID, capabilityID, eventID), Payload: payload, ObservedAt: observedAt, Sequence: p.eventSequence}
		p.mu.Unlock()
		p.events.Add(1)
		p.broadcastDeviceEvent(occurrence)
	case cloudMIPSState:
		if message.Online == nil {
			return
		}
		if updated, changed := p.applyCloudOnlineState(configured, *message.Online); changed {
			p.events.Add(1)
			p.broadcast(updated)
		}
	}
}

func (p *Provider) eventNameLocked(deviceID, endpointID, capabilityID, eventID string) string {
	item, exists := p.sourceDevices[deviceID]
	if !exists {
		return eventID
	}
	for _, endpoint := range item.Endpoints {
		if endpoint.ID != endpointID {
			continue
		}
		for _, capability := range endpoint.Capabilities {
			if capability.ID != capabilityID {
				continue
			}
			for _, definition := range capability.Events {
				if definition.ID == eventID {
					return definition.Name
				}
			}
		}
	}
	return eventID
}

func (p *Provider) configuredDevice(id string) (DeviceConfig, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, configured := range p.config.Devices {
		if configured.ID == id {
			return configured, true
		}
	}
	return DeviceConfig{}, false
}

func (p *Provider) applyCloudOnlineState(configured DeviceConfig, online bool) (device.Device, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	item, exists := p.devices[configured.ID]
	if !exists {
		return device.Device{}, false
	}
	// In auto mode an explicit cloud-offline message must not take a device
	// down while its central-gateway route is still healthy.
	if !online && configured.ConnectionMode == connectionModeAuto && p.routes[configured.DID].local && item.RuntimeMode == device.RuntimeModeLocal {
		return item.Clone(), false
	}
	if item.Online == online && ((configured.ConnectionMode == connectionModeAuto && item.RuntimeMode == device.RuntimeModeLocal) || item.RuntimeMode == device.RuntimeModeCloud) {
		return item.Clone(), false
	}
	item.SetOnline(online)
	if configured.ConnectionMode == connectionModeCloud || item.RuntimeMode != device.RuntimeModeLocal {
		item.RuntimeMode = device.RuntimeModeCloud
		item.StateTransport = device.StateTransportCloudMQTT
	}
	p.sequence++
	item.Sequence, item.LastUpdateAt = p.sequence, time.Now().UTC()
	p.devices[configured.ID] = item
	if source, ok := p.sourceDevices[configured.ID]; ok {
		source.SetOnline(online)
		source.RuntimeMode, source.StateTransport, source.Sequence, source.LastUpdateAt = item.RuntimeMode, item.StateTransport, item.Sequence, item.LastUpdateAt
		p.sourceDevices[configured.ID] = source
	}
	return item.Clone(), true
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
		case interval := <-p.pollIntervalChanges:
			if interval > 0 {
				ticker.Reset(interval)
			}
		case <-ticker.C:
			p.refreshAll(ctx, false)
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
				p.refreshDevice(ctx, configured, initial)
			}
		}()
	}
	p.mu.RLock()
	configuredDevices := append([]DeviceConfig(nil), p.config.Devices...)
	p.mu.RUnlock()
sendLoop:
	for _, configured := range configuredDevices {
		select {
		case jobs <- configured:
		case <-ctx.Done():
			break sendLoop
		}
	}
	close(jobs)
	workers.Wait()
}

const cloudPropertyReadBatchSize = 50

func (p *Provider) refreshDevice(ctx context.Context, configured DeviceConfig, initial bool) {
	mappings := p.calibrationMappings(configured, initial)
	if len(mappings) == 0 {
		return
	}
	mode, route, _, cloud := p.routeFor(configured)
	if cloud != nil && route.cloud && (mode == connectionModeCloud || !route.local) {
		p.refreshCloudDevice(ctx, configured, mappings, cloud, initial)
		return
	}
	for _, mapping := range mappings {
		p.mu.RLock()
		timeout := p.config.requestTimeout()
		p.mu.RUnlock()
		readCtx, cancel := context.WithTimeout(ctx, timeout)
		_, err := p.ReadProperty(readCtx, providersdk.PropertyReadRequest{DeviceID: configured.ID, EndpointID: mapping.EndpointID, CapabilityID: mapping.CapabilityID, PropertyID: mapping.PropertyID})
		cancel()
		if err != nil {
			p.markValueError(configured.ID, mapping, err)
		}
	}
}

func (p *Provider) refreshCloudDevice(ctx context.Context, configured DeviceConfig, mappings []PropertyMapping, cloud homeCloudClient, initial bool) {
	var latest device.Device
	changed := false
	for start := 0; start < len(mappings); start += cloudPropertyReadBatchSize {
		end := start + cloudPropertyReadBatchSize
		if end > len(mappings) {
			end = len(mappings)
		}
		batch := mappings[start:end]
		input := make([]cloudProperty, 0, len(batch))
		for _, mapping := range batch {
			input = append(input, cloudProperty{DID: configured.DID, SIID: mapping.SIID, PIID: mapping.PIID})
		}
		p.mu.RLock()
		timeout := p.config.requestTimeout()
		p.mu.RUnlock()
		readStartedAt := time.Now().UTC()
		readCtx, cancel := context.WithTimeout(ctx, timeout)
		p.requests.Add(1)
		p.cloudRequests.Add(1)
		if initial {
			p.cloudHTTPInitialReads.Add(1)
		} else {
			p.cloudHTTPReconcileReads.Add(1)
		}
		results, err := cloud.GetProperties(readCtx, input)
		cancel()
		if err != nil {
			p.errors.Add(1)
			p.cloudHTTPReadFailures.Add(uint64(len(batch)))
			for _, mapping := range batch {
				p.markValueError(configured.ID, mapping, err)
			}
			continue
		}
		byProperty := make(map[[2]int]cloudProperty, len(results))
		for _, result := range results {
			byProperty[[2]int{result.SIID, result.PIID}] = result
		}
		for _, mapping := range batch {
			result, exists := byProperty[[2]int{mapping.SIID, mapping.PIID}]
			if !exists || !miotResultOK(result.Code) {
				p.cloudHTTPReadFailures.Add(1)
				p.markValueError(configured.ID, mapping, errors.New("Xiaomi Home cloud property response was incomplete"))
				continue
			}
			value, decodeErr := decodePropertyValue(mapping, result.Value)
			if decodeErr != nil {
				p.cloudHTTPReadFailures.Add(1)
				p.markValueError(configured.ID, mapping, decodeErr)
				continue
			}
			updated, propertyChanged := p.applyObservedProperty(configured.ID, mapping, value, observationCloudHTTP, readStartedAt, device.RuntimeModeCloud)
			if propertyChanged {
				latest, changed = updated, true
			}
		}
	}
	if changed {
		p.broadcast(latest)
	}
}

func (p *Provider) calibrationMappings(configured DeviceConfig, initial bool) []PropertyMapping {
	mappings := p.readableMappings(configured)
	result := make([]PropertyMapping, 0, len(mappings))
	cloudPushConnected := false
	p.mu.RLock()
	cloudMIPS := p.cloudMIPS
	p.mu.RUnlock()
	if cloudMIPS != nil {
		cloudPushConnected = cloudMIPS.Stats().Connected
	}
	mode, route, _, _ := p.routeFor(configured)
	now := time.Now().UTC()
	for _, mapping := range mappings {
		if mapping.Readable != nil && !*mapping.Readable {
			continue
		}
		if initial {
			result = append(result, mapping)
			continue
		}
		if !p.isPeriodicallyMapped(configured, mapping) {
			continue
		}
		key := sourcePropertyKey(configured.ID, mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)
		p.mu.RLock()
		failure := p.propertyFailures[key]
		p.mu.RUnlock()
		if failure.NextRetryAt.After(now) {
			p.propertyReadBackoffs.Add(1)
			continue
		}
		notifiable := mapping.Notifiable == nil || *mapping.Notifiable
		pushAvailable := (mode != connectionModeCloud && route.local && route.push) || (mode != connectionModeLocal && cloudPushConnected)
		if !pushAvailable || !notifiable {
			result = append(result, mapping)
		}
	}
	return result
}

func (p *Provider) isPeriodicallyMapped(configured DeviceConfig, mapping PropertyMapping) bool {
	for _, candidate := range configured.Properties {
		if candidate.EndpointID == mapping.EndpointID && candidate.CapabilityID == mapping.CapabilityID && candidate.PropertyID == mapping.PropertyID {
			return true
		}
	}
	p.mu.RLock()
	_, interested := p.propertyInterests[sourcePropertyKey(configured.ID, mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)]
	p.mu.RUnlock()
	return interested
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

func (p *Provider) handleIncoming(incoming hubIncoming) {
	if incoming.Topic == topicDeviceListChange {
		p.mu.RLock()
		changes := p.directoryChanges
		lifecycle := p.lifecycle
		p.mu.RUnlock()
		if changes != nil && lifecycle != nil {
			select {
			case changes <- struct{}{}:
			case <-lifecycle.Done():
			default:
			}
		}
		return
	}
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
		updated, changed := p.applyObservedProperty(configured.ID, mapping, value, observationLocalMQTT, time.Time{}, device.RuntimeModeLocal)
		if changed {
			p.events.Add(1)
			p.broadcast(updated)
		}
	}
}

func (p *Provider) updateProperty(id string, mapping PropertyMapping, value device.PropertyValue) device.Device {
	updated, _ := p.applyObservedProperty(id, mapping, value, observationUnknown, time.Time{}, "")
	return updated
}

func (p *Provider) applyObservedProperty(id string, mapping PropertyMapping, value device.PropertyValue, observationSource observationSource, observedAt time.Time, runtimeMode device.RuntimeMode) (device.Device, bool) {
	now := time.Now().UTC()
	valueBytes, _ := json.Marshal(value)
	valueDigest := string(valueBytes)
	key := sourcePropertyKey(id, mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)
	p.mu.Lock()
	previousObservation, observed := p.observations[key]
	if observed {
		if !observedAt.IsZero() && !previousObservation.ObservedAt.IsZero() {
			if observedAt.Before(previousObservation.ObservedAt) || (observedAt.Equal(previousObservation.ObservedAt) && observationSource < previousObservation.Source) {
				item := p.devices[id].Clone()
				p.mu.Unlock()
				return item, false
			}
		}
		if valueDigest == previousObservation.Value {
			previousObservation.ReceivedAt = now
			if observedAt.After(previousObservation.ObservedAt) {
				previousObservation.ObservedAt = observedAt
			}
			if observationSource > previousObservation.Source {
				previousObservation.Source = observationSource
			}
			p.observations[key] = previousObservation
			p.valueStatus[key] = providersdk.SourceValueStatus{Known: true, Available: true, ObservedAt: now}
			delete(p.propertyFailures, key)
			item := p.devices[id].Clone()
			p.mu.Unlock()
			return item, false
		}
		// Messages without device timestamps are ordered by receipt. Preserve a
		// near-simultaneous local update over the cloud copy of the same change.
		if observedAt.IsZero() && previousObservation.ObservedAt.IsZero() && observationSource < previousObservation.Source && now.Sub(previousObservation.ReceivedAt) < 2*time.Second {
			item := p.devices[id].Clone()
			p.mu.Unlock()
			return item, false
		}
	}
	item := p.devices[id]
	setObservedProperty(&item, mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID, value)
	if runtimeMode != "" {
		item.RuntimeMode = runtimeMode
	}
	switch observationSource {
	case observationLocalMQTT:
		item.StateTransport = device.StateTransportLocalMQTT
	case observationCloudMQTT:
		item.StateTransport = device.StateTransportCloudMQTT
	case observationCloudHTTP:
		item.StateTransport = device.StateTransportCloudHTTP
	}
	setPropertyStateTransport(&item, mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID, item.StateTransport)
	source := p.sourceDevices[id]
	setObservedProperty(&source, mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID, value)
	setPropertyStateTransport(&source, mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID, item.StateTransport)
	p.valueStatus[key] = providersdk.SourceValueStatus{Known: true, Available: true, ObservedAt: now}
	delete(p.propertyFailures, key)
	p.observations[key] = propertyObservation{Source: observationSource, ObservedAt: observedAt, ReceivedAt: now, Value: valueDigest}
	p.sequence++
	item.Sequence = p.sequence
	item.LastUpdateAt = now
	item.SetOnline(true)
	p.devices[id] = item
	source.Sequence, source.LastUpdateAt, source.RuntimeMode, source.StateTransport = item.Sequence, item.LastUpdateAt, item.RuntimeMode, item.StateTransport
	source.SetOnline(true)
	p.sourceDevices[id] = source
	snapshot := item.Clone()
	p.mu.Unlock()
	return snapshot, true
}

func (p *Provider) markValueError(id string, mapping PropertyMapping, cause error) {
	p.mu.Lock()
	key := sourcePropertyKey(id, mapping.EndpointID, mapping.CapabilityID, mapping.PropertyID)
	status := p.valueStatus[key]
	status.Available, status.Error = false, cause.Error()
	p.valueStatus[key] = status
	failure := p.propertyFailures[key]
	failure.Count++
	failure.NextRetryAt = time.Now().UTC().Add(propertyReadRetryDelay(failure.Count, p.config.pollInterval()))
	p.propertyFailures[key] = failure
	p.mu.Unlock()
}

func propertyReadRetryDelay(count uint, pollInterval time.Duration) time.Duration {
	base := pollInterval
	if base <= 0 || base > 30*time.Second {
		base = 30 * time.Second
	}
	if base < time.Second {
		base = time.Second
	}
	shift := uint(0)
	if count > 1 {
		shift = count - 1
	}
	if shift > 8 {
		shift = 8
	}
	delay := base * time.Duration(1<<shift)
	if delay > maximumPropertyReadRetryInterval {
		return maximumPropertyReadRetryInterval
	}
	return delay
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

func (p *Provider) broadcastDeviceEvent(event providersdk.DeviceEvent) {
	p.mu.RLock()
	handlers := make([]func(providersdk.DeviceEvent), 0, len(p.eventListeners))
	for _, handler := range p.eventListeners {
		handlers = append(handlers, handler)
	}
	p.mu.RUnlock()
	for _, handler := range handlers {
		copy := event
		copy.Payload = append(json.RawMessage(nil), event.Payload...)
		handler(copy)
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
	item = item.Clone()
	p.mu.RUnlock()
	return item, ok
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
	item := device.Device{
		SchemaVersion: device.SchemaVersion,
		ID:            configured.ID,
		ProviderID:    providerID,
		Name:          configured.Name,
		Type:          configured.Type,
		HomeID:        configured.HomeID,
		HomeName:      configured.Home,
		RoomID:        configured.RoomID,
		RoomName:      configured.Room,
		Endpoints:     endpointList,
		LastUpdateAt:  time.Now().UTC(),
	}
	item.SetAvailability(device.AvailabilityUnknown)
	return item
}

func applyCentralStalePolicy(item *device.Device, compensationInterval time.Duration) {
	seconds := int(compensationInterval / time.Second)
	if seconds <= 0 {
		seconds = defaultPollInterval
	}
	for endpointIndex := range item.Endpoints {
		for capabilityIndex := range item.Endpoints[endpointIndex].Capabilities {
			for propertyIndex := range item.Endpoints[endpointIndex].Capabilities[capabilityIndex].Properties {
				definition := &item.Endpoints[endpointIndex].Capabilities[capabilityIndex].Properties[propertyIndex].Definition
				if definition.Notifiable {
					definition.StaleAfterSeconds = max(seconds*4, 300)
				} else {
					definition.StaleAfterSeconds = max(seconds*2, 30)
				}
			}
		}
	}
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

func setPropertyStateTransport(item *device.Device, endpointID, capabilityID, propertyID string, transport device.StateTransport) bool {
	for endpointIndex := range item.Endpoints {
		if item.Endpoints[endpointIndex].ID != endpointID {
			continue
		}
		for capabilityIndex := range item.Endpoints[endpointIndex].Capabilities {
			if item.Endpoints[endpointIndex].Capabilities[capabilityIndex].ID != capabilityID {
				continue
			}
			for propertyIndex := range item.Endpoints[endpointIndex].Capabilities[capabilityIndex].Properties {
				property := &item.Endpoints[endpointIndex].Capabilities[capabilityIndex].Properties[propertyIndex]
				if property.Definition.ID == propertyID {
					property.StateTransport = transport
					return true
				}
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
