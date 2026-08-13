package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

// Prober is deliberately narrow so protocol behavior is testable without a
// real LAN. A nil error is a TCP connection that completed successfully.
type Prober interface {
	Probe(context.Context, string, int) error
}

type tcpProber struct{}

func (tcpProber) Probe(ctx context.Context, host string, port int) error {
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return connection.Close()
}

// Waker sends a Magic Packet. It is injected in tests and can be replaced by
// embedders that have a stricter network boundary.
type Waker interface {
	Wake(context.Context, WakeRequest) error
}

type WakeRequest struct {
	MAC              net.HardwareAddr
	BroadcastAddress string
	Port             int
	Interface        string
}

type deviceRuntime struct {
	configured monitoredDevice
	snapshot   device.Device
	successes  int
	failures   int
	nextProbe  time.Time
	lastError  string
}

type Provider struct {
	id, name string
	prober   Prober
	waker    Waker

	mu        sync.RWMutex
	devices   map[string]*deviceRuntime
	listeners map[uint64]func(device.Device)
	next      uint64
	running   bool
	cancel    context.CancelFunc
	done      chan struct{}

	probes atomic.Uint64
	wakes  atomic.Uint64
	events atomic.Uint64
	errors atomic.Uint64
}

var (
	_ providersdk.Provider            = (*Provider)(nil)
	_ providersdk.ConnectionTester    = (*Provider)(nil)
	_ providersdk.Discoverer          = (*Provider)(nil)
	_ providersdk.EventSubscriber     = (*Provider)(nil)
	_ providersdk.PropertyWriter      = (*Provider)(nil)
	_ providersdk.MetricsReporter     = (*Provider)(nil)
	_ providersdk.DiagnosticsReporter = (*Provider)(nil)
)

func NewProviderFromConfig(item providerconfig.Config) (*Provider, error) {
	return newProvider(item, tcpProber{}, udpWaker{})
}

// NewProviderWithDependencies is intended for deterministic tests and hosts
// that need to own the network transport themselves.
func NewProviderWithDependencies(item providerconfig.Config, prober Prober, waker Waker) (*Provider, error) {
	if prober == nil || waker == nil {
		return nil, errors.New("network provider prober and waker are required")
	}
	return newProvider(item, prober, waker)
}

func newProvider(item providerconfig.Config, prober Prober, waker Waker) (*Provider, error) {
	config, err := decodeConfig(item)
	if err != nil {
		return nil, err
	}
	configured, err := materializeDevices(config)
	if err != nil {
		return nil, err
	}
	provider := &Provider{
		id: item.ID, name: item.Name, prober: prober, waker: waker,
		devices: make(map[string]*deviceRuntime, len(configured)), listeners: make(map[uint64]func(device.Device)),
	}
	for _, entry := range configured {
		provider.devices[entry.ID] = &deviceRuntime{configured: entry, snapshot: buildDevice(item.ID, entry), nextProbe: time.Now().UTC()}
	}
	return provider, nil
}

func (p *Provider) Manifest() providersdk.Manifest {
	p.mu.RLock()
	name := p.name
	p.mu.RUnlock()
	return providersdk.Manifest{ID: p.id, Type: ProviderType, Name: name, Version: "0.1.0"}
}

func (*Provider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{Discovery: true, PropertyWrite: true, Events: true}
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

	p.probeDue(ctx, true)
	go p.pollLoop(lifecycle, done)
	return nil
}

func (p *Provider) Close(context.Context) error {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return nil
	}
	cancel, done := p.cancel, p.done
	p.running, p.cancel, p.done = false, nil, nil
	p.mu.Unlock()
	cancel()
	<-done
	return nil
}

