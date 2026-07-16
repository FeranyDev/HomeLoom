package xiaomi

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

const maximumCertificateRenewalLead = 7 * 24 * time.Hour

func credentialStatus(config Config, now time.Time) (providersdk.CredentialStatus, error) {
	if config.OAuth == nil || strings.TrimSpace(config.OAuth.RefreshToken) == "" {
		return providersdk.CredentialStatus{}, nil
	}
	status := providersdk.CredentialStatus{Managed: true}
	if config.OAuth.ExpiresAt > 0 {
		status.TokenExpiresAt = time.Unix(config.OAuth.ExpiresAt, 0).UTC()
	}
	tokenRefreshAt := time.Unix(config.OAuth.RefreshAfter, 0).UTC()
	if config.OAuth.RefreshAfter <= 0 || strings.TrimSpace(config.OAuth.AccessToken) == "" {
		tokenRefreshAt = now
	}
	if config.OAuth.ExpiresAt > 0 {
		tokenRefreshAt = earlierTime(tokenRefreshAt, time.Unix(config.OAuth.ExpiresAt, 0).UTC().Add(-5*time.Minute))
	}
	status.RefreshAt = tokenRefreshAt
	certificate, err := parseClientCertificate(config.ClientCertificate)
	if err != nil {
		// A malformed or missing leaf can still be replaced using the persisted
		// private key, UID and refresh token.
		status.RefreshAt = earlierTime(status.RefreshAt, now)
		return status, nil
	}
	status.CertificateExpiresAt = certificate.NotAfter.UTC()
	certificateRefreshAt := certificate.NotAfter.Add(-certificateRenewalLead(*certificate))
	status.RefreshAt = earlierTime(status.RefreshAt, certificateRefreshAt)
	return status, nil
}

func renewCredentials(ctx context.Context, config Config, httpClient *http.Client, now time.Time) (Config, error) {
	status, err := credentialStatus(config, now)
	if err != nil {
		return Config{}, err
	}
	if !status.Managed {
		return Config{}, errors.New("Xiaomi OAuth refresh token is not configured")
	}
	if status.RefreshAt.After(now) {
		return config, nil
	}
	oauth := *config.OAuth
	client := oauthClient{config: OAuthStartRequest{ClientID: oauth.ClientID, Region: oauth.Region, RedirectURL: oauth.RedirectURL, OAuthUUID: oauth.OAuthUUID, VirtualDID: oauth.VirtualDID}, http: httpClient}
	if err := client.validate(); err != nil {
		return Config{}, fmt.Errorf("validate Xiaomi OAuth renewal identity: %w", err)
	}
	certificate, certificateErr := parseClientCertificate(config.ClientCertificate)
	certificateDue := certificateErr != nil || !certificate.NotAfter.Add(-certificateRenewalLead(*certificate)).After(now)
	// Always rotate the access token before requesting a new certificate. A
	// provider may have had its access token revoked before its advertised
	// expiry, while its refresh token is still valid.
	tokenDue := certificateDue || oauth.RefreshAfter <= 0 || oauth.RefreshAfter <= now.Unix() || oauth.ExpiresAt <= now.Add(5*time.Minute).Unix() || strings.TrimSpace(oauth.AccessToken) == ""
	if tokenDue {
		token, refreshErr := client.refresh(ctx, oauth.RefreshToken)
		if refreshErr != nil {
			return Config{}, fmt.Errorf("refresh Xiaomi OAuth token: %w", refreshErr)
		}
		oauth.AccessToken, oauth.RefreshToken = token.AccessToken, token.RefreshToken
		oauth.RefreshAfter, oauth.ExpiresAt = token.RefreshAfter, token.ExpiresAt
	}
	if certificateDue {
		privateKey, keyErr := parseEd25519PrivateKey(config.PrivateKey)
		if keyErr != nil {
			return Config{}, keyErr
		}
		if strings.TrimSpace(oauth.UID) == "" {
			return Config{}, errors.New("Xiaomi OAuth account UID is required for certificate renewal")
		}
		csr, csrErr := createCSR(oauth.UID, oauth.VirtualDID, privateKey)
		if csrErr != nil {
			return Config{}, csrErr
		}
		certificate, certificateErr := client.certificate(ctx, oauth.AccessToken, csr)
		if certificateErr != nil {
			return Config{}, fmt.Errorf("renew Xiaomi central certificate: %w", certificateErr)
		}
		if verifyErr := verifyRenewedClientCertificate(certificate, oauth.UID, oauth.VirtualDID, privateKey, now); verifyErr != nil {
			return Config{}, verifyErr
		}
		config.ClientCertificate = certificate
	}
	config.OAuth = &oauth
	return config, nil
}

func (p *Provider) CredentialStatus(now time.Time) (providersdk.CredentialStatus, error) {
	p.mu.RLock()
	config := p.config
	p.mu.RUnlock()
	return credentialStatus(config, now)
}

func (p *Provider) RenewCredentials(ctx context.Context) (json.RawMessage, error) {
	p.mu.RLock()
	config := p.config
	p.mu.RUnlock()
	updated, err := renewCredentials(ctx, config, &http.Client{Timeout: 30 * time.Second}, time.Now())
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(updated)
	if err != nil {
		return nil, fmt.Errorf("encode renewed Xiaomi credentials: %w", err)
	}
	return encoded, nil
}

func parseClientCertificate(value string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("Xiaomi client certificate has no CERTIFICATE PEM block")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse Xiaomi client certificate: %w", err)
	}
	return certificate, nil
}

func parseEd25519PrivateKey(value string) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("Xiaomi private key has no PRIVATE KEY PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse Xiaomi private key: %w", err)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("Xiaomi private key is not Ed25519")
	}
	return privateKey, nil
}

func verifyRenewedClientCertificate(value, uid, virtualDID string, privateKey ed25519.PrivateKey, now time.Time) error {
	certificate, err := parseClientCertificate(value)
	if err != nil {
		return err
	}
	expectedCSR, err := createCSR(uid, virtualDID, privateKey)
	if err != nil {
		return err
	}
	block, _ := pem.Decode([]byte(expectedCSR))
	request, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse Xiaomi renewal CSR: %w", err)
	}
	publicKey, ok := certificate.PublicKey.(ed25519.PublicKey)
	if !ok || !publicKey.Equal(privateKey.Public()) {
		return errors.New("renewed Xiaomi certificate does not match the persisted private key")
	}
	if certificate.Subject.CommonName != request.Subject.CommonName {
		return fmt.Errorf("renewed Xiaomi certificate common name mismatch: got %q, want %q", certificate.Subject.CommonName, request.Subject.CommonName)
	}
	if now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) {
		return fmt.Errorf("renewed Xiaomi certificate is not currently valid: %s to %s", certificate.NotBefore.Format(time.RFC3339), certificate.NotAfter.Format(time.RFC3339))
	}
	return nil
}

func certificateRenewalLead(certificate x509.Certificate) time.Duration {
	lifetime := certificate.NotAfter.Sub(certificate.NotBefore)
	lead := lifetime / 5
	if lead <= 0 || lead > maximumCertificateRenewalLead {
		lead = maximumCertificateRenewalLead
	}
	return lead
}

func earlierTime(left, right time.Time) time.Time {
	if left.IsZero() || right.Before(left) {
		return right
	}
	return left
}
