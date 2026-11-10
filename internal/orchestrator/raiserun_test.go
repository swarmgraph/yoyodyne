package orchestrator

// An item a role raised as unmeetable, amended by its owner and run again.
//
// yoyodyne-ifd.437.13 is the shape: a reviewer raised the README rewrite because
// its done-means required a review that had been moved to another item, the
// development manager recorded a repair and then a re-run, and both were refused
// at carry-out because a raise is not a stopped run. The change the raising run
// made was sound and sat on its branch while nothing could pick it up. So the
// sequence is replayed end to end here — the raise through the pipeline, the
// owner's amendment and release on the item, the decision on the durable record,
// and the carry-out through the pipeline again — and what is asserted is where
// the fresh run started: from the raising run's change rather than from nothing.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// raisedChange is what the raising run's developer wrote, and what the re-run
// has to find in its worktree before its own developer writes anything.
const raisedChange = "the rewrite the reviewer found sound\n"

// raisedItem is one item raised as unmeetable through the pipeline, with every
// durable record the re-run of it is carried out against.
type raisedItem struct {
	pipeline Pipeline
	store    *runstate.Store
	docket   *runstate.DocketStore
	intake   *runstate.IntakeHoldStore
	tracker  *orchestratortest.Tracker
	raised   Outcome
	// found is what a later developer attempt found of the raising run's change
	// in its worktree before it wrote anything, and nil where no later attempt
	// has been made or it found nothing.
	found func() *string
}

// raiseAnItem runs one item into a reviewer's raise. The developer's first
// attempt writes the change the raise leaves on its branch; any later attempt
// records what it found of that change and adds its own.
func raiseAnItem(t *testing.T) *raisedItem {
	t.Helper()
	tracker := newOutcomeTracker()
	tracker.Item.Description = "Done means: the README is rewritten to two reviews, the architect's among them."
	var found *string
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		if attempts == 1 {
			return os.WriteFile(filepath.Join(request.WorkingDirectory, "lifted.txt"), []byte(raisedChange), 0o600)
		}
		content, err := os.ReadFile(filepath.Join(request.WorkingDirectory, "lifted.txt"))
		switch {
		case err == nil:
			text := string(content)
			found = &text
		case !errors.Is(err, os.ErrNotExist):
			return err
		}
		return writeFeature(request)
	}, escalateVerdict, approveVerdict)
	pipeline, store, docket := escalatingPipeline(t, tracker, provider)
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	pipeline.Intake = intake

	raised, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The run the re-run starts is a second run of the item, so it is minted a
	// run identifier of its own.
	pipeline.NewRunID = func() (string, error) { return "run-fedcba9876543210fedcba9876543210", nil }
	if raised.ReviewDecision != review.DecisionEscalate || raised.Integration != nil {
		t.Fatalf("decision = %q, integration = %#v, want a raise that integrated nothing", raised.ReviewDecision, raised.Integration)
	}
	if runID, byRaise := runstate.RaisedBy(tracker.Item.Parking.Reason()); !byRaise || runID != raised.RunID {
		t.Fatalf("the item is parked %q, want the parking the raise of %s placed", tracker.Item.Parking, raised.RunID)
	}
	return &raisedItem{
		pipeline: pipeline, store: store, docket: docket, intake: intake, tracker: tracker, raised: raised,
		found: func() *string { return found },
	}
}

// amendAndRelease is the item's owner answering the raise: the done-means is
// rewritten to what can be met, and the parking the raise placed is released.
func (r *raisedItem) amendAndRelease() {
	r.tracker.Item.Description = "Done means: the README is rewritten to the Lead Product Manager's review; the architect's review is its own item."
	r.tracker.Item.Parking = ""
}

