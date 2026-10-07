package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// run-838ffc48 on yoyodyne-ifd.432.10 is the case: a run that ended failed on a
// tracker read that timed out, its record saying its artifacts were removed, and
// its branch holding the approved change. The development manager crossed a cap
// on the reading that it had preserved nothing. So this stops a run whose flags
// say both artifacts are gone while its branch is standing with a commit on it,
// and asks every surface that says what a stopped run preserved: each has to
// name the branch as checked and there.
func TestEverySurfaceReportsTheBranchARunsFlagsSayIsRemoved(t *testing.T) {
	t.Parallel()

	fixture := newFlaggedFixture(t)
	ctx := context.Background()
	state := fixture.flaggedRun(t, "yoyodyne-flagged.1", 1, runstate.StatusFailed)
	branchThere := "checked and there at "

	// The hold the pull reads.
	held, err := readmodel.HeldForAPerson(ctx, fixture.store, fixture.store.Triage(), fixture.worktrees)
	if err != nil {
		t.Fatalf("HeldForAPerson() error = %v", err)
	}
	reason, isHeld := held.Reason(state.WorkItemID)
	if !isHeld || !strings.Contains(reason, "branch checked and there") {
		t.Fatalf("hold = %q, held = %t, want the item held over a branch checked and there", reason, isHeld)
	}

	// The claim audit keeps the claim over it rather than releasing it as work
	// nothing is holding.
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: state.WorkItemID, Title: "Flagged", Status: "in_progress"}}
	auditor := ClaimAuditor{
		Tracker:   tracker,
		Runs:      fixture.store,
		Releases:  fixture.releases,
		ProductID: "yoyodyne",
		Remains:   fixture.worktrees,
		Clock:     fixedClock{at: fixture.now},
	}
	sweep, err := auditor.Audit(ctx, []beads.WorkItem{tracker.Item})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(sweep.Released) != 0 || tracker.Released {
		t.Fatalf("sweep = %#v, want the claim over a standing branch kept", sweep)
	}

	// The docket entry, as it is written at the death and as the development
	// manager's context reads it.
	docket := &memoryDocket{}
	docketer := Docketer{
		Docket:    docket,
		Runs:      fixture.store,
		Decisions: fixture.store.Triage(),
		Reruns:    fixture.store.Reruns(),
		Caps:      docketedCaps,
		Triage:    docketedTriage,
		Remains:   fixture.worktrees,
		Clock:     fixedClock{at: fixture.now},
	}
	created, err := docketer.RecordStoppedRun(state)
	if err != nil || !created {
		t.Fatalf("RecordStoppedRun() = %t, %v, want the death that held its change docketed", created, err)
	}
	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 {
		t.Fatalf("docket = %#v, want the one stoppage", built.Entries)
	}
	for _, entry := range append([]string{docket.entries[0].Render()}, built.Entries[0].Render()) {
		if !strings.Contains(entry, "Branch ("+branchThere) || !strings.Contains(entry, state.Branch) {
			t.Fatalf("docket entry does not report the branch as checked and there:\n%s", entry)
		}
		if !strings.Contains(entry, "Worktree (checked and NOT there at ") {
			t.Fatalf("docket entry does not report the checkout as looked for and gone:\n%s", entry)
		}
	}

	// `yoyo status`, which says it from the same look.
	history, err := fixture.store.History(runstate.RunQuery{WorkItemID: state.WorkItemID})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	readmodel.LookForSummaries(ctx, fixture.worktrees, fixture.store, history.Runs)
	if len(history.Runs) != 1 || history.Runs[0].Found == nil {
		t.Fatalf("history = %#v, want the stopped run looked for", history.Runs)
	}
	listed := history.Runs[0]
	if got := listed.DescribeRemains(); got != "work preserved, checked" {
		t.Fatalf("DescribeRemains() = %q, want the branch counted as preserved", got)
	}
	if !strings.HasPrefix(listed.Found.BranchState(), branchThere) {
		t.Fatalf("status branch = %q, want it checked and there", listed.Found.BranchState())
	}
}

