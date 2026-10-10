package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A recovery recorded about an older run of an item stops applying once the
// item is closed with its work settled. The cases are the ones reported on
// 2026-10-02: Human identity mapping (yoyodyne-ifd.113) and Approved proposals
// becoming work items (yoyodyne-ifd.120), whose older runs' repairs were
// attempted after a later run merged; the successful run on part 1 of the
// README split (yoyodyne-ifd.121.3) being repaired itself; and the settled
// stoppage of Guarding the attribution-destroying writer (yoyodyne-ifd.149).

const laterRunID = "run-fedcba9876543210fedcba9876543210"

// noteTracker is the item as the tracker holds it, with the notes appended to it,
// and how often it was read.
type noteTracker struct {
	item  beads.WorkItem
	notes []string
	shown int
}

func (n *noteTracker) Show(context.Context, string) (beads.WorkItem, error) {
	n.shown++
	item := n.item
	item.Notes = strings.Join(n.notes, "\n")
	return item, nil
}

func (n *noteTracker) RecordOutcome(_ context.Context, _ string, note string) (beads.WorkItem, error) {
	n.notes = append(n.notes, note)
	return n.item, nil
}

// countingRepairer is a repair action that records being asked, which none of
// these sequences may do once the recovery no longer applies.
type countingRepairer struct{ asked []string }

func (r *countingRepairer) Continue(_ context.Context, request RepairContinueRequest) (RepairContinueResult, error) {
	r.asked = append(r.asked, request.Run)
	return RepairContinueResult{Continued: true, Reason: "continued"}, nil
}

// settledFixture is a stopped run of the docketed item with a repair the
// development manager granted and the entry her decision closed.
type settledFixture struct {
	harness  *rerunHarness
	tracker  *noteTracker
	repairer *countingRepairer
}

func newSettledFixture(t *testing.T, status string) settledFixture {
	t.Helper()
	harness := newDocketedHarness(t, stoppedState())
	if _, err := harness.runs.Triage().GrantRepair(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRepair, docketedRunID), 1, docketedNow, laterCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	harness.docket.close(triage.Key(triage.ClassStoppedRun, docketedRunID), runstate.TriageDecisionRepair, docketedNow)
	return settledFixture{
		harness:  harness,
		tracker:  &noteTracker{item: beads.WorkItem{ID: docketedItem, Title: docketedTitle, Status: status}},
		repairer: &countingRepairer{},
	}
}

func (f settledFixture) carryOut(after time.Duration) CarryOut {
	carrying := f.harness.carryOutAt(after)
	carrying.Notes = f.tracker
	carrying.Repairer = f.repairer
	return carrying
}

// mergedLaterRun is a run of the same item started after the stopped one, whose
// change merged and whose publication completed; merged false leaves its pull
// request open.
func mergedLaterRun(t *testing.T, harness *rerunHarness, merged bool) runstate.State {
	t.Helper()
	later := droppedPublication()
	later.RunID = laterRunID
	later.StartedAt = docketedNow.Add(10 * time.Minute)
	completed := docketedNow.Add(30 * time.Minute)
	later.UpdatedAt, later.CompletedAt = completed, &completed
	later.WorktreePath, later.Branch = "/state/worktrees/later", "yoyodyne/task/later"
	later.WorktreeRemoved, later.BranchRemoved = true, true
	published := *later.PullRequest
	published.Branch, published.Number, published.URL = later.Branch, 751, "https://forge.invalid/pull/751"
	later.PullRequest = &published
	if merged {
		settleLaterRun(&later)
	}
	if err := harness.runs.Create(later); err != nil {
		t.Fatalf("Create() of the later run error = %v", err)
	}
	return later
}

// settleLaterRun is the later run's merge confirmed and its publication
// complete.
func settleLaterRun(later *runstate.State) {
	later.Status, later.Phase = runstate.StatusSucceeded, runstate.PhaseComplete
	later.Blocker, later.PublishFailure = "", ""
	later.PullRequest.State, later.PullRequest.Merged, later.PullRequest.MergeCommit = "MERGED", true, strings.Repeat("e", 40)
}

