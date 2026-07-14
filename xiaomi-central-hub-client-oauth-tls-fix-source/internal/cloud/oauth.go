package cloud

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // required by Xiaomi OAuth protocol
	"encoding/hex"
	"encoding/json"
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
	AuthorizeEndpoint = "https://account.xiaomi.com/oauth2/authorize"
	DefaultAPIHost    = "ha.api.io.mi.com"
	TokenPath         = "/app/v2/ha/oauth/get_token"
	HomeInfoPath      = "/app/v2/homeroom/gethome"
	CentralCertPath   = "/app/v2/ha/oauth/get_central_crt"
)

// OAuthClient implements the Xiaomi Home OAuth authorization-code flow used by
// the central-gateway API. ClientID must be an OAuth application ID the caller
// is authorized to use.
type OAuthClient struct {
	ClientID    string
	RedirectURL string
	Region      string
	DeviceID    string
	HTTPClient  *http.Client
}

// Token is the persisted OAuth token set. RefreshAfter intentionally uses 70%
// of expires_in, matching the official integration's early-refresh behavior.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	ObtainedAt   int64  `json:"obtained_at"`
	RefreshAfter int64  `json:"refresh_after"`
	ExpiresAt    int64  `json:"expires_at"`
}

func (c OAuthClient) Validate() error {
	if strings.TrimSpace(c.ClientID) == "" {
		return errors.New("OAuth client ID is required")
	}
	if _, err := strconv.ParseUint(c.ClientID, 10, 64); err != nil {
		return fmt.Errorf("OAuth client ID must be numeric: %w", err)
	}
	if strings.TrimSpace(c.RedirectURL) == "" {
		return errors.New("redirect URL is required")
	}
	redirect, err := url.Parse(c.RedirectURL)
	if err != nil || redirect.Scheme == "" || redirect.Host == "" {
		return errors.New("redirect URL must be an absolute URL")
	}
	if strings.TrimSpace(c.Region) == "" {
		return errors.New("region is required")
	}
	if strings.TrimSpace(c.DeviceID) == "" {
		return errors.New("OAuth device ID is required")
	}
	return nil
}

func (c OAuthClient) State() string {
	digest := sha1.Sum([]byte("d=" + c.DeviceID))
	return hex.EncodeToString(digest[:])
}

func (c OAuthClient) APIHost() string {
	if strings.EqualFold(c.Region, "cn") {
		return DefaultAPIHost
	}
	return strings.ToLower(c.Region) + "." + DefaultAPIHost
}

func (c OAuthClient) AuthorizationURL(scopes []string, skipConfirm bool) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	query := url.Values{}
	query.Set("redirect_uri", c.RedirectURL)
	query.Set("client_id", c.ClientID)
	query.Set("response_type", "code")
	query.Set("device_id", c.DeviceID)
	query.Set("state", c.State())
	if len(scopes) > 0 {
		filtered := make([]string, 0, len(scopes))
		for _, scope := range scopes {
			if value := strings.TrimSpace(scope); value != "" {
				filtered = append(filtered, value)
			}
		}
		if len(filtered) > 0 {
			query.Set("scope", strings.Join(filtered, " "))
		}
	}
	if skipConfirm {
		query.Set("skip_confirm", "True")
	} else {
		query.Set("skip_confirm", "False")
	}
	return AuthorizeEndpoint + "?" + query.Encode(), nil
}

func (c OAuthClient) ExchangeCode(ctx context.Context, code string) (Token, error) {
	if strings.TrimSpace(code) == "" {
		return Token{}, errors.New("authorization code is empty")
	}
	return c.getToken(ctx, map[string]any{
		"client_id":    mustNumericJSON(c.ClientID),
		"redirect_uri": c.RedirectURL,
		"code":         code,
		"device_id":    c.DeviceID,
	})
}

func (c OAuthClient) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return Token{}, errors.New("refresh token is empty")
	}
	return c.getToken(ctx, map[string]any{
		"client_id":     mustNumericJSON(c.ClientID),
		"redirect_uri":  c.RedirectURL,
		"refresh_token": refreshToken,
	})
}

