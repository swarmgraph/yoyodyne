package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A decision is carried out or refused with the reason, whatever became of the
// docket entry for its run. yoyodyne-ifd.428.52 is the case where it was
// neither in any sense the development manager could read: yoyodyne-ifd.187's
// re-run of run-04e578ce, a run the docket never held, was refused onto the
// item's record thirty-nine times with nothing said about what would clear it,
// and no entry she was shown carried the refusal at all.

// undocketedRunID is a run of the docketed item that ended without being
// docketed: a re-run the harness cancelled on its way out.
const undocketedRunID = "run-33333333333333333333333333333333"

// recordUndocketedRun records a run of the item that ended cancelled and was
// never docketed as a stoppage, which is what run-04e578ce was.
func recordUndocketedRun(t *testing.T, harness *rerunHarness) {
	t.Helper()
	cancelled := stoppedState()
	cancelled.RunID = undocketedRunID
	cancelled.Status = runstate.StatusCancelled
	cancelled.Blocker = ""
	cancelled.CheckFailure = nil
	cancelled.ReviewFindingDetails = nil
	cancelled.ReviewFindings = 0
	cancelled.ReviewSummary = ""
	if err := harness.runs.Create(cancelled); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}

// joined is the docket as the development manager is shown it: the entries
// with the item's triage record joined onto them.
func joined(t *testing.T, harness *rerunHarness) []triage.Entry {
	t.Helper()
	entries, err := harness.docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	docketer := Docketer{Decisions: harness.runs.Triage(), Reruns: harness.reruns}
	if problems := docketer.joinDecisions(entries, docketedRunsOf(entries), nil, nil); len(problems) != 0 {
		t.Fatalf("joinDecisions() problems = %v", problems)
	}
	return entries
}

// The shape the item names: an entry an earlier repair decision closed, and a
// re-run recorded about the same stoppage afterwards. The closure does not keep
// the re-run from the pass; it is attempted at the next pull as any other.
func TestARerunRecordedAfterARepairClosedTheEntryIsAttemptedAtTheNextPull(t *testing.T) {
	t.Parallel()

	harness := newDocketedHarness(t, stoppedState())
	if _, err := harness.runs.Triage().GrantRepair(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRepair, docketedRunID), 1, docketedNow, laterCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	harness.docket.close(triage.Key(triage.ClassStoppedRun, docketedRunID), runstate.TriageDecisionRepair, docketedNow)
	if _, err := harness.runs.Triage().RecordRerun(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRerun, docketedRunID), docketedNow.Add(time.Hour), laterCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}

	carrying := harness.carryOutAt(2 * time.Hour)
	task := theOneOutstanding(t, carrying)
	if task.RunID != docketedRunID || task.Decision != runstate.TriageDecisionRerun {
		t.Fatalf("task = %#v, want the re-run recorded over the closed entry offered", task)
	}
	carried, _, err := carrying.Carry(context.Background(), task)
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if !carried.Carried || len(harness.started) != 1 {
		t.Fatalf("carried = %#v, started = %#v, want the re-run fired", carried, harness.started)
	}
}

// The 187 case: a re-run recorded against a run the docket never held, where
// the item's one docketed stoppage has already been re-run. It is carried out:
// the item starts again from the change that run kept on its branch, claimed
// under the key the docket would have given the run, and it is not offered a
// second time.
func TestARerunOfAnUndocketedRunStartsTheItemAgain(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	first := harness.carryOut()
	if _, _, err := first.Carry(context.Background(), theOneOutstanding(t, first)); err != nil {
		t.Fatalf("Carry() of the first decision error = %v", err)
	}
	recordUndocketedRun(t, harness)
	if _, err := harness.runs.Triage().RecordRerun(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRerun, undocketedRunID), docketedNow.Add(time.Hour), laterCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}

	later := harness.carryOutAt(2 * time.Hour)
	// The claim is stamped by the rerunner's own clock, which has to read the
	// same moment the carry-out does for the claim to answer the decision.
	rerunner := harness.rerunner()
	rerunner.Clock = laterClock{after: 2 * time.Hour}
	later.Rerunner = rerunner
	task := theOneOutstanding(t, later)
	if task.RunID != undocketedRunID {
		t.Fatalf("task = %#v, want the decision about the undocketed run attempted", task)
	}
	carried, _, err := later.Carry(context.Background(), task)
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if !carried.Carried || len(harness.started) != 2 {
		t.Fatalf("carried = %#v, started = %#v, want the item started again", carried, harness.started)
	}
	if lift := harness.started[1].selection.Lift; lift == nil || lift.RunID != undocketedRunID || !lift.FallBackToTarget {
		t.Fatalf("lift = %#v, want the fresh run started from the change run %s kept, falling back to the target", lift, undocketedRunID)
	}
	claimed, err := harness.reruns.Claimed(docketedItem)
	if err != nil {
		t.Fatalf("Claimed() error = %v", err)
	}
	found := false
	for _, claim := range claimed {
		found = found || claim.DocketKey == triage.Key(triage.ClassStoppedRun, undocketedRunID)
	}
	if !found {
		t.Fatalf("claimed = %#v, want the re-run claimed under the undocketed run's stopped-run key", claimed)
	}
	if outstanding, err := harness.carryOutAt(3 * time.Hour).Outstanding(); err != nil || len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, %v, want the carried-out decision not offered again", outstanding, err)
	}
}

