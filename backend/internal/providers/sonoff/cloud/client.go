package cloud

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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

const (
	// DefaultEndpoint is the commonly used eWeLink cloud endpoint. Regional
	// endpoints should be supplied to NewClient explicitly when needed.
	DefaultEndpoint = "https://eu-apia.coolkit.cc"
	DefaultAppID    = "R8Oq3y0eSZSYdKccHlrQzT1ACCOUT9Gv"

	pathHomes       = "/v2/family"
	pathDevices     = "/v2/device/thing"
	pathThing       = "/v2/device/thing/status"
	pathUserLogin   = "/v2/user/login"
	maxRequestBytes = 1 << 20

	maxResponseBytes = 8 << 20
)

var regionalEndpoints = map[string]string{
	"cn": "https://cn-apia.coolkit.cn",
	"as": "https://as-apia.coolkit.cc",
	"us": "https://us-apia.coolkit.cc",
	"eu": "https://eu-apia.coolkit.cc",
}

// LoginCredentials follows the account login used by SonoffLAN. AppID and
// AppSecret are optional: the reference-compatible signing pair is used when
// they are omitted, while explicit values are available for deployments that
// use their own eWeLink application.
type LoginCredentials struct {
	Username    string
	Password    string
	CountryCode string
	Region      string
	Endpoint    string
	AppID       string
	AppSecret   string
}

type LoginResult struct {
	AccessToken string `json:"accessToken"`
	Region      string `json:"region"`
	Endpoint    string `json:"endpoint"`
}

// EndpointForRegion returns the eWeLink API endpoint for a SonoffLAN region.
func EndpointForRegion(region string) string {
	return regionalEndpoints[strings.ToLower(strings.TrimSpace(region))]
}

var countryRegions = map[string]string{
	"+93": "as", "+355": "eu", "+213": "eu", "+376": "eu", "+244": "eu", "+1264": "us", "+1268": "as", "+54": "us", "+374": "as", "+297": "eu", "+247": "eu", "+61": "us", "+43": "eu", "+994": "as", "+1242": "us", "+973": "as", "+880": "as", "+1246": "us", "+375": "eu", "+32": "eu", "+501": "us", "+229": "eu", "+1441": "as", "+591": "us", "+387": "eu", "+267": "eu", "+55": "us", "+673": "as", "+359": "eu", "+226": "eu", "+257": "eu", "+855": "as", "+237": "eu", "+238": "eu", "+1345": "as", "+236": "eu", "+235": "eu", "+56": "us", "+86": "cn", "+57": "us", "+682": "us", "+506": "us", "+385": "eu", "+53": "us", "+357": "eu", "+420": "eu", "+243": "eu", "+45": "eu", "+253": "eu", "+1767": "as", "+1809": "us", "+670": "as", "+684": "us", "+593": "us", "+20": "eu", "+503": "us", "+372": "eu", "+251": "eu", "+298": "eu", "+679": "us", "+358": "eu", "+33": "eu", "+594": "us", "+689": "as", "+241": "eu", "+220": "eu", "+995": "as", "+49": "eu", "+233": "eu", "+350": "eu", "+30": "eu", "+299": "us", "+1473": "as", "+590": "us", "+1671": "us", "+502": "us", "+240": "eu", "+224": "eu", "+592": "us", "+509": "us", "+504": "us", "+852": "as", "+36": "eu", "+354": "eu", "+91": "as", "+62": "as", "+98": "as", "+353": "eu", "+269": "eu", "+972": "as", "+39": "eu", "+225": "eu", "+1876": "us", "+81": "as", "+962": "as", "+254": "eu", "+975": "as", "+383": "eu", "+965": "as", "+996": "as", "+856": "as", "+371": "eu", "+961": "as", "+266": "eu", "+231": "eu", "+218": "eu", "+423": "eu", "+370": "eu", "+352": "eu", "+853": "as", "+261": "eu", "+265": "eu", "+60": "as", "+960": "as", "+223": "eu", "+356": "eu", "+596": "us", "+222": "eu", "+230": "eu", "+52": "us", "+373": "eu", "+377": "eu", "+976": "as", "+382": "as", "+1664": "as", "+212": "eu", "+258": "eu", "+95": "as", "+264": "eu", "+977": "as", "+31": "eu", "+599": "as", "+687": "as", "+64": "us", "+505": "us", "+227": "eu", "+234": "eu", "+47": "eu", "+968": "as", "+92": "as", "+970": "as", "+507": "us", "+675": "as", "+595": "us", "+51": "us", "+63": "as", "+48": "eu", "+351": "eu", "+974": "as", "+242": "eu", "+964": "as", "+389": "eu", "+262": "eu", "+40": "eu", "+7": "eu", "+250": "eu", "+1869": "as", "+1758": "us", "+1784": "as", "+378": "eu", "+239": "eu", "+966": "as", "+221": "eu", "+381": "eu", "+248": "eu", "+232": "eu", "+65": "as", "+421": "eu", "+386": "eu", "+27": "eu", "+82": "as", "+34": "eu", "+94": "as", "+249": "eu", "+597": "us", "+268": "eu", "+46": "eu", "+41": "eu", "+963": "as", "+886": "as", "+992": "as", "+255": "eu", "+66": "as", "+228": "eu", "+676": "us", "+1868": "us", "+216": "eu", "+90": "as", "+993": "as", "+1649": "as", "+44": "eu", "+256": "eu", "+380": "eu", "+971": "as", "+1": "us", "+598": "us", "+998": "as", "+678": "us", "+58": "us", "+84": "as", "+685": "us", "+1340": "as", "+967": "as", "+260": "eu", "+263": "eu",
}

