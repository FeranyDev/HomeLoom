package mqtt

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	domaincommand "github.com/feranydev/homeloom/backend/internal/domain/command"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
)

type publishedMessage struct {
	topic    string
	qos      byte
	retained bool
	payload  []byte
}

type fakeTransport struct {
	handlers  transportHandlers
	mu        sync.Mutex
	published []publishedMessage
	closed    bool
}

func (f *fakeTransport) Start(context.Context, context.Context, time.Duration) error {
	f.handlers.onConnectionUp()
	return nil
}
func (f *fakeTransport) Publish(_ context.Context, topic string, qos byte, retained bool, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errors.New("closed")
	}
	f.published = append(f.published, publishedMessage{topic: topic, qos: qos, retained: retained, payload: append([]byte(nil), payload...)})
	return nil
}
func (f *fakeTransport) Close(context.Context) error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}
func (f *fakeTransport) emit(topic string, payload any, retained bool) {
	var encoded []byte
	if payload != nil {
		encoded, _ = json.Marshal(payload)
	}
	f.handlers.onMessage(inboundMessage{topic: topic, payload: encoded, retained: retained})
}
func (f *fakeTransport) disconnect() { f.handlers.onConnectionDown() }
func (f *fakeTransport) publications() []publishedMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]publishedMessage(nil), f.published...)
}

func newTestProvider(t *testing.T) (*Provider, *fakeTransport) {
	t.Helper()
	provider, transport := newUninitializedTestProvider(t)
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = provider.Close(ctx)
	})
	return provider, transport
}

