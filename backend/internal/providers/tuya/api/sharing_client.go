package api

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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

// These are the public client identifiers used by Home Assistant's Tuya
// integration. The QR login is a Tuya device-sharing flow, not OpenAPI OAuth.
const (
	HomeAssistantClientID = "HA_3y9q4ak7g4ephrvke"
	HomeAssistantSchema   = "haauthorize"
	SharingLoginBaseURL   = "https://apigw.iotbing.com"
)

// SharingClient implements the encrypted customer API used by the Home
// Assistant Tuya integration. It deliberately implements the same narrow API
// interface as the OpenAPI client so the provider can use either login mode.
type SharingClient struct {
	endpoint   *url.URL
	clientID   string
	userCode   string
	terminalID string
	httpClient *http.Client
	now        func() time.Time
	random     func([]byte) (int, error)

	mu        sync.RWMutex
	token     Token
	expiresAt time.Time
	refreshMu sync.Mutex
}

var _ API = (*SharingClient)(nil)

func NewSharingClient(endpoint, clientID, userCode, terminalID string, token Token, expiresAt time.Time, httpClient *http.Client) (*SharingClient, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(endpoint), "/"))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("tuya sharing endpoint must be an absolute HTTP(S) URL")
	}
	if strings.TrimSpace(userCode) == "" {
		return nil, errors.New("tuya sharing user code is required")
	}
	if strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.RefreshToken) == "" {
		return nil, errors.New("tuya sharing access and refresh tokens are required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	if strings.TrimSpace(clientID) == "" {
		clientID = HomeAssistantClientID
	}
	return &SharingClient{
		endpoint: parsed, clientID: strings.TrimSpace(clientID), userCode: strings.TrimSpace(userCode),
		terminalID: strings.TrimSpace(terminalID), httpClient: httpClient, now: time.Now, random: rand.Read,
		token: token, expiresAt: expiresAt,
	}, nil
}

func (c *SharingClient) SetAccessToken(token string) {
	c.mu.Lock()
	c.token.AccessToken = strings.TrimSpace(token)
	c.mu.Unlock()
}

func (c *SharingClient) GetToken(context.Context) (Token, error) {
	c.mu.RLock()
	token, expiresAt := c.token, c.expiresAt
	c.mu.RUnlock()
	if strings.TrimSpace(token.AccessToken) == "" {
		return Token{}, errors.New("tuya sharing access token is missing; scan the QR code again")
	}
	return c.tokenWithExpiry(token, expiresAt), nil
}

func (c *SharingClient) RefreshToken(ctx context.Context, refreshToken string) (Token, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return Token{}, errors.New("tuya sharing refresh token is required")
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	// Another caller may have refreshed the token while this caller waited.
	c.mu.RLock()
	if !c.expiresAt.IsZero() && c.currentTime().Add(time.Minute).Before(c.expiresAt) {
		token := c.token
		expiresAt := c.expiresAt
		c.mu.RUnlock()
		return c.tokenWithExpiry(token, expiresAt), nil
	}
	c.mu.RUnlock()
	response, err := c.request(ctx, http.MethodGet, "/v1.0/m/token/"+url.PathEscape(refreshToken), nil, nil, true)
	if err != nil {
		return Token{}, err
	}
	token, expiresAt, err := decodeSharingToken(response.result, response.t)
	if err != nil {
		return Token{}, err
	}
	if token.AccessToken == "" || token.RefreshToken == "" {
		return Token{}, errors.New("tuya sharing refresh response did not contain complete token information")
	}
	c.mu.Lock()
	c.token, c.expiresAt = token, expiresAt
	c.mu.Unlock()
	return token, nil
}

func (c *SharingClient) ListUserDevices(ctx context.Context, _ string, _, _ int) ([]TuyaDevice, error) {
	homesResponse, err := c.request(ctx, http.MethodGet, "/v1.0/m/life/users/homes", nil, nil, false)
	if err != nil {
		return nil, err
	}
	var homes []sharingHome
	if err := json.Unmarshal(homesResponse.result, &homes); err != nil {
		return nil, fmt.Errorf("decode Tuya sharing homes: %w", err)
	}
	devices := make([]TuyaDevice, 0)
	seenHomes := make(map[string]struct{})
	for _, home := range homes {
		homeID := firstNonEmpty(home.OwnerID, home.HomeID, home.ID)
		if homeID == "" {
			continue
		}
		if _, seen := seenHomes[homeID]; seen {
			continue
		}
		seenHomes[homeID] = struct{}{}
		response, requestErr := c.request(ctx, http.MethodGet, "/v1.0/m/life/ha/home/devices", map[string]string{"homeId": homeID}, nil, false)
		if requestErr != nil {
			return nil, requestErr
		}
		items, decodeErr := decodeSharingDevices(response.result)
		if decodeErr != nil {
			return nil, decodeErr
		}
		devices = append(devices, items...)
	}
	return devices, nil
}

