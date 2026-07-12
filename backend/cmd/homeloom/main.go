package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/feranydev/homeloom/backend/internal/application"
	"github.com/feranydev/homeloom/backend/internal/config"
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	"github.com/feranydev/homeloom/backend/internal/persistence/sqlite"
	"github.com/feranydev/homeloom/backend/internal/platform/httpapi"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/virtual"
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
	"github.com/feranydev/homeloom/backend/internal/runtime/targetmanager"
)

func main() {
	configPath := flag.String("config", "", "path to a YAML configuration file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	settings, err := config.Load(*configPath)
	if err != nil {
		logger.Error("configuration failed", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := sqlite.Open(ctx, settings.Storage.Database)
	if err != nil {
		logger.Error("database initialization failed", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	providerConfigs, err := store.ListProviders(ctx)
	if err != nil {
		logger.Error("provider configuration load failed", "error", err)
		os.Exit(1)
	}
	factory := providersdk.NewFactory()
	if err := factory.Register("virtual", func(config providerconfig.Config) (providersdk.Provider, error) {
		return virtual.NewProviderWithIdentity(config.ID, config.Name), nil
	}); err != nil {
		logger.Error("provider factory registration failed", "error", err)
		os.Exit(1)
	}
	providerInstances := make([]providersdk.Provider, 0, len(providerConfigs))
	for _, providerConfig := range providerConfigs {
		if !providerConfig.Enabled {
			continue
		}
		instance, createErr := factory.Create(providerConfig)
		if createErr != nil {
			logger.Error("provider creation failed", "provider_id", providerConfig.ID, "error", createErr)
			continue
		}
		providerInstances = append(providerInstances, instance)
	}
	providerManager, err := providermanager.New(providerInstances...)
	if err != nil {
		logger.Error("provider manager creation failed", "error", err)
		os.Exit(1)
	}
	if err := providerManager.Initialize(ctx); err != nil {
		logger.Error("provider initialization failed", "error", err)
		os.Exit(1)
	}
	service := application.NewDeviceService(providerManager)
	defer service.Close()
	providerService := application.NewProviderService(providerConfigs, store, factory, providerManager)
	targetConfigs, err := store.ListTargets(ctx)
	if err != nil {
		logger.Error("target configuration load failed", "error", err)
		os.Exit(1)
	}
	registrations := make([]application.TargetRegistration, 0, len(targetConfigs))
	manager := targetmanager.New(ctx, service, logger)
	for _, targetConfig := range targetConfigs {
		registration, targetErr := manager.Apply(ctx, targetConfig)
		if targetErr != nil {
			registration.Info = application.TargetInfo{
				ID: targetConfig.ID, Type: targetConfig.Type, Name: targetConfig.Name,
				Enabled: targetConfig.Enabled, Status: "error", Address: targetConfig.Address,
				SetupID: targetConfig.SetupID, DeviceIDs: append([]string{}, targetConfig.DeviceIDs...),
				Error: targetErr.Error(),
			}
			logger.Error("target initialization failed", "target_id", targetConfig.ID, "error", targetErr)
		}
		registrations = append(registrations, registration)
	}
	targetService := application.NewTargetService(registrations, store)
	targetService.SetRuntime(manager)
	manager.SetStatusHandler(targetService.SetStatus)
	server := httpapi.NewServer(settings.Server.Address, service, targetService, logger, providerService)

	go func() {
		if err := server.Start(); err != nil {
			logger.Error("http server stopped", "error", err)
			stop()
		}
	}()
	logger.Info("HomeLoom demo started", "address", server.Address())
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
	if err := manager.Close(shutdownCtx); err != nil {
		logger.Error("target shutdown failed", "error", err)
	}
}
