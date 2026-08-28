package network

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type sequenceProber struct {
	mu        sync.Mutex
	responses []error
	requests  []ProbeRequest
}

func (p *sequenceProber) Probe(_ context.Context, request ProbeRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, request)
	if len(p.responses) == 0 {
		return nil
	}
	result := p.responses[0]
	p.responses = p.responses[1:]
	return result
}

func (p *sequenceProber) recordedRequests() []ProbeRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ProbeRequest(nil), p.requests...)
}

type recordingWaker struct {
	mu       sync.Mutex
	requests []WakeRequest
	err      error
}

func (w *recordingWaker) Wake(_ context.Context, request WakeRequest) error {
	w.mu.Lock()
	w.requests = append(w.requests, request)
	err := w.err
	w.mu.Unlock()
	return err
}

func networkProviderConfig() providerconfig.Config {
	return providerconfig.Config{
		ID: "network-main", Type: ProviderType, Name: "LAN", Enabled: true,
		Config: []byte(`{"onlineThreshold":2,"offlineThreshold":2,"devices":[{"id":"nas","name":"NAS","host":"192.0.2.10","probePort":443,"mac":"aa:bb:cc:dd:ee:ff"}]}`),
	}
}

func TestProviderAppliesPowerThresholdsWithoutMarkingTheProviderOffline(t *testing.T) {
	prober := &sequenceProber{responses: []error{nil, nil, errors.New("connection refused"), errors.New("connection refused"), nil, nil}}
	provider, err := NewProviderWithDependencies(networkProviderConfig(), prober, &recordingWaker{})
	if err != nil {
		t.Fatal(err)
	}
	var events []device.Device
	unsubscribe := provider.Subscribe(func(item device.Device) { events = append(events, item) })
	defer unsubscribe()

	for range 2 {
		provider.probeDue(context.Background(), true)
	}
	items, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(items) != 1 || !items[0].IsOnline() {
		t.Fatalf("after successful threshold: items=%#v err=%v", items, err)
	}
	power, ok := items[0].Property("main", "switch", "power")
	if items[0].Type != device.TypeNetworkDevice || !ok || power.Value.Bool == nil || !*power.Value.Bool {
		t.Fatalf("powered-on network state = %#v, type=%q, found=%v", power, items[0].Type, ok)
	}

	for range 2 {
		provider.probeDue(context.Background(), true)
	}
	items, _ = provider.DiscoverDevices(context.Background())
	if items[0].EffectiveAvailability() != device.AvailabilityOnline || !items[0].Online {
		t.Fatalf("a powered-off host must remain manageable: %#v", items[0])
	}
	power, _ = items[0].Property("main", "switch", "power")
	if power.Value.Bool == nil || *power.Value.Bool {
		t.Fatalf("powered-off network state = %#v", power)
	}

	for range 2 {
		provider.probeDue(context.Background(), true)
	}
	if len(events) != 3 { // power on, power off, then power on; initial off state needs no event.
		t.Fatalf("published events = %d, want 3", len(events))
	}
	for _, event := range events {
		if event.EffectiveAvailability() != device.AvailabilityOnline {
			t.Fatalf("a power-state transition must not mark the Provider offline: %#v", event)
		}
	}
	if stats := provider.ProviderMetrics(); stats["probes"] != 6 || stats["errors"] != 2 {
		t.Fatalf("provider stats = %#v", stats)
	}
}

