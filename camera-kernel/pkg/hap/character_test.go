package hap

import "testing"

func TestCharacterWriteUInt8FromHAPJSONNumber(t *testing.T) {
	character := &Character{Format: FormatUInt8, Value: uint8(1)}
	if err := character.Write(float64(0)); err != nil {
		t.Fatal(err)
	}
	if value, ok := character.Value.(uint8); !ok || value != 0 {
		t.Fatalf("uint8 characteristic value = %#v", character.Value)
	}
}
