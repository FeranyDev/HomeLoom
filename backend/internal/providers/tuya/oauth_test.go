package tuya

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type oauthRoundTrip func(*http.Request) (*http.Response, error)

func (f oauthRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestOAuthServiceBuildsAuthorizationURLAndExchangesCode(t *testing.T) {
	service := NewOAuthService()
	service.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	service.random = func(value []byte) (int, error) {
		for index := range value {
			value[index] = byte(index + 1)
		}
		return len(value), nil
	}
	var received *http.Request
	service.httpClient = func() *http.Client {
		return &http.Client{Transport: oauthRoundTrip(func(request *http.Request) (*http.Response, error) {
			received = request
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"success":true,"result":{"access_token":"access","refresh_token":"refresh","uid":"uid-1","expire_time":7200}}`)), Header: make(http.Header)}, nil
		})}
	}
	started, err := service.Start(OAuthStartRequest{AccessID: "project-id", AccessSecret: "project-secret", Region: "cn", AuthorizationURL: "https://auth.example/authorize?project=demo", RedirectURL: "http://homeassistant.local:8123/tuya/oauth/callback"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	parsed, err := url.Parse(started.AuthorizationURL)
	if err != nil {
		t.Fatalf("authorization URL parse error = %v", err)
	}
	query := parsed.Query()
	if query.Get("client_id") != "project-id" || query.Get("response_type") != "code" || query.Get("redirect_uri") == "" || query.Get("state") != started.State || query.Get("project") != "demo" {
		t.Fatalf("authorization query = %#v", query)
	}
	if returned, ok := service.AuthorizationURL(started.State); !ok || returned != started.AuthorizationURL {
		t.Fatalf("AuthorizationURL() = %q, %v", returned, ok)
	}
	completed, err := service.Complete(context.Background(), OAuthCompleteRequest{State: started.State, Code: "one-time-code"})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if completed.AccessToken != "access" || completed.RefreshToken != "refresh" || completed.UID != "uid-1" || completed.ExpiresAt != service.now().Add(7200*time.Second) {
		t.Fatalf("Complete() = %#v", completed)
	}
	if received == nil || received.URL.Path != "/v1.0/token" || received.URL.Query().Get("grant_type") != "2" || received.URL.Query().Get("code") != "one-time-code" || received.Header.Get("client_id") != "project-id" || received.Header.Get("sign") == "" {
		t.Fatalf("exchange request = %#v", received)
	}
	if _, ok := service.AuthorizationURL(started.State); ok {
		t.Fatal("OAuth state remained usable after successful exchange")
	}
}

func TestOAuthServiceRejectsMissingAuthorizationConfiguration(t *testing.T) {
	service := NewOAuthService()
	if _, err := service.Start(OAuthStartRequest{AccessID: "id", AccessSecret: "secret", RedirectURL: "http://localhost/callback"}); err == nil {
		t.Fatal("Start() accepted a missing authorization URL")
	}
	if _, err := service.Start(OAuthStartRequest{AccessID: "id", AccessSecret: "secret", AuthorizationURL: "https://auth.example/authorize", RedirectURL: "not-a-url"}); err == nil {
		t.Fatal("Start() accepted an invalid redirect URL")
	}
}

func TestOAuthServiceExpiresAndConsumesStateOnFailedExchange(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	service := NewOAuthService()
	service.now = func() time.Time { return clock }
	service.random = func(value []byte) (int, error) {
		for index := range value {
			value[index] = byte(index + 1)
		}
		return len(value), nil
	}
	service.httpClient = func() *http.Client {
		return &http.Client{Transport: oauthRoundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"success":true,"result":{"access_token":"access","uid":"uid-1","expire_time":7200}}`)),
				Header:     make(http.Header),
			}, nil
		})}
	}
	started, err := service.Start(OAuthStartRequest{
		AccessID:         "project-id",
		AccessSecret:     "project-secret",
		AuthorizationURL: "https://auth.example/authorize",
		RedirectURL:      "http://localhost/tuya/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(tuyaOAuthSessionTTL)
	if _, ok := service.AuthorizationURL(started.State); ok {
		t.Fatal("expired OAuth state remained available")
	}

	clock = time.Unix(1_700_000_000, 0)
	started, err = service.Start(OAuthStartRequest{
		AccessID:         "project-id",
		AccessSecret:     "project-secret",
		AuthorizationURL: "https://auth.example/authorize",
		RedirectURL:      "http://localhost/tuya/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Complete(context.Background(), OAuthCompleteRequest{State: started.State, Code: "one-time-code"}); err == nil {
		t.Fatal("Complete() accepted token without refresh token")
	}
	if _, ok := service.AuthorizationURL(started.State); ok {
		t.Fatal("OAuth state remained usable after failed exchange")
	}
}
