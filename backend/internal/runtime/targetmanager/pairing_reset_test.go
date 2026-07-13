package targetmanager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemovePairingIdentityRejectsUnsafePathsAndSymlinks(t *testing.T) {
	directory := t.TempDir()
	identity := filepath.Join(directory, "hap", "apple-main")
	if err := os.MkdirAll(identity, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identity, "pairings.data"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removePairingIdentity("apple-main", identity); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(identity); !os.IsNotExist(err) {
		t.Fatalf("identity directory still exists: %v", err)
	}
	if err := removePairingIdentity("apple-main", directory); err == nil {
		t.Fatal("unsafe parent path was accepted")
	}
	if err := os.Remove(filepath.Join(directory, "hap")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "real")
	if err := os.MkdirAll(filepath.Join(target, "apple-main"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "hap")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := removePairingIdentity("apple-main", filepath.Join(link, "apple-main")); err == nil {
		t.Fatal("symlinked identity parent was accepted")
	}
}
