//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package dashboard

import (
	"context"
	"net"
	"syscall"
)

// listenShared binds with SO_REUSEPORT, which lets a second socket bind the
// same address only where the first set it too: the dashboard being replaced
// and the one replacing it share the configured port for the moment both are
// up, and a process that did not ask to share it still holds it alone.
func listenShared(address string) (net.Listener, error) {
	config := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var set error
		if err := raw.Control(func(descriptor uintptr) {
			set = syscall.SetsockoptInt(int(descriptor), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
		}); err != nil {
			return err
		}
		return set
	}}
	return config.Listen(context.Background(), "tcp4", address)
}
