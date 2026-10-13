package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A re-run recorded against a run the docket never held is carried out or
// refused whether or not anything else of its item is on the docket. The hold
// reads every item with a run, so a decision the carry-out only looked for
// under the docket's items held an item with no entry out of every pull, with
// nothing ever attempting it (yoyodyne-ifd.428.94).

// neverDocketedRun is a run of the item that ended cancelled, with no blocker
// and nothing left of its change, and that the docket never held.
func neverDocketedRun() runstate.State {
	cancelled := diedHoldingItsChange()
	cancelled.RunID = undocketedRunID
	cancelled.Status = runstate.StatusCancelled
	cancelled.Failure = ""
	cancelled.Branch, cancelled.WorktreePath, cancelled.BaseCommit, cancelled.TargetBranch = "", "", "", ""
	return cancelled
}

// neverDocketedHarness is an item whose only run is one the docket never held,
// with a re-run recorded against that run and an empty docket.
func neverDocketedHarness(t *testing.T) *rerunHarness {
	t.Helper()
	harness := newRerunHarness(t, neverDocketedRun())
	harness.docket = &memoryDocket{}
	return harness
}

// neverDocketedBesideAStoppage is an item with a docketed stoppage nobody has
// decided about, and a later run of it the docket never held, against which the
// re-run is recorded.
func neverDocketedBesideAStoppage(t *testing.T) *rerunHarness {
	t.Helper()
	harness := newDocketedHarness(t, stoppedState())
	later := neverDocketedRun()
	later.StartedAt = docketedNow.Add(-30 * time.Minute)
	completed := docketedNow.Add(-10 * time.Minute)
	later.UpdatedAt, later.CompletedAt = completed, &completed
	if err := harness.runs.Create(later); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	recordRerunDecision(t, harness.runs, docketedItem, undocketedRunID)
	return harness
}

func TestARerunOfARunTheDocketNeverHeldIsCarriedOut(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		harness func(*testing.T) *rerunHarness
	}{
		{name: "no other entry for the item", harness: neverDocketedHarness},
		{name: "another entry for the item", harness: neverDocketedBesideAStoppage},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness := test.harness(t)
			ctx := context.Background()

			// Held for the harness, as any decided re-run is, rather than for a
			// reference nobody can reconcile.
			held, err := readmodel.HeldForAPerson(ctx, harness.runs, harness.runs.Triage(), nil)
			if err != nil {
				t.Fatalf("HeldForAPerson() error = %v", err)
			}
			reason, isHeld := held.Reason(docketedItem)
			if !isHeld || !held.Decided(docketedItem) || !strings.Contains(reason, "waits on the harness") || strings.Contains(reason, "could not be reconciled") {
				t.Fatalf("hold = %q, decided = %t, want the item held for the harness to carry out the re-run", reason, held.Decided(docketedItem))
			}

			carrying := harness.carryOut()
			task := theOneOutstanding(t, carrying)
			if task.Decision != runstate.TriageDecisionRerun || task.RunID != undocketedRunID {
				t.Fatalf("task = %#v, want the re-run of the run the docket never held", task)
			}
			carried, _, err := carrying.Carry(ctx, task)
			if err != nil {
				t.Fatalf("Carry() error = %v", err)
			}
			if !carried.Carried || len(harness.started) != 1 || harness.started[0].workItemID != docketedItem {
				t.Fatalf("carried = %#v, started = %#v, want the item pulled", carried, harness.started)
			}
			if lift := harness.started[0].selection.Lift; lift != nil {
				t.Fatalf("lift = %#v, want a fresh run from the target branch", lift)
			}
			if _, claimed, err := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, undocketedRunID)); err != nil || !claimed {
				t.Fatalf("claimed = %t, error = %v, want the re-run claimed under the run's own key", claimed, err)
			}
			if outstanding, err := harness.carryOutAt(time.Hour).Outstanding(); err != nil || len(outstanding) != 0 {
				t.Fatalf("outstanding = %#v, %v, want the carried-out re-run not offered again", outstanding, err)
			}
		})
	}
}

