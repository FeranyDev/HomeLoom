package homekit

import (
	"context"
	"encoding/json"
	"fmt"
	"go.uber.org/zap"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	domaintarget "github.com/feranydev/homeloom/backend/internal/domain/target"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

func TestSecureFSStoreEnforcesPrivatePermissionsAndRejectsSymlinks(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "identity")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(directory, "existing")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := newSecureFSStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("private:key", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{directory: 0o700, existing: 0o600, filepath.Join(directory, "privatekey"): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("%s mode = %v", path, info.Mode().Perm())
		}
	}
	unsafe := filepath.Join(t.TempDir(), "unsafe")
	if err := os.MkdirAll(unsafe, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(existing, filepath.Join(unsafe, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := newSecureFSStore(unsafe); err == nil {
		t.Fatal("identity symlink was accepted")
	}
}

func TestHasPairingsReadsPersistedControllerIdentity(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "identity")
	paired, err := HasPairings(directory)
	if err != nil || paired {
		t.Fatalf("empty HasPairings() = %v, %v", paired, err)
	}
	store, err := newSecureFSStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("controller.pairing", []byte(`{"name":"controller"}`)); err != nil {
		t.Fatal(err)
	}
	paired, err = HasPairings(directory)
	if err != nil || !paired {
		t.Fatalf("persisted HasPairings() = %v, %v", paired, err)
	}
}

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
	logger := zap.NewNop()
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
	if pushes := bindings.update(updated); pushes != 4 {
		t.Fatalf("temperature pushes = %d, want fault + measurement + two battery properties", pushes)
	}
	if value := bindings.temperatures[updated.ID].Value(); value != 0 {
		t.Fatalf("clamped temperature = %v", value)
	}
	offline := false
	updated, err = service.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: updated.ID, Online: &offline})
	if err != nil {
		t.Fatal(err)
	}
	if pushes := bindings.update(updated); pushes != 1 {
		t.Fatalf("offline pushes = %d, want fault only", pushes)
	}
	if fault := bindings.faults[updated.ID].Value(); fault != characteristic.StatusFaultGeneralFault {
		t.Fatalf("fault = %d", fault)
	}
	updated.SetAvailability(device.AvailabilityUnknown)
	if pushes := bindings.update(updated); pushes != 1 || bindings.faults[updated.ID].Value() != characteristic.StatusFaultGeneralFault {
		t.Fatalf("unknown availability did not preserve fault, pushes = %d", pushes)
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
	logger := zap.NewNop()
	bindings := newAccessoryBindings(items, map[string]bool{"virtual-switch-1": true}, nil, service, logger)
	if len(bindings.accessories) != 1 || bindings.switches["virtual-switch-1"] == nil || bindings.temperatures["virtual-temperature-1"] != nil {
		t.Fatalf("selected bindings = %#v", bindings)
	}
}

func TestTargetBuildsConsumerOwnedVirtualAccessoryIdentity(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider())
	defer service.Close()
	logger := zap.NewNop()
	target, err := New(context.Background(), Config{ID: "bridge", Name: "Bridge", Address: "127.0.0.1:0", Pin: "12345678", SetupID: "TEST", StorePath: t.TempDir(), Devices: []domaintarget.VirtualDevice{{ID: "living-switch", Name: "客厅虚拟开关", Type: device.TypeSwitch, SourceDeviceID: "virtual-switch-1", Enabled: true}}}, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	if got := target.PairingInfo().Devices; len(got) != 1 || got[0] != "living-switch" {
		t.Fatalf("virtual accessory identities = %#v", got)
	}
}

func TestEmptyTargetPublishesNoAccessories(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider())
	defer service.Close()
	logger := zap.NewNop()
	target, err := New(context.Background(), Config{
		ID: "empty-bridge", Name: "Empty Bridge", Address: "127.0.0.1:0",
		Pin: "12345678", SetupID: "NONE", StorePath: t.TempDir(),
	}, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	if target.PublishedAccessoryCount() != 0 {
		t.Fatalf("published accessories = %d, want 0", target.PublishedAccessoryCount())
	}
	if devices := target.PairingInfo().Devices; len(devices) != 0 {
		t.Fatalf("pairing devices = %#v, want none", devices)
	}
}

