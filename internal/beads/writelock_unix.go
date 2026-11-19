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