// decideRerun is the development manager recording a re-run of the raise, which
// spends the item's re-run budget in the same write.
func (r *raisedItem) decideRerun(t *testing.T) {
	t.Helper()
	caps := TriageCaps(r.pipeline.Config.Execution, r.pipeline.Config.Triage)
	if _, err := r.store.Triage().RecordRerun(context.Background(), r.tracker.Item.ID, triageDecided(runstate.TriageDecisionRerun, r.raised.RunID), time.Now(), caps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
}

// rerunner carries the decision out through the same pipeline the raise ran in,
// over the same records, exactly as the command wires it.
func (r *raisedItem) rerunner() Rerunner {
	return Rerunner{
		Docket:    r.docket,
		Runs:      r.store,
		Intake:    r.intake,
		Reruns:    r.store.Reruns(),
		Decisions: r.store.Triage(),
		Items:     r.tracker,
		Gates:     r.store,
		Capacity:  1,
		Start: func(ctx context.Context, workItemID string, selection runstate.Selection) (Outcome, error) {
			fresh := r.pipeline
			fresh.Selection = selection
			return fresh.Run(ctx, workItemID)
		},
	}
}

// The item's done-means: a test raises an item as unmeetable, amends and
// releases it, records a re-run, and asserts the run starts from the preserved
// branch.
func TestARaiseAmendedAndReleasedIsRunAgainFromItsPreservedChange(t *testing.T) {
	t.Parallel()

	raised := raiseAnItem(t)
	entry := onlyDocketed(t, raised.docket)
	if entry.Class != triage.ClassEscalation || entry.RunID != raised.raised.RunID {
		t.Fatalf("entry = %#v, want the raise on the docket", entry)
	}
	prior, err := raised.store.Read(raised.raised.RunID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	preservedCommit := gitLine(t, raised.pipeline.Repository, "rev-parse", "refs/heads/"+prior.Branch)
	if preservedCommit == prior.BaseCommit {
		t.Fatalf("the raising run's branch %s carries nothing past its base, so there is no change to lift", prior.Branch)
	}

	raised.amendAndRelease()
	raised.decideRerun(t)
	result, err := raised.rerunner().Rerun(context.Background(), RerunRequest{Run: raised.raised.RunID})
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if !result.Started || result.DocketKey != entry.Key {
		t.Fatalf("result = %#v, want the raise's own entry carried out", result)
	}
	// Where the fresh run started: its developer found the raising run's change
	// in the worktree before it wrote a line.
	found := raised.found()
	if found == nil || *found != raisedChange {
		t.Fatalf("the re-run's developer found %v in its worktree, want the raising run's change %q", found, raisedChange)
	}
	fresh, err := raised.store.Read(result.Outcome.RunID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if fresh.Selection == nil || fresh.Selection.Lift == nil ||
		fresh.Selection.Lift.RunID != prior.RunID || fresh.Selection.Lift.Branch != prior.Branch {
		t.Fatalf("selection = %#v, want the lift of %s's branch %s recorded", fresh.Selection, prior.RunID, prior.Branch)
	}
	if fresh.LiftedCommit != preservedCommit {
		t.Fatalf("lifted commit = %q, want the preserved branch's %s", fresh.LiftedCommit, preservedCommit)
	}
	if fresh.Selection.By != runstate.SelectedByDevelopmentManager || !strings.Contains(fresh.Selection.Reason, prior.RunID) {
		t.Fatalf("selection = %#v, want the development manager's re-run of the raise accounting for the run", fresh.Selection)
	}
	// And the change the fresh run integrated is the raising run's carried
	// forward with its own on top, judged whole against the target.
	if result.Outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want the re-run integrated", result.Outcome)
	}
	if got := gitOutput(t, raised.pipeline.Repository, "show", "main:lifted.txt"); got != raisedChange {
		t.Fatalf("main:lifted.txt = %q, want the lifted change integrated", got)
	}
	if got := gitOutput(t, raised.pipeline.Repository, "show", "main:feature.txt"); got != "implemented\n" {
		t.Fatalf("main:feature.txt = %q, want the re-run's own change beside it", got)
	}
}

// A re-run of a raise waits for the owner. The raise said the item could not be
// met as it stood, so carrying the decision out before the owner released the
// parking would run the item the raise was raised to save — and it is refused
// before anything is claimed, so asking again after the release carries out the
// same decision.
func TestARerunOfARaiseWaitsForTheOwnerToReleaseItsParking(t *testing.T) {
	t.Parallel()

	raised := raiseAnItem(t)
	raised.decideRerun(t)
	_, err := raised.rerunner().Rerun(context.Background(), RerunRequest{Run: raised.raised.RunID})
	if err == nil || !strings.Contains(err.Error(), "still parked by that raise") || !strings.Contains(err.Error(), "nothing was claimed") {
		t.Fatalf("Rerun() error = %v, want a refusal naming the raise's parking and saying nothing was spent", err)
	}
	if found := raised.found(); found != nil {
		t.Fatalf("a developer ran while the item was still parked by its raise")
	}
	claimed, err := raised.store.Reruns().Claimed(raised.tracker.Item.ID)
	if err != nil {
		t.Fatalf("Claimed() error = %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed = %#v, want the decision's re-run left unspent", claimed)
	}

	raised.amendAndRelease()
	result, err := raised.rerunner().Rerun(context.Background(), RerunRequest{Run: raised.raised.RunID})
	if err != nil || !result.Started {
		t.Fatalf("Rerun() after the release = %#v, %v; want the same decision carried out", result, err)
	}
}

// A re-run starts from the change its earlier run kept wherever that run's
// branch is still there, whatever ended the run. A re-run of a raise keeps to
// that change; a re-run of any other stopped run falls back to the target where
// the change no longer applies, which is where it always used to start. A run
// whose branch is gone leaves nothing to lift.
func TestARerunStartsFromTheChangeItsEarlierRunKept(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	lift := liftOf(stopped, preservedOf(stopped, triage.Found{BranchThere: true, WorktreeThere: true}))
	if lift == nil || lift.Branch != stopped.Branch || lift.RunID != stopped.RunID || !lift.FallBackToTarget {
		t.Fatalf("lift = %#v, want the stopped run's branch %s, falling back to the target", lift, stopped.Branch)
	}
	if lift := liftOf(stopped, preservedOf(stopped, triage.Found{WorktreeThere: true})); lift != nil {
		t.Fatalf("lift = %#v, want nothing lifted from a stopped run whose branch is gone", lift)
	}
	raised := escalatedState(docketedRunID, docketedItem)
	lift = liftOf(raised, preservedOf(raised, triage.Found{BranchThere: !raised.BranchRemoved, WorktreeThere: !raised.WorktreeRemoved}))
	if lift == nil || lift.Branch != raised.Branch || lift.RunID != raised.RunID || lift.FallBackToTarget {
		t.Fatalf("lift = %#v, want the raise's branch %s with no fall-back", lift, raised.Branch)
	}
	raised.BranchRemoved = true
	if lift := liftOf(raised, preservedOf(raised, triage.Found{BranchThere: !raised.BranchRemoved, WorktreeThere: !raised.WorktreeRemoved})); lift != nil {
		t.Fatalf("lift = %#v, want nothing lifted from a branch the record says is gone", lift)
	}
}

// refusingLifter is a worktree manager whose every lift fails with one error.
type refusingLifter struct {
	WorktreeManager
	err error
}

func (l refusingLifter) LiftChange(context.Context, gitworktree.Worktree, string) (gitworktree.Lift, error) {
	return gitworktree.Lift{}, l.err
}

// A lift that may fall back starts the run from the target where the kept
// change no longer applies or its branch has gone, and records no lifted
// commit; a lift that may not ends the run, and any other failure ends either.
func TestALiftThatMayFallBackStartsFromTheTargetWhenTheChangeNoLongerApplies(t *testing.T) {
	t.Parallel()

	other := errors.New("the repository could not be read")
	for _, test := range []struct {
		name     string
		fallBack bool
		err      error
		ends     bool
	}{
		{name: "does-not-apply-falls-back", fallBack: true, err: fmt.Errorf("%w: conflict in README.txt", gitworktree.ErrLiftDoesNotApply)},
		{name: "branch-gone-falls-back", fallBack: true, err: fmt.Errorf("%w: yoyodyne/xdj/01234567", gitworktree.ErrLiftBranchGone)},
		{name: "other-failure-ends", fallBack: true, err: other, ends: true},
		{name: "raise-does-not-apply-ends", err: fmt.Errorf("%w: conflict in README.txt", gitworktree.ErrLiftDoesNotApply), ends: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			run := &activeRun{
				pipeline: Pipeline{Worktrees: refusingLifter{err: test.err}},
				state: runstate.State{Selection: &runstate.Selection{
					Lift: &runstate.Lift{RunID: docketedRunID, Branch: "yoyodyne/xdj/01234567", FallBackToTarget: test.fallBack},
				}},
			}
			err := run.liftPreserved(context.Background())
			if test.ends {
				if !errors.Is(err, test.err) {
					t.Fatalf("liftPreserved() error = %v, want the run ended on %v", err, test.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("liftPreserved() error = %v, want the run started from the target", err)
			}
			if run.state.LiftedCommit != "" {
				t.Fatalf("lifted commit = %q, want none recorded", run.state.LiftedCommit)
			}
		})
	}
}

// The raise's own record is what the carry-out pass offers the decision under,
// so the claim a re-run takes is against the raise's key and the pass does not
// offer the same decision again once it is claimed.
func TestTheCarryOutPassOffersARerunOfARaiseUnderTheRaisesEntry(t *testing.T) {
	t.Parallel()

	raised := raiseAnItem(t)
	raised.amendAndRelease()
	raised.decideRerun(t)
	carry := CarryOut{
		Docket:    raised.docket,
		Runs:      raised.store,
		Decisions: raised.store.Triage(),
		Reruns:    raised.store.Reruns(),
		Clock:     docketClockAt{at: time.Now()},
	}
	tasks, err := carry.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	want := triage.Key(triage.ClassEscalation, raised.raised.RunID)
	if len(tasks) != 1 || tasks[0].DocketKey != want || tasks[0].Decision != runstate.TriageDecisionRerun {
		t.Fatalf("tasks = %#v, want the re-run offered under the raise's entry %s", tasks, want)
	}
	if _, err := raised.rerunner().Rerun(context.Background(), RerunRequest{Run: raised.raised.RunID}); err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	tasks, err = carry.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %#v, want nothing offered once the re-run is claimed", tasks)
	}
}
