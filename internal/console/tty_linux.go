//go:build linux

package console

import "syscall"

const (
	readTermios  = syscall.TCGETS
	writeTermios = syscall.TCSETS
)

// noCharacter is the value that turns a terminal's control character off.
const noCharacter = 0
