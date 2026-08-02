package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/buildinfo"
	"github.com/feranydev/homeloom/backend/internal/config"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/mapping"
	"github.com/feranydev/homeloom/backend/internal/persistence/gormstore"
	"github.com/feranydev/homeloom/backend/internal/platform/httpapi"
	"github.com/feranydev/homeloom/backend/internal/platform/logging"
	"github.com/feranydev/homeloom/backend/internal/platform/subprocesslog"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	cameraprovider "github.com/feranydev/homeloom/backend/internal/providers/camera"
	"github.com/feranydev/homeloom/backend/internal/providers/gree"
	mqttprovider "github.com/feranydev/homeloom/backend/internal/providers/mqtt"
	"github.com/feranydev/homeloom/backend/internal/providers/tuya"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/feranydev/homeloom/backend/internal/providers/xiaomi"
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
	"github.com/feranydev/homeloom/backend/internal/runtime/targetmanager"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	configPath := flag.String("config", "", "path to a YAML configuration file")
	mainLogLevel := flag.String("log-level", "info", "Core terminal log level: debug, info, warn, or error")
	showVersion := flag.Bool("version", false, "print build version and exit")
	backupPath := flag.String("backup", "", "create a consistent database logical snapshot and exit")
	restorePath := flag.String("restore", "", "restore a validated database logical snapshot and exit")
	restoreReplace := flag.Bool("restore-replace", false, "explicitly allow restore to replace the configured database")
	initializeVirtualModels := flag.Bool("init-all-virtual-models", false, "initialize one demo device for every supported Virtual Provider model")
	flag.Parse()
	if *showVersion {
		info := buildinfo.Current()
		fmt.Printf("HomeLoom %s (%s) built %s with %s\n", info.Version, info.Commit, info.BuildTime, info.GoVersion)
		return
	}

	level, levelErr := parseMainLogLevel(*mainLogLevel)
	if levelErr != nil {
		fmt.Fprintln(os.Stderr, levelErr)
		os.Exit(2)
	}
	logger := logging.NewStderr(level)
	defer func() { _ = logger.Sync() }()
	zap.ReplaceGlobals(logger)
	childLogs := subprocesslog.New(subprocesslog.DefaultCapacity)
	settings, err := config.Load(*configPath)
	if err != nil {
		logger.Error("configuration failed", zap.Error(err))
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *backupPath != "" && *restorePath != "" {
		logger.Error("backup and restore flags cannot be used together")
		os.Exit(2)
	}
	if *restoreReplace && *restorePath == "" {
		logger.Error("restore replacement confirmation requires -restore")
		os.Exit(2)
	}
	if *restorePath != "" {
		recoveryPath, restoreErr := gormstore.Restore(ctx, *restorePath, settings.Storage.DatabaseURL, settings.Storage.MasterKey, *restoreReplace)
		if restoreErr != nil {
			logger.Error("database restore failed", zap.String("source", *restorePath), zap.Error(restoreErr))
			os.Exit(1)
		}
		if discardErr := gormstore.DiscardPendingRestore(settings.Storage.MasterKey); discardErr != nil {
			logger.Error("database restored but stale pending restore cleanup failed", zap.Error(discardErr))
			os.Exit(1)
		}
		logger.Info("database restore completed", zap.String("source", *restorePath), zap.String("pre_restore_backup", recoveryPath))
		return
	}
	if *backupPath != "" {
		store, openErr := gormstore.OpenForBackup(ctx, settings.Storage.DatabaseURL, settings.Storage.MasterKey)
		if openErr != nil {
			logger.Error("database backup source failed", zap.Error(openErr))
			os.Exit(1)
		}
		defer store.Close()
		if err := store.Backup(ctx, *backupPath); err != nil {
			logger.Error("database backup failed", zap.String("destination", *backupPath), zap.Error(err))
			os.Exit(1)
		}
		version, _ := store.SchemaVersion(ctx)
		logger.Info("database backup completed", zap.String("destination", *backupPath), zap.Int("schema_version", version))
		return
	}
	recoveryPath, pendingApplied, pendingErr := gormstore.ApplyPendingRestore(ctx, settings.Storage.DatabaseURL, settings.Storage.MasterKey)
	if pendingErr != nil && !pendingApplied {
		logger.Error("pending database restore failed", zap.Error(pendingErr))
		os.Exit(1)
	}
	if pendingErr != nil {
		logger.Warn("pending database restore applied with staging cleanup warning", zap.Error(pendingErr))
	}
	if pendingApplied {
		logger.Info("pending database restore applied", zap.String("pre_restore_backup", recoveryPath))
	}
	store, err := gormstore.Open(ctx, settings.Storage.DatabaseURL, settings.Storage.MasterKey)
	if err != nil {
		logger.Error("database initialization failed", zap.Error(err))
		os.Exit(1)
	}
	defer store.Close()
	var mediaRuntime *embeddedMediaRuntime
	if *initializeVirtualModels {
		changed, initializeErr := initializeAllVirtualModels(ctx, store)
		if initializeErr != nil {
			logger.Error("virtual model device initialization failed", zap.Error(initializeErr))
			os.Exit(1)
		}
		if changed {
			logger.Info("virtual model demo devices initialized", zap.String("provider_id", "virtual-main"), zap.Int("device_count", len(virtual.AllModelDeviceConfigs())))
		}
	}
	providerConfigs, err := store.ListProviders(ctx)
	if err != nil {
		logger.Error("provider configuration load failed", zap.Error(err))
		os.Exit(1)
	}
	providerConfigs, err = migrateXiaomiCameraProviders(ctx, providerConfigs, store)
	if err != nil {
		logger.Error("camera provider migration failed", zap.Error(err))
		os.Exit(1)
	}
	factory := providersdk.NewFactory()
	if err := factory.Register("virtual", func(config providerconfig.Config) (providersdk.Provider, error) {
		return virtual.NewProviderFromConfig(config)
	}); err != nil {
		logger.Error("provider factory registration failed", zap.Error(err))
		os.Exit(1)
	}
	if err := factory.Register("mqtt", func(config providerconfig.Config) (providersdk.Provider, error) {
		return mqttprovider.NewProviderFromConfig(config)
	}); err != nil {
		logger.Error("mqtt provider factory registration failed", zap.Error(err))
		os.Exit(1)
	}
	if err := factory.Register(gree.ProviderType, func(config providerconfig.Config) (providersdk.Provider, error) {
		return gree.NewProviderFromConfig(config)
	}); err != nil {
		logger.Error("gree provider factory registration failed", zap.Error(err))
		os.Exit(1)
	}
	if err := factory.Register(tuya.ProviderType, func(config providerconfig.Config) (providersdk.Provider, error) {
		return tuya.NewProviderFromConfig(config)
	}); err != nil {
		logger.Error("tuya provider factory registration failed", zap.Error(err))
		os.Exit(1)
	}
	xiaomiSpecs := xiaomi.NewSpecResolver(store)
	var providerManager *providermanager.Manager
	if err := factory.Register("xiaomi", func(config providerconfig.Config) (providersdk.Provider, error) {
		return xiaomi.NewProviderFromConfigWithSpecResolver(config, xiaomiSpecs)
	}); err != nil {
		logger.Error("xiaomi provider factory registration failed", zap.Error(err))
		os.Exit(1)
	}
	if err := factory.Register(xiaomi.XiaomiMIoTCloudProviderType, func(config providerconfig.Config) (providersdk.Provider, error) {
		return xiaomi.NewCloudProviderFromConfigWithSpecResolver(config, xiaomiSpecs)
	}); err != nil {
		logger.Error("xiaomi MIoT cloud provider factory registration failed", zap.Error(err))
		os.Exit(1)
	}
	if err := factory.Register(cameraprovider.ProviderType, func(config providerconfig.Config) (providersdk.Provider, error) {
		return cameraprovider.NewProviderFromConfigWithXiaomiCredentialResolver(config, func(id string) (xiaomi.CloudConfig, error) {
			return resolveXiaomiCameraCredentials(providerManager, id)
		})
	}); err != nil {
		logger.Error("camera provider factory registration failed", zap.Error(err))
		os.Exit(1)
	}
	providerInstances := make([]providersdk.Provider, 0, len(providerConfigs))
	for _, providerConfig := range providerConfigs {
		if !providerConfig.Enabled {
			continue
		}
		instance, createErr := factory.Create(providerConfig)
		if createErr != nil {
			logger.Error("provider creation failed", zap.String("provider_id", providerConfig.ID), zap.Error(createErr))
			continue
		}
		providerInstances = append(providerInstances, instance)
	}
	providerManager, err = providermanager.New(providerInstances...)
	if err != nil {
		logger.Error("provider manager creation failed", zap.Error(err))
		os.Exit(1)
	}
	if err := providerManager.Initialize(ctx); err != nil {
		logger.Error("provider initialization failed", zap.Error(err))
		os.Exit(1)
	}
	for _, info := range providerManager.ProviderInfos() {
		if info.Status != "running" {
			logger.Warn("provider is not running", zap.String("provider_id", info.Manifest.ID), zap.String("provider_type", info.Manifest.Type), zap.String("status", info.Status), zap.String("error", info.Error))
		}
	}
	// Media authorization must not depend on DeviceService's constructor
	// silently succeeding later in startup. Validate and populate ordinary
	// Device routes before the Media Worker can connect.
	if _, err := providerManager.DiscoverDevices(ctx); err != nil {
		logger.Error("initial device discovery failed", zap.Error(err))
		os.Exit(1)
	}
	if err := reconcileDiscoveredMedia(ctx, providerManager, store, providerConfigs); err != nil {
		logger.Error("media source reconciliation failed", zap.Error(err))
		os.Exit(1)
	}
	if moved, err := migrateLegacyCameraPublisherRuntimeDir(settings.Media.RuntimeDir); err != nil {
		logger.Error("camera publisher runtime migration failed", zap.Error(err))
		os.Exit(1)
	} else if moved > 0 {
		logger.Info("camera publisher runtime migrated from legacy cache path", zap.Int("count", moved))
	}
	if removed, err := pruneOrphanedCameraPublisherDirectories(ctx, settings.Media.RuntimeDir, store); err != nil {
		logger.Error("orphaned camera publisher cleanup failed", zap.Error(err))
		os.Exit(1)
	} else if removed > 0 {
		logger.Info("orphaned camera publisher directories removed", zap.Int("count", removed))
	}
	profileService, err := application.NewProfileService(ctx, store)
	if err != nil {
		logger.Error("mapping profile load failed", zap.Error(err))
		os.Exit(1)
	}
	knownProviders := make(map[string]struct{}, len(providerConfigs))
	configuredProviderDevices := make(map[string]map[string]struct{}, len(providerConfigs))
	for _, providerConfig := range providerConfigs {
		knownProviders[providerConfig.ID] = struct{}{}
		if deviceIDs, authoritative := application.ConfiguredProviderDeviceIDs(providerConfig); authoritative {
			configuredProviderDevices[providerConfig.ID] = deviceIDs
		}
	}
	prunedBindings, err := profileService.PruneOrphanedBindings(ctx, knownProviders, configuredProviderDevices)
	if err != nil {
		logger.Error("orphaned mapping binding cleanup failed", zap.Error(err))
		os.Exit(1)
	}
	if prunedBindings > 0 {
		logger.Info("orphaned mapping bindings pruned", zap.Int("count", prunedBindings))
	}
	syncProviderPropertyInterests := func() {
		bindings := profileService.ListBindings()
		interests := make([]providersdk.PropertyInterest, 0, len(bindings))
		for _, binding := range bindings {
			if !binding.Enabled || binding.EffectiveStage() != mapping.StageProvider {
				continue
			}
			interests = append(interests, providersdk.PropertyInterest{ProviderID: binding.ProviderID, DeviceID: binding.DeviceID, EndpointID: binding.EndpointID, CapabilityID: binding.CapabilityID, PropertyID: binding.PropertyID})
		}
		providerManager.SetPropertyInterests(interests)
	}
	syncProviderPropertyInterests()
	service := application.NewDeviceService(providerManager, store, profileService)
	defer service.Close()
	if err := service.LoadDevicePreferences(ctx); err != nil {
		logger.Error("device preference load failed", zap.Error(err))
		os.Exit(1)
	}
	if settings.Media.Enabled {
		mediaRuntime, err = newEmbeddedMediaRuntime(ctx, store, providerManager, embeddedMediaConfig{
			CameraKernelBinary: settings.Media.CameraKernelBinary,
			RuntimeDir:         settings.Media.RuntimeDir,
			HAPHost:            settings.Media.HAPHost,
			HAPPortBase:        settings.Media.HAPPortBase,
			RTSPPortBase:       settings.Media.RTSPPortBase,
			SRTPPortBase:       settings.Media.SRTPPortBase,
			ChildLogLevel:      strings.ToLower(settings.Logging.ChildLevel),
			ChildLogWriter:     childLogs.Writer,
		}, logger)
		if err != nil {
			logger.Error("embedded media runtime initialization failed", zap.Error(err))
			os.Exit(1)
		}
	}
	mediaService := application.NewMediaService(gormMediaStreamStore{store: store}, mediaRuntime)
	cameraPublications := newCameraTargetPublication(mediaService, service, settings.Media.RuntimeDir, settings.Media.HAPPortBase)
	settingsService, err := application.NewSettingsService(ctx, store, service)
	if err != nil {
		logger.Error("runtime settings load failed", zap.Error(err))
		os.Exit(1)
	}
	providerService := application.NewProviderService(providerConfigs, store, factory, providerManager)
	providerService.StartCredentialMaintenance(ctx)
	targetConfigs, err := store.ListTargets(ctx)
	if err != nil {
		logger.Error("target configuration load failed", zap.Error(err))
		os.Exit(1)
	}
	registrations := make([]application.TargetRegistration, 0, len(targetConfigs))
	manager := targetmanager.New(ctx, service, logger, store, cameraPublications, targetmanager.SubprocessLogging{
		Level: strings.ToLower(settings.Logging.ChildLevel), Writer: childLogs.Writer,
	})
	for _, targetConfig := range targetConfigs {
		registration, targetErr := manager.Apply(ctx, targetConfig)
		if targetErr != nil {
			registration.Info = application.TargetInfoFromConfig(targetConfig, "error")
			registration.Info.Error = targetErr.Error()
			logger.Error("target initialization failed", zap.String("target_id", targetConfig.ID), zap.Error(targetErr))
		}
		registrations = append(registrations, registration)
	}
	targetService := application.NewTargetService(registrations, store, targetConfigs...)
	targetService.SetRuntime(manager)
	manager.SetStatusHandler(targetService.SetStatus)
	profileService.SetChangeHandler(func(changeCtx context.Context) {
		syncProviderPropertyInterests()
		refreshCtx, cancelRefresh := context.WithTimeout(context.WithoutCancel(changeCtx), 15*time.Second)
		defer cancelRefresh()
		if refreshErr := service.RefreshDevices(refreshCtx); refreshErr != nil {
			logger.Error("mapping hot reload failed", zap.Error(refreshErr))
			return
		}
		if refreshErr := targetService.Refresh(refreshCtx); refreshErr != nil {
			logger.Error("consumer mapping target refresh failed", zap.Error(refreshErr))
		}
	})
	providerService.SetChangeHandler(func(changeCtx context.Context, item providerconfig.Config, deleted bool) error {
		configuredDevices := map[string]struct{}{}
		authoritative := deleted
		if !deleted {
			configuredDevices, authoritative = application.ConfiguredProviderDeviceIDs(item)
		}
		if authoritative {
			pruned, pruneErr := profileService.PruneProviderBindings(changeCtx, item.ID, configuredDevices)
			if pruneErr != nil {
				return fmt.Errorf("prune mapping bindings for provider %q: %w", item.ID, pruneErr)
			}
			if pruned > 0 {
				logger.Info("provider mapping bindings pruned", zap.String("provider_id", item.ID), zap.Int("count", pruned))
			}
		}
		if item.Type == cameraprovider.ProviderType {
			infos := providerService.List()
			configs := make([]providerconfig.Config, 0, len(infos))
			for _, info := range infos {
				configs = append(configs, info.Config)
			}
			if err := reconcileDiscoveredMedia(changeCtx, providerManager, store, configs); err != nil {
				return fmt.Errorf("reconcile Camera Provider media: %w", err)
			}
			if mediaRuntime != nil {
				if err := mediaRuntime.Replay(changeCtx); err != nil {
					return fmt.Errorf("replay embedded media runtime: %w", err)
				}
				removed, err := pruneOrphanedCameraPublisherDirectories(changeCtx, settings.Media.RuntimeDir, store)
				if err != nil {
					return fmt.Errorf("clean orphaned Camera Provider publishers: %w", err)
				}
				if removed > 0 {
					logger.Info("orphaned camera publisher directories removed", zap.Int("count", removed))
				}
			}
		}
		return nil
	})
	server := httpapi.NewServer(settings.Server.Address, service, targetService, logger, providerService)
	if err := server.SetTrustedProxies(settings.Server.TrustedProxies); err != nil {
		logger.Error("trusted proxy configuration failed", zap.Error(err))
		os.Exit(1)
	}
	server.SetAuthService(application.NewAuthService(store))
	server.SetMaintenanceService(application.NewMaintenanceService(store, settings.Storage.MasterKey, gormstore.ValidateRestoreCandidate, gormstore.PendingRestorePaths, gormstore.WritePendingRestoreMarker))
	server.SetSettingsService(settingsService)
	server.SetSubprocessLogs(childLogs)
	auditService := application.NewAuditService(store)
	server.SetAuditService(auditService)
	server.SetProfileService(profileService)
	server.SetExportService(application.NewExportService(service, providerService, targetService, settingsService, auditService, profileService))
	server.SetMediaService(mediaService)
	if settings.Media.Enabled {
		server.SetMediaPreview(settings.Media.RuntimeDir)
	}

	go func() {
		if err := server.Start(); err != nil {
			logger.Error("http server stopped", zap.Error(err))
			stop()
		}
	}()
	version := buildinfo.Current()
	logger.Info("HomeLoom demo started", zap.String("address", server.Address()), zap.String("version", version.Version), zap.String("commit", version.Commit), zap.String("build_time", version.BuildTime))
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if mediaRuntime != nil {
		if err := mediaRuntime.Close(); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("embedded media runtime shutdown failed", zap.Error(err))
		}
	}
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", zap.Error(err))
	}
	if err := manager.Close(shutdownCtx); err != nil {
		logger.Error("target shutdown failed", zap.Error(err))
	}
}

