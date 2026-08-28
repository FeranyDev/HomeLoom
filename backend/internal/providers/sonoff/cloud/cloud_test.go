package cloud

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestClientListsHomesAndDevicesAndSetsState(t *testing.T) {
	const accessToken = "access-token-secret"
	var commandBody deviceStatusCommand
	server := newTestServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer "+accessToken {
			t.Errorf("authorization header = %q", got)
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == pathHomes:
			_, _ = io.WriteString(response, `{"error":0,"data":{"familyList":[{"familyid":"home-1","name":"Main"}]}}`)
		case request.Method == http.MethodGet && request.URL.Path == pathDevices:
			if request.URL.Query().Get("num") != "0" {
				t.Errorf("device query = %s", request.URL.RawQuery)
			}
			_, _ = io.WriteString(response, `{"code":0,"result":{"thingList":[{"itemData":{"deviceid":"device-1","name":"Lamp","productModel":"SWV","familyid":"home-1","params":{"switch":"on","level":42},"online":true,"apikey":"device-key-secret"}}]}}`)
		case request.Method == http.MethodPost && request.URL.Path == pathThing:
			if err := json.NewDecoder(request.Body).Decode(&commandBody); err != nil {
				t.Errorf("decode command: %v", err)
			}
			_, _ = io.WriteString(response, `{"code":0,"data":{"deviceid":"device-1","params":{"switch":"off"}}}`)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.Client(), server.URL, accessToken, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	homes, err := client.ListHomes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(homes) != 1 || homes[0].ID != "home-1" || homes[0].HomeID != "home-1" || homes[0].FamilyID != "home-1" {
		t.Fatalf("homes = %#v", homes)
	}

	devices, err := client.ListDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("devices = %#v", devices)
	}
	device := devices[0]
	if device.ID != "device-1" || device.DeviceID != "device-1" || device.HomeID != "home-1" || device.Model != "SWV" || !device.Online {
		t.Fatalf("device = %#v", device)
	}
	if device.Params["switch"] != "on" || string(device.RawParams) != `{"switch":"on","level":42}` {
		t.Fatalf("device params = %#v, raw = %s", device.Params, device.RawParams)
	}
	if device.DeviceKey != "device-key-secret" {
		t.Fatalf("device key was not decoded")
	}

	if err := client.SetDeviceState(context.Background(), "device-1", map[string]any{"switch": "off"}); err != nil {
		t.Fatal(err)
	}
	if commandBody.ID != "device-1" || commandBody.Type != 1 || commandBody.Params["switch"] != "off" {
		t.Fatalf("command = %#v", commandBody)
	}
}

func TestLoginUsesSonoffLANSignatureAndReturnsRegionalEndpoint(t *testing.T) {
	server := newTestServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != pathUserLogin || request.Method != http.MethodPost {
			t.Fatalf("login request = %s %s", request.Method, request.URL.Path)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		signature := hmac.New(sha256.New, []byte(defaultAppSecret))
		_, _ = signature.Write(body)
		if got := request.Header.Get("Authorization"); got != "Sign "+base64.StdEncoding.EncodeToString(signature.Sum(nil)) {
			t.Fatalf("signature = %q", got)
		}
		if request.Header.Get("X-CK-Appid") != DefaultAppID {
			t.Fatalf("app id = %q", request.Header.Get("X-CK-Appid"))
		}
		_, _ = io.WriteString(response, `{"error":0,"data":{"at":"access-token","region":"cn"}}`)
	}))
	defer server.Close()
	result, err := Login(context.Background(), server.Client(), LoginCredentials{Username: "user@example.com", Password: "password", CountryCode: "+86", Endpoint: server.URL}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.AccessToken != "access-token" || result.Region != "cn" || result.Endpoint != server.URL {
		t.Fatalf("result = %#v", result)
	}
}

