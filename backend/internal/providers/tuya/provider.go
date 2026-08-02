package tuya

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
	tuyaapi "github.com/feranydev/homeloom/backend/internal/providers/tuya/api"
)

type Provider struct {
	id     string
	name   string
	config Config
	client tuyaapi.API

	mu        sync.RWMutex
	refreshMu sync.Mutex
	devices   map[string]device.Device
	sources   map[string]device.Device
	remote    map[string]TuyaDevice
	specs     map[string]map[string]DPSpec
	statuses  map[string]map[string]TuyaStatus
	tuyaIDs   map[string]string
	listeners map[uint64]func(device.Device)
	next      uint64
	interests map[string]struct{}
	lastError string
	running   bool
	cancel    context.CancelFunc
	done      chan struct{}

	requests   atomic.Uint64
	events     atomic.Uint64
	errors     atomic.Uint64
	writes     atomic.Uint64
	refreshes  atomic.Uint64
	mqttEvents atomic.Uint64
}

var (
	_ providersdk.Provider               = (*Provider)(nil)
	_ providersdk.ConnectionTester       = (*Provider)(nil)
	_ providersdk.Discoverer             = (*Provider)(nil)
	_ providersdk.PropertyReader         = (*Provider)(nil)
	_ providersdk.PropertyWriter         = (*Provider)(nil)
	_ providersdk.CommandExecutor        = (*Provider)(nil)
	_ providersdk.EventSubscriber        = (*Provider)(nil)
	_ providersdk.SourceCataloger        = (*Provider)(nil)
	_ providersdk.PropertyInterestSetter = (*Provider)(nil)
	_ providersdk.DiagnosticsReporter    = (*Provider)(nil)
	_ providersdk.MetricsReporter        = (*Provider)(nil)
)

func NewProviderFromConfig(item providerconfig.Config) (*Provider, error) {
	config, err := decodeConfig(item.ID, item.Config)
	if err != nil {
		return nil, err
	}
	var client tuyaapi.API
	if config.usesSharing() {
		client, err = tuyaapi.NewSharingClient(config.Endpoint, config.ClientID, config.UserCode, config.TerminalID, tuyaapi.Token{AccessToken: config.AccessToken, RefreshToken: config.RefreshToken, UID: config.UID}, config.TokenExpiresAt, &http.Client{
			Timeout: time.Duration(config.RequestTimeoutSec) * time.Second,
		})
	} else {
		client, err = tuyaapi.NewClient(config.BaseURL, config.AccessID, config.AccessSecret, &http.Client{
			Timeout: time.Duration(config.RequestTimeoutSec) * time.Second,
		})
	}
	if err != nil {
		return nil, err
	}
	client.SetAccessToken(config.AccessToken)
	return newProvider(item.ID, item.Name, config, client)
}

// NewProviderWithAPI is intended for tests and embedders that already own an
// authenticated Tuya transport. It uses the same config validation as the
// production constructor and never changes the injected client.
func NewProviderWithAPI(item providerconfig.Config, client tuyaapi.API) (*Provider, error) {
	if client == nil {
		return nil, errors.New("tuya api client is required")
	}
	config, err := decodeConfig(item.ID, item.Config)
	if err != nil {
		return nil, err
	}
	return newProvider(item.ID, item.Name, config, client)
}

func newProvider(id, name string, config Config, client tuyaapi.API) (*Provider, error) {
	if !device.ValidStableID(id) {
		return nil, errors.New("tuya provider id must be a stable lowercase id")
	}
	if strings.TrimSpace(name) == "" {
		name = id
	}
	return &Provider{id: id, name: strings.TrimSpace(name), config: config, client: client,
		devices: make(map[string]device.Device), sources: make(map[string]device.Device), remote: make(map[string]TuyaDevice),
		specs: make(map[string]map[string]DPSpec), statuses: make(map[string]map[string]TuyaStatus), tuyaIDs: make(map[string]string),
		listeners: make(map[uint64]func(device.Device)), interests: make(map[string]struct{})}, nil
}