func parseMainLogLevel(value string) (zapcore.Level, error) {
	level, err := logging.ParseLevel(value)
	if err != nil || level < zapcore.DebugLevel || level > zapcore.ErrorLevel {
		return 0, fmt.Errorf("invalid -log-level %q: expected debug, info, warn, or error", value)
	}
	return level, nil
}

// resolveXiaomiCameraCredentials resolves a Camera Provider's account
// reference at authorization time. A configured Provider that is temporarily
// not running is a recoverable runtime condition: media replay can be retried
// once the Provider becomes available. Missing references and
// malformed credentials remain structural errors so they continue to fail
// startup rather than being silently deferred.
func resolveXiaomiCameraCredentials(manager *providermanager.Manager, id string) (xiaomi.CloudConfig, error) {
	if manager == nil {
		return xiaomi.CloudConfig{}, errors.New("provider runtime is not initialized")
	}
	instance, ok := manager.Provider(id)
	if !ok {
		for _, info := range manager.ProviderInfos() {
			if info.Manifest.ID == id {
				return xiaomi.CloudConfig{}, fmt.Errorf("%w: referenced provider is not running", providersdk.ErrProviderUnavailable)
			}
		}
		return xiaomi.CloudConfig{}, fmt.Errorf("referenced provider %q does not exist", id)
	}
	account, ok := instance.(interface{ CameraAccountCredentials() xiaomi.CloudConfig })
	if !ok {
		return xiaomi.CloudConfig{}, errors.New("referenced provider does not expose Xiaomi camera credentials")
	}
	credentials := account.CameraAccountCredentials()
	if strings.TrimSpace(credentials.UserID) == "" || strings.TrimSpace(credentials.PassToken) == "" {
		return xiaomi.CloudConfig{}, errors.New("referenced provider has no Xiaomi MISS userId/passToken session")
	}
	return credentials, nil
}
