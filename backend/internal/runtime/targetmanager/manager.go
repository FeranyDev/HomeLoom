package targetmanager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/domain/target"
	"github.com/feranydev/homeloom/backend/internal/targets/homekit"
)

type runningTarget struct {
	cancel  context.CancelFunc
	done    chan error
	address string
	target  managedTarget
}

type managedTarget interface {
	Start(context.Context) error
	PairingInfo() homekit.PairingInfo
	IsPaired() bool
}

type targetFactory func(context.Context, homekit.Config, *application.DeviceService, *slog.Logger) (managedTarget, error)

type Manager struct {
	root       context.Context
	devices    *application.DeviceService
	logger     *slog.Logger
	mu         sync.Mutex
	running    map[string]runningTarget
	onStatus   func(string, string)
	identities homekit.AccessoryIdentityStore
	factory    targetFactory
	startGrace time.Duration
}

func New(root context.Context, devices *application.DeviceService, logger *slog.Logger, identityStores ...homekit.AccessoryIdentityStore) *Manager {
	manager := &Manager{
		root: root, devices: devices, logger: logger, running: make(map[string]runningTarget),
		factory: func(ctx context.Context, config homekit.Config, devices *application.DeviceService, logger *slog.Logger) (managedTarget, error) {
			return homekit.New(ctx, config, devices, logger)
		},
		startGrace: 200 * time.Millisecond,
	}
	if len(identityStores) > 0 {
		manager.identities = identityStores[0]
	}
	return manager
}

func (m *Manager) SetStatusHandler(handler func(string, string)) {
	m.mu.Lock()
	m.onStatus = handler
	m.mu.Unlock()
}

func (m *Manager) Apply(ctx context.Context, config target.Config) (application.TargetRegistration, error) {
	if !config.Enabled {
		if err := m.stop(ctx, config.ID); err != nil {
			return application.TargetRegistration{}, err
		}
		info := infoFromConfig(config, "disabled")
		info.Paired = m.IsPaired(config)
		return application.TargetRegistration{Info: info}, nil
	}
	if config.Type != "apple-hap" {
		return application.TargetRegistration{}, fmt.Errorf("target type %q is not implemented", config.Type)
	}
	m.mu.Lock()
	current, exists := m.running[config.ID]
	sameAddress := exists && current.address == config.Address
	m.mu.Unlock()
	if !sameAddress {
		if err := homekit.CheckAddressAvailable(config.Address); err != nil {
			return application.TargetRegistration{}, err
		}
	}

	next, err := m.factory(ctx, homekit.Config{
		ID: config.ID, Name: config.Name, Address: config.Address, Pin: config.Pin,
		SetupID: config.SetupID, StorePath: config.StorePath, DeviceIDs: config.DeviceIDs, Devices: config.Devices, IdentityStore: m.identities,
	}, m.devices, m.logger)
	if err != nil {
		return application.TargetRegistration{}, err
	}
	if err := m.stop(ctx, config.ID); err != nil {
		return application.TargetRegistration{}, err
	}

	runCtx, cancel := context.WithCancel(m.root)
	done := make(chan error, 1)
	m.mu.Lock()
	m.running[config.ID] = runningTarget{cancel: cancel, done: done, address: config.Address, target: next}
	m.mu.Unlock()
	go func() {
		err := next.Start(runCtx)
		done <- err
		close(done)
		if runCtx.Err() == nil && err != nil {
			m.logger.Error("target runtime exited", "target_id", config.ID, "error", err)
			m.setStatus(config.ID, "error")
		}
	}()

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

	pairing := next.PairingInfo()
	info := infoFromConfig(config, "running")
	info.Paired = next.IsPaired()
	if !info.Paired {
		info.PairingCode, info.SetupURI = pairing.Code, pairing.SetupURI
	}
	return application.TargetRegistration{Info: info, QR: pairing.QR}, nil
}

func (m *Manager) IsPaired(config target.Config) bool {
	descriptor, _ := target.DescriptorForType(config.Type)
	if !descriptor.SupportsHomeKitPairing || config.StorePath == "" {
		return false
	}
	m.mu.Lock()
	running, exists := m.running[config.ID]
	m.mu.Unlock()
	if exists {
		return running.target.IsPaired()
	}
	paired, err := homekit.HasPairings(config.StorePath)
	if err != nil {
		m.logger.Warn("inspect HomeKit pairing state failed", "target_id", config.ID, "error", err)
		return false
	}
	return paired
}

func (m *Manager) Remove(ctx context.Context, id string) error { return m.stop(ctx, id) }

func (m *Manager) ResetPairing(ctx context.Context, config target.Config) (application.TargetRegistration, error) {
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

func infoFromConfig(config target.Config, status string) application.TargetInfo {
	descriptor, _ := target.DescriptorForType(config.Type)
	return application.TargetInfo{
		ID: config.ID, Type: config.Type, ConsumerID: descriptor.ConsumerID, Name: config.Name, Enabled: config.Enabled,
		Status: status, Address: config.Address, SetupID: config.SetupID,
		DeviceIDs: append([]string{}, config.DeviceIDs...),
		Devices:   append([]target.VirtualDevice(nil), config.Devices...),
	}
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
