package gree

import (
	"context"
	"errors"
	"fmt"
	"math"
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

type Provider struct {
	id        string
	name      string
	config    Config
	transport Transport

	mu        sync.RWMutex
	configs   map[string]DeviceConfig
	devices   map[string]device.Device
	raw       map[string]map[string]any
	keys      map[string][]byte
	listeners map[uint64]func(device.Device)
	next      uint64

	running    bool
	cancel     context.CancelFunc
	done       chan struct{}
	lastErrors map[string]string

	requests atomic.Uint64
	binds    atomic.Uint64
	polls    atomic.Uint64
	writes   atomic.Uint64
	events   atomic.Uint64
	errors   atomic.Uint64
}

var (
	_ providersdk.Provider            = (*Provider)(nil)
	_ providersdk.ConnectionTester    = (*Provider)(nil)
	_ providersdk.Discoverer          = (*Provider)(nil)
	_ providersdk.PropertyReader      = (*Provider)(nil)
	_ providersdk.PropertyWriter      = (*Provider)(nil)
	_ providersdk.EventSubscriber     = (*Provider)(nil)
	_ providersdk.MetricsReporter     = (*Provider)(nil)
	_ providersdk.DiagnosticsReporter = (*Provider)(nil)
)

func NewProviderFromConfig(item providerconfig.Config) (*Provider, error) {
	config, err := decodeConfig(item)
	if err != nil {
		return nil, err
	}
	return newProvider(item, config, newUDPTransport(config.requestTimeout()))
}

// NewProviderWithTransport is useful to embedders that already own a UDP
// socket abstraction, and keeps Provider tests independent from the LAN.
func NewProviderWithTransport(item providerconfig.Config, transport Transport) (*Provider, error) {
	config, err := decodeConfig(item)
	if err != nil {
		return nil, err
	}
	if transport == nil {
		return nil, errors.New("Gree Provider transport is required")
	}
	return newProvider(item, config, transport)
}

func newProvider(item providerconfig.Config, config Config, transport Transport) (*Provider, error) {
	if !device.ValidStableID(item.ID) {
		return nil, errors.New("gree Provider id must be a stable lowercase id")
	}
	if strings.TrimSpace(item.Name) == "" {
		return nil, errors.New("gree Provider name is required")
	}
	p := &Provider{
		id: item.ID, name: strings.TrimSpace(item.Name), config: config, transport: transport,
		configs: make(map[string]DeviceConfig), devices: make(map[string]device.Device), raw: make(map[string]map[string]any),
		keys: make(map[string][]byte), listeners: make(map[uint64]func(device.Device)), lastErrors: make(map[string]string),
	}
	for _, configured := range config.Devices {
		if !configured.enabled() {
			continue
		}
		subMAC, mainMAC := configured.macParts()
		if subMAC == "" || mainMAC == "" {
			return nil, fmt.Errorf("device %q has an invalid mac", configured.ID)
		}
		p.configs[configured.ID] = configured
		raw := defaultRawState(configured)
		p.raw[configured.ID] = raw
		if configured.EncryptionKey != "" {
			p.keys[configured.ID] = []byte(configured.EncryptionKey)
		}
		item, err := buildAirConditionerDevice(p.id, configured, raw, configured.DisableAvailableCheck)
		if err != nil {
			return nil, fmt.Errorf("build Gree device %q: %w", configured.ID, err)
		}
		p.devices[configured.ID] = item
	}
	return p, nil
}

func (p *Provider) Manifest() providersdk.Manifest {
	p.mu.RLock()
	name := p.name
	p.mu.RUnlock()
	return providersdk.Manifest{ID: p.id, Type: ProviderType, Name: name, Version: "0.1.0"}
}

func (p *Provider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyRead: true, PropertyWrite: true, Events: true}
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
	lifecycle, cancel := context.WithCancel(context.Background())
	p.running, p.cancel, p.done = true, cancel, make(chan struct{})
	done := p.done
	p.mu.Unlock()

	// A Gree unit may be temporarily asleep or unreachable. Keep the Provider
	// alive and expose the unit as offline so the runtime can recover on the
	// next poll instead of failing the whole configured catalog.
	p.refreshAll(ctx)
	go p.pollLoop(lifecycle, done)
	return nil
}

