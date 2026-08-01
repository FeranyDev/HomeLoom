package targetmanager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/device"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/targets/homekit"
	mattertarget "github.com/feranydev/homeloom/backend/internal/targets/matter"
)

type runningTarget struct {
	cancel  context.CancelFunc
	done    chan error
	address string
	target  managedTarget
	config  target.Config
}

type managedTarget interface {
	Start(context.Context) error
	PairingInfo() homekit.PairingInfo
	IsPaired() bool
}

type readyTarget interface {
	Ready() <-chan struct{}
}

type targetFactory func(context.Context, homekit.Config, *application.DeviceService, *slog.Logger) (managedTarget, error)
type matterFactory func(context.Context, mattertarget.Config, *application.DeviceService, mattertarget.Storage, *slog.Logger) (managedTarget, error)

type CameraPublicationRuntime interface {
	EnableHomeKitCamera(context.Context, string, string, string) (homekit.PairingInfo, bool, string, error)
	DisableHomeKitCamera(context.Context, string, string) error
	InspectHomeKitCamera(string) (homekit.PairingInfo, bool, string)
	ResetHomeKitCamera(context.Context, string, string) error
}

type MatterCameraRuntime interface {
	mattertarget.CameraMediaRuntime
}

type Manager struct {
	root          context.Context
	devices       *application.DeviceService
	logger        *slog.Logger
	mu            sync.Mutex
	running       map[string]runningTarget
	onStatus      func(string, string)
	identities    homekit.AccessoryIdentityStore
	factory       targetFactory
	matterStore   *application.MatterStorageService
	matterFactory matterFactory
	cameraRuntime CameraPublicationRuntime
	matterCamera  MatterCameraRuntime
	startGrace    time.Duration
}

func New(root context.Context, devices *application.DeviceService, logger *slog.Logger, dependencies ...any) *Manager {
	manager := &Manager{
		root: root, devices: devices, logger: logger, running: make(map[string]runningTarget),
		factory: func(ctx context.Context, config homekit.Config, devices *application.DeviceService, logger *slog.Logger) (managedTarget, error) {
			return homekit.New(ctx, config, devices, logger)
		},
		matterFactory: func(_ context.Context, config mattertarget.Config, devices *application.DeviceService, storage mattertarget.Storage, logger *slog.Logger) (managedTarget, error) {
			created, err := mattertarget.New(config, devices, storage, logger)
			if err != nil {
				return nil, err
			}
			return &managedMatterTarget{Target: created}, nil
		},
		startGrace: 200 * time.Millisecond,
	}
	for _, dependency := range dependencies {
		if identities, ok := dependency.(homekit.AccessoryIdentityStore); ok && manager.identities == nil {
			manager.identities = identities
		}
		if storage, ok := dependency.(application.MatterStorageStore); ok && manager.matterStore == nil {
			manager.matterStore = application.NewMatterStorageService(storage)
		}
		if runtime, ok := dependency.(CameraPublicationRuntime); ok && manager.cameraRuntime == nil {
			manager.cameraRuntime = runtime
		}
		if runtime, ok := dependency.(MatterCameraRuntime); ok && manager.matterCamera == nil {
			manager.matterCamera = runtime
		}
	}
	return manager
}

type managedMatterTarget struct {
	*mattertarget.Target
}

func (t *managedMatterTarget) PairingInfo() homekit.PairingInfo { return homekit.PairingInfo{} }
func (t *managedMatterTarget) IsPaired() bool                   { return t.Status().FabricCount > 0 }
func (t *managedMatterTarget) MatterStatus() mattertarget.CommissioningState {
	return t.Status()
}

func (m *Manager) SetStatusHandler(handler func(string, string)) {
	m.mu.Lock()
	m.onStatus = handler
	m.mu.Unlock()
}

