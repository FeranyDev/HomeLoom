package lan

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientCommandDIYAndGetState(t *testing.T) {
	var paths []string
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if request["deviceid"] != "1000abcd" || request["selfApikey"] != "123" || request["encrypt"] != false {
			t.Errorf("request envelope = %#v", request)
		}
		if _, ok := request["sequence"].(string); !ok || request["sequence"] == "" {
			t.Errorf("request sequence = %#v", request["sequence"])
		}
		if _, ok := request["data"].(map[string]any); !ok {
			t.Errorf("DIY request data = %#v", request["data"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":0,"data":{"ok":true}}`))
	}))
	defer server.Close()

	client := NewClient(server.Client(), time.Second)
	req := Request{DeviceID: "1000abcd", Host: server.URL, DIY: true}
	response, err := client.Command(context.Background(), req, "switch", map[string]any{"switch": "on"})
	if err != nil {
		t.Fatal(err)
	}
	if response["error"] != float64(0) || response["data"].(map[string]any)["ok"] != true {
		t.Fatalf("response = %#v", response)
	}
	if _, err := client.GetState(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths, ",") != "/zeroconf/switch,/zeroconf/getState" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestClientSupportsZeroconfCommands(t *testing.T) {
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":0}`))
	}))
	defer server.Close()
	client := NewClient(server.Client(), time.Second)
	req := Request{DeviceID: "1000abcd", Host: server.URL, DIY: true}
	if _, err := client.Switch(context.Background(), req, "on"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Switches(context.Background(), req, []SwitchCommand{{Outlet: 0, Switch: "off"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Light(context.Background(), req, map[string]any{"switch": "on"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fan(context.Background(), req, map[string]any{"fan": "on"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Cover(context.Background(), req, map[string]any{"location": 50}); err != nil {
		t.Fatal(err)
	}
}

func TestClientEncryptedRequestAndResponse(t *testing.T) {
	const deviceKey = "device-key"
	iv := []byte("0123456789abcdef")
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if request["encrypt"] != true || request["deviceid"] != "1000abcd" {
			t.Errorf("encrypted envelope = %#v", request)
		}
		encoded := request["data"].(string)
		requestIV, err := ParseIV(request["iv"].(string))
		if err != nil {
			t.Errorf("request IV: %v", err)
			return
		}
		clear, err := Decode(deviceKey, encoded, requestIV)
		if err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		var command map[string]any
		if err := json.Unmarshal(clear, &command); err != nil || command["switch"] != "on" {
			t.Errorf("clear request = %s, %v", clear, err)
		}
		encodedResponse, err := Encode(deviceKey, []byte(`{"state":"on"}`), iv)
		if err != nil {
			t.Errorf("encode response: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":0,"encrypt":true,"iv":"` + "MDEyMzQ1Njc4OWFiY2RlZg==" + `","data":"` + encodedResponse + `"}`))
	}))
	defer server.Close()
	client := NewClient(server.Client(), time.Second)
	req := Request{DeviceID: "1000abcd", DeviceKey: deviceKey, Host: server.URL, IV: base64IV(iv)}
	response, err := client.Command(context.Background(), req, "switch", map[string]any{"switch": "on"})
	if err != nil {
		t.Fatal(err)
	}
	if response["data"].(map[string]any)["state"] != "on" {
		t.Fatalf("decoded response = %#v", response)
	}
}

func TestClientTimeoutAndResponseErrors(t *testing.T) {
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream down"))
	}))
	client := NewClient(server.Client(), time.Second)
	_, err := client.Command(context.Background(), Request{DeviceID: "id", Host: server.URL, DIY: true}, "switch", map[string]any{"switch": "on"})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("HTTP error = %v", err)
	}

	appServer := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":422}`))
	}))
	defer appServer.Close()
	client = NewClient(appServer.Client(), time.Second)
	_, err = client.Command(context.Background(), Request{DeviceID: "id", Host: appServer.URL, DIY: true}, "switch", map[string]any{"switch": "on"})
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.Code != 422 {
		t.Fatalf("response error = %v", err)
	}

	timeoutServer := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer timeoutServer.Close()
	timeoutClient := NewClient(timeoutServer.Client(), 20*time.Millisecond)
	_, err = timeoutClient.Command(context.Background(), Request{DeviceID: "id", Host: timeoutServer.URL, DIY: true}, "switch", map[string]any{"switch": "on"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
}

func base64IV(iv []byte) string {
	encoded, _ := EncodeIV(iv)
	return encoded
}

type testServer struct {
	URL    string
	client *http.Client
}

func (s *testServer) Client() *http.Client { return s.client }
func (s *testServer) Close()               {}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func newTestServer(handler http.Handler) *testServer {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		return recorder.Result(), nil
	})
	return &testServer{URL: "http://sonoff.test", client: &http.Client{Transport: transport}}
}
