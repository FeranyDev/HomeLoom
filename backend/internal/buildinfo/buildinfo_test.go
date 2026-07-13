package buildinfo

import "testing"

func TestCurrentIncludesRuntimeDefaults(t *testing.T) {
	info := Current()
	if info.Version == "" || info.Commit == "" || info.BuildTime == "" || info.GoVersion == "" {
		t.Fatalf("Current() = %#v", info)
	}
}
