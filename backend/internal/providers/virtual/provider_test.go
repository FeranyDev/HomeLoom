package virtual

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

func TestSetPower(t *testing.T) {
	provider := NewProvider()

	updated, err := provider.SetPower(context.Background(), "virtual-switch-1", true)
	if err != nil {
		t.Fatalf("SetPower() error = %v", err)
	}
	if !boolProperty(updated, "switch", "power") {
		t.Fatal("SetPower() did not persist the requested value")
	}

	items, err := provider.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, item := range items {
		if item.ID == updated.ID && !boolProperty(item, "switch", "power") {
			t.Fatal("List() returned stale switch state")
		}
	}
}

func TestProviderUsesDatabaseDeviceConfiguration(t *testing.T) {
	provider, err := NewProviderFromConfig(providerconfig.Config{ID: "virtual-lab", Name: "Lab", Config: []byte(`{
		"devices":[
			{"id":"desk-light","name":"Desk light","type":"switch","power":true},
			{"id":"outdoor-temp","name":"Outdoor","type":"temperature-sensor","temperature":12.5,"online":false}
		]}`)})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := provider.List(context.Background())
	if len(items) != 2 {
		t.Fatalf("devices = %#v", items)
	}
	if items[0].ID != "desk-light" || !boolProperty(items[0], "switch", "power") {
		t.Fatalf("switch = %#v", items[0])
	}
	if items[1].ID != "outdoor-temp" || items[1].Online || items[1].Type != device.TypeTemperatureSensor || numberProperty(items[1], "temperature", "current-temperature") != 12.5 {
		t.Fatalf("sensor = %#v", items[1])
	}
}

