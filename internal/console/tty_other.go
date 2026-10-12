//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package console

import "errors"

// Where the terminal cannot be addressed, the conversation is the plain stream
// of text. That is the honest degradation: no cursor control is written, and
// nothing pretends the composing line has a region of its own.

func isTerminal(uintptr) bool { return false }

func enterCBreak(uintptr) (func() error, error) {
	return nil, errors.New("this platform has no terminal control")
}

func terminalWidth(uintptr) int { return 80 }

func terminalHeight(uintptr) int { return 24 }

// raiseSignal has nothing to raise: no keyboard is negotiated where no terminal
// can be addressed, so the driver is still handling these keys itself.
func raiseSignal(signalKey) {}

const noCharacter = 0

func interruptCharacter(uintptr) (uint8, error) {
	return 0, errors.New("this platform has no terminal control")
}

func setInterruptCharacter(uintptr, uint8) error {
	return errors.New("this platform has no terminal control")
}