func TestLegacyDeviceIDsStillSelectAccessories(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider())
	defer service.Close()
	logger := zap.NewNop()
	target, err := New(context.Background(), Config{
		ID: "legacy-bridge", Name: "Legacy Bridge", Address: "127.0.0.1:0",
		Pin: "12345678", SetupID: "OLD1", StorePath: t.TempDir(),
		DeviceIDs: []string{"virtual-switch-1"},
	}, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	if target.PublishedAccessoryCount() != 1 {
		t.Fatalf("published accessories = %d, want 1", target.PublishedAccessoryCount())
	}
	if devices := target.PairingInfo().Devices; len(devices) != 1 || devices[0] != "virtual-switch-1" {
		t.Fatalf("pairing devices = %#v", devices)
	}
}

func TestPersistentIIDsSurviveAccessoryRebuildAndNewCharacteristic(t *testing.T) {
	service := application.NewDeviceService(virtual.NewProvider())
	defer service.Close()
	items, _ := service.List(context.Background())
	logger := zap.NewNop()
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
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "room", Name: "Room", Config: []byte(`{"devices":[{"id":"plain","type":"switch"},{"id":"lamp","type":"lightbulb","power":true},{"id":"socket","type":"outlet","power":true},{"id":"temp","type":"temperature-sensor"},{"id":"humidity","type":"humidity-sensor","humidity":42.5},{"id":"climate","type":"temperature-humidity-sensor","temperature":21.5,"humidity":52.5},{"id":"door","type":"contact-sensor","contact":true},{"id":"motion","type":"motion-sensor","motion":true}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(provider)
	defer service.Close()
	items, _ := service.List(context.Background())
	logger := zap.NewNop()
	bindings := newAccessoryBindings(items, map[string]bool{}, nil, service, logger)
	if len(bindings.accessories) != 8 || len(bindings.switches) != 3 || len(bindings.outletInUse) != 1 || len(bindings.temperatures) != 2 || len(bindings.humidities) != 2 || len(bindings.contacts) != 1 || len(bindings.motions) != 1 || len(bindings.brightness) != 1 || len(bindings.colorTemps) != 1 || len(bindings.hues) != 1 || len(bindings.saturations) != 1 || len(bindings.batteryLevels) != 5 || len(bindings.lowBatteries) != 5 || len(bindings.tampered) != 2 {
		t.Fatalf("bindings = %#v", bindings)
	}
	if !bindings.switches["lamp"].Value() || !bindings.outletInUse["socket"].Value() {
		t.Fatal("initial powered values were not mapped")
	}
	if bindings.humidities["humidity"].Value() != 42.5 || bindings.contacts["door"].Value() != characteristic.ContactSensorStateContactDetected || !bindings.motions["motion"].Value() {
		t.Fatal("sensor values were not mapped")
	}
	if bindings.temperatures["climate"].Value() != 21.5 || bindings.humidities["climate"].Value() != 52.5 {
		t.Fatal("combined temperature/humidity values were not mapped")
	}
	if bindings.batteryLevels["door"].Value() != 100 || bindings.lowBatteries["door"].Value() != characteristic.StatusLowBatteryBatteryLevelNormal || bindings.tampered["door"].Value() != characteristic.StatusTamperedNotTampered {
		t.Fatal("sensor battery and tamper values were not mapped")
	}
	if bindings.batteryLevels["temp"].Value() != 100 || bindings.lowBatteries["climate"].Value() != characteristic.StatusLowBatteryBatteryLevelNormal {
		t.Fatal("single and combined sensor battery values were not mapped")
	}
	request := &http.Request{}
	if _, status := bindings.brightness["lamp"].SetValueRequest(42, request); status != 0 {
		t.Fatalf("brightness write status = %d", status)
	}
	property, err := provider.ReadProperty(context.Background(), providersdk.PropertyReadRequest{DeviceID: "lamp", EndpointID: "main", CapabilityID: "light", PropertyID: "brightness"})
	if err != nil || property.Value.Number == nil || *property.Value.Number != 42 {
		t.Fatalf("brightness was not written back: %#v, %v", property, err)
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

func TestColorTemperaturePublishesDeviceRangeAndClampsUpdates(t *testing.T) {
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{
		ID: "room", Name: "Room",
		Config: []byte(`{"devices":[{"id":"lamp","type":"lightbulb","power":true}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(provider)
	defer service.Close()
	items, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var lamp *device.Device
	for index := range items {
		if items[index].ID == "lamp" {
			lamp = &items[index]
			break
		}
	}
	if lamp == nil {
		t.Fatal("virtual lightbulb not found")
	}
	minimum, maximum := 154.0, 370.0
	for endpointIndex := range lamp.Endpoints {
		for capabilityIndex := range lamp.Endpoints[endpointIndex].Capabilities {
			for propertyIndex := range lamp.Endpoints[endpointIndex].Capabilities[capabilityIndex].Properties {
				property := &lamp.Endpoints[endpointIndex].Capabilities[capabilityIndex].Properties[propertyIndex]
				if property.Definition.ID == "color-temperature" {
					property.Definition.Min, property.Definition.Max = &minimum, &maximum
					property.Value = device.IntValue(250)
				}
			}
		}
	}
	logger := zap.NewNop()
	bindings := newAccessoryBindings([]device.Device{*lamp}, map[string]bool{}, nil, service, logger)
	current := bindings.colorTemps[lamp.ID]
	if current == nil {
		t.Fatal("color temperature characteristic was not published")
	}
	if current.MinValue() != 154 || current.MaxValue() != 370 || current.Value() != 250 {
		t.Fatalf("color temperature range/value = %d..%d value %d", current.MinValue(), current.MaxValue(), current.Value())
	}

	lamp.SetProperty("main", "light", "color-temperature", device.IntValue(800))
	if pushes := bindings.update(*lamp); pushes == 0 || current.Value() != 370 {
		t.Fatalf("high update was not clamped: pushes=%d value=%d", pushes, current.Value())
	}
	lamp.SetProperty("main", "light", "color-temperature", device.IntValue(100))
	if pushes := bindings.update(*lamp); pushes == 0 || current.Value() != 154 {
		t.Fatalf("low update was not clamped: pushes=%d value=%d", pushes, current.Value())
	}
}

func TestAccessoryBindingsMapAndWriteAdvancedDeviceTypes(t *testing.T) {
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "advanced", Config: []byte(`{"devices":[{"id":"fan","type":"fan","speed":20},{"id":"purifier","type":"air-purifier","active":true,"speed":60,"mode":"auto","filterLife":5},{"id":"shade","type":"window-covering","position":30}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(provider)
	defer service.Close()
	items, _ := service.List(context.Background())
	bindings := newAccessoryBindings(items, map[string]bool{}, nil, service, zap.NewNop())
	if len(bindings.accessories) != 3 || len(bindings.actives) != 2 || len(bindings.fanCurrent) != 1 || len(bindings.airCurrent) != 1 || len(bindings.filterLife) != 1 || len(bindings.positions) != 1 || len(bindings.swingModes) != 2 || len(bindings.directions) != 1 || len(bindings.controlLocks) != 2 || len(bindings.airQualities) != 1 || len(bindings.pm25) != 1 || len(bindings.voc) != 1 || len(bindings.obstructions) != 1 {
		t.Fatalf("advanced bindings = %#v", bindings)
	}
	if bindings.fanCurrent["fan"].Value() != characteristic.CurrentFanStateInactive || bindings.airCurrent["purifier"].Value() != characteristic.CurrentAirPurifierStatePurifyingAir || bindings.filterChange["purifier"].Value() != characteristic.FilterChangeIndicationChangeFilter || bindings.positions["shade"].Value() != 30 || bindings.airQualities["purifier"].Value() != characteristic.AirQualityGood || bindings.pm25["purifier"].Value() != 12 || bindings.voc["purifier"].Value() != 80 {
		t.Fatal("initial advanced values were not mapped")
	}
	request := &http.Request{}
	if _, status := bindings.actives["fan"].SetValueRequest(characteristic.ActiveActive, request); status != 0 {
		t.Fatalf("fan write status = %d", status)
	}
	property, _ := provider.ReadProperty(context.Background(), providersdk.PropertyReadRequest{DeviceID: "fan", EndpointID: "main", CapabilityID: "fan", PropertyID: "active"})
	if property.Value.Bool == nil || !*property.Value.Bool {
		t.Fatal("fan active was not written back")
	}
	if _, status := bindings.positionTargets["shade"].SetValueRequest(75, request); status != 0 {
		t.Fatalf("window write status = %d", status)
	}
	property, _ = provider.ReadProperty(context.Background(), providersdk.PropertyReadRequest{DeviceID: "shade", EndpointID: "main", CapabilityID: "window-covering", PropertyID: "current-position"})
	if property.Value.Int == nil || *property.Value.Int != 75 {
		t.Fatal("window target was not written back")
	}
	if _, status := bindings.filterResets["purifier"].SetValueRequest(1, request); status != 0 {
		t.Fatalf("filter reset status = %d", status)
	}
	property, _ = provider.ReadProperty(context.Background(), providersdk.PropertyReadRequest{DeviceID: "purifier", EndpointID: "main", CapabilityID: "filter", PropertyID: "life-level"})
	if property.Value.Number == nil || *property.Value.Number != 100 {
		t.Fatal("filter reset was not written back")
	}
	offline := false
	item, err := provider.Simulate(context.Background(), providersdk.SimulationRequest{DeviceID: "purifier", Online: &offline})
	if err != nil {
		t.Fatal(err)
	}
	if pushes := bindings.update(item); pushes != 3 || bindings.faults["purifier"].Value() != characteristic.StatusFaultGeneralFault || len(bindings.extraFaults["purifier"]) != 2 || bindings.extraFaults["purifier"][0].Value() != characteristic.StatusFaultGeneralFault || bindings.extraFaults["purifier"][1].Value() != characteristic.StatusFaultGeneralFault {
		t.Fatalf("air purifier offline faults were not mapped, pushes=%d", pushes)
	}
	if _, status := bindings.actives["purifier"].SetValueRequest(characteristic.ActiveInactive, request); status != -70402 || bindings.actives["purifier"].Value() != characteristic.ActiveActive {
		t.Fatalf("offline HomeKit write status=%d value=%d", status, bindings.actives["purifier"].Value())
	}
}

func TestAccessoryBindingsMapAndWriteAirConditioner(t *testing.T) {
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "climate", Config: []byte(`{"devices":[{"id":"ac","name":"客厅空调","type":"air-conditioner"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(provider)
	defer service.Close()
	items, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		contract, found := homeKitModelContract(item.Type)
		if !found {
			t.Errorf("HomeKit contract for %q is missing", item.Type)
			continue
		}
		if _, projectErr := device.ProjectForConsumer(item, contract); projectErr != nil {
			t.Errorf("HomeKit projection for %q failed: %v", item.Type, projectErr)
		}
	}
	bindings := newAccessoryBindings(items, map[string]bool{}, nil, service, zap.NewNop())
	if len(bindings.accessories) != 1 || bindings.actives["ac"] == nil || bindings.heaterCurrent["ac"] == nil || bindings.heaterTargets["ac"] == nil || bindings.temperatures["ac"] == nil || bindings.coolingTargets["ac"] == nil || bindings.heatingTargets["ac"] == nil || bindings.speeds["ac"] == nil || bindings.swingModes["ac"] == nil || bindings.humidities["ac"] == nil || bindings.filterLife["ac"] == nil || bindings.filterChange["ac"] == nil || findHomeKitCharacteristic(bindings.byDevice["ac"], characteristic.TypeTemperatureDisplayUnits) == nil {
		t.Fatalf("air-conditioner bindings = %#v", bindings)
	}
	if bindings.heaterCurrent["ac"].Value() != characteristic.CurrentHeaterCoolerStateInactive || bindings.heaterTargets["ac"].Value() != characteristic.TargetHeaterCoolerStateAuto || bindings.temperatures["ac"].Value() != 23.5 || bindings.coolingTargets["ac"].Value() != 22 || bindings.heatingTargets["ac"].Value() != 22 {
		t.Fatal("initial air-conditioner values were not mapped")
	}
	request := &http.Request{}
	if _, status := bindings.actives["ac"].SetValueRequest(characteristic.ActiveActive, request); status != 0 {
		t.Fatalf("active write status = %d", status)
	}
	active, err := provider.ReadProperty(context.Background(), providersdk.PropertyReadRequest{DeviceID: "ac", EndpointID: "main", CapabilityID: "air-conditioner", PropertyID: "active"})
	if err != nil || active.Value.Bool == nil || !*active.Value.Bool {
		t.Fatalf("active was not written back: %#v, %v", active, err)
	}
	if _, status := bindings.heaterTargets["ac"].SetValueRequest(characteristic.TargetHeaterCoolerStateCool, request); status != 0 {
		t.Fatalf("target mode write status = %d", status)
	}
	mode, err := provider.ReadProperty(context.Background(), providersdk.PropertyReadRequest{DeviceID: "ac", EndpointID: "main", CapabilityID: "air-conditioner", PropertyID: "target-mode"})
	if err != nil || mode.Value.String == nil || *mode.Value.String != "cool" {
		t.Fatalf("target mode was not written back: %#v, %v", mode, err)
	}
	if _, status := bindings.coolingTargets["ac"].SetValueRequest(24.5, request); status != 0 {
		t.Fatalf("target temperature write status = %d", status)
	}
	temperature, err := provider.ReadProperty(context.Background(), providersdk.PropertyReadRequest{DeviceID: "ac", EndpointID: "main", CapabilityID: "temperature", PropertyID: "target-temperature"})
	if err != nil || temperature.Value.Number == nil || *temperature.Value.Number != 24.5 {
		t.Fatalf("target temperature was not written back: %#v, %v", temperature, err)
	}
}

func TestAccessoryBindingsBuildAllExtendedHomeKitDeviceTypes(t *testing.T) {
	types := []string{
		"illuminance-sensor", "occupancy-sensor", "leak-sensor", "smoke-sensor", "carbon-monoxide-sensor", "carbon-dioxide-sensor", "air-quality-sensor",
		"thermostat", "heater-cooler", "humidifier-dehumidifier", "lock", "garage-door", "security-system", "valve", "speaker",
	}
	definitions := make([]virtual.DeviceConfig, 0, len(types))
	for _, deviceType := range types {
		definitions = append(definitions, virtual.DeviceConfig{ID: deviceType, Name: deviceType, Type: deviceType})
	}
	raw, err := json.Marshal(virtual.Config{Devices: definitions})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "extended", Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(provider)
	defer service.Close()
	items, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(types) {
		t.Fatalf("extended unified devices = %d, want %d", len(items), len(types))
	}
	for _, item := range items {
		contract, found := homeKitModelContract(item.Type)
		if !found {
			t.Errorf("HomeKit contract for %q is missing", item.Type)
			continue
		}
		if _, projectErr := device.ProjectForConsumer(item, contract); projectErr != nil {
			t.Errorf("HomeKit projection for %q failed: %v", item.Type, projectErr)
		}
	}
	bindings := newAccessoryBindings(items, map[string]bool{}, nil, service, zap.NewNop())
	if len(bindings.accessories) != len(types) {
		t.Fatalf("extended HomeKit accessories = %d, want %d", len(bindings.accessories), len(types))
	}
	for _, deviceType := range types {
		if bindings.byDevice[deviceType] == nil || bindings.faults[deviceType] == nil {
			t.Errorf("HomeKit accessory %q was not built", deviceType)
		}
	}

	type writeCase struct {
		deviceID, characteristicType, capabilityID, propertyID string
		input                                                  any
		want                                                   device.PropertyValue
	}
	writes := []writeCase{
		{"thermostat", characteristic.TypeTargetHeatingCoolingState, "thermostat", "target-mode", characteristic.TargetHeatingCoolingStateCool, device.EnumValue("cool")},
		{"thermostat", characteristic.TypeTargetTemperature, "temperature", "target-temperature", 24.0, device.NumberValue(24)},
		{"heater-cooler", characteristic.TypeTargetHeaterCoolerState, "heater-cooler", "target-state", characteristic.TargetHeaterCoolerStateHeat, device.EnumValue("heat")},
		{"humidifier-dehumidifier", characteristic.TypeTargetHumidifierDehumidifierState, "humidifier-dehumidifier", "target-state", characteristic.TargetHumidifierDehumidifierStateDehumidifier, device.EnumValue("dehumidify")},
		{"lock", characteristic.TypeLockTargetState, "lock", "target-state", characteristic.LockTargetStateSecured, device.EnumValue("secured")},
		{"garage-door", characteristic.TypeTargetDoorState, "garage-door", "target-state", characteristic.TargetDoorStateClosed, device.EnumValue("closed")},
		{"security-system", characteristic.TypeSecuritySystemTargetState, "security-system", "target-state", characteristic.SecuritySystemTargetStateAwayArm, device.EnumValue("away-arm")},
		{"valve", characteristic.TypeActive, "valve", "active", characteristic.ActiveActive, device.BoolValue(true)},
		{"speaker", characteristic.TypeMute, "speaker", "mute", true, device.BoolValue(true)},
		{"speaker", characteristic.TypeVolume, "speaker", "volume", 55, device.NumberValue(55)},
		{"speaker", characteristic.TypeTargetMediaState, "speaker", "target-media-state", characteristic.TargetMediaStatePause, device.EnumValue("pause")},
	}
	request := &http.Request{}
	for _, test := range writes {
		current := findHomeKitCharacteristic(bindings.byDevice[test.deviceID], test.characteristicType)
		if current == nil {
			t.Errorf("%s characteristic %s is missing", test.deviceID, test.characteristicType)
			continue
		}
		if _, status := current.SetValueRequest(test.input, request); status != 0 {
			t.Errorf("%s characteristic %s write status = %d", test.deviceID, test.characteristicType, status)
			continue
		}
		property, readErr := provider.ReadProperty(context.Background(), providersdk.PropertyReadRequest{DeviceID: test.deviceID, EndpointID: "main", CapabilityID: test.capabilityID, PropertyID: test.propertyID})
		if readErr != nil || !reflect.DeepEqual(property.Value, test.want) {
			t.Errorf("%s/%s write = %#v, %v; want %#v", test.deviceID, test.propertyID, property.Value, readErr, test.want)
		}
	}
}

func findHomeKitCharacteristic(item *accessory.A, characteristicType string) *characteristic.C {
	if item == nil {
		return nil
	}
	for _, currentService := range item.Ss {
		for _, current := range currentService.Cs {
			if current.Type == characteristicType {
				return current
			}
		}
	}
	return nil
}

func TestAccessoryBindingsPublishMinimalAirPurifierWithoutFilter(t *testing.T) {
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "minimal-purifier", Config: []byte(`{"devices":[{"id":"purifier","type":"air-purifier","active":true,"speed":40,"mode":"manual"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(provider)
	defer service.Close()
	items, _ := service.List(context.Background())
	var purifier *device.Device
	for index := range items {
		if items[index].ID == "purifier" {
			item := items[index]
			// Real Xiaomi-like purifiers often only publish active/current-state initially.
			item.Endpoints = []device.Endpoint{{ID: "main", Name: "主端点", Type: "air-purifier", Capabilities: []device.Capability{{ID: "air-purifier", Type: "air-purifier", Properties: []device.Property{
				{Definition: device.PropertyDefinition{ID: "active", Name: "启用", Type: device.ValueTypeBool, Readable: true, Writable: true, Notifiable: true}, Value: device.BoolValue(true)},
				{Definition: device.PropertyDefinition{ID: "current-state", Name: "当前状态", Type: device.ValueTypeEnum, Readable: true, Notifiable: true, Enum: []string{"inactive", "idle", "purifying-air"}}, Value: device.EnumValue("purifying-air")},
			}}}}}
			purifier = &item
			items[index] = item
		}
	}
	if purifier == nil {
		t.Fatal("purifier device missing")
	}
	contract, found := homeKitModelContract(device.TypeAirPurifier)
	if !found {
		t.Fatal("HomeKit air purifier contract missing")
	}
	if _, err := device.ProjectForConsumer(*purifier, contract); err != nil {
		t.Fatalf("minimal purifier should project to HomeKit: %v", err)
	}
	bindings := newAccessoryBindings(items, map[string]bool{"purifier": true}, nil, service, zap.NewNop())
	if len(bindings.accessories) != 1 || bindings.actives["purifier"] == nil || bindings.airCurrent["purifier"] == nil || bindings.airTargets["purifier"] == nil {
		t.Fatalf("minimal purifier bindings = %#v", bindings)
	}
	if bindings.speeds["purifier"] != nil || bindings.filterLife["purifier"] != nil || bindings.filterChange["purifier"] != nil || bindings.filterResets["purifier"] != nil || bindings.airQualities["purifier"] != nil {
		t.Fatalf("optional characteristics should be omitted: speeds=%v filterLife=%v filterChange=%v filterResets=%v airQuality=%v", bindings.speeds["purifier"], bindings.filterLife["purifier"], bindings.filterChange["purifier"], bindings.filterResets["purifier"], bindings.airQualities["purifier"])
	}
	accessory := bindings.byDevice["purifier"]
	if accessory == nil {
		t.Fatal("purifier accessory missing")
	}
	serviceTypes := make([]string, 0, len(accessory.Ss))
	for _, current := range accessory.Ss {
		serviceTypes = append(serviceTypes, current.Type)
	}
	if len(serviceTypes) != 2 || serviceTypes[0] != "3E" || serviceTypes[1] != "BB" {
		t.Fatalf("minimal purifier services = %#v", serviceTypes)
	}
	if len(accessory.Ss[1].Linked) != 0 {
		t.Fatalf("minimal purifier should not link missing services: %#v", accessory.Ss[1].Linked)
	}
}

func TestAccessoryBindingsHandleOneHundredAccessoriesAndBurstUpdates(t *testing.T) {
	definitions := make([]virtual.DeviceConfig, 0, 100)
	types := []string{"switch", "fan", "air-purifier", "window-covering", "temperature-sensor"}
	for index := 0; index < 100; index++ {
		definitions = append(definitions, virtual.DeviceConfig{ID: fmt.Sprintf("load-%03d", index), Name: fmt.Sprintf("Load %d", index), Type: types[index%len(types)]})
	}
	raw, err := json.Marshal(virtual.Config{Devices: definitions})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "load", Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(provider)
	defer service.Close()
	items, _ := service.List(context.Background())
	bindings := newAccessoryBindings(items, map[string]bool{}, nil, service, zap.NewNop())
	if len(bindings.accessories) != 100 || len(bindings.faults) != 100 {
		t.Fatalf("accessories=%d faults=%d", len(bindings.accessories), len(bindings.faults))
	}
	before := runtime.NumGoroutine()
	var pushes uint64
	for round := 0; round < 20; round++ {
		for _, item := range items {
			pushes += bindings.update(item)
		}
	}
	if pushes < 4000 {
		t.Fatalf("burst pushes = %d", pushes)
	}
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Fatalf("goroutines grew during burst: %d -> %d", before, after)
	}
}

type incompleteSwitchProvider struct {
	inner *virtual.Provider
}

func (p *incompleteSwitchProvider) Manifest() providersdk.Manifest { return p.inner.Manifest() }
func (p *incompleteSwitchProvider) Capabilities() providersdk.Capabilities {
	return p.inner.Capabilities()
}
func (p *incompleteSwitchProvider) Initialize(ctx context.Context) error {
	return p.inner.Initialize(ctx)
}
func (p *incompleteSwitchProvider) Close(ctx context.Context) error { return p.inner.Close(ctx) }
func (p *incompleteSwitchProvider) DiscoverDevices(ctx context.Context) ([]device.Device, error) {
	items, err := p.inner.DiscoverDevices(ctx)
	if err != nil {
		return nil, err
	}
	for index := range items {
		if items[index].ID != "broken-switch" {
			continue
		}
		items[index].Endpoints = []device.Endpoint{{
			ID: "main", Name: "主端点", Type: "switch",
			Capabilities: []device.Capability{{ID: "switch", Type: "switch", Properties: []device.Property{}}},
		}}
	}
	return items, nil
}

func TestTargetReportsProjectionIssuesForIncompleteDevices(t *testing.T) {
	inner, err := virtual.NewProviderFromConfig(providerconfig.Config{ID: "broken-bridge", Config: []byte(`{"devices":[{"id":"ok-switch","type":"switch"},{"id":"broken-switch","type":"switch"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewDeviceService(&incompleteSwitchProvider{inner: inner})
	defer service.Close()
	logger := zap.NewNop()
	target, err := New(context.Background(), Config{
		ID: "bridge", Name: "Bridge", Address: "127.0.0.1:0", Pin: "12345678", SetupID: "TEST",
		StorePath: t.TempDir(), DeviceIDs: []string{"ok-switch", "broken-switch"},
	}, service, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	issues := target.Issues()
	if len(issues) != 1 {
		t.Fatalf("issues = %#v", issues)
	}
	if issues[0].DeviceID != "broken-switch" || issues[0].Stage != "consumer-contract" || !strings.Contains(issues[0].Message, "requires parameter") {
		t.Fatalf("issue = %#v", issues[0])
	}
	if target.PublishedAccessoryCount() != 1 {
		t.Fatalf("published accessories = %d", target.PublishedAccessoryCount())
	}
}
