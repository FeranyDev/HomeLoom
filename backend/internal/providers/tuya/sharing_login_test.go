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

func TestSharingLoginStartAndPollMatchesHomeAssistantFlow(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	pollCount := 0
	service := NewSharingLoginService()
	service.now = func() time.Time { return clock }
	service.random = func(value []byte) (int, error) {
		for index := range value {
			value[index] = byte(index + 1)
		}
		return len(value), nil
	}
	service.httpClient = func() *http.Client {
		return &http.Client{Transport: tuyaRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			query := request.URL.Query()
			if query.Get("clientid") != "HA_3y9q4ak7g4ephrvke" || query.Get("usercode") != "user-code-1" || (request.Method == http.MethodPost && query.Get("schema") != "haauthorize") {
				t.Fatalf("QR login query = %#v", query)
			}
			if request.Method == http.MethodPost {
				if request.URL.Path != "/v1.0/m/life/home-assistant/qrcode/tokens" {
					t.Fatalf("start path = %q", request.URL.Path)
				}
				return sharingLoginResponse(`{"success":true,"result":{"qrcode":"qr-token"}}`), nil
			}
			pollCount++
			if request.URL.Path != "/v1.0/m/life/home-assistant/qrcode/tokens/qr-token" {
				t.Fatalf("poll path = %q", request.URL.Path)
			}
			if pollCount == 1 {
				return sharingLoginResponse(`{"success":false,"code":1001,"msg":"waiting"}`), nil
			}
			return sharingLoginResponse(`{"success":true,"t":1700000000000,"result":{"uid":"uid-1","access_token":"access","refresh_token":"refresh","expire_time":7200,"endpoint":"https://openapi.tuyaus.com","terminal_id":"terminal-1"}}`), nil
		})}
	}

	started, err := service.Start(context.Background(), SharingLoginStartRequest{UserCode: " user-code-1 "})
	if err != nil {
		t.Fatal(err)
	}
	if started.State == "" || started.QRData != "tuyaSmart--qrLogin?token=qr-token" || !started.ExpiresAt.Equal(clock.Add(sharingLoginSessionTTL)) {
		t.Fatalf("start result = %#v", started)
	}
	if qrData, ok := service.QRData(started.State); !ok || qrData != started.QRData {
		t.Fatalf("QR data = %q, %v", qrData, ok)
	}

	pending, err := service.Poll(context.Background(), SharingLoginPollRequest{State: started.State})
	if err != nil || pending.Status != "pending" || pending.Message != "waiting" {
		t.Fatalf("pending result = %#v, err=%v", pending, err)
	}
	complete, err := service.Poll(context.Background(), SharingLoginPollRequest{State: started.State})
	if err != nil {
		t.Fatal(err)
	}
	if complete.Status != "complete" || complete.AccessToken != "access" || complete.RefreshToken != "refresh" || complete.UID != "uid-1" || complete.Endpoint != "https://openapi.tuyaus.com" || complete.TerminalID != "terminal-1" {
		t.Fatalf("complete result = %#v", complete)
	}
	if _, ok := service.QRData(started.State); ok {
		t.Fatal("completed QR login state was not consumed")
	}
}

func TestSharingLoginRejectsMissingAndExpiredSessions(t *testing.T) {
	service := NewSharingLoginService()
	if _, err := service.Start(context.Background(), SharingLoginStartRequest{}); err == nil {
		t.Fatal("expected missing user code error")
	}
	if result, err := service.Poll(context.Background(), SharingLoginPollRequest{}); err == nil || result.Status != "" {
		t.Fatalf("missing state result = %#v, err=%v", result, err)
	}
	if result, err := service.Poll(context.Background(), SharingLoginPollRequest{State: "unknown"}); err != nil || result.Status != "expired" {
		t.Fatalf("unknown state result = %#v, err=%v", result, err)
	}

	clock := time.Unix(1_700_000_000, 0)
	service.now = func() time.Time { return clock }
	service.random = func(value []byte) (int, error) { return len(value), nil }
	service.httpClient = func() *http.Client {
		return &http.Client{Transport: tuyaRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return sharingLoginResponse(`{"success":true,"result":{"qrcode":"qr-token"}}`), nil
		})}
	}
	started, err := service.Start(context.Background(), SharingLoginStartRequest{UserCode: "user-code"})
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(sharingLoginSessionTTL)
	result, err := service.Poll(context.Background(), SharingLoginPollRequest{State: started.State})
	if err != nil || result.Status != "expired" {
		t.Fatalf("expired state result = %#v, err=%v", result, err)
	}
}

func sharingLoginResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestSharingLoginQueryEscapesUserCode(t *testing.T) {
	service := NewSharingLoginService()
	service.random = func(value []byte) (int, error) { return len(value), nil }
	service.httpClient = func() *http.Client {
		return &http.Client{Transport: tuyaRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			if _, err := url.ParseQuery(request.URL.RawQuery); err != nil {
				t.Fatalf("invalid query: %v", err)
			}
			return sharingLoginResponse(`{"success":true,"result":{"qrcode":"qr token/?"}}`), nil
		})}
	}
	result, err := service.Start(context.Background(), SharingLoginStartRequest{UserCode: "code /?&"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.QRData, "qr token/?") {
		t.Fatalf("QR data = %q", result.QRData)
	}
}

type tuyaRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tuyaRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
