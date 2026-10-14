//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package dashboard

import "net"

// listenShared binds alone where the platform offers no shared port, so a
// dashboard replacing another there cannot start until the old one has stopped.
func listenShared(address string) (net.Listener, error) {
	return net.Listen("tcp4", address)
}