func TestLoginBuildsE164PhoneNumberFromCountryCode(t *testing.T) {
	var phoneNumbers []string
	server := newTestServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		phoneNumbers = append(phoneNumbers, payload["phoneNumber"])
		_, _ = io.WriteString(response, `{"error":0,"data":{"at":"access-token","region":"cn"}}`)
	}))
	defer server.Close()

	for _, username := range []string{"13800138000", "+8613800138000", "8613800138000"} {
		if _, err := Login(context.Background(), server.Client(), LoginCredentials{Username: username, Password: "password", CountryCode: "+86", Endpoint: server.URL}, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"+8613800138000", "+8613800138000", "+8613800138000"}; !slices.Equal(phoneNumbers, want) {
		t.Fatalf("phone numbers = %#v, want %#v", phoneNumbers, want)
	}
}

func TestClientReloginsStoredCredentialsAfterCloudAuthExpiry(t *testing.T) {
	loginCount := 0
	familyCount := 0
	server := newTestServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case pathUserLogin:
			loginCount++
			_, _ = io.WriteString(response, `{"error":0,"data":{"at":"access-token","region":"cn"}}`)
		case pathHomes:
			familyCount++
			if familyCount == 1 {
				response.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(response, `{"error":0,"data":{"familyList":[]}}`)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	credentials := LoginCredentials{Username: "user@example.com", Password: "password", CountryCode: "+86", Endpoint: server.URL}
	client, err := NewClientWithOptions(Options{Endpoint: server.URL, HTTPClient: server.Client(), Credentials: &credentials})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListHomes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loginCount != 2 || familyCount != 2 {
		t.Fatalf("login count = %d, family count = %d", loginCount, familyCount)
	}
}

func TestClientSetDeviceStateResultParsesResultAndPreservesRawParams(t *testing.T) {
	server := newTestServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != pathThing {
			t.Fatalf("path = %s", request.URL.Path)
		}
		_, _ = io.WriteString(response, `{"error":0,"result":{"deviceid":"device-1","params":{"switch":"on","value":1.25}}}`)
	}))
	defer server.Close()
	client, err := NewClient(server.Client(), server.URL, "token", 0)
	if err != nil {
		t.Fatal(err)
	}

	state, err := client.SetDeviceStateResult(context.Background(), Command{
		DeviceID: "device-1",
		Params:   map[string]any{"switch": "on"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.DeviceID != "device-1" || state.Params["switch"] != "on" || string(state.RawParams) != `{"switch":"on","value":1.25}` {
		t.Fatalf("state = %#v", state)
	}
}

func TestClientServerErrorsDoNotExposeSecrets(t *testing.T) {
	const secret = "devicekey-password-token-secret"
	server := newTestServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(response, `{"error":500,"msg":"`+secret+`"}`)
	}))
	defer server.Close()
	client, err := NewClient(server.Client(), server.URL, secret, 0)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.ListHomes(context.Background())
	if err == nil {
		t.Fatal("expected server error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("server error exposed secret: %v", err)
	}
}

func TestClientNonZeroResponseCodeDoesNotExposeMessage(t *testing.T) {
	const secret = "password-devicekey-access-token"
	server := newTestServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(response, `{"code":1001,"message":"`+secret+`"}`)
	}))
	defer server.Close()
	client, err := NewClient(server.Client(), server.URL, "token", 0)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.ListDevices(context.Background())
	if err == nil {
		t.Fatal("expected response code error")
	}
	if !strings.Contains(err.Error(), "1001") {
		t.Fatalf("response code missing from error: %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("response error exposed secret: %v", err)
	}
}

func TestNewClientInjectsTimeoutWithoutReplacingInjectedTransport(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"error":0,"data":{"familyList":[]}}`)),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})
	httpClient := &http.Client{Transport: transport}
	client, err := NewClient(httpClient, "https://sonoff.test", "token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if client.httpClient.Transport == nil || client.httpClient.Timeout != time.Second {
		t.Fatalf("client = %#v", client.httpClient)
	}
	if _, err := client.ListHomes(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type testServer struct {
	URL    string
	client *http.Client
}

func (s *testServer) Client() *http.Client { return s.client }
func (s *testServer) Close()               {}

func newTestServer(handler http.Handler) *testServer {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Result(), nil
	})
	return &testServer{URL: "http://sonoff.test", client: &http.Client{Transport: transport}}
}
