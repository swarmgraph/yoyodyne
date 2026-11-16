package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// resumeHarness is the durable state a resumption acts on, held together so a
// test can drive one without rebuilding four stores.
type resumeHarness struct {
	// docket is the real store rather than the memory one, because the closure a
	// resumption writes carries a decision word no role's vocabulary has, and
	// what accepts it is the store's own validation.
	docket    *runstate.DocketStore
	runs      *runstate.Store
	intake    *runstate.IntakeHoldStore
	tracker   *orchestratortest.Tracker
	ownership *orchestratortest.ResumeOwnership
	started   []continuedRun
	outcome   Outcome
	failure   error
	capacity  int
	// recorded is the run as it was created, which is what a refusal has to
	// leave.
	recorded runstate.State
	// runsRoot is the state root the stores are opened over, so a test can open
	// them again as a session started afterwards would.
	runsRoot string
}

func (h *resumeHarness) resumer() IntegrationResumer {
	return IntegrationResumer{
		Docket:    h.docket,
		Runs:      h.runs,
		Intake:    h.intake,
		Items:     h.tracker,
		Worktrees: h.ownership,
		Capacity:  h.capacity,
		Clock:     docketClock{},
		Start: func(_ context.Context, workItemID, runID string) (Outcome, error) {
			h.started = append(h.started, continuedRun{workItemID: workItemID, runID: runID})
			return h.outcome, h.failure
		},
	}
}

// approvedStoppedState is the run this action is about: one whose change the
// reviewer approved, whose promotion the environment then refused, with the
// branch, the worktree, and the session it stopped in all preserved. It is the
// shape yoyodyne-ifd.309's rerun stopped in on 2026-09-18, twice.
func approvedStoppedState() runstate.State {
	completed := docketedNow.Add(-time.Hour)
	return runstate.State{
		SchemaVersion:     runstate.StateSchemaVersion,
		RunID:             docketedRunID,
		ProductID:         "yoyodyne",
		RepositoryID:      "yoyodyne",
		WorkItemID:        docketedItem,
		WorkItemTitle:     docketedTitle,
		Backend:           "claude-code",
		Status:            runstate.StatusFailed,
		Phase:             runstate.PhaseIntegrating,
		StartedAt:         completed.Add(-time.Hour),
		UpdatedAt:         completed,
		CompletedAt:       &completed,
		WorktreePath:      "/state/worktrees/task",
		Branch:            "yoyodyne/task/abc",
		BaseCommit:        strings.Repeat("a", 40),
		HarnessCommit:     strings.Repeat("c", 40),
		TargetBranch:      "main",
		ProviderSessionID: "developer-session",
		ProviderModel:     "opus",
		ReviewSessionID:   "reviewer-session",
		ReviewModel:       "opus",
		ReviewDecision:    runstate.ReviewApprove,
		ReviewApproves:    "implementation",
		ReviewSummary:     "the change matches the acceptance criteria",
		ReviewRounds:      1,
		RepairAttempts:    1,
		Failure:           "integrate approved change: primary checkout is not ready for integration: the primary checkout is not as the harness left it: primary repository has uncommitted changes: AGENTS.md",
		Environmental:     &runstate.EnvironmentalRefusal{Cause: runstate.CauseDirtyPrimary, RecordedAt: completed, Settled: true},
		IntegrationStop: &runstate.IntegrationStop{
			Cause:      runstate.CauseDirtyPrimary,
			Detail:     "integrate approved change: primary checkout is not ready for integration",
			Phase:      runstate.PhaseIntegrating,
			RecordedAt: completed,
		},
	}
}

func newResumeHarness(t *testing.T, state runstate.State) *resumeHarness {
	t.Helper()
	root := t.TempDir()
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	docket, err := runstate.NewDocketStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewDocketStore() error = %v", err)
	}
	// The docket and the resumption share the one fixed clock, as the two share
	// the wall clock in the harness: the store refuses a closure made before the
	// stoppage it settles was docketed.
	docketer := docketerOverStore(docket, runs, docketConfig())
	docketer.Clock = docketClock{}
	if _, err := docketer.RecordStoppedRun(state); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	return &resumeHarness{
		docket:  docket,
		runs:    runs,
		intake:  intake,
		tracker: &orchestratortest.Tracker{Item: beads.WorkItem{ID: state.WorkItemID, Title: state.WorkItemTitle, Status: "in_progress"}},
		// The checkout is clean again, which is the ordinary case: the operator
		// committed the edit that stopped the run.
		ownership: &orchestratortest.ResumeOwnership{},
		capacity:  2,
		outcome:   Outcome{RunID: state.RunID, WorkItemID: state.WorkItemID, Status: runstate.StatusSucceeded},
		recorded:  state,
		runsRoot:  root,
	}
}

