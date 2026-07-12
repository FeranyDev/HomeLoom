package provider

import (
	"context"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

type factoryProvider struct{ id string }

func (p *factoryProvider) Manifest() Manifest             { return Manifest{ID: p.id} }
func (*factoryProvider) Capabilities() Capabilities       { return Capabilities{} }
func (*factoryProvider) Initialize(context.Context) error { return nil }
func (*factoryProvider) Close(context.Context) error      { return nil }

func TestFactoryRegistrationAndCreate(t *testing.T) {
	factory := NewFactory()
	constructor := func(config providerconfig.Config) (Provider, error) { return &factoryProvider{id: config.ID}, nil }
	if err := factory.Register("test", constructor); err != nil {
		t.Fatal(err)
	}
	if err := factory.Register("test", constructor); err == nil {
		t.Fatal("duplicate registration was accepted")
	}
	created, err := factory.Create(providerconfig.Config{ID: "one", Type: "test"})
	if err != nil || created.Manifest().ID != "one" {
		t.Fatalf("Create() = %#v, %v", created, err)
	}
	if _, err := factory.Create(providerconfig.Config{Type: "missing"}); err == nil {
		t.Fatal("unknown provider type was accepted")
	}
}
