package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestClientSignMatchesTuyaTokenVector(t *testing.T) {
	client := &Client{
		accessID:     "1KAD46OrT9HafiKdsXeg",
		accessSecret: "4OHBOnWOqaEC1mWXOpVL3yV50s0qGSRC",
	}
	endpoint, err := url.Parse("https://openapi.tuyaus.com/v1.0/token?grant_type=2")
	if err != nil {
		t.Fatal(err)
	}
	got := client.sign(http.MethodGet, endpoint, nil, "", "1588925778000", "5138cc3a9033d69856923fd07b491173")
	const want = "2AF1596CB7D7410ECF93D1BF3E2AA6287158BA388CC9FB0179CB1306D62E1A35"
	if got != want {
		t.Fatalf("signature = %s, want %s", got, want)
	}
}

func TestClientTokenAndBusinessRequestsUseSignedHeaders(t *testing.T) {
	clock := time.UnixMilli(1_588_922_577_800)
	var requests []string
	server := newTuyaTestServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.RequestURI())
		if request.Header.Get("client_id") != "client" || request.Header.Get("sign_method") != "HMAC-SHA256" || request.Header.Get("t") != "1588922577800" {
			t.Errorf("signed headers = %#v", request.Header)
		}
		if request.Header.Get("nonce") != "fixed-nonce" || request.Header.Get("access_token") != "access-token" {
			// Token acquisition intentionally has no access token. The business
			// request below must carry both values.
			if request.URL.Path != "/v1.0/token" {
				t.Errorf("business auth headers = %#v", request.Header)
			}
		}
		switch request.URL.Path {
		case "/v1.0/token":
			_, _ = response.Write([]byte(`{"success":true,"result":{"access_token":"access-token","refresh_token":"refresh-token","expire_time":7200}}`))
		case "/v1.0/iot-03/devices/device-1/status":
			_, _ = response.Write([]byte(`{"success":true,"result":[{"code":"switch","value":true}]}`))
		default:
			response.WriteHeader(http.StatusNotFound)
			_, _ = response.Write([]byte(`{"success":false,"code":1000,"msg":"missing"}`))
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "client", "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return clock }
	client.nonce = func() string { return "fixed-nonce" }
	if _, err := client.GetToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err := client.GetStatus(context.Background(), "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || status[0].Code != "switch" || status[0].Value != true {
		t.Fatalf("status = %#v", status)
	}
	if len(requests) != 2 || requests[0] != "GET /v1.0/token?grant_type=1" || requests[1] != "GET /v1.0/iot-03/devices/device-1/status" {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestClientExchangeAuthorizationCodeUsesOAuthCodeGrant(t *testing.T) {
	var received *http.Request
	client, err := NewClient("https://tuya.test", "client", "secret", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		received = request
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"success":true,"result":{"access_token":"access","refresh_token":"refresh","uid":"uid-1","expire_time":7200}}`)),
		}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return time.UnixMilli(1_588_922_577_800) }
	client.nonce = func() string { return "fixed-nonce" }

	token, err := client.ExchangeAuthorizationCode(context.Background(), "code /?&")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access" || token.RefreshToken != "refresh" || token.UID != "uid-1" || token.ExpiresIn != 7200 {
		t.Fatalf("token = %#v", token)
	}
	if received == nil {
		t.Fatal("OAuth request was not sent")
	}
	if received.Method != http.MethodGet || received.URL.Path != "/v1.0/token" {
		t.Fatalf("request = %s %s", received.Method, received.URL.RequestURI())
	}
	query := received.URL.Query()
	if query.Get("grant_type") != "2" || query.Get("code") != "code /?&" {
		t.Fatalf("OAuth query = %#v", query)
	}
	if received.Header.Get("access_token") != "" {
		t.Fatalf("OAuth token request unexpectedly carried access token: %q", received.Header.Get("access_token"))
	}
	if client.accessToken != "access" {
		t.Fatalf("client access token = %q", client.accessToken)
	}
}

func TestClientOAuthTransportErrorsDoNotExposeCredentials(t *testing.T) {
	const code = "authorization-code-secret"
	const accessSecret = "project-secret"
	client, err := NewClient("https://tuya.test", "client-id", accessSecret, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial failed for %s?code=%s secret=%s", request.URL.Path, code, accessSecret)
	})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ExchangeAuthorizationCode(context.Background(), code)
	if err == nil {
		t.Fatal("expected transport error")
	}
	if strings.Contains(err.Error(), code) || strings.Contains(err.Error(), accessSecret) {
		t.Fatalf("transport error exposed sensitive value: %v", err)
	}
}

func TestClientRefreshErrorsRedactRefreshToken(t *testing.T) {
	const refreshToken = "refresh-token-secret"
	client, err := NewClient("https://tuya.test", "client-id", "project-secret", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"msg":"refresh-token-secret"}`)),
		}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RefreshToken(context.Background(), refreshToken); err == nil {
		t.Fatal("expected refresh transport response error")
	} else if strings.Contains(err.Error(), refreshToken) {
		t.Fatalf("refresh error exposed refresh token: %v", err)
	}
}

func TestClientListSpecificationAndCommands(t *testing.T) {
	var commandBody string
	server := newTuyaTestServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1.0/users/user-1/devices":
			if request.URL.Query().Get("page_no") != "2" || request.URL.Query().Get("page_size") != "10" {
				t.Errorf("device list query = %s", request.URL.RawQuery)
			}
			_, _ = response.Write([]byte(`{"success":true,"result":[{"id":"device-1","name":"Lamp","online":true}]}`))
		case "/v1.0/iot-03/devices/device-1/specification":
			_, _ = response.Write([]byte(`{"success":true,"result":{"category":"dj","functions":[{"code":"switch","type":"Boolean","values":"{}"}],"status":[]}}`))
		case "/v1.0/iot-03/devices/device-1/commands":
			var payload struct {
				Commands []Command `json:"commands"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			commandBody = fmt.Sprintf("%#v", payload.Commands)
			_, _ = response.Write([]byte(`{"success":true,"result":true}`))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "client", "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.SetAccessToken("access-token")
	devices, err := client.ListUserDevices(context.Background(), "user-1", 2, 10)
	if err != nil || len(devices) != 1 || devices[0].ID != "device-1" {
		t.Fatalf("devices = %#v, err=%v", devices, err)
	}
	specification, err := client.GetSpecification(context.Background(), "device-1")
	if err != nil || len(specification.Functions) != 1 || !specification.Functions[0].Writable {
		t.Fatalf("specification = %#v, err=%v", specification, err)
	}
	if err := client.SendCommands(context.Background(), "device-1", []Command{{Code: "switch", Value: true}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(commandBody, "switch") || !strings.Contains(commandBody, "true") {
		t.Fatalf("command body = %s", commandBody)
	}
}

type tuyaTestServer struct {
	URL     string
	client  *http.Client
	handler http.Handler
}

func newTuyaTestServer(handler http.Handler) *tuyaTestServer {
	server := &tuyaTestServer{URL: "http://tuya.test", handler: handler}
	server.client = &http.Client{Transport: roundTripFunc(server.roundTrip)}
	return server
}

func (s *tuyaTestServer) Client() *http.Client { return s.client }
func (s *tuyaTestServer) Close()               {}

func (s *tuyaTestServer) roundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)
	return &http.Response{
		StatusCode: recorder.Code,
		Status:     fmt.Sprintf("%d", recorder.Code),
		Header:     recorder.Header(),
		Body:       io.NopCloser(recorder.Result().Body),
		Request:    request,
	}, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
