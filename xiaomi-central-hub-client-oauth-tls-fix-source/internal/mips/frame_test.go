package mips

import (
	"encoding/binary"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	want := Message{
		ID:         123456,
		From:       "local",
		ReplyTopic: "ha.test/reply",
		Payload:    `{"did":"123","siid":2,"piid":1}`,
	}
	encoded, err := Encode(want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	got, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %#v want %#v", got, want)
	}
}

func TestDecodeRejectsTruncatedField(t *testing.T) {
	data := make([]byte, 7)
	binary.LittleEndian.PutUint32(data[:4], 100)
	data[4] = byte(FieldPayload)
	if _, err := Decode(data); err == nil {
		t.Fatal("Decode() accepted truncated field")
	}
}
