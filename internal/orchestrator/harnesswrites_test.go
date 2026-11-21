package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// writeHarnessTicket leaves a ticket a tracker call holds in the primary checkout, as
// the harness's own tracker calls do for as long as each waits and runs.
func writeHarnessTicket(t *testing.T, repository string) {
	t.Helper()
	path := filepath.Join(repository, filepath.FromSlash(beads.HarnessWritePath), "turns", "01795256487569255000-0000034417-00000000000000000090.turn")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A fresh run starts while one of the harness's own tracker tickets is in the
// primary checkout.
func TestARunStartsBesideTheHarnessesOwnTrackerTicket(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	writeHarnessTicket(t, f.repository)

	f.park(t)
}

// A run waiting for the forge's checks is continued while one of the
// harness's own tracker tickets is in the primary checkout.
func TestAContinuationGoesOnBesideTheHarnessesOwnTrackerTicket(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	f.park(t)
	f.forge.setResult(publish.CheckPassed)
	writeHarnessTicket(t, f.repository)

	outcome, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() continuing error = %v", err)
	}
	if outcome.Paused || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("outcome = %#v, want the run to review and finish", outcome)
	}
}

// A file somebody left in the primary checkout still refuses a start and a
// continuation. The refused continuation takes no developer slot back: the
// run stays waiting, holding none, and is not paced, so the first pull after
// the checkout is clean continues it.
func TestAGenuineUncommittedFileRefusesWithoutHoldingASlot(t *testing.T) {
	t.Parallel()
	f := newCIFixture(t)
	dirty := filepath.Join(f.repository, "dirty.txt")
	if err := os.WriteFile(dirty, []byte("somebody's change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID); !errors.Is(err, gitworktree.ErrPrimaryNotReady) {
		t.Fatalf("Run() error = %v, want the dirty checkout refused", err)
	}
	if err := os.Remove(dirty); err != nil {
		t.Fatal(err)
	}

	_, parked := f.park(t)
	f.forge.setResult(publish.CheckPassed)
	if err := os.WriteFile(dirty, []byte("somebody's change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	if !errors.Is(err, gitworktree.ErrPrimaryNotReady) {
		t.Fatalf("Run() continuing error = %v, want the dirty checkout refused", err)
	}
	if !refusedForUnreadyCheckout(completed{err: err}) {
		t.Fatalf("refusal %v is paced, want it tried again at the next pull", err)
	}
	refused, loadErr := f.store.Load(parked.RunID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if refused.HoldsDeveloperSlot() || !refused.CIWait.Waiting() {
		t.Fatalf("refused run holds a slot = %v, waiting = %v; want it still waiting and holding no slot", refused.HoldsDeveloperSlot(), refused.CIWait.Waiting())
	}

	if err := os.Remove(dirty); err != nil {
		t.Fatal(err)
	}
	outcome, err := f.pipeline(t, true).Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() continuing once clean error = %v", err)
	}
	if outcome.Paused || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("outcome = %#v, want the run to review and finish once the checkout is clean", outcome)
	}
}
