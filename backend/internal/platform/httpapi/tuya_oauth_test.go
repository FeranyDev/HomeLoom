package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/feranydev/homeloom/backend/internal/providers/tuya"
)

func TestTuyaOAuthRoutesStartGenerateQRCodeAndReturnCallback(t *testing.T) {
	server := newTestServer()
	startRequest := httptest.NewRequest(http.MethodPost, "/api/v1/tuya/oauth/start", bytes.NewBufferString(`{
		"accessId":"project-id",
		"accessSecret":"project-secret",
		"region":"cn",
		"authorizationUrl":"https://auth.example/authorize",
		"redirectUrl":"http://localhost/api/v1/tuya/oauth/callback"
	}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusOK {
		t.Fatalf("start response = %d %s", startResponse.Code, startResponse.Body.String())
	}
	var envelope struct {
		Data tuya.OAuthStartResult `json:"data"`
	}
	if err := json.Unmarshal(startResponse.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if envelope.Data.State == "" || !strings.Contains(envelope.Data.AuthorizationURL, "client_id=project-id") {
		t.Fatalf("start data = %#v", envelope.Data)
	}

	qrRequest := httptest.NewRequest(http.MethodGet, "/api/v1/tuya/oauth/qr?state="+envelope.Data.State, nil)
	qrResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(qrResponse, qrRequest)
	if qrResponse.Code != http.StatusOK || qrResponse.Header().Get("Content-Type") != "image/png" || !bytes.HasPrefix(qrResponse.Body.Bytes(), []byte("\x89PNG")) {
		t.Fatalf("qr response = %d content-type=%q bytes=%q", qrResponse.Code, qrResponse.Header().Get("Content-Type"), qrResponse.Body.Bytes()[:minInt(8, qrResponse.Body.Len())])
	}

	callbackRequest := httptest.NewRequest(http.MethodGet, "/api/v1/tuya/oauth/callback?code=one-time-code&state="+envelope.Data.State, nil)
	callbackResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(callbackResponse, callbackRequest)
	if callbackResponse.Code != http.StatusOK || !strings.Contains(callbackResponse.Body.String(), "homeloom-tuya-oauth") || !strings.Contains(callbackResponse.Body.String(), "one-time-code") {
		t.Fatalf("callback response = %d %s", callbackResponse.Code, callbackResponse.Body.String())
	}
}

func TestTuyaOAuthCallbackIsPublicButManagementRoutesRequireAuthentication(t *testing.T) {
	callback := httptest.NewRequest(http.MethodGet, "/api/v1/tuya/oauth/callback", nil)
	start := httptest.NewRequest(http.MethodPost, "/api/v1/tuya/oauth/start", nil)
	qr := httptest.NewRequest(http.MethodGet, "/api/v1/tuya/oauth/qr?state=state", nil)
	sharingStart := httptest.NewRequest(http.MethodPost, "/api/v1/tuya/login/start", nil)
	sharingPoll := httptest.NewRequest(http.MethodPost, "/api/v1/tuya/login/poll", nil)
	sharingQR := httptest.NewRequest(http.MethodGet, "/api/v1/tuya/login/qr?state=state", nil)
	if requiresAuthentication(callback) {
		t.Fatal("Tuya OAuth callback must be public for the Tuya redirect")
	}
	if !requiresAuthentication(start) || !requiresAuthentication(qr) || !requiresAuthentication(sharingStart) || !requiresAuthentication(sharingPoll) || !requiresAuthentication(sharingQR) {
		t.Fatal("Tuya OAuth management routes must require authentication")
	}
}

func TestTuyaSharingLoginRoutesValidateRequestAndExpiredQRCode(t *testing.T) {
	server := newTestServer()
	start := httptest.NewRequest(http.MethodPost, "/api/v1/tuya/login/start", strings.NewReader(`{"userCode":""}`))
	start.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusBadRequest || !strings.Contains(startResponse.Body.String(), "User Code") {
		t.Fatalf("invalid sharing start response = %d %s", startResponse.Code, startResponse.Body.String())
	}
	qr := httptest.NewRequest(http.MethodGet, "/api/v1/tuya/login/qr?state=missing", nil)
	qrResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(qrResponse, qr)
	if qrResponse.Code != http.StatusGone {
		t.Fatalf("expired sharing QR response = %d %s", qrResponse.Code, qrResponse.Body.String())
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
