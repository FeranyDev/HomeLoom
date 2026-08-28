package sonoff

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/sonoff/catalog"
	"github.com/feranydev/homeloom/backend/internal/providers/sonoff/cloud"
	"github.com/feranydev/homeloom/backend/internal/providers/sonoff/lan"
)

type lanTransport interface {
	Command(context.Context, lan.Request, string, map[string]any) (map[string]any, error)
	GetState(context.Context, lan.Request) (map[string]any, error)
}

type cloudTransport interface {
	ListDevices(context.Context) ([]cloud.Device, error)
	SetDeviceState(context.Context, string, map[string]any) error
}

type lanDiscovery func(context.Context, time.Duration) ([]lan.Service, error)

type runtimeDevice struct {
	config DeviceConfig
	item   device.Device
	params map[string]any
}

// DirectoryDevice is the non-sensitive device metadata exposed to the
// management UI. DeviceKey and raw Params deliberately stay inside the
// Provider process.
type DirectoryDevice struct {
	ID         string `json:"id"`
	DeviceID   string `json:"deviceId"`
	Name       string `json:"name"`
	Model      string `json:"model,omitempty"`
	UIID       int    `json:"uiid"`
	Type       string `json:"type,omitempty"`
	HomeID     string `json:"homeId,omitempty"`
	HomeName   string `json:"homeName,omitempty"`
	RoomID     string `json:"roomId,omitempty"`
	RoomName   string `json:"roomName,omitempty"`
	Channels   int    `json:"channels"`
	Online     bool   `json:"online"`
	Configured bool   `json:"configured"`
}

type Provider struct {
	id     string
	name   string
	config Config

	lan          lanTransport
	cloud        cloudTransport
	discoverLAN  lanDiscovery
	realtime     cloud.RealtimeSubscriber
	pollInterval time.Duration

	mu             sync.RWMutex
	running        bool
	devices        map[string]runtimeDevice
	listeners      map[uint64]func(device.Device)
	next           uint64
	realtimeCancel context.CancelFunc
	realtimeDone   chan struct{}
	pollDone       chan struct{}
	requests       atomic.Uint64
	errors         atomic.Uint64
	writes         atomic.Uint64
	polls          atomic.Uint64
	realtimeEvents atomic.Uint64
}

var _ providersdk.DeviceIdentityResolver = (*Provider)(nil)

// ProviderDeviceID resolves the stable HomeLoom device ID to the Sonoff cloud
// identity retained in the configured device catalog.
func (p *Provider) ProviderDeviceID(deviceID string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	current, ok := p.devices[deviceID]
	if !ok || strings.TrimSpace(current.config.DeviceID) == "" {
		return "", false
	}
	return current.config.DeviceID, true
}

func NewProviderFromConfig(item providerconfig.Config) (*Provider, error) {
	config, err := decodeConfig(item)
	if err != nil {
		return nil, err
	}
	requestTimeout := time.Duration(config.RequestTimeoutSeconds) * time.Second
	var cloudClient cloudTransport
	var realtime cloud.RealtimeSubscriber
	if config.Mode != ModeLocal && (config.Cloud.Endpoint != "" || config.Cloud.AccessToken != "" || config.Cloud.Username != "" || config.Cloud.Password != "") {
		endpoint := config.Cloud.Endpoint
		if endpoint == "" {
			endpoint = cloud.EndpointForRegion(config.Region)
		}
		credentials := cloud.LoginCredentials{Username: config.Cloud.Username, Password: config.Cloud.Password, CountryCode: config.Cloud.CountryCode, Region: config.Region, Endpoint: endpoint, AppID: config.Cloud.AppID, AppSecret: config.Cloud.AppSecret}
		var client *cloud.Client
		var clientErr error
		if config.Cloud.AccessToken != "" {
			var storedCredentials *cloud.LoginCredentials
			if config.Cloud.Username != "" && config.Cloud.Password != "" {
				storedCredentials = &credentials
			}
			client, clientErr = cloud.NewClientWithOptions(cloud.Options{Endpoint: endpoint, HTTPClient: &http.Client{Timeout: requestTimeout}, Authenticator: cloud.NewTokenAuthenticator(config.Cloud.AccessToken), Credentials: storedCredentials, AppID: config.Cloud.AppID})
		} else {
			client, clientErr = cloud.NewClientWithCredentials(nil, endpoint, credentials, requestTimeout)
		}
		if clientErr != nil {
			return nil, clientErr
		}
		cloudClient = client
		if config.Cloud.WebSocketEndpoint != "" {
			realtime, clientErr = cloud.NewWebSocketSubscriber(config.Cloud.WebSocketEndpoint, client)
			if clientErr != nil {
				return nil, clientErr
			}
		}
	}
	return newProvider(item.ID, item.Name, config, lan.NewClient(http.DefaultClient, requestTimeout), cloudClient, realtime), nil
}