func newUninitializedTestProvider(t *testing.T) (*Provider, *fakeTransport) {
	t.Helper()
	transport := &fakeTransport{}
	provider, err := newProviderFromConfig(providerconfig.Config{ID: "mqtt-main", Name: "MQTT", Config: json.RawMessage(`{"brokerUrl":"mqtt://broker:1883","topicPrefix":"house","qos":1,"retainedStateMaxAgeSeconds":60}`)}, func(_ Config, _ *url.URL, _ *tls.Config, handlers transportHandlers) mqttTransport {
		transport.handlers = handlers
		return transport
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider, transport
}

func TestProviderDiscoveryStateAvailabilityAndRemoval(t *testing.T) {
	provider, transport := newTestProvider(t)
	events := make(chan device.Device, 16)
	unsubscribe := provider.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()

	discovered := mqttSwitch("kitchen-switch", true, false)
	transport.emit(discoveryTopic("house", discovered.ID), discovered, true)
	item := waitDevice(t, events)
	if item.EffectiveAvailability() != device.AvailabilityUnknown {
		t.Fatalf("retained discovery availability = %q", item.EffectiveAvailability())
	}
	now := time.Now().UTC()
	transport.emit(availabilityTopic("house", item.ID), availabilityMessage{SchemaVersion: 1, Availability: device.AvailabilityOnline, Sequence: 1, ObservedAt: now}, true)
	item = waitDevice(t, events)
	if !item.IsOnline() {
		t.Fatalf("availability item = %#v", item)
	}
	transport.emit(stateTopic("house", item.ID, "main", "switch", "power"), stateMessage{SchemaVersion: 1, Value: valuePointer(device.BoolValue(true)), Sequence: 1, ObservedAt: now.Add(time.Second)}, false)
	item = waitDevice(t, events)
	assertDevicePower(t, item, true)
	property, err := provider.ReadProperty(context.Background(), providersdk.PropertyReadRequest{DeviceID: item.ID, EndpointID: "main", CapabilityID: "switch", PropertyID: "power"})
	if err != nil || property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatalf("ReadProperty() = %#v, %v", property, err)
	}

	// Duplicate remote sequence and stale retained values never replace the last report.
	transport.emit(stateTopic("house", item.ID, "main", "switch", "power"), stateMessage{SchemaVersion: 1, Value: valuePointer(device.BoolValue(false)), Sequence: 1, ObservedAt: now.Add(2 * time.Second)}, false)
	assertNoDevice(t, events)
	transport.emit(stateTopic("house", item.ID, "main", "switch", "power"), stateMessage{SchemaVersion: 1, Value: valuePointer(device.BoolValue(false)), Sequence: 2, ObservedAt: now.Add(-2 * time.Minute)}, true)
	assertNoDevice(t, events)

	transport.disconnect()
	item = waitDevice(t, events)
	if item.EffectiveAvailability() != device.AvailabilityUnknown {
		t.Fatalf("disconnect availability = %q", item.EffectiveAvailability())
	}
	if _, err := provider.ReadProperty(context.Background(), providersdk.PropertyReadRequest{DeviceID: item.ID, EndpointID: "main", CapabilityID: "switch", PropertyID: "power"}); !errors.Is(err, providersdk.ErrProviderUnavailable) {
		t.Fatalf("ReadProperty() disconnect error = %v", err)
	}

	transport.emit(discoveryTopic("house", item.ID), nil, true)
	removed := waitDevice(t, events)
	if !removed.Removed || removed.IsOnline() {
		t.Fatalf("removed = %#v", removed)
	}
	items, _ := provider.DiscoverDevices(context.Background())
	if len(items) != 0 {
		t.Fatalf("discovered after removal = %#v", items)
	}
}

func TestProviderPublishesPropertyAndActionCommands(t *testing.T) {
	provider, transport := newTestProvider(t)
	events := make(chan device.Device, 8)
	provider.Subscribe(func(item device.Device) { events <- item })
	discovered := mqttSwitch("desk-lamp", true, false)
	transport.emit(discoveryTopic("house", discovered.ID), discovered, false)
	waitDevice(t, events)

	candidate, err := provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: discovered.ID, EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(true)})
	if err != nil {
		t.Fatal(err)
	}
	assertDevicePower(t, candidate, true)
	publications := transport.publications()
	if len(publications) != 1 || publications[0].topic != "house/command/desk-lamp/main/switch/power" || publications[0].qos != 1 || publications[0].retained {
		t.Fatalf("property publications = %#v", publications)
	}
	var propertyCommand commandMessage
	if err := json.Unmarshal(publications[0].payload, &propertyCommand); err != nil || propertyCommand.Kind != "property" || propertyCommand.Value == nil || propertyCommand.Value.Bool == nil || !*propertyCommand.Value.Bool || propertyCommand.CorrelationID == "" {
		t.Fatalf("property command = %#v, %v", propertyCommand, err)
	}

	_, err = provider.ExecuteCommand(context.Background(), providersdk.CommandRequest{DeviceID: discovered.ID, EndpointID: "main", CapabilityID: "switch", CommandID: "set-power", IdempotencyKey: "request-one", Parameters: map[string]device.PropertyValue{"value": device.BoolValue(false)}})
	if err != nil {
		t.Fatal(err)
	}
	publications = transport.publications()
	if len(publications) != 2 || publications[1].topic != "house/command/desk-lamp/main/switch/set-power" {
		t.Fatalf("action publications = %#v", publications)
	}
	var action commandMessage
	if err := json.Unmarshal(publications[1].payload, &action); err != nil || action.Kind != "action" || action.IdempotencyKey != "request-one" || action.Parameters["value"].Bool == nil {
		t.Fatalf("action command = %#v, %v", action, err)
	}
	if _, err := provider.ExecuteCommand(context.Background(), providersdk.CommandRequest{DeviceID: discovered.ID, EndpointID: "main", CapabilityID: "switch", CommandID: "set-power"}); !errors.Is(err, providersdk.ErrCommandInvalid) {
		t.Fatalf("invalid action error = %v", err)
	}
	if stats := provider.Stats(); stats.CommandsPublished != 2 || stats.MessagesInvalid != 0 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestProviderRejectsInvalidDiscoveryWithoutPoisoningState(t *testing.T) {
	provider, transport := newTestProvider(t)
	transport.emit("house/discovery/Bad ID", map[string]any{"schemaVersion": 1}, false)
	deadline := time.Now().Add(time.Second)
	for provider.Stats().MessagesInvalid == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if stats := provider.Stats(); stats.MessagesInvalid != 1 {
		t.Fatalf("stats = %#v", stats)
	}
	items, _ := provider.DiscoverDevices(context.Background())
	if len(items) != 0 {
		t.Fatalf("items = %#v", items)
	}
}