func (h *resumeHarness) reload(t *testing.T) runstate.State {
	t.Helper()
	state, err := h.runs.Load(docketedRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return state
}

func resumeRequest() IntegrationResumeRequest {
	return IntegrationResumeRequest{Run: docketedRunID}
}

// docketConfig is the configuration the harness's docket is built against here:
// the triage thresholds every other docket in these tests uses.
func docketConfig() config.Config {
	return config.Config{Execution: config.Execution{IntegrationRetriesBeforeReconciliation: 1}, Triage: docketedTriage}
}

// closure is the decision the docket now carries about the stopped run, and
// whether it carries one.
func (h *resumeHarness) closure(t *testing.T) (triage.Closure, bool) {
	t.Helper()
	entries, err := h.docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, entry := range entries {
		if entry.Key == triage.Key(triage.ClassStoppedRun, docketedRunID) && entry.Closed != nil {
			return *entry.Closed, true
		}
	}
	return triage.Closure{}, false
}

// assertNothingWritten is what every refusal has to leave: the run exactly as it
// stopped, the item untouched, and nothing dispatched.
func (h *resumeHarness) assertNothingWritten(t *testing.T) {
	t.Helper()
	state := h.reload(t)
	if state.Status != runstate.StatusFailed || len(state.IntegrationResumptions) != len(h.recorded.IntegrationResumptions) || (state.IntegrationStop == nil) != (h.recorded.IntegrationStop == nil) {
		t.Fatalf("a refused resumption changed the run: %s/%s, resumptions %#v, stop %#v", state.Status, state.Phase, state.IntegrationResumptions, state.IntegrationStop)
	}
	if len(h.tracker.Calls) != 0 {
		t.Fatalf("a refused resumption wrote to the item: %v", h.tracker.Calls)
	}
	if len(h.started) != 0 {
		t.Fatalf("a refused resumption dispatched something: %#v", h.started)
	}
	if _, closed := h.closure(t); closed {
		t.Fatal("a refused resumption closed the docket entry")
	}
	if len(h.ownership.Restored) != 0 {
		t.Fatalf("a refused resumption restored a worktree: %#v", h.ownership.Restored)
	}
}

// What the action is for: the same run goes on at the promotion it stopped
// short of, with its approval standing, and nothing about it is counted as an
// attempt or a round.
func TestAResumptionMakesTheStoppedRunLiveAtItsPromotionChargingNothing(t *testing.T) {
	t.Parallel()

	harness := newResumeHarness(t, approvedStoppedState())
	result, err := harness.resumer().Resume(context.Background(), resumeRequest())
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if !result.Resumed || len(harness.started) != 1 || harness.started[0] != (continuedRun{workItemID: docketedItem, runID: docketedRunID}) {
		t.Fatalf("started = %#v, resumed = %t, want the docketed run continued once", harness.started, result.Resumed)
	}
	if result.RecordProblem != "" {
		t.Fatalf("record problem = %q, want every record the resumption makes written", result.RecordProblem)
	}
	if result.Cause != runstate.CauseDirtyPrimary || !strings.Contains(result.Stopped, "dirty-primary") {
		t.Fatalf("result = %#v, want the stop it supersedes named", result)
	}
	state := harness.reload(t)
	if state.Status != runstate.StatusRunning || state.Phase != runstate.PhaseIntegrating || state.CompletedAt != nil {
		t.Fatalf("resumed run = %s/%s completed %v, want it running at the integrating phase", state.Status, state.Phase, state.CompletedAt)
	}
	if !state.ResumingIntegration() {
		t.Fatalf("resumed run does not read as resuming its integration: %#v", state)
	}
	// The approval stands exactly as the reviewer left it.
	if state.ReviewDecision != runstate.ReviewApprove || state.ReviewSessionID != "reviewer-session" || state.ReviewApproves != "implementation" {
		t.Fatalf("resumed run lost its approval: decision %q session %q approves %q", state.ReviewDecision, state.ReviewSessionID, state.ReviewApproves)
	}
	// Nothing was counted: not an attempt, not a round, not a grant.
	if state.RepairAttempts != 1 || state.ReviewRounds != 1 || len(state.RepairContinuations) != 0 {
		t.Fatalf("resumed run was charged: attempts %d rounds %d continuations %#v", state.RepairAttempts, state.ReviewRounds, state.RepairContinuations)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 0 || counters.RepairGrants != 0 || counters.Reruns != 0 || counters.CommittedRounds != 0 {
		t.Fatalf("the item's triage record was spent by a resumption: %#v", counters)
	}
	// The resumption is a continuation on the record, carrying what it superseded.
	if len(state.IntegrationResumptions) != 1 {
		t.Fatalf("resumptions = %#v, want the one this made", state.IntegrationResumptions)
	}
	resumed := state.IntegrationResumptions[0]
	if resumed.Cause != runstate.CauseDirtyPrimary || !strings.Contains(resumed.SupersededFailure, "uncommitted changes: AGENTS.md") {
		t.Fatalf("resumption = %#v, want the stop's cause and failure carried", resumed)
	}
	if !strings.Contains(resumed.Reason, "No review round, repair grant, or re-run was spent") {
		t.Fatalf("reason = %q, want it to say what the resumption cost", resumed.Reason)
	}
	if state.IntegrationStop != nil || state.Failure != "" || state.Blocker != "" || state.Environmental != nil {
		t.Fatalf("resumed run still carries its stop: stop %#v failure %q blocker %q environmental %#v", state.IntegrationStop, state.Failure, state.Blocker, state.Environmental)
	}
	// The refusal the round settled is carried onto the resumption rather than
	// lost with the clearing: it is the account of what that round cost the item.
	if resumed.SupersededRefusal == nil || resumed.SupersededRefusal.Cause != runstate.CauseDirtyPrimary || !resumed.SupersededRefusal.Settled {
		t.Fatalf("resumption = %#v, want the settled environmental refusal carried onto it", resumed.SupersededRefusal)
	}
	// The item was told and put back, and the docket entry closed in the
	// harness's name rather than left for the development manager to decide.
	if !strings.Contains(harness.tracker.Notes, "Resumed: the integration of run "+docketedRunID) || !harness.tracker.Claimed {
		t.Fatalf("item notes = %q, claimed = %t; want the resumption recorded and the item put back", harness.tracker.Notes, harness.tracker.Claimed)
	}
	closure, closed := harness.closure(t)
	if !closed || closure.Decision != resumedDocketDecision || !strings.Contains(closure.DecidedBy, "the harness") {
		t.Fatalf("docket closure = %#v, want the stoppage settled as resumed by the harness, through the real store", closure)
	}
	if len(harness.ownership.Restored) != 0 {
		t.Fatalf("a worktree the sweep never retired was restored: %#v", harness.ownership.Restored)
	}
}

// The pipeline's own reading of a resumed run: it is picked up at its promotion
// and nowhere else, and a run at that phase nothing resumed on purpose is not.
func TestOnlyARunResumedOnPurposeIsPickedUpAtItsPromotion(t *testing.T) {
	t.Parallel()

	resumed := approvedStoppedState()
	resumed.Status = runstate.StatusRunning
	resumed.CompletedAt = nil
	resumed.IntegrationStop = nil
	resumed.Failure = ""
	resumed.Environmental = nil
	resumed.IntegrationResumptions = []runstate.IntegrationResumption{{Cause: runstate.CauseDirtyPrimary, Reason: "resumed", ResumedAt: docketedNow}}
	if !resumableIntegration(resumed) {
		t.Fatalf("a resumed run at its promotion is not picked up: %#v", resumed)
	}
	died := resumed
	died.IntegrationResumptions = nil
	if resumableIntegration(died) {
		t.Fatal("a run a process died in at the integrating phase was picked up as a resumption")
	}
	unapproved := resumed
	unapproved.ReviewDecision = ""
	unapproved.ReviewSessionID = ""
	if resumableIntegration(unapproved) {
		t.Fatal("a run with no approval standing was picked up at its promotion")
	}
}

// A replay the harness killed stops an approved change for the environment, in
// the words the retry path wraps it in, and a replay that conflicted still does
// not: the conflict is a person's, and nothing about it is resumable.
func TestAKilledReplayIsAnIntegrationStopAndAConflictIsNot(t *testing.T) {
	t.Parallel()

	killed := fmt.Errorf("replay the change onto the moved target branch: %w",
		fmt.Errorf("%w: replay yoyodyne/task onto main at abc123 timed out", gitworktree.ErrReplayKilled))
	if cause, environmental := integrationStopCauseOf(killed); !environmental || cause != runstate.CauseReplayKilled {
		t.Fatalf("integrationStopCauseOf(killed replay) = %q, %v; want %q", cause, environmental, runstate.CauseReplayKilled)
	}
	if !runstate.CauseReplayKilled.Valid() {
		t.Fatalf("%q is not a cause the record accepts", runstate.CauseReplayKilled)
	}
	conflicted := fmt.Errorf("%w: replay yoyodyne/task onto main at abc123 failed with exit code 1: CONFLICT (content)", gitworktree.ErrRebaseConflict)
	if cause, environmental := integrationStopCauseOf(conflicted); environmental {
		t.Fatalf("integrationStopCauseOf(conflict) = %q; a conflict must never be an environmental stop", cause)
	}
	// Nor whatever failed while the conflict was being recorded, joined to it:
	// the tracker write timing out, or the checkout refusing a save.
	for _, recording := range []error{
		errors.New("record the replay conflict as a blocker: bd update failed with status cancelled and exit code -1: signal: killed"),
		fmt.Errorf("save the replayed change: %w", gitworktree.ErrPrimaryNotReady),
	} {
		if cause, environmental := integrationStopCauseOf(errors.Join(conflicted, recording)); environmental {
			t.Fatalf("integrationStopCauseOf(conflict joined to %q) = %q; a failure recording a conflict must never reclassify it", recording, cause)
		}
	}
}

// The two stops three approved changes each spent a re-run on — a target the
// harness would not catch up, and a remote that refused the SSH key — are
// integration stops, each named by its own sentinel. The SSH refusal is named
// as what it was even though SSH closes the connection after it, which read on
// its own is the transport class.
func TestADivergedTargetAndARefusedKeyAreIntegrationStops(t *testing.T) {
	t.Parallel()

	diverged := fmt.Errorf("%w: main cannot be brought onto origin before promoting: main on origin is at abc, which does not contain the local main at def", ErrDivergedTarget)
	if cause, environmental := integrationStopCauseOf(diverged); !environmental || cause != runstate.CauseDivergedTarget {
		t.Fatalf("integrationStopCauseOf(diverged target) = %q, %v; want %q", cause, environmental, runstate.CauseDivergedTarget)
	}
	refused := fmt.Errorf("republish the replayed developer branch: %w",
		fmt.Errorf("%w: git push exited 128: git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.\nConnection closed by 140.82.112.3 port 22", gitworktree.ErrRemoteAuthRefused))
	if cause, environmental := integrationStopCauseOf(refused); !environmental || cause != runstate.CauseRemoteAuthRefused {
		t.Fatalf("integrationStopCauseOf(refused key) = %q, %v; want %q", cause, environmental, runstate.CauseRemoteAuthRefused)
	}
	for _, cause := range []runstate.EnvironmentalCause{runstate.CauseDivergedTarget, runstate.CauseRemoteAuthRefused} {
		if !cause.Valid() || cause.ClearedBy() == "" || cause.Title() == string(cause) {
			t.Fatalf("%q: valid %t, cleared by %q, title %q; want a recorded cause that says what clears it", cause, cause.Valid(), cause.ClearedBy(), cause.Title())
		}
	}
}

// The fourth cause the item names: the worktree retired under the run. The
// change is approved and stopped for the checkout; the convergence sweep then
// takes the worktree, as it did to yoyodyne-ifd.309's on 2026-09-18; the resume
// puts the worktree back from the branch at the reviewed commit and promotes
// it, charging nothing — where before this the only way on was a re-run.
func TestAnApprovedChangeWhoseWorktreeWasRetiredIsRestoredAndResumed(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	dirtying := filepath.Join(repository, "AGENTS.md")
	serve := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			if err := os.WriteFile(dirtying, []byte("an edit nobody committed\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
		}
		return serve(request)
	}
	build := func() Pipeline {
		return publishing(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"test -f feature.txt"}), provider), forge)
	}
	outcome, err := build().Run(context.Background(), tracker.Item.ID)
	if err == nil || !errors.Is(err, gitworktree.ErrPrimaryNotReady) {
		t.Fatalf("Run() error = %v, want the promotion refused for the dirty checkout", err)
	}
	if err := os.Remove(dirtying); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	left, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	// The sweep takes the checkout, and records on the run that it did, exactly
	// as the convergence sweep does: the directory is gone and the branch stands.
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testGitBudget})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	removal, err := worktrees.RemovePreservedWorktree(context.Background(), worktreeOf(stopped), gitworktree.KeepUncommittedWork)
	if err != nil || !removal.Removed || removal.PreservedWork != "" {
		t.Fatalf("RemovePreservedWorktree() = %#v, error = %v; want the clean checkout taken with nothing captured", removal, err)
	}
	swept := time.Now().UTC()
	stopped.WorktreeRemoved = true
	stopped.WorktreeSweptAt = &swept
	stopped.UpdatedAt = swept
	if err := store.Save(stopped); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(stopped.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("the worktree is still on disk: %v", err)
	}
	if !stopped.ResumableIntegration() {
		t.Fatalf("a run whose worktree was retired is not resumable on its record: %#v", stopped)
	}
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewDocketStore() error = %v", err)
	}
	if _, err := docketerOverStore(docket, store, build().Config).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	resumer := IntegrationResumer{
		Docket: docket, Runs: store, Intake: newIntakeHoldStore(t), Items: tracker, Worktrees: worktrees,
		Capacity: 1,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return build().Continue(ctx, workItemID, runID)
		},
	}
	result, err := resumer.Resume(context.Background(), IntegrationResumeRequest{Run: outcome.RunID})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if !result.Resumed || !result.WorktreeRestored || result.RecordProblem != "" {
		t.Fatalf("result = %#v, want the run resumed in a restored worktree with every record written", result)
	}
	if result.Outcome.Integration == nil || result.Outcome.PullRequest == nil || !result.Outcome.PullRequest.Merged || !tracker.Closed {
		t.Fatalf("outcome = integration %#v, pull request %#v, closed %t; want the approved change promoted, merged, and the item closed", result.Outcome.Integration, result.Outcome.PullRequest, tracker.Closed)
	}
	if integrated := gitLine(t, repository, "show", "main:feature.txt"); integrated != "implemented" {
		t.Fatalf("integrated feature.txt = %q, want the approved content", integrated)
	}
	// One developer attempt and one review, both the first run's; nothing was
	// re-derived from the branch by anybody.
	if developer, reviewer := provider.RequestsForRole(domain.RoleDeveloper), provider.RequestsForRole(domain.RoleReviewer); len(developer) != 1 || len(reviewer) != 1 {
		t.Fatalf("invocations = %d developer, %d reviewer; want the one attempt and the one review the first run made", len(developer), len(reviewer))
	}
	landed, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if !reflect.DeepEqual(landed, left) {
		t.Fatalf("the item's triage record moved across a restore and a resumption:\nleft   %#v\nlanded %#v", left, landed)
	}
	// The restored checkout was cleaned up after the promotion exactly as any
	// integrated run's is.
	final, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if final.Status != runstate.StatusSucceeded || !final.WorktreeRemoved || final.WorktreeSweptAt != nil || len(final.IntegrationResumptions) != 1 {
		t.Fatalf("final run = %s, removed %t, swept %v, resumptions %d; want it succeeded with the restored checkout cleaned up by its own integration", final.Status, final.WorktreeRemoved, final.WorktreeSweptAt, len(final.IntegrationResumptions))
	}
}