// The 113 and 120 shape: a closed item whose later run merged, and an older
// run's repair standing. Nothing is offered, nothing is asked of the repair
// action, and the finding is written once, with a note, removing nothing.
func TestARecoveryOfAClosedItemsOlderRunAfterALaterMergeIsNotOffered(t *testing.T) {
	t.Parallel()
	f := newSettledFixture(t, "closed")
	mergedLaterRun(t, f.harness, true)
	before, err := f.harness.runs.Load(docketedRunID)
	if err != nil {
		t.Fatal(err)
	}

	carrying := f.carryOut(time.Hour)
	if outstanding, err := carrying.Outstanding(); err != nil || len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, %v; want the obsolete repair not offered", outstanding, err)
	}
	if _, err := carrying.RecordUnattempted(context.Background(), time.Minute, nil); err != nil {
		t.Fatalf("RecordUnattempted() error = %v", err)
	}
	counters, err := f.harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatal(err)
	}
	finding, found := counters.NoLongerApplies(docketedRunID)
	if !found || !strings.Contains(finding.Refusal, laterRunID) || !strings.Contains(finding.Refusal, "#751") ||
		!strings.Contains(finding.Refusal, docketedRunID) || !strings.Contains(finding.Refusal, "does not say the item's acceptance criteria were met") {
		t.Fatalf("finding = %#v, %v; want one naming the old run and the merge that settled it", finding, found)
	}
	if counters.AwaitingCarryOut(docketedRunID) {
		t.Fatal("the repair still reads as awaiting the harness")
	}
	if _, refused := counters.RefusedCarryOut(docketedRunID); refused {
		t.Fatal("the finding reads as a refusal the development manager has to answer")
	}
	if refused, unattempted := counters.CarryOutFindings(); refused != 0 || unattempted != 0 {
		t.Fatalf("findings = %d refused, %d unattempted; want none counted", refused, unattempted)
	}
	if len(f.tracker.notes) != 1 || !strings.Contains(f.tracker.notes[0], "no longer applies") ||
		!strings.Contains(f.tracker.notes[0], "branch was not removed and its worktree was not removed") {
		t.Fatalf("notes = %#v; want one note saying what the run's record says it kept", f.tracker.notes)
	}

	// The docket the development manager reads does not hand it back to her.
	for _, entry := range joined(t, f.harness) {
		if entry.RunID == docketedRunID && entry.Undecided(docketedNow.Add(time.Hour)) {
			t.Fatalf("entry %#v is a question again", entry)
		}
		if entry.RunID == docketedRunID && !strings.Contains(entry.Render(), "no longer applies") {
			t.Fatalf("entry renders %q; want it to say the recovery no longer applies", entry.Render())
		}
	}

	// Repeated passes neither ask anything again nor write anything twice.
	shown := f.tracker.shown
	again := f.carryOut(2 * time.Hour)
	if outstanding, err := again.Outstanding(); err != nil || len(outstanding) != 0 {
		t.Fatalf("again = %#v, %v", outstanding, err)
	}
	if _, err := again.RecordUnattempted(context.Background(), time.Minute, nil); err != nil {
		t.Fatal(err)
	}
	if f.tracker.shown != shown || len(f.tracker.notes) != 1 {
		t.Fatalf("shown %d→%d, notes %d; want the recorded finding to settle later passes", shown, f.tracker.shown, len(f.tracker.notes))
	}
	if len(f.repairer.asked) != 0 {
		t.Fatalf("repair asked for %v", f.repairer.asked)
	}
	after, err := f.harness.runs.Load(docketedRunID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != before.Status || after.Blocker != before.Blocker || after.Branch != before.Branch ||
		after.WorktreePath != before.WorktreePath || after.BranchRemoved || after.WorktreeRemoved || after.Retirement != nil {
		t.Fatalf("the old run's record changed: %+v", after)
	}
}

// A recovery the pass chose before the later publication settled is asked again
// as it is carried out, and nothing is attempted.
func TestAChosenRecoveryIsRecheckedBeforeItIsCarriedOut(t *testing.T) {
	t.Parallel()
	f := newSettledFixture(t, "closed")
	later := mergedLaterRun(t, f.harness, false)
	task := theOneOutstanding(t, f.carryOut(time.Hour))
	if task.RunID != docketedRunID || task.Decision != runstate.TriageDecisionRepair {
		t.Fatalf("task = %#v; want the repair offered while the later publication is unsettled", task)
	}

	settleLaterRun(&later)
	later.UpdatedAt = docketedNow.Add(40 * time.Minute)
	if err := f.harness.runs.Save(later); err != nil {
		t.Fatal(err)
	}
	carried, outcome, err := f.carryOut(time.Hour).Carry(context.Background(), task)
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if carried.Carried || carried.Cause != triage.CarryOutNoLongerApplies || outcome.RunID != "" || len(f.repairer.asked) != 0 {
		t.Fatalf("carried = %#v, outcome = %#v, asked = %v; want nothing carried out", carried, outcome, f.repairer.asked)
	}
	counters, err := f.harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := counters.NoLongerApplies(docketedRunID); !found {
		t.Fatal("the execution-time finding was not recorded")
	}
}

