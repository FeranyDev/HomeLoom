package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTokenRequiresPrivateSufficientlyLongFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.token")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 24)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := readToken(path)
	if err != nil || token != strings.Repeat("x", 24) {
		t.Fatalf("read token = %q, %v", token, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(path); err == nil {
		t.Fatal("public token file was accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(path); err == nil {
		t.Fatal("short token was accepted")
	}
}
