//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package console

import (
	"fmt"
	"syscall"
	"unsafe"
)

// defaultColumns is the width assumed when the terminal will not say. It is the
// width terminals have been by default for long enough that guessing it is
// safer than guessing anything else.
const defaultColumns = 80

// minimumColumns keeps the arithmetic that positions the cursor away from
// widths a region cannot be drawn in at all.
const minimumColumns = 20

// defaultRows is the height assumed when the terminal will not say, for the
// same reason defaultColumns is the width it will not say.
const defaultRows = 24

// minimumRows keeps the region bounded to a window it can still be drawn in: a
// row for what the operator is typing, and one for what they are waiting on.
const minimumRows = 2

// winsize is the terminal's own account of its size. The syscall package
// declares the request but not the structure it fills in, so it is declared
// here, in the layout every one of these systems uses for it.
type winsize struct {
	rows    uint16
	columns uint16
	xpixels uint16
	ypixels uint16
}

func ioctl(fd, request uintptr, argument unsafe.Pointer) error {
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(argument)); err != 0 {
		return err
	}
	return nil
}

// isTerminal reports whether the descriptor is a terminal, by asking it for the
// settings only a terminal has.
func isTerminal(fd uintptr) bool {
	var settings syscall.Termios
	return ioctl(fd, readTermios, unsafe.Pointer(&settings)) == nil
}

// enterCBreak puts the terminal into cbreak mode: keystrokes arrive as they are
// typed and nothing is echoed, so the composing line is drawn rather than typed
// into and output can be put above it.
//
// It is deliberately not full raw mode. The signal characters are left alone,
// so Ctrl-Z and Ctrl-\ still do what the terminal does with them rather than
// being reimplemented here, and Ctrl-C is taken over only while the operator is
// being read (setInterruptCharacter); output processing is left alone
// too, so a newline is still a newline and the harness can go on writing lines.
func enterCBreak(fd uintptr) (func() error, error) {
	var original syscall.Termios
	if err := ioctl(fd, readTermios, unsafe.Pointer(&original)); err != nil {
		return nil, fmt.Errorf("read terminal settings: %w", err)
	}
	settings := original
	settings.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN
	// One byte is enough to wake a read, and nothing waits for a second, so a
	// keystroke reaches the line as it is typed.
	settings.Cc[syscall.VMIN] = 1
	settings.Cc[syscall.VTIME] = 0
	if err := ioctl(fd, writeTermios, unsafe.Pointer(&settings)); err != nil {
		return nil, fmt.Errorf("set terminal settings: %w", err)
	}
	restored := false
	return func() error {
		if restored {
			return nil
		}
		restored = true
		return ioctl(fd, writeTermios, unsafe.Pointer(&original))
	}, nil
}

// interruptCharacter is the character the terminal turns into an interrupt,
// Ctrl-C unless the operator chose another, read so it can be put back.
func interruptCharacter(fd uintptr) (uint8, error) {
	var settings syscall.Termios
	if err := ioctl(fd, readTermios, unsafe.Pointer(&settings)); err != nil {
		return 0, fmt.Errorf("read terminal settings: %w", err)
	}
	return settings.Cc[syscall.VINTR], nil
}

// setInterruptCharacter sets the character the terminal turns into an
// interrupt. The console sets it to noCharacter while it is reading the
// operator, so Ctrl-C arrives as a byte it gives a meaning of its own — clearing
// the prompt, or stopping a turn — and puts it back the rest of the time, so
// Ctrl-C still interrupts whatever the conversation is doing when nothing is
// reading the keyboard. Only the interrupt is changed: Ctrl-Z and Ctrl-\ are
// the terminal's throughout.
func setInterruptCharacter(fd uintptr, character uint8) error {
	var settings syscall.Termios
	if err := ioctl(fd, readTermios, unsafe.Pointer(&settings)); err != nil {
		return fmt.Errorf("read terminal settings: %w", err)
	}
	settings.Cc[syscall.VINTR] = character
	if err := ioctl(fd, writeTermios, unsafe.Pointer(&settings)); err != nil {
		return fmt.Errorf("set terminal settings: %w", err)
	}
	return nil
}

// terminalWidth asks the terminal how wide it is, every time it is drawn, so a
// window that was resized is drawn at the size it is now.
func terminalWidth(fd uintptr) int {
	var size winsize
	if err := ioctl(fd, syscall.TIOCGWINSZ, unsafe.Pointer(&size)); err != nil || size.columns == 0 {
		return defaultColumns
	}
	if int(size.columns) < minimumColumns {
		return minimumColumns
	}
	return int(size.columns)
}

// terminalHeight asks the terminal how tall it is, every time the region is
// drawn, so a window the operator resized bounds the region at the size it is
// now. The region is drawn at the bottom of the screen and erased by climbing
// back up it, so a region drawn taller than this would be climbing over rows
// that had already scrolled away.
func terminalHeight(fd uintptr) int {
	var size winsize
	if err := ioctl(fd, syscall.TIOCGWINSZ, unsafe.Pointer(&size)); err != nil || size.rows == 0 {
		return defaultRows
	}
	if int(size.rows) < minimumRows {
		return minimumRows
	}
	return int(size.rows)
}

// raiseSignal delivers the signal the terminal driver would have raised itself.
// A negotiated keyboard reports Ctrl-Z and Ctrl-\ as keys rather than raising
// anything, so the console raises them — to the whole process group, which is
// what the driver does, so a conversation and whatever it started are stopped
// exactly as they were before any of this was negotiated. A key the driver is
// still handling never reaches here, so nothing is raised twice.
func raiseSignal(pressed signalKey) {
	var signal syscall.Signal
	switch pressed {
	case signalSuspend:
		signal = syscall.SIGTSTP
	case signalQuit:
		signal = syscall.SIGQUIT
	default:
		return
	}
	syscall.Kill(0, signal)
}
