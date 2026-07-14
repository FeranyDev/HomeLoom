package xiaomi

import (
	"bytes"
	"testing"
)

func TestMIPSRoundTrip(t *testing.T) {
	want := mipsMessage{ID: 42, From: "local", ReplyTopic: "123/reply", Payload: `{"did":"1"}`}
	encoded, err := encodeMIPS(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeMIPS(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("decoded = %#v, want %#v", got, want)
	}
	if _, err := decodeMIPS(bytes.TrimSuffix(encoded, []byte{0})); err == nil {
		t.Fatal("expected truncated payload to fail")
	}
}
