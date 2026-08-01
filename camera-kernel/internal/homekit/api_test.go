package homekit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pkghomekit "github.com/AlexxIT/go2rtc/pkg/homekit"
)

func TestHomeKitSessionAPIReportsOnlyRedactedMediaState(t *testing.T) {
	previous := servers
	servers = map[string]*server{
		"camera-main": {stream: "camera-main"},
	}
	t.Cleanup(func() {
		servers = previous
	})

	request := httptest.NewRequest(http.MethodGet, "/api/homekit/session?id=camera-main", nil)
	response := httptest.NewRecorder()

	apiHomeKitSession(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var status pkghomekit.SessionStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if status.State != "idle" {
		t.Fatalf("state = %q, want idle", status.State)
	}

	body := strings.ToLower(response.Body.String())
	for _, secret := range []string{"masterkey", "mastersalt", "targetaddress", "sessionid"} {
		if strings.Contains(body, secret) {
			t.Fatalf("diagnostic response exposes %q: %s", secret, response.Body.String())
		}
	}
}

func TestHomeKitSessionAPIRejectsMutation(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/homekit/session", nil)
	response := httptest.NewRecorder()

	apiHomeKitSession(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func TestHomeKitSessionAPIRetainsLastEndedMediaCounters(t *testing.T) {
	previous := servers
	servers = map[string]*server{
		"camera-main": {
			stream: "camera-main",
			lastSession: pkghomekit.SessionStatus{
				State:        "streaming",
				VideoPackets: 42,
				VideoBytes:   8192,
			},
		},
	}
	t.Cleanup(func() {
		servers = previous
	})

	request := httptest.NewRequest(http.MethodGet, "/api/homekit/session?id=camera-main", nil)
	response := httptest.NewRecorder()
	apiHomeKitSession(response, request)

	var status pkghomekit.SessionStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Active || status.State != "streaming" || status.VideoPackets != 42 || status.VideoBytes != 8192 {
		t.Fatalf("last HomeKit session status = %#v", status)
	}
}
