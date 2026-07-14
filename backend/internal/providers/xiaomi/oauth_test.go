package xiaomi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type oauthRoundTrip func(*http.Request) (*http.Response, error)

func (f oauthRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func oauthResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestStartOAuthGeneratesStableIdentities(t *testing.T) {
	result, err := StartOAuth(OAuthStartRequest{ClientID: "1234567890", Region: "cn", RedirectURL: "http://homeloom.local/xiaomi/oauth/callback"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.OAuthUUID) != 32 || result.VirtualDID == "" || len(result.State) != 40 {
		t.Fatalf("unexpected OAuth start result: %#v", result)
	}
	if !strings.Contains(result.AuthorizationURL, "account.xiaomi.com/oauth2/authorize") {
		t.Fatalf("unexpected authorization URL: %s", result.AuthorizationURL)
	}
	authorizationURL, err := url.Parse(result.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := authorizationURL.Query().Get("redirect_uri"); got != DefaultOAuthRedirectURL {
		t.Fatalf("unexpected redirect URI %q", got)
	}
	repeated, err := StartOAuth(OAuthStartRequest{ClientID: "1234567890", Region: "cn", RedirectURL: "http://homeloom.local/xiaomi/oauth/callback", OAuthUUID: result.OAuthUUID, VirtualDID: result.VirtualDID})
	if err != nil || repeated.State != result.State || repeated.VirtualDID != result.VirtualDID {
		t.Fatalf("OAuth identity was not stable: %#v err=%v", repeated, err)
	}
}

func TestCompleteOAuthProvisioningFlow(t *testing.T) {
	start := OAuthStartRequest{ClientID: "1234567890", Region: "cn", RedirectURL: "http://homeloom.local/xiaomi/oauth/callback", OAuthUUID: "0123456789abcdef0123456789abcdef", VirtualDID: "987654321"}
	started, err := StartOAuth(start)
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	httpClient := &http.Client{Transport: oauthRoundTrip(func(request *http.Request) (*http.Response, error) {
		requests++
		switch request.URL.Path {
		case xiaomiTokenPath:
			var payload struct {
				RedirectURL string `json:"redirect_uri"`
			}
			if err := json.Unmarshal([]byte(request.URL.Query().Get("data")), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.RedirectURL != DefaultOAuthRedirectURL {
				t.Fatalf("unexpected token redirect URI %q", payload.RedirectURL)
			}
			return oauthResponse(`{"code":0,"result":{"access_token":"access","refresh_token":"refresh","expires_in":3600}}`), nil
		case xiaomiHomeInfoPath:
			if request.Header.Get("Authorization") != "Beareraccess" {
				t.Fatalf("unexpected Authorization header %q", request.Header.Get("Authorization"))
			}
			return oauthResponse(`{"code":0,"result":{"homelist":[{"uid":12345}]}}`), nil
		case xiaomiCentralCertPath:
			return oauthResponse(`{"code":0,"result":{"cert":"-----BEGIN CERTIFICATE-----\nTEST\n-----END CERTIFICATE-----\n"}}`), nil
		default:
			t.Fatalf("unexpected request path %s", request.URL.Path)
			return nil, nil
		}
	})}
	result, err := completeOAuth(context.Background(), OAuthCompleteRequest{OAuthStartRequest: start, Code: "code", State: started.State}, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 3 || result.ClientID != start.VirtualDID || result.OAuth.UID != "12345" || result.OAuth.RefreshToken != "refresh" || result.OAuth.RedirectURL != DefaultOAuthRedirectURL || !strings.Contains(result.PrivateKey, "BEGIN PRIVATE KEY") {
		t.Fatalf("unexpected provision result: %#v requests=%d", result, requests)
	}
}

func TestCompleteOAuthRejectsStateMismatch(t *testing.T) {
	request := OAuthCompleteRequest{OAuthStartRequest: OAuthStartRequest{ClientID: "1234567890", Region: "cn", RedirectURL: "http://homeloom.local/callback", OAuthUUID: "0123456789abcdef0123456789abcdef", VirtualDID: "1"}, Code: "code", State: "wrong"}
	if _, err := completeOAuth(context.Background(), request, http.DefaultClient); err == nil {
		t.Fatal("expected state mismatch")
	}
}