func (p *Provider) Manifest() providersdk.Manifest {
	p.mu.RLock()
	name := p.name
	p.mu.RUnlock()
	return providersdk.Manifest{ID: p.id, Type: ProviderType, Name: name, Version: "0.1.0"}
}

func (p *Provider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyRead: true, PropertyWrite: true, Commands: true, Events: true}
}

// TestConnection performs the minimal live account check used by the
// provider-management API. It intentionally does not initialize polling or
// publish devices, so it is safe to call while editing a provider.
func (p *Provider) TestConnection(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := p.ensureToken(ctx); err != nil {
		return err
	}
	p.requests.Add(1)
	if _, err := p.client.ListUserDevices(ctx, p.config.UID, 1, maximumPageSize); err != nil {
		return fmt.Errorf("Tuya connection test failed: %w", err)
	}
	return nil
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
	lifecycle, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p.running, p.cancel, p.done = true, cancel, make(chan struct{})
	done := p.done
	p.mu.Unlock()
	if err := p.Refresh(ctx); err != nil {
		cancel()
		p.mu.Lock()
		p.running, p.cancel, p.done = false, nil, nil
		p.mu.Unlock()
		return err
	}
	go p.pollLoop(lifecycle, done)
	return nil
}

func (p *Provider) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.cancel, p.done, p.running = nil, nil, false
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Provider) pollLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(time.Duration(p.config.PollIntervalSec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = p.Refresh(ctx)
		}
	}
}

func (p *Provider) Refresh(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	if err := p.ensureToken(ctx); err != nil {
		p.recordError(err)
		return err
	}
	p.requests.Add(1)
	all := make([]TuyaDevice, 0)
	for page := 1; page <= 100; page++ {
		items, err := p.client.ListUserDevices(ctx, p.config.UID, page, maximumPageSize)
		if err != nil {
			p.errors.Add(1)
			p.recordError(err)
			return err
		}
		all = append(all, items...)
		if len(items) < maximumPageSize {
			break
		}
	}
	seen := make(map[string]struct{}, len(all))
	hadDeviceErrors := false
	for _, item := range all {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		seen[item.ID] = struct{}{}
		if err := p.refreshDevice(ctx, item); err != nil {
			// A single unsupported or temporarily unavailable device must not
			// hide the rest of the account directory.
			p.errors.Add(1)
			p.recordError(err)
			hadDeviceErrors = true
			continue
		}
	}
	p.mu.Lock()
	removed := make([]device.Device, 0)
	for remoteID := range p.remote {
		if _, exists := seen[remoteID]; exists {
			continue
		}
		localID := p.tuyaIDs[remoteID]
		item, exists := p.devices[localID]
		if !exists || item.Removed {
			continue
		}
		item.Removed = true
		item.SetOnline(false)
		item.Sequence++
		item.LastUpdateAt = time.Now().UTC()
		p.devices[localID], p.sources[localID] = item, item.Clone()
		removed = append(removed, item.Clone())
	}
	p.mu.Unlock()
	for _, item := range removed {
		p.notify(item)
	}
	p.refreshes.Add(1)
	if !hadDeviceErrors {
		p.mu.Lock()
		p.lastError = ""
		p.mu.Unlock()
	}
	return nil
}

func (p *Provider) refreshDevice(ctx context.Context, item TuyaDevice) error {
	spec, err := p.client.GetSpecification(ctx, item.ID)
	if err != nil {
		return fmt.Errorf("get Tuya device %q specification: %w", item.ID, err)
	}
	if strings.TrimSpace(item.Category) == "" {
		item.Category = spec.Category
	}
	applyQuirks(&spec, item.ProductID, p.config.Quirks)
	status := item.Status
	if len(status) == 0 {
		p.requests.Add(1)
		status, err = p.client.GetStatus(ctx, item.ID)
		if err != nil {
			return fmt.Errorf("get Tuya device %q status: %w", item.ID, err)
		}
	}
	p.commitRemote(item, spec, status, nil)
	return nil
}

