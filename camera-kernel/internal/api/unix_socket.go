package api

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

// prepareUnixSocket removes only a socket that is proven stale. Blindly
// unlinking the path can detach a live camera-kernel listener when a second
// Core instance starts, leaving the authenticated preview proxy unable to
// reconnect to the original process.
func prepareUnixSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect unix socket: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refuse to replace non-socket path %q", path)
	}

	connection, dialErr := net.DialTimeout("unix", path, 250*time.Millisecond)
	if dialErr == nil {
		_ = connection.Close()
		return fmt.Errorf("unix socket %q is already in use", path)
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, syscall.ENOENT) {
		return fmt.Errorf("probe unix socket: %w", dialErr)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale unix socket: %w", err)
	}
	return nil
}
