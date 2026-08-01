package source

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/feranydev/homeloom/backend/internal/mediaruntime/contract"
)

func TestNetworkResolverBuildsAuthenticatedRTSPURIOnlyInMemory(t *testing.T) {
	auth := &networkAuthorizationClient{response: contract.AuthorizationResponse{
		SchemaVersion: 1, LeaseID: "lease-rtsp", ExpiresAt: time.Now().Add(time.Minute),
		Endpoint: contract.EndpointSpec{Protocol: "rtsp", Host: "192.0.2.10", Port: 554, Path: "/live/main"},
		AuthType: "basic", SecretMaterial: json.RawMessage(`{"username":"viewer","password":"secret"}`), MaxUses: 1,
	}}
	resolver := &Resolver{}
	resolver.network.setAuthClient(auth)
	source, err := resolver.Resolve(context.Background(), contract.StreamSpec{DeviceID: "front-door", Protocol: "rtsp"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(source.URI)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := parsed.User.Password()
	if parsed.Scheme != "rtsp" || parsed.Host != "192.0.2.10:554" || parsed.Path != "/live/main" ||
		parsed.User.Username() != "viewer" || password != "secret" {
		t.Fatalf("RTSP source URI = %s", source.URI)
	}
	if len(source.PublicMaterial) != 0 || len(source.SecretMaterial) != 0 {
		t.Fatalf("network credentials duplicated outside URI: %#v", source)
	}
}

func TestNetworkResolverBuildsONVIFProfileURI(t *testing.T) {
	auth := &networkAuthorizationClient{response: contract.AuthorizationResponse{
		SchemaVersion: 1, LeaseID: "lease-onvif", ExpiresAt: time.Now().Add(time.Minute),
		Endpoint: contract.EndpointSpec{Protocol: "onvif", Host: "192.0.2.11", Port: 80, Path: "profile-main"},
		AuthType: "digest", SecretMaterial: json.RawMessage(`{"username":"viewer","password":"secret"}`), MaxUses: 1,
	}}
	resolver := &Resolver{}
	resolver.network.setAuthClient(auth)
	source, err := resolver.Resolve(context.Background(), contract.StreamSpec{DeviceID: "garage", Protocol: "onvif"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(source.URI)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "onvif" || parsed.Query().Get("subtype") != "profile-main" {
		t.Fatalf("ONVIF source URI = %s", source.URI)
	}
}

func TestResolverRejectsProtocolsOutsideKernelScope(t *testing.T) {
	resolver := &Resolver{}
	if _, err := resolver.Resolve(context.Background(), contract.StreamSpec{Protocol: "tapo"}); err == nil {
		t.Fatal("unsupported fourth protocol was accepted")
	}
}

type networkAuthorizationClient struct {
	response contract.AuthorizationResponse
	request  contract.AuthorizationRequest
}

func (f *networkAuthorizationClient) Acquire(_ context.Context, request contract.AuthorizationRequest) (contract.AuthorizationResponse, error) {
	f.request = request
	return f.response, nil
}