// unrecordedRunID is a run the harness holds no record of at all, which is
// what a decision can name that nothing can start from.
const unrecordedRunID = "run-44444444444444444444444444444444"

// A re-run of a run the harness holds no record of cannot start anything. It
// is refused, the refusal says why and names the decision the harness would
// carry out instead, and the item's docket entry carries it — naming nobody
// but the development manager, whose decision it is.
func TestARerunOfAnUnrecordedRunIsRefusedNamingWhyAndTheDecisionThatApplies(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	first := harness.carryOut()
	if _, _, err := first.Carry(context.Background(), theOneOutstanding(t, first)); err != nil {
		t.Fatalf("Carry() of the first decision error = %v", err)
	}
	if _, err := harness.runs.Triage().RecordRerun(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRerun, unrecordedRunID), docketedNow.Add(time.Hour), laterCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}

	later := harness.carryOutAt(2 * time.Hour)
	carried, _, err := later.Carry(context.Background(), theOneOutstanding(t, later))
	if err != nil {
		t.Fatalf("Carry() error = %v, want the refusal reported as a gate", err)
	}
	if carried.Carried || len(harness.started) != 1 {
		t.Fatalf("carried = %#v, started = %#v, want nothing fired", carried, harness.started)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	recorded, found := counters.CarryOutOf(unrecordedRunID)
	if !found || recorded.Unattempted || recorded.Attempts != 1 {
		t.Fatalf("finding = %#v (found %t), want the refusal of an attempt on the item", recorded, found)
	}
	for _, want := range []string{"holds no stoppage of it", "is not a run the harness holds a record of"} {
		if !strings.Contains(recorded.Refusal, want) {
			t.Fatalf("refusal = %q, want it to say %q", recorded.Refusal, want)
		}
	}
	if !strings.Contains(recorded.Clears, "a re-run recorded instead against the latest run of "+docketedItem) {
		t.Fatalf("clears = %q, want it to name the re-run the harness would carry out", recorded.Clears)
	}
	for _, unwanted := range []string{"what the refusal itself names", "operator", "yoyo run"} {
		if strings.Contains(recorded.Clears, unwanted) {
			t.Fatalf("clears = %q, want nothing routed to %q", recorded.Clears, unwanted)
		}
	}

	entries := joined(t, harness)
	if len(entries) != 1 {
		t.Fatalf("docket = %#v, want the item's one stoppage", entries)
	}
	shown := entries[0].CarryOut
	if shown == nil || shown.RunID != unrecordedRunID || shown.Refusal != recorded.Refusal {
		t.Fatalf("carry-out = %#v, want the item's entry to carry the refusal about the unrecorded run", shown)
	}
	if rendered := entries[0].Render(); !strings.Contains(rendered, "is about run "+unrecordedRunID+", which this docket holds no entry for") {
		t.Fatalf("rendered = %q, want it to say which run the finding is about", rendered)
	}
}

