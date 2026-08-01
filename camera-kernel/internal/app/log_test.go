package app

import "testing"

func TestHomeKitModuleDefaultsToWarn(t *testing.T) {
	if got := modules["homekit"]; got != "warn" {
		t.Fatalf("homekit module log level = %q, want warn", got)
	}
}
