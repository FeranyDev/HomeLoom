package xiaomi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCredentialStatusUsesEarliestTokenOrCertificateBoundary(t *testing.T) {
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	privateKey, privatePEM := credentialTestPrivateKey(t)
	certificate := credentialTestCertificate(t, privateKey.Public().(ed25519.PublicKey), "mips.123.expected.2", now.Add(-24*time.Hour), now.Add(20*time.Hour))
	config := Config{ClientCertificate: certificate, PrivateKey: privatePEM, OAuth: &OAuthConfig{AccessToken: "access", RefreshToken: "refresh", RefreshAfter: now.Add(15 * time.Hour).Unix(), ExpiresAt: now.Add(19 * time.Hour).Unix()}}
	status, err := credentialStatus(config, now)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := parseClientCertificate(certificate)
	want := parsed.NotAfter.Add(-certificateRenewalLead(*parsed))
	if !status.Managed || !status.RefreshAt.Equal(want) || !status.CertificateExpiresAt.Equal(parsed.NotAfter) {
		t.Fatalf("status=%#v want refresh=%s", status, want)
	}
}

func TestRenewCredentialsRefreshesTokenAndReusesPrivateKeyForCertificate(t *testing.T) {
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	privateKey, privatePEM := credentialTestPrivateKey(t)
	virtualDID, uid := "987654321", "12345"
	csr, err := createCSR(uid, virtualDID, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	csrBlock, _ := pem.Decode([]byte(csr))
	parsedCSR, _ := x509.ParseCertificateRequest(csrBlock.Bytes)
	currentCertificate := credentialTestCertificate(t, privateKey.Public().(ed25519.PublicKey), parsedCSR.Subject.CommonName, now.Add(-24*time.Hour), now.Add(time.Hour))
	var tokenRequests atomic.Int32
	var certificateRequests atomic.Int32
	client := &http.Client{Transport: oauthRoundTrip(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case xiaomiTokenPath:
			tokenRequests.Add(1)
			payload := credentialTokenPayload(t, request.URL)
			if payload["refresh_token"] != "old-refresh" {
				t.Fatalf("refresh payload=%#v", payload)
			}
			return oauthResponse(`{"code":0,"result":{"access_token":"new-access","refresh_token":"new-refresh","expires_in":7200}}`), nil
		case xiaomiCentralCertPath:
			certificateRequests.Add(1)
			var payload struct {
				CSR string `json:"csr"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			encodedCSR, _ := base64.StdEncoding.DecodeString(payload.CSR)
			block, _ := pem.Decode(encodedCSR)
			requestCSR, err := x509.ParseCertificateRequest(block.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			certificate := credentialTestCertificate(t, requestCSR.PublicKey.(ed25519.PublicKey), requestCSR.Subject.CommonName, now.Add(-time.Minute), now.Add(90*24*time.Hour))
			body, _ := json.Marshal(map[string]any{"code": 0, "result": map[string]string{"cert": certificate}})
			return oauthResponse(string(body)), nil
		default:
			t.Fatalf("unexpected request %s", request.URL)
			return nil, nil
		}
	})}
	config := Config{ClientID: virtualDID, ClientCertificate: currentCertificate, PrivateKey: privatePEM, OAuth: &OAuthConfig{ClientID: "1234567890", Region: "cn", RedirectURL: DefaultOAuthRedirectURL, OAuthUUID: "0123456789abcdef0123456789abcdef", VirtualDID: virtualDID, UID: uid, AccessToken: "old-access", RefreshToken: "old-refresh", RefreshAfter: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(time.Hour).Unix()}}
	updated, err := renewCredentials(context.Background(), config, client, now)
	if err != nil {
		t.Fatal(err)
	}
	if tokenRequests.Load() != 1 || certificateRequests.Load() != 1 || updated.OAuth.AccessToken != "new-access" || updated.OAuth.RefreshToken != "new-refresh" || updated.PrivateKey != privatePEM || updated.ClientCertificate == currentCertificate {
		t.Fatalf("requests token=%d cert=%d updated=%#v", tokenRequests.Load(), certificateRequests.Load(), updated.OAuth)
	}
}

func TestRenewCredentialsRefreshesTokenBeforeCertificateEvenWhenTokenIsNotDue(t *testing.T) {
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	privateKey, privatePEM := credentialTestPrivateKey(t)
	virtualDID, uid := "987654321", "12345"
	csr, err := createCSR(uid, virtualDID, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	csrBlock, _ := pem.Decode([]byte(csr))
	parsedCSR, _ := x509.ParseCertificateRequest(csrBlock.Bytes)
	currentCertificate := credentialTestCertificate(t, privateKey.Public().(ed25519.PublicKey), parsedCSR.Subject.CommonName, now.Add(-24*time.Hour), now.Add(time.Hour))
	var tokenRequests atomic.Int32
	client := &http.Client{Transport: oauthRoundTrip(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case xiaomiTokenPath:
			tokenRequests.Add(1)
			return oauthResponse(`{"code":0,"result":{"access_token":"fresh-access","refresh_token":"fresh-refresh","expires_in":7200}}`), nil
		case xiaomiCentralCertPath:
			if request.Header.Get("Authorization") != "Bearerfresh-access" {
				t.Fatalf("certificate authorization=%q", request.Header.Get("Authorization"))
			}
			var payload struct {
				CSR string `json:"csr"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			encodedCSR, _ := base64.StdEncoding.DecodeString(payload.CSR)
			block, _ := pem.Decode(encodedCSR)
			requestCSR, err := x509.ParseCertificateRequest(block.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			certificate := credentialTestCertificate(t, requestCSR.PublicKey.(ed25519.PublicKey), requestCSR.Subject.CommonName, now.Add(-time.Minute), now.Add(90*24*time.Hour))
			body, _ := json.Marshal(map[string]any{"code": 0, "result": map[string]string{"cert": certificate}})
			return oauthResponse(string(body)), nil
		default:
			t.Fatalf("unexpected request %s", request.URL)
			return nil, nil
		}
	})}
	config := Config{ClientID: virtualDID, ClientCertificate: currentCertificate, PrivateKey: privatePEM, OAuth: &OAuthConfig{ClientID: "1234567890", Region: "cn", RedirectURL: DefaultOAuthRedirectURL, OAuthUUID: "0123456789abcdef0123456789abcdef", VirtualDID: virtualDID, UID: uid, AccessToken: "still-scheduled", RefreshToken: "refresh", RefreshAfter: now.Add(12 * time.Hour).Unix(), ExpiresAt: now.Add(24 * time.Hour).Unix()}}
	updated, err := renewCredentials(context.Background(), config, client, now)
	if err != nil {
		t.Fatal(err)
	}
	if tokenRequests.Load() != 1 || updated.OAuth.AccessToken != "fresh-access" {
		t.Fatalf("token requests=%d oauth=%#v", tokenRequests.Load(), updated.OAuth)
	}
}

func credentialTestPrivateKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}))
}

func credentialTestCertificate(t *testing.T, publicKey ed25519.PublicKey, commonName string, notBefore, notAfter time.Time) string {
	t.Helper()
	_, issuerKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(notAfter.UnixNano()), Subject: pkix.Name{CommonName: commonName}, NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, issuerKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func credentialTokenPayload(t *testing.T, endpoint *url.URL) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(endpoint.Query().Get("data")), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestRenewedCertificateRejectsDifferentPrivateKey(t *testing.T) {
	now := time.Now().UTC()
	privateKey, _ := credentialTestPrivateKey(t)
	otherKey, _ := credentialTestPrivateKey(t)
	csr, _ := createCSR("123", "456", privateKey)
	block, _ := pem.Decode([]byte(csr))
	request, _ := x509.ParseCertificateRequest(block.Bytes)
	certificate := credentialTestCertificate(t, otherKey.Public().(ed25519.PublicKey), request.Subject.CommonName, now.Add(-time.Minute), now.Add(time.Hour))
	if err := verifyRenewedClientCertificate(certificate, "123", "456", privateKey, now); err == nil || !strings.Contains(err.Error(), "private key") {
		t.Fatalf("error=%v", err)
	}
}
