package xiaomi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // required by Xiaomi's OAuth state and certificate subject protocols.
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	xiaomiAuthorizeEndpoint = "https://account.xiaomi.com/oauth2/authorize"
	xiaomiDefaultAPIHost    = "ha.api.io.mi.com"
	xiaomiTokenPath         = "/app/v2/ha/oauth/get_token"
	xiaomiHomeInfoPath      = "/app/v2/homeroom/gethome"
	xiaomiCentralCertPath   = "/app/v2/ha/oauth/get_central_crt"
	DefaultOAuthRedirectURL = "http://homeassistant.local:8123"
)

const CentralGatewayCAPEM = `-----BEGIN CERTIFICATE-----
MIIBazCCAQ+gAwIBAgIEA/UKYDAMBggqhkjOPQQDAgUAMCIxEzARBgNVBAoTCk1p
amlhIFJvb3QxCzAJBgNVBAYTAkNOMCAXDTE2MTEyMzAxMzk0NVoYDzIwNjYxMTEx
MDEzOTQ1WjAiMRMwEQYDVQQKEwpNaWppYSBSb290MQswCQYDVQQGEwJDTjBZMBMG
ByqGSM49AgEGCCqGSM49AwEHA0IABL71iwLa4//4VBqgRI+6xE23xpovqPCxtv96
2VHbZij61/Ag6jmi7oZ/3Xg/3C+whglcwoUEE6KALGJ9vccV9PmjLzAtMAwGA1Ud
EwQFMAMBAf8wHQYDVR0OBBYEFJa3onw5sblmM6n40QmyAGDI5sURMAwGCCqGSM49
BAMCBQADSAAwRQIgchciK9h6tZmfrP8Ka6KziQ4Lv3hKfrHtAZXMHPda4IYCIQCG
az93ggFcbrG9u2wixjx1HKW4DUA5NXZG0wWQTpJTbQ==
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
MIIBjzCCATWgAwIBAgIBATAKBggqhkjOPQQDAjAiMRMwEQYDVQQKEwpNaWppYSBS
b290MQswCQYDVQQGEwJDTjAgFw0yMjA2MDkxNDE0MThaGA8yMDcyMDUyNzE0MTQx
OFowLDELMAkGA1UEBhMCQ04xHTAbBgNVBAoMFE1JT1QgQ0VOVFJBTCBHQVRFV0FZ
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEdYrzbnp/0x/cZLZnuEDXTFf8mhj4
CVpZPwgj9e9Ve5r3K7zvu8Jjj7JF1JjQYvEC6yhp1SzBgglnK4L8xQzdiqNQME4w
HQYDVR0OBBYEFCf9+YBU7pXDs6K6CAQPRhlGJ+cuMB8GA1UdIwQYMBaAFJa3onw5
sblmM6n40QmyAGDI5sURMAwGA1UdEwQFMAMBAf8wCgYIKoZIzj0EAwIDSAAwRQIh
AKUv+c8v98vypkGMTzMwckGjjVqTef8xodsy6PhcSCq+AiA/n9mDs62hAo5zXyJy
Bs1s7mqXPf1XgieoxIvs1MqyiA==
-----END CERTIFICATE-----
`

type OAuthStartRequest struct {
	ClientID    string `json:"clientId"`
	Region      string `json:"region"`
	RedirectURL string `json:"redirectUrl"`
	OAuthUUID   string `json:"oauthUuid,omitempty"`
	VirtualDID  string `json:"virtualDid,omitempty"`
}

type OAuthStartResult struct {
	AuthorizationURL string `json:"authorizationUrl"`
	State            string `json:"state"`
	OAuthUUID        string `json:"oauthUuid"`
	VirtualDID       string `json:"virtualDid"`
}

type OAuthCompleteRequest struct {
	OAuthStartRequest
	Code  string `json:"code"`
	State string `json:"state"`
}

type OAuthProvisionResult struct {
	OAuth             OAuthConfig `json:"oauth"`
	ClientID          string      `json:"clientId"`
	CACertificate     string      `json:"caCertificate"`
	ClientCertificate string      `json:"clientCertificate"`
	PrivateKey        string      `json:"privateKey"`
}

type oauthToken struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
	ObtainedAt   int64
	RefreshAfter int64
	ExpiresAt    int64
}

type oauthClient struct {
	config OAuthStartRequest
	http   *http.Client
}