func TestPowerOnSendsMagicPacketWithoutClaimingTheDeviceStarted(t *testing.T) {
	waker := &recordingWaker{}
	provider, err := NewProviderWithDependencies(networkProviderConfig(), &sequenceProber{}, waker)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := provider.DiscoverDevices(context.Background())
	updated, err := provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: "nas", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)})
	if err != nil {
		t.Fatal(err)
	}
	power, found := updated.Property("main", "switch", "power")
	if updated.EffectiveAvailability() != device.AvailabilityOnline || before[0].Availability != updated.Availability || !found || power.Value.Bool == nil || *power.Value.Bool {
		t.Fatalf("wake must not claim that the device started: before=%#v updated=%#v", before[0], updated)
	}
	pending, found := updated.Property("main", "network", "wake-pending")
	if !found || pending.Value.Bool == nil || !*pending.Value.Bool {
		t.Fatalf("wake must expose a pending startup state: %#v", updated)
	}
	if !provider.AcknowledgesPropertyWrite(providersdk.PropertyWriteRequest{EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)}) {
		t.Fatal("WOL write must acknowledge delivery independently from startup observation")
	}
	waker.mu.Lock()
	requests := append([]WakeRequest(nil), waker.requests...)
	waker.mu.Unlock()
	if len(requests) != 1 || requests[0].MAC.String() != "aa:bb:cc:dd:ee:ff" || requests[0].BroadcastAddress != defaultWOLAddress || requests[0].Port != defaultWOLPort {
		t.Fatalf("wake request = %#v", requests)
	}
	if stats := provider.ProviderMetrics(); stats["wakes"] != 1 {
		t.Fatalf("provider stats = %#v", stats)
	}
}

func TestWakePendingClearsOnlyWhenAProbeConfirmsStartupOrGraceEnds(t *testing.T) {
	config := networkProviderConfig()
	config.Config = []byte(`{"onlineThreshold":2,"wakeGraceSeconds":5,"devices":[{"id":"nas","name":"NAS","host":"192.0.2.10","probePort":443,"mac":"aa:bb:cc:dd:ee:ff"}]}`)
	provider, err := NewProviderWithDependencies(config, &sequenceProber{}, &recordingWaker{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: "nas", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)}); err != nil {
		t.Fatal(err)
	}
	provider.recordProbe("nas", nil)
	items, _ := provider.DiscoverDevices(context.Background())
	pending, _ := items[0].Property("main", "network", "wake-pending")
	power, _ := items[0].Property("main", "switch", "power")
	if pending.Value.Bool == nil || !*pending.Value.Bool || power.Value.Bool == nil || *power.Value.Bool {
		t.Fatalf("one successful probe must keep startup pending until the configured threshold: %#v", items[0])
	}
	provider.recordProbe("nas", nil)
	items, _ = provider.DiscoverDevices(context.Background())
	pending, _ = items[0].Property("main", "network", "wake-pending")
	power, _ = items[0].Property("main", "switch", "power")
	if pending.Value.Bool == nil || *pending.Value.Bool || power.Value.Bool == nil || !*power.Value.Bool {
		t.Fatalf("threshold-confirmed startup state = %#v", items[0])
	}
}

func TestPowerOnRejectsMonitorOnlyDeviceAndPowerOff(t *testing.T) {
	config := networkProviderConfig()
	config.Config = []byte(`{"devices":[{"id":"printer","name":"Printer","host":"192.0.2.11","probePort":631}]}`)
	provider, err := NewProviderWithDependencies(config, &sequenceProber{}, &recordingWaker{})
	if err != nil {
		t.Fatal(err)
	}
	items, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(items) != 1 || items[0].Endpoints[0].Capabilities[0].Properties[0].Definition.Writable {
		t.Fatalf("monitor-only device must not advertise power-on: items=%#v err=%v", items, err)
	}
	_, err = provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: "printer", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)})
	if !errors.Is(err, providersdk.ErrPropertyUnsupported) {
		t.Fatalf("power-on error = %v, want unsupported property write", err)
	}
	_, err = provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: "printer", EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(false)})
	if !errors.Is(err, providersdk.ErrPropertyUnsupported) {
		t.Fatalf("power-off error = %v, want unsupported", err)
	}
}

func TestConnectionSucceedsWhenAnyConfiguredDeviceIsReachable(t *testing.T) {
	prober := &sequenceProber{responses: []error{errors.New("offline"), nil}}
	config := networkProviderConfig()
	config.Config = []byte(`{"devices":[{"id":"nas","name":"NAS","host":"192.0.2.10","probePort":443},{"id":"pc","name":"PC","host":"192.0.2.11","probePort":3389}]}`)
	provider, err := NewProviderWithDependencies(config, prober, &recordingWaker{})
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.TestConnection(context.Background()); err != nil {
		t.Fatalf("connection test = %v", err)
	}
}

