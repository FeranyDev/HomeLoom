//go:build embed_webui

package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbeddedWebUIAndAPINotFoundBoundary(t *testing.T) {
	server := newTestServer()
	page := httptest.NewRecorder()
	server.Handler().ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusOK || !bytes.Contains(page.Body.Bytes(), []byte(`id="root"`)) || page.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("embedded page = %d %q", page.Code, page.Body.String())
	}

	missingAPI := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingAPI, httptest.NewRequest(http.MethodGet, "/api/v1/not-a-route", nil))
	if missingAPI.Code != http.StatusNotFound || !bytes.Contains(missingAPI.Body.Bytes(), []byte(`"code":"not_found"`)) {
		t.Fatalf("missing API = %d %q", missingAPI.Code, missingAPI.Body.String())
	}
}
