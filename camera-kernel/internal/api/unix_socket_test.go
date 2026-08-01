package api

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareUnixSocketRefusesToReplaceLiveListener(t *testing.T) {
	path := filepath.Join(shortSocketTempDir(t), "media.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	if err := prepareUnixSocket(path); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("prepareUnixSocket() = %v, want active-listener error", err)
	}
	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("live listener was detached: %v", err)
	}
	_ = connection.Close()
}

func TestPrepareUnixSocketRemovesStaleSocket(t *testing.T) {
	path := filepath.Join(shortSocketTempDir(t), "media.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if unixListener, ok := listener.(*net.UnixListener); ok {
		unixListener.SetUnlinkOnClose(false)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	if err := prepareUnixSocket(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("stale socket still exists: %v", err)
	}
}

func TestPrepareUnixSocketRefusesRegularFile(t *testing.T) {
	path := filepath.Join(shortSocketTempDir(t), "media.sock")
	if err := os.WriteFile(path, []byte("do not remove"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := prepareUnixSocket(path); err == nil || !strings.Contains(err.Error(), "non-socket") {
		t.Fatalf("prepareUnixSocket() = %v, want non-socket error", err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "do not remove" {
		t.Fatalf("regular file was changed: %q, %v", raw, err)
	}
}

func shortSocketTempDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "homeloom-api-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
