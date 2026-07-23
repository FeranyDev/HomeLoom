package targetmanager

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/feranydev/homeloom/backend/internal/targets/homekit"
	mattertarget "github.com/feranydev/homeloom/backend/internal/targets/matter"
)

type lifecycleTarget struct {
	pairing homekit.PairingInfo
	paired  bool
	started chan struct{}
	stopped chan struct{}
	exit    chan error
	once    sync.Once
}

type lifecycleMatterStore struct{}

func (lifecycleMatterStore) PutMatterRuntimeValue(context.Context, string, string, []byte) error {
	return nil
}
func (lifecycleMatterStore) GetMatterRuntimeValue(context.Context, string, string) ([]byte, bool, error) {
	return nil, false, nil
}
func (lifecycleMatterStore) ListMatterRuntimeValues(context.Context, string) ([]target.MatterRuntimeValue, error) {
	return nil, nil
}
func (lifecycleMatterStore) DeleteMatterRuntimeValue(context.Context, string, string) error {
	return nil
}
func (lifecycleMatterStore) ClearMatterRuntimeValues(context.Context, string) error { return nil }
func (lifecycleMatterStore) AllocateMatterEndpoint(context.Context, string, string, device.Type) (uint16, error) {
	return 2, nil
}
func (lifecycleMatterStore) TombstoneMatterEndpoint(context.Context, string, string) error {
	return nil
}
func (lifecycleMatterStore) ConfirmMatterEndpointDeviceType(context.Context, string, string, device.Type, bool) error {
	return nil
}
func (lifecycleMatterStore) MatterEndpointIdentity(context.Context, string, string) (target.MatterEndpointIdentity, bool, error) {
	return target.MatterEndpointIdentity{}, false, nil
}
func (lifecycleMatterStore) ListMatterEndpointIdentities(context.Context, string) ([]target.MatterEndpointIdentity, error) {
	return nil, nil
}

func newLifecycleTarget(id string) *lifecycleTarget {
	return &lifecycleTarget{
		pairing: homekit.PairingInfo{Code: "123-45-678", SetupURI: "X-HM://" + id, QR: []byte(id)},
		started: make(chan struct{}), stopped: make(chan struct{}), exit: make(chan error, 1),
	}
}

func (t *lifecycleTarget) PairingInfo() homekit.PairingInfo { return t.pairing }
func (t *lifecycleTarget) IsPaired() bool                   { return t.paired }

func (t *lifecycleTarget) Start(ctx context.Context) error {
	close(t.started)
	select {
	case err := <-t.exit:
		return err
	case <-ctx.Done():
		t.once.Do(func() { close(t.stopped) })
		return nil
	}
}

type lifecycleFactory struct {
	mu      sync.Mutex
	items   map[string][]*lifecycleTarget
	configs []homekit.Config
}

func (f *lifecycleFactory) create(_ context.Context, config homekit.Config, _ *application.DeviceService, _ *slog.Logger) (managedTarget, error) {
	created := newLifecycleTarget(config.ID)
	f.mu.Lock()
	f.items[config.ID] = append(f.items[config.ID], created)
	f.configs = append(f.configs, config)
	f.mu.Unlock()
	return created, nil
}

func (f *lifecycleFactory) target(id string, index int) *lifecycleTarget {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.items[id][index]
}

func newLifecycleManager(t *testing.T) (*Manager, *lifecycleFactory) {
	t.Helper()
	devices := application.NewDeviceService(virtual.NewProvider())
	t.Cleanup(func() { _ = devices.Close() })
	factory := &lifecycleFactory{items: make(map[string][]*lifecycleTarget)}
	manager := New(context.Background(), devices, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.factory = factory.create
	manager.startGrace = time.Millisecond
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Close(ctx)
	})
	return manager, factory
}

func TestMultipleTargetsRunIndependentlyAndReloadInPlace(t *testing.T) {
	manager, factory := newLifecycleManager(t)
	first := target.Config{ID: "first", Type: "apple-hap", Name: "First", Enabled: true, Address: "127.0.0.1:0", Pin: "12345678", SetupID: "ONE1", StorePath: "data/hap/first"}
	second := target.Config{ID: "second", Type: "apple-hap", Name: "Second", Enabled: true, Address: "127.0.0.1:0", Pin: "87654321", SetupID: "TWO2", StorePath: "data/hap/second"}

	registration, err := manager.Apply(context.Background(), first)
	if err != nil || registration.Info.Status != "running" || registration.Info.SetupURI != "X-HM://first" || string(registration.QR) != "first" {
		t.Fatalf("first Apply() = %#v, %v", registration, err)
	}
	if _, err := manager.Apply(context.Background(), second); err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	oldFirst := factory.target("first", 0)
	runningSecond := factory.target("second", 0)
	first.Name = "First Reloaded"
	if _, err := manager.Apply(context.Background(), first); err != nil {
		t.Fatalf("reload Apply() error = %v", err)
	}
	select {
	case <-oldFirst.stopped:
	case <-time.After(time.Second):
		t.Fatal("reloaded target did not stop its previous runtime")
	}
	select {
	case <-runningSecond.stopped:
		t.Fatal("reloading one target stopped another target")
	default:
	}

	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	for _, current := range []*lifecycleTarget{factory.target("first", 1), runningSecond} {
		select {
		case <-current.stopped:
		case <-time.After(time.Second):
			t.Fatal("Close() did not stop every target")
		}
	}
}

