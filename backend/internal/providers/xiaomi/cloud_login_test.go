package xiaomi

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCloudLoginServiceContinuesIdentityVerificationWithLegacyCode2(t *testing.T) {
	security := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	verificationCalls := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/pass/serviceLogin":
			if cookie, err := request.Cookie("identity_verified"); err == nil && cookie.Value == "yes" {
				_, _ = response.Write([]byte(`&&&START&&&{"location":"` + server.URL + `/sts","userId":"42","ssecurity":"` + security + `"}`))
				return
			}
			_, _ = response.Write([]byte(`&&&START&&&{"_sign":"sign","qs":"qs","callback":"callback"}`))
		case "/pass/serviceLoginAuth2":
			// No Path attribute mirrors Xiaomi's default cookie scope. The
			// client must explicitly carry it to /identity/list.
			http.SetCookie(response, &http.Cookie{Name: "identity_session", Value: "identity-cookie"})
			response.WriteHeader(http.StatusUpgradeRequired)
			_, _ = response.Write([]byte(`&&&START&&&{"code":81003,"notificationUrl":"/fe/service/identity/authStart"}`))
		case "/identity/list":
			if cookie, err := request.Cookie("identity_session"); err != nil || cookie.Value != "identity-cookie" {
				t.Errorf("identity list cookie = %#v, %v", cookie, err)
			}
			// Some Xiaomi account shards return code 2 without method metadata;
			// the client must retain the documented SMS fallback (flag 4).
			_, _ = response.Write([]byte(`{"code":2}`))
		case "/identity/auth/verifyPhone":
			verificationCalls++
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			if request.Form.Get("_flag") != "4" || request.Form.Get("trust") != "false" {
				t.Errorf("verification form = %v", request.Form)
			}
			if request.Form.Get("ticket") != "123456" {
				_, _ = response.Write([]byte(`&&&START&&&{"code":70016,"description":"invalid ticket"}`))
				return
			}
			_, _ = response.Write([]byte(`&&&START&&&{"code":0,"location":"` + server.URL + `/identity/verified"}`))
		case "/identity/verified":
			http.SetCookie(response, &http.Cookie{Name: "identity_verified", Value: "yes", Path: "/"})
			_, _ = response.Write([]byte("ok"))
		case "/sts":
			http.SetCookie(response, &http.Cookie{Name: "serviceToken", Value: "verified-token", Path: "/"})
			http.SetCookie(response, &http.Cookie{Name: "passToken", Value: "verified-pass-token", Path: "/"})
			_, _ = response.Write([]byte("ok"))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	service := NewCloudLoginService()
	service.newClient = func(config CloudConfig) *httpMiotCloudClient {
		client := newHTTPMiotCloudClient(config)
		client.accountBase, client.apiBase = server.URL, server.URL+"/app"
		client.http.Transport = server.Client().Transport
		return client
	}
	started, err := service.Start(context.Background(), CloudLoginStartRequest{Region: "cn", Username: "owner@example.com", Password: "password", RequestTimeoutSec: 5})
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != "verification_required" || started.ChallengeID == "" || started.VerificationURL != server.URL+"/fe/service/identity/authStart" {
		t.Fatalf("started = %#v", started)
	}
	if _, err := service.Verify(context.Background(), CloudLoginVerifyRequest{ChallengeID: started.ChallengeID, Code: "000000"}); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("wrong-code error = %v", err)
	}
	verified, err := service.Verify(context.Background(), CloudLoginVerifyRequest{ChallengeID: started.ChallengeID, Code: "123456"})
	if err != nil {
		t.Fatal(err)
	}
	if verificationCalls != 2 || verified.Status != "verified" || verified.UserID != "42" ||
		verified.Ssecurity != security || verified.ServiceToken != "verified-token" ||
		verified.PassToken != "verified-pass-token" {
		t.Fatalf("calls=%d verified=%#v", verificationCalls, verified)
	}
	if _, err := service.Verify(context.Background(), CloudLoginVerifyRequest{ChallengeID: started.ChallengeID, Code: "123456"}); err == nil || !strings.Contains(err.Error(), "missing or expired") {
		t.Fatalf("reused challenge error = %v", err)
	}
}

func TestCloudLoginServiceRejectsRedactedPassword(t *testing.T) {
	service := NewCloudLoginService()
	_, err := service.Start(context.Background(), CloudLoginStartRequest{Region: "cn", Username: "owner@example.com", Password: "********"})
	if err == nil || !strings.Contains(err.Error(), "current Xiaomi account password") {
		t.Fatalf("error = %v", err)
	}
}

