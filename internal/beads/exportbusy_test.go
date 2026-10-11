//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package beads

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
)

// An export that could not start because another held the lock past one call's
// bound, and one bd was killed at that bound, are both said in a way a caller
// waiting out a stalled tracker recognizes, and neither asked bd for anything
// or changed the published copy.
func TestAnExportThatDidNotAnswerReadsAsOneToWaitOut(t *testing.T) {
	t.Parallel()
	repository := exportRepository(t)
	lock, err := os.OpenFile(filepath.Join(repository, ".beads", "yoyodyne-export.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{responses: []string{"{\"id\":\"item\"}\n"}}
	client := Client{Dir: repository, Runner: runner, Timeout: 50 * time.Millisecond}
	err = client.RefreshExport(context.Background())
	if !errors.Is(err, ErrExportBusy) {
		t.Fatalf("export behind a held lock = %v, want ErrExportBusy", err)
	}
	if len(runner.commands) != 0 {
		t.Fatal("bd was asked for an export that never held the lock")
	}
	if _, err := os.Stat(filepath.Join(repository, ExportPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published copy after a busy export: %v, want none", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}

	runner.results = []execution.ProcessResult{{Status: execution.ProcessTimedOut, ExitCode: -1}}
	err = client.RefreshExport(context.Background())
	if err == nil || !recovery.Recoverable(err) || errors.Is(err, ErrExportBusy) {
		t.Fatalf("killed export = %v, want a failure read as one to wait out", err)
	}
}
