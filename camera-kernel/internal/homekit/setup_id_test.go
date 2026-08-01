package homekit

import "testing"

func TestCalcSetupIDMatchesCore(t *testing.T) {
	if got := calcSetupID("camera-main"); got != "9DD5" {
		t.Fatalf("calcSetupID(camera-main) = %q, want 9DD5", got)
	}
}