func TestPreservationGuardsDoNotDeclareAnUnreadableRepositoryEmpty(t *testing.T) {
	t.Parallel()
	state := approvedStoppedState()
	state.Blocker = ""
	found := triage.Found{Branch: state.Branch, WorktreePath: state.WorktreePath, Unknown: true, Unchecked: "repository could not be read"}
	if err := stoppageIsOver(state, found); err != nil {
		t.Fatalf("stoppageIsOver() = %v, want an unknown change held", err)
	}
	if err := resumableStop(state, found); err == nil || !strings.Contains(err.Error(), found.Unchecked) {
		t.Fatalf("resumableStop() = %v, want the failed look named", err)
	}
	if found.DescribeRemains() != "work possibly preserved, not checked" {
		t.Fatalf("remains = %s", found.DescribeRemains())
	}
}

// The other side of the same look. An approved change the environment stopped
// short of its promotion, whose branch and checkout are both gone from the
// repository, has nothing left for `yoyo triage resume` to finish — the resume
// refuses once the branch is gone. The claim audit gives its claim back for that
// reason, so the hold the pull reads has to let the item go as well rather than
// hold it out of the pull naming a verb that cannot act.
func TestAnIntegrationStopWhoseChangeIsGoneIsNeitherHeldNorKeptClaimed(t *testing.T) {
	t.Parallel()

	fixture := newFlaggedFixture(t)
	ctx := context.Background()
	state := fixture.flaggedRun(t, "yoyodyne-flagged.4", 4, runstate.StatusFailed)
	runPipelineGit(t, fixture.repository, "branch", "-D", state.Branch)
	state.ReviewDecision = runstate.ReviewApprove
	state.ReviewSessionID = "f4c1a0de-review"
	state.IntegrationStop = &runstate.IntegrationStop{
		Cause:      runstate.CauseTransportFailure,
		Detail:     state.Failure,
		Phase:      runstate.PhaseIntegrating,
		RecordedAt: state.UpdatedAt,
	}
	if err := fixture.store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	held, err := readmodel.HeldForAPerson(ctx, fixture.store, fixture.store.Triage(), fixture.worktrees)
	if err != nil {
		t.Fatalf("HeldForAPerson() error = %v", err)
	}
	reason, isHeld := held.Reason(state.WorkItemID)

	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: state.WorkItemID, Title: "Stopped and swept", Status: "in_progress"}}
	auditor := ClaimAuditor{
		Tracker:   tracker,
		Runs:      fixture.store,
		Releases:  fixture.releases,
		ProductID: "yoyodyne",
		Remains:   fixture.worktrees,
		Clock:     fixedClock{at: fixture.now},
	}
	sweep, err := auditor.Audit(ctx, []beads.WorkItem{tracker.Item})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	released := len(sweep.Released) == 1
	if isHeld == released {
		t.Fatalf("the hold says held = %t (%q) and the claim audit says released = %t: one run, two answers", isHeld, reason, released)
	}
	if isHeld {
		t.Fatalf("the item is held for %q, want nothing holding a stop with nothing left to resume", reason)
	}
	if strings.Contains(tracker.ReleaseReason, "triage resume") {
		t.Fatalf("release note = %q, want no resume named for a change that is gone", tracker.ReleaseReason)
	}
}

// A release the audit does make says what the run left, and where the run's
// branch is standing it says so: the release that said only "nothing was
// working on it" is what was read as run-838ffc48 having preserved nothing.
func TestAReleaseSaysTheBranchItsRunLeftStanding(t *testing.T) {
	t.Parallel()

	fixture := newFlaggedFixture(t)
	// A cancelled run hands nobody a decision, so its claim is given back — with
	// its branch still standing, which is exactly what the note has to say.
	state := fixture.flaggedRun(t, "yoyodyne-flagged.2", 2, runstate.StatusCancelled)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: state.WorkItemID, Title: "Flagged", Status: "in_progress"}}
	auditor := ClaimAuditor{
		Tracker:   tracker,
		Runs:      fixture.store,
		Releases:  fixture.releases,
		ProductID: "yoyodyne",
		Remains:   fixture.worktrees,
		Clock:     fixedClock{at: fixture.now},
	}
	sweep, err := auditor.Audit(context.Background(), []beads.WorkItem{tracker.Item})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(sweep.Released) != 1 {
		t.Fatalf("sweep = %#v, want the claim of a cancelled run given back", sweep)
	}
	for _, want := range []string{"What the run left: branch " + state.Branch + " (checked and there at", "That change is still there"} {
		if !strings.Contains(tracker.ReleaseReason, want) {
			t.Fatalf("release note = %q, want it to contain %q", tracker.ReleaseReason, want)
		}
	}
	recorded := sweep.Released[0].Found
	if recorded == nil || !recorded.Looked() || !recorded.BranchThere {
		t.Fatalf("released record found = %#v, want the look written down", recorded)
	}
}

