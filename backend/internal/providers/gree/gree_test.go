package gree

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

const (
	testMAC       = "aabbccddeeff"
	testDeviceKey = "0123456789abcdef"
)

type scriptedTransport struct {
	mu          sync.Mutex
	calls       [][]byte
	handler     func([]byte) ([]byte, error)
	hostHandler func(string, []byte) ([]byte, error)
}

func (t *scriptedTransport) Exchange(_ context.Context, host string, _ int, payload []byte) ([]byte, error) {
	t.mu.Lock()
	t.calls = append(t.calls, append([]byte(nil), payload...))
	handler := t.handler
	hostHandler := t.hostHandler
	t.mu.Unlock()
	if hostHandler != nil {
		return hostHandler(host, payload)
	}
	if handler == nil {
		return nil, errors.New("scripted transport has no handler")
	}
	return handler(payload)
}

func (t *scriptedTransport) callCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.calls)
}

func testStatusResponse(payload []byte) ([]byte, error) {
	var envelope greeEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, err
	}
	inner, err := decryptPack([]byte(testDeviceKey), envelope.Pack)
	if err != nil {
		return nil, err
	}
	var request map[string]any
	if err := json.Unmarshal(inner, &request); err != nil {
		return nil, err
	}
	if request["t"] != "status" {
		return nil, fmt.Errorf("unexpected Gree request %#v", request)
	}
	values := make([]any, len(statusColumns))
	for index, column := range statusColumns {
		switch column {
		case "Pow":
			values[index] = 1
		case "Mod":
			values[index] = 1
		case "SetTem":
			values[index] = 25
		case "WdSpd":
			values[index] = 3
		case "SwhSlp":
			values[index] = 1
		case "SwUpDn":
			values[index] = 4
		case "SwingLfRig":
			values[index] = 1
		case "TemSen":
			values[index] = 65
		case "DwatSen":
			values[index] = 46
		case "OutEnvTem":
			values[index] = 55
		case "ErrCode":
			values[index] = ""
		default:
			values[index] = 0
		}
	}
	return makeEnvelope([]byte(testDeviceKey), 0, testMAC, 0, map[string]any{"t": "dat", "r": 200, "cols": statusColumns, "dat": values})
}

func newGreeTestConfig(t *testing.T, devices ...DeviceConfig) providerconfig.Config {
	t.Helper()
	encoded, err := json.Marshal(Config{
		Devices:               devices,
		RequestTimeoutSeconds: 1,
		PollIntervalSeconds:   3600,
	})
	if err != nil {
		t.Fatal(err)
	}
	return providerconfig.Config{ID: "gree-main", Type: ProviderType, Name: "Gree", Config: encoded}
}

