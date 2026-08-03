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

type runtimeDevice struct {
	config DeviceConfig
	item   device.Device
	params map[string]any
}

type Provider struct {
	id     string
	name   string
	config Config

	lan   lanTransport
	cloud cloudTransport

	mu        sync.RWMutex
	running   bool
	devices   map[string]runtimeDevice
	listeners map[uint64]func(device.Device)
	next      uint64
	requests  atomic.Uint64
	errors    atomic.Uint64
	writes    atomic.Uint64
}

func NewProviderFromConfig(item providerconfig.Config) (*Provider, error) {
	config, err := decodeConfig(item)
	if err != nil {
		return nil, err
	}
	requestTimeout := time.Duration(config.RequestTimeoutSeconds) * time.Second
	var cloudClient cloudTransport
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
	}
	return newProvider(item.ID, item.Name, config, lan.NewClient(http.DefaultClient, requestTimeout), cloudClient), nil
}

// NewProviderWithTransports is used by deterministic tests and embedding
// integrations. Production callers should use NewProviderFromConfig.
func NewProviderWithTransports(item providerconfig.Config, local lanTransport, remote cloudTransport) (*Provider, error) {
	config, err := decodeConfig(item)
	if err != nil {
		return nil, err
	}
	return newProvider(item.ID, item.Name, config, local, remote), nil
}

func newProvider(id, name string, config Config, local lanTransport, remote cloudTransport) *Provider {
	return &Provider{id: id, name: name, config: config, lan: local, cloud: remote, devices: make(map[string]runtimeDevice), listeners: make(map[uint64]func(device.Device))}
}

func (p *Provider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: ProviderType, Name: p.name, Version: "0.1.0"}
}

func (p *Provider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyRead: true, PropertyWrite: true, Commands: true, Events: true}
}

func (p *Provider) Initialize(context.Context) error {
	p.mu.Lock()
	p.running = true
	p.mu.Unlock()
	return nil
}

func (p *Provider) Close(context.Context) error {
	p.mu.Lock()
	p.running = false
	p.mu.Unlock()
	return nil
}

func (p *Provider) DiscoverDevices(ctx context.Context) ([]device.Device, error) {
	p.requests.Add(1)
	if p.cloud != nil && p.config.Mode != ModeLocal {
		if err := p.refreshCloud(ctx); err != nil && p.config.Mode == ModeCloud {
			p.errors.Add(1)
			return nil, err
		}
	}
	p.mu.Lock()
	for _, configured := range p.config.Devices {
		if _, exists := p.devices[configured.ID]; exists {
			continue
		}
		if err := p.commitConfigLocked(configured, nil, nil); err != nil {
			p.mu.Unlock()
			p.errors.Add(1)
			return nil, err
		}
	}
	result := make([]device.Device, 0, len(p.devices))
	for _, current := range p.devices {
		result = append(result, current.item)
	}
	p.mu.Unlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
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
		configured := p.configForRemoteLocked(deviceID, remote)
		if err := p.commitConfigLocked(configured, remote.Params, boolPointer(remote.Online)); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) configForRemoteLocked(deviceID string, remote cloud.Device) DeviceConfig {
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
			return configured
		}
	}
	id := "sonoff-" + stableID(deviceID)
	return DeviceConfig{ID: id, DeviceID: deviceID, Name: nonEmpty(remote.Name, deviceID), Model: remote.Model, UIID: remote.UIID, HomeID: remote.HomeID, HomeName: remote.HomeName, RoomID: remote.RoomID, RoomName: remote.RoomName, DeviceKey: remote.DeviceKey, Host: "", Port: defaultLANPort, Channels: 1}
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
	return map[string]uint64{"requests": p.requests.Load(), "writes": p.writes.Load(), "errors": p.errors.Load(), "devices": count, "onlineDevices": online}
}

func (p *Provider) ProviderDiagnostics() map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return map[string]string{"mode": p.config.Mode, "region": p.config.Region, "lan": strconv.FormatBool(p.lan != nil), "cloud": strconv.FormatBool(p.cloud != nil), "devices": strconv.Itoa(len(p.devices))}
}

func (p *Provider) current(id string) (runtimeDevice, bool) {
	p.mu.RLock()
	current, ok := p.devices[id]
	p.mu.RUnlock()
	return current, ok
}

func (p *Provider) applyState(id string, state map[string]any) {
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
	if exists {
		p.notify(latest.item)
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
