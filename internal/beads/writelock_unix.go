//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package beads

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// lockWriteFile takes an exclusive lock, waiting until it gets one or until the
// context bounds the wait. The lock belongs to the open file description rather
// than to the process, so two writes from one process queue behind one another
// exactly as two processes' do.
func lockWriteFile(ctx context.Context, file *os.File) error {
	return lockFile(ctx, file, syscall.LOCK_EX)
}

// lockStoreFile takes the store lock (storelock.go): shared for an ordinary bd
// call, exclusive for the rebuild of the store.
func lockStoreFile(ctx context.Context, file *os.File, shared bool) error {
	if shared {
		return lockFile(ctx, file, syscall.LOCK_SH)
	}
	return lockFile(ctx, file, syscall.LOCK_EX)
}

func lockFile(ctx context.Context, file *os.File, how int) error {
	for {
		err := syscall.Flock(int(file.Fd()), how|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func unlockWriteFile(file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// turnsSupported says the harness's bd calls take turns here (turns.go).
const turnsSupported = true

// probeLockFile says whether another open file holds a lock on file, by taking
// it shared without waiting and letting go at once.
func probeLockFile(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, unlockWriteFile(file)
}

// turnHeld says whether the ticket at path is held by a call (turns.go), by
// trying to take it shared without waiting and letting go at once.
func turnHeld(path string) (bool, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false, err
	}
	defer file.Close()
	return probeLockFile(file)
}