func (c *SharingClient) GetSpecification(ctx context.Context, deviceID string) (Specification, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return Specification{}, errors.New("tuya sharing device id is required")
	}
	response, err := c.request(ctx, http.MethodGet, "/v1.1/m/life/"+url.PathEscape(deviceID)+"/specifications", nil, nil, false)
	if err != nil {
		return Specification{}, err
	}
	var wire struct {
		Category  string   `json:"category"`
		Functions []DPSpec `json:"functions"`
		Status    []DPSpec `json:"status"`
	}
	if err := json.Unmarshal(response.result, &wire); err != nil {
		return Specification{}, fmt.Errorf("decode Tuya sharing specification: %w", err)
	}
	for index := range wire.Functions {
		wire.Functions[index].Writable = true
		wire.Functions[index].Readable = true
	}
	for index := range wire.Status {
		wire.Status[index].Readable = true
	}
	return Specification{Category: wire.Category, Functions: wire.Functions, Status: wire.Status}, nil
}

func (c *SharingClient) GetStatus(ctx context.Context, deviceID string) ([]TuyaStatus, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil, errors.New("tuya sharing device id is required")
	}
	response, err := c.request(ctx, http.MethodGet, "/v1.0/m/life/ha/devices/detail", map[string]string{"devIds": deviceID}, nil, false)
	if err != nil {
		return nil, err
	}
	devices, err := decodeSharingDevices(response.result)
	if err != nil {
		return nil, err
	}
	for _, device := range devices {
		if device.ID == deviceID {
			return device.Status, nil
		}
	}
	return nil, nil
}

func (c *SharingClient) SendCommands(ctx context.Context, deviceID string, commands []Command) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" || len(commands) == 0 {
		return errors.New("tuya sharing command requires device id and commands")
	}
	_, err := c.request(ctx, http.MethodPost, "/v1.1/m/thing/"+url.PathEscape(deviceID)+"/commands", nil, struct {
		Commands []Command `json:"commands"`
	}{Commands: commands}, false)
	return err
}

type sharingResponse struct {
	success bool
	code    string
	message string
	t       int64
	result  []byte
}