func TestTargetConfigurationRemainsStableAcrossThreeReloads(t *testing.T) {
	manager, factory := newLifecycleManager(t)
	config := target.Config{ID: "stable", Type: "apple-hap", Name: "Stable", Enabled: true, Address: "127.0.0.1:0", Pin: "12345678", SetupID: "KEEP", StorePath: "data/hap/stable", DeviceIDs: []string{"switch-1"}}
	for attempt := 0; attempt < 4; attempt++ {
		if _, err := manager.Apply(context.Background(), config); err != nil {
			t.Fatalf("Apply() attempt %d error = %v", attempt, err)
		}
	}
	factory.mu.Lock()
	defer factory.mu.Unlock()
	if len(factory.configs) != 4 {
		t.Fatalf("created configs = %d", len(factory.configs))
	}
	for _, current := range factory.configs {
		if current.SetupID != config.SetupID || current.StorePath != config.StorePath || current.Pin != config.Pin || len(current.DeviceIDs) != 1 || current.DeviceIDs[0] != "switch-1" {
			t.Fatalf("configuration drifted across reloads: %#v", current)
		}
	}
}

func TestManagerReportsLivePairingState(t *testing.T) {
	manager, factory := newLifecycleManager(t)
	config := target.Config{ID: "paired", Type: "apple-hap", Name: "Paired", Enabled: true, Address: "127.0.0.1:0", Pin: "12345678", SetupID: "PAIR", StorePath: "data/hap/paired"}
	registration, err := manager.Apply(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if registration.Info.Paired || manager.IsPaired(config) {
		t.Fatal("new target unexpectedly reported as paired")
	}
	factory.target(config.ID, 0).paired = true
	if !manager.IsPaired(config) {
		t.Fatal("manager did not observe the live HAP pairing state")
	}
}

func TestRuntimeFailureAfterStartupPublishesErrorStatus(t *testing.T) {
	manager, factory := newLifecycleManager(t)
	statuses := make(chan string, 1)
	manager.SetStatusHandler(func(id, status string) {
		if id == "failed" {
			statuses <- status
		}
	})
	config := target.Config{ID: "failed", Type: "apple-hap", Name: "Failed", Enabled: true, Address: "127.0.0.1:0"}
	if _, err := manager.Apply(context.Background(), config); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	factory.target("failed", 0).exit <- errors.New("runtime failed")
	select {
	case status := <-statuses:
		if status != "error" {
			t.Fatalf("status = %q", status)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime failure status was not published")
	}
}

func TestMatterTargetUsesIndependentRuntimeFactory(t *testing.T) {
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	manager := New(context.Background(), devices, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycleMatterStore{})
	created := newLifecycleTarget("matter-one")
	var received mattertarget.Config
	manager.matterFactory = func(_ context.Context, config mattertarget.Config, _ *application.DeviceService, _ mattertarget.Storage, _ *slog.Logger) (managedTarget, error) {
		received = config
		return created, nil
	}
	manager.startGrace = time.Millisecond
	discriminator := uint16(1234)
	config := target.Config{
		ID: "matter-one", Type: "matter", Name: "Matter One", Enabled: true,
		MatterConfig: &target.MatterConfig{
			Discriminator: &discriminator, Passcode: "20202021", VendorID: 0xfff1, ProductID: 0x8000,
			ProductName: "HomeLoom", SerialNumber: "matter-one", CommissioningWindowSeconds: 900,
		},
	}
	registration, err := manager.Apply(context.Background(), config)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if received.ID != config.ID || received.Matter.Passcode != config.MatterConfig.Passcode {
		t.Fatalf("Matter factory config = %#v", received)
	}
	if registration.Info.Type != "matter" || registration.Info.ConsumerID != "matter" || registration.Info.ProtocolVersion != "1.4.1" {
		t.Fatalf("Matter registration = %#v", registration.Info)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMatterTargetRefusesStartupWithoutPersistentStorage(t *testing.T) {
	devices := application.NewDeviceService(virtual.NewProvider())
	defer devices.Close()
	manager := New(context.Background(), devices, slog.New(slog.NewTextHandler(io.Discard, nil)))
	discriminator := uint16(1)
	_, err := manager.Apply(context.Background(), target.Config{
		ID: "matter", Type: "matter", Enabled: true,
		MatterConfig: &target.MatterConfig{Discriminator: &discriminator},
	})
	if err == nil {
		t.Fatal("Matter target started without persistent storage")
	}
}