func TestV1PacketsRoundTripBindingStatusAndCommand(t *testing.T) {
	bindPacket, err := buildBindPacket(testMAC)
	if err != nil {
		t.Fatal(err)
	}
	bind, err := decodeEnvelope(bindPacket, []byte(genericGreeDeviceKey))
	if err != nil {
		t.Fatal(err)
	}
	if bind["t"] != "bind" || bind["mac"] != testMAC {
		t.Fatalf("bind payload = %#v", bind)
	}

	bindResponse, err := makeEnvelope([]byte(genericGreeDeviceKey), 1, testMAC, 0, map[string]any{
		"t": "bindok", "mac": testMAC, "key": testDeviceKey, "r": 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	key, err := parseBindResponse(bindResponse)
	if err != nil || string(key) != testDeviceKey {
		t.Fatalf("bound key = %q, error = %v", key, err)
	}

	statusPacket, err := buildStatusPacket(key, testMAC, testMAC, 7)
	if err != nil {
		t.Fatal(err)
	}
	statusRequest, err := decodeEnvelope(statusPacket, key)
	if err != nil {
		t.Fatal(err)
	}
	if statusRequest["t"] != "status" || statusRequest["mac"] != testMAC {
		t.Fatalf("status payload = %#v", statusRequest)
	}
	columns, ok := statusRequest["cols"].([]any)
	if !ok || len(columns) < 18 {
		t.Fatalf("status columns = %#v", statusRequest["cols"])
	}

	statusResponse, err := makeEnvelope(key, 0, testMAC, 7, map[string]any{
		"t": "dat", "r": 200, "cols": []string{"Pow", "Mod"}, "dat": []int{1, 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	values, err := parseStatusResponse(statusResponse, key)
	if err != nil || rawInt(values, "Pow", 0) != 1 || rawInt(values, "Mod", 0) != 1 {
		t.Fatalf("status values = %#v, error = %v", values, err)
	}

	commandPacket, err := buildCommandPacket(key, testMAC, testMAC, 7, []string{"Pow", "Mod"}, []any{1, 4})
	if err != nil {
		t.Fatal(err)
	}
	command, err := decodeEnvelope(commandPacket, key)
	if err != nil {
		t.Fatal(err)
	}
	if command["t"] != "cmd" || command["sub"] != testMAC {
		t.Fatalf("command payload = %#v", command)
	}
}

func TestV2PacketsRoundTripBindingStatusAndCommand(t *testing.T) {
	bindPacket, err := buildBindPacket(testMAC, 2)
	if err != nil {
		t.Fatal(err)
	}
	repeatedBindPacket, err := buildBindPacket(testMAC, 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(bindPacket) != string(repeatedBindPacket) {
		t.Fatalf("v2 bind packet is not deterministic:\n%s\n%s", bindPacket, repeatedBindPacket)
	}
	var bindEnvelope greeEnvelope
	if err := json.Unmarshal(bindPacket, &bindEnvelope); err != nil {
		t.Fatal(err)
	}
	if bindEnvelope.Tag == "" {
		t.Fatal("v2 bind packet has no authentication tag")
	}
	bind, err := decodeEnvelope(bindPacket, []byte(genericGreeDeviceKeyGCM), 2)
	if err != nil {
		t.Fatal(err)
	}
	if bind["cid"] != testMAC || bind["mac"] != testMAC || bind["t"] != "bind" {
		t.Fatalf("v2 bind payload = %#v", bind)
	}
	if uid, ok := numberFromAny(bind["uid"]); !ok || uid != 0 {
		t.Fatalf("v2 bind uid = %#v", bind["uid"])
	}

	bindResponse, err := makeEnvelope([]byte(genericGreeDeviceKeyGCM), 1, testMAC, 0, map[string]any{
		"t": "bindok", "mac": testMAC, "key": testDeviceKey, "r": 200,
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	key, err := parseBindResponse(bindResponse, 2)
	if err != nil || string(key) != testDeviceKey {
		t.Fatalf("v2 bound key = %q, error = %v", key, err)
	}

	statusPacket, err := buildStatusPacket(key, testMAC, testMAC, 7, 2)
	if err != nil {
		t.Fatal(err)
	}
	repeatedStatusPacket, err := buildStatusPacket(key, testMAC, testMAC, 7, 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(statusPacket) != string(repeatedStatusPacket) {
		t.Fatal("v2 status packet is not deterministic")
	}
	statusRequest, err := decodeEnvelope(statusPacket, key, 2)
	if err != nil {
		t.Fatal(err)
	}
	if statusRequest["t"] != "status" || statusRequest["mac"] != testMAC {
		t.Fatalf("v2 status payload = %#v", statusRequest)
	}
	statusResponse, err := makeEnvelope(key, 0, testMAC, 7, map[string]any{
		"t": "dat", "r": 200, "cols": []string{"Pow", "Mod"}, "dat": []int{1, 1},
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	values, err := parseStatusResponse(statusResponse, key, 2)
	if err != nil || rawInt(values, "Pow", 0) != 1 || rawInt(values, "Mod", 0) != 1 {
		t.Fatalf("v2 status values = %#v, error = %v", values, err)
	}

	commandPacket, err := buildCommandPacket(key, testMAC, testMAC, 7, []string{"Pow", "Mod"}, []any{1, 4}, 2)
	if err != nil {
		t.Fatal(err)
	}
	repeatedCommandPacket, err := buildCommandPacket(key, testMAC, testMAC, 7, []string{"Pow", "Mod"}, []any{1, 4}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(commandPacket) != string(repeatedCommandPacket) {
		t.Fatal("v2 command packet is not deterministic")
	}
	command, err := decodeEnvelope(commandPacket, key, 2)
	if err != nil {
		t.Fatal(err)
	}
	if command["t"] != "cmd" || command["sub"] != testMAC {
		t.Fatalf("v2 command payload = %#v", command)
	}
}

func TestV2EnvelopeRejectsTamperedTag(t *testing.T) {
	packet, err := buildStatusPacket([]byte(testDeviceKey), testMAC, testMAC, 7, 2)
	if err != nil {
		t.Fatal(err)
	}
	var envelope greeEnvelope
	if err := json.Unmarshal(packet, &envelope); err != nil {
		t.Fatal(err)
	}
	tag, err := base64.StdEncoding.DecodeString(envelope.Tag)
	if err != nil {
		t.Fatal(err)
	}
	tag[0] ^= 0x01
	envelope.Tag = base64.StdEncoding.EncodeToString(tag)
	tampered, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeEnvelope(tampered, []byte(testDeviceKey), 2); err == nil {
		t.Fatal("v2 envelope with a tampered tag was accepted")
	}
}

func TestConfigAcceptsV2AndRejectsOtherEncryptionVersions(t *testing.T) {
	provider, err := NewProviderFromConfig(providerconfig.Config{
		ID: "gree-main", Name: "Gree", Config: json.RawMessage(`{
			"devices":[{"id":"living-ac","host":"192.0.2.10","mac":"aabbccddeeff","encryptionKey":"0123456789abcdef","encryptionVersion":2}]
		}`),
	})
	if err != nil {
		t.Fatalf("v2 configuration rejected: %v", err)
	}
	if provider.config.Devices[0].EncryptionVersion != 2 {
		t.Fatalf("normalized v2 configuration = %#v", provider.config.Devices[0])
	}
	_, err = NewProviderFromConfig(providerconfig.Config{
		ID: "gree-main", Name: "Gree", Config: json.RawMessage(`{
			"devices":[{"id":"living-ac","host":"192.0.2.10","mac":"aabbccddeeff","encryptionVersion":3}]
		}`),
	})
	if err == nil || !strings.Contains(err.Error(), "1 or 2") {
		t.Fatalf("unsupported encryption version error = %v", err)
	}
	_, err = NewProviderFromConfig(providerconfig.Config{
		ID: "gree-main", Name: "Gree", Config: json.RawMessage(`{
			"devices":[{"id":"living-ac","host":"192.0.2.10","mac":"aabbccddeeff","encryptionKey":"short","encryptionVersion":2}]
		}`),
	})
	if err == nil {
		t.Fatal("invalid v2 encryption key was accepted")
	}
}

func TestConfigAcceptsReferenceRuntimeOptions(t *testing.T) {
	provider, err := NewProviderFromConfig(providerconfig.Config{
		ID: "gree-main", Name: "Gree", Config: json.RawMessage(`{
			"devices":[{"id":"living-ac","host":"192.0.2.10","mac":"aabbccddeeff","target_temperature_step":0.5,"temp_sensor_offset":true,"disable_available_check":true,"auto_xfan":true,"auto_light":true}]
		}`),
	})
	if err != nil {
		t.Fatalf("reference runtime options rejected: %v", err)
	}
	item := provider.config.Devices[0]
	if item.TargetTemperatureStep != 0.5 || item.TempSensorOffset == nil || !*item.TempSensorOffset || !item.DisableAvailableCheck || !item.AutoXFan || !item.AutoLight {
		t.Fatalf("normalized reference runtime options = %#v", item)
	}
}

func TestProviderBindsPollsMapsAirConditionerV3AndWrites(t *testing.T) {
	transport := &scriptedTransport{}
	transport.handler = func(payload []byte) ([]byte, error) {
		var envelope greeEnvelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return nil, err
		}
		var request map[string]any
		inner, err := decryptPack([]byte(genericGreeDeviceKey), envelope.Pack)
		if err == nil {
			err = json.Unmarshal(inner, &request)
		}
		if err != nil {
			inner, err = decryptPack([]byte(testDeviceKey), envelope.Pack)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(inner, &request); err != nil {
				return nil, err
			}
		}
		switch request["t"] {
		case "bind":
			return makeEnvelope([]byte(genericGreeDeviceKey), 1, testMAC, 0, map[string]any{
				"t": "bindok", "key": testDeviceKey, "r": 200,
			})
		case "status":
			return makeEnvelope([]byte(testDeviceKey), 0, testMAC, 0, map[string]any{
				"t": "dat", "r": 200, "cols": statusColumns, "dat": []any{
					1, 1, 25, 3, 0, 0, 0, 1, 0, 4, 1, 0, 0, 0, 0, 0, 0, 1, 1, 65, 46, 55, 0, 0, "E3",
				},
			})
		case "cmd":
			return makeEnvelope([]byte(testDeviceKey), 0, testMAC, 0, map[string]any{
				"t": "res", "r": 200, "opt": request["opt"], "p": request["p"],
			})
		default:
			return nil, fmt.Errorf("unexpected Gree request %#v", request)
		}
	}

	provider, err := NewProviderWithTransport(providerconfig.Config{
		ID: "gree-main", Type: ProviderType, Name: "Gree", Config: json.RawMessage(`{
			"requestTimeoutSeconds": 1,
			"devices": [{"id":"living-ac","name":"Living AC","host":"192.0.2.10","mac":"AA:BB:CC:DD:EE:FF","uid":"7"}]
		}`),
	}, transport)
	if err != nil {
		t.Fatal(err)
	}
	if provider.config.Devices[0].Port != defaultPort || provider.config.Devices[0].UID != 7 {
		t.Fatalf("normalized config = %#v", provider.config.Devices[0])
	}

	events := make(chan device.Device, 4)
	unsub := provider.Subscribe(func(item device.Device) { events <- item })
	defer unsub()
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	devices, err := provider.DiscoverDevices(context.Background())
	if err != nil || len(devices) != 1 {
		t.Fatalf("DiscoverDevices() = %#v, %v", devices, err)
	}
	item := devices[0]
	if !item.IsOnline() || item.Type != device.TypeAirConditioner {
		t.Fatalf("device availability/type = %#v, diagnostics=%#v", item, provider.ProviderDiagnostics())
	}
	if err := item.Validate(); err != nil {
		t.Fatalf("Air Conditioner v3 device validation = %v", err)
	}
	assertNumberProperty(t, item, "temperature", "current-temperature", 25)
	assertNumberProperty(t, item, "temperature", "target-temperature", 25)
	assertNumberProperty(t, item, "humidity", "current-humidity", 46)
	assertEnumProperty(t, item, "air-conditioner", "target-mode", "cool")
	assertEnumProperty(t, item, "air-conditioner", "current-state", "cooling")
	assertEnumProperty(t, item, "air-conditioner", "fan-speed", "medium")
	assertEnumProperty(t, item, "air-conditioner", "vertical-swing-mode", "swing_full")
	assertEnumProperty(t, item, "air-conditioner", "horizontal-swing-mode", "fixed_middle")
	assertEnumProperty(t, item, "air-conditioner", "fault", "E3")
	assertBoolProperty(t, item, "air-conditioner", "active", true)
	assertBoolProperty(t, item, "air-conditioner", "vertical-swing", true)
	assertBoolProperty(t, item, "air-conditioner", "horizontal-swing", true)
	assertBoolProperty(t, item, "air-conditioner", "sleep-mode", true)
	assertBoolProperty(t, item, "air-conditioner", "eco-mode", true)
	assertBoolProperty(t, item, "air-conditioner", "display-enabled", false)
	assertBoolProperty(t, item, "air-conditioner", "x-fan", false)
	assertBoolProperty(t, item, "air-conditioner", "health", false)
	assertBoolProperty(t, item, "air-conditioner", "air", false)
	assertBoolProperty(t, item, "air-conditioner", "beeper", true)
	assertNumberProperty(t, item, "temperature", "outside-temperature", 15)
	assertNumberProperty(t, item, "air-conditioner", "target-temperature-step", 1)
	if len(events) != 1 {
		t.Fatalf("initial event count = %d", len(events))
	}

	updated, err := provider.WriteProperty(context.Background(), providersdk.PropertyWriteRequest{
		DeviceID: "living-ac", EndpointID: "main", CapabilityID: "air-conditioner", PropertyID: "sleep-mode", Value: device.BoolValue(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertBoolProperty(t, updated, "air-conditioner", "sleep-mode", false)
	if len(events) != 2 {
		t.Fatalf("write event count = %d", len(events))
	}

	read, err := provider.ReadProperty(context.Background(), providersdk.PropertyReadRequest{
		DeviceID: "living-ac", EndpointID: "main", CapabilityID: "air-conditioner", PropertyID: "display-enabled",
	})
	if err != nil || read.Value.Bool == nil || *read.Value.Bool {
		t.Fatalf("ReadProperty() = %#v, %v", read, err)
	}
	if transport.callCount() != 4 { // bind + initial status + command + explicit read
		t.Fatalf("transport calls = %d", transport.callCount())
	}
	transport.mu.Lock()
	commandPayload := append([]byte(nil), transport.calls[2]...)
	transport.mu.Unlock()
	command, err := decodeEnvelope(commandPayload, []byte(testDeviceKey))
	options, optionsOK := command["opt"].([]any)
	values, valuesOK := command["p"].([]any)
	var firstValue float64
	firstValueOK := false
	if len(values) > 0 {
		firstValue, firstValueOK = numberFromAny(values[0])
	}
	if err != nil || !optionsOK || !valuesOK || len(options) != 2 || len(values) != 2 || options[0] != "SwhSlp" || options[1] != "SlpMod" || !firstValueOK || firstValue != 0 {
		t.Fatalf("sleep command = %#v, error = %v", command, err)
	}
	metrics := provider.ProviderMetrics()
	if metrics["binds"] != 1 || metrics["polls"] != 2 || metrics["writes"] != 1 || metrics["onlineDevices"] != 1 {
		t.Fatalf("metrics = %#v", metrics)
	}
	diagnostics := provider.ProviderDiagnostics()
	if diagnostics["state"] != "running" || diagnostics["lastError.living-ac"] != "" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if err := provider.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestProviderConnectionTestAcceptsAnyReachableConfiguredDevice(t *testing.T) {
	transport := &scriptedTransport{hostHandler: func(host string, payload []byte) ([]byte, error) {
		if host == "192.0.2.10" {
			return nil, errors.New("offline device did not answer")
		}
		return testStatusResponse(payload)
	}}
	provider, err := NewProviderWithTransport(newGreeTestConfig(t,
		DeviceConfig{ID: "offline", Name: "Offline", Host: "192.0.2.10", MAC: testMAC, EncryptionKey: testDeviceKey},
		DeviceConfig{ID: "online", Name: "Online", Host: "192.0.2.11", MAC: testMAC, EncryptionKey: testDeviceKey},
	), transport)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _ = provider.Close(ctx) })
	if err := provider.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := provider.TestConnection(ctx); err != nil {
		t.Fatalf("TestConnection() error = %v", err)
	}
	if transport.callCount() != 4 {
		t.Fatalf("transport calls = %d, want initial refresh plus connection-test refresh for both devices", transport.callCount())
	}
	devices, err := provider.DiscoverDevices(ctx)
	if err != nil || len(devices) != 2 {
		t.Fatalf("DiscoverDevices() = %#v, %v", devices, err)
	}
	for _, item := range devices {
		wantOnline := item.ID == "online"
		if item.IsOnline() != wantOnline {
			t.Fatalf("device %q online = %v, want %v", item.ID, item.IsOnline(), wantOnline)
		}
	}
}

func TestProviderConnectionTestFailsWhenAllConfiguredDevicesAreUnavailable(t *testing.T) {
	unreachable := errors.New("device did not answer")
	transport := &scriptedTransport{handler: func([]byte) ([]byte, error) {
		return nil, unreachable
	}}
	provider, err := NewProviderWithTransport(newGreeTestConfig(t,
		DeviceConfig{ID: "offline-a", Name: "Offline A", Host: "192.0.2.10", MAC: testMAC, EncryptionKey: testDeviceKey},
		DeviceConfig{ID: "offline-b", Name: "Offline B", Host: "192.0.2.11", MAC: testMAC, EncryptionKey: testDeviceKey},
	), transport)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _ = provider.Close(ctx) })
	if err := provider.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() must remain recoverable offline, got %v", err)
	}
	if err := provider.TestConnection(ctx); err == nil || !errors.Is(err, unreachable) {
		t.Fatalf("TestConnection() error = %v, want unreachable-device error", err)
	}
	if transport.callCount() != 4 {
		t.Fatalf("transport calls = %d, want both devices checked during Initialize and TestConnection", transport.callCount())
	}
	devices, err := provider.DiscoverDevices(ctx)
	if err != nil || len(devices) != 2 {
		t.Fatalf("offline catalog = %#v, %v", devices, err)
	}
}

func TestProviderRejectsInvalidConfigAndUnavailableReads(t *testing.T) {
	_, err := NewProviderFromConfig(providerconfig.Config{ID: "gree-main", Name: "Gree", Config: json.RawMessage(`{
		"devices":[{"id":"bad","host":"192.0.2.10","mac":"not-a-mac","encryptionKey":"short"}]
	}`)})
	if err == nil {
		t.Fatal("invalid MAC was accepted")
	}

	provider, err := NewProviderWithTransport(providerconfig.Config{ID: "gree-main", Name: "Gree", Config: json.RawMessage(`{
		"devices":[{"id":"offline","host":"192.0.2.10","mac":"aabbccddeeff","encryptionKey":"0123456789abcdef"}]
	}`)}, &scriptedTransport{handler: func([]byte) ([]byte, error) {
		return nil, errors.New("device did not answer")
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.ReadProperty(context.Background(), providersdk.PropertyReadRequest{DeviceID: "offline", EndpointID: "main", CapabilityID: "air-conditioner", PropertyID: "active"})
	if err == nil || !errors.Is(err, providersdk.ErrProviderUnavailable) {
		t.Fatalf("unavailable read error = %v", err)
	}
	if diagnostics := provider.ProviderDiagnostics(); diagnostics["lastError.offline"] == "" {
		t.Fatalf("diagnostics did not record failure: %#v", diagnostics)
	}
}

func assertNumberProperty(t *testing.T, item device.Device, capability, propertyID string, want float64) {
	t.Helper()
	property, ok := item.Property("main", capability, propertyID)
	if !ok || property.Value.Number == nil || *property.Value.Number != want {
		var got any
		if property.Value.Number != nil {
			got = *property.Value.Number
		}
		t.Fatalf("%s/%s = %v (%#v), want %v", capability, propertyID, got, property, want)
	}
}

func assertEnumProperty(t *testing.T, item device.Device, capability, propertyID, want string) {
	t.Helper()
	property, ok := item.Property("main", capability, propertyID)
	if !ok || property.Value.String == nil || *property.Value.String != want {
		t.Fatalf("%s/%s = %#v, want %q", capability, propertyID, property, want)
	}
}

func assertBoolProperty(t *testing.T, item device.Device, capability, propertyID string, want bool) {
	t.Helper()
	property, ok := item.Property("main", capability, propertyID)
	if !ok || property.Value.Bool == nil || *property.Value.Bool != want {
		t.Fatalf("%s/%s = %#v, want %v", capability, propertyID, property, want)
	}
}

func TestTemperatureEncodingAndStatusObjectCompatibility(t *testing.T) {
	if setTem, temRec := encodeTargetTemperature(24.5); setTem != 24 || temRec != 1 {
		t.Fatalf("encoded Celsius temperature = %d/%d", setTem, temRec)
	}
	inner := map[string]any{"t": "dat", "r": 200, "dat": map[string]any{"Pow": 1, "SetTem": 26}}
	values, err := statusValues(inner)
	if err != nil || rawInt(values, "Pow", 0) != 1 || rawInt(values, "SetTem", 0) != 26 {
		t.Fatalf("object status values = %#v, %v", values, err)
	}
	if _, err := statusValues(map[string]any{"t": "dat", "r": 200}); err == nil {
		t.Fatal("empty status response was accepted")
	}
	if _, err := decodeEnvelope([]byte(`{"pack":"not-base64"}`), []byte(genericGreeDeviceKey)); err == nil {
		t.Fatal("invalid encrypted response was accepted")
	}
}

func TestGreeReferenceOptionsAndOptionalSensors(t *testing.T) {
	options, values, updates, err := commandFor("fan-speed", device.EnumValue("medium_low"), nil)
	if err != nil || len(options) != 3 || options[0] != "WdSpd" || updates["WdSpd"] != 2 {
		t.Fatalf("medium-low fan command = %#v %#v %#v, error=%v", options, values, updates, err)
	}
	options, values, updates, err = commandFor("vertical-swing-mode", device.EnumValue("swing_upmost"), nil)
	if err != nil || len(options) != 1 || options[0] != "SwUpDn" || values[0] != 11 || updates["SwUpDn"] != 11 {
		t.Fatalf("vertical swing command = %#v %#v %#v, error=%v", options, values, updates, err)
	}
	options, values, updates, err = commandFor("light-sensor", device.BoolValue(false), nil)
	if err != nil || len(options) != 1 || options[0] != "LigSen" || values[0] != 1 || updates["LigSen"] != 1 {
		t.Fatalf("light sensor command = %#v %#v %#v, error=%v", options, values, updates, err)
	}
	options, values, updates, err = commandFor("beeper", device.BoolValue(false), nil)
	if err != nil || len(options) != 2 || options[0] != "Buzzer_ON_OFF" || values[0] != 1 || updates["BuzzerCtrl"] != 0 {
		t.Fatalf("beeper command = %#v %#v %#v, error=%v", options, values, updates, err)
	}
	configured := DeviceConfig{ID: "gree-empty", Name: "Gree", Host: "192.0.2.10", MAC: testMAC, AutoXFan: true, AutoLight: true}
	item, err := buildAirConditionerDevice("gree-main", configured, defaultRawState(configured), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := item.Property("main", "temperature", "current-temperature"); found {
		t.Fatal("current temperature should be absent before the optional sensor is observed")
	}
	if _, found := item.Property("main", "humidity", "current-humidity"); found {
		t.Fatal("humidity should be absent before the optional sensor is observed")
	}
	assertBoolProperty(t, item, "air-conditioner", "auto-x-fan", true)
	assertBoolProperty(t, item, "air-conditioner", "auto-light", true)
}
