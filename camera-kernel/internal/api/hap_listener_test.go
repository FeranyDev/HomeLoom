package api

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHAPListenerUsesIPv4OnlyNetwork(t *testing.T) {
	listener, err := net.Listen(hapListenNetwork, "0.0.0.0:0")
	if err != nil {
		t.Skipf("TCP listen not permitted in this environment: %v", err)
	}
	defer listener.Close()

	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || address.IP.To4() == nil {
		t.Fatalf("HAP listener address = %v, want IPv4", listener.Addr())
	}
}

func TestHAPOnlyHandlerRejectsMediaEndpoints(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := hapOnlyHandler(next)
	for _, path := range []string{"/api/stream.mp4", "/api/frame.mp4", "/api/homekit/session", "/api/streams"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d", path, response.Code)
		}
	}
	for _, path := range []string{"/pair-setup", "/pair-verify"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("%s status = %d", path, response.Code)
		}
	}
}
