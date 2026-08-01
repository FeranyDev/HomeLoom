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
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	domaintarget "github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

type memoryStorage struct {
	next       uint16
	endpoints  map[string]domaintarget.MatterEndpointIdentity
	values     map[string][]byte
	tombstoned []string
}

type cameraMediaStub struct {
	streamID string
	request  CameraWebRTCRequest
	width    uint16
	height   uint16
}

func (s *cameraMediaStub) WebRTC(
	_ context.Context,
	streamID string,
	request CameraWebRTCRequest,
) (CameraWebRTCResponse, error) {
	s.streamID, s.request = streamID, request
	return CameraWebRTCResponse{SessionID: "media-session", SDP: "answer"}, nil
}

func (s *cameraMediaStub) Snapshot(_ context.Context, streamID string, width, height uint16) ([]byte, error) {
	s.streamID, s.width, s.height = streamID, width, height
	return []byte{0xff, 0xd8, 0xff, 0xd9}, nil
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

func TestCameraNodeUsesFixedEndpointAndMediaContractWithoutMappingAllocation(t *testing.T) {
	provider, err := virtual.NewProviderFromConfig(providerconfig.Config{
		ID: "virtual-main", Type: "virtual", Name: "Virtual", Enabled: true,
		Config: json.RawMessage(`{"devices":[{"id":"virtual-camera-1","name":"Camera","type":"camera","online":true}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	devices := application.NewDeviceService(provider)
	defer devices.Close()
	discriminator := uint16(1234)
	storage := newMemoryStorage()
	media := &cameraMediaStub{}
	target, err := New(Config{
		ID: "matter-camera", Name: "Camera", NodeKind: "camera",
		Matter: domaintarget.MatterConfig{
			Discriminator: &discriminator, Passcode: "20202021", VendorID: 0xfff1,
			ProductID: 0x8000, ProductName: "Camera", SerialNumber: "matter-camera",
			CommissioningWindowSeconds: 900, UDPPort: 5540,
		},
		Devices: []domaintarget.VirtualDevice{{
			ID: "camera", Name: "Camera", Type: device.TypeCamera,
			SourceDeviceID: "virtual-camera-1", Enabled: true,
		}},
		CameraMedia: media,
	}, devices, storage, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	state, err := target.replayState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Bridge.NodeKind != "camera" || len(state.Devices) != 1 || state.Devices[0].EndpointID != 1 ||
		state.Media == nil || state.Media.DeviceID != "virtual-camera-1" ||
		state.Media.StreamID != cameraStreamID("virtual-camera-1") {
		t.Fatalf("camera replay state = %#v", state)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	deviceDocuments := document["devices"].([]any)
	if document["media"] == nil || deviceDocuments[0].(map[string]any)["media"] != nil {
		t.Fatalf("camera media binding is not top-level: %s", raw)
	}
	if storage.next != 2 || len(storage.endpoints) != 0 {
		t.Fatalf("camera node used Matter bridge endpoint allocator: %#v", storage)
	}
	result, err := target.handleRequest(context.Background(), "camera.webrtc", json.RawMessage(
		`{"operation":"open","sdp":"offer"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if response := result.(CameraWebRTCResponse); response.SessionID != "media-session" ||
		media.streamID != cameraStreamID("virtual-camera-1") || media.request.SDP != "offer" {
		t.Fatalf("WebRTC relay = %#v, media = %#v", result, media)
	}
	snapshot, err := target.handleRequest(context.Background(), "camera.snapshot", json.RawMessage(
		`{"width":640,"height":360}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.(map[string]string)["jpegBase64"] != "/9j/2Q==" || media.width != 640 || media.height != 360 {
		t.Fatalf("snapshot relay = %#v, media = %#v", snapshot, media)
	}
}

func TestCameraNodeRejectsInvalidTopology(t *testing.T) {
	provider := virtual.NewProvider()
	devices := application.NewDeviceService(provider)
	defer devices.Close()
	discriminator := uint16(1234)
	_, err := New(Config{
		ID: "matter-camera", NodeKind: "camera",
		Matter: domaintarget.MatterConfig{Discriminator: &discriminator},
		Devices: []domaintarget.VirtualDevice{{
			ID: "switch", Type: device.TypeSwitch, SourceDeviceID: "virtual-switch-1", Enabled: true,
		}},
	}, devices, newMemoryStorage(), nil)
	if err == nil {
		t.Fatal("New() accepted a non-camera topology")
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

func TestOpenCommissioningWaitsForRuntimeReconnect(t *testing.T) {
	target, _ := newTestTarget(t, nil)
	closedClientConn, closedRuntimeConn := net.Pipe()
	closedClient := NewClient(closedClientConn, ClientOptions{DefaultTimeout: time.Second})
	_ = closedClient.Close()
	_ = closedRuntimeConn.Close()
	target.setClient(closedClient)
	t.Cleanup(func() {
		target.clearClient(closedClient)
		_ = closedClient.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- target.OpenCommissioningWindow(ctx, 600) }()
	select {
	case err := <-result:
		t.Fatalf("OpenCommissioningWindow() returned before reconnect: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	target.clearClient(closedClient)
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
		for _, expectedMethod := range []string{"commissioning.open", "runtime.status"} {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var request rpcMessage
			if json.Unmarshal(line, &request) != nil || request.Method != expectedMethod {
				return
			}
			responseResult := json.RawMessage(`{}`)
			if expectedMethod == "runtime.status" {
				responseResult = json.RawMessage(`{"fabricCount":0,"commissioningWindowOpen":true,"manualPairingCode":"349701123","qrPairingCode":"MT:TEST"}`)
			}
			response, _ := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: request.ID, Result: responseResult})
			_, _ = runtimeConn.Write(append(response, '\n'))
		}
	}()

	if err := <-result; err != nil {
		t.Fatalf("OpenCommissioningWindow() after reconnect = %v", err)
	}
	if status := target.Status(); !status.WindowOpen || status.ManualPairingCode != "349701123" {
		t.Fatalf("commissioning status = %#v", status)
	}
}

func TestRuntimeScriptPathFindsRepositorySiblingFromBackendDirectory(t *testing.T) {
	root := t.TempDir()
	backendDirectory := filepath.Join(root, "backend")
	runtimePath := filepath.Join(root, "matter-runtime", "dist", "src", "cli.js")
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, []byte("export {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(backendDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	previousOverride, hadOverride := os.LookupEnv("HOMELOOM_MATTER_RUNTIME")
	if err := os.Unsetenv("HOMELOOM_MATTER_RUNTIME"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(backendDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(previousDirectory)
		if hadOverride {
			_ = os.Setenv("HOMELOOM_MATTER_RUNTIME", previousOverride)
		} else {
			_ = os.Unsetenv("HOMELOOM_MATTER_RUNTIME")
		}
	})

	got := runtimeScriptPath()
	gotInfo, gotErr := os.Stat(got)
	wantInfo, wantErr := os.Stat(runtimePath)
	if gotErr != nil || wantErr != nil || !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("runtimeScriptPath() = %q, want file %q", got, runtimePath)
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
