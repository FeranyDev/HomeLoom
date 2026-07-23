package matter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	domaintarget "github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

type memoryStorage struct {
	next       uint16
	endpoints  map[string]domaintarget.MatterEndpointIdentity
	values     map[string][]byte
	tombstoned []string
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{next: 2, endpoints: map[string]domaintarget.MatterEndpointIdentity{}, values: map[string][]byte{}}
}

func (s *memoryStorage) AllocateEndpoint(_ context.Context, targetID, consumerDeviceID string, deviceType device.Type) (uint16, error) {
	if current, found := s.endpoints[consumerDeviceID]; found {
		return current.EndpointID, nil
	}
	id := s.next
	s.next++
	s.endpoints[consumerDeviceID] = domaintarget.MatterEndpointIdentity{
		TargetID: targetID, ConsumerDeviceID: consumerDeviceID, EndpointID: id, DeviceType: deviceType,
	}
	return id, nil
}

func (s *memoryStorage) TombstoneEndpoint(_ context.Context, _ string, id string) error {
	current := s.endpoints[id]
	current.Tombstone = true
	s.endpoints[id] = current
	s.tombstoned = append(s.tombstoned, id)
	return nil
}

func (s *memoryStorage) Endpoints(context.Context, string) ([]domaintarget.MatterEndpointIdentity, error) {
	result := make([]domaintarget.MatterEndpointIdentity, 0, len(s.endpoints))
	for _, current := range s.endpoints {
		result = append(result, current)
	}
	return result, nil
}

func (s *memoryStorage) Put(_ context.Context, _, key string, value []byte) error {
	s.values[key] = append([]byte(nil), value...)
	return nil
}
func (s *memoryStorage) Get(_ context.Context, _, key string) ([]byte, bool, error) {
	value, found := s.values[key]
	return append([]byte(nil), value...), found, nil
}
func (s *memoryStorage) List(context.Context, string) ([]domaintarget.MatterRuntimeValue, error) {
	result := make([]domaintarget.MatterRuntimeValue, 0, len(s.values))
	for key, value := range s.values {
		result = append(result, domaintarget.MatterRuntimeValue{Key: key, Value: append([]byte(nil), value...)})
	}
	return result, nil
}
func (s *memoryStorage) Delete(_ context.Context, _, key string) error {
	delete(s.values, key)
	return nil
}
func (s *memoryStorage) Clear(context.Context, string) error {
	s.values = map[string][]byte{}
	return nil
}

func newTestTarget(t *testing.T, virtuals []domaintarget.VirtualDevice) (*Target, *memoryStorage) {
	t.Helper()
	provider := virtual.NewProvider()
	devices := application.NewDeviceService(provider)
	t.Cleanup(func() { _ = devices.Close() })
	discriminator := uint16(1234)
	storage := newMemoryStorage()
	created, err := New(Config{
		ID: "matter-test", Name: "Matter Test", Devices: virtuals,
		Matter: domaintarget.MatterConfig{
			Discriminator: &discriminator, Passcode: "20202021",
			VendorID: 0xfff1, ProductID: 0x8000, ProductName: "HomeLoom", SerialNumber: "matter-test",
			CommissioningWindowSeconds: 900,
		},
	}, devices, storage, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return created, storage
}

func TestDeviceSnapshotsUseStableEndpointAndMatterAttributes(t *testing.T) {
	target, storage := newTestTarget(t, []domaintarget.VirtualDevice{{
		ID: "living-switch", Name: "Living Switch", Type: device.TypeSwitch,
		SourceDeviceID: "virtual-switch-1", Enabled: true,
	}})
	first, err := target.buildDeviceSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := target.buildDeviceSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].EndpointID != 2 || second[0].EndpointID != first[0].EndpointID {
		t.Fatalf("snapshots = %#v, %#v", first, second)
	}
	if first[0].DeviceType != "switch" || !first[0].Reachable || first[0].Attributes["OnOff.OnOff"] != false {
		t.Fatalf("Matter switch snapshot = %#v", first[0])
	}
	if storage.next != 3 {
		t.Fatalf("endpoint allocator advanced on replay: %d", storage.next)
	}
}

func TestBuildsOneHundredStableEndpoints(t *testing.T) {
	virtuals := make([]domaintarget.VirtualDevice, 100)
	for index := range virtuals {
		virtuals[index] = domaintarget.VirtualDevice{
			ID:             fmt.Sprintf("switch-%03d", index),
			Name:           fmt.Sprintf("Switch %03d", index),
			Type:           device.TypeSwitch,
			SourceDeviceID: "virtual-switch-1",
			Enabled:        true,
		}
	}
	target, storage := newTestTarget(t, virtuals)
	first, err := target.buildDeviceSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := target.buildDeviceSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 100 || len(second) != 100 {
		t.Fatalf("snapshot counts = %d, %d", len(first), len(second))
	}
	for index := range first {
		want := uint16(index + 2)
		if first[index].EndpointID != want || second[index].EndpointID != want {
			t.Fatalf("endpoint %d = %d, %d; want %d", index, first[index].EndpointID, second[index].EndpointID, want)
		}
	}
	if storage.next != 102 {
		t.Fatalf("endpoint allocator advanced on replay: %d", storage.next)
	}
}

