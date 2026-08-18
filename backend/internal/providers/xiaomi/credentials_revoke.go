package xiaomi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

// ErrCredentialsRevoked prevents a deliberately cleared Provider from being
// accidentally started again. Re-authorize it through the normal setup flow.
var ErrCredentialsRevoked = errors.New("Xiaomi provider credentials were revoked; authorize it again before enabling")

type oauthTokenRevoker func(context.Context, OAuthConfig) error

// RevokeCredentials removes every credential used by the central-hub
// Provider. Remote revocation has no Xiaomi default: it is attempted only for
// an explicit, validated endpoint. A remote failure is returned as status
// data, while the local replacement remains safe to persist.
func (p *Provider) RevokeCredentials(ctx context.Context) (providersdk.CredentialRevocation, error) {
	p.mu.RLock()
	config := p.config
	revoker := p.remoteTokenRevoker
	p.mu.RUnlock()

	result := providersdk.CredentialRevocation{}
	if config.OAuth != nil && strings.TrimSpace(config.OAuth.RevocationEndpoint) != "" && !config.CredentialsRevoked {
		result.RemoteAttempted = true
		if revoker == nil {
			result.RemoteError = "configured Xiaomi OAuth revocation client is unavailable"
		} else if err := revoker(ctx, *config.OAuth); err != nil {
			// Do not include endpoint URLs or request errors here: either can
			// contain sensitive values in custom transports.
			result.RemoteError = "configured Xiaomi OAuth token revocation failed"
		} else {
			result.RemoteRevoked = true
		}
	}
	config = revokedCentralConfig(config)
	encoded, err := marshalRevokedConfig(config)
	if err != nil {
		return providersdk.CredentialRevocation{}, err
	}
	result.Config = encoded
	return result, nil
}

// RevokeCredentials removes local account/password/session material for the
// third-party compatibility Provider. There is intentionally no remote call:
// this adapter has no verified Xiaomi logout/revocation API contract.
func (p *CloudProvider) RevokeCredentials(context.Context) (providersdk.CredentialRevocation, error) {
	p.mu.RLock()
	config := p.config
	p.mu.RUnlock()
	config = revokedCloudConfig(config)
	encoded, err := marshalRevokedConfig(config)
	if err != nil {
		return providersdk.CredentialRevocation{}, err
	}
	return providersdk.CredentialRevocation{Config: encoded}, nil
}

func revokedCentralConfig(config Config) Config {
	config.CredentialsRevoked = true
	config.ClientID = ""
	config.CACertificate = ""
	config.ClientCertificate = ""
	config.PrivateKey = ""
	config.OAuth = nil
	return config
}

func revokedCloudConfig(config CloudConfig) CloudConfig {
	config.CredentialsRevoked = true
	config.Username = ""
	config.Password = ""
	config.UserID = ""
	config.Ssecurity = ""
	config.ServiceToken = ""
	config.PassToken = ""
	return config
}

func marshalRevokedConfig(value any) ([]byte, error) {
	encoded, err := jsonMarshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode revoked Xiaomi credentials: %w", err)
	}
	return encoded, nil
}

// jsonMarshal is a variable solely to make the failure path injectable if a
// future config contains custom marshalers. Normal configurations cannot
// fail, but the error contract must not silently report local success.
var jsonMarshal = func(value any) ([]byte, error) {
	return json.Marshal(value)
}

// revokeOAuthTokens implements the standard OAuth token-revocation request
// only when the endpoint has already been explicitly configured. The project
// does not infer or publish a Xiaomi revocation URL.
func revokeOAuthTokens(ctx context.Context, config OAuthConfig) error {
	return revokeOAuthTokensWithClient(ctx, config, &http.Client{Timeout: 15 * time.Second})
}

func revokeOAuthTokensWithClient(ctx context.Context, config OAuthConfig, client *http.Client) error {
	endpoint, err := url.Parse(config.RevocationEndpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return errors.New("configured OAuth revocation endpoint is invalid")
	}
	if client == nil {
		return errors.New("configured OAuth token revocation client is unavailable")
	}
	for _, token := range []struct {
		value string
		hint  string
	}{
		{value: strings.TrimSpace(config.RefreshToken), hint: "refresh_token"},
		{value: strings.TrimSpace(config.AccessToken), hint: "access_token"},
	} {
		if token.value == "" {
			continue
		}
		form := url.Values{"token": {token.value}, "token_type_hint": {token.hint}}
		if clientID := strings.TrimSpace(config.ClientID); clientID != "" {
			form.Set("client_id", clientID)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form.Encode()))
		if err != nil {
			return errors.New("create configured OAuth token revocation request")
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := client.Do(request)
		if err != nil {
			return errors.New("call configured OAuth token revocation endpoint")
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8<<10))
		response.Body.Close()
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("configured OAuth token revocation endpoint returned HTTP %d", response.StatusCode)
		}
	}
	return nil
}
