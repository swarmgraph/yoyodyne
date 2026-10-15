//go:build darwin || dragonfly || freebsd || netbsd || openbsd || (linux && !mips && !mipsle && !mips64 && !mips64le)

package dashboard

import (
	"context"
	"net"
	"syscall"
)

// listenShared binds with SO_REUSEPORT, which lets a second socket bind the
// same address only where the first set it too: the dashboard being replaced
// and the one replacing it share the configured port for the moment both are
// up, and a process that did not ask to share it still holds it alone. The
// option's number is the platform's (reuseport_*.go).
func listenShared(address string) (net.Listener, error) {
	config := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var set error
		if err := raw.Control(func(descriptor uintptr) {
			set = syscall.SetsockoptInt(int(descriptor), syscall.SOL_SOCKET, soReusePort, 1)
		}); err != nil {
			return err
		}
		return set
	}}
	return config.Listen(context.Background(), "tcp4", address)
}