// What does not establish that the work is settled: a later publication still
// unsettled, an item closed with no settled work behind it, an item still open,
// and an item reopened after the later merge. In each the repair is offered and
// carried out by the action, whose own gates decide it.
func TestARecoveryStillAppliesWithoutSettledWorkOrAfterAReopening(t *testing.T) {
	t.Parallel()
	for name, setup := range map[string]struct {
		status string
		later  func(*testing.T, *rerunHarness)
	}{
		"a later publication still unsettled": {"closed", func(t *testing.T, h *rerunHarness) { mergedLaterRun(t, h, false) }},
		"closed status alone":                 {"closed", func(*testing.T, *rerunHarness) {}},
		"an unfinished item":                  {"open", func(*testing.T, *rerunHarness) {}},
		"an item reopened after the merge":    {"open", func(t *testing.T, h *rerunHarness) { mergedLaterRun(t, h, true) }},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newSettledFixture(t, setup.status)
			setup.later(t, f.harness)
			carrying := f.carryOut(time.Hour)
			task := theOneOutstanding(t, carrying)
			if _, _, err := carrying.Carry(context.Background(), task); err != nil {
				t.Fatalf("Carry() error = %v", err)
			}
			if len(f.repairer.asked) != 1 {
				t.Fatalf("asked = %v; want the repair handed to its action", f.repairer.asked)
			}
			counters, err := f.harness.runs.Triage().Counters(docketedItem)
			if err != nil {
				t.Fatal(err)
			}
			if _, found := counters.NoLongerApplies(docketedRunID); found {
				t.Fatal("a recovery that still applies was recorded as settled")
			}
		})
	}
}

// The 121.3 and 150 shape: the run a repair was recorded against is itself the
// one that merged. It is judged from its own outcome, not only the item's.
func TestARecoveryOfARunThatItselfMergedIsNotOffered(t *testing.T) {
	t.Parallel()
	f := newSettledFixture(t, "closed")
	mergedLaterRun(t, f.harness, true)
	if _, err := f.harness.runs.Triage().GrantRepair(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRepair, laterRunID), 1, docketedNow.Add(time.Hour),
		runstate.TriageCaps{ReviewRounds: 7, RepairGrants: 2, Reruns: 2, MergeRearms: 2}); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	carrying := f.carryOut(2 * time.Hour)
	if outstanding, err := carrying.Outstanding(); err != nil || len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, %v; want neither run's repair offered", outstanding, err)
	}
	if _, err := carrying.RecordUnattempted(context.Background(), time.Minute, nil); err != nil {
		t.Fatal(err)
	}
	counters, err := f.harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatal(err)
	}
	finding, found := counters.NoLongerApplies(laterRunID)
	if !found || !strings.Contains(finding.Refusal, "itself succeeded") ||
		!strings.Contains(finding.Refusal, "branch was removed and its worktree was removed") {
		t.Fatalf("finding = %#v, %v; want the successful run named as succeeded, with its removed work said as removed", finding, found)
	}
	if len(f.repairer.asked) != 0 {
		t.Fatalf("asked = %v", f.repairer.asked)
	}
}

// The 149 shape: nothing decided about the run, and the harness already settled
// its entry with its item. That is settled work; a closure somebody decided, or
// one with a decision recorded since, is not.
func TestASettledDocketEntryWithNothingDecidedSettlesTheRecovery(t *testing.T) {
	t.Parallel()
	settledAt := docketedNow.Add(time.Hour)
	settled := &triage.Closure{Decision: triage.ItemClosedDecision, Reason: "the tracker holds it as closed", ClosedAt: settledAt}
	recorded := []runstate.State{stoppedState()}
	if work, found := settledWorkOf(docketedRunID, docketedItem, recorded, settled, runstate.TriageCounters{}, settledAt); !found || !strings.Contains(work.account, "already settled its docket entry") {
		t.Fatalf("work = %#v, %v; want the settlement named", work, found)
	}
	repaired := runstate.TriageCounters{Decisions: []runstate.TriageDecision{{Decision: runstate.TriageDecisionRepair, RunID: docketedRunID, DecidedAt: docketedNow}}}
	if _, found := settledWorkOf(docketedRunID, docketedItem, recorded, settled, repaired, settledAt); found {
		t.Fatal("a decision recorded about the run was overridden by the settlement")
	}
	decided := &triage.Closure{Decision: runstate.TriageDecisionRepair, ClosedAt: settledAt}
	if _, found := settledWorkOf(docketedRunID, docketedItem, recorded, decided, runstate.TriageCounters{}, settledAt); found {
		t.Fatal("a closure somebody decided was read as the harness settling the work")
	}
}

