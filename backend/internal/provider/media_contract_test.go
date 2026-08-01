package provider

import (
	"context"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/media"
)

type mediaCapabilityStub struct{}

func (mediaCapabilityStub) DiscoverMediaSources(context.Context) ([]media.MediaSourceDescriptor, error) {
	return nil, nil
}

func (mediaCapabilityStub) RefreshMediaSource(context.Context, string) (*media.MediaSourceDescriptor, error) {
	return nil, nil
}

func (mediaCapabilityStub) AcquireMediaAuthorization(context.Context, media.AuthorizationRequest) (*media.AuthorizationResponse, error) {
	return nil, nil
}

func TestMediaCapabilitiesRemainOptionalSmallInterfaces(t *testing.T) {
	var implementation any = mediaCapabilityStub{}
	if _, ok := implementation.(MediaSourceDiscoverer); !ok {
		t.Fatal("media discovery capability contract is not satisfied")
	}
	if _, ok := implementation.(MediaSourceRefresher); !ok {
		t.Fatal("media refresh capability contract is not satisfied")
	}
	if _, ok := implementation.(MediaAuthorizer); !ok {
		t.Fatal("media authorization capability contract is not satisfied")
	}
}
