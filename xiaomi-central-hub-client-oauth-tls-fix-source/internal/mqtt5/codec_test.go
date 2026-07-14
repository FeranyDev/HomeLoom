package mqtt5

import (
	"bufio"
	"bytes"
	"testing"
)

func TestVariableByteInteger(t *testing.T) {
	for _, value := range []int{0, 127, 128, 16383, 16384, maxRemainingBytes} {
		encoded := encodeVarInt(value)
		decoded, err := readVarInt(bufio.NewReader(bytes.NewReader(encoded)))
		if err != nil {
			t.Fatalf("readVarInt(%d): %v", value, err)
		}
		if decoded != value {
			t.Fatalf("round trip: got %d want %d", decoded, value)
		}
	}
}
