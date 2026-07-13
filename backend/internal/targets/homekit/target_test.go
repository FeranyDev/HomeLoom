package homekit

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/brutella/hap/characteristic"
	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

func TestHomeKitAddressPreflightDetectsOccupiedPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := CheckAddressAvailable(listener.Addr().String()); err == nil {
		t.Fatal("occupied address was accepted")
	}
	available, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := available.Addr().String()
	available.Close()
	if err := CheckAddressAvailable(address); err != nil {
		t.Fatalf("available address rejected: %v", err)
	}
}

type memoryIdentityStore struct {
	values map[string]uint64
	next   map[string]uint64
}

func (s *memoryIdentityStore) HomeKitAccessoryAID(context.Context, string, string) (uint64, error) {
	return 2, nil
}
func (s *memoryIdentityStore) HomeKitIID(_ context.Context, targetID, deviceID, key string) (uint64, error) {
	composite := targetID + "/" + deviceID + "/" + key
	if value := s.values[composite]; value != 0 {
		return value, nil
	}
	scope := targetID + "/" + deviceID
	s.next[scope]++
	value := s.next[scope]
	s.values[composite] = value
	return value, nil
}

func TestFormatPin(t *testing.T) {
	if got := formatPin("00102003"); got != "001-02-003" {
		t.Fatalf("formatPin() = %q", got)
	}
	if got := formatPin("short"); got != "invalid" {
		t.Fatalf("formatPin(short) = %q", got)
	}
}

func TestAccessoryBindingsUpdateTemperatureAndOfflineFault(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider())
	defer service.Close()
	items, _ := service.List(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bindings := newAccessoryBindings(items, map[string]bool{}, map[string]uint64{"virtual-switch-1": 7, "virtual-temperature-1": 11}, service, logger)
	if len(bindings.accessories) != 2 || len(bindings.switches) != 1 || len(bindings.temperatures) != 1 || len(bindings.faults) != 2 {
		t.Fatalf("bindings = %#v", bindings)
	}
	if bindings.accessories[0].Id == 0 || bindings.accessories[1].Id == 0 || bindings.accessories[0].Id == bindings.accessories[1].Id {
		t.Fatalf("persistent AIDs were not applied: %d %d", bindings.accessories[0].Id, bindings.accessories[1].Id)
	}
	temperature := -12.5
	updated, err := service.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: "virtual-temperature-1", Properties: []providersdk.PropertyWriteRequest{{EndpointID: "main", CapabilityID: "temperature", PropertyID: "current-temperature", Value: device.NumberValue(temperature)}}})
	if err != nil {
		t.Fatal(err)
	}
	bindings.update(updated)
	if value := bindings.temperatures[updated.ID].Value(); value != 0 {
		t.Fatalf("clamped temperature = %v", value)
	}
	offline := false
	updated, err = service.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: updated.ID, Online: &offline})
	if err != nil {
		t.Fatal(err)
	}
	bindings.update(updated)
	if fault := bindings.faults[updated.ID].Value(); fault != characteristic.StatusFaultGeneralFault {
		t.Fatalf("fault = %d", fault)
	}
	online := true
	updated, _ = service.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: updated.ID, Online: &online})
	bindings.update(updated)
	if fault := bindings.faults[updated.ID].Value(); fault != characteristic.StatusFaultNoFault {
		t.Fatalf("recovered fault = %d", fault)
	}
}

func TestAccessoryBindingsHonorSelectedDevices(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider())
	defer service.Close()
	items, _ := service.List(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bindings := newAccessoryBindings(items, map[string]bool{"virtual-switch-1": true}, nil, service, logger)
	if len(bindings.accessories) != 1 || bindings.switches["virtual-switch-1"] == nil || bindings.temperatures["virtual-temperature-1"] != nil {
		t.Fatalf("selected bindings = %#v", bindings)
	}
}

func TestPersistentIIDsSurviveAccessoryRebuildAndNewCharacteristic(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider())
	defer service.Close()
	items, _ := service.List(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	identities := &memoryIdentityStore{values: make(map[string]uint64), next: make(map[string]uint64)}
	first := newAccessoryBindings(items, map[string]bool{"virtual-switch-1": true}, map[string]uint64{"virtual-switch-1": 2}, service, logger).byDevice["virtual-switch-1"]
	if err := assignPersistentIIDs(context.Background(), "bridge", "virtual-switch-1", first, identities); err != nil {
		t.Fatal(err)
	}
	existing := make(map[string]uint64)
	for _, currentService := range first.Ss {
		existing["s:"+currentService.Type] = currentService.Id
		for _, current := range currentService.Cs {
			existing["c:"+currentService.Type+":"+current.Type] = current.Id
		}
	}
	second := newAccessoryBindings(items, map[string]bool{"virtual-switch-1": true}, map[string]uint64{"virtual-switch-1": 2}, service, logger).byDevice["virtual-switch-1"]
	newCharacteristic := characteristic.NewStatusActive()
	second.Ss[len(second.Ss)-1].AddC(newCharacteristic.C)
	if err := assignPersistentIIDs(context.Background(), "bridge", "virtual-switch-1", second, identities); err != nil {
		t.Fatal(err)
	}
	for _, currentService := range second.Ss {
		if expected, ok := existing["s:"+currentService.Type]; ok && currentService.Id != expected {
			t.Fatalf("service IID changed: %d -> %d", expected, currentService.Id)
		}
		for _, current := range currentService.Cs {
			if expected, ok := existing["c:"+currentService.Type+":"+current.Type]; ok && current.Id != expected {
				t.Fatalf("characteristic %s IID changed: %d -> %d", current.Type, expected, current.Id)
			}
		}
	}
	if newCharacteristic.Id <= uint64(len(existing)) {
		t.Fatalf("new characteristic IID %d was not appended", newCharacteristic.Id)
	}
}

func TestAccessoryBindingsMapSupportedDeviceTypes(t *testing.T) {
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "room", Name: "Room", Config: []byte(`{"devices":[{"id":"plain","type":"switch"},{"id":"lamp","type":"lightbulb","power":true},{"id":"socket","type":"outlet","power":true},{"id":"temp","type":"temperature-sensor"},{"id":"humidity","type":"humidity-sensor","humidity":42.5},{"id":"door","type":"contact-sensor","contact":true},{"id":"motion","type":"motion-sensor","motion":true}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(provider)
	defer service.Close()
	items, _ := service.List(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bindings := newAccessoryBindings(items, map[string]bool{}, nil, service, logger)
	if len(bindings.accessories) != 7 || len(bindings.switches) != 3 || len(bindings.outletInUse) != 1 || len(bindings.temperatures) != 1 || len(bindings.humidities) != 1 || len(bindings.contacts) != 1 || len(bindings.motions) != 1 {
		t.Fatalf("bindings = %#v", bindings)
	}
	if !bindings.switches["lamp"].Value() || !bindings.outletInUse["socket"].Value() {
		t.Fatal("initial powered values were not mapped")
	}
	if bindings.humidities["humidity"].Value() != 42.5 || bindings.contacts["door"].Value() != characteristic.ContactSensorStateContactDetected || !bindings.motions["motion"].Value() {
		t.Fatal("sensor values were not mapped")
	}
	off := false
	updated, err := service.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: "socket", Properties: []providersdk.PropertyWriteRequest{{EndpointID: "main", CapabilityID: "switch", PropertyID: "power", Value: device.BoolValue(off)}}})
	if err != nil {
		t.Fatal(err)
	}
	bindings.update(updated)
	if bindings.switches["socket"].Value() || bindings.outletInUse["socket"].Value() {
		t.Fatal("outlet power was not updated")
	}
}
