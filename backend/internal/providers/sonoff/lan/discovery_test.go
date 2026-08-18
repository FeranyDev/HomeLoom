package lan

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeServiceBrowser struct {
	services []Service
	err      error
}

func (f fakeServiceBrowser) Browse(_ context.Context, serviceType string, add func(Service)) error {
	if serviceType != ServiceType {
		return errors.New("unexpected service type")
	}
	for _, service := range f.services {
		add(service)
	}
	return f.err
}

func TestDiscoverServicesDeduplicatesByDeviceIDWithoutDecodingTXTData(t *testing.T) {
	items, err := DiscoverServices(context.Background(), time.Second, fakeServiceBrowser{services: []Service{
		{Address: "192.0.2.20", Host: "second.local.", Port: 8081, TXT: map[string]string{"id": "device-2", "data1": `{"token":"never-returned"}`}},
		{Address: "192.0.2.11", Host: "first.local.", Port: 8081, TXT: map[string]string{"id": "device-1", "type": "plug"}},
		{Address: "192.0.2.10", Host: "first.local.", Port: 8081, TXT: map[string]string{"id": "device-1", "type": "plug"}},
		{Address: "192.0.2.12", TXT: map[string]string{}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].TXT["id"] != "device-1" || items[0].Address != "192.0.2.10" || items[1].TXT["id"] != "device-2" {
		t.Fatalf("services = %#v", items)
	}
	if items[1].TXT["data1"] == "" {
		t.Fatalf("raw service profile was unexpectedly altered: %#v", items[1])
	}
}

func TestDiscoverServicesTreatsItsTimeoutAsCompletedScan(t *testing.T) {
	items, err := DiscoverServices(context.Background(), time.Second, fakeServiceBrowser{err: context.DeadlineExceeded})
	if err != nil || len(items) != 0 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
}

func TestDiscoverServicesPreservesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DiscoverServices(ctx, time.Second, fakeServiceBrowser{err: context.DeadlineExceeded})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}