func TestProviderAddsConfiguredDeviceThroughLiveReconfiguration(t *testing.T) {
	current, err := NewProviderFromConfig(providerconfig.Config{ID: "virtual-lab", Name: "Lab", Config: []byte(`{"devices":[{"id":"desk-switch","type":"switch","power":false}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := current.SetPower(context.Background(), "desk-switch", true); err != nil {
		t.Fatal(err)
	}
	replacement, err := NewProviderFromConfig(providerconfig.Config{ID: "virtual-lab", Name: "Updated Lab", Config: []byte(`{"devices":[{"id":"desk-switch","type":"switch","power":false},{"id":"desk-outlet","type":"outlet","power":false}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	handled, err := current.Reconfigure(context.Background(), replacement)
	if err != nil || !handled {
		t.Fatalf("Reconfigure() = %v, %v", handled, err)
	}
	items, err := current.DiscoverDevices(context.Background())
	if err != nil || len(items) != 2 || current.Manifest().Name != "Updated Lab" {
		t.Fatalf("items=%#v manifest=%#v err=%v", items, current.Manifest(), err)
	}
	for _, item := range items {
		if item.ID == "desk-switch" && !boolProperty(item, "switch", "power") {
			t.Fatal("existing runtime state was reset while adding a child device")
		}
	}
}

func TestProviderSupportsLightbulbAndOutlet(t *testing.T) {
	provider, err := NewProviderFromConfig(providerconfig.Config{ID: "virtual-room", Name: "Room", Config: []byte(`{"devices":[{"id":"lamp","type":"lightbulb","power":true},{"id":"socket","type":"outlet","power":false}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := provider.List(context.Background())
	if len(items) != 2 || items[0].Type != device.TypeLightbulb || items[1].Type != device.TypeOutlet {
		t.Fatalf("items = %#v", items)
	}
	updated, err := provider.SetPower(context.Background(), "socket", true)
	if err != nil || !boolProperty(updated, "switch", "power") {
		t.Fatalf("outlet update = %#v, %v", updated, err)
	}
}

func TestProviderSupportsHumidityContactAndMotionSensors(t *testing.T) {
	provider, err := NewProviderFromConfig(providerconfig.Config{ID: "sensors", Config: []byte(`{"devices":[{"id":"humidity","type":"humidity-sensor","humidity":61.2},{"id":"room-climate","type":"temperature-humidity-sensor","temperature":22.4,"humidity":48.5},{"id":"door","type":"contact-sensor","contact":true},{"id":"motion","type":"motion-sensor","motion":false}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := provider.List(context.Background())
	if len(items) != 4 || items[0].Type != device.TypeContactSensor || items[1].Type != device.TypeHumiditySensor || items[2].Type != device.TypeMotionSensor || items[3].Type != device.TypeTemperatureHumiditySensor {
		t.Fatalf("items = %#v", items)
	}
	if numberProperty(items[3], "temperature", "current-temperature") != 22.4 || numberProperty(items[3], "humidity", "current-humidity") != 48.5 {
		t.Fatalf("combined sensor = %#v", items[3])
	}
	humidity := 72.5
	updated, err := provider.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: "humidity", Properties: []providersdk.PropertyWriteRequest{{EndpointID: "main", CapabilityID: "humidity", PropertyID: "current-humidity", Value: device.NumberValue(humidity)}}})
	if err != nil {
		t.Fatal(err)
	}
	property, ok := updated.Property("main", "humidity", "current-humidity")
	if !ok || property.Value.Number == nil || *property.Value.Number != humidity {
		t.Fatalf("humidity = %#v", updated)
	}
}

func TestProviderSupportsFanAirPurifierFilterAndWindowCovering(t *testing.T) {
	provider, err := NewProviderFromConfig(providerconfig.Config{ID: "advanced", Config: []byte(`{"devices":[{"id":"fan","type":"fan","active":true,"speed":35,"mode":"auto"},{"id":"purifier","type":"air-purifier","active":true,"speed":60,"filterLife":8},{"id":"shade","type":"window-covering","position":25}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := provider.List(context.Background())
	if len(items) != 3 || items[0].Type != device.TypeFan || items[1].Type != device.TypeAirPurifier || items[2].Type != device.TypeWindowCovering {
		t.Fatalf("items = %#v", items)
	}
	updated, err := provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: "fan", EndpointID: "main", CapabilityID: "fan", PropertyID: "rotation-speed", Value: device.NumberValue(0)})
	if err != nil || enumPropertyValue(updated, "fan", "current-state") != stateIdle {
		t.Fatalf("fan update = %#v, %v", updated, err)
	}
	updated, err = provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: "shade", EndpointID: "main", CapabilityID: "window-covering", PropertyID: "target-position", Value: device.IntValue(80)})
	if err != nil || intProperty(updated, "window-covering", "current-position") != 80 || enumPropertyValue(updated, "window-covering", "position-state") != positionStopped {
		t.Fatalf("window update = %#v, %v", updated, err)
	}
	updated, err = provider.ExecuteCommand(context.Background(), providersdk.CommandRequest{DeviceID: "purifier", EndpointID: "main", CapabilityID: "filter", CommandID: "reset-filter"})
	if err != nil || numberProperty(updated, "filter", "life-level") != 100 || boolProperty(updated, "filter", "change-indication") {
		t.Fatalf("filter reset = %#v, %v", updated, err)
	}
	if _, err := provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{DeviceID: "fan", EndpointID: "main", CapabilityID: "fan", PropertyID: "rotation-speed", Value: device.NumberValue(101)}); !errors.Is(err, providersdk.ErrPropertyInvalid) {
		t.Fatalf("invalid speed error = %v", err)
	}
}

func TestAllModelDemoDevicesPublishCompleteStandardContracts(t *testing.T) {
	raw, err := json.Marshal(Config{Devices: AllModelDeviceConfigs()})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewProviderFromConfig(providerconfig.Config{ID: "virtual-main", Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	items, err := provider.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(device.ModelContracts()) {
		t.Fatalf("device count = %d, contracts = %d", len(items), len(device.ModelContracts()))
	}
	for _, item := range items {
		contract, ok := device.ModelContractFor(item.Type)
		if !ok {
			t.Fatalf("missing contract for %q", item.Type)
		}
		for _, parameter := range contract.Parameters {
			property, found := item.Property(parameter.Path.EndpointID, parameter.Path.CapabilityID, parameter.Path.PropertyID)
			if !found {
				t.Errorf("%s is missing standard parameter %s", item.ID, parameter.Path)
				continue
			}
			if property.Definition.ParameterLevel != parameter.Level {
				t.Errorf("%s parameter %s level = %q, want %q", item.ID, parameter.Path, property.Definition.ParameterLevel, parameter.Level)
			}
		}
	}
}

func TestProviderSimulatesRejectedAndCancelledWrites(t *testing.T) {
	rejecting, err := NewProviderFromConfig(providerconfig.Config{ID: "reject", Config: []byte(`{"rejectWrites":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rejecting.SetPower(context.Background(), "reject-switch-1", true); !errors.Is(err, providersdk.ErrWriteRejected) {
		t.Fatalf("error = %v", err)
	}
	delayed, err := NewProviderFromConfig(providerconfig.Config{ID: "slow", Config: []byte(`{"latencyMs":1000}`)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := delayed.SetPower(ctx, "slow-switch-1", true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if time.Since(started) > 250*time.Millisecond {
		t.Fatal("write did not honor context cancellation")
	}
}

func TestProviderConfigSupportsUnknownAvailability(t *testing.T) {
	provider, err := NewProviderFromConfig(providerconfig.Config{ID: "virtual-test", Name: "Test", Config: []byte(`{"devices":[{"id":"pending","name":"Pending","type":"switch","availability":"unknown"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	items, err := provider.List(context.Background())
	if err != nil || len(items) != 1 || items[0].Availability != device.AvailabilityUnknown || items[0].Online {
		t.Fatalf("items = %#v, error = %v", items, err)
	}
}

func TestProviderRejectsInvalidDeviceConfiguration(t *testing.T) {
	for _, raw := range []string{`{"latencyMs":-1}`, `{"devices":[{"id":"x","type":"unknown"}]}`, `{"devices":[{"id":"x","type":"switch"},{"id":"x","type":"switch"}]}`, `{"devices":[{"id":"Invalid ID","type":"switch"}]}`} {
		if _, err := NewProviderFromConfig(providerconfig.Config{ID: "invalid", Config: []byte(raw)}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestSimulateUpdatesStateAndPublishesSnapshot(t *testing.T) {
	provider := NewProvider()
	events := make(chan device.Device, 1)
	unsubscribe := provider.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()
	online := false
	temperature := 18.25
	updated, err := provider.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: "virtual-temperature-1", Online: &online, Properties: []providersdk.PropertyWriteRequest{{EndpointID: "main", CapabilityID: "temperature", PropertyID: "current-temperature", Value: device.NumberValue(temperature)}}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Online || numberProperty(updated, "temperature", "current-temperature") != temperature {
		t.Fatalf("updated = %#v", updated)
	}
	select {
	case event := <-events:
		if event.ID != updated.ID || event.Online {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("simulation event was not published")
	}
	invalid := 500.0
	if _, err := provider.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: updated.ID, Properties: []providersdk.PropertyWriteRequest{{EndpointID: "main", CapabilityID: "temperature", PropertyID: "current-temperature", Value: device.NumberValue(invalid)}}}); !errors.Is(err, providersdk.ErrSimulationInvalid) {
		t.Fatalf("invalid error = %v", err)
	}
}

func TestNewProviderStartsWithFreshRuntimeState(t *testing.T) {
	first := NewProvider()
	if _, err := first.SetPower(context.Background(), "virtual-switch-1", true); err != nil {
		t.Fatal(err)
	}
	second := NewProvider()
	items, _ := second.List(context.Background())
	for _, item := range items {
		if item.ID == "virtual-switch-1" && boolProperty(item, "switch", "power") {
			t.Fatal("new provider restored an old runtime state")
		}
	}
}

func TestSimulationCanEmitRepeatedAndOutOfOrderSequences(t *testing.T) {
	provider := NewProvider()
	events := make(chan device.Device, 4)
	unsubscribe := provider.Subscribe(func(item device.Device) { events <- item })
	defer unsubscribe()
	sequence := uint64(9)
	updated, err := provider.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: "virtual-switch-1", Sequence: &sequence, Repeat: 2})
	if err != nil || updated.Sequence != sequence {
		t.Fatalf("simulation = %#v, %v", updated, err)
	}
	for index := 0; index < 2; index++ {
		if event := <-events; event.Sequence != sequence {
			t.Fatalf("event sequence = %d", event.Sequence)
		}
	}
	older := uint64(3)
	if event, err := provider.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: "virtual-switch-1", Sequence: &older}); err != nil || event.Sequence != older {
		t.Fatalf("out-of-order simulation = %#v, %v", event, err)
	}
	if _, err := provider.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: "virtual-switch-1", Repeat: 11}); !errors.Is(err, providersdk.ErrSimulationInvalid) {
		t.Fatalf("repeat error = %v", err)
	}
}

func TestProviderManifestAndCapabilities(t *testing.T) {
	provider := NewProvider()
	manifest := provider.Manifest()
	capabilities := provider.Capabilities()
	if manifest.ID != "virtual-main" || manifest.Type != "virtual" || manifest.Version == "" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if !capabilities.Discovery || !capabilities.PropertyRead || !capabilities.PropertyWrite || !capabilities.Commands || !capabilities.Events {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}

func TestReadProperty(t *testing.T) {
	provider := NewProvider()
	request := providersdk.PropertyReadRequest{DeviceID: "virtual-temperature-1", EndpointID: "main", CapabilityID: "temperature", PropertyID: "current-temperature"}
	property, err := provider.ReadProperty(context.Background(), request)
	if err != nil || property.Value.Number == nil || *property.Value.Number != 23.6 {
		t.Fatalf("ReadProperty() = %#v, %v", property, err)
	}
	request.PropertyID = "missing"
	if _, err := provider.ReadProperty(context.Background(), request); !errors.Is(err, providersdk.ErrPropertyUnsupported) {
		t.Fatalf("unsupported error = %v", err)
	}
	request.DeviceID = "missing"
	if _, err := provider.ReadProperty(context.Background(), request); !errors.Is(err, providersdk.ErrDeviceNotFound) {
		t.Fatalf("missing device error = %v", err)
	}
	request.DeviceID, request.PropertyID = "virtual-temperature-1", "value"
	offline := false
	if _, err := provider.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: request.DeviceID, Online: &offline}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ReadProperty(context.Background(), request); !errors.Is(err, providersdk.ErrProviderUnavailable) {
		t.Fatalf("offline error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.ReadProperty(canceled, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
}

func TestExecuteCommand(t *testing.T) {
	provider := NewProvider()
	request := providersdk.CommandRequest{DeviceID: "virtual-switch-1", EndpointID: "main", CapabilityID: "switch", CommandID: "toggle"}
	updated, err := provider.ExecuteCommand(context.Background(), request)
	if err != nil || !boolProperty(updated, "switch", "power") {
		t.Fatalf("toggle = %#v, %v", updated, err)
	}
	request.CommandID = "set-power"
	request.Parameters = map[string]device.PropertyValue{"value": device.BoolValue(false)}
	updated, err = provider.ExecuteCommand(context.Background(), request)
	if err != nil || boolProperty(updated, "switch", "power") {
		t.Fatalf("set-power = %#v, %v", updated, err)
	}
	request.Parameters = nil
	if _, err := provider.ExecuteCommand(context.Background(), request); !errors.Is(err, providersdk.ErrCommandInvalid) {
		t.Fatalf("invalid error = %v", err)
	}
}

func TestSetPowerErrors(t *testing.T) {
	provider := NewProvider()
	if _, err := provider.SetPower(context.Background(), "missing", true); !errors.Is(err, application.ErrDeviceNotFound) {
		t.Fatalf("missing device error = %v", err)
	}
	if _, err := provider.SetPower(context.Background(), "virtual-temperature-1", true); !errors.Is(err, application.ErrPropertyUnsupported) {
		t.Fatalf("unsupported property error = %v", err)
	}
	offline := false
	if _, err := provider.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: "virtual-switch-1", Online: &offline}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.SetPower(context.Background(), "virtual-switch-1", true); !errors.Is(err, providersdk.ErrProviderUnavailable) {
		t.Fatalf("offline error = %v", err)
	}
}

func TestSetPowerNotifiesSubscribers(t *testing.T) {
	provider := NewProvider()
	notifications := make(chan bool, 1)
	unsubscribe := provider.Subscribe(func(item device.Device) {
		if property, ok := item.Property("main", "switch", "power"); ok && property.Value.Bool != nil {
			notifications <- *property.Value.Bool
		}
	})
	defer unsubscribe()

	if _, err := provider.SetPower(context.Background(), "virtual-switch-1", true); err != nil {
		t.Fatalf("SetPower() error = %v", err)
	}
	select {
	case value := <-notifications:
		if !value {
			t.Fatal("subscriber received the wrong value")
		}
	default:
		t.Fatal("subscriber was not notified")
	}
}

func boolProperty(item device.Device, capabilityID, propertyID string) bool {
	property, ok := item.Property("main", capabilityID, propertyID)
	return ok && property.Value.Bool != nil && *property.Value.Bool
}

func numberProperty(item device.Device, capabilityID, propertyID string) float64 {
	property, ok := item.Property("main", capabilityID, propertyID)
	if !ok || property.Value.Number == nil {
		return 0
	}
	return *property.Value.Number
}

func intProperty(item device.Device, capabilityID, propertyID string) int64 {
	property, ok := item.Property("main", capabilityID, propertyID)
	if !ok || property.Value.Int == nil {
		return 0
	}
	return *property.Value.Int
}

func enumPropertyValue(item device.Device, capabilityID, propertyID string) string {
	property, ok := item.Property("main", capabilityID, propertyID)
	if !ok || property.Value.String == nil {
		return ""
	}
	return *property.Value.String
}
