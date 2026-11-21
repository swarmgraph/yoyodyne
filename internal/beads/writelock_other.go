//go:build !(darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd)

package beads

import (
	"context"
	"errors"
	"os"
)

// lockWriteFile refuses rather than letting a write run unqueued, because an
// unqueued write is the one bd can lose without saying so.
func lockWriteFile(context.Context, *os.File) error {
	return errors.New("queueing writes to one tracker item is unsupported on this platform")
}

func unlockWriteFile(*os.File) error {
	return nil
}

// lockStoreFile is not available here; a call runs without the store lock, as
// it did before there was one.
func lockStoreFile(context.Context, *os.File, bool) error { return nil }

// turnsSupported says calls take no turns here: there is no lock to hold a
// ticket with, so each runs as it did before there was a line (turns.go).
const turnsSupported = false

func turnHeld(string) (bool, error) { return false, nil }
