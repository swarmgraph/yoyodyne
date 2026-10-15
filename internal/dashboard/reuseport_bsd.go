//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package dashboard

import "syscall"

const soReusePort = syscall.SO_REUSEPORT
