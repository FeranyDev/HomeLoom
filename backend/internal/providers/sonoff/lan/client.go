package lan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	DefaultTimeout      = 2 * time.Second
	maximumResponseSize = 1 << 20
)

// Client is a small, injectable HTTP client for Sonoff zeroconf endpoints.
// HTTPClient is an interface so tests can provide a *http.Client with a
// custom RoundTripper while production callers can use any normal client.
type Client struct {
	HTTPClient Doer
	Timeout    time.Duration
}

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// NewClient creates a Sonoff LAN client. A nil HTTP client uses
// http.DefaultClient. A non-positive timeout uses DefaultTimeout.
func NewClient(httpClient *http.Client, timeout time.Duration) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{HTTPClient: httpClient, Timeout: timeout}
}

// HTTPError describes a non-2xx HTTP response. Body is bounded to avoid
// retaining an unexpectedly large device response in an error.
type HTTPError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("Sonoff HTTP request failed with status %s", e.Status)
	}
	return fmt.Sprintf("Sonoff HTTP request failed with status %s: %s", e.Status, e.Body)
}

// ResponseError describes an application-level Sonoff response with a
// non-zero error code, normally returned with HTTP 200.
type ResponseError struct {
	Code     int
	Response map[string]any
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("Sonoff zeroconf request failed with error %d", e.Code)
}

// Command sends POST /zeroconf/{command}. data is encoded as a JSON object in
// DIY mode and as base64 AES ciphertext otherwise. The returned map is the
// decoded response; encrypted response data is transparently decoded too.
func (c *Client) Command(ctx context.Context, req Request, command string, data map[string]any) (map[string]any, error) {
	if err := validateCommand(command); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal Sonoff %s data: %w", command, err)
	}

	encrypt := !req.DIY
	envelope := map[string]any{
		"sequence":   requestSequence(req.Sequence),
		"deviceid":   req.DeviceID,
		"selfApikey": requestSelfAPIKey(req.SelfAPIKey),
		"encrypt":    encrypt,
	}
	if req.DeviceID == "" {
		return nil, errors.New("Sonoff device ID is required")
	}

	if encrypt {
		if req.DeviceKey == "" {
			return nil, errors.New("Sonoff device key is required for encrypted LAN requests")
		}
		iv, ivText, err := requestIV(req.IV)
		if err != nil {
			return nil, err
		}
		encoded, err := Encode(req.DeviceKey, payload, iv)
		if err != nil {
			return nil, fmt.Errorf("encode Sonoff %s data: %w", command, err)
		}
		envelope["data"] = encoded
		envelope["iv"] = ivText
	} else {
		// Decode into any so the JSON object remains an object in the outgoing
		// envelope rather than becoming a quoted JSON string.
		var clear any
		if err := json.Unmarshal(payload, &clear); err != nil {
			return nil, fmt.Errorf("decode Sonoff %s data: %w", command, err)
		}
		envelope["data"] = clear
		if req.IV != "" {
			if _, err := ParseIV(req.IV); err != nil {
				return nil, err
			}
			envelope["iv"] = req.IV
		}
	}

	endpoint, err := req.Endpoint()
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal Sonoff %s request: %w", command, err)
	}
	response, err := c.post(ctx, endpoint+"/zeroconf/"+command, body)
	if err != nil {
		return nil, err
	}
	if encrypted, ok := response["encrypt"].(bool); ok && encrypted {
		if err := decodeResponseDataWithKey(response, req.DeviceKey); err != nil {
			return nil, err
		}
	}
	if responseError := applicationError(response); responseError != nil {
		return nil, responseError
	}
	return response, nil
}

// GetState sends POST /zeroconf/getState with an empty data object.
func (c *Client) GetState(ctx context.Context, req Request) (map[string]any, error) {
	return c.Command(ctx, req, "getState", map[string]any{})
}

// Switch sends the single-channel switch command.
func (c *Client) Switch(ctx context.Context, req Request, state string) (map[string]any, error) {
	if state != "on" && state != "off" {
		return nil, fmt.Errorf("invalid Sonoff switch state %q", state)
	}
	return c.Command(ctx, req, "switch", map[string]any{"switch": state})
}

// SwitchCommand is one outlet in a multi-channel switch command.
type SwitchCommand struct {
	Outlet int    `json:"outlet"`
	Switch string `json:"switch"`
}

type SwitchChannel = SwitchCommand

