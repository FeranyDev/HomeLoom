package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

const maximumRealtimeMessageSize int64 = 1 << 20

// RealtimeEvent is the small, state-only subset of an eWeLink push frame the
// Provider needs. No raw frame is retained because frames can contain account
// or device metadata that does not belong in diagnostics or device snapshots.
type RealtimeEvent struct {
	DeviceID string
	Params   map[string]any
	Online   *bool
}

// RealtimeSubscriber is deliberately transport-agnostic so a production
// WebSocket connection and deterministic tests share the same provider path.
// Subscribe blocks until the stream ends or ctx is cancelled.
type RealtimeSubscriber interface {
	Subscribe(context.Context, func(RealtimeEvent)) error
}

// RealtimeHeaderProvider supplies short-lived authentication headers without
// giving the subscriber direct access to the persistent account credential.
type RealtimeHeaderProvider interface {
	RealtimeHeaders(context.Context) (http.Header, error)
}

type realtimeConnection interface {
	ReadMessage() (int, []byte, error)
	Close() error
}

type realtimeDial func(context.Context, string, http.Header) (realtimeConnection, error)

// WebSocketSubscriber is an opt-in, bearer-authenticated JSON stream. eWeLink
// websocket endpoints differ by app/region, so callers must explicitly supply
// the endpoint rather than HomeLoom guessing one from the REST endpoint.
type WebSocketSubscriber struct {
	endpoint string
	headers  RealtimeHeaderProvider
	dial     realtimeDial
}

func NewWebSocketSubscriber(endpoint string, headers RealtimeHeaderProvider) (*WebSocketSubscriber, error) {
	endpoint = strings.TrimSpace(endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "wss" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Sonoff websocket endpoint must be an absolute wss URL without user information, query, or fragment")
	}
	if headers == nil {
		return nil, errors.New("Sonoff websocket authentication is required")
	}
	return &WebSocketSubscriber{endpoint: endpoint, headers: headers, dial: defaultRealtimeDial}, nil
}

func (s *WebSocketSubscriber) Subscribe(ctx context.Context, handler func(RealtimeEvent)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.headers == nil || s.dial == nil {
		return errors.New("Sonoff websocket subscriber is unavailable")
	}
	headers, err := s.headers.RealtimeHeaders(ctx)
	if err != nil {
		return errors.New("Sonoff websocket authentication is unavailable")
	}
	connection, err := s.dial(ctx, s.endpoint, headers)
	if err != nil {
		return errors.New("connect Sonoff websocket failed")
	}
	defer connection.Close()
	closed := make(chan struct{})
	defer close(closed)
	go func() {
		select {
		case <-ctx.Done():
			// Gorilla's ReadMessage has no context parameter. Closing the active
			// connection is what makes Provider.Close interrupt an idle stream.
			_ = connection.Close()
		case <-closed:
		}
	}()
	for {
		_, frame, readErr := connection.ReadMessage()
		if readErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("Sonoff websocket connection closed")
		}
		event, ok := DecodeRealtimeEvent(frame)
		if ok && handler != nil {
			handler(event)
		}
	}
}

func defaultRealtimeDial(ctx context.Context, endpoint string, headers http.Header) (realtimeConnection, error) {
	connection, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, headers)
	if err != nil {
		return nil, err
	}
	connection.SetReadLimit(maximumRealtimeMessageSize)
	return connection, nil
}

// DecodeRealtimeEvent accepts the state-update envelopes observed across the
// eWeLink REST/WebSocket families: state fields can be top-level, nested in
// data, or JSON-encoded in data. Unknown/partial frames are ignored instead
func DecodeRealtimeEvent(frame []byte) (RealtimeEvent, bool) {
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(string(frame)))
	if decoder.Decode(&decoded) != nil {
		return RealtimeEvent{}, false
	}
	return decodeRealtimeObject(decoded, 0)
}

func decodeRealtimeObject(value any, depth int) (RealtimeEvent, bool) {
	if depth > 3 {
		return RealtimeEvent{}, false
	}
	if text, ok := value.(string); ok {
		var nested any
		decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(text)))
		if decoder.Decode(&nested) != nil {
			return RealtimeEvent{}, false
		}
		return decodeRealtimeObject(nested, depth+1)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return RealtimeEvent{}, false
	}
	deviceID := firstEventString(object, "deviceid", "deviceId", "device_id", "id")
	params := eventParams(object["params"])
	online, onlineSet := eventBool(object["online"])
	if deviceID != "" && (params != nil || onlineSet) {
		event := RealtimeEvent{DeviceID: deviceID, Params: params}
		if onlineSet {
			event.Online = &online
		}
		return event, true
	}
	for _, key := range []string{"data", "payload", "itemData", "item_data"} {
		if nested, exists := object[key]; exists {
			if event, valid := decodeRealtimeObject(nested, depth+1); valid {
				return event, true
			}
		}
	}
	return RealtimeEvent{}, false
}

func eventParams(value any) map[string]any {
	params, ok := value.(map[string]any)
	if !ok || params == nil {
		return nil
	}
	copyOf := make(map[string]any, len(params))
	for key, item := range params {
		copyOf[key] = item
	}
	return copyOf
}

func firstEventString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func eventBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case json.Number:
		if typed.String() == "1" {
			return true, true
		}
		if typed.String() == "0" {
			return false, true
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "online":
			return true, true
		case "0", "false", "offline":
			return false, true
		}
	}
	return false, false
}