// The 2026-09-22 duplication, end to end, over a real repository and a forge.
//
// run-b0b6d18d's change on yoyodyne-ifd.436.4 was approved and then stopped
// short of the target branch by the environment, leaving a record that ends
// `failed` with the stop on it, its branch preserved, and its pull request open.
// The docket named the harness and `yoyo triage resume`. Half an hour later the
// claim audit read the item as a claim with nothing working on it and gave it
// back, the next pull started a fresh run, and that run re-derived the same
// change, spent a developer run and a review, and integrated it a second time.
//
// Two things had to be true for that, and both are asserted here: the audit does
// not release such a claim, and the pull's hold covers the run even if something
// else has put the item back — a run that ended `failed` holding its change is
// the same fact to a reader as one that ended `stopped`. Then the resume, which
// is what actually promotes it, with nothing bought in between.
//
// The cause here is the dirty primary checkout rather than the tracker read that
// timed out, because it is the one an ordinary fixture can produce on demand;
// what the audit and the pull read is the stop rather than which of the two
// environmental causes wrote it.
func TestAnIntegrationStoppedRunIsNeitherReleasedNorRestartedAndItsResumePromotesIt(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-ifd.436.4", Title: "The duplicated one", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	// The operator's uncommitted edit lands while the reviewer is judging, so the
	// promotion is refused for the checkout after the approval rather than before.
	dirtying := filepath.Join(repository, "AGENTS.md")
	serve := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			if err := os.WriteFile(dirtying, []byte("an edit nobody committed\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
		}
		return serve(request)
	}
	pipeline := publishing(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"test -f feature.txt"}), provider), forge)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	assertSavedStopClass(t, store, outcome.RunID, runstate.CauseDirtyPrimary.StopClass())
	if err == nil || !errors.Is(err, gitworktree.ErrPrimaryNotReady) {
		t.Fatalf("Run() error = %v, want the promotion refused for the dirty checkout", err)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The record the two readings below are asked about, and the shape that had no
	// marker either of them read: failed rather than stopped, no blocker, the
	// approval standing, and the change on its branch.
	if stopped.Outcome() != runstate.OutcomeFailed || strings.TrimSpace(stopped.Blocker) != "" {
		t.Fatalf("the stopped run reads as %q with blocker %q, want the ending that hands nobody one", stopped.Outcome(), stopped.Blocker)
	}
	if stopped.IntegrationStop == nil || !stopped.ResumableIntegration() {
		t.Fatalf("the stopped run is not a resumable integration stop: %#v", stopped.IntegrationStop)
	}

	// The claim audit, half an hour later, over the item the run left claimed.
	// Nothing is working on it and nothing ever will be again, and that is exactly
	// what must not be read as a claim to give back.
	audited := stopped.UpdatedAt.Add(9 * time.Hour)
	queue := newScheduleHarness()
	queue.now = audited
	queue.finished = []runstate.State{stopped}
	queue.Items = []beads.WorkItem{claimedItem(tracker.Item.ID, tracker.Item.Title)}
	queue.ReadyItems = map[string]bool{}
	queue.stoppages = haltedWork{runs: []runstate.State{stopped}}
	queue.claims = ClaimAuditor{
		Tracker: queue, Runs: queue, Releases: &claimLog{},
		ProductID: "yoyodyne", Clock: fixedClock{at: audited},
	}

	schedule, err := Scheduler{Open: queue.open, Sleep: queue.sleep, Now: queue.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.ReleasedClaims) != 0 {
		t.Fatalf("released = %+v, want the claim over an approved change left exactly where it is", schedule.ReleasedClaims)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %d run(s), want nothing started over a change that is already on a branch: %s", len(schedule.Started), schedule.Render())
	}
	if _, ended := queue.Recorded(); ended != nil {
		t.Fatalf("Recorded() error = %v", ended)
	}
	if reloaded, err := store.Load(outcome.RunID); err != nil || reloaded.Status != runstate.StatusFailed || len(reloaded.IntegrationResumptions) != 0 {
		t.Fatalf("the audit wrote to the stopped run: %#v (error %v)", reloaded, err)
	}

	// And the other half, because the audit is not the only way an item comes
	// back: put the item back in the queue by hand and the pull holds it anyway,
	// naming the run, the change still on its branch, and the verb that finishes
	// it. Before this, a run that ended `failed` read to the pull as an item with
	// nothing holding it at all.
	if _, err := queue.Release(context.Background(), tracker.Item.ID, "put back by hand"); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	schedule, err = Scheduler{Open: queue.open, Sleep: queue.sleep, Now: queue.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %d run(s), want the reopened item held rather than run again: %s", len(schedule.Started), schedule.Render())
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != tracker.Item.ID {
		t.Fatalf("deferred = %#v, want the stoppage passed over with what holds it named", schedule.Deferred)
	}
	for _, want := range []string{outcome.RunID, "its change approved", "`yoyo triage resume`"} {
		if !strings.Contains(schedule.Deferred[0].Reason, want) {
			t.Fatalf("deferred reason = %q, want it to name %q", schedule.Deferred[0].Reason, want)
		}
	}

	// The resume is what promotes it. Nothing was bought in between: the run is
	// the one the reviewer approved, made live again at the promotion it stopped
	// short of, with the developer's one attempt and the reviewer's one verdict.
	left, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if err := os.Remove(dirtying); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	docket := &memoryDocket{}
	if _, err := docketerOverStore(docket, store, pipeline.Config).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testGitBudget})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	resumed, err := IntegrationResumer{
		Docket: docket, Runs: store, Intake: newIntakeHoldStore(t), Items: tracker, Worktrees: worktrees,
		Capacity: pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start:    pipeline.Continue,
	}.Resume(context.Background(), IntegrationResumeRequest{Run: outcome.RunID})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if !resumed.Resumed || resumed.Outcome.Integration == nil || resumed.Outcome.PullRequest == nil || !resumed.Outcome.PullRequest.Merged {
		t.Fatalf("result = %#v, want the approved change promoted and its pull request merged", resumed)
	}
	assertRemoteCarriesPromotion(t, repository, remote, "main", resumed.Outcome.Integration.TargetCommit)
	if developer, reviewer := provider.RequestsForRole(domain.RoleDeveloper), provider.RequestsForRole(domain.RoleReviewer); len(developer) != 1 || len(reviewer) != 1 {
		t.Fatalf("invocations = %d developer, %d reviewer; want the one attempt and the one review the stopped run made", len(developer), len(reviewer))
	}
	landed, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if !reflect.DeepEqual(landed, left) {
		t.Fatalf("the item's triage record moved across the resumption:\nleft   %#v\nlanded %#v", left, landed)
	}
}

