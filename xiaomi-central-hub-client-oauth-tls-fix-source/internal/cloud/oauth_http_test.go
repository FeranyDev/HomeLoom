package cloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestExchangeCodeRequest(t *testing.T) {
	client := OAuthClient{
		ClientID:    "123456789012345678",
		RedirectURL: "http://homeassistant.local:8123/callback",
		Region:      "cn",
		DeviceID:    "ha.0123456789abcdef0123456789abcdef",
	}
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != TokenPath {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(request.URL.Query().Get("data")), &data); err != nil {
			t.Fatal(err)
		}
		if data["device_id"] != client.DeviceID || data["code"] != "test-code" {
			t.Fatalf("unexpected token data: %#v", data)
		}
		return jsonResponse(`{"code":0,"result":{"access_token":"access","refresh_token":"refresh","expires_in":3600}}`), nil
	})}

	token, err := client.ExchangeCode(context.Background(), "test-code")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access" || token.RefreshToken != "refresh" || token.ExpiresIn != 3600 {
		t.Fatalf("unexpected token: %#v", token)
	}
}

func TestGetAccountUID(t *testing.T) {
	client := OAuthClient{
		ClientID:    "123456789012345678",
		RedirectURL: "http://homeassistant.local:8123/callback",
		Region:      "cn",
		DeviceID:    "ha.0123456789abcdef0123456789abcdef",
	}
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != HomeInfoPath {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Beareraccess-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := request.Header.Get("X-Client-AppId"); got != client.ClientID {
			t.Fatalf("X-Client-AppId = %q", got)
		}
		return jsonResponse(`{"code":0,"result":{"homelist":[{"uid":1234567890}]}}`), nil
	})}

	uid, err := client.GetAccountUID(context.Background(), "access-token")
	if err != nil {
		t.Fatal(err)
	}
	if uid != "1234567890" {
		t.Fatalf("UID = %q, want 1234567890", uid)
	}
}