func TestCloudLoginServiceSupportsShortIdentityAuthStartAndPreloadsCookies(t *testing.T) {
	security := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/pass/serviceLogin":
			if cookie, err := request.Cookie("identity_verified"); err == nil && cookie.Value == "yes" {
				_, _ = response.Write([]byte(`&&&START&&&{"location":"` + server.URL + `/sts","userId":"42","ssecurity":"` + security + `"}`))
				return
			}
			_, _ = response.Write([]byte(`&&&START&&&{"_sign":"sign","qs":"qs","callback":"callback"}`))
		case "/pass/serviceLoginAuth2":
			response.WriteHeader(http.StatusUpgradeRequired)
			_, _ = response.Write([]byte(`&&&START&&&{"code":81003,"notificationUrl":"` + server.URL + `/identity/authStart?sid=passport&context=short-lived-context&_locale=en_US"}`))
		case "/identity/authStart":
			if request.URL.Query().Get("context") != "short-lived-context" {
				t.Errorf("authStart query = %v", request.URL.Query())
			}
			// The short authStart endpoint is responsible for establishing the
			// cookie used by identity/list and verifyPhone.
			http.SetCookie(response, &http.Cookie{Name: "identity_session", Value: "short-identity-cookie", Path: "/"})
			http.SetCookie(response, &http.Cookie{Name: "ick", Value: "short-ick", Path: "/"})
			_, _ = response.Write([]byte("ok"))
		case "/identity/list":
			if request.URL.Query().Get("context") != "short-lived-context" || request.URL.Query().Get("sid") != "passport" || request.URL.Query().Get("_locale") != "en_US" {
				t.Errorf("identity list query = %v", request.URL.Query())
			}
			if cookie, err := request.Cookie("identity_session"); err != nil || cookie.Value != "short-identity-cookie" {
				t.Errorf("identity list cookie = %#v, %v", cookie, err)
			}
			_, _ = response.Write([]byte(`{"code":0,"flag":4,"options":[4]}`))
		case "/identity/auth/verifyPhone":
			if cookie, err := request.Cookie("identity_session"); err != nil || cookie.Value != "short-identity-cookie" {
				t.Errorf("verify cookie = %#v, %v", cookie, err)
			}
			if cookie, err := request.Cookie("ick"); err != nil || cookie.Value != "short-ick" {
				t.Errorf("verify ick cookie = %#v, %v", cookie, err)
			}
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			if request.Form.Get("ticket") != "123456" || request.Form.Get("trust") != "false" {
				t.Errorf("verification form = %v", request.Form)
			}
			_, _ = response.Write([]byte(`&&&START&&&{"code":0,"location":"` + server.URL + `/identity/verified"}`))
		case "/identity/verified":
			http.SetCookie(response, &http.Cookie{Name: "identity_verified", Value: "yes", Path: "/"})
			_, _ = response.Write([]byte("ok"))
		case "/sts":
			http.SetCookie(response, &http.Cookie{Name: "serviceToken", Value: "short-service-token", Path: "/"})
			_, _ = response.Write([]byte("ok"))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	service := NewCloudLoginService()
	service.newClient = func(config CloudConfig) *httpMiotCloudClient {
		client := newHTTPMiotCloudClient(config)
		client.accountBase, client.apiBase = server.URL, server.URL+"/app"
		client.http.Transport = server.Client().Transport
		return client
	}
	started, err := service.Start(context.Background(), CloudLoginStartRequest{Region: "cn", Username: "owner@example.com", Password: "password", RequestTimeoutSec: 5})
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != "verification_required" || !strings.Contains(started.VerificationURL, "/identity/authStart?") {
		t.Fatalf("started = %#v", started)
	}
	verified, err := service.Verify(context.Background(), CloudLoginVerifyRequest{ChallengeID: started.ChallengeID, Code: "123456"})
	if err != nil {
		t.Fatal(err)
	}
	if verified.Status != "verified" || verified.UserID != "42" || verified.Ssecurity != security || verified.ServiceToken != "short-service-token" {
		t.Fatalf("verified = %#v", verified)
	}
}

func TestCloudLoginServiceExpiryClearsRetainedPassword(t *testing.T) {
	service := NewCloudLoginService()
	service.newClient = func(config CloudConfig) *httpMiotCloudClient {
		client := newHTTPMiotCloudClient(config)
		return client
	}
	// Inject a challenge directly so this test does not depend on an account
	// server; cleanupLocked is the security boundary under test.
	challenge := &cloudLoginChallenge{
		client:    newHTTPMiotCloudClient(CloudConfig{Username: "owner", Password: "secret"}),
		expiresAt: time.Now().Add(-time.Second),
	}
	service.challenges["expired"] = challenge
	service.now = time.Now
	_, err := service.Verify(context.Background(), CloudLoginVerifyRequest{ChallengeID: "expired", Code: "123456"})
	if err == nil || !strings.Contains(err.Error(), "missing or expired") {
		t.Fatalf("error = %v", err)
	}
	if challenge.client.config.Password != "" {
		t.Fatalf("expired challenge retained password %q", challenge.client.config.Password)
	}
}