func TestMQTTStateReportConfirmsDeviceServicePropertyCommand(t *testing.T) {
	provider, transport := newUninitializedTestProvider(t)
	manager, err := providermanager.New(provider)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, cancelLifecycle := context.WithCancel(context.Background())
	defer cancelLifecycle()
	if err := manager.Initialize(lifecycle); err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(manager)
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close device service: %v", err)
		}
	})

	discovered := mqttSwitch("hall-switch", true, false)
	transport.emit(discoveryTopic("house", discovered.ID), discovered, false)
	waitForCondition(t, func() bool {
		items, _ := service.List(context.Background())
		return len(items) == 1 && items[0].ID == discovered.ID && items[0].IsOnline()
	}, "MQTT device to reach the application registry")

	_, command, err := service.ExecuteProperty(context.Background(), discovered.ID, "main", "switch", "power", device.BoolValue(true))
	if err != nil {
		t.Fatal(err)
	}
	if command.Status != domaincommand.StatusAccepted {
		t.Fatalf("command status after publish = %q", command.Status)
	}
	publications := transport.publications()
	if len(publications) != 1 || publications[0].topic != "house/command/hall-switch/main/switch/power" {
		t.Fatalf("publications = %#v", publications)
	}

	transport.emit(stateTopic("house", discovered.ID, "main", "switch", "power"), stateMessage{
		SchemaVersion: 1,
		Value:         valuePointer(device.BoolValue(true)),
		Sequence:      1,
		ObservedAt:    time.Now().UTC(),
	}, false)
	waitForCondition(t, func() bool {
		current, ok := service.Command(command.ID)
		return ok && current.Status == domaincommand.StatusConfirmed && current.Outcome == domaincommand.OutcomeSucceeded
	}, "MQTT state report to confirm the property command")

	metrics := service.Metrics()
	if metrics.CommandsConfirmed != 1 || metrics.ProviderCommandsPublished != 1 || metrics.ProviderMessagesReceived != 2 {
		t.Fatalf("metrics = %#v", metrics)
	}
}

func mqttSwitch(id string, online, power bool) device.Device {
	item := device.Device{SchemaVersion: 1, ID: id, Name: id, Type: device.TypeSwitch, LastUpdateAt: time.Now().UTC(), Endpoints: []device.Endpoint{{ID: "main", Name: "Main", Type: "switch", Capabilities: []device.Capability{{ID: "switch", Type: "switch", Properties: []device.Property{{Definition: device.PropertyDefinition{ID: "power", Name: "Power", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true}, Value: device.BoolValue(power)}}, Commands: []device.CommandDefinition{{ID: "set-power", Name: "Set power", Idempotent: true, Parameters: []device.CommandParameter{{ID: "value", Name: "Value", Type: device.ValueTypeBool, Required: true}}}}}}}}}
	item.SetOnline(online)
	return item
}

func valuePointer(value device.PropertyValue) *device.PropertyValue { return &value }

func waitDevice(t *testing.T, events <-chan device.Device) device.Device {
	t.Helper()
	select {
	case item := <-events:
		return item
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for MQTT device event")
		return device.Device{}
	}
}

func assertNoDevice(t *testing.T, events <-chan device.Device) {
	t.Helper()
	select {
	case item := <-events:
		t.Fatalf("unexpected device event: %#v", item)
	case <-time.After(30 * time.Millisecond):
	}
}

func assertDevicePower(t *testing.T, item device.Device, expected bool) {
	t.Helper()
	property, exists := item.Property("main", "switch", "power")
	if !exists || property.Value.Bool == nil || *property.Value.Bool != expected {
		t.Fatalf("power = %#v, expected %v", property.Value, expected)
	}
}

func waitForCondition(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}
