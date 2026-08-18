package cloud

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

type fakeRealtimeHeaders struct{}

func (fakeRealtimeHeaders) RealtimeHeaders(context.Context) (http.Header, error) {
	return http.Header{"Authorization": {"Bearer token"}}, nil
}

type blockingRealtimeConnection struct{ closed chan struct{} }

func (c *blockingRealtimeConnection) ReadMessage() (int, []byte, error) {
	<-c.closed
	return 0, nil, errors.New("closed")
}
func (c *blockingRealtimeConnection) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

func TestDecodeRealtimeEventAcceptsNestedAndEncodedStateFrames(t *testing.T) {
	for _, test := range []struct {
		name  string
		frame string
		want  RealtimeEvent
	}{
		{name: "top level", frame: `{"deviceid":"1000a","params":{"switch":"on"},"online":true}`, want: RealtimeEvent{DeviceID: "1000a", Params: map[string]any{"switch": "on"}, Online: boolPointer(true)}},
		{name: "nested", frame: `{"action":"update","data":{"deviceId":"1000b","params":{"brightness":42},"online":"0"}}`, want: RealtimeEvent{DeviceID: "1000b", Params: map[string]any{"brightness": float64(42)}, Online: boolPointer(false)}},
		{name: "encoded data", frame: `{"data":"{\"device_id\":\"1000c\",\"params\":{\"switch\":\"off\"}}"}`, want: RealtimeEvent{DeviceID: "1000c", Params: map[string]any{"switch": "off"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := DecodeRealtimeEvent([]byte(test.frame))
			if !ok || got.DeviceID != test.want.DeviceID || !sameRealtimeParams(got.Params, test.want.Params) || !sameRealtimeOnline(got.Online, test.want.Online) {
				t.Fatalf("event=%#v ok=%v, want %#v", got, ok, test.want)
			}
		})
	}
}

func TestDecodeRealtimeEventIgnoresInvalidAndNonStateFrames(t *testing.T) {
	for _, frame := range []string{`not json`, `{}`, `{"data":{"deviceid":"1000a"}}`, `{"deviceid":"1000a","online":"unknown"}`} {
		if event, ok := DecodeRealtimeEvent([]byte(frame)); ok {
			t.Fatalf("frame %s produced event %#v", frame, event)
		}
	}
}

func TestWebSocketSubscriberCancelsAnIdleRead(t *testing.T) {
	subscriber, err := NewWebSocketSubscriber("wss://events.example/stream", fakeRealtimeHeaders{})
	if err != nil {
		t.Fatal(err)
	}
	connection := &blockingRealtimeConnection{closed: make(chan struct{})}
	subscriber.dial = func(context.Context, string, http.Header) (realtimeConnection, error) { return connection, nil }
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- subscriber.Subscribe(ctx, nil) }()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("subscribe error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("idle read did not stop after cancellation")
	}
}

func boolPointer(value bool) *bool { return &value }

func sameRealtimeOnline(left, right *bool) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameRealtimeParams(left, right map[string]any) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if value != right[key] {
			return false
		}
	}
	return true
}
