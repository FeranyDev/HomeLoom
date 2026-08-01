package api

import (
	"net/http"
)

// hapOnlyHandler keeps the HomeKit listener protocol-only. Media endpoints are
// served exclusively by the per-camera Unix socket consumed by authenticated
// HomeLoom Core routes.
func hapOnlyHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pair-setup", "/pair-verify":
			next.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}