// NewProviderWithTransports is used by deterministic tests and embedding
// integrations. Production callers should use NewProviderFromConfig.
func NewProviderWithTransports(item providerconfig.Config, local lanTransport, remote cloudTransport) (*Provider, error) {
	config, err := decodeConfig(item)
	if err != nil {
		return nil, err
	}
	return newProvider(item.ID, item.Name, config, local, remote, nil), nil
}

// NewProviderWithTransportsAndRealtime additionally injects the cloud push
// subscription. It is primarily useful for embedded integrations and tests;
// the normal constructor creates a WebSocket subscriber only when an explicit
// cloud.websocketEndpoint is configured.
func NewProviderWithTransportsAndRealtime(item providerconfig.Config, local lanTransport, remote cloudTransport, realtime cloud.RealtimeSubscriber) (*Provider, error) {
	config, err := decodeConfig(item)
	if err != nil {
		return nil, err
	}
	return newProvider(item.ID, item.Name, config, local, remote, realtime), nil
}

func newProvider(id, name string, config Config, local lanTransport, remote cloudTransport, realtime cloud.RealtimeSubscriber) *Provider {
	return &Provider{
		id: id, name: name, config: config, lan: local, cloud: remote, realtime: realtime,
		discoverLAN: func(ctx context.Context, timeout time.Duration) ([]lan.Service, error) {
			return lan.DiscoverServices(ctx, timeout, nil)
		},
		devices: make(map[string]runtimeDevice), listeners: make(map[uint64]func(device.Device)),
		pollInterval: time.Duration(config.RefreshIntervalSec) * time.Second,
	}
}

func (p *Provider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: ProviderType, Name: p.name, Version: "0.1.0"}
}

func (p *Provider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyRead: true, PropertyWrite: true, Commands: true, Events: true}
}

func (p *Provider) Initialize(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return nil
	}
	p.running = true
	lifecycle, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p.realtimeCancel = cancel
	p.pollDone = make(chan struct{})
	source := p.realtime
	var realtimeDone chan struct{}
	if source != nil {
		realtimeDone = make(chan struct{})
		p.realtimeDone = realtimeDone
	}
	pollDone := p.pollDone
	p.mu.Unlock()
	go p.pollLoop(lifecycle, pollDone)
	if source != nil {
		go p.consumeRealtime(lifecycle, source, realtimeDone)
	}
	return nil
}

func (p *Provider) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	p.running = false
	cancel, realtimeDone, pollDone := p.realtimeCancel, p.realtimeDone, p.pollDone
	p.realtimeCancel, p.realtimeDone, p.pollDone = nil, nil, nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, done := range []chan struct{}{realtimeDone, pollDone} {
		if done == nil {
			continue
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (p *Provider) pollLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.polls.Add(1)
			items, err := p.pollDevices(ctx)
			if err != nil {
				p.errors.Add(1)
			}
			for _, item := range items {
				p.notify(item)
			}
		}
	}
}

// pollDevices refreshes the complete configured catalog. Cloud state is read
// first and a reachable LAN device then wins with its fresher local snapshot.
func (p *Provider) pollDevices(ctx context.Context) ([]device.Device, error) {
	if err := p.seedConfiguredDevices(); err != nil {
		return nil, err
	}
	var refreshErr error
	if p.cloud != nil && p.config.Mode != ModeLocal {
		if refreshErr = p.refreshCloud(ctx); refreshErr != nil {
			p.markConfiguredOffline()
		}
	}
	p.mu.RLock()
	configured := make([]runtimeDevice, 0, len(p.devices))
	for _, current := range p.devices {
		configured = append(configured, current)
	}
	p.mu.RUnlock()
	for _, current := range configured {
		if p.lan == nil || current.config.Host == "" || p.config.Mode == ModeCloud {
			continue
		}
		state, err := p.lan.GetState(ctx, p.lanRequest(current.config))
		if err != nil {
			continue
		}
		p.applyStateSnapshot(current.item.ID, state)
	}
	return p.snapshotDevices(), refreshErr
}