func TestDeviceSnapshotDiffUsesBoundedIncrementalUpdates(t *testing.T) {
	previous := []deviceSnapshot{{
		ID: "switch", EndpointID: 2, DeviceType: "switch", Name: "Switch",
		Reachable: true, Attributes: map[string]any{"OnOff.OnOff": false},
	}}
	current := cloneDeviceSnapshots(previous)
	current[0].Reachable = false
	current[0].Attributes["OnOff.OnOff"] = true
	replace, attributes, availability := diffDeviceSnapshots(previous, current)
	if replace {
		t.Fatal("attribute-only change requested a full device replacement")
	}
	if len(attributes) != 1 || attributes[0].DeviceID != "switch" || attributes[0].Path != "OnOff.OnOff" || attributes[0].Value != true {
		t.Fatalf("attribute updates = %#v", attributes)
	}
	if len(availability) != 1 || availability[0].DeviceID != "switch" || availability[0].Reachable {
		t.Fatalf("availability updates = %#v", availability)
	}
	if previous[0].Attributes["OnOff.OnOff"] != false {
		t.Fatal("snapshot clone mutated the replay baseline")
	}
}

func TestDeviceSnapshotDiffReplacesStructuralChanges(t *testing.T) {
	previous := []deviceSnapshot{{
		ID: "switch", EndpointID: 2, DeviceType: "switch", Name: "Switch",
		Reachable: true, Attributes: map[string]any{"OnOff.OnOff": false},
	}}
	for name, current := range map[string][]deviceSnapshot{
		"added": append(cloneDeviceSnapshots(previous), deviceSnapshot{
			ID: "second", EndpointID: 3, DeviceType: "switch", Name: "Second",
		}),
		"renamed": {{
			ID: "switch", EndpointID: 2, DeviceType: "switch", Name: "Renamed",
			Reachable: true, Attributes: map[string]any{"OnOff.OnOff": false},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			replace, _, _ := diffDeviceSnapshots(previous, current)
			if !replace {
				t.Fatal("structural change did not request devices.replace")
			}
		})
	}
}