// otherRunID is a run of the item the development manager stopped in flight,
// naming the items that do its work instead.
const otherRunID = "run-44444444444444444444444444444444"

// recordSupersession records the development manager stopping a run of the item
// because other items do its work, at the moment given.
func recordSupersession(t *testing.T, harness *rerunHarness, at time.Time) {
	t.Helper()
	stop := triageDecided(runstate.TriageDecisionStop, otherRunID)
	stop.SupersededBy = "yoyodyne-ifd.121.5"
	if _, err := harness.runs.Triage().RecordDecision(context.Background(), docketedItem, stop, at); err != nil {
		t.Fatalf("RecordDecision() error = %v", err)
	}
}

// The 121 shape with a decision recorded: the item closed because other items
// replaced its approach, with no later run of its own. The record of that is the
// development manager's stop naming the item doing the work instead; with it, the
// older run's repair is not offered and is recorded as no longer applying. A
// repair decided after the supersession is somebody deciding past it, and an
// item closed with no such record is closed status alone.
func TestARecoveryOfAnItemWhoseApproachWasReplacedIsNotOffered(t *testing.T) {
	t.Parallel()
	f := newSettledFixture(t, "closed")
	recordSupersession(t, f.harness, docketedNow.Add(30*time.Minute))
	carrying := f.carryOut(time.Hour)
	if outstanding, err := carrying.Outstanding(); err != nil || len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, %v; want the replaced item's repair not offered", outstanding, err)
	}
	if _, err := carrying.RecordUnattempted(context.Background(), time.Minute, nil); err != nil {
		t.Fatal(err)
	}
	counters, err := f.harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatal(err)
	}
	finding, found := counters.NoLongerApplies(docketedRunID)
	if !found || !strings.Contains(finding.Refusal, "yoyodyne-ifd.121.5 does this item's work instead") {
		t.Fatalf("finding = %#v, %v; want the superseding item named", finding, found)
	}
	if len(f.repairer.asked) != 0 {
		t.Fatalf("asked = %v", f.repairer.asked)
	}

	after := newSettledFixture(t, "closed")
	recordSupersession(t, after.harness, docketedNow.Add(-time.Hour))
	carrying = after.carryOut(time.Hour)
	if _, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying)); err != nil {
		t.Fatal(err)
	}
	if len(after.repairer.asked) != 1 {
		t.Fatalf("asked = %v; want a repair decided after the supersession handed to its action", after.repairer.asked)
	}
}

// A continuation the harness fires itself, with nobody having decided it, is
// refused the same way once the item's work is settled, and the explanation is
// noted on the item once, since there is no decision on the triage record to
// write it against.
func TestAHarnessContinuationOfSettledWorkIsRefusedAndNotedOnce(t *testing.T) {
	t.Parallel()
	f := newSettledFixture(t, "closed")
	mergedLaterRun(t, f.harness, true)
	task := CarryOutTask{WorkItemID: docketedItem, RunID: docketedRunID,
		DocketKey: triage.Key(triage.ClassStoppedRun, docketedRunID), Decision: DecisionContinueStall}
	for range 2 {
		carried, outcome, err := f.carryOut(time.Hour).Carry(context.Background(), task)
		if err != nil {
			t.Fatalf("Carry() error = %v", err)
		}
		if carried.Carried || carried.Cause != triage.CarryOutNoLongerApplies || outcome.RunID != "" {
			t.Fatalf("carried = %#v, outcome = %#v; want the continuation refused", carried, outcome)
		}
	}
	if len(f.tracker.notes) != 1 || !strings.Contains(f.tracker.notes[0], "did not continue run "+docketedRunID) ||
		!strings.Contains(f.tracker.notes[0], laterRunID) {
		t.Fatalf("notes = %#v; want one note naming the run and the later merge", f.tracker.notes)
	}
}
