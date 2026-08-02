package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
	"github.com/feranydev/homeloom/backend/internal/providers/xiaomi"
	"github.com/feranydev/homeloom/backend/internal/runtime/providermanager"
)

func TestResolveXiaomiCameraCredentialsClassifiesProviderReferenceErrors(t *testing.T) {
	tests := []struct {
		name            string
		provider        providersdk.Provider
		initialize      bool
		wantUnavailable bool
		wantMessage     string
	}{
		{
			name: "provider exists but is not running",
			provider: &resolverProvider{
				id:      "xiaomi-account",
				initErr: errors.New("login failed"),
			},
			initialize:      true,
			wantUnavailable: true,
			wantMessage:     "not running",
		},
		{
			name:            "provider is missing",
			wantUnavailable: false,
			wantMessage:     "does not exist",
		},
		{
			name: "provider has wrong type",
			provider: &resolverPlainProvider{
				id: "xiaomi-account",
			},
			initialize:      true,
			wantUnavailable: false,
			wantMessage:     "does not expose Xiaomi camera credentials",
		},
		{
			name: "provider credentials are incomplete",
			provider: &resolverProvider{
				id:          "xiaomi-account",
				credentials: xiaomi.CloudConfig{UserID: "user-only"},
			},
			initialize:      true,
			wantUnavailable: false,
			wantMessage:     "no Xiaomi MISS userId/passToken session",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			providers := []providersdk.Provider{}
			if test.provider != nil {
				providers = append(providers, test.provider)
			}
			manager, err := providermanager.New(providers...)
			if err != nil {
				t.Fatal(err)
			}
			if test.initialize {
				if err := manager.Initialize(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			defer func() {
				if err := manager.Close(context.Background()); err != nil {
					t.Errorf("close manager: %v", err)
				}
			}()

			_, err = resolveXiaomiCameraCredentials(manager, "xiaomi-account")
			if (errors.Is(err, providersdk.ErrProviderUnavailable)) != test.wantUnavailable {
				t.Fatalf("error = %v, unavailable = %v, want %v", err, errors.Is(err, providersdk.ErrProviderUnavailable), test.wantUnavailable)
			}
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("error = %v, want message containing %q", err, test.wantMessage)
			}
		})
	}
}

type resolverProvider struct {
	id          string
	initErr     error
	credentials xiaomi.CloudConfig
}

func (p *resolverProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "resolver-test", Name: "Resolver Test", Version: "test"}
}

func (*resolverProvider) Capabilities() providersdk.Capabilities { return providersdk.Capabilities{} }

func (p *resolverProvider) Initialize(context.Context) error { return p.initErr }

func (*resolverProvider) Close(context.Context) error { return nil }

func (p *resolverProvider) CameraAccountCredentials() xiaomi.CloudConfig { return p.credentials }

type resolverPlainProvider struct {
	id      string
	initErr error
}

func (p *resolverPlainProvider) Manifest() providersdk.Manifest {
	return providersdk.Manifest{ID: p.id, Type: "resolver-test", Name: "Resolver Test", Version: "test"}
}

func (*resolverPlainProvider) Capabilities() providersdk.Capabilities {
	return providersdk.Capabilities{}
}

func (p *resolverPlainProvider) Initialize(context.Context) error { return p.initErr }

func (*resolverPlainProvider) Close(context.Context) error { return nil }
