package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