func StartOAuth(request OAuthStartRequest) (OAuthStartResult, error) {
	request.ClientID = strings.TrimSpace(request.ClientID)
	request.Region = strings.ToLower(strings.TrimSpace(request.Region))
	request.RedirectURL = DefaultOAuthRedirectURL
	if request.OAuthUUID == "" {
		value, err := randomHex(16)
		if err != nil {
			return OAuthStartResult{}, err
		}
		request.OAuthUUID = value
	}
	if request.VirtualDID == "" {
		value, err := randomVirtualDID()
		if err != nil {
			return OAuthStartResult{}, err
		}
		request.VirtualDID = value
	}
	client := oauthClient{config: request}
	if err := client.validate(); err != nil {
		return OAuthStartResult{}, err
	}
	state := client.state()
	query := url.Values{
		"redirect_uri":  {request.RedirectURL},
		"client_id":     {request.ClientID},
		"response_type": {"code"},
		"device_id":     {"ha." + request.OAuthUUID},
		"state":         {state},
		"skip_confirm":  {"False"},
	}
	return OAuthStartResult{AuthorizationURL: xiaomiAuthorizeEndpoint + "?" + query.Encode(), State: state, OAuthUUID: request.OAuthUUID, VirtualDID: request.VirtualDID}, nil
}

func CompleteOAuth(ctx context.Context, request OAuthCompleteRequest) (OAuthProvisionResult, error) {
	return completeOAuth(ctx, request, &http.Client{Timeout: 30 * time.Second})
}

func completeOAuth(ctx context.Context, request OAuthCompleteRequest, httpClient *http.Client) (OAuthProvisionResult, error) {
	started, err := StartOAuth(request.OAuthStartRequest)
	if err != nil {
		return OAuthProvisionResult{}, err
	}
	if request.State == "" || request.State != started.State {
		return OAuthProvisionResult{}, errors.New("OAuth state mismatch")
	}
	if strings.TrimSpace(request.Code) == "" {
		return OAuthProvisionResult{}, errors.New("authorization code is required")
	}
	request.ClientID = strings.TrimSpace(request.ClientID)
	request.Region = strings.ToLower(strings.TrimSpace(request.Region))
	request.RedirectURL = DefaultOAuthRedirectURL
	request.OAuthUUID, request.VirtualDID = started.OAuthUUID, started.VirtualDID
	client := oauthClient{config: request.OAuthStartRequest, http: httpClient}
	token, err := client.exchange(ctx, request.Code)
	if err != nil {
		return OAuthProvisionResult{}, err
	}
	uid, err := client.accountUID(ctx, token.AccessToken)
	if err != nil {
		return OAuthProvisionResult{}, err
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return OAuthProvisionResult{}, fmt.Errorf("generate Ed25519 key: %w", err)
	}
	csr, err := createCSR(uid, request.VirtualDID, privateKey)
	if err != nil {
		return OAuthProvisionResult{}, err
	}
	certificate, err := client.certificate(ctx, token.AccessToken, csr)
	if err != nil {
		return OAuthProvisionResult{}, err
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return OAuthProvisionResult{}, fmt.Errorf("encode private key: %w", err)
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey}))
	return OAuthProvisionResult{
		OAuth:    OAuthConfig{ClientID: request.ClientID, Region: request.Region, RedirectURL: request.RedirectURL, OAuthUUID: request.OAuthUUID, VirtualDID: request.VirtualDID, UID: uid, AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, RefreshAfter: token.RefreshAfter, ExpiresAt: token.ExpiresAt},
		ClientID: request.VirtualDID, CACertificate: CentralGatewayCAPEM, ClientCertificate: certificate, PrivateKey: privatePEM,
	}, nil
}

func (c oauthClient) validate() error {
	if _, err := strconv.ParseUint(c.config.ClientID, 10, 64); err != nil {
		return errors.New("OAuth clientId must be numeric")
	}
	redirect, err := url.Parse(c.config.RedirectURL)
	if err != nil || redirect.Scheme == "" || redirect.Host == "" {
		return errors.New("redirectUrl must be an absolute URL")
	}
	if c.config.Region == "" {
		return errors.New("region is required")
	}
	if len(c.config.OAuthUUID) != 32 {
		return errors.New("oauthUuid must contain 32 hexadecimal characters")
	}
	if _, err := hex.DecodeString(c.config.OAuthUUID); err != nil {
		return errors.New("oauthUuid must contain 32 hexadecimal characters")
	}
	if _, err := strconv.ParseUint(c.config.VirtualDID, 10, 64); err != nil {
		return errors.New("virtualDid must be decimal")
	}
	return nil
}

func (c oauthClient) state() string {
	digest := sha1.Sum([]byte("d=ha." + c.config.OAuthUUID))
	return hex.EncodeToString(digest[:])
}

