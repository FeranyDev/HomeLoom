package homekit

import "testing"

func TestFormatPin(t *testing.T) {
	if got := formatPin("00102003"); got != "001-02-003" {
		t.Fatalf("formatPin() = %q", got)
	}
	if got := formatPin("short"); got != "invalid" {
		t.Fatalf("formatPin(short) = %q", got)
	}
}