func TestProviderAllowsAnEmptyCatalogUntilDevicesAreManaged(t *testing.T) {
	config := networkProviderConfig()
	config.Config = []byte(`{"devices":[]}`)
	provider, err := NewProviderWithDependencies(config, &sequenceProber{}, &recordingWaker{})
	if err != nil {
		t.Fatalf("create empty network catalog: %v", err)
	}
	items, err := provider.DiscoverDevices(context.Background())
	if err != nil {
		t.Fatalf("discover empty catalog: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("empty catalog devices = %#v, want none", items)
	}
	if err := provider.TestConnection(context.Background()); err == nil || err.Error() != "network connection test requires at least one device" {
		t.Fatalf("empty catalog connection test error = %v", err)
	}
}

func TestProviderSupportsICMPAndPerDeviceProbeOverrides(t *testing.T) {
	config := networkProviderConfig()
	config.Config = []byte(`{"probeMethod":"icmp","devices":[{"id":"nas","name":"NAS","host":"192.0.2.10"},{"id":"pc","name":"PC","host":"192.0.2.11","probeMethod":"tcp","probePort":3389}]}`)
	prober := &sequenceProber{responses: []error{errors.New("no echo response"), nil}}
	provider, err := NewProviderWithDependencies(config, prober, &recordingWaker{})
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.TestConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
	requests := prober.recordedRequests()
	if len(requests) != 2 || requests[0] != (ProbeRequest{Method: ProbeMethodICMP, Host: "192.0.2.10"}) || requests[1] != (ProbeRequest{Method: ProbeMethodTCP, Host: "192.0.2.11", Port: 3389}) {
		t.Fatalf("probe requests = %#v", requests)
	}
}

func TestConfigValidationAndMagicPacket(t *testing.T) {
	config := networkProviderConfig()
	config.Config = []byte(`{"devices":[{"id":"NAS","name":"NAS","host":"192.0.2.10","probePort":443,"mac":"not-a-mac"}]}`)
	if _, err := NewProviderFromConfig(config); err == nil {
		t.Fatal("invalid network config was accepted")
	}
	config.Config = []byte(`{"probeMethod":"udp","devices":[{"id":"nas","name":"NAS","host":"192.0.2.10","probePort":443}]}`)
	if _, err := NewProviderFromConfig(config); err == nil {
		t.Fatal("unsupported probe method was accepted")
	}
	config.Config = []byte(`{"wakeGraceSeconds":4,"devices":[{"id":"nas","name":"NAS","host":"192.0.2.10","probePort":443}]}`)
	if _, err := NewProviderFromConfig(config); err == nil {
		t.Fatal("too-short wake grace was accepted")
	}
	provider, err := NewProviderWithDependencies(networkProviderConfig(), &sequenceProber{}, &recordingWaker{})
	if err != nil {
		t.Fatal(err)
	}
	items, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("published network device = %#v, err=%v", items, err)
	}
	if validateErr := items[0].Validate(); validateErr != nil {
		t.Fatalf("published network device must satisfy the model contract: %#v", validateErr)
	}
	packet := magicPacket(net.HardwareAddr{0, 1, 2, 3, 4, 5})
	if len(packet) != 102 {
		t.Fatalf("magic packet length = %d", len(packet))
	}
	for index := 0; index < 6; index++ {
		if packet[index] != 0xff {
			t.Fatalf("magic packet preamble[%d] = %x", index, packet[index])
		}
	}
	for index := 6; index < len(packet); index += 6 {
		if got := net.HardwareAddr(packet[index : index+6]); got.String() != "00:01:02:03:04:05" {
			t.Fatalf("magic packet mac at %d = %s", index, got)
		}
	}
}