// Where the item has a docketed stoppage that can still take the re-run, the
// refusal names that stoppage as where the decision belongs.
func TestARerunOfAnUnrecordedRunNamesTheDocketedStoppageThatCouldTakeIt(t *testing.T) {
	t.Parallel()

	harness := newDocketedHarness(t, stoppedState())
	if _, err := harness.runs.Triage().RecordRerun(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRerun, unrecordedRunID), docketedNow, rerunCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	carrying := harness.carryOut()
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if carried.Carried {
		t.Fatalf("carried = %#v, want the decision refused", carried)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	recorded, _ := counters.CarryOutOf(unrecordedRunID)
	for _, want := range []string{"recorded instead against a stoppage the docket holds", docketedRunID} {
		if !strings.Contains(recorded.Clears, want) {
			t.Fatalf("clears = %q, want it to name %q", recorded.Clears, want)
		}
	}
}

// A repair of a run the docket never held has no worktree to re-enter, so it is
// refused naming the re-run of the same run as the decision the harness would
// carry out.
func TestARepairOfAnUndocketedRunNamesTheRerunThatWouldApply(t *testing.T) {
	t.Parallel()

	harness := newDocketedHarness(t, stoppedState())
	recordUndocketedRun(t, harness)
	carrying := harness.carryOut()
	carrying.Repairer = RepairContinuer{Docket: harness.docket}
	task := CarryOutTask{
		WorkItemID: docketedItem,
		RunID:      undocketedRunID,
		DocketKey:  triage.Key(triage.ClassStoppedRun, undocketedRunID),
		Decision:   runstate.TriageDecisionRepair,
		DecidedAt:  docketedNow,
	}
	refusal, clears, undocketed := carrying.undocketed(task, NoDocketedStoppageError{RunID: undocketedRunID, Act: "repair"})
	if !undocketed {
		t.Fatalf("undocketed = false, want the missing stoppage recognised")
	}
	if !strings.Contains(refusal, "ended cancelled") {
		t.Fatalf("refusal = %q, want how the run ended", refusal)
	}
	if !strings.Contains(clears, "a re-run recorded against run "+undocketedRunID+" instead") || strings.Contains(clears, "operator") {
		t.Fatalf("clears = %q, want the re-run of the same run named and nobody else", clears)
	}
}

// A run whose only entry is left out of the entries the join is handed is still
// a run the docket holds, so a finding about it is not shown on its siblings as
// though the docket held none.
func TestAFindingIsNotShownAsUndocketedForARunTheWholeDocketHolds(t *testing.T) {
	t.Parallel()

	harness := newDocketedHarness(t, stoppedState())
	if _, err := harness.runs.Triage().RecordRerun(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRerun, unrecordedRunID), docketedNow, rerunCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	carrying := harness.carryOut()
	if _, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	entries, err := harness.docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	docketer := Docketer{Decisions: harness.runs.Triage(), Reruns: harness.reruns}
	whole := docketedRunsOf(entries)
	whole[unrecordedRunID] = true
	if problems := docketer.joinDecisions(entries, whole, nil, nil); len(problems) != 0 {
		t.Fatalf("joinDecisions() problems = %v", problems)
	}
	if entries[0].CarryOut != nil {
		t.Fatalf("carry-out = %#v, want nothing shown about a run the whole docket holds", entries[0].CarryOut)
	}
}

// The unattempted record covers the same shape: a decision about an undocketed
// run that no pass attempts is written onto the item, and the item's docket
// entry carries it, naming the run it is about.
func TestAnUnattemptedDecisionAboutAnUndocketedRunIsShownOnTheItemsEntry(t *testing.T) {
	t.Parallel()

	harness := newDocketedHarness(t, stoppedState())
	recordUndocketedRun(t, harness)
	if _, err := harness.runs.Triage().RecordRerun(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRerun, undocketedRunID), docketedNow, rerunCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	going := stoppedState()
	going.RunID = "run-11111111111111111111111111111111"
	going.Status = runstate.StatusRunning
	going.Phase = runstate.PhaseDeveloping
	going.CompletedAt = nil
	going.Blocker = ""
	going.CheckFailure = nil
	going.ReviewFindingDetails = nil
	going.ReviewFindings = 0
	going.ReviewSummary = ""
	if err := harness.runs.Create(going); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	written, err := harness.carryOutAt(2*time.Minute).RecordUnattempted(context.Background(), time.Minute, nil)
	if err != nil {
		t.Fatalf("RecordUnattempted() error = %v", err)
	}
	if len(written) != 1 || written[0].RunID != undocketedRunID {
		t.Fatalf("written = %#v, want the decision about the undocketed run written down as unattempted", written)
	}
	entries := joined(t, harness)
	if len(entries) != 1 || entries[0].CarryOut == nil || !entries[0].CarryOut.Unattempted || entries[0].CarryOut.RunID != undocketedRunID {
		t.Fatalf("docket = %#v, want the item's entry to carry the unattempted decision", entries)
	}
	if rendered := entries[0].Render(); !strings.Contains(rendered, "No pass has attempted") || !strings.Contains(rendered, undocketedRunID) {
		t.Fatalf("rendered = %q, want the entry to say the decision about the undocketed run was not attempted", rendered)
	}
}

// A finding about a decision she has since decided past is not shown on the
// item's entry: only the item's latest decision is.
func TestAFindingAboutAnUndocketedRunIsNotShownOnceSheDecidesPastIt(t *testing.T) {
	t.Parallel()

	harness := newDocketedHarness(t, stoppedState())
	if _, err := harness.runs.Triage().RecordRerun(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRerun, unrecordedRunID), docketedNow, rerunCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	carrying := harness.carryOut()
	if _, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if _, err := harness.runs.Triage().RecordDecision(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionEscalate, docketedRunID), docketedNow.Add(time.Hour)); err != nil {
		t.Fatalf("RecordDecision() error = %v", err)
	}
	if entries := joined(t, harness); len(entries) != 1 || entries[0].CarryOut != nil {
		t.Fatalf("docket = %#v, want no finding about the decision she moved past", entries)
	}
}