// GetAccountUID retrieves the account UID required by the central certificate
// subject. The endpoint may return numeric or string UIDs, so parsing preserves
// both forms.
func (c OAuthClient) GetAccountUID(ctx context.Context, accessToken string) (string, error) {
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Result  struct {
			HomeList []struct {
				UID json.RawMessage `json:"uid"`
			} `json:"homelist"`
		} `json:"result"`
	}
	if err := c.apiPost(ctx, accessToken, HomeInfoPath, map[string]any{
		"limit":           150,
		"fetch_share":     true,
		"fetch_share_dev": true,
		"plat_form":       0,
		"app_ver":         9,
	}, &envelope); err != nil {
		return "", err
	}
	if envelope.Code != 0 {
		return "", fmt.Errorf("home API returned code %d: %s", envelope.Code, envelope.Message)
	}
	for _, home := range envelope.Result.HomeList {
		if uid := rawScalarString(home.UID); uid != "" && uid != "0" {
			return uid, nil
		}
	}
	return "", errors.New("the Xiaomi account has no owned home with a usable UID")
}

func mustNumericJSON(value string) any {
	if parsed, err := strconv.ParseUint(value, 10, 64); err == nil {
		return parsed
	}
	return value
}

func (c OAuthClient) getToken(ctx context.Context, data map[string]any) (Token, error) {
	if err := c.Validate(); err != nil {
		return Token{}, err
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return Token{}, fmt.Errorf("encode token request: %w", err)
	}
	endpoint := url.URL{Scheme: "https", Host: c.APIHost(), Path: TokenPath}
	query := endpoint.Query()
	query.Set("data", string(encoded))
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Token{}, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := c.httpClient().Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("request token: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return Token{}, fmt.Errorf("read token response: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return Token{}, errors.New("OAuth token request unauthorized (HTTP 401)")
	}
	if response.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("OAuth token request failed: HTTP %d: %s", response.StatusCode, compactBody(body))
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
		return Token{}, fmt.Errorf("decode token response: %w", err)
	}
	if envelope.Code != 0 {
		return Token{}, fmt.Errorf("OAuth token API returned code %d: %s", envelope.Code, envelope.Message)
	}
	if envelope.Result.AccessToken == "" || envelope.Result.RefreshToken == "" || envelope.Result.ExpiresIn <= 0 {
		return Token{}, fmt.Errorf("OAuth token response is incomplete: %s", compactBody(body))
	}
	now := time.Now().Unix()
	return Token{
		AccessToken:  envelope.Result.AccessToken,
		RefreshToken: envelope.Result.RefreshToken,
		ExpiresIn:    envelope.Result.ExpiresIn,
		ObtainedAt:   now,
		RefreshAfter: now + envelope.Result.ExpiresIn*7/10,
		ExpiresAt:    now + envelope.Result.ExpiresIn,
	}, nil
}

func (c OAuthClient) apiPost(ctx context.Context, accessToken, path string, payload any, output any) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(accessToken) == "" {
		return errors.New("access token is required")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode API request: %w", err)
	}
	endpoint := "https://" + c.APIHost() + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create API request: %w", err)
	}
	req.Host = c.APIHost()
	req.Header.Set("X-Client-BizId", "haapi")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer"+accessToken)
	req.Header.Set("X-Client-AppId", c.ClientID)

	response, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", path, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("read %s response: %w", path, err)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("request %s unauthorized (HTTP 401)", path)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("request %s failed: HTTP %d: %s", path, response.StatusCode, compactBody(body))
	}
	if err := json.Unmarshal(body, output); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}
	return nil
}

func (c OAuthClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func rawScalarString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		return number.String()
	}
	return ""
}

func compactBody(body []byte) string {
	value := strings.TrimSpace(string(body))
	if len(value) > 500 {
		return value[:500] + "…"
	}
	return value
}