// The classifier reads the cause the pipeline recorded — the step's own failure
// before any write it attempted afterwards — and not the whole error. The 441
// shape is a replay conflict whose blocker write timed out: the tail is in the
// recovery package's closed set, and the whole was read as transport. The cause
// alone is a conflict, which is refused by its sentinel whatever wraps it; and
// the same write failing after a genuinely environmental stop does not hide the
// stop.
func TestAnIntegrationStopIsClassifiedFromTheStepsOwnCauseAndNeverFromAFailedRecord(t *testing.T) {
	t.Parallel()

	conflict := fmt.Errorf("change cannot be replayed onto the moved target branch: %w", gitworktree.ErrRebaseConflict)
	timedOut := fmt.Errorf("record the replay conflict as a blocker: %w", errors.New("bd update failed with status timed_out and exit code -1: "))
	dirty := fmt.Errorf("integrate approved change: %w", gitworktree.ErrPrimaryNotReady)
	transport := errors.New("bd show failed with status timed_out and exit code -1: ")
	for name, tc := range map[string]struct {
		failure error
		cause   runstate.EnvironmentalCause
		stop    bool
	}{
		"the 441 shape: a conflict, then a timed-out blocker write":  {failure: withFailedRecord(conflict, timedOut)},
		"a conflict wrapped again after the failed write":            {failure: fmt.Errorf("run ended: %w", withFailedRecord(conflict, timedOut))},
		"a conflict whose blocker write landed":                      {failure: conflict},
		"a conflict joined the old way, matched on its tail before":  {failure: errors.Join(conflict, timedOut)},
		"a dirty checkout, then a store that would not save":         {failure: withFailedRecord(dirty, errors.New("record the commit the refused promotion made: disk full")), cause: runstate.CauseDirtyPrimary, stop: true},
		"a tracker read that timed out, with nothing recorded after": {failure: transport, cause: runstate.CauseTransportFailure, stop: true},
		"a diverged target, then a timed-out blocker write":          {failure: withFailedRecord(fmt.Errorf("%w: main cannot be brought onto origin before promoting: diverged", ErrDivergedTarget), timedOut), cause: runstate.CauseDivergedTarget, stop: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cause, stop := integrationStopCauseOf(tc.failure)
			if stop != tc.stop || cause != tc.cause {
				t.Fatalf("integrationStopCauseOf(%v) = %q, %t; want %q, %t", tc.failure, cause, stop, tc.cause, tc.stop)
			}
		})
	}
	// Both sides are still in the error: the message carries them in order, and
	// errors.Is finds each.
	paired := withFailedRecord(conflict, timedOut)
	if !errors.Is(paired, gitworktree.ErrRebaseConflict) || !strings.Contains(paired.Error(), "cannot be replayed onto") || !strings.Contains(paired.Error(), "bd update failed with status timed_out") {
		t.Fatalf("withFailedRecord() = %v; want the conflict and the failed write both preserved", paired)
	}
	if stepCauseOf(paired) != conflict || stepCauseOf(conflict) != conflict || withFailedRecord(nil, timedOut) != timedOut {
		t.Fatal("stepCauseOf() did not read the step's own cause back")
	}
}