func (m *Manager) Apply(ctx context.Context, config target.Config) (application.TargetRegistration, error) {
	config = config.NormalizeProtocolConfig()
	if !config.Enabled {
		if err := m.stop(ctx, config.ID); err != nil {
			return application.TargetRegistration{}, err
		}
		info := infoFromConfig(config, "disabled")
		info.Paired = m.IsPaired(config)
		return application.TargetRegistration{Info: info}, nil
	}
	m.mu.Lock()
	current, exists := m.running[config.ID]
	sameAddress := exists && current.address == config.Address
	m.mu.Unlock()
	if config.Type == "apple-hap" && !sameAddress {
		if err := homekit.CheckAddressAvailable(config.Address); err != nil {
			return application.TargetRegistration{}, err
		}
	}

	var (
		next       managedTarget
		err        error
		preStopped bool
	)
	switch config.Type {
	case "apple-hap":
		next, err = m.factory(ctx, homekit.Config{
			ID: config.ID, Name: config.Name, Address: config.Address, Pin: config.Pin,
			SetupID: config.SetupID, StorePath: config.StorePath, DeviceIDs: config.DeviceIDs, Devices: config.Devices, IdentityStore: m.identities,
		}, m.devices, m.logger)
	case "matter", "matter-camera":
		if config.MatterConfig == nil {
			return application.TargetRegistration{}, errors.New("Matter protocol configuration is required")
		}
		if m.matterStore == nil {
			return application.TargetRegistration{}, errors.New("Matter persistent storage is unavailable")
		}
		next, err = m.matterFactory(ctx, mattertarget.Config{
			ID: config.ID, Name: config.Name, NodeKind: matterNodeKind(config.Type), Matter: *config.MatterConfig, Devices: config.Devices,
			OnStatus:    func() { m.setStatus(config.ID, "running") },
			CameraMedia: m.matterCamera,
		}, m.devices, m.matterStore, m.logger)
	case "homekit-camera":
		if m.cameraRuntime == nil {
			return application.TargetRegistration{}, errors.New("HomeKit Camera publication runtime is unavailable")
		}
		if len(config.Devices) != 1 || config.Devices[0].Type != device.TypeCamera {
			return application.TargetRegistration{}, errors.New("HomeKit Camera target requires exactly one camera device")
		}
		if err := m.stop(ctx, config.ID); err != nil {
			return application.TargetRegistration{}, err
		}
		preStopped = true
		current := config.Devices[0]
		pairing, paired, address, enableErr := m.cameraRuntime.EnableHomeKitCamera(ctx, config.ID, current.SourceDeviceID, config.Name)
		if enableErr != nil {
			return application.TargetRegistration{}, enableErr
		}
		config.Address = address
		next = &managedCameraPublication{
			targetID: config.ID, deviceID: current.SourceDeviceID,
			runtime: m.cameraRuntime, pairing: pairing, paired: paired,
		}
	default:
		return application.TargetRegistration{}, fmt.Errorf("target type %q is not implemented", config.Type)
	}
	if err != nil {
		return application.TargetRegistration{}, err
	}
	if !preStopped {
		err = m.stop(ctx, config.ID)
	}
	if err != nil {
		return application.TargetRegistration{}, err
	}

	runCtx, cancel := context.WithCancel(m.root)
	done := make(chan error, 1)
	m.mu.Lock()
	m.running[config.ID] = runningTarget{cancel: cancel, done: done, address: config.Address, target: next, config: config}
	m.mu.Unlock()
	go func() {
		err := next.Start(runCtx)
		done <- err
		close(done)
		if runCtx.Err() == nil && err != nil {
			m.mu.Lock()
			if current, exists := m.running[config.ID]; exists && current.done == done {
				delete(m.running, config.ID)
			}
			m.mu.Unlock()
			m.logger.Error("target runtime exited", "target_id", config.ID, "error", err)
			m.setStatus(config.ID, "error")
		}
	}()

	if readiness, ok := next.(readyTarget); ok {
		select {
		case startErr := <-done:
			m.mu.Lock()
			delete(m.running, config.ID)
			m.mu.Unlock()
			if startErr == nil {
				startErr = errors.New("target stopped during startup")
			}
			return application.TargetRegistration{}, startErr
		case <-readiness.Ready():
		case <-ctx.Done():
			_ = m.stop(context.Background(), config.ID)
			return application.TargetRegistration{}, fmt.Errorf("start target %q: %w", config.ID, ctx.Err())
		}
	} else {
		select {
		case startErr := <-done:
			m.mu.Lock()
			delete(m.running, config.ID)
			m.mu.Unlock()
			if startErr == nil {
				startErr = errors.New("target stopped during startup")
			}
			return application.TargetRegistration{}, startErr
		case <-time.After(m.startGrace):
		}
	}

	pairing := next.PairingInfo()
	info := infoFromConfig(config, "running")
	info.Paired = next.IsPaired()
	applyMatterStatus(&info, next)
	applyTargetDiagnostics(&info, next)
	if !info.Paired {
		info.PairingCode, info.SetupURI = pairing.Code, pairing.SetupURI
	}
	return application.TargetRegistration{Info: info, QR: pairing.QR}, nil
}