// The record run-838ffc48 left is corrected by the sweep rather than by hand: a
// release written before the audit looked names no branch, and the sweep that
// finds that branch standing tells the item once.
func TestTheSweepCorrectsAReleaseThatLeftOutAStandingBranch(t *testing.T) {
	t.Parallel()

	fixture := newFlaggedFixture(t)
	state := fixture.flaggedRun(t, "yoyodyne-flagged.3", 3, runstate.StatusFailed)
	released := runstate.ReleasedClaim{
		SchemaVersion: runstate.ReleasedClaimSchemaVersion,
		ProductID:     "yoyodyne",
		WorkItemID:    state.WorkItemID,
		RunID:         state.RunID,
		Because:       "its run ended failed and the claim outlived it",
		ReleasedAt:    fixture.now.Add(-time.Hour),
	}
	if err := fixture.releases.Append(released); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: state.WorkItemID, Title: "Flagged", Status: "open"}}
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: fixture.worktrees,
		Store:     fixture.store,
		Releases:  fixture.releases,
		Clock:     fixedClock{at: fixture.now},
	}
	convergence, err := reconciler.Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge() error = %v", err)
	}
	var branch BranchSweep
	for _, swept := range convergence.Branches {
		if swept.RunID == state.RunID {
			branch = swept
		}
	}
	if !branch.ReleaseCorrected || branch.Removed || branch.ItemProblem != "" {
		t.Fatalf("branch sweep = %#v, want the branch kept and its item corrected", branch)
	}
	for _, want := range []string{"Correction:", state.RunID, "Branch: " + state.Branch + " (checked and there at"} {
		if !strings.Contains(tracker.Notes, want) {
			t.Fatalf("item notes = %q, want them to contain %q", tracker.Notes, want)
		}
	}
	corrected, err := fixture.store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if corrected.ReleaseCorrectedAt == nil {
		t.Fatalf("record = %#v, want the correction written down so it is made once", corrected)
	}
	notes := len(tracker.NoteRecords)
	if _, err := reconciler.Converge(context.Background()); err != nil {
		t.Fatalf("second Converge() error = %v", err)
	}
	if len(tracker.NoteRecords) != notes {
		t.Fatalf("notes = %q, want the correction made once", tracker.NoteRecords)
	}
}

type flaggedFixture struct {
	repository string
	worktrees  *gitworktree.Manager
	store      *runstate.Store
	releases   *runstate.ClaimStore
	now        time.Time
}

func newFlaggedFixture(t *testing.T) flaggedFixture {
	t.Helper()
	repository, worktreeRoot, store := restartableFixture(t)
	releases, err := runstate.NewClaimStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewClaimStore() error = %v", err)
	}
	return flaggedFixture{
		repository: repository,
		worktrees:  newSweepManager(t, repository, worktreeRoot),
		store:      store,
		releases:   releases,
		now:        time.Date(2026, 9, 23, 5, 0, 0, 0, time.UTC),
	}
}