// TestConnection is successful when at least one configured endpoint appears
// powered on. A Provider catalog can legitimately include sleeping devices, so
// requiring every entry to answer would make configuration testing unusable
// for Wake-on-LAN.
func (p *Provider) TestConnection(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	entries := p.entries()
	if len(entries) == 0 {
		return errors.New("network connection test requires at least one device")
	}
	var lastErr error
	for _, entry := range entries {
		probeCtx, cancel := context.WithTimeout(ctx, entry.probeTimeout)
		err := p.prober.Probe(probeCtx, entry.Host, entry.ProbePort)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return fmt.Errorf("no configured network device appears to be powered on: %w", lastErr)
}

func (p *Provider) DiscoverDevices(context.Context) ([]device.Device, error) {
	p.mu.RLock()
	result := make([]device.Device, 0, len(p.devices))
	for _, runtime := range p.devices {
		result = append(result, runtime.snapshot.Clone())
	}
	p.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
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

// WriteProperty binds Wake-on-LAN to the normal power-on operation. A probe is
// still the authority for the reported power state, so a successful packet is
// not treated as proof that the device has already started.
func (p *Provider) WriteProperty(ctx context.Context, request providersdk.PropertyWriteRequest) (device.Device, error) {
	if request.EndpointID != "main" || request.CapabilityID != "switch" || request.PropertyID != "power" {
		return device.Device{}, providersdk.ErrPropertyUnsupported
	}
	if request.Value.Type != device.ValueTypeBool || request.Value.Bool == nil {
		return device.Device{}, providersdk.ErrPropertyInvalid
	}
	if !*request.Value.Bool {
		return device.Device{}, fmt.Errorf("%w: network monitoring can only turn a device on", providersdk.ErrPropertyUnsupported)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.RLock()
	runtime := p.devices[request.DeviceID]
	if runtime == nil {
		p.mu.RUnlock()
		return device.Device{}, providersdk.ErrDeviceNotFound
	}
	configured, snapshot := runtime.configured, runtime.snapshot.Clone()
	p.mu.RUnlock()
	if len(configured.mac) != 6 {
		return device.Device{}, fmt.Errorf("%w: device %q has no MAC address configured", providersdk.ErrPropertyUnsupported, request.DeviceID)
	}
	if err := p.waker.Wake(ctx, WakeRequest{MAC: append(net.HardwareAddr(nil), configured.mac...), BroadcastAddress: configured.wolAddress, Port: configured.wolPort, Interface: configured.wolInterface}); err != nil {
		p.errors.Add(1)
		return device.Device{}, fmt.Errorf("%w: %v", providersdk.ErrProviderUnavailable, err)
	}
	p.wakes.Add(1)
	// Magic Packets have no acknowledgement. Return the existing state and let
	// the next TCP probe become the sole authority for the power state.
	return snapshot, nil
}

func (p *Provider) ProviderMetrics() map[string]uint64 {
	p.mu.RLock()
	devices, online := uint64(len(p.devices)), uint64(0)
	for _, runtime := range p.devices {
		if runtime.snapshot.IsOnline() {
			online++
		}
	}
	p.mu.RUnlock()
	return map[string]uint64{
		// Keep the common Provider dashboard metrics populated while preserving
		// the more specific counters for API consumers.
		"requests": p.probes.Load(), "events": p.events.Load(),
		"probes": p.probes.Load(), "wakes": p.wakes.Load(), "errors": p.errors.Load(),
		"devices": devices, "onlineDevices": online,
	}
}

func (p *Provider) ProviderDiagnostics() map[string]string {
	p.mu.RLock()
	result := map[string]string{"state": "stopped", "devices": strconv.Itoa(len(p.devices))}
	if p.running {
		result["state"] = "running"
	}
	for id, runtime := range p.devices {
		if runtime.lastError != "" {
			result["lastError."+id] = runtime.lastError
		}
	}
	p.mu.RUnlock()
	return result
}

func (p *Provider) pollLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		wait := p.nextProbeDelay(time.Now().UTC())
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
			p.probeDue(ctx, false)
		}
	}
}