func (c oauthClient) host() string {
	if c.config.Region == "cn" {
		return xiaomiDefaultAPIHost
	}
	return c.config.Region + "." + xiaomiDefaultAPIHost
}

func (c oauthClient) exchange(ctx context.Context, code string) (oauthToken, error) {
	payload := map[string]any{"client_id": numericOrString(c.config.ClientID), "redirect_uri": c.config.RedirectURL, "code": code, "device_id": "ha." + c.config.OAuthUUID}
	encoded, _ := json.Marshal(payload)
	endpoint := url.URL{Scheme: "https", Host: c.host(), Path: xiaomiTokenPath}
	query := endpoint.Query()
	query.Set("data", string(encoded))
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return oauthToken{}, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return oauthToken{}, fmt.Errorf("request Xiaomi token: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return oauthToken{}, err
	}
	if response.StatusCode != http.StatusOK {
		return oauthToken{}, fmt.Errorf("Xiaomi token request failed with HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Result  struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int64  `json:"expires_in"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return oauthToken{}, err
	}
	if envelope.Code != 0 || envelope.Result.AccessToken == "" || envelope.Result.RefreshToken == "" || envelope.Result.ExpiresIn <= 0 {
		return oauthToken{}, fmt.Errorf("Xiaomi token API returned code %d: %s", envelope.Code, envelope.Message)
	}
	now := time.Now().Unix()
	return oauthToken{AccessToken: envelope.Result.AccessToken, RefreshToken: envelope.Result.RefreshToken, ExpiresIn: envelope.Result.ExpiresIn, ObtainedAt: now, RefreshAfter: now + envelope.Result.ExpiresIn*7/10, ExpiresAt: now + envelope.Result.ExpiresIn}, nil
}

func (c oauthClient) accountUID(ctx context.Context, token string) (string, error) {
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Result  struct {
			HomeList []struct {
				UID json.RawMessage `json:"uid"`
			} `json:"homelist"`
		} `json:"result"`
	}
	if err := c.post(ctx, token, xiaomiHomeInfoPath, map[string]any{"limit": 150, "fetch_share": true, "fetch_share_dev": true, "plat_form": 0, "app_ver": 9}, &envelope); err != nil {
		return "", err
	}
	if envelope.Code != 0 {
		return "", fmt.Errorf("Xiaomi home API returned code %d: %s", envelope.Code, envelope.Message)
	}
	for _, home := range envelope.Result.HomeList {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(home.UID))
		decoder.UseNumber()
		if decoder.Decode(&value) == nil && fmt.Sprint(value) != "" && fmt.Sprint(value) != "0" {
			return fmt.Sprint(value), nil
		}
	}
	return "", errors.New("Xiaomi account has no owned home with a usable UID")
}

func (c oauthClient) certificate(ctx context.Context, token, csr string) (string, error) {
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Result  struct {
			Cert string `json:"cert"`
		} `json:"result"`
	}
	if err := c.post(ctx, token, xiaomiCentralCertPath, map[string]string{"csr": base64.StdEncoding.EncodeToString([]byte(csr))}, &envelope); err != nil {
		return "", err
	}
	if envelope.Code != 0 || !strings.Contains(envelope.Result.Cert, "BEGIN CERTIFICATE") {
		return "", fmt.Errorf("Xiaomi certificate API returned code %d: %s", envelope.Code, envelope.Message)
	}
	return envelope.Result.Cert, nil
}

func (c oauthClient) post(ctx context.Context, token, path string, payload, output any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+c.host()+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("X-Client-BizId", "haapi")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer"+token)
	request.Header.Set("X-Client-AppId", c.config.ClientID)
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("request Xiaomi API %s: %w", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Xiaomi API %s failed with HTTP %d", path, response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(output)
}

func createCSR(uid, virtualDID string, privateKey ed25519.PrivateKey) (string, error) {
	digest := sha1.Sum([]byte(virtualDID))
	template := &x509.CertificateRequest{Subject: pkix.Name{Country: []string{"CN"}, Organization: []string{"Mijia Device"}, CommonName: fmt.Sprintf("mips.%s.%s.2", uid, hex.EncodeToString(digest[:]))}, SignatureAlgorithm: x509.PureEd25519}
	der, err := x509.CreateCertificateRequest(rand.Reader, template, privateKey)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})), nil
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func randomVirtualDID() (string, error) {
	var raw [8]byte
	for {
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		if value := binary.BigEndian.Uint64(raw[:]); value != 0 {
			return strconv.FormatUint(value, 10), nil
		}
	}
}

func numericOrString(value string) any {
	if number, err := strconv.ParseUint(value, 10, 64); err == nil {
		return number
	}
	return value
}
