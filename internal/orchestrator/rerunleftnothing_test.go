package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// leftNothingHarness is a run that died holding its change, was docketed for it,
// had a re-run recorded against it, and then lost its branch and worktree: no
// durable blocker, no branch, no worktree. The docket entry stands, because it is
// the record that the work stopped.
func leftNothingHarness(t *testing.T) *rerunHarness {
	t.Helper()
	harness := newRerunHarness(t, diedHoldingItsChange())
	gone := diedHoldingItsChange()
	gone.Branch, gone.WorktreePath, gone.BaseCommit, gone.TargetBranch = "", "", "", ""
	if err := harness.runs.Save(gone); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return harness
}

// A recorded re-run of a run that left nothing is carried out by the next pull
// as a fresh run from the target branch, exactly as a re-run of a run the docket
// never held is. Refusing it left a decision the harness would never carry out
// and nobody could withdraw, holding its item out of every pull.
func TestARecordedRerunOfARunThatLeftNothingIsStartedFresh(t *testing.T) {
	t.Parallel()

	harness := leftNothingHarness(t)
	ctx := context.Background()

	// Before the pull, the item is held for the harness, and the hold names the
	// harness and the next pull rather than a decision.
	held, err := readmodel.HeldForAPerson(ctx, harness.runs, harness.runs.Triage(), nil)
	if err != nil {
		t.Fatalf("HeldForAPerson() error = %v", err)
	}
	reason, isHeld := held.Reason(docketedItem)
	if !isHeld || !held.Decided(docketedItem) {
		t.Fatalf("hold = %q, decided = %t, want the item held for the harness", reason, held.Decided(docketedItem))
	}
	for _, want := range []string{"from the target branch", "next pull", "waits on the harness rather than on a decision"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("hold = %q, is missing %q", reason, want)
		}
	}

	carrying := harness.carryOut()
	task := theOneOutstanding(t, carrying)
	if task.Decision != runstate.TriageDecisionRerun || task.RunID != docketedRunID {
		t.Fatalf("task = %#v, want the recorded re-run", task)
	}
	carried, outcome, err := carrying.Carry(ctx, task)
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if !carried.Carried || len(harness.started) != 1 || outcome.RunID == "" {
		t.Fatalf("carried = %#v, started = %#v, want the item started rather than refused", carried, harness.started)
	}
	selection := harness.started[0].selection
	if selection.By != runstate.SelectedByDevelopmentManager || !strings.Contains(selection.Reason, rerunReasoning) {
		t.Fatalf("selection = %#v, want the decision's attribution and reasoning", selection)
	}
	// Nothing survived to lift from, so the fresh run starts from the target.
	if selection.Lift != nil {
		t.Fatalf("lift = %#v, want a run from the target branch", selection.Lift)
	}
	if _, claimed, err := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); err != nil || !claimed {
		t.Fatalf("claimed = %t, error = %v, want the decision's one re-run spent on the run it started", claimed, err)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if finding, found := counters.CarryOutOf(docketedRunID); found {
		t.Fatalf("finding = %#v, want no refusal left standing over a run that started", finding)
	}
}

// Where the carry-out of such a re-run is refused for a cause that will not
// clear, the decision lets the item go rather than holding it for good, and the
// item is told both what refused it and that it was released.
func TestAPermanentlyRefusedRerunOfARunThatLeftNothingReleasesItsItem(t *testing.T) {
	t.Parallel()

	harness := leftNothingHarness(t)
	ctx := context.Background()
	carrying := harness.carryOut()
	task := theOneOutstanding(t, carrying)
	// The stoppage's one re-run is spent by something else first, which is a
	// refusal no later attempt of this decision can clear.
	if _, err := harness.reruns.Claim(ctx, runstate.Rerun{
		DocketKey:  task.DocketKey,
		PriorRunID: docketedRunID,
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
	if carried.Carried || len(harness.started) != 0 {
		t.Fatalf("carried = %#v, want the refusal", carried)
	}
	if carried.Cause == "" || !carried.ReleasedHold {
		t.Fatalf("carried = %#v, want a permanent refusal that releases the item", carried)
	}

	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if len(counters.PendingCarryOutNotes) != 1 {
		t.Fatalf("pending notes = %#v, want one note for the item", counters.PendingCarryOutNotes)
	}
	note := counters.PendingCarryOutNotes[0]
	for _, want := range []string{"will not clear on its own", "already", "hold this decision placed on the item is released", "next pull may start it"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note = %q, is missing %q", note, want)
		}
	}

	held, err := readmodel.HeldForAPerson(ctx, harness.runs, harness.runs.Triage(), nil)
	if err != nil {
		t.Fatalf("HeldForAPerson() error = %v", err)
	}
	if reason, isHeld := held.Reason(docketedItem); isHeld {
		t.Fatalf("hold = %q, want the item released for a fresh pull", reason)
	}
}

// The release is only for a run that left nothing. A permanent refusal of a
// re-run whose stopped run's change is still there keeps holding the item, since
// a fresh pull would start over on top of that change.
func TestAPermanentlyRefusedRerunOfAPreservedRunStillHoldsItsItem(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	ctx := context.Background()
	carrying := harness.carryOut()
	task := theOneOutstanding(t, carrying)
	if _, err := harness.reruns.Claim(ctx, runstate.Rerun{
		DocketKey:  task.DocketKey,
		PriorRunID: docketedRunID,
		WorkItemID: docketedItem,
		Reason:     "claimed by a carry-out beside this one",
		ClaimedAt:  docketedNow,
		Preserved:  runstate.PreservedArtifacts{Disposition: runstate.PreservedKept, Branch: "yoyodyne/task/abc"},
	}); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	carried, _, err := carrying.Carry(ctx, task)
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if carried.Cause == "" || carried.ReleasedHold {
		t.Fatalf("carried = %#v, want a permanent refusal that releases nothing", carried)
	}
	held, err := readmodel.HeldForAPerson(ctx, harness.runs, harness.runs.Triage(), nil)
	if err != nil {
		t.Fatalf("HeldForAPerson() error = %v", err)
	}
	if _, isHeld := held.Reason(docketedItem); !isHeld {
		t.Fatal("the item was released although its stopped run's change is still there")
	}
}