func (p *Provider) nextProbeDelay(now time.Time) time.Duration {
	p.mu.RLock()
	var next time.Time
	for _, runtime := range p.devices {
		if next.IsZero() || runtime.nextProbe.Before(next) {
			next = runtime.nextProbe
		}
	}
	p.mu.RUnlock()
	if next.IsZero() || !next.After(now) {
		return 0
	}
	return next.Sub(now)
}

func (p *Provider) probeDue(parent context.Context, all bool) {
	now := time.Now().UTC()
	p.mu.Lock()
	entries := make([]monitoredDevice, 0, len(p.devices))
	for _, runtime := range p.devices {
		if all || !runtime.nextProbe.After(now) {
			runtime.nextProbe = now.Add(runtime.configured.probeInterval)
			entries = append(entries, runtime.configured)
		}
	}
	p.mu.Unlock()
	var group sync.WaitGroup
	for _, entry := range entries {
		entry := entry
		group.Add(1)
		go func() {
			defer group.Done()
			probeCtx, cancel := context.WithTimeout(parent, entry.probeTimeout)
			err := p.prober.Probe(probeCtx, entry.Host, entry.ProbePort)
			cancel()
			p.probes.Add(1)
			p.recordProbe(entry.ID, err)
		}()
	}
	group.Wait()
}

func (p *Provider) recordProbe(id string, probeErr error) {
	p.mu.Lock()
	runtime := p.devices[id]
	if runtime == nil {
		p.mu.Unlock()
		return
	}
	previous := runtime.snapshot
	if probeErr == nil {
		runtime.successes++
		runtime.failures = 0
		runtime.lastError = ""
		if runtime.successes >= runtime.configured.onlineThreshold {
			runtime.snapshot = updatePowerState(runtime.snapshot, true)
		}
	} else {
		p.errors.Add(1)
		runtime.failures++
		runtime.successes = 0
		runtime.lastError = probeErr.Error()
		if runtime.failures >= runtime.configured.offlineThreshold {
			runtime.snapshot = updatePowerState(runtime.snapshot, false)
		}
	}
	current := runtime.snapshot.Clone()
	changed := previous.Sequence != current.Sequence
	listeners := make([]func(device.Device), 0, len(p.listeners))
	if changed {
		p.events.Add(1)
		for _, listener := range p.listeners {
			listeners = append(listeners, listener)
		}
	}
	p.mu.Unlock()
	for _, listener := range listeners {
		listener(current.Clone())
	}
}

func (p *Provider) entries() []monitoredDevice {
	p.mu.RLock()
	entries := make([]monitoredDevice, 0, len(p.devices))
	for _, runtime := range p.devices {
		entries = append(entries, runtime.configured)
	}
	p.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries
}

func buildDevice(providerID string, configured monitoredDevice) device.Device {
	power := device.Capability{
		ID: "switch", Type: "switch",
		Properties: []device.Property{{
			Definition: device.PropertyDefinition{ID: "power", Name: "电源状态", Type: device.ValueTypeBool, Readable: true, Writable: len(configured.mac) == 6, Notifiable: true},
			Value:      device.BoolValue(false),
		}},
	}
	result := device.Device{
		SchemaVersion: device.SchemaVersion, ID: configured.ID, ProviderID: providerID, Name: configured.Name,
		Type: device.TypeNetworkDevice, RuntimeMode: device.RuntimeModeLocal, StateTransport: device.StateTransportPending,
		// The Provider remains able to send WOL while the managed host is off.
		Availability: device.AvailabilityOnline, Online: true, Sequence: 1, LastUpdateAt: time.Now().UTC(),
		Endpoints: []device.Endpoint{{ID: "main", Name: "网络设备", Type: "network", Capabilities: []device.Capability{
			power,
		}}},
	}
	return result
}

func updatePowerState(item device.Device, power bool) device.Device {
	property, found := item.Property("main", "switch", "power")
	if found && property.Value.Bool != nil && *property.Value.Bool == power {
		return item
	}
	item.SetProperty("main", "switch", "power", device.BoolValue(power))
	item.Sequence++
	item.LastUpdateAt = time.Now().UTC()
	return item
}
