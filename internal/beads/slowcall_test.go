package beads

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// keptSlowCalls is a recorder that keeps what it is handed, or refuses it.
type keptSlowCalls struct {
	calls []SlowCall
	err   error
}

func (k *keptSlowCalls) RecordSlowCall(call SlowCall) error {
	k.calls = append(k.calls, call)
	return k.err
}

// slowStore is a repository whose tracker store holds a database of size bytes.
func slowStore(t *testing.T, size int) string {
	t.Helper()
	repository := exportRepository(t)
	database := filepath.Join(repository, ".beads", "embeddeddolt")
	if err := os.Mkdir(database, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(database, "chunks"), make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return repository
}

// An export stopped at its limit is kept with how long it ran, the store's
// size on disk, and what held the store, and says the same in its failure;
// the cause it comes to tells a call waiting behind other processes from a
// tracker slow on its own.
func TestAnExportStoppedAtItsLimitIsKeptWithTheStoresSizeAndHolders(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 10, 25, 8, 45, 0, 0, time.Local)
	for name, tc := range map[string]struct {
		holders   []StoreHolder
		wantSaid  string
		wantCause string
	}{
		"another process held the store": {
			holders:   []StoreHolder{{PID: 38601, Command: "bd show yoyodyne-ifd --json", Since: since}, {PID: 39706, Command: "bd list --all --json"}},
			wantSaid:  "when it was stopped, the tracker's store was held open by process 38601 (bd show yoyodyne-ifd --json), running since " + since.Format("2006-01-02 15:04:05 MST") + "; process 39706 (bd list --all --json); the tracker's store was 2.0 MB on disk",
			wantCause: "exporting every work item ran 30s without finishing, on a store of 2.0 MB on disk, while 2 other processes had the store open, so it was most likely waiting behind them",
		},
		"nothing else held the store": {
			wantSaid:  "no process had the tracker's store open when it was stopped; the tracker's store was 2.0 MB on disk",
			wantCause: "exporting every work item ran 30s without finishing, on a store of 2.0 MB on disk, while no other process had the store open, so the tracker itself was too slow",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository := slowStore(t, 2<<20)
			kept := &keptSlowCalls{}
			client := Client{
				Runner:    &fakeRunner{results: []execution.ProcessResult{timedOutCall()}},
				Dir:       repository,
				Holders:   fixedHolders{holders: tc.holders},
				SlowCalls: kept,
			}
			ctx := OnBehalfOf(context.Background(), "yoyo reconcile")

			err := client.RefreshExport(ctx)
			if err == nil || !timedOut(err) {
				t.Fatalf("RefreshExport() error = %v, want the export's timeout", err)
			}
			if !strings.Contains(err.Error(), tc.wantSaid) {
				t.Errorf("RefreshExport() error = %q, want it to say %q", err, tc.wantSaid)
			}
			if len(kept.calls) != 1 {
				t.Fatalf("kept %d slow calls, want 1", len(kept.calls))
			}
			call := kept.calls[0]
			if call.Verb != "export" || call.For != "yoyo reconcile" || call.Limit != DefaultTimeout || call.StartedAt.IsZero() || call.CallerDeadline != 0 {
				t.Errorf("slow call = %+v, want the export for yoyo reconcile at the client's limit", call)
			}
			if call.StoreBytes != 2<<20 || call.StoreUnmeasured != "" || call.Store != canonicalPath(filepath.Join(repository, ".beads", "embeddeddolt")) {
				t.Errorf("slow call store = %q, %d bytes, unmeasured %q; want the database's 2 MB", call.Store, call.StoreBytes, call.StoreUnmeasured)
			}
			if call.Held() != (len(tc.holders) > 0) || len(call.Holders) != len(tc.holders) {
				t.Errorf("slow call holders = %+v, want %+v", call.Holders, tc.holders)
			}
			read, ok := SlowCallOf(err)
			if !ok || read.Verb != "export" {
				t.Fatalf("SlowCallOf() = %+v, %t; want the export", read, ok)
			}
			// The fake runner answers at once, so the cause is checked against a
			// call that ran the whole limit.
			read.Ran = 30 * time.Second
			if got := read.Cause(); got != tc.wantCause {
				t.Errorf("Cause() = %q, want %q", got, tc.wantCause)
			}
		})
	}
}

// A slow call that cannot be kept says so in its failure, which is still the
// timeout and nothing else.
func TestASlowCallThatCannotBeKeptSaysSo(t *testing.T) {
	t.Parallel()
	client := Client{
		Runner:    &fakeRunner{results: []execution.ProcessResult{timedOutCall()}},
		Dir:       slowStore(t, 10),
		Holders:   fixedHolders{},
		SlowCalls: &keptSlowCalls{err: errors.New("the state directory is full")},
	}
	_, err := client.run(context.Background(), "ready", "--json")
	if err == nil || !timedOut(err) || !Unanswered(err) {
		t.Fatalf("run() error = %v, want the timeout", err)
	}
	if want := "the record of this slow call could not be kept: the state directory is full"; !strings.Contains(err.Error(), want) {
		t.Errorf("run() error = %q, want it to say %q", err, want)
	}
}

// A call that never started, and a call that failed for any reason but time,
// is not a slow call.
func TestOnlyACallThatRanOutOfTimeIsASlowCall(t *testing.T) {
	t.Parallel()
	kept := &keptSlowCalls{}
	client := Client{Runner: &deadlineRunner{}, Dir: slowStore(t, 10), Holders: fixedHolders{}, SlowCalls: kept}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := client.run(ctx, "list", "--all"); err == nil {
		t.Fatal("run() error = nil, want the call refused")
	} else if _, ok := SlowCallOf(err); ok {
		t.Errorf("SlowCallOf(%q) found a slow call in a call that never started", err)
	}

	client.Runner = &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "no such issue"}}}
	if _, err := client.run(context.Background(), "show", "yoyodyne-1", "--json"); err == nil {
		t.Fatal("run() error = nil, want bd's refusal")
	} else if _, ok := SlowCallOf(err); ok {
		t.Errorf("SlowCallOf(%q) found a slow call in a refusal", err)
	}
	if len(kept.calls) != 0 {
		t.Errorf("kept %+v, want nothing", kept.calls)
	}
}

// A call stopped by its caller's shorter deadline says that was the cause,
// rather than the tracker or a holder.
func TestACallStoppedByItsCallersDeadlineSaysThatWasTheCause(t *testing.T) {
	t.Parallel()
	kept := &keptSlowCalls{}
	client := Client{Runner: &deadlineRunner{}, Dir: slowStore(t, 10), Holders: fixedHolders{}, SlowCalls: kept}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := client.run(ctx, "list", "--all")
	call, ok := SlowCallOf(err)
	if !ok || call.CallerDeadline <= 0 || len(kept.calls) != 1 {
		t.Fatalf("SlowCallOf(%v) = %+v, %t, kept %d; want the caller's deadline kept", err, call, ok, len(kept.calls))
	}
	if want := "and was stopped by its caller's own deadline of"; !strings.Contains(call.Cause(), want) {
		t.Errorf("Cause() = %q, want it to say %q", call.Cause(), want)
	}
}