func regionForCountryCode(countryCode string) string {
	if region, ok := countryRegions[strings.TrimSpace(countryCode)]; ok {
		return region
	}
	return "eu"
}

// Login authenticates an eWeLink account through the same v2 endpoint used
// by SonoffLAN. The returned token is safe to store as a provider secret.
func Login(ctx context.Context, httpClient *http.Client, credentials LoginCredentials, timeout time.Duration) (LoginResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	credentials.Username = strings.TrimSpace(credentials.Username)
	credentials.Password = strings.TrimSpace(credentials.Password)
	credentials.CountryCode = strings.TrimSpace(credentials.CountryCode)
	if credentials.Username == "" || credentials.Password == "" {
		return LoginResult{}, errors.New("sonoff username and password are required")
	}
	if credentials.CountryCode == "" {
		credentials.CountryCode = "+86"
	}
	region := strings.ToLower(strings.TrimSpace(credentials.Region))
	if region == "" || region == "auto" {
		region = regionForCountryCode(credentials.CountryCode)
	}
	endpoint := strings.TrimRight(strings.TrimSpace(credentials.Endpoint), "/")
	if endpoint == "" {
		endpoint = EndpointForRegion(region)
	}
	if endpoint == "" {
		return LoginResult{}, errors.New("sonoff cloud region is unsupported")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	} else if timeout > 0 && httpClient.Timeout == 0 {
		copyOf := *httpClient
		copyOf.Timeout = timeout
		httpClient = &copyOf
	}
	appID := strings.TrimSpace(credentials.AppID)
	if appID == "" {
		appID = DefaultAppID
	}
	appSecret := strings.TrimSpace(credentials.AppSecret)
	if appSecret == "" {
		appSecret = defaultAppSecret
	}
	payload := map[string]string{"password": credentials.Password, "countryCode": credentials.CountryCode}
	if strings.Contains(credentials.Username, "@") {
		payload["email"] = credentials.Username
	} else if strings.HasPrefix(credentials.Username, "+") {
		payload["phoneNumber"] = credentials.Username
	} else {
		payload["phoneNumber"] = "+" + credentials.Username
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return LoginResult{}, errors.New("encode sonoff login request failed")
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, responseRegion, responseCode, requestErr := loginRequest(ctx, httpClient, endpoint, appID, appSecret, body)
		if requestErr != nil {
			return LoginResult{}, requestErr
		}
		if responseCode == 10004 && responseRegion != "" && attempt == 0 {
			endpoint = EndpointForRegion(responseRegion)
			if endpoint == "" {
				return LoginResult{}, &ResponseCodeError{Code: responseCode}
			}
			continue
		}
		if responseCode != 0 {
			return LoginResult{}, &ResponseCodeError{Code: responseCode}
		}
		if strings.TrimSpace(result.AccessToken) == "" {
			return LoginResult{}, errors.New("sonoff login response did not contain an access token")
		}
		result.Region = nonEmptyString(responseRegion, region)
		result.Endpoint = endpoint
		return result, nil
	}
	return LoginResult{}, errors.New("sonoff login region discovery failed")
}