// TestConnection performs a live status read against the configured devices.
// Initialize intentionally keeps an offline catalog recoverable, so it cannot
// by itself be used to validate connectivity.
func (p *Provider) TestConnection(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.RLock()
	ids := make([]string, 0, len(p.configs))
	for id := range p.configs {
		ids = append(ids, id)
	}
	p.mu.RUnlock()
	sort.Strings(ids)
	if len(ids) == 0 {
		return errors.New("Gree connection test requires at least one enabled device")
	}

	failures := make([]error, 0, len(ids))
	for _, id := range ids {
		if err := p.refreshDevice(ctx, id); err == nil {
			return nil
		} else {
			failures = append(failures, fmt.Errorf("device %q: %w", id, err))
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return fmt.Errorf("Gree connection test failed: %w", errors.Join(failures...))
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
	ticker := time.NewTicker(p.config.pollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.refreshAll(ctx)
		}
	}
}

func (p *Provider) refreshAll(ctx context.Context) {
	p.mu.RLock()
	ids := make([]string, 0, len(p.configs))
	for id := range p.configs {
		ids = append(ids, id)
	}
	p.mu.RUnlock()
	sort.Strings(ids)
	for _, id := range ids {
		if err := p.refreshDevice(ctx, id); err != nil && ctx.Err() != nil {
			return
		}
	}
}

func (p *Provider) refreshDevice(ctx context.Context, id string) error {
	configured, ok := p.configFor(id)
	if !ok {
		return providersdk.ErrDeviceNotFound
	}
	key, err := p.keyForDevice(ctx, configured)
	if err != nil {
		p.recordFailure(id, err)
		return err
	}
	subMAC, mainMAC := configured.macParts()
	packet, err := buildStatusPacket(key, subMAC, mainMAC, configured.UID, configured.EncryptionVersion)
	if err != nil {
		p.recordFailure(id, err)
		return err
	}
	p.requests.Add(1)
	response, err := p.transport.Exchange(ctx, configured.Host, configured.Port, packet)
	if err != nil {
		p.errors.Add(1)
		p.recordFailure(id, err)
		return err
	}
	values, err := parseStatusResponse(response, key, configured.EncryptionVersion)
	if err != nil {
		p.errors.Add(1)
		p.recordFailure(id, err)
		return err
	}
	p.polls.Add(1)
	p.applyRaw(id, values, true)
	p.recordSuccess(id)
	return nil
}

func (p *Provider) keyForDevice(ctx context.Context, configured DeviceConfig) ([]byte, error) {
	p.mu.RLock()
	key := append([]byte(nil), p.keys[configured.ID]...)
	p.mu.RUnlock()
	if len(key) > 0 {
		return key, nil
	}
	_, mainMAC := configured.macParts()
	packet, err := buildBindPacket(mainMAC, configured.EncryptionVersion)
	if err != nil {
		return nil, err
	}
	p.requests.Add(1)
	response, err := p.transport.Exchange(ctx, configured.Host, configured.Port, packet)
	if err != nil {
		p.errors.Add(1)
		return nil, fmt.Errorf("bind Gree device %q: %w", configured.ID, err)
	}
	key, err = parseBindResponse(response, configured.EncryptionVersion)
	if err != nil {
		p.errors.Add(1)
		return nil, fmt.Errorf("bind Gree device %q: %w", configured.ID, err)
	}
	p.binds.Add(1)
	p.mu.Lock()
	p.keys[configured.ID] = append([]byte(nil), key...)
	p.mu.Unlock()
	return key, nil
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

func (p *Provider) ReadProperty(ctx context.Context, request providersdk.PropertyReadRequest) (device.Property, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	item, property, ok := p.property(request.DeviceID, request.EndpointID, request.CapabilityID, request.PropertyID)
	if !ok {
		p.mu.RLock()
		_, deviceExists := p.devices[request.DeviceID]
		p.mu.RUnlock()
		if !deviceExists {
			return device.Property{}, providersdk.ErrDeviceNotFound
		}
		return device.Property{}, providersdk.ErrPropertyUnsupported
	}
	if !property.Definition.Readable {
		return device.Property{}, providersdk.ErrPropertyUnsupported
	}
	if err := p.refreshDevice(ctx, request.DeviceID); err != nil {
		if ctx != nil && ctx.Err() != nil {
			return device.Property{}, ctx.Err()
		}
		return device.Property{}, fmt.Errorf("%w: %v", providersdk.ErrProviderUnavailable, err)
	}
	p.mu.RLock()
	item = p.devices[request.DeviceID].Clone()
	p.mu.RUnlock()
	if !item.IsOnline() && !localGreeProperty(request.PropertyID) {
		return device.Property{}, providersdk.ErrProviderUnavailable
	}
	property, ok = item.Property(request.EndpointID, request.CapabilityID, request.PropertyID)
	if !ok {
		return device.Property{}, providersdk.ErrPropertyUnsupported
	}
	return property, nil
}

func (p *Provider) WriteProperty(ctx context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	configured, ok := p.configFor(request.DeviceID)
	if !ok {
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	item, property, ok := p.property(request.DeviceID, request.EndpointID, request.CapabilityID, request.PropertyID)
	if !ok {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	if !property.Definition.Writable {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	property.Value = request.Value
	if err := property.Validate(); err != nil {
		return device.Device{}, fmt.Errorf("%w: %v", providersdk.ErrPropertyInvalid, err)
	}
	if !item.IsOnline() {
		if err := p.refreshDevice(ctx, request.DeviceID); err != nil {
			if ctx != nil && ctx.Err() != nil {
				return device.Device{}, ctx.Err()
			}
			return device.Device{}, fmt.Errorf("%w: %v", providersdk.ErrProviderUnavailable, err)
		}
		item, _, _ = p.property(request.DeviceID, request.EndpointID, request.CapabilityID, request.PropertyID)
	}
	p.mu.RLock()
	raw := cloneRaw(p.raw[request.DeviceID])
	p.mu.RUnlock()
	options, values, updates, err := commandFor(request.PropertyID, request.Value, raw)
	if err != nil {
		return device.Device{}, err
	}
	if len(options) > 0 {
		key, err := p.keyForDevice(ctx, configured)
		if err != nil {
			p.recordFailure(request.DeviceID, err)
			return device.Device{}, fmt.Errorf("%w: %v", providersdk.ErrProviderUnavailable, err)
		}
		subMAC, mainMAC := configured.macParts()
		packet, err := buildCommandPacket(key, subMAC, mainMAC, configured.UID, options, values, configured.EncryptionVersion)
		if err != nil {
			return device.Device{}, err
		}
		p.requests.Add(1)
		response, err := p.transport.Exchange(ctx, configured.Host, configured.Port, packet)
		if err != nil {
			p.errors.Add(1)
			p.recordFailure(request.DeviceID, err)
			p.markOffline(request.DeviceID, err)
			return device.Device{}, fmt.Errorf("%w: %v", providersdk.ErrProviderUnavailable, err)
		}
		if _, err := decodeEnvelope(response, key, configured.EncryptionVersion); err != nil {
			p.errors.Add(1)
			p.recordFailure(request.DeviceID, err)
			p.markOffline(request.DeviceID, err)
			return device.Device{}, fmt.Errorf("%w: %v", providersdk.ErrWriteRejected, err)
		}
	}
	for name, value := range updates {
		raw[name] = value
	}
	p.writes.Add(1)
	p.recordSuccess(request.DeviceID)
	return p.applyRaw(request.DeviceID, raw, true), nil
}

func (p *Provider) property(deviceID, endpointID, capabilityID, propertyID string) (device.Device, device.Property, bool) {
	p.mu.RLock()
	item, exists := p.devices[deviceID]
	p.mu.RUnlock()
	if !exists {
		return device.Device{}, device.Property{}, false
	}
	property, exists := item.Property(endpointID, capabilityID, propertyID)
	return item, property, exists
}

func (p *Provider) configFor(id string) (DeviceConfig, bool) {
	p.mu.RLock()
	configured, ok := p.configs[id]
	p.mu.RUnlock()
	return configured, ok
}

func (p *Provider) applyRaw(id string, values map[string]any, online bool) device.Device {
	p.mu.Lock()
	configured, ok := p.configs[id]
	if !ok {
		p.mu.Unlock()
		return device.Device{}
	}
	merged := cloneRaw(p.raw[id])
	for key, value := range values {
		merged[key] = value
	}
	current := p.devices[id]
	next, err := buildAirConditionerDevice(p.id, configured, merged, online)
	if err != nil {
		p.mu.Unlock()
		return current.Clone()
	}
	next.Sequence, next.LastUpdateAt = current.Sequence, current.LastUpdateAt
	changed := !sameDevice(current, next)
	if changed {
		next.Sequence++
		next.LastUpdateAt = time.Now().UTC()
	}
	p.raw[id] = merged
	p.devices[id] = next
	if changed {
		p.events.Add(1)
	}
	listeners := p.listenersLocked(changed)
	p.mu.Unlock()
	p.notify(next, listeners)
	return next.Clone()
}

func (p *Provider) markOffline(id string, reason error) {
	p.mu.Lock()
	configured, ok := p.configs[id]
	if !ok {
		p.mu.Unlock()
		return
	}
	if configured.DisableAvailableCheck {
		p.lastErrors[id] = reason.Error()
		p.mu.Unlock()
		return
	}
	current := p.devices[id]
	if !current.IsOnline() {
		p.mu.Unlock()
		return
	}
	next, buildErr := buildAirConditionerDevice(p.id, configured, p.raw[id], false)
	if buildErr != nil {
		p.mu.Unlock()
		return
	}
	next.Sequence, next.LastUpdateAt = current.Sequence+1, time.Now().UTC()
	p.devices[id] = next
	p.lastErrors[id] = reason.Error()
	p.events.Add(1)
	listeners := p.listenersLocked(true)
	p.mu.Unlock()
	p.notify(next, listeners)
}

func (p *Provider) recordFailure(id string, err error) {
	p.mu.Lock()
	p.lastErrors[id] = err.Error()
	if configured, ok := p.configs[id]; ok && configured.DisableAvailableCheck {
		p.mu.Unlock()
		return
	}
	wasOnline := p.devices[id].IsOnline()
	p.mu.Unlock()
	if wasOnline {
		p.markOffline(id, err)
	}
}

func (p *Provider) recordSuccess(id string) {
	p.mu.Lock()
	delete(p.lastErrors, id)
	p.mu.Unlock()
}

func (p *Provider) listenersLocked(copyListeners bool) []func(device.Device) {
	if !copyListeners {
		return nil
	}
	result := make([]func(device.Device), 0, len(p.listeners))
	for _, listener := range p.listeners {
		result = append(result, listener)
	}
	return result
}

func (p *Provider) notify(item device.Device, listeners []func(device.Device)) {
	if len(listeners) == 0 {
		return
	}
	for _, listener := range listeners {
		listener(item.Clone())
	}
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
	return func() {
		once.Do(func() {
			p.mu.Lock()
			delete(p.listeners, id)
			p.mu.Unlock()
		})
	}
}

func (p *Provider) ProviderMetrics() map[string]uint64 {
	p.mu.RLock()
	var online uint64
	for _, item := range p.devices {
		if item.IsOnline() {
			online++
		}
	}
	devices := uint64(len(p.devices))
	p.mu.RUnlock()
	return map[string]uint64{
		"requests": p.requests.Load(), "binds": p.binds.Load(), "polls": p.polls.Load(),
		"writes": p.writes.Load(), "events": p.events.Load(), "errors": p.errors.Load(),
		"devices": devices, "onlineDevices": online,
	}
}

func (p *Provider) ProviderDiagnostics() map[string]string {
	p.mu.RLock()
	result := map[string]string{
		"state":   "stopped",
		"devices": strconv.Itoa(len(p.devices)),
	}
	if p.running {
		result["state"] = "running"
	}
	for _, item := range p.devices {
		if item.IsOnline() {
			result["onlineDevices"] = strconv.Itoa(parseDiagnosticCount(result["onlineDevices"]) + 1)
		}
	}
	for id, err := range p.lastErrors {
		result["lastError."+id] = err
	}
	p.mu.RUnlock()
	return result
}

func parseDiagnosticCount(value string) int {
	if value == "" {
		return 0
	}
	result, _ := strconv.Atoi(value)
	return result
}

func sameDevice(left, right device.Device) bool {
	left.Sequence, right.Sequence = 0, 0
	left.LastUpdateAt, right.LastUpdateAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(left, right)
}

func defaultRawState(configured DeviceConfig) map[string]any {
	step := configured.TargetTemperatureStep
	if step == 0 {
		step = 1
	}
	return map[string]any{
		"Pow": 0, "Mod": 0, "SetTem": 24, "WdSpd": 0, "SwhSlp": 0, "Lig": 1,
		"SwingLfRig": 0, "SwUpDn": 0, "Quiet": 0, "Tur": 0, "StHt": 0,
		"TemUn": 0, "TemRec": 0, "SvSt": 0, "SlpMod": 0, "Air": 0, "Blo": 0,
		"Health": 0, "BuzzerCtrl": 1,
		"AutoXFan": configured.AutoXFan, "AutoLight": configured.AutoLight,
		"TargetTemperatureStep": step,
		"ErrCode":               0,
	}
}

func cloneRaw(raw map[string]any) map[string]any {
	result := make(map[string]any, len(raw))
	for key, value := range raw {
		result[key] = value
	}
	return result
}

func commandFor(propertyID string, value device.PropertyValue, raw map[string]any) ([]string, []any, map[string]any, error) {
	boolValue := func() (int, error) {
		if value.Bool == nil {
			return 0, fmt.Errorf("%w: bool value is missing", providersdk.ErrPropertyInvalid)
		}
		if *value.Bool {
			return 1, nil
		}
		return 0, nil
	}
	enumValue := func() (string, error) {
		if value.String == nil {
			return "", fmt.Errorf("%w: enum value is missing", providersdk.ErrPropertyInvalid)
		}
		return *value.String, nil
	}
	updates := make(map[string]any)
	switch propertyID {
	case "active":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"Pow": v}
		if rawBool(raw, "AutoLight") {
			updates["Lig"] = v
		}
	case "target-mode":
		mode, err := enumValue()
		if err != nil {
			return nil, nil, nil, err
		}
		if mode == "off" {
			updates["Pow"] = 0
		} else {
			greeMode, ok := map[string]int{"auto": 0, "cool": 1, "dry": 2, "fan": 3, "heat": 4}[mode]
			if !ok {
				return nil, nil, nil, fmt.Errorf("%w: unsupported Gree target mode %q", providersdk.ErrPropertyInvalid, mode)
			}
			updates["Pow"], updates["Mod"] = 1, greeMode
		}
		if rawBool(raw, "AutoLight") {
			updates["Lig"] = updates["Pow"]
		}
		if rawBool(raw, "AutoXFan") {
			updates["Blo"] = 0
			if mode == "cool" || mode == "dry" {
				updates["Blo"] = 1
			}
		}
	case "target-temperature":
		if value.Number == nil {
			return nil, nil, nil, fmt.Errorf("%w: temperature value is missing", providersdk.ErrPropertyInvalid)
		}
		setTem, temRec := encodeTargetTemperature(*value.Number)
		updates = map[string]any{"SetTem": setTem, "TemRec": temRec}
	case "fan-speed":
		mode, err := enumValue()
		if err != nil {
			return nil, nil, nil, err
		}
		switch mode {
		case "auto":
			updates = map[string]any{"WdSpd": 0, "Tur": 0, "Quiet": 0}
		case "low":
			updates = map[string]any{"WdSpd": 1, "Tur": 0, "Quiet": 0}
		case "medium_low":
			updates = map[string]any{"WdSpd": 2, "Tur": 0, "Quiet": 0}
		case "medium":
			updates = map[string]any{"WdSpd": 3, "Tur": 0, "Quiet": 0}
		case "medium_high":
			updates = map[string]any{"WdSpd": 4, "Tur": 0, "Quiet": 0}
		case "high":
			updates = map[string]any{"WdSpd": 5, "Tur": 0, "Quiet": 0}
		case "turbo":
			updates = map[string]any{"Tur": 1, "Quiet": 0}
		case "quiet":
			updates = map[string]any{"Tur": 0, "Quiet": 1}
		default:
			return nil, nil, nil, fmt.Errorf("%w: unsupported Gree fan speed %q", providersdk.ErrPropertyInvalid, mode)
		}
	case "rotation-speed":
		if value.Number == nil {
			return nil, nil, nil, fmt.Errorf("%w: rotation value is missing", providersdk.ErrPropertyInvalid)
		}
		percent := *value.Number
		if percent >= 95 {
			updates = map[string]any{"Tur": 1, "Quiet": 0}
		} else {
			level := int(math.Round(percent / 20))
			if level < 0 {
				level = 0
			}
			if level > 5 {
				level = 5
			}
			updates = map[string]any{"WdSpd": level, "Tur": 0, "Quiet": 0}
		}
	case "vertical-swing":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"SwUpDn": v}
	case "horizontal-swing":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"SwingLfRig": v}
	case "vertical-swing-mode":
		mode, err := enumValue()
		if err != nil {
			return nil, nil, nil, err
		}
		index := enumIndex(greeVerticalSwingModes, mode)
		if index < 0 {
			return nil, nil, nil, fmt.Errorf("%w: unsupported Gree vertical swing mode %q", providersdk.ErrPropertyInvalid, mode)
		}
		updates = map[string]any{"SwUpDn": index}
	case "horizontal-swing-mode":
		mode, err := enumValue()
		if err != nil {
			return nil, nil, nil, err
		}
		index := enumIndex(greeHorizontalSwingModes, mode)
		if index < 0 {
			return nil, nil, nil, fmt.Errorf("%w: unsupported Gree horizontal swing mode %q", providersdk.ErrPropertyInvalid, mode)
		}
		updates = map[string]any{"SwingLfRig": index}
	case "auxiliary-heat", "eight-degree-heat":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"StHt": v}
	case "sleep-mode", "sleep":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"SwhSlp": v, "SlpMod": v}
	case "eco-mode", "power-save":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"SvSt": v}
	case "x-fan":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"Blo": v}
	case "health":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"Health": v}
	case "air":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"Air": v}
	case "anti-direct-blow":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"AntiDirectBlow": v}
	case "light-sensor":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"LigSen": 1 - v}
		if v == 1 {
			updates["Lig"] = 1
		}
	case "display-enabled":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"Lig": v}
	case "beeper":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"Buzzer_ON_OFF": 1 - v, "BuzzerCtrl": v}
	case "auto-x-fan":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"AutoXFan": v}
	case "auto-light":
		v, err := boolValue()
		if err != nil {
			return nil, nil, nil, err
		}
		updates = map[string]any{"AutoLight": v}
	case "target-temperature-step":
		if value.Number == nil || *value.Number < 0.1 || *value.Number > 5 {
			return nil, nil, nil, fmt.Errorf("%w: target temperature step must be between 0.1 and 5", providersdk.ErrPropertyInvalid)
		}
		updates = map[string]any{"TargetTemperatureStep": *value.Number}
	case "display-units":
		units, err := enumValue()
		if err != nil {
			return nil, nil, nil, err
		}
		v := 0
		if units == "fahrenheit" {
			v = 1
		} else if units != "celsius" {
			return nil, nil, nil, fmt.Errorf("%w: unsupported Gree display unit %q", providersdk.ErrPropertyInvalid, units)
		}
		updates = map[string]any{"TemUn": v}
	default:
		return nil, nil, nil, providersdk.ErrPropertyUnsupported
	}
	options := make([]string, 0, len(updates))
	values := make([]any, 0, len(updates))
	for _, option := range []string{"Pow", "Mod", "SetTem", "TemRec", "WdSpd", "Tur", "Quiet", "SwUpDn", "SwingLfRig", "Air", "Blo", "Health", "StHt", "SwhSlp", "SlpMod", "SvSt", "Lig", "TemUn", "AntiDirectBlow", "LigSen", "Buzzer_ON_OFF", "BuzzerCtrl"} {
		if value, ok := updates[option]; ok {
			options = append(options, option)
			values = append(values, value)
		}
	}
	return options, values, updates, nil
}

func enumIndex(values []string, wanted string) int {
	for index, value := range values {
		if value == wanted {
			return index
		}
	}
	return -1
}

func localGreeProperty(propertyID string) bool {
	switch propertyID {
	case "auto-x-fan", "auto-light", "target-temperature-step":
		return true
	default:
		return false
	}
}

func encodeTargetTemperature(celsius float64) (int, int) {
	rounded := math.Round(celsius*2) / 2
	whole := int(math.Floor(rounded))
	remainder := int(math.Round((rounded - float64(whole)) * 2))
	if remainder > 1 {
		whole++
		remainder = 0
	}
	return whole, remainder
}

func rawNumber(raw map[string]any, key string, fallback float64) float64 {
	if value, ok := numberFromAny(raw[key]); ok {
		return value
	}
	return fallback
}