func TestRemovedVirtualDeviceBecomesEndpointTombstone(t *testing.T) {
	target, storage := newTestTarget(t, nil)
	storage.endpoints["removed"] = domaintarget.MatterEndpointIdentity{
		TargetID: "matter-test", ConsumerDeviceID: "removed", EndpointID: 7, DeviceType: device.TypeSwitch,
	}
	if _, err := target.buildDeviceSnapshots(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(storage.tombstoned) != 1 || storage.tombstoned[0] != "removed" || !storage.endpoints["removed"].Tombstone {
		t.Fatalf("tombstones = %#v", storage)
	}
}

func TestStorageRPCIsBoundToTargetNamespace(t *testing.T) {
	target, storage := newTestTarget(t, nil)
	_, err := target.handleRequest(context.Background(), "storage.put", []byte(`{"key":"fabric/1","valueBase64":"c2VjcmV0"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(storage.values["fabric/1"]) != "secret" {
		t.Fatalf("stored value = %q", storage.values["fabric/1"])
	}
	result, err := target.handleRequest(context.Background(), "storage.get", []byte(`{"key":"fabric/1"}`))
	if err != nil {
		t.Fatal(err)
	}
	response := result.(map[string]any)
	if response["found"] != true || response["valueBase64"] != "c2VjcmV0" {
		t.Fatalf("storage.get = %#v", response)
	}
}

func TestFactoryResetLeavesIdentityCleanupToRuntime(t *testing.T) {
	target, storage := newTestTarget(t, nil)
	storage.values["runtime/recreated-identity"] = []byte("new")
	clientConn, runtimeConn := net.Pipe()
	client := NewClient(clientConn, ClientOptions{DefaultTimeout: time.Second})
	target.setClient(client)
	t.Cleanup(func() {
		target.clearClient(client)
		_ = client.Close()
		_ = runtimeConn.Close()
	})
	go func() {
		reader := bufio.NewReader(runtimeConn)
		for _, expectedMethod := range []string{"identity.factoryReset", "runtime.status"} {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var request rpcMessage
			if json.Unmarshal(line, &request) != nil || request.Method != expectedMethod {
				return
			}
			result := json.RawMessage(`{}`)
			if expectedMethod == "runtime.status" {
				result = json.RawMessage(`{"fabricCount":0,"commissioningWindowOpen":true,"manualPairingCode":"123","qrPairingCode":"MT:TEST"}`)
			}
			response, _ := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: request.ID, Result: result})
			_, _ = runtimeConn.Write(append(response, '\n'))
		}
	}()
	if err := target.FactoryReset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if string(storage.values["runtime/recreated-identity"]) != "new" {
		t.Fatal("Go cleared the identity that the runtime recreated during factory reset")
	}
	if status := target.Status(); !status.WindowOpen || status.SetupPayload != "MT:TEST" {
		t.Fatalf("factory reset status = %#v", status)
	}
}

func TestFabricNotificationsMaintainSafeSummaries(t *testing.T) {
	target, _ := newTestTarget(t, nil)
	target.handleNotification("fabric.changed", json.RawMessage(`{"change":"added","fabricId":"1234","label":"Apple Home","fabricCount":1}`))
	status := target.Status()
	if status.FabricCount != 1 || len(status.Fabrics) != 1 || status.Fabrics[0] != (FabricSummary{ID: "1234", Label: "Apple Home"}) {
		t.Fatalf("added Fabric status = %#v", status)
	}
	status.Fabrics[0].Label = "mutated"
	if target.Status().Fabrics[0].Label != "Apple Home" {
		t.Fatal("Status returned an aliased Fabric summary")
	}
	target.handleNotification("fabric.changed", json.RawMessage(`{"change":"removed","fabricId":"1234","fabricCount":0}`))
	if status := target.Status(); status.FabricCount != 0 || len(status.Fabrics) != 0 {
		t.Fatalf("removed Fabric status = %#v", status)
	}
}

func TestRemoveStaleSocketRefusesRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sock")
	if err := os.WriteFile(path, []byte("owned by user"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleSocket(path); err == nil {
		t.Fatal("regular file was accepted as a stale socket")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("regular file was removed: %v", err)
	}
}

func TestJSONPropertyValueRejectsTypeMismatch(t *testing.T) {
	if _, err := jsonPropertyValue("true", device.ValueTypeBool); err == nil {
		t.Fatal("string was accepted as bool")
	}
	value, err := jsonPropertyValue(true, device.ValueTypeBool)
	if err != nil || value.Bool == nil || !*value.Bool {
		t.Fatalf("bool conversion = %#v, %v", value, err)
	}
}

func TestCommandsWithReadOnlyMatterAttributesRouteToWritableModelProperties(t *testing.T) {
	tests := []struct {
		command      string
		capabilityID string
		propertyID   string
		value        any
	}{
		{"WindowCovering.StopMotion", "window-covering", "hold-position", true},
		{"DoorLock.LockDoor", "lock", "target-state", "secured"},
		{"DoorLock.UnlockDoor", "lock", "target-state", "unsecured"},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			path, value, found := directCommandModelWrite(test.command)
			if !found || path.EndpointID != "main" || path.CapabilityID != test.capabilityID || path.PropertyID != test.propertyID || value != test.value {
				t.Fatalf("direct command route = %#v, %#v, %v", path, value, found)
			}
		})
	}
}

func TestFanModeRoutesOffAndOperatingModesWithoutCorruptingEnum(t *testing.T) {
	off, err := fanModeModelWrites("off")
	if err != nil || len(off) != 1 || off[0].Path.PropertyID != "active" || off[0].Value != false {
		t.Fatalf("off writes = %#v, %v", off, err)
	}
	for _, mode := range []string{"manual", "auto"} {
		writes, err := fanModeModelWrites(mode)
		if err != nil || len(writes) != 2 {
			t.Fatalf("%s writes = %#v, %v", mode, writes, err)
		}
		if writes[0].Path.PropertyID != "active" || writes[0].Value != true ||
			writes[1].Path.PropertyID != "target-state" || writes[1].Value != mode {
			t.Fatalf("%s writes = %#v", mode, writes)
		}
	}
	if _, err := fanModeModelWrites("high"); err == nil {
		t.Fatal("un-normalized Matter FanMode was accepted")
	}
}

func TestTargetRequiresPersistentStorage(t *testing.T) {
	discriminator := uint16(1)
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	_, err := New(Config{ID: "matter", Matter: domaintarget.MatterConfig{Discriminator: &discriminator}}, devices, nil, nil)
	if err == nil {
		t.Fatal("target accepted missing storage")
	}
}

func TestRuntimeUDPPortKeepsConfiguredPort(t *testing.T) {
	target, _ := newTestTarget(t, nil)
	target.config.Matter.UDPPort = 5540
	first, err := target.runtimeUDPPort()
	if err != nil {
		t.Fatal(err)
	}
	second, err := target.runtimeUDPPort()
	if err != nil {
		t.Fatal(err)
	}
	if first != 5540 || second != first {
		t.Fatalf("runtime ports = %d, %d", first, second)
	}
}
