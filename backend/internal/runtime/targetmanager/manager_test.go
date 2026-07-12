package targetmanager_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/feranydev/homeloom/backend/internal/runtime/targetmanager"
)

func TestDisabledTargetLifecycleDoesNotStartHAP(t *testing.T) {
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	manager := targetmanager.New(context.Background(), devices, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	manager := targetmanager.New(context.Background(), devices, slog.Default())
	_, err := manager.Apply(context.Background(), target.Config{ID: "matter", Type: "matter", Name: "Matter", Enabled: true})
	if err == nil {
		t.Fatal("Apply() accepted an unsupported runtime")
	}
}