func (c *SharingClient) request(ctx context.Context, method, path string, params map[string]string, body any, skipRefresh bool) (sharingResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !skipRefresh && c.isExpiring() {
		c.mu.RLock()
		refreshToken := c.token.RefreshToken
		c.mu.RUnlock()
		if refreshToken != "" {
			if _, err := c.RefreshToken(ctx, refreshToken); err != nil {
				return sharingResponse{}, err
			}
		}
	}
	c.mu.RLock()
	accessToken, refreshToken := c.token.AccessToken, c.token.RefreshToken
	c.mu.RUnlock()
	rid := c.requestID()
	hash := md5.Sum([]byte(rid + refreshToken))
	hashKey := hex.EncodeToString(hash[:])
	secret := sharingSecret(rid, "", hashKey)
	var query url.Values
	queryData := ""
	if len(params) > 0 {
		encoded, err := json.Marshal(params)
		if err != nil {
			return sharingResponse{}, fmt.Errorf("encode Tuya sharing query: %w", err)
		}
		queryData, err = encryptSharing(encoded, secret, c.random)
		if err != nil {
			return sharingResponse{}, err
		}
		query = url.Values{"encdata": []string{queryData}}
	}
	bodyData := ""
	var requestBody []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return sharingResponse{}, fmt.Errorf("encode Tuya sharing body: %w", err)
		}
		bodyData, err = encryptSharing(encoded, secret, c.random)
		if err != nil {
			return sharingResponse{}, err
		}
		requestBody, err = json.Marshal(map[string]string{"encdata": bodyData})
		if err != nil {
			return sharingResponse{}, fmt.Errorf("encode Tuya sharing envelope: %w", err)
		}
	}
	u := *c.endpoint
	u.Path = strings.TrimRight(c.endpoint.Path, "/") + "/" + strings.TrimLeft(path, "/")
	if query != nil {
		u.RawQuery = query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(requestBody))
	if err != nil {
		return sharingResponse{}, errors.New("create Tuya sharing request failed")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-appKey", c.clientID)
	request.Header.Set("X-requestId", rid)
	request.Header.Set("X-sid", "")
	request.Header.Set("X-time", strconv.FormatInt(c.currentTime().UnixMilli(), 10))
	if accessToken != "" {
		request.Header.Set("X-token", accessToken)
	}
	request.Header.Set("X-sign", sharingSign(hashKey, queryData, bodyData, request.Header))
	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return sharingResponse{}, fmt.Errorf("Tuya sharing request %s %s: %w", method, path, ctxErr)
		}
		return sharingResponse{}, fmt.Errorf("Tuya sharing request %s %s failed", method, path)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return sharingResponse{}, fmt.Errorf("read Tuya sharing response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return sharingResponse{}, fmt.Errorf("Tuya sharing API HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Success bool            `json:"success"`
		Code    json.RawMessage `json:"code"`
		Msg     string          `json:"msg"`
		T       int64           `json:"t"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return sharingResponse{}, fmt.Errorf("decode Tuya sharing response: %w", err)
	}
	code := strings.Trim(string(envelope.Code), `"`)
	if !envelope.Success {
		return sharingResponse{success: false, code: code, message: redactSharingText(envelope.Msg, accessToken, refreshToken)}, fmt.Errorf("Tuya sharing API rejected request (%s): %s", code, redactSharingText(envelope.Msg, accessToken, refreshToken))
	}
	if len(bytes.TrimSpace(envelope.Result)) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Result), []byte("null")) {
		return sharingResponse{success: true, code: code, t: envelope.T}, nil
	}
	var encrypted string
	if err := json.Unmarshal(envelope.Result, &encrypted); err == nil {
		decrypted, decryptErr := decryptSharing(encrypted, secret)
		if decryptErr != nil {
			return sharingResponse{}, fmt.Errorf("decrypt Tuya sharing response: %w", decryptErr)
		}
		return sharingResponse{success: true, code: code, t: envelope.T, result: decrypted}, nil
	}
	// Keep fixtures and future server responses that return an unencrypted
	// object usable; production customer APIs currently return encrypted data.
	return sharingResponse{success: true, code: code, t: envelope.T, result: envelope.Result}, nil
}

func (c *SharingClient) isExpiring() bool {
	c.mu.RLock()
	expiresAt := c.expiresAt
	c.mu.RUnlock()
	return !expiresAt.IsZero() && c.currentTime().Add(time.Minute).After(expiresAt)
}

func (c *SharingClient) currentTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *SharingClient) tokenWithExpiry(token Token, expiresAt time.Time) Token {
	if expiresAt.IsZero() {
		return token
	}
	remaining := int64(expiresAt.Sub(c.currentTime()) / time.Second)
	if remaining < 0 {
		remaining = 0
	}
	token.ExpiresIn = remaining
	token.ExpireTime = remaining
	return token
}

func (c *SharingClient) requestID() string {
	value := make([]byte, 16)
	random := c.random
	if random == nil {
		random = rand.Read
	}
	if _, err := random(value); err != nil {
		return fmt.Sprintf("%d", c.currentTime().UnixNano())
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[:4], value[4:6], value[6:8], value[8:10], value[10:])
}

type sharingHome struct {
	ID      string `json:"id"`
	HomeID  string `json:"homeId"`
	OwnerID string `json:"ownerId"`
}

type sharingDeviceWire struct {
	ID             string       `json:"id"`
	DeviceID       string       `json:"deviceId"`
	Name           string       `json:"name"`
	Category       string       `json:"category"`
	CategoryCode   string       `json:"categoryCode"`
	ProductID      string       `json:"productId"`
	ProductIDSnake string       `json:"product_id"`
	ProductName    string       `json:"productName"`
	Online         bool         `json:"online"`
	Status         []TuyaStatus `json:"status"`
	LocalKey       string       `json:"localKey"`
	UUID           string       `json:"uuid"`
	GatewayID      string       `json:"gatewayId"`
	OwnerID        string       `json:"ownerId"`
	UID            string       `json:"uid"`
	AssetID        string       `json:"assetId"`
	RoomID         string       `json:"roomId"`
	RoomName       string       `json:"roomName"`
	HomeID         string       `json:"homeId"`
	HomeName       string       `json:"homeName"`
	Timezone       string       `json:"timeZone"`
	ActiveTime     int64        `json:"activeTime"`
	UpdateTime     int64        `json:"updateTime"`
	Sub            bool         `json:"sub"`
}