type managedCameraPublication struct {
	targetID string
	deviceID string
	runtime  CameraPublicationRuntime
	pairing  homekit.PairingInfo
	paired   bool
}

func (t *managedCameraPublication) Start(ctx context.Context) error {
	<-ctx.Done()
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return t.runtime.DisableHomeKitCamera(stopCtx, t.targetID, t.deviceID)
}
func (t *managedCameraPublication) PairingInfo() homekit.PairingInfo { return t.pairing }
func (t *managedCameraPublication) IsPaired() bool {
	_, paired, _ := t.runtime.InspectHomeKitCamera(t.deviceID)
	return paired
}

func (m *Manager) IsPaired(config target.Config) bool {
	descriptor, _ := target.DescriptorForType(config.Type)
	if target.IsMatterType(config.Type) {
		m.mu.Lock()
		running, exists := m.running[config.ID]
		m.mu.Unlock()
		return exists && running.target.IsPaired()
	}
	if !descriptor.SupportsHomeKitPairing {
		return false
	}
	m.mu.Lock()
	running, exists := m.running[config.ID]
	m.mu.Unlock()
	if exists {
		return running.target.IsPaired()
	}
	if config.Type == "homekit-camera" && m.cameraRuntime != nil && len(config.Devices) == 1 {
		_, paired, _ := m.cameraRuntime.InspectHomeKitCamera(config.Devices[0].SourceDeviceID)
		return paired
	}
	// Only a normal HAP bridge owns the target StorePath. Independent cameras
	// keep their pairings in the per-stream publisher directory inspected
	// above, so an empty StorePath must not erase their live pairing state.
	if config.StorePath == "" {
		return false
	}
	paired, err := homekit.HasPairings(config.StorePath)
	if err != nil {
		m.logger.Warn("inspect HomeKit pairing state failed", "target_id", config.ID, "error", err)
		return false
	}
	return paired
}

func matterNodeKind(targetType string) string {
	if targetType == "matter-camera" {
		return "camera"
	}
	return "bridge"
}

func (m *Manager) RuntimeInfo(config target.Config) application.TargetInfo {
	m.mu.Lock()
	running, exists := m.running[config.ID]
	m.mu.Unlock()
	status := "disabled"
	if config.Enabled {
		status = "error"
	}
	if exists {
		status = "running"
	}
	info := infoFromConfig(config, status)
	if exists {
		info.Paired = running.target.IsPaired()
		applyMatterStatus(&info, running.target)
		applyTargetDiagnostics(&info, running.target)
	}
	return info
}

func (m *Manager) Remove(ctx context.Context, id string) error { return m.stop(ctx, id) }

// RemoveTarget additionally destroys an independent HomeKit Camera identity.
// Stopping publication alone intentionally preserves Device Center preview,
// but deleting the target must not leave a pairing that Apple Home can retain.
func (m *Manager) RemoveTarget(ctx context.Context, config target.Config) error {
	if err := m.stop(ctx, config.ID); err != nil {
		return err
	}
	if config.Type != "homekit-camera" {
		return nil
	}
	if m.cameraRuntime == nil || len(config.Devices) != 1 {
		return errors.New("HomeKit Camera publication runtime is unavailable")
	}
	return m.cameraRuntime.ResetHomeKitCamera(ctx, config.ID, config.Devices[0].SourceDeviceID)
}

func (m *Manager) ResetPairing(ctx context.Context, config target.Config) (application.TargetRegistration, error) {
	if config.Type == "homekit-camera" {
		if m.cameraRuntime == nil || len(config.Devices) != 1 {
			return application.TargetRegistration{}, errors.New("HomeKit Camera publication runtime is unavailable")
		}
		if err := m.stop(ctx, config.ID); err != nil {
			return application.TargetRegistration{}, err
		}
		if err := m.cameraRuntime.ResetHomeKitCamera(ctx, config.ID, config.Devices[0].SourceDeviceID); err != nil {
			return application.TargetRegistration{}, err
		}
		return m.Apply(ctx, config)
	}
	if config.Type != "apple-hap" {
		return application.TargetRegistration{}, fmt.Errorf("target type %q does not support pairing reset", config.Type)
	}
	if err := m.stop(ctx, config.ID); err != nil {
		return application.TargetRegistration{}, err
	}
	if err := removePairingIdentity(config.ID, config.StorePath); err != nil {
		return application.TargetRegistration{}, err
	}
	return m.Apply(ctx, config)
}

