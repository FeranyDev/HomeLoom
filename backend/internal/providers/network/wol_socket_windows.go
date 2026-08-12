//go:build windows

package network

import "syscall"

// Windows enables UDP broadcast for regular UDP sockets without an additional
// syscall in the Go runtime. Keeping this separate preserves cross-compilation
// while Unix platforms explicitly request SO_BROADCAST.
func enableBroadcast(_ string, _ string, _ syscall.RawConn) error { return nil }