// Switches sends the multi-channel switch command.
func (c *Client) Switches(ctx context.Context, req Request, switches []SwitchCommand) (map[string]any, error) {
	if len(switches) == 0 {
		return nil, errors.New("Sonoff switches command needs at least one outlet")
	}
	for _, item := range switches {
		if item.Outlet < 0 {
			return nil, fmt.Errorf("invalid Sonoff outlet %d", item.Outlet)
		}
		if item.Switch != "on" && item.Switch != "off" {
			return nil, fmt.Errorf("invalid Sonoff switch state %q", item.Switch)
		}
	}
	return c.Command(ctx, req, "switches", map[string]any{"switches": switches})
}

// Light sends the device-specific light command payload.
func (c *Client) Light(ctx context.Context, req Request, data map[string]any) (map[string]any, error) {
	return c.Command(ctx, req, "light", data)
}

// Fan sends the device-specific fan command payload.
func (c *Client) Fan(ctx context.Context, req Request, data map[string]any) (map[string]any, error) {
	return c.Command(ctx, req, "fan", data)
}

// Cover sends the device-specific cover command payload, such as
// {"location": 50}.
func (c *Client) Cover(ctx context.Context, req Request, data map[string]any) (map[string]any, error) {
	return c.Command(ctx, req, "cover", data)
}

func (c *Client) post(ctx context.Context, endpoint string, payload []byte) (map[string]any, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("create Sonoff HTTP request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		if requestContext.Err() != nil {
			return nil, fmt.Errorf("Sonoff HTTP request timeout: %w", requestContext.Err())
		}
		return nil, fmt.Errorf("Sonoff HTTP request: %w", err)
	}
	if response == nil {
		return nil, errors.New("Sonoff HTTP client returned a nil response")
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read Sonoff HTTP response: %w", err)
	}
	if len(responseBody) > maximumResponseSize {
		return nil, fmt.Errorf("Sonoff HTTP response exceeds %d bytes", maximumResponseSize)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &HTTPError{StatusCode: response.StatusCode, Status: response.Status, Body: strings.TrimSpace(string(responseBody))}
	}
	var decoded map[string]any
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, fmt.Errorf("decode Sonoff HTTP response: %w", err)
	}
	return decoded, nil
}

func decodeResponseDataWithKey(response map[string]any, deviceKey string) error {
	encoded, ok := response["data"].(string)
	if !ok || encoded == "" {
		return nil
	}
	ivText, ok := response["iv"].(string)
	if !ok {
		return errors.New("encrypted Sonoff response has no IV")
	}
	iv, err := ParseIV(ivText)
	if err != nil {
		return fmt.Errorf("parse encrypted Sonoff response IV: %w", err)
	}
	decoded, err := Decode(deviceKey, encoded, iv)
	if err != nil {
		return fmt.Errorf("decode encrypted Sonoff response data: %w", err)
	}
	var data any
	if err := json.Unmarshal(decoded, &data); err != nil {
		return fmt.Errorf("decode encrypted Sonoff response JSON: %w", err)
	}
	response["data"] = data
	return nil
}

func applicationError(response map[string]any) error {
	value, exists := response["error"]
	if !exists {
		return nil
	}
	code, ok := numberAsInt(value)
	if !ok || code == 0 {
		return nil
	}
	return &ResponseError{Code: code, Response: response}
}

func numberAsInt(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), typed == float64(int(typed))
	case json.Number:
		parsed, err := strconv.Atoi(string(typed))
		return parsed, err == nil
	case int:
		return typed, true
	default:
		return 0, false
	}
}

func validateCommand(command string) error {
	switch command {
	case "switch", "switches", "light", "fan", "cover", "getState":
		return nil
	default:
		return fmt.Errorf("unsupported Sonoff zeroconf command %q", command)
	}
}

func requestSequence(sequence string) string {
	if sequence != "" {
		return sequence
	}
	return strconv.FormatInt(nextSequence(), 10)
}

var lastSequence atomic.Int64

func nextSequence() int64 {
	now := time.Now().UnixMilli()
	for {
		previous := lastSequence.Load()
		if now <= previous {
			now = previous + 1
		}
		if lastSequence.CompareAndSwap(previous, now) {
			return now
		}
	}
}

func requestSelfAPIKey(value string) string {
	if value == "" {
		return "123"
	}
	return value
}

func requestIV(value string) ([]byte, string, error) {
	if value != "" {
		iv, err := ParseIV(value)
		if err != nil {
			return nil, "", err
		}
		encoded, err := EncodeIV(iv)
		return iv, encoded, err
	}
	iv, err := randomIV()
	if err != nil {
		return nil, "", err
	}
	encoded, err := EncodeIV(iv)
	return iv, encoded, err
}