func TestARefusedRerunOfARunTheDocketNeverHeldReleasesItsItemAndSaysWhy(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		harness func(*testing.T) *rerunHarness
		// heldAfter is whether the item is still held once the re-run lets go: a
		// docketed stoppage nobody has decided about still holds it for her.
		heldAfter bool
	}{
		{name: "no other entry for the item", harness: neverDocketedHarness},
		{name: "another entry for the item", harness: neverDocketedBesideAStoppage, heldAfter: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness := test.harness(t)
			ctx := context.Background()
			recording := &neverDocketedNotes{}
			carrying := harness.carryOut()
			carrying.Notes = recording
			task := theOneOutstanding(t, carrying)
			// The run's one re-run is spent by something else first, which is a
			// refusal no later attempt of this decision can clear.
			if _, err := harness.reruns.Claim(ctx, runstate.Rerun{
				DocketKey:  task.DocketKey,
				PriorRunID: undocketedRunID,
				WorkItemID: docketedItem,
				Reason:     "claimed by a carry-out beside this one",
				ClaimedAt:  docketedNow,
				Preserved:  runstate.PreservedArtifacts{Disposition: runstate.PreservedGone},
			}); err != nil {
				t.Fatalf("Claim() error = %v", err)
			}

			carried, _, err := carrying.Carry(ctx, task)
			if err != nil {
				t.Fatalf("Carry() error = %v", err)
			}
			if carried.Carried || len(harness.started) != 0 || carried.Cause == "" || !carried.ReleasedHold {
				t.Fatalf("carried = %#v, started = %#v, want a permanent refusal that releases the item", carried, harness.started)
			}
			if len(recording.notes) != 1 || !strings.Contains(recording.notes[0], "hold this decision placed on the item is released") {
				t.Fatalf("notes = %#v, want the refusal and the release said on the item", recording.notes)
			}

			held, err := readmodel.HeldForAPerson(ctx, harness.runs, harness.runs.Triage(), nil)
			if err != nil {
				t.Fatalf("HeldForAPerson() error = %v", err)
			}
			reason, isHeld := held.Reason(docketedItem)
			if isHeld != test.heldAfter || strings.Contains(reason, undocketedRunID) {
				t.Fatalf("hold = %q (held %t), want the re-run's hold released", reason, isHeld)
			}
		})
	}
}

// A re-run answered by a run of the item started since it was decided is not
// offered again, as the hold no longer holds the item for it.
func TestARerunOfARunTheDocketNeverHeldIsNotOfferedOnceTheItemRanAgain(t *testing.T) {
	t.Parallel()

	harness := neverDocketedHarness(t)
	since := stoppedState()
	since.RunID = "run-55555555555555555555555555555555"
	since.StartedAt = docketedNow.Add(time.Minute)
	completed := docketedNow.Add(time.Hour)
	since.UpdatedAt, since.CompletedAt = completed, &completed
	since.Status = runstate.StatusSucceeded
	since.Blocker = ""
	if err := harness.runs.Create(since); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if outstanding, err := harness.carryOutAt(2 * time.Hour).Outstanding(); err != nil || len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, %v, want nothing offered", outstanding, err)
	}
}

// neverDocketedNotes is the tracker's notes on the item, kept so a test can read what the
// item was told.
type neverDocketedNotes struct{ notes []string }

func (n *neverDocketedNotes) Show(context.Context, string) (beads.WorkItem, error) {
	return beads.WorkItem{ID: docketedItem, Notes: strings.Join(n.notes, "\n")}, nil
}

func (n *neverDocketedNotes) RecordOutcome(_ context.Context, _ string, note string) (beads.WorkItem, error) {
	n.notes = append(n.notes, note)
	return beads.WorkItem{ID: docketedItem}, nil
}
