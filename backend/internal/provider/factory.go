package provider

import (
	"fmt"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

type FactoryFunc func(providerconfig.Config) (Provider, error)

type Factory struct {
	constructors map[string]FactoryFunc
}

func NewFactory() *Factory { return &Factory{constructors: make(map[string]FactoryFunc)} }

func (f *Factory) Register(providerType string, constructor FactoryFunc) error {
	if providerType == "" || constructor == nil {
		return fmt.Errorf("provider type and constructor are required")
	}
	if _, exists := f.constructors[providerType]; exists {
		return fmt.Errorf("provider type %q is already registered", providerType)
	}
	f.constructors[providerType] = constructor
	return nil
}

func (f *Factory) Create(config providerconfig.Config) (Provider, error) {
	constructor, exists := f.constructors[config.Type]
	if !exists {
		return nil, fmt.Errorf("provider type %q is not registered", config.Type)
	}
	provider, err := constructor(config)
	if err != nil {
		return nil, fmt.Errorf("create provider %q: %w", config.ID, err)
	}
	return provider, nil
}
