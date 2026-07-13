package homekit

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/brutella/hap/characteristic"
	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
)

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
	bindings := newAccessoryBindings(items, map[string]bool{}, service, logger)
	if len(bindings.accessories) != 2 || len(bindings.switches) != 1 || len(bindings.temperatures) != 1 || len(bindings.faults) != 2 {
		t.Fatalf("bindings = %#v", bindings)
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
	bindings := newAccessoryBindings(items, map[string]bool{"virtual-switch-1": true}, service, logger)
	if len(bindings.accessories) != 1 || bindings.switches["virtual-switch-1"] == nil || bindings.temperatures["virtual-temperature-1"] != nil {
		t.Fatalf("selected bindings = %#v", bindings)
	}
}