// flaggedRun records a run whose branch holds a commit the target does not carry
// and whose checkout is gone, with a record that says both were removed — the
// disagreement between the flags and the repository that run-838ffc48 had.
func (f flaggedFixture) flaggedRun(t *testing.T, workItemID string, index int, status runstate.Status) runstate.State {
	t.Helper()
	runID := "run-" + strings.Repeat("0", 31) + string(rune('0'+index))
	worktree, err := f.worktrees.Create(context.Background(), gitworktree.CreateRequest{
		RunID:        runID,
		WorkItemID:   workItemID,
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree.Path, "approved.txt"), []byte("the approved change\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, worktree.Path, "add", ".")
	runPipelineGit(t, worktree.Path, "commit", "-m", "the approved change")
	runPipelineGit(t, f.repository, "worktree", "remove", "--force", worktree.Path)

	ended := f.now.Add(-2 * time.Hour)
	state := runstate.State{
		SchemaVersion:      runstate.StateSchemaVersion,
		RunID:              runID,
		ProductID:          "yoyodyne",
		RepositoryID:       "repository",
		WorkItemID:         workItemID,
		Backend:            domain.BackendClaudeCode,
		Status:             status,
		Phase:              runstate.PhaseIntegrating,
		StartedAt:          ended.Add(-time.Hour),
		UpdatedAt:          ended,
		CompletedAt:        &ended,
		WorkItemClaimedAt:  &ended,
		WorktreePath:       worktree.Path,
		Branch:             worktree.Branch,
		BaseCommit:         worktree.BaseCommit,
		TargetBranch:       worktree.TargetBranch,
		Failure:            "read what " + workItemID + " waits on: bd show failed with status timed_out and exit code -1",
		WorktreeRemoved:    true,
		BranchRemoved:      true,
		ArtifactsRetiredBy: "run-" + strings.Repeat("f", 32),
	}
	if err := f.store.Create(state); err != nil {
		t.Fatalf("Create() run error = %v", err)
	}
	return state
}

// The same repository check reaches the guards and the cost listing. The flags
// disagree in both directions: first a surviving branch marked removed, then
// a deleted branch marked kept.
func TestPreservationGuardsAndPricesFollowTheRepositoryRatherThanRemovalFlags(t *testing.T) {
	t.Parallel()
	fixture := newFlaggedFixture(t)
	state := fixture.flaggedRun(t, "yoyodyne-flagged.8", 8, runstate.StatusFailed)
	ctx := context.Background()
	for _, there := range []bool{true, false} {
		if !there {
			runPipelineGit(t, fixture.repository, "branch", "-D", state.Branch)
			state.BranchRemoved, state.WorktreeRemoved = false, false
			state.ArtifactsRetiredBy = ""
			if err := fixture.store.Save(state); err != nil {
				t.Fatal(err)
			}
		}
		found := readmodel.LookFor(ctx, fixture.worktrees, state)
		if !found.Looked() || found.BranchThere != there || found.WorktreeThere {
			t.Fatalf("found = %#v, want branch present %t and checkout gone", found, there)
		}
		if err := stoppageIsOver(state, found); (err == nil) != there {
			t.Fatalf("stoppageIsOver() = %v, want admitted %t", err, there)
		} else if err != nil && (!strings.Contains(err.Error(), state.Branch) || !strings.Contains(err.Error(), state.WorktreePath)) {
			t.Fatalf("refusal does not name what was checked: %v", err)
		}
		// A re-run starts from the target branch, so it is admitted whether or not
		// the change survived.
		if err := rerunnable(state); err != nil {
			t.Fatalf("rerunnable() = %v, want an ended run admitted to a re-run", err)
		}
		if kept := preservedOf(state, found); (kept.Branch != "") != there {
			t.Fatalf("preserved = %#v", kept)
		}
		approved := approvedStoppedState()
		approved.Branch, approved.WorktreePath = state.Branch, state.WorktreePath
		approved.BranchRemoved, approved.WorktreeRemoved = state.BranchRemoved, state.WorktreeRemoved
		if err := resumableStop(approved, found); (err == nil) != there {
			t.Fatalf("resumableStop() = %v", err)
		}
		price, err := fixture.store.Price(state.WorkItemID)
		if err != nil {
			t.Fatal(err)
		}
		prices := []runstate.ItemPrice{price}
		readmodel.LookForPrices(ctx, fixture.worktrees, fixture.store, prices)
		want := "work removed, checked"
		if there {
			want = "work preserved, checked"
		}
		if len(prices[0].Runs) != 1 || prices[0].Runs[0].Remains != want || prices[0].Runs[0].Found == nil {
			t.Fatalf("price = %#v, want %s", prices, want)
		}
	}
}
