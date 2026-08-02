package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DPStatus is the short, publisher-facing name for a status data point. The
// wire model retains TuyaStatus for compatibility with existing callers.
type DPStatus = TuyaStatus

// API is the narrow Tuya cloud surface used by the Provider. Keeping it
// injectable makes reconciliation and command encoding testable without a
// live Tuya account.
type API interface {
	GetToken(context.Context) (Token, error)
	RefreshToken(context.Context, string) (Token, error)
	SetAccessToken(string)
	ListUserDevices(context.Context, string, int, int) ([]TuyaDevice, error)
	GetSpecification(context.Context, string) (Specification, error)
	GetStatus(context.Context, string) ([]DPStatus, error)
	SendCommands(context.Context, string, []Command) error
}

type Client struct {
	baseURL      *url.URL
	accessID     string
	accessSecret string
	httpClient   *http.Client

	mu          sync.RWMutex
	accessToken string
	now         func() time.Time
	nonce       func() string
}

const noAccessToken = "\x00tuya-no-access-token\x00"

var _ API = (*Client)(nil)

func NewClient(baseURL, accessID, accessSecret string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("tuya api base url must be absolute")
	}
	if strings.TrimSpace(accessID) == "" || strings.TrimSpace(accessSecret) == "" {
		return nil, errors.New("tuya api access id and secret are required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	client := &Client{baseURL: parsed, accessID: accessID, accessSecret: accessSecret, httpClient: httpClient, now: time.Now, nonce: randomNonce}
	return client, nil
}

// SetAccessToken replaces the short-lived bearer token used for business
// calls. The token value is never included in diagnostics or errors.
func (c *Client) SetAccessToken(token string) {
	c.mu.Lock()
	c.accessToken = strings.TrimSpace(token)
	c.mu.Unlock()
}

func (c *Client) GetToken(ctx context.Context) (Token, error) {
	return c.getToken(ctx, url.Values{"grant_type": []string{"1"}})
}

// ExchangeAuthorizationCode exchanges a user-authorized OAuth 2.0 code for a
// Tuya user token. The code is intentionally accepted only for this single
// request and is never copied into logs or provider diagnostics.
func (c *Client) ExchangeAuthorizationCode(ctx context.Context, code string) (Token, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return Token{}, errors.New("Tuya authorization code is required")
	}
	return c.getToken(ctx, url.Values{"grant_type": []string{"2"}, "code": []string{code}})
}

func (c *Client) getToken(ctx context.Context, query url.Values) (Token, error) {
	var response struct {
		Success bool            `json:"success"`
		Code    json.RawMessage `json:"code"`
		Msg     string          `json:"msg"`
		Result  json.RawMessage `json:"result"`
		Token
	}
	if err := c.getJSON(ctx, "/v1.0/token", query, noAccessToken, &response); err != nil {
		return Token{}, err
	}
	token := response.Token
	if len(bytes.TrimSpace(response.Result)) > 0 && string(bytes.TrimSpace(response.Result)) != "null" {
		if err := json.Unmarshal(response.Result, &token); err != nil {
			return Token{}, fmt.Errorf("decode Tuya token result: %w", err)
		}
	}
	if token.AccessToken == "" {
		return Token{}, errors.New("Tuya token response did not contain access_token")
	}
	c.SetAccessToken(token.AccessToken)
	return token, nil
}

func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (Token, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return Token{}, errors.New("Tuya refresh token is required")
	}
	var response struct {
		Success bool            `json:"success"`
		Code    json.RawMessage `json:"code"`
		Msg     string          `json:"msg"`
		Result  json.RawMessage `json:"result"`
		Token
	}
	path := "/v1.0/token/" + url.PathEscape(refreshToken)
	if err := c.getJSON(ctx, path, nil, noAccessToken, &response); err != nil {
		return Token{}, err
	}
	token := response.Token
	if len(bytes.TrimSpace(response.Result)) > 0 && string(bytes.TrimSpace(response.Result)) != "null" {
		if err := json.Unmarshal(response.Result, &token); err != nil {
			return Token{}, fmt.Errorf("decode Tuya refresh token result: %w", err)
		}
	}
	if token.AccessToken == "" {
		return Token{}, errors.New("Tuya refresh response did not contain access_token")
	}
	c.SetAccessToken(token.AccessToken)
	return token, nil
}

func (c *Client) ListUserDevices(ctx context.Context, uid string, page, pageSize int) ([]TuyaDevice, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 100
	}
	path := "/v1.0/users/" + url.PathEscape(strings.TrimSpace(uid)) + "/devices"
	query := url.Values{"page_no": []string{strconv.Itoa(page)}, "page_size": []string{strconv.Itoa(pageSize)}}
	var result []TuyaDevice
	if err := c.getJSON(ctx, path, query, "", &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) GetSpecification(ctx context.Context, deviceID string) (Specification, error) {
	path := "/v1.0/iot-03/devices/" + url.PathEscape(strings.TrimSpace(deviceID)) + "/specification"
	var result Specification
	if err := c.getJSON(ctx, path, nil, "", &result); err != nil {
		return Specification{}, err
	}
	for i := range result.Functions {
		result.Functions[i].Writable, result.Functions[i].writableSet = true, true
	}
	for i := range result.Status {
		result.Status[i].Readable, result.Status[i].readableSet = true, true
	}
	for i := range result.Properties {
		result.Properties[i].Readable, result.Properties[i].readableSet = true, true
	}
	return result, nil
}

