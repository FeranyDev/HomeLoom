package cloud

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// RealtimeHeaders produces the current bearer/app headers for an explicitly
// configured realtime endpoint. It intentionally returns a fresh header map
// and never exposes the access token through diagnostics or error text.
func (c *Client) RealtimeHeaders(ctx context.Context) (http.Header, error) {
	if c == nil {
		return nil, errors.New("Sonoff cloud client is unavailable")
	}
	if err := c.ensureAuthenticated(ctx); err != nil {
		return nil, errors.New("Sonoff cloud authentication is unavailable")
	}
	c.mu.Lock()
	authenticator := c.authenticator
	appID := c.appID
	c.mu.Unlock()
	if authenticator == nil {
		return nil, errors.New("Sonoff cloud authentication is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://realtime.invalid/", nil)
	if err != nil {
		return nil, errors.New("prepare Sonoff websocket authentication failed")
	}
	if strings.TrimSpace(appID) != "" {
		request.Header.Set("X-CK-Appid", strings.TrimSpace(appID))
	}
	if err := authenticator.Authenticate(request); err != nil {
		return nil, errors.New("Sonoff cloud authentication is unavailable")
	}
	return request.Header.Clone(), nil
}
