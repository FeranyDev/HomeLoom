package targetmanager_test

import (
	"context"
	"go.uber.org/zap"
	"sync"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/feranydev/homeloom/backend/internal/runtime/targetmanager"
	"github.com/feranydev/homeloom/backend/internal/targets/homekit"
)

type cameraPublicationRuntimeStub struct {
	mu       sync.Mutex
	enabled  []string
	disabled []string
	reset    []string
	paired   bool
}

func (s *cameraPublicationRuntimeStub) EnableHomeKitCamera(_ context.Context, targetID, deviceID, _ string) (homekit.PairingInfo, bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = append(s.enabled, targetID+"/"+deviceID)
	return homekit.PairingInfo{Code: "123-45-678", SetupID: "CAM1", SetupURI: "X-HM://CAMERA", QR: []byte("camera-qr"), Devices: []string{deviceID}}, s.paired, ":52431", nil
}

func (s *cameraPublicationRuntimeStub) DisableHomeKitCamera(_ context.Context, targetID, deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disabled = append(s.disabled, targetID+"/"+deviceID)
	return nil
}

func (s *cameraPublicationRuntimeStub) InspectHomeKitCamera(string) (homekit.PairingInfo, bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return homekit.PairingInfo{Code: "123-45-678", SetupID: "CAM1", SetupURI: "X-HM://CAMERA", QR: []byte("camera-qr")}, s.paired, ":52431"
}

func (s *cameraPublicationRuntimeStub) ResetHomeKitCamera(_ context.Context, targetID, deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset = append(s.reset, targetID+"/"+deviceID)
	return nil
}

func TestDisabledTargetLifecycleDoesNotStartHAP(t *testing.T) {
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	manager := targetmanager.New(context.Background(), devices, zap.NewNop())
	registration, err := manager.Apply(context.Background(), target.Config{ID: "disabled", Type: "apple-hap", Name: "Disabled"})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if registration.Info.Status != "disabled" || registration.Info.Enabled {
		t.Fatalf("registration = %#v", registration)
	}
	if err := manager.Remove(context.Background(), "missing"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestUnsupportedEnabledTargetReturnsError(t *testing.T) {
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	manager := targetmanager.New(context.Background(), devices, zap.NewNop())
	_, err := manager.Apply(context.Background(), target.Config{ID: "matter", Type: "matter", Name: "Matter", Enabled: true})
	if err == nil {
		t.Fatal("Apply() accepted an unsupported runtime")
	}
}

func TestHomeKitCameraTargetUsesIndependentPublicationRuntime(t *testing.T) {
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	cameraRuntime := &cameraPublicationRuntimeStub{}
	manager := targetmanager.New(context.Background(), devices, zap.NewNop(), cameraRuntime)
	config := target.Config{
		ID: "camera-homekit-1", Type: "homekit-camera", Name: "客厅摄像头", Enabled: true,
		Devices: []target.VirtualDevice{{
			ID: "xiaomi-camera-1", Name: "客厅摄像头", Type: device.TypeCamera,
			SourceDeviceID: "xiaomi-camera-1", Enabled: true,
		}},
	}
	registration, err := manager.Apply(context.Background(), config)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if registration.Info.Type != "homekit-camera" || registration.Info.Address != ":52431" ||
		registration.Info.PairingCode != "123-45-678" || registration.Info.SetupID != "CAM1" ||
		registration.Info.SetupURI != "X-HM://CAMERA" || string(registration.QR) != "camera-qr" || registration.Info.Status != "running" {
		t.Fatalf("registration = %#v", registration)
	}
	cameraRuntime.mu.Lock()
	cameraRuntime.paired = true
	cameraRuntime.mu.Unlock()
	if !manager.IsPaired(config) {
		t.Fatal("Camera target without a bridge StorePath did not refresh its live pairing state")
	}
	if err := manager.RemoveTarget(context.Background(), config); err != nil {
		t.Fatalf("RemoveTarget() error = %v", err)
	}
	cameraRuntime.mu.Lock()
	defer cameraRuntime.mu.Unlock()
	if len(cameraRuntime.enabled) != 1 || len(cameraRuntime.disabled) != 1 || len(cameraRuntime.reset) != 1 {
		t.Fatalf("enabled = %#v, disabled = %#v, reset = %#v", cameraRuntime.enabled, cameraRuntime.disabled, cameraRuntime.reset)
	}
}
