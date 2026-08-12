package main

import (
	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/network"
)

// registerNetworkProvider keeps the network monitoring and Wake-on-LAN
// Provider available wherever the main binary builds its Provider factory.
func registerNetworkProvider(factory *providersdk.Factory) error {
	return factory.Register(network.ProviderType, func(config providerconfig.Config) (providersdk.Provider, error) {
		return network.NewProviderFromConfig(config)
	})
}