func (c *Client) GetStatus(ctx context.Context, deviceID string) ([]TuyaStatus, error) {
	path := "/v1.0/iot-03/devices/" + url.PathEscape(strings.TrimSpace(deviceID)) + "/status"
	var result []TuyaStatus
	if err := c.getJSON(ctx, path, nil, "", &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) SendCommands(ctx context.Context, deviceID string, commands []Command) error {
	if strings.TrimSpace(deviceID) == "" || len(commands) == 0 {
		return errors.New("Tuya command requires device id and commands")
	}
	path := "/v1.0/iot-03/devices/" + url.PathEscape(strings.TrimSpace(deviceID)) + "/commands"
	body, err := json.Marshal(struct {
		Commands []Command `json:"commands"`
	}{Commands: commands})
	if err != nil {
		return fmt.Errorf("encode Tuya commands: %w", err)
	}
	return c.doJSON(ctx, http.MethodPost, path, nil, body, "", nil)
}

func (c *Client) getJSON(ctx context.Context, path string, query url.Values, token string, result any) error {
	return c.doJSON(ctx, http.MethodGet, path, query, nil, token, result)
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, body []byte, explicitToken string, result any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	u := *c.baseURL
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + "/" + strings.TrimLeft(path, "/")
	u.RawQuery = query.Encode()
	token := explicitToken
	if token == noAccessToken {
		token = ""
	} else if token == "" {
		c.mu.RLock()
		token = c.accessToken
		c.mu.RUnlock()
	}
	request, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return errors.New("create Tuya request failed")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("client_id", c.accessID)
	request.Header.Set("sign_method", "HMAC-SHA256")
	timestamp := strconv.FormatInt(c.now().UnixMilli(), 10)
	request.Header.Set("t", timestamp)
	if token != "" {
		request.Header.Set("access_token", token)
	}
	nonce := ""
	if c.nonce != nil {
		nonce = strings.TrimSpace(c.nonce())
	}
	if nonce != "" {
		request.Header.Set("nonce", nonce)
	}
	request.Header.Set("sign", c.sign(method, request.URL, body, token, timestamp, nonce))
	response, err := c.httpClient.Do(request)
	if err != nil {
		// Do not wrap the transport error: net/http may include the complete
		// request URL, which would expose an OAuth code or refresh token.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("Tuya API request %s %s: %w", method, safeTuyaPath(path), ctxErr)
		}
		return fmt.Errorf("Tuya API request %s %s failed", method, safeTuyaPath(path))
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read Tuya API response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := redactTuyaText(compactErrorBody(payload), c.accessID, c.accessSecret, token, query.Get("code"), tuyaRefreshTokenFromPath(path))
		return fmt.Errorf("Tuya API HTTP %d: %s", response.StatusCode, message)
	}
	var envelope struct {
		Success bool            `json:"success"`
		Code    json.RawMessage `json:"code"`
		Msg     string          `json:"msg"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("decode Tuya API response: %w", err)
	}
	if !envelope.Success {
		code := strings.Trim(string(envelope.Code), `"`)
		if code == "" {
			code = "unknown"
		}
		message := redactTuyaText(envelope.Msg, c.accessID, c.accessSecret, token, query.Get("code"), tuyaRefreshTokenFromPath(path))
		return fmt.Errorf("Tuya API rejected request (%s): %s", code, message)
	}
	if result == nil || len(envelope.Result) == 0 || string(bytes.TrimSpace(envelope.Result)) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("decode Tuya API result: %w", err)
	}
	return nil
}

func (c *Client) sign(method string, u *url.URL, body []byte, token, timestamp, nonce string) string {
	hash := sha256.Sum256(body)
	contentHash := hex.EncodeToString(hash[:])
	requestURI := u.EscapedPath()
	if requestURI == "" {
		requestURI = "/"
	}
	if u.RawQuery != "" {
		requestURI += "?" + u.RawQuery
	}
	stringToSign := strings.ToUpper(method) + "\n" + contentHash + "\n\n" + requestURI
	plain := c.accessID + token + timestamp + nonce + stringToSign
	mac := hmac.New(sha256.New, []byte(c.accessSecret))
	_, _ = mac.Write([]byte(plain))
	return strings.ToUpper(hex.EncodeToString(mac.Sum(nil)))
}

func randomNonce() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return ""
	}
	return hex.EncodeToString(buffer)
}

func compactErrorBody(payload []byte) string {
	text := strings.Join(strings.Fields(string(payload)), " ")
	if len(text) > 512 {
		text = text[:512]
	}
	return text
}

func safeTuyaPath(path string) string {
	if strings.HasPrefix(path, "/v1.0/token/") {
		return "/v1.0/token/{refresh_token}"
	}
	return path
}

func tuyaRefreshTokenFromPath(path string) string {
	const prefix = "/v1.0/token/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	value, err := url.PathUnescape(strings.TrimPrefix(path, prefix))
	if err != nil {
		return ""
	}
	return value
}

func redactTuyaText(text string, values ...string) string {
	for _, value := range values {
		if value != "" {
			text = strings.ReplaceAll(text, value, "[redacted]")
		}
	}
	return text
}