func decodeSharingDevices(raw json.RawMessage) ([]TuyaDevice, error) {
	var wire []sharingDeviceWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("decode Tuya sharing devices: %w", err)
	}
	result := make([]TuyaDevice, 0, len(wire))
	for _, item := range wire {
		result = append(result, TuyaDevice{
			ID: firstNonEmpty(item.ID, item.DeviceID), Name: item.Name, Category: firstNonEmpty(item.Category, item.CategoryCode), ProductID: firstNonEmpty(item.ProductID, item.ProductIDSnake), ProductName: item.ProductName,
			Online: item.Online, Status: item.Status, LocalKey: item.LocalKey, UUID: item.UUID, GatewayID: item.GatewayID, OwnerID: item.OwnerID, UID: item.UID, AssetID: item.AssetID, RoomID: item.RoomID, RoomName: item.RoomName, HomeID: item.HomeID, HomeName: item.HomeName, Timezone: item.Timezone, ActiveTime: item.ActiveTime, UpdateTime: item.UpdateTime, Sub: item.Sub,
		})
	}
	return result, nil
}

func decodeSharingToken(raw []byte, timestamp int64) (Token, time.Time, error) {
	var wire struct {
		AccessToken       string `json:"accessToken"`
		AccessTokenSnake  string `json:"access_token"`
		RefreshToken      string `json:"refreshToken"`
		RefreshTokenSnake string `json:"refresh_token"`
		UID               string `json:"uid"`
		ExpireTime        int64  `json:"expireTime"`
		ExpireTimeSnake   int64  `json:"expire_time"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Token{}, time.Time{}, fmt.Errorf("decode Tuya sharing token: %w", err)
	}
	access, refresh := firstNonEmpty(wire.AccessToken, wire.AccessTokenSnake), firstNonEmpty(wire.RefreshToken, wire.RefreshTokenSnake)
	expires := wire.ExpireTime
	if expires == 0 {
		expires = wire.ExpireTimeSnake
	}
	if access == "" || refresh == "" || expires <= 0 {
		return Token{}, time.Time{}, errors.New("Tuya sharing token response did not contain complete token information")
	}
	if timestamp <= 0 {
		timestamp = time.Now().UnixMilli()
	}
	expiresAt := time.UnixMilli(timestamp).Add(time.Duration(expires) * time.Second)
	return Token{AccessToken: access, RefreshToken: refresh, UID: wire.UID, ExpiresIn: expires, ExpireTime: expires}, expiresAt, nil
}

// DecodeSharingToken decodes the token object returned by the Home Assistant
// QR login endpoint. The login endpoint adds endpoint and terminal metadata
// around this same token object, so the flow package can reuse this parser.
func DecodeSharingToken(raw []byte, timestamp int64) (Token, time.Time, error) {
	return decodeSharingToken(raw, timestamp)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sharingSecret(rid, sid, hashKey string) string {
	message := hashKey
	if sid != "" {
		length := len(sid)
		if length > 16 {
			length = 16
		}
		var encoded strings.Builder
		for index := 0; index < length; index++ {
			position := int(sid[index]) % 16
			if position >= len(sid) {
				position = len(sid) - 1
			}
			encoded.WriteByte(sid[position])
		}
		message += "_" + encoded.String()
	}
	mac := hmac.New(sha256.New, []byte(rid))
	_, _ = mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

func sharingSign(hashKey, queryData, bodyData string, headers http.Header) string {
	parts := make([]string, 0, 5)
	for _, name := range []string{"X-appKey", "X-requestId", "X-sid", "X-time", "X-token"} {
		if value := headers.Get(name); value != "" {
			parts = append(parts, name+"="+value)
		}
	}
	signText := strings.Join(parts, "||") + queryData + bodyData
	mac := hmac.New(sha256.New, []byte(hashKey))
	_, _ = mac.Write([]byte(signText))
	return hex.EncodeToString(mac.Sum(nil))
}

func encryptSharing(raw []byte, secret string, random func([]byte) (int, error)) (string, error) {
	key := []byte(secret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create Tuya sharing cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create Tuya sharing GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if random == nil {
		random = rand.Read
	}
	if _, err := random(nonce); err != nil {
		return "", errors.New("generate Tuya sharing nonce failed")
	}
	ciphertext := gcm.Seal(nil, nonce, raw, nil)
	return base64.StdEncoding.EncodeToString(nonce) + base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decryptSharing(value, secret string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher([]byte(secret))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(decoded) < gcm.NonceSize() {
		return nil, errors.New("Tuya sharing response is too short")
	}
	return gcm.Open(nil, decoded[:gcm.NonceSize()], decoded[gcm.NonceSize():], nil)
}

func redactSharingText(value string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	return value
}
