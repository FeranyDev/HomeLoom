//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package network

import "syscall"

func enableBroadcast(_ string, _ string, connection syscall.RawConn) error {
	var result error
	if err := connection.Control(func(fd uintptr) {
		result = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return result
}