func loginRequest(ctx context.Context, httpClient *http.Client, endpoint, appID, appSecret string, body []byte) (LoginResult, string, int, error) {
	requestURL := strings.TrimRight(endpoint, "/") + pathUserLogin
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return LoginResult{}, "", 0, errors.New("create sonoff login request failed")
	}
	signature := hmac.New(sha256.New, []byte(appSecret))
	_, _ = signature.Write(body)
	request.Header.Set("Authorization", "Sign "+base64.StdEncoding.EncodeToString(signature.Sum(nil)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-CK-Appid", appID)
	response, err := httpClient.Do(request)
	if err != nil {
		return LoginResult{}, "", 0, errors.New("sonoff login request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return LoginResult{}, "", response.StatusCode, &HTTPStatusError{StatusCode: response.StatusCode}
	}
	limited := io.LimitReader(response.Body, maxRequestBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil || len(responseBody) > maxRequestBytes {
		return LoginResult{}, "", 0, errors.New("read sonoff login response failed")
	}
	var envelope struct {
		Error int    `json:"error"`
		Msg   string `json:"msg"`
		Data  struct {
			At          string `json:"at"`
			AccessToken string `json:"accessToken"`
			Region      string `json:"region"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return LoginResult{}, "", 0, errors.New("decode sonoff login response failed")
	}
	return LoginResult{AccessToken: nonEmptyString(envelope.Data.At, envelope.Data.AccessToken)}, envelope.Data.Region, envelope.Error, nil
}

func nonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// defaultAppSecret is the reference-compatible signing key derived by
// SonoffLAN from its public region table. It is not a user credential.
const defaultAppSecret = "1ve5Qk9GXfUhKAn1svnKwpAlxXkMarru"

// Authenticator adds authentication to an individual request. It is kept
// deliberately small so OAuth, service accounts, or test authenticators can
// be added without teaching this package an account/password protocol.
type Authenticator interface {
	Authenticate(*http.Request) error
}

// TokenAuthenticator injects an already-issued eWeLink access token as a
// bearer token. It never includes the token in an error or string value.
type TokenAuthenticator struct {
	AccessToken string
}

func NewTokenAuthenticator(accessToken string) *TokenAuthenticator {
	return &TokenAuthenticator{AccessToken: accessToken}
}

func (a TokenAuthenticator) Authenticate(request *http.Request) error {
	if strings.TrimSpace(a.AccessToken) == "" {
		return errors.New("sonoff access token is required")
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(a.AccessToken))
	return nil
}

// Options configures a Client. Endpoint must be an absolute URL. HTTPClient
// and Authenticator are optional, which is useful for public test endpoints
// and for callers that install authentication through a custom transport.
type Options struct {
	Endpoint      string
	HTTPClient    *http.Client
	Authenticator Authenticator
	Credentials   *LoginCredentials
	AppID         string
}

// Client is an injectable eWeLink cloud REST client.
type Client struct {
	endpoint      *url.URL
	httpClient    *http.Client
	authenticator Authenticator
	credentials   *LoginCredentials
	appID         string
	mu            sync.Mutex
}

// NewClient creates a client with an already-issued access token. The token
// is only used by TokenAuthenticator and is never copied into an error.
func NewClient(httpClient *http.Client, endpoint, accessToken string, timeout time.Duration) (*Client, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	} else if timeout > 0 && httpClient.Timeout == 0 {
		clientCopy := *httpClient
		clientCopy.Timeout = timeout
		httpClient = &clientCopy
	}
	var authenticator Authenticator
	if strings.TrimSpace(accessToken) != "" {
		authenticator = TokenAuthenticator{AccessToken: accessToken}
	}
	return NewClientWithOptions(Options{
		Endpoint:      endpoint,
		HTTPClient:    httpClient,
		Authenticator: authenticator,
	})
}

// NewClientWithAuthenticator is the constructor for callers that need an
// authentication strategy other than an already-issued bearer token.
func NewClientWithAuthenticator(httpClient *http.Client, endpoint string, authenticator Authenticator, timeout time.Duration) (*Client, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	} else if timeout > 0 && httpClient.Timeout == 0 {
		clientCopy := *httpClient
		clientCopy.Timeout = timeout
		httpClient = &clientCopy
	}
	return NewClientWithOptions(Options{
		Endpoint:      endpoint,
		HTTPClient:    httpClient,
		Authenticator: authenticator,
	})
}

func NewClientWithOptions(options Options) (*Client, error) {
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("sonoff cloud endpoint must be an absolute URL")
	}
	if parsed.User != nil {
		return nil, errors.New("sonoff cloud endpoint must not contain user information")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("sonoff cloud endpoint must not contain query or fragment")
	}
	if options.HTTPClient == nil {
		options.HTTPClient = http.DefaultClient
	}
	return &Client{
		endpoint:      parsed,
		httpClient:    options.HTTPClient,
		authenticator: options.Authenticator,
		credentials:   options.Credentials,
		appID:         strings.TrimSpace(options.AppID),
	}, nil
}

// NewClientWithCredentials creates a client that logs in lazily on its first
// cloud request. The provider UI normally performs Login eagerly, but lazy
// login keeps stored account credentials useful after an access token expires.
func NewClientWithCredentials(httpClient *http.Client, endpoint string, credentials LoginCredentials, timeout time.Duration) (*Client, error) {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = EndpointForRegion(credentials.Region)
		if endpoint == "" {
			endpoint = EndpointForRegion(regionForCountryCode(credentials.CountryCode))
		}
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	} else if timeout > 0 && httpClient.Timeout == 0 {
		copyOf := *httpClient
		copyOf.Timeout = timeout
		httpClient = &copyOf
	}
	return NewClientWithOptions(Options{Endpoint: endpoint, HTTPClient: httpClient, Credentials: &credentials, AppID: credentials.AppID})
}

// ListHomes returns the homes/families visible to the authenticated user.
func (c *Client) ListHomes(ctx context.Context) ([]CloudHome, error) {
	payload, err := c.do(ctx, http.MethodGet, pathHomes, nil, nil)
	if err != nil {
		return nil, err
	}
	items, ok := collectionPayload(payload, "familyList", "homeList", "homes", "families")
	if !ok {
		return nil, errors.New("sonoff cloud homes response has no list")
	}
	homes := make([]CloudHome, 0, len(items))
	for _, item := range items {
		var home CloudHome
		if err := json.Unmarshal(item, &home); err != nil {
			return nil, errors.New("decode sonoff cloud home failed")
		}
		normalizeHome(&home)
		homes = append(homes, home)
	}
	return homes, nil
}

// ListDevices returns all devices visible to the authenticated user.
func (c *Client) ListDevices(ctx context.Context) ([]Device, error) {
	payload, err := c.do(ctx, http.MethodGet, pathDevices, url.Values{"num": []string{"0"}}, nil)
	if err != nil {
		return nil, err
	}
	items, ok := collectionPayload(payload, "thingList", "deviceList", "devices", "things")
	if !ok {
		return nil, errors.New("sonoff cloud devices response has no list")
	}
	devices := make([]Device, 0, len(items))
	for _, item := range items {
		var device Device
		if err := json.Unmarshal(item, &device); err != nil {
			return nil, errors.New("decode sonoff cloud device failed")
		}
		devices = append(devices, device)
	}
	return devices, nil
}

// ListDevicesForHome is an optional convenience for the eWeLink endpoint
// variant that accepts a familyid query parameter.
func (c *Client) ListDevicesForHome(ctx context.Context, homeID string) ([]Device, error) {
	query := url.Values{"num": []string{"0"}, "familyid": []string{strings.TrimSpace(homeID)}}
	payload, err := c.do(ctx, http.MethodGet, pathDevices, query, nil)
	if err != nil {
		return nil, err
	}
	items, ok := collectionPayload(payload, "thingList", "deviceList", "devices", "things")
	if !ok {
		return nil, errors.New("sonoff cloud devices response has no list")
	}
	devices := make([]Device, 0, len(items))
	for _, item := range items {
		var device Device
		if err := json.Unmarshal(item, &device); err != nil {
			return nil, errors.New("decode sonoff cloud device failed")
		}
		devices = append(devices, device)
	}
	return devices, nil
}

// SetDeviceState sends a device state command. No command or response body is
// copied into errors.
func (c *Client) SetDeviceState(ctx context.Context, deviceID string, params map[string]any) error {
	command, err := commandFromArguments(deviceID, params)
	if err != nil {
		return err
	}
	body, err := json.Marshal(deviceStatusCommand{Type: 1, ID: command.DeviceID, Params: command.Params})
	if err != nil {
		return errors.New("encode sonoff cloud command failed")
	}
	_, err = c.do(ctx, http.MethodPost, pathThing, nil, body)
	return err
}

// SetDeviceStateResult is useful to callers that need the cloud's echoed
// state. The integration-facing SetDeviceState intentionally returns only an
// error.
func (c *Client) SetDeviceStateResult(ctx context.Context, command Command) (CloudState, error) {
	if strings.TrimSpace(command.DeviceID) == "" || command.Params == nil {
		return CloudState{}, errors.New("sonoff cloud command requires device id and params")
	}
	body, err := json.Marshal(deviceStatusCommand{Type: 1, ID: command.DeviceID, Params: command.Params})
	if err != nil {
		return CloudState{}, errors.New("encode sonoff cloud command failed")
	}
	payload, err := c.do(ctx, http.MethodPost, pathThing, nil, body)
	if err != nil {
		return CloudState{}, err
	}
	return decodeCloudState(payload, command.DeviceID, command.Params), nil
}

type deviceStatusCommand struct {
	Type   int            `json:"type"`
	ID     string         `json:"id"`
	Params map[string]any `json:"params"`
}

func commandFromArguments(deviceID string, params map[string]any) (Command, error) {
	if strings.TrimSpace(deviceID) == "" || params == nil {
		return Command{}, errors.New("sonoff cloud command requires device id and params")
	}
	return Command{DeviceID: strings.TrimSpace(deviceID), Params: params}, nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body []byte) (json.RawMessage, error) {
	if err := c.ensureAuthenticated(ctx); err != nil {
		return nil, err
	}
	payload, err := c.doRequest(ctx, method, path, query, body)
	if err == nil || c.credentials == nil || !shouldRelogin(err) {
		return payload, err
	}
	c.mu.Lock()
	c.authenticator = nil
	c.mu.Unlock()
	if err := c.ensureAuthenticated(ctx); err != nil {
		return nil, err
	}
	return c.doRequest(ctx, method, path, query, body)
}

func (c *Client) doRequest(ctx context.Context, method, path string, query url.Values, body []byte) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	endpoint := *c.endpoint
	authenticator := c.authenticator
	appID := c.appID
	c.mu.Unlock()
	requestURL := endpoint
	requestURL.Path = strings.TrimRight(endpoint.Path, "/") + "/" + strings.TrimLeft(path, "/")
	requestURL.RawQuery = query.Encode()
	requestURL.Fragment = ""
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("create sonoff cloud request failed")
	}
	request.Header.Set("Accept", "application/json")
	if strings.TrimSpace(appID) != "" {
		request.Header.Set("X-CK-Appid", strings.TrimSpace(appID))
	}
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if authenticator != nil {
		if err := authenticator.Authenticate(request); err != nil {
			return nil, errors.New("sonoff cloud authentication failed")
		}
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, errors.New("sonoff cloud request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &HTTPStatusError{StatusCode: response.StatusCode}
	}
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return nil, errors.New("read sonoff cloud response failed")
	}
	if len(responseBody) > maxResponseBytes {
		return nil, errors.New("sonoff cloud response is too large")
	}
	var envelope responseEnvelope
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return nil, errors.New("decode sonoff cloud response failed")
	}
	if code, present := nonZeroResponseCode(envelope.Code, envelope.Error); present {
		return nil, &ResponseCodeError{Code: code}
	}
	if envelope.Success != nil && !*envelope.Success {
		return nil, errors.New("sonoff cloud response reported failure")
	}
	payload := envelope.Data
	if isJSONNullOrEmpty(payload) {
		payload = envelope.Result
	}
	if isJSONNullOrEmpty(payload) {
		payload = json.RawMessage("null")
	}
	return cloneRawMessage(payload), nil
}

func shouldRelogin(err error) bool {
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode == http.StatusUnauthorized || statusErr.StatusCode == http.StatusForbidden
	}
	var codeErr *ResponseCodeError
	if errors.As(err, &codeErr) {
		return codeErr.Code == http.StatusUnauthorized || codeErr.Code == http.StatusForbidden || codeErr.Code == 406
	}
	return false
}

func (c *Client) ensureAuthenticated(ctx context.Context) error {
	if c.credentials == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.authenticator != nil {
		return nil
	}
	credentials := *c.credentials
	if credentials.Endpoint == "" {
		credentials.Endpoint = c.endpoint.String()
	}
	if credentials.AppID == "" {
		credentials.AppID = c.appID
	}
	result, err := Login(ctx, c.httpClient, credentials, c.httpClient.Timeout)
	if err != nil {
		return errors.New("sonoff cloud account login failed")
	}
	parsed, err := url.Parse(result.Endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("sonoff cloud login returned an invalid endpoint")
	}
	c.endpoint = parsed
	c.authenticator = TokenAuthenticator{AccessToken: result.AccessToken}
	if c.appID == "" {
		c.appID = credentials.AppID
	}
	return nil
}

type responseEnvelope struct {
	Code    json.RawMessage `json:"code"`
	Error   json.RawMessage `json:"error"`
	Success *bool           `json:"success"`
	Data    json.RawMessage `json:"data"`
	Result  json.RawMessage `json:"result"`
}

func nonZeroResponseCode(values ...json.RawMessage) (int, bool) {
	for _, value := range values {
		if isJSONNullOrEmpty(value) {
			continue
		}
		code, err := responseCode(value)
		if err != nil {
			return 0, true
		}
		if code != 0 {
			return code, true
		}
	}
	return 0, false
}

func responseCode(raw json.RawMessage) (int, error) {
	var number int
	if err := json.Unmarshal(raw, &number); err == nil {
		return number, nil
	}
	var textValue string
	if err := json.Unmarshal(raw, &textValue); err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(textValue))
}

func collectionPayload(payload json.RawMessage, keys ...string) ([]json.RawMessage, bool) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 || bytes.Equal(payload, []byte("null")) {
		return nil, false
	}
	var direct []json.RawMessage
	if json.Unmarshal(payload, &direct) == nil {
		return direct, true
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(payload, &object) != nil {
		return nil, false
	}
	for _, key := range keys {
		value, ok := object[key]
		if !ok {
			continue
		}
		items, found := collectionPayload(value, keys...)
		if found {
			return items, true
		}
	}
	return nil, false
}

func normalizeHome(home *CloudHome) {
	if home.HomeID == "" {
		home.HomeID = home.FamilyID
	}
	if home.FamilyID == "" {
		home.FamilyID = home.HomeID
	}
	if home.ID == "" {
		home.ID = home.HomeID
	}
	if home.HomeID == "" {
		home.HomeID = home.ID
	}
}

func decodeCloudState(payload json.RawMessage, deviceID string, fallbackParams map[string]any) CloudState {
	state := CloudState{DeviceID: deviceID}
	var object map[string]json.RawMessage
	if json.Unmarshal(payload, &object) != nil {
		state.Params = cloneParams(fallbackParams)
		state.RawParams = marshalParams(fallbackParams)
		return state
	}
	if rawDeviceID, ok := object["deviceid"]; ok {
		_ = json.Unmarshal(rawDeviceID, &state.DeviceID)
	}
	if rawParams, ok := object["params"]; ok {
		state.RawParams = cloneRawMessage(rawParams)
		_ = json.Unmarshal(rawParams, &state.Params)
	} else {
		state.RawParams = cloneRawMessage(payload)
		_ = json.Unmarshal(payload, &state.Params)
	}
	if state.Params == nil {
		state.Params = cloneParams(fallbackParams)
		if len(state.RawParams) == 0 {
			state.RawParams = marshalParams(fallbackParams)
		}
	}
	return state
}

func cloneParams(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	copyOf := make(map[string]any, len(params))
	for key, value := range params {
		copyOf[key] = value
	}
	return copyOf
}

func marshalParams(params map[string]any) json.RawMessage {
	if params == nil {
		return nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil
	}
	return raw
}

func isJSONNullOrEmpty(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// HTTPStatusError reports only the HTTP status, never the response body.
type HTTPStatusError struct {
	StatusCode int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("sonoff cloud returned HTTP status %d", e.StatusCode)
}

// ResponseCodeError reports only the numeric application code. The response
// message is intentionally omitted because cloud services can echo secrets.
type ResponseCodeError struct {
	Code int
}

func (e *ResponseCodeError) Error() string {
	return fmt.Sprintf("sonoff cloud returned response code %d", e.Code)
}