func (p *Provider) ensureToken(ctx context.Context) error {
	p.mu.RLock()
	token, refresh, expiry := p.config.AccessToken, p.config.RefreshToken, p.config.TokenExpiresAt
	p.mu.RUnlock()
	if token != "" && (expiry.IsZero() || time.Until(expiry) > 2*time.Minute) {
		p.client.SetAccessToken(token)
		return nil
	}
	var next Token
	var err error
	if refresh != "" {
		next, err = p.client.RefreshToken(ctx, refresh)
	} else {
		next, err = p.client.GetToken(ctx)
	}
	if err != nil {
		return fmt.Errorf("Tuya authentication failed: %w", err)
	}
	if next.UID != "" && next.UID != p.config.UID {
		return errors.New("Tuya token belongs to a different uid")
	}
	expires := time.Time{}
	if next.ExpiresIn > 0 {
		expires = time.Now().Add(time.Duration(next.ExpiresIn) * time.Second)
	}
	p.mu.Lock()
	p.config.AccessToken, p.config.RefreshToken, p.config.TokenExpiresAt = next.AccessToken, next.RefreshToken, expires
	p.mu.Unlock()
	p.client.SetAccessToken(next.AccessToken)
	return nil
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

func (p *Provider) SourceCatalog(context.Context) ([]providersdk.SourceCatalogDevice, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]providersdk.SourceCatalogDevice, 0, len(p.sources))
	for id, item := range p.sources {
		remoteID := p.remoteIDForLocalLocked(id)
		metadata := providersdk.SourceCatalogMetadata{Complete: true, Source: "tuya-device-specification", SpecType: "tuya-dp", Model: tuyaModel(p.remote[remoteID]), FetchedAt: item.LastUpdateAt, Values: make(map[string]providersdk.SourceValueStatus)}
		for _, endpoint := range item.Endpoints {
			for _, capability := range endpoint.Capabilities {
				for _, property := range capability.Properties {
					metadata.Values[providersdk.SourceValueKey(endpoint.ID, capability.ID, property.Definition.ID)] = providersdk.SourceValueStatus{Known: true, Available: item.IsOnline(), ObservedAt: item.LastUpdateAt}
				}
			}
		}
		result = append(result, providersdk.SourceCatalogDevice{Device: item.Clone(), Catalog: metadata})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (p *Provider) ReadProperty(ctx context.Context, request providersdk.PropertyReadRequest) (device.Property, error) {
	p.mu.RLock()
	item, exists := p.devices[request.DeviceID]
	p.mu.RUnlock()
	if !exists {
		return device.Property{}, providersdk.ErrDeviceNotFound
	}
	property, exists := item.Property(request.EndpointID, request.CapabilityID, request.PropertyID)
	if !exists || !property.Definition.Readable {
		return device.Property{}, providersdk.ErrPropertyUnsupported
	}
	if !item.IsOnline() {
		return device.Property{}, providersdk.ErrProviderUnavailable
	}
	return property, nil
}

func (p *Provider) WriteProperty(ctx context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.RLock()
	item, exists := p.devices[request.DeviceID]
	remoteID := p.remoteIDForLocalLocked(request.DeviceID)
	specs := cloneSpecs(p.specs[remoteID])
	remoteItem := p.remote[remoteID]
	p.mu.RUnlock()
	if !exists {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	property, ok := item.Property(request.EndpointID, request.CapabilityID, request.PropertyID)
	if !ok || !property.Definition.Writable {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	property.Value = request.Value
	if err := property.Validate(); err != nil {
		return device.Device{}, fmt.Errorf("%w: %v", providersdk.ErrPropertyInvalid, err)
	}
	spec, ok := specs[request.PropertyID]
	if !ok {
		for code, candidate := range specs {
			if sanitizeDPID(code) == request.PropertyID {
				spec, ok = candidate, true
				break
			}
		}
	}
	if !ok {
		code, candidate, found := commonDPCode(remoteItem, specs, request.EndpointID, request.CapabilityID, request.PropertyID)
		if found {
			spec, ok = candidate, true
			spec.Code = code
		}
	}
	if !ok {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	raw, err := encodeDPValue(spec, request.Value)
	if err != nil {
		return device.Device{}, fmt.Errorf("%w: %v", providersdk.ErrPropertyInvalid, err)
	}
	p.requests.Add(1)
	if err := p.client.SendCommands(ctx, remoteID, []TuyaCommand{{Code: spec.Code, Value: raw}}); err != nil {
		p.errors.Add(1)
		return device.Device{}, fmt.Errorf("%w: %v", providersdk.ErrWriteRejected, err)
	}
	p.writes.Add(1)
	p.commitStatus(remoteID, []TuyaStatus{{Code: spec.Code, Value: raw}}, nil, "")
	p.mu.RLock()
	updated := p.devices[request.DeviceID].Clone()
	p.mu.RUnlock()
	return updated, nil
}

func (p *Provider) remoteIDForLocalLocked(localID string) string {
	for remoteID, candidate := range p.tuyaIDs {
		if candidate == localID {
			return remoteID
		}
	}
	return ""
}

func (p *Provider) ExecuteCommand(ctx context.Context, request providersdk.CommandRequest) (device.Device, error) {
	if request.EndpointID != "main" || request.CapabilityID != "tuya-dp" || request.CommandID != "set-dp" {
		return device.Device{}, providersdk.ErrCommandUnsupported
	}
	code, ok := request.Parameters["code"]
	if !ok || code.String == nil || code.Type != device.ValueTypeString {
		return device.Device{}, providersdk.ErrCommandInvalid
	}
	value, ok := request.Parameters["value"]
	if !ok {
		return device.Device{}, providersdk.ErrCommandInvalid
	}
	return p.WriteProperty(ctx, providersdk.PropertyWriteRequest{DeviceID: request.DeviceID, EndpointID: "main", CapabilityID: "tuya-dp", PropertyID: *code.String, Value: value})
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

func (p *Provider) SetPropertyInterests(interests []providersdk.PropertyInterest) {
	next := make(map[string]struct{})
	for _, item := range interests {
		if item.ProviderID != "" && item.ProviderID != p.id {
			continue
		}
		if item.DeviceID != "" && item.PropertyID != "" {
			next[item.DeviceID+"/"+item.EndpointID+"/"+item.CapabilityID+"/"+item.PropertyID] = struct{}{}
		}
	}
	p.mu.Lock()
	p.interests = next
	p.mu.Unlock()
}

// HandleMQTTMessage lets an external MQTT owner feed Tuya status messages into
// the same reconciliation path as HTTP. It is intentionally separate from
// Initialize because Tuya's app-specific openHubConfig handshake returns
// temporary credentials that must be supplied by the hosting integration.
func (p *Provider) HandleMQTTMessage(payload []byte) error {
	p.mu.RLock()
	accessKey := ""
	if p.config.MQTT != nil {
		accessKey = p.config.MQTT.AccessKey
		if accessKey == "" {
			// openHubConfig returns the message accessKey separately from the
			// broker password. AccessSecret is a compatibility fallback for
			// integrations that use the cloud project secret as that key.
			accessKey = p.config.AccessSecret
		}
	}
	p.mu.RUnlock()
	event, err := DecodeMQTTMessage(payload, accessKey)
	if err != nil {
		return err
	}
	if event.Online != nil {
		p.commitStatus(event.DeviceID, event.Status, event.Online, event.Name)
	} else {
		p.commitStatus(event.DeviceID, event.Status, nil, event.Name)
	}
	p.mqttEvents.Add(1)
	return nil
}

func (p *Provider) ProviderMetrics() map[string]uint64 {
	p.mu.RLock()
	devices, online := uint64(len(p.devices)), uint64(0)
	for _, item := range p.devices {
		if item.IsOnline() {
			online++
		}
	}
	p.mu.RUnlock()
	return map[string]uint64{"requests": p.requests.Load(), "events": p.events.Load(), "errors": p.errors.Load(), "writes": p.writes.Load(), "refreshes": p.refreshes.Load(), "mqttEvents": p.mqttEvents.Load(), "devices": devices, "onlineDevices": online}
}

func (p *Provider) ProviderDiagnostics() map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	state := "stopped"
	if p.running {
		state = "running"
	}
	result := map[string]string{"state": state, "devices": strconv.Itoa(len(p.devices)), "transport": "cloud-http"}
	if p.config.MQTT != nil && p.config.MQTT.Enabled {
		result["mqtt"] = "external-feed"
	}
	if p.lastError != "" {
		result["lastError"] = p.lastError
	}
	return result
}

func (p *Provider) recordError(err error) { p.mu.Lock(); p.lastError = err.Error(); p.mu.Unlock() }

func (p *Provider) commitRemote(item TuyaDevice, specification TuyaSpecification, status []TuyaStatus, online *bool) {
	p.mu.Lock()
	if online == nil {
		value := item.Online
		online = &value
	}
	localID := p.tuyaIDs[item.ID]
	if localID == "" {
		localID = stableDeviceID(item.ID)
		p.tuyaIDs[item.ID] = localID
	}
	previous := p.devices[localID]
	p.remote[item.ID] = item
	p.specs[item.ID] = mergedSpecs(specification)
	p.statuses[item.ID] = statusMap(status)
	current := buildSourceDevice(p.id, localID, item, p.specs[item.ID], p.statuses[item.ID], *online)
	current.Sequence, current.LastUpdateAt = previous.Sequence, previous.LastUpdateAt
	if !sameDevice(previous, current) {
		current.Sequence++
		current.LastUpdateAt = time.Now().UTC()
		p.events.Add(1)
	} else if current.LastUpdateAt.IsZero() {
		current.LastUpdateAt = time.Now().UTC()
	}
	p.devices[localID], p.sources[localID] = current, current.Clone()
	p.mu.Unlock()
	p.notify(current)
}

func (p *Provider) commitStatus(remoteID string, status []TuyaStatus, online *bool, name string) {
	p.mu.Lock()
	item, exists := p.remote[remoteID]
	if !exists {
		p.mu.Unlock()
		return
	}
	if name != "" {
		item.Name = name
	}
	if online != nil {
		item.Online = *online
	}
	merged := p.statuses[remoteID]
	if merged == nil {
		merged = make(map[string]TuyaStatus)
	}
	for _, value := range status {
		merged[value.Code] = value
	}
	p.statuses[remoteID] = merged
	specs := p.specs[remoteID]
	localID := p.tuyaIDs[remoteID]
	previous := p.devices[localID]
	current := buildSourceDevice(p.id, localID, item, specs, merged, item.Online)
	current.Sequence, current.LastUpdateAt = previous.Sequence, previous.LastUpdateAt
	if !sameDevice(previous, current) {
		current.Sequence++
		current.LastUpdateAt = time.Now().UTC()
		p.events.Add(1)
	}
	p.devices[localID], p.sources[localID] = current, current.Clone()
	p.mu.Unlock()
	p.notify(current)
}

func (p *Provider) notify(item device.Device) {
	p.mu.RLock()
	listeners := make([]func(device.Device), 0, len(p.listeners))
	for _, listener := range p.listeners {
		listeners = append(listeners, listener)
	}
	p.mu.RUnlock()
	for _, listener := range listeners {
		listener(item.Clone())
	}
}
func buildSourceDevice(providerID, localID string, item TuyaDevice, specs map[string]DPSpec, statuses map[string]TuyaStatus, online bool) device.Device {
	properties := make([]device.Property, 0, len(specs))
	codes := make([]string, 0, len(specs))
	for code := range specs {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	seen := make(map[string]struct{})
	for _, code := range codes {
		spec := specs[code]
		id := sanitizeDPID(code)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			id = id + "-" + strconv.Itoa(len(seen))
		}
		seen[id] = struct{}{}
		definition, value := propertyFromSpec(spec, statuses[code])
		properties = append(properties, device.Property{Definition: definition, Value: value, StateTransport: device.StateTransportCloudHTTP})
	}
	name := item.Name
	if strings.TrimSpace(name) == "" {
		name = item.ID
	}
	dtype := inferDeviceType(item.Category, specs)
	result := device.Device{SchemaVersion: device.SchemaVersion, ID: localID, ProviderID: providerID, Name: name, Type: dtype, HomeID: item.HomeID, HomeName: item.HomeName, RoomID: item.RoomID, RoomName: item.RoomName, RuntimeMode: device.RuntimeModeCloud, StateTransport: device.StateTransportCloudHTTP, Sequence: 1, LastUpdateAt: time.Now().UTC(), Endpoints: []device.Endpoint{{ID: "main", Name: "Tuya", Type: "tuya", Capabilities: []device.Capability{{ID: "tuya-dp", Type: "tuya-dp", Properties: properties}}}}}
	result.SetOnline(online)
	result.Endpoints[0].Capabilities = append(result.Endpoints[0].Capabilities, buildCommonCapabilities(item, specs, statuses)...)
	return result
}

func propertyFromSpec(spec DPSpec, status TuyaStatus) (device.PropertyDefinition, device.PropertyValue) {
	_ = spec.Normalize()
	typeValue := spec.Type.Canonical()
	value := any(nil)
	if status.Code != "" {
		value = status.Value
	}
	parsed, err := parseStatusValue(spec, value)
	if err != nil {
		parsed = defaultDPValue(spec)
	}
	propertyType := device.ValueTypeString
	switch typeValue {
	case DPTypeBoolean:
		propertyType = device.ValueTypeBool
	case DPTypeEnum:
		if len(spec.EnumValues) > 0 {
			propertyType = device.ValueTypeEnum
		}
	case DPTypeInteger, DPTypeNumber:
		if spec.Scale == 0 && integerRange(spec) {
			propertyType = device.ValueTypeInt
		} else {
			propertyType = device.ValueTypeNumber
		}
	}
	definition := device.PropertyDefinition{ID: sanitizeDPID(spec.Code), Name: spec.Name, Type: propertyType, Unit: normalizeUnit(spec.Unit), Readable: spec.Readable, Writable: spec.Writable, Notifiable: spec.Readable, Enum: append([]string(nil), spec.EnumValues...)}
	if definition.Name == "" {
		definition.Name = spec.Code
	}
	definition.Min, definition.Max, definition.Step = scaledBounds(spec)
	if definition.Type == device.ValueTypeEnum && len(definition.Enum) == 0 {
		definition.Type = device.ValueTypeString
	}
	return definition, propertyValue(propertyType, parsed, definition.Enum)
}

func propertyValue(valueType device.ValueType, value any, enum []string) device.PropertyValue {
	switch valueType {
	case device.ValueTypeBool:
		if v, ok := value.(bool); ok {
			return device.BoolValue(v)
		}
		return device.BoolValue(false)
	case device.ValueTypeInt:
		if v, ok := numericInt(value); ok {
			return device.IntValue(v)
		}
		return device.IntValue(0)
	case device.ValueTypeNumber:
		if v, ok := numericFloat(value); ok {
			return device.NumberValue(v)
		}
		return device.NumberValue(0)
	case device.ValueTypeEnum:
		if v, ok := value.(string); ok {
			for _, item := range enum {
				if item == v {
					return device.EnumValue(v)
				}
			}
		}
		if len(enum) > 0 {
			return device.EnumValue(enum[0])
		}
		return device.StringValue("")
	default:
		if v, ok := value.(string); ok {
			return device.StringValue(v)
		}
		encoded, _ := json.Marshal(value)
		return device.StringValue(string(encoded))
	}
}

func encodeDPValue(spec DPSpec, value device.PropertyValue) (any, error) {
	var raw any
	switch value.Type {
	case device.ValueTypeBool:
		if value.Bool == nil {
			return nil, errors.New("bool value missing")
		}
		raw = *value.Bool
	case device.ValueTypeEnum, device.ValueTypeString:
		if value.String == nil {
			return nil, errors.New("string value missing")
		}
		raw = *value.String
	case device.ValueTypeInt:
		if value.Int == nil {
			return nil, errors.New("int value missing")
		}
		raw = *value.Int
	case device.ValueTypeNumber:
		if value.Number == nil {
			return nil, errors.New("number value missing")
		}
		raw = *value.Number
	default:
		return nil, errors.New("unsupported value type")
	}
	if spec.Type.Canonical() == DPTypeInteger || spec.Type.Canonical() == DPTypeNumber {
		number, ok := numericFloat(raw)
		if !ok {
			return nil, errors.New("numeric value required")
		}
		number *= math.Pow10(spec.Scale)
		if spec.Type.Canonical() == DPTypeInteger {
			if math.Trunc(number) != number {
				return nil, errors.New("Tuya integer command is not integral")
			}
			return int64(number), nil
		}
		return number, nil
	}
	return raw, nil
}

func mergedSpecs(specification TuyaSpecification) map[string]DPSpec {
	result := make(map[string]DPSpec)
	for _, spec := range specification.Properties {
		spec.Readable = true
		result[spec.Code] = spec
	}
	for _, spec := range specification.Status {
		spec.Readable = true
		result[spec.Code] = spec
	}
	for _, spec := range specification.Functions {
		current := result[spec.Code]
		if current.Code == "" {
			current = spec
		}
		if current.Name == "" {
			current.Name = spec.Name
		}
		if current.Type == "" {
			current.Type = spec.Type
		}
		if current.Min == nil {
			current.Min = spec.Min
		}
		if current.Max == nil {
			current.Max = spec.Max
		}
		if current.Step == nil {
			current.Step = spec.Step
		}
		if current.Scale == 0 {
			current.Scale = spec.Scale
		}
		if current.Unit == "" {
			current.Unit = spec.Unit
		}
		if current.MaxLength == 0 {
			current.MaxLength = spec.MaxLength
		}
		if len(current.EnumValues) == 0 {
			current.EnumValues = append([]string(nil), spec.EnumValues...)
		}
		if len(current.Enum) == 0 {
			current.Enum = append([]string(nil), spec.Enum...)
		}
		current.Writable = true
		result[spec.Code] = current
	}
	return result
}
func statusMap(values []TuyaStatus) map[string]TuyaStatus {
	result := make(map[string]TuyaStatus, len(values))
	for _, value := range values {
		result[value.Code] = value
	}
	return result
}
func cloneSpecs(value map[string]DPSpec) map[string]DPSpec {
	result := make(map[string]DPSpec, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
func sameDevice(left, right device.Device) bool {
	left.Sequence, right.Sequence = 0, 0
	left.LastUpdateAt, right.LastUpdateAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(left, right)
}

func stableDeviceID(remoteID string) string {
	value := sanitizeDPID(remoteID)
	if value == "" {
		value = "device"
	}
	result := "tuya-" + value
	if len(result) > 63 {
		result = result[:63]
	}
	return result
}
func sanitizeDPID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('-')
		}
	}
	result := strings.Trim(builder.String(), "-_.")
	if result == "" {
		return "dp"
	}
	if len(result) > 63 {
		result = result[:63]
	}
	return result
}
func inferDeviceType(category string, specs map[string]DPSpec) device.Type {
	category = strings.ToLower(strings.TrimSpace(category))
	switch category {
	case "dj", "dd":
		return device.TypeLightbulb
	case "cz", "pc", "kg", "dlq":
		if category == "cz" || category == "pc" || category == "dlq" || hasCode(specs, "cur_power", "cur_voltage", "cur_current") {
			return device.TypeOutlet
		}
		return device.TypeSwitch
	case "cl", "ds":
		return device.TypeWindowCovering
	case "fs", "fjd":
		return device.TypeFan
	case "kj", "air", "air_purifier", "air-purifier":
		return device.TypeAirPurifier
	case "kt", "ac", "air_conditioner", "air-conditioner":
		return device.TypeAirConditioner
	case "jsq", "water_heater", "water-heater":
		return device.TypeWaterHeater
	case "js", "jq", "humidifier", "dehumidifier", "humidifier-dehumidifier":
		return device.TypeHumidifierDehumidifier
	case "zndb", "power_meter", "power-meter", "energy":
		return device.TypePowerMeter
	case "sd", "robot", "robot_vacuum", "robot-vacuum":
		return device.TypeRobotVacuum
	case "ws", "temp", "temperature", "temperature_sensor":
		return device.TypeTemperatureSensor
	case "wsdcg", "temp_humidity", "temperature_humidity", "temperature-humidity":
		return device.TypeTemperatureHumiditySensor
	case "sj", "mshj", "humidity", "humidity_sensor", "湿度":
		return device.TypeHumiditySensor
	case "pir", "ty", "hps", "ldcg", "motion", "motion_sensor":
		return device.TypeMotionSensor
	case "mcs", "door", "contact", "contact_sensor":
		return device.TypeContactSensor
	case "ywbj", "water", "leak", "leak_sensor":
		return device.TypeLeakSensor
	case "ywb", "smoke", "smoke_sensor":
		return device.TypeSmokeSensor
	case "pm2.5", "pm25", "air_quality", "air-quality":
		return device.TypeAirQualitySensor
	case "light_sensor", "illuminance", "照度":
		return device.TypeIlluminanceSensor
	default:
		if hasCode(specs, "bright_value", "bright_value_v2", "brightness") {
			return device.TypeLightbulb
		}
		if hasCode(specs, "fan_speed", "fan_speed_enum", "speed_value") && hasCode(specs, "switch", "fan_switch", "power") {
			return device.TypeFan
		}
		if hasCode(specs, "percent_control", "position", "position_1", "cur_position") {
			return device.TypeWindowCovering
		}
		if hasCode(specs, "va_temperature", "temp_current", "temperature", "temp_value") && hasCode(specs, "va_humidity", "humidity_value", "humidity") {
			return device.TypeTemperatureHumiditySensor
		}
		return device.Type("tuya-" + sanitizeDPID(category))
	}
}

func tuyaModel(item TuyaDevice) string {
	for _, value := range []string{item.Model, item.ProductName, item.ProductID, item.CategoryName, item.Category} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
func hasCode(specs map[string]DPSpec, codes ...string) bool {
	for _, code := range codes {
		if _, ok := specs[code]; ok {
			return true
		}
	}
	return false
}
func integerRange(spec DPSpec) bool {
	return spec.Min == nil || (math.Trunc(*spec.Min) == *spec.Min && (spec.Max == nil || math.Trunc(*spec.Max) == *spec.Max) && (spec.Step == nil || math.Trunc(*spec.Step) == *spec.Step))
}
func scaledBounds(spec DPSpec) (*float64, *float64, *float64) {
	scale := math.Pow10(spec.Scale)
	scaleOne := func(value *float64) *float64 {
		if value == nil {
			return nil
		}
		result := *value / scale
		return &result
	}
	return scaleOne(spec.Min), scaleOne(spec.Max), scaleOne(spec.Step)
}
func normalizeUnit(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "℃", "°c", "c", "celsius":
		return "celsius"
	case "%", "percent":
		return "percent"
	case "w", "watt":
		return "watt"
	case "v", "volt":
		return "volt"
	case "ma", "milliampere":
		return "milliampere"
	case "a", "ampere":
		return "ampere"
	case "kwh", "kilowatt-hour":
		return "kilowatt-hour"
	case "lux", "lx":
		return "lux"
	case "mired", "mirek":
		return "mired"
	case "hz", "hertz":
		return "hertz"
	case "pa", "hpa", "hectopascal":
		return "hectopascal"
	case "ppm":
		return "ppm"
	case "ratio":
		return "ratio"
	default:
		return value
	}
}
func defaultDPValue(spec DPSpec) any {
	switch spec.Type.Canonical() {
	case DPTypeBoolean:
		return false
	case DPTypeEnum:
		if len(spec.EnumValues) > 0 {
			return spec.EnumValues[0]
		}
		return ""
	case DPTypeInteger, DPTypeNumber:
		if spec.Min != nil {
			return scaleValue(*spec.Min, spec.Scale)
		}
		return int64(0)
	default:
		return ""
	}
}
func numericFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	default:
		return 0, false
	}
}
func numericInt(value any) (int64, bool) {
	number, ok := numericFloat(value)
	return int64(number), ok && math.Trunc(number) == number
}
