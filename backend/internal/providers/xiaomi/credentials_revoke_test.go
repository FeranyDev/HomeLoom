package xiaomi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/domain/providerconfig"
)

type revocationRoundTripper func(*http.Request) (*http.Response, error)

func (f revocationRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestCentralProviderCredentialRevocationClearsLocalSecretsAfterRemoteFailure(t *testing.T) {
	config := testConfig()
	config.Host, config.ClientID = "192.0.2.10", "123456789"
	config.CACertificate, config.ClientCertificate, config.PrivateKey = "ca", "certificate", "private-key"
	config.OAuth = &OAuthConfig{ClientID: "100", AccessToken: "access-secret", RefreshToken: "refresh-secret", RevocationEndpoint: "https://revocation.example.invalid/oauth/revoke"}
	provider, err := newProvider("xiaomi-main", "Xiaomi", config, func() hubClient { return &fakeHub{} })
	if err != nil {
		t.Fatal(err)
	}
	provider.remoteTokenRevoker = func(context.Context, OAuthConfig) error { return errors.New("endpoint failed") }

	result, err := provider.RevokeCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.RemoteAttempted || result.RemoteRevoked || result.RemoteError == "" {
		t.Fatalf("remote result = %#v", result)
	}
	encoded := string(result.Config)
	for _, secret := range []string{"access-secret", "refresh-secret", "private-key", "certificate", `"clientId":"123456789"`} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("revoked configuration exposed %q: %s", secret, encoded)
		}
	}
	var cleared Config
	if err := json.Unmarshal(result.Config, &cleared); err != nil {
		t.Fatal(err)
	}
	if !cleared.CredentialsRevoked || cleared.OAuth != nil || cleared.Host != config.Host || len(cleared.Devices) != len(config.Devices) {
		t.Fatalf("cleared central config = %#v", cleared)
	}
}

func TestCloudProviderCredentialRevocationClearsAccountAndSessionLocally(t *testing.T) {
	config := cloudTestConfig()
	config.Username, config.Password = "owner@example.com", "password"
	config.UserID, config.Ssecurity, config.ServiceToken, config.PassToken = "1", "ssecurity", "service-token", "pass-token"
	provider, err := newCloudProvider("xiaomi-cloud", "Xiaomi cloud", config, func() miotCloudClient { return &fakeMIoTCloud{} }, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.RevokeCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.RemoteAttempted || result.RemoteRevoked || result.RemoteError != "" {
		t.Fatalf("cloud provider unexpectedly attempted remote revocation: %#v", result)
	}
	encoded := string(result.Config)
	for _, secret := range []string{"owner@example.com", "password", "ssecurity", "service-token", "pass-token"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("revoked cloud config exposed %q: %s", secret, encoded)
		}
	}
}

func TestConfiguredOAuthRevocationUsesOnlyExplicitEndpoint(t *testing.T) {
	var received []url.Values
	client := &http.Client{Transport: revocationRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://revocation.example.invalid/oauth/revoke" || request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatalf("request = %#v", request)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		received = append(received, values)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	config := OAuthConfig{ClientID: "100", AccessToken: "access", RefreshToken: "refresh", RevocationEndpoint: "https://revocation.example.invalid/oauth/revoke"}
	if err := revokeOAuthTokensWithClient(context.Background(), config, client); err != nil {
		t.Fatal(err)
	}
	if len(received) != 2 || received[0].Get("token") != "refresh" || received[0].Get("token_type_hint") != "refresh_token" || received[1].Get("token") != "access" || received[1].Get("token_type_hint") != "access_token" || received[0].Get("client_id") != "100" {
		t.Fatalf("revocation requests = %#v", received)
	}
}

func TestRevokedXiaomiConfigurationsStayDisabledOnly(t *testing.T) {
	central := providerconfig.Config{Enabled: false, Config: []byte(`{"credentialsRevoked":true,"devices":[]}`)}
	if _, err := NewProviderFromConfig(central); err != nil {
		t.Fatalf("disabled revoked central configuration = %v", err)
	}
	central.Enabled = true
	if _, err := NewProviderFromConfig(central); err == nil {
		t.Fatal("enabled revoked central configuration was accepted")
	}
	cloud := providerconfig.Config{Enabled: false, Config: []byte(`{"credentialsRevoked":true,"devices":[]}`)}
	if _, err := NewCloudProviderFromConfig(cloud); err != nil {
		t.Fatalf("disabled revoked cloud configuration = %v", err)
	}
	cloud.Enabled = true
	if _, err := NewCloudProviderFromConfig(cloud); err == nil {
		t.Fatal("enabled revoked cloud configuration was accepted")
	}
}
