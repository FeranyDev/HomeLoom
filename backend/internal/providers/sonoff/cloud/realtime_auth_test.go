package cloud

import (
	"context"
	"testing"
)

func TestClientRealtimeHeadersUseCurrentAuthenticatorWithoutLeakingIt(t *testing.T) {
	client, err := NewClient(nil, "https://cloud.example", "realtime-token", 0)
	if err != nil {
		t.Fatal(err)
	}
	headers, err := client.RealtimeHeaders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := headers.Get("Authorization"); got != "Bearer realtime-token" {
		t.Fatalf("authorization = %q", got)
	}
}
