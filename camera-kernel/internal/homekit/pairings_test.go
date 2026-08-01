package homekit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AlexxIT/go2rtc/internal/app"
)

func TestDurablePairingsRoundTripAndYAMLSeedMigration(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "go2rtc.yaml")
	if err := os.WriteFile(configPath, []byte("homekit:\n  camera-main:\n    pairings: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.ConfigPath = configPath
	t.Cleanup(func() { app.ConfigPath = "" })

	seed := []string{"client_id=controller&client_public=abcd&permissions=1"}
	got := loadDurablePairings("camera-main", seed)
	if len(got) != 1 || got[0] != seed[0] {
		t.Fatalf("migrated pairings = %#v", got)
	}
	path := pairingsStorePath("camera-main")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("durable pairings missing: %v", err)
	}

	// YAML seed is ignored once durable store exists with content.
	got = loadDurablePairings("camera-main", nil)
	if len(got) != 1 || got[0] != seed[0] {
		t.Fatalf("reloaded pairings = %#v", got)
	}

	updated := []string{"client_id=phone&client_public=ef01&permissions=1"}
	if err := persistDurablePairings("camera-main", updated); err != nil {
		t.Fatal(err)
	}
	got = loadDurablePairings("camera-main", nil)
	if len(got) != 1 || got[0] != updated[0] {
		t.Fatalf("persisted pairings = %#v", got)
	}
}