func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	ids := make([]string, 0, len(m.running))
	for id := range m.running {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		if err := m.stop(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) stop(ctx context.Context, id string) error {
	m.mu.Lock()
	running, ok := m.running[id]
	if ok {
		delete(m.running, id)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	running.cancel()
	select {
	case <-running.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop target %q: %w", id, ctx.Err())
	case <-time.After(5 * time.Second):
		return fmt.Errorf("stop target %q: timeout", id)
	}
}

func (m *Manager) setStatus(id, status string) {
	m.mu.Lock()
	handler := m.onStatus
	m.mu.Unlock()
	if handler != nil {
		handler(id, status)
	}
}

func applyTargetDiagnostics(info *application.TargetInfo, current managedTarget) {
	reporter, ok := current.(interface {
		Issues() []homekit.ProjectionIssue
	})
	if !ok {
		return
	}
	issues := reporter.Issues()
	if len(issues) == 0 {
		return
	}
	info.Issues = make([]application.TargetIssue, 0, len(issues))
	info.Diagnostics = map[string]string{
		"skippedAccessories": fmt.Sprintf("%d", len(issues)),
	}
	if counter, ok := current.(interface{ PublishedAccessoryCount() int }); ok {
		info.Diagnostics["publishedAccessories"] = fmt.Sprintf("%d", counter.PublishedAccessoryCount())
	}
	summaryParts := make([]string, 0, len(issues))
	for _, issue := range issues {
		info.Issues = append(info.Issues, application.TargetIssue{
			DeviceID: issue.DeviceID, DeviceName: issue.DeviceName, DeviceType: string(issue.DeviceType),
			Stage: issue.Stage, Message: issue.Message,
		})
		label := issue.DeviceName
		if label == "" {
			label = issue.DeviceID
		}
		if label == "" {
			label = string(issue.DeviceType)
		}
		summaryParts = append(summaryParts, fmt.Sprintf("%s: %s", label, issue.Message))
	}
	if info.Error == "" {
		if len(summaryParts) == 1 {
			info.Error = fmt.Sprintf("1 台设备未能发布到桥：%s", summaryParts[0])
		} else {
			info.Error = fmt.Sprintf("%d 台设备未能发布到桥：%s", len(summaryParts), summaryParts[0])
			if len(summaryParts) > 1 {
				info.Error += fmt.Sprintf(" 等 %d 项问题", len(summaryParts))
			}
		}
	}
	info.Diagnostics["issueSummary"] = strings.Join(summaryParts, " | ")
}

func infoFromConfig(config target.Config, status string) application.TargetInfo {
	descriptor, _ := target.DescriptorForType(config.Type)
	info := application.TargetInfo{
		ID: config.ID, Type: config.Type, ConsumerID: descriptor.ConsumerID, Name: config.Name, Enabled: config.Enabled,
		Status: status, Address: config.Address, SetupID: config.SetupID,
		DeviceIDs: append([]string{}, config.DeviceIDs...),
		Devices:   append([]target.VirtualDevice(nil), config.Devices...),
	}
	if config.MatterConfig != nil {
		info.NetworkInterface = config.MatterConfig.NetworkInterface
		info.UDPPort = config.MatterConfig.UDPPort
		if config.MatterConfig.Discriminator != nil {
			info.Discriminator = *config.MatterConfig.Discriminator
		}
		info.VendorID, info.ProductID = config.MatterConfig.VendorID, config.MatterConfig.ProductID
		info.ProductName, info.SerialNumber = config.MatterConfig.ProductName, config.MatterConfig.SerialNumber
		info.CommissioningWindowSeconds = config.MatterConfig.CommissioningWindowSeconds
		info.ProtocolVersion, info.Certification = "1.4.1", "test"
	}
	return info
}

func applyMatterStatus(info *application.TargetInfo, current managedTarget) {
	provider, ok := current.(interface {
		MatterStatus() mattertarget.CommissioningState
	})
	if !ok {
		return
	}
	status := provider.MatterStatus()
	info.CommissioningState = status.State
	info.CommissioningWindowOpen = status.WindowOpen
	info.CommissioningWindowExpiresAt = status.WindowExpiresAt
	info.ManualPairingCode, info.SetupPayload = status.ManualPairingCode, status.SetupPayload
	info.FabricCount, info.EndpointCount = status.FabricCount, status.EndpointCount
	if status.UDPPort != 0 {
		info.UDPPort = status.UDPPort
	}
	info.Fabrics = make([]application.MatterFabric, 0, len(status.Fabrics))
	for _, fabric := range status.Fabrics {
		info.Fabrics = append(info.Fabrics, application.MatterFabric{ID: fabric.ID, Label: fabric.Label})
	}
}

func (m *Manager) matterRuntime(id string) (runningTarget, *managedMatterTarget, error) {
	m.mu.Lock()
	running, found := m.running[id]
	m.mu.Unlock()
	if !found {
		return runningTarget{}, nil, application.ErrTargetNotFound
	}
	runtime, ok := running.target.(*managedMatterTarget)
	if !ok {
		return runningTarget{}, nil, fmt.Errorf("target %q is not a Matter runtime", id)
	}
	return running, runtime, nil
}

func (m *Manager) OpenCommissioningWindow(ctx context.Context, id string, durationSeconds uint32) (application.TargetRegistration, error) {
	running, runtime, err := m.matterRuntime(id)
	if err != nil {
		return application.TargetRegistration{}, err
	}
	if err := runtime.OpenCommissioningWindow(ctx, durationSeconds); err != nil {
		return application.TargetRegistration{}, err
	}
	info := infoFromConfig(running.config, "running")
	applyMatterStatus(&info, runtime)
	return application.TargetRegistration{Info: info}, nil
}

func (m *Manager) CloseCommissioningWindow(ctx context.Context, id string) (application.TargetRegistration, error) {
	running, runtime, err := m.matterRuntime(id)
	if err != nil {
		return application.TargetRegistration{}, err
	}
	if err := runtime.CloseCommissioningWindow(ctx); err != nil {
		return application.TargetRegistration{}, err
	}
	info := infoFromConfig(running.config, "running")
	applyMatterStatus(&info, runtime)
	return application.TargetRegistration{Info: info}, nil
}

func (m *Manager) RemoveFabric(ctx context.Context, id, fabricID string) (application.TargetRegistration, error) {
	running, runtime, err := m.matterRuntime(id)
	if err != nil {
		return application.TargetRegistration{}, err
	}
	if err := runtime.RemoveFabric(ctx, fabricID); err != nil {
		return application.TargetRegistration{}, err
	}
	info := infoFromConfig(running.config, "running")
	applyMatterStatus(&info, runtime)
	return application.TargetRegistration{Info: info}, nil
}

func (m *Manager) FactoryResetMatter(ctx context.Context, id string) (application.TargetRegistration, error) {
	running, runtime, err := m.matterRuntime(id)
	if err != nil {
		return application.TargetRegistration{}, err
	}
	if err := runtime.FactoryReset(ctx); err != nil {
		return application.TargetRegistration{}, err
	}
	info := infoFromConfig(running.config, "running")
	applyMatterStatus(&info, runtime)
	return application.TargetRegistration{Info: info}, nil
}

func (m *Manager) ConfirmMatterEndpointDeviceType(ctx context.Context, targetID, consumerDeviceID string, nextType device.Type) error {
	if m.matterStore == nil {
		return errors.New("Matter persistent storage is unavailable")
	}
	return m.matterStore.ConfirmEndpointDeviceType(ctx, targetID, consumerDeviceID, nextType, true)
}

func removePairingIdentity(id, path string) error {
	clean := filepath.Clean(path)
	if clean == "." || filepath.Base(clean) != id || filepath.Base(filepath.Dir(clean)) != "hap" {
		return fmt.Errorf("refuse unsafe HomeKit identity path %q", path)
	}
	absolute, err := filepath.Abs(clean)
	if err != nil {
		return fmt.Errorf("resolve HomeKit identity path: %w", err)
	}
	root := filepath.Dir(filepath.Dir(absolute))
	for _, current := range []string{root, filepath.Dir(absolute), absolute} {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect HomeKit identity path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("HomeKit identity path contains symlink %q", current)
		}
	}
	if err := os.RemoveAll(absolute); err != nil {
		return fmt.Errorf("clear HomeKit pairing identity: %w", err)
	}
	return nil
}