func (p *Provider) consumeRealtime(ctx context.Context, source cloud.RealtimeSubscriber, done chan struct{}) {
	defer close(done)
	for {
		err := source.Subscribe(ctx, p.applyRealtimeEvent)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			p.errors.Add(1)
		}
		// A completed stream is unexpected as well. Back off before reconnecting
		// so a misconfigured endpoint cannot spin a CPU or flood diagnostics.
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (p *Provider) DiscoverDevices(ctx context.Context) ([]device.Device, error) {
	p.requests.Add(1)
	if err := p.seedConfiguredDevices(); err != nil {
		p.errors.Add(1)
		return nil, err
	}
	if p.cloud != nil && p.config.Mode != ModeLocal {
		if err := p.refreshCloud(ctx); err != nil {
			p.errors.Add(1)
			if len(p.config.Devices) == 0 && p.config.Mode == ModeCloud {
				return nil, err
			}
			p.markConfiguredOffline()
		}
	}
	return p.snapshotDevices(), nil
}

func (p *Provider) seedConfiguredDevices() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, configured := range p.config.Devices {
		if _, exists := p.devices[configured.ID]; exists {
			continue
		}
		if err := p.commitConfigLocked(configured, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) snapshotDevices() []device.Device {
	p.mu.RLock()
	result := make([]device.Device, 0, len(p.devices))
	for _, current := range p.devices {
		result = append(result, current.item)
	}
	p.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (p *Provider) markConfiguredOffline() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, configured := range p.config.Devices {
		current, exists := p.devices[configured.ID]
		if exists {
			_ = p.commitConfigLocked(current.config, current.params, boolPointer(false))
			continue
		}
		_ = p.commitConfigLocked(configured, nil, boolPointer(false))
	}
}

// Scan discovers transient eWeLink LAN endpoints through mDNS. Scan never
// mutates p.config or p.devices: the caller must explicitly turn a candidate
// into a saved device configuration.
func (p *Provider) Scan(ctx context.Context) ([]providersdk.DiscoveryCandidate, error) {
	if p.discoverLAN == nil {
		return nil, fmt.Errorf("Sonoff LAN discovery is unavailable")
	}
	p.requests.Add(1)
	services, err := p.discoverLAN(ctx, time.Duration(p.config.DiscoveryTimeoutSec)*time.Second)
	if err != nil {
		p.errors.Add(1)
		return nil, err
	}
	configured := make(map[string]bool, len(p.config.Devices))
	for _, item := range p.config.Devices {
		configured[item.DeviceID] = true
	}
	result := make([]providersdk.DiscoveryCandidate, 0, len(services))
	for _, service := range services {
		found, parseErr := lan.ParseService(service)
		if parseErr != nil {
			continue
		}
		name := strings.TrimSpace(service.Instance)
		if name == "" {
			name = strings.TrimSpace(found.Type)
		}
		if name == "" {
			name = "Sonoff " + stableID(found.DeviceID)
		}
		metadata := map[string]string{
			"deviceId":   found.DeviceID,
			"type":       found.Type,
			"apiVersion": found.APIVersion,
			"encrypted":  strconv.FormatBool(found.Encrypt),
			"diy":        strconv.FormatBool(!found.Encrypt),
		}
		if configured[found.DeviceID] {
			metadata["configured"] = "true"
		}
		result = append(result, providersdk.DiscoveryCandidate{
			ID: "sonoff-" + stableID(found.DeviceID), Provider: ProviderType, Name: name,
			Host: found.Host, Port: found.Port, Metadata: metadata,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ID == result[j].ID {
			return result[i].Host < result[j].Host
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (p *Provider) refreshCloud(ctx context.Context) error {
	items, err := p.cloud.ListDevices(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, remote := range items {
		deviceID := strings.TrimSpace(remote.DeviceID)
		if deviceID == "" {
			deviceID = remote.ID
		}
		if deviceID == "" {
			continue
		}
		configured, explicitlyConfigured := p.configForRemoteLocked(deviceID, remote)
		if p.config.ManagedDevices && !explicitlyConfigured {
			continue
		}
		if err := p.commitConfigLocked(configured, remote.Params, boolPointer(remote.Online)); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) configForRemoteLocked(deviceID string, remote cloud.Device) (DeviceConfig, bool) {
	for _, configured := range p.config.Devices {
		if configured.DeviceID == deviceID || configured.ID == remote.ID {
			configured.DeviceID = deviceID
			if configured.Name == "" {
				configured.Name = remote.Name
			}
			if configured.Model == "" {
				configured.Model = remote.Model
			}
			if configured.UIID == 0 {
				configured.UIID = remote.UIID
			}
			if configured.Type == "" {
				configured.Type = remote.Type
			}
			if configured.HomeID == "" {
				configured.HomeID = remote.HomeID
			}
			if configured.HomeName == "" {
				configured.HomeName = remote.HomeName
			}
			if configured.RoomID == "" {
				configured.RoomID = remote.RoomID
			}
			if configured.RoomName == "" {
				configured.RoomName = remote.RoomName
			}
			if configured.DeviceKey == "" {
				configured.DeviceKey = remote.DeviceKey
			}
			return configured, true
		}
	}
	id := "sonoff-" + stableID(deviceID)
	return DeviceConfig{ID: id, DeviceID: deviceID, Name: nonEmpty(remote.Name, deviceID), Model: remote.Model, UIID: remote.UIID, HomeID: remote.HomeID, HomeName: remote.HomeName, RoomID: remote.RoomID, RoomName: remote.RoomName, DeviceKey: remote.DeviceKey, Host: "", Port: defaultLANPort, Channels: channelsFromParams(remote.Params)}, false
}

// DiscoverCloudDevices reads the complete account directory without changing
// the managed/public device set. It is used by the explicit device manager in
// the same way Xiaomi keeps discovery separate from saved mappings.
func (p *Provider) DiscoverCloudDevices(ctx context.Context) ([]DirectoryDevice, error) {
	if p.cloud == nil || p.config.Mode == ModeLocal {
		return nil, fmt.Errorf("Sonoff cloud directory is unavailable")
	}
	p.requests.Add(1)
	items, err := p.cloud.ListDevices(ctx)
	if err != nil {
		p.errors.Add(1)
		return nil, err
	}
	p.mu.RLock()
	configured := make(map[string]DeviceConfig, len(p.config.Devices))
	for _, item := range p.config.Devices {
		configured[item.DeviceID] = item
	}
	p.mu.RUnlock()
	result := make([]DirectoryDevice, 0, len(items))
	for _, remote := range items {
		deviceID := strings.TrimSpace(remote.DeviceID)
		if deviceID == "" {
			deviceID = strings.TrimSpace(remote.ID)
		}
		if deviceID == "" {
			continue
		}
		entry, exists := configured[deviceID]
		id := "sonoff-" + stableID(deviceID)
		name, model, typeValue := nonEmpty(remote.Name, deviceID), remote.Model, remote.Type
		homeID, homeName, roomID, roomName := remote.HomeID, remote.HomeName, remote.RoomID, remote.RoomName
		channels := channelsFromParams(remote.Params)
		if exists {
			if entry.ID != "" {
				id = entry.ID
			}
			name, model, typeValue = nonEmpty(entry.Name, name), nonEmpty(entry.Model, model), nonEmpty(entry.Type, typeValue)
			homeID, homeName = nonEmpty(entry.HomeID, homeID), nonEmpty(entry.HomeName, homeName)
			roomID, roomName = nonEmpty(entry.RoomID, roomID), nonEmpty(entry.RoomName, roomName)
			if entry.Channels > channels {
				channels = entry.Channels
			}
		}
		result = append(result, DirectoryDevice{
			ID: id, DeviceID: deviceID, Name: name, Model: model,
			UIID: remote.UIID, Type: typeValue, HomeID: homeID, HomeName: homeName,
			RoomID: roomID, RoomName: roomName, Channels: channels,
			Online: remote.Online, Configured: exists,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DeviceID < result[j].DeviceID })
	return result, nil
}

func channelsFromParams(params map[string]any) int {
	if switches, ok := params["switches"].([]any); ok && len(switches) > 0 {
		return len(switches)
	}
	return 1
}

func (p *Provider) commitConfigLocked(configured DeviceConfig, remoteParams map[string]any, online *bool) error {
	params := cloneParams(configured.Params)
	for key, value := range remoteParams {
		params[key] = value
	}
	if online == nil {
		online = configured.Online
	}
	isOnline := online != nil && *online
	if online == nil && len(params) > 0 {
		isOnline = true
	}
	runtimeMode := device.RuntimeModeCloud
	stateTransport := device.StateTransportCloudHTTP
	if configured.Host != "" {
		runtimeMode = device.RuntimeModeLocal
		stateTransport = device.StateTransportLocalMQTT
	}
	item, err := catalog.BuildDevice(catalog.DeviceInput{ID: configured.ID, ProviderID: p.id, DeviceID: configured.DeviceID, Name: configured.Name, Model: configured.Model, UIID: configured.UIID, Type: configured.Type, HomeID: configured.HomeID, HomeName: configured.HomeName, RoomID: configured.RoomID, RoomName: configured.RoomName, Params: params, Online: isOnline, Channels: configured.Channels, RuntimeMode: string(runtimeMode), StateTransport: string(stateTransport)})
	if err != nil {
		return err
	}
	if previous, exists := p.devices[configured.ID]; exists {
		item.Sequence = previous.item.Sequence + 1
	}
	p.devices[configured.ID] = runtimeDevice{config: configured, item: item, params: params}
	return nil
}

func (p *Provider) ReadProperty(ctx context.Context, request providersdk.PropertyReadRequest) (device.Property, error) {
	current, ok := p.current(request.DeviceID)
	if !ok {
		return device.Property{}, providersdk.ErrDeviceNotFound
	}
	if p.lan != nil && current.config.Host != "" && (p.config.Mode == ModeLocal || p.config.Mode == ModeAuto) {
		if state, err := p.lan.GetState(ctx, p.lanRequest(current.config)); err == nil {
			p.applyState(request.DeviceID, state)
			current, _ = p.current(request.DeviceID)
		}
	}
	property, exists := current.item.Property(request.EndpointID, request.CapabilityID, request.PropertyID)
	if !exists {
		return device.Property{}, providersdk.ErrPropertyUnsupported
	}
	return property, nil
}

func (p *Provider) WriteProperty(ctx context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	current, ok := p.current(request.DeviceID)
	if !ok {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	property, exists := current.item.Property(request.EndpointID, request.CapabilityID, request.PropertyID)
	if !exists {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	requested := property
	requested.Value = request.Value
	if !property.Definition.Writable || requested.Validate() != nil {
		return device.Device{}, providersdk.ErrPropertyInvalid
	}
	command, data, err := catalog.EncodePropertyCommand(current.item, request)
	if err != nil {
		return device.Device{}, err
	}
	var transportErr error
	if p.lan != nil && current.config.Host != "" && p.config.Mode != ModeCloud {
		_, transportErr = p.lan.Command(ctx, p.lanRequest(current.config), command, data)
	}
	if transportErr != nil && p.config.Mode == ModeLocal {
		p.errors.Add(1)
		return device.Device{}, fmt.Errorf("Sonoff LAN write: %w", transportErr)
	}
	if transportErr != nil || (p.config.Mode != ModeLocal && current.config.Host == "") {
		if p.cloud == nil {
			p.errors.Add(1)
			return device.Device{}, providersdk.ErrProviderUnavailable
		}
		transportErr = p.cloud.SetDeviceState(ctx, current.config.DeviceID, data)
	}
	if transportErr != nil {
		p.errors.Add(1)
		return device.Device{}, fmt.Errorf("Sonoff write: %w", transportErr)
	}
	p.writes.Add(1)
	p.mu.Lock()
	latest, exists := p.devices[request.DeviceID]
	if exists {
		latest.item.SetProperty(request.EndpointID, request.CapabilityID, request.PropertyID, request.Value)
		latest.item.Sequence++
		latest.item.LastUpdateAt = time.Now().UTC()
		p.devices[request.DeviceID] = latest
	}
	result := latest.item
	p.mu.Unlock()
	if exists {
		p.notify(result)
	}
	return result, nil
}

func (p *Provider) ExecuteCommand(ctx context.Context, request providersdk.CommandRequest) (device.Device, error) {
	if request.CommandID != "set-power" {
		return device.Device{}, providersdk.ErrCommandUnsupported
	}
	value, ok := request.Parameters["value"]
	if !ok {
		value = request.Parameters["power"]
	}
	return p.WriteProperty(ctx, providersdk.PropertyWriteRequest{DeviceID: request.DeviceID, EndpointID: request.EndpointID, CapabilityID: request.CapabilityID, PropertyID: "power", Value: value})
}

func (p *Provider) Subscribe(handler func(device.Device)) func() {
	if handler == nil {
		return func() {}
	}
	p.mu.Lock()
	p.next++
	id := p.next
	p.listeners[id] = handler
	p.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { p.mu.Lock(); delete(p.listeners, id); p.mu.Unlock() }) }
}

func (p *Provider) SourceCatalog(ctx context.Context) ([]providersdk.SourceCatalogDevice, error) {
	items, err := p.DiscoverDevices(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]providersdk.SourceCatalogDevice, 0, len(items))
	for _, item := range items {
		result = append(result, providersdk.SourceCatalogDevice{Device: item, Catalog: providersdk.SourceCatalogMetadata{Complete: false, Source: "sonoff-uiid-params", SpecType: "sonoff-raw", Model: string(item.Type), FetchedAt: item.LastUpdateAt, Values: providersdk.SnapshotValueStatuses(item)}})
	}
	return result, nil
}

func (p *Provider) ProviderMetrics() map[string]uint64 {
	p.mu.RLock()
	count := uint64(len(p.devices))
	online := uint64(0)
	for _, current := range p.devices {
		if current.item.IsOnline() {
			online++
		}
	}
	p.mu.RUnlock()
	return map[string]uint64{"requests": p.requests.Load(), "writes": p.writes.Load(), "polls": p.polls.Load(), "errors": p.errors.Load(), "realtimeEvents": p.realtimeEvents.Load(), "devices": count, "onlineDevices": online}
}

func (p *Provider) ProviderDiagnostics() map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return map[string]string{"mode": p.config.Mode, "region": p.config.Region, "lan": strconv.FormatBool(p.lan != nil), "cloud": strconv.FormatBool(p.cloud != nil), "realtime": strconv.FormatBool(p.realtime != nil), "refreshIntervalSeconds": strconv.Itoa(p.config.RefreshIntervalSec), "devices": strconv.Itoa(len(p.devices))}
}

func (p *Provider) current(id string) (runtimeDevice, bool) {
	p.mu.RLock()
	current, ok := p.devices[id]
	p.mu.RUnlock()
	return current, ok
}

func (p *Provider) applyState(id string, state map[string]any) {
	latest, exists := p.applyStateSnapshot(id, state)
	if exists {
		p.notify(latest)
	}
}

func (p *Provider) applyStateSnapshot(id string, state map[string]any) (device.Device, bool) {
	params := extractState(state)
	p.mu.Lock()
	current, ok := p.devices[id]
	if ok {
		for key, value := range params {
			current.params[key] = value
		}
		_ = p.commitConfigLocked(current.config, current.params, boolPointer(true))
	}
	latest, exists := p.devices[id]
	p.mu.Unlock()
	return latest.item, exists
}

func (p *Provider) applyRealtimeEvent(event cloud.RealtimeEvent) {
	deviceID := strings.TrimSpace(event.DeviceID)
	if deviceID == "" {
		return
	}
	p.mu.Lock()
	updated := make([]device.Device, 0, 1)
	for id, current := range p.devices {
		if current.config.DeviceID != deviceID {
			continue
		}
		for key, value := range event.Params {
			current.params[key] = value
		}
		online := event.Online
		if online == nil {
			online = boolPointer(true)
		}
		if err := p.commitConfigLocked(current.config, current.params, online); err != nil {
			p.errors.Add(1)
			continue
		}
		updated = append(updated, p.devices[id].item)
	}
	p.mu.Unlock()
	for _, item := range updated {
		p.realtimeEvents.Add(1)
		p.notify(item)
	}
}

func (p *Provider) lanRequest(config DeviceConfig) lan.Request {
	return lan.Request{DeviceID: config.DeviceID, DeviceKey: config.DeviceKey, DIY: config.DIY, Host: config.Host, Port: config.Port}
}

func (p *Provider) notify(item device.Device) {
	p.mu.RLock()
	listeners := make([]func(device.Device), 0, len(p.listeners))
	for _, listener := range p.listeners {
		listeners = append(listeners, listener)
	}
	p.mu.RUnlock()
	for _, listener := range listeners {
		listener(item)
	}
}

func extractState(response map[string]any) map[string]any {
	if data, ok := response["data"].(map[string]any); ok {
		if params, ok := data["params"].(map[string]any); ok {
			return params
		}
		return data
	}
	if params, ok := response["params"].(map[string]any); ok {
		return params
	}
	return response
}

func cloneParams(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
func boolPointer(value bool) *bool { return &value }
func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
func stableID(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			builder.WriteRune(r)
		} else if builder.Len() > 0 {
			builder.WriteByte('-')
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "device"
	}
	return result
}

var _ providersdk.Provider = (*Provider)(nil)
var _ providersdk.Discoverer = (*Provider)(nil)
var _ providersdk.PropertyReader = (*Provider)(nil)
var _ providersdk.PropertyWriter = (*Provider)(nil)
var _ providersdk.CommandExecutor = (*Provider)(nil)
var _ providersdk.EventSubscriber = (*Provider)(nil)
var _ providersdk.DiscoveryScanner = (*Provider)(nil)
