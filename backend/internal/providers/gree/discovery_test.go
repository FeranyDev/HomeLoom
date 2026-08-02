package gree

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestGreeScanMessageIsTheProtocolJSON(t *testing.T) {
	if greeScanMessage != `{"t":"scan"}` || !json.Valid([]byte(greeScanMessage)) {
		t.Fatalf("scan message = %q", greeScanMessage)
	}
}

type scriptedDiscoveryTransport struct {
	datagrams []discoveryDatagram
	err       error
	timeout   time.Duration
}

func (t *scriptedDiscoveryTransport) Scan(_ context.Context, timeout time.Duration) ([]discoveryDatagram, error) {
	t.timeout = timeout
	if t.err != nil {
		return nil, t.err
	}
	return t.datagrams, nil
}

func discoveryResponse(t *testing.T, mac, name string, fields map[string]any) []byte {
	t.Helper()
	inner := map[string]any{"t": "dev", "mac": mac, "name": name}
	for key, value := range fields {
		inner[key] = value
	}
	packet, err := makeEnvelope([]byte(genericGreeDeviceKey), 0, mac, 0, inner)
	if err != nil {
		t.Fatal(err)
	}
	return packet
}

func TestGreeScanUsesScanMessageAndIgnoresBadPacketsAndDuplicates(t *testing.T) {
	transport := &scriptedDiscoveryTransport{datagrams: []discoveryDatagram{
		{host: "192.168.1.10", payload: []byte("not-json")},
		{host: "192.168.1.11", payload: discoveryResponse(t, "AA:BB:CC:DD:EE:FF", "客厅空调", map[string]any{"brand": "gree", "model": "X1", "ver": "1.2"})},
		{host: "192.168.1.12", payload: discoveryResponse(t, "aa-bb-cc-dd-ee-ff", "重复响应", nil)},
		{host: "192.168.1.13", payload: discoveryResponse(t, "invalid", "坏 MAC", nil)},
		{host: "192.168.1.14", payload: func() []byte {
			packet, err := makeEnvelope([]byte(genericGreeDeviceKey), 0, "112233445566", 0, map[string]any{"t": "other", "mac": "112233445566"})
			if err != nil {
				t.Fatal(err)
			}
			return packet
		}()},
		{host: "192.168.1.15", payload: discoveryResponse(t, "11:22:33:44:55:66", "卧室空调", nil)},
	}}
	items, err := scanGreeDevicesWithTransport(context.Background(), 3*time.Second, transport)
	if err != nil {
		t.Fatal(err)
	}
	if transport.timeout != 3*time.Second {
		t.Fatalf("scan timeout = %v", transport.timeout)
	}
	if len(items) != 2 {
		t.Fatalf("scan items = %#v", items)
	}
	if items[0].MAC != "112233445566" || items[0].Host != "192.168.1.15" {
		t.Fatalf("first candidate = %#v", items[0])
	}
	if items[0].Provider != ProviderType || items[0].ID != "gree-112233445566" {
		t.Fatalf("candidate identity = %#v", items[0])
	}
	if items[1].MAC != "aabbccddeeff" || items[1].Host != "192.168.1.11" || items[1].Name != "客厅空调" {
		t.Fatalf("second candidate = %#v", items[1])
	}
	if items[1].Metadata["model"] != "X1" || items[1].Metadata["version"] != "1.2" {
		t.Fatalf("candidate metadata = %#v", items[1].Metadata)
	}
}

func TestGreeScanPropagatesTransportFailure(t *testing.T) {
	want := errors.New("socket unavailable")
	_, err := scanGreeDevicesWithTransport(context.Background(), time.Second, &scriptedDiscoveryTransport{err: want})
	if !errors.Is(err, want) {
		t.Fatalf("scan error = %v, want %v", err, want)
	}
}

func TestGreeDiscoveryResponseRejectsWrongHostAndEnvelope(t *testing.T) {
	packet := discoveryResponse(t, "AA:BB:CC:DD:EE:FF", "客厅空调", nil)
	if _, ok := parseGreeDiscoveryResponse("", packet); ok {
		t.Fatal("empty source host was accepted")
	}
	if _, ok := parseGreeDiscoveryResponse("192.168.1.10", []byte(`{"pack":"bad"}`)); ok {
		t.Fatal("invalid encrypted envelope was accepted")
	}
}
