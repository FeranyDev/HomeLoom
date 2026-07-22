package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testAssets() fs.FS {
	return fstest.MapFS{
		"index.html":             {Data: []byte("<html>HomeLoom</html>")},
		"assets/index-abc123.js": {Data: []byte("console.log('HomeLoom')")},
	}
}

func TestHandlerServesIndexAssetsAndSPAFallback(t *testing.T) {
	handler := NewHandler(testAssets())
	tests := []struct {
		path, contains, cache string
	}{
		{path: "/", contains: "HomeLoom", cache: "no-cache"},
		{path: "/devices", contains: "HomeLoom", cache: "no-cache"},
		{path: "/assets/index-abc123.js", contains: "console.log", cache: "public, max-age=31536000, immutable"},
	}
	for _, current := range tests {
		request := httptest.NewRequest(http.MethodGet, current.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), current.contains) || response.Header().Get("Cache-Control") != current.cache {
			t.Fatalf("GET %s = %d %q, cache %q", current.path, response.Code, response.Body.String(), response.Header().Get("Cache-Control"))
		}
		if response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("GET %s omitted security headers", current.path)
		}
	}
}

func TestHandlerDoesNotHideAPIOrMissingStaticFiles(t *testing.T) {
	handler := NewHandler(testAssets())
	for _, requestPath := range []string{"/api/v1/missing", "/health/missing", "/assets/missing.js", "/favicon.ico"} {
		request := httptest.NewRequest(http.MethodGet, requestPath, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d", requestPath, response.Code)
		}
	}
}

func TestApplicationPathBoundary(t *testing.T) {
	for value, expected := range map[string]bool{
		"/": true, "/targets": true, "/assets/app.js": true,
		"/api": false, "/api/v1/devices": false, "/health": false,
		"/ready": false, "/metrics": false, "/robots.txt": false,
	} {
		if actual := IsApplicationPath(value); actual != expected {
			t.Fatalf("IsApplicationPath(%q) = %v, want %v", value, actual, expected)
		}
	}
}
