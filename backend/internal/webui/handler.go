package webui

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

type Handler struct {
	assets fs.FS
}

func NewHandler(assets fs.FS) *Handler {
	return &Handler{assets: assets}
}

// IsApplicationPath keeps the SPA fallback away from management and health
// endpoints. Extensionless paths are safe to route to index.html; missing
// files with extensions remain real 404 responses.
func IsApplicationPath(requestPath string) bool {
	cleaned := "/" + strings.TrimPrefix(path.Clean("/"+requestPath), "/")
	for _, reserved := range []string{"/api", "/health", "/ready", "/metrics"} {
		if cleaned == reserved || strings.HasPrefix(cleaned, reserved+"/") {
			return false
		}
	}
	if cleaned == "/" || strings.HasPrefix(cleaned, "/assets/") {
		return true
	}
	return !strings.Contains(path.Base(cleaned), ".")
}

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if h == nil || h.assets == nil || !IsApplicationPath(request.URL.Path) {
		http.NotFound(response, request)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	} else if _, err := fs.Stat(h.assets, name); err != nil {
		if strings.HasPrefix(name, "assets/") {
			http.NotFound(response, request)
			return
		}
		name = "index.html"
	}

	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Referrer-Policy", "same-origin")
	response.Header().Set("X-Frame-Options", "DENY")
	if strings.HasPrefix(name, "assets/") {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		response.Header().Set("Cache-Control", "no-cache")
	}
	payload, err := fs.ReadFile(h.assets, name)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	http.ServeContent(response, request, name, time.Time{}, bytes.NewReader(payload))
}
