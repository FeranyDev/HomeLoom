package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSharingClientUsesEncryptedCustomerAPIAndMapsDevices(t *testing.T) {
	var requests []*http.Request
	var commandEnvelope map[string]string
	httpClient := &http.Client{Transport: sharingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request)
		if request.Header.Get("X-appKey") != HomeAssistantClientID || request.Header.Get("X-requestId") == "" || request.Header.Get("X-time") != "1700000000000" || request.Header.Get("X-token") != "access-token" || request.Header.Get("X-sign") == "" {
			t.Errorf("sharing headers = %#v", request.Header)
		}
		switch request.URL.Path {
		case "/v1.0/m/life/users/homes":
			return sharingAPIResponse(`{"success":true,"result":[{"ownerId":"home-1"}]}`), nil
		case "/v1.0/m/life/ha/home/devices":
			if request.URL.Query().Get("encdata") == "" {
				t.Error("home device request did not encrypt query data")
			}
			return sharingAPIResponse(`{"success":true,"result":[{"id":"device-1","name":"客厅灯","categoryCode":"dj","product_id":"product-1","online":true,"status":[{"code":"switch_led","value":true}]}]}`), nil
		case "/v1.1/m/life/device-1/specifications":
			return sharingAPIResponse(`{"success":true,"result":{"category":"dj","functions":[{"code":"switch_led","type":"Boolean","values":"{}"}],"status":[{"code":"switch_led","type":"Boolean","values":"{}"}]}}`), nil
		case "/v1.0/m/life/ha/devices/detail":
			return sharingAPIResponse(`{"success":true,"result":[{"id":"device-1","status":[{"code":"switch_led","value":false}]}]}`), nil
		case "/v1.1/m/thing/device-1/commands":
			if err := json.NewDecoder(request.Body).Decode(&commandEnvelope); err != nil {
				t.Errorf("decode encrypted command envelope: %v", err)
			}
			if commandEnvelope["encdata"] == "" {
				t.Error("command body did not contain encrypted data")
			}
			return sharingAPIResponse(`{"success":true,"result":true}`), nil
		default:
			return sharingAPIResponse(`{"success":false,"code":1000,"msg":"missing"}`), nil
		}
	})}
	client, err := NewSharingClient("https://tuya.test", HomeAssistantClientID, "user-code", "terminal-1", Token{AccessToken: "access-token", RefreshToken: "refresh-token"}, time.Now().Add(time.Hour), httpClient)
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return time.UnixMilli(1_700_000_000_000) }
	client.random = func(value []byte) (int, error) {
		for index := range value {
			value[index] = byte(index + 1)
		}
		return len(value), nil
	}

	devices, err := client.ListUserDevices(context.Background(), "", 0, 0)
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices = %#v, err=%v", devices, err)
	}
	if devices[0].ID != "device-1" || devices[0].Category != "dj" || devices[0].ProductID != "product-1" || !devices[0].Online || len(devices[0].Status) != 1 {
		t.Fatalf("device mapping = %#v", devices[0])
	}
	specification, err := client.GetSpecification(context.Background(), "device-1")
	if err != nil || specification.Category != "dj" || len(specification.Functions) != 1 || !specification.Functions[0].Readable || !specification.Functions[0].Writable {
		t.Fatalf("specification = %#v, err=%v", specification, err)
	}
	status, err := client.GetStatus(context.Background(), "device-1")
	if err != nil || len(status) != 1 || status[0].Value != false {
		t.Fatalf("status = %#v, err=%v", status, err)
	}
	if err := client.SendCommands(context.Background(), "device-1", []Command{{Code: "switch_led", Value: true}}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 5 {
		t.Fatalf("requests = %d, want 5", len(requests))
	}
	if requests[0].URL.Query().Get("encdata") != "" {
		t.Fatal("request without parameters unexpectedly has encdata")
	}
}

func TestSharingEncryptionRoundTripAndSignature(t *testing.T) {
	secret := sharingSecret("request-id", "", "0123456789abcdef0123456789abcdef")
	if len(secret) != 16 {
		t.Fatalf("sharing secret length = %d", len(secret))
	}
	random := func(value []byte) (int, error) {
		for index := range value {
			value[index] = byte(index)
		}
		return len(value), nil
	}
	original := []byte(`{"homeId":"home-1","enabled":true}`)
	encrypted, err := encryptSharing(original, secret, random)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := decryptSharing(encrypted, secret)
	if err != nil || !bytes.Equal(decrypted, original) {
		t.Fatalf("decrypted = %q, err=%v", decrypted, err)
	}
	if sharingSign("hash-key", "query", "body", http.Header{"X-appKey": []string{"app"}, "X-requestId": []string{"rid"}, "X-time": []string{"1"}}) == "" {
		t.Fatal("sharing signature is empty")
	}
}

func TestSharingClientRefreshesExpiredToken(t *testing.T) {
	var refreshRequest *http.Request
	httpClient := &http.Client{Transport: sharingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		refreshRequest = request
		return sharingAPIResponse(`{"success":true,"result":{"access_token":"new-access","refresh_token":"new-refresh","uid":"uid-1","expire_time":7200}}`), nil
	})}
	clock := time.Unix(1_700_000_000, 0)
	client, err := NewSharingClient("https://tuya.test", "client", "user-code", "terminal-1", Token{AccessToken: "old-access", RefreshToken: "old-refresh"}, clock.Add(-time.Hour), httpClient)
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return clock }
	client.random = func(value []byte) (int, error) { return len(value), nil }
	token, err := client.RefreshToken(context.Background(), "old-refresh")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "new-access" || token.RefreshToken != "new-refresh" || refreshRequest == nil || refreshRequest.URL.Path != "/v1.0/m/token/old-refresh" {
		t.Fatalf("refresh token = %#v, request = %#v", token, refreshRequest)
	}
}

func TestSharingClientRefreshKeepsRemainingExpiryWhenTokenIsStillFresh(t *testing.T) {
	called := false
	httpClient := &http.Client{Transport: sharingRoundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, io.ErrUnexpectedEOF
	})}
	clock := time.Unix(1_700_000_000, 0)
	client, err := NewSharingClient("https://tuya.test", "client", "user-code", "terminal-1", Token{AccessToken: "access-token", RefreshToken: "refresh-token"}, clock.Add(time.Hour), httpClient)
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return clock }
	token, err := client.RefreshToken(context.Background(), "refresh-token")
	if err != nil {
		t.Fatal(err)
	}
	if called || token.ExpiresIn < 3599 || token.RefreshToken != "refresh-token" {
		t.Fatalf("fresh token = %#v, request called = %v", token, called)
	}
}

type sharingRoundTripFunc func(*http.Request) (*http.Response, error)

func (f sharingRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func sharingAPIResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
