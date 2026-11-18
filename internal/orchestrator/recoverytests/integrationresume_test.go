package recoverytests

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
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Reasoning given to the command is recorded beside the harness's own account,
// attributed to the command rather than quoted as anybody's decision.
func TestAResumptionRecordsReasoningItWasGivenBesideItsOwnAccount(t *testing.T) {
	t.Parallel()

	harness := testharness.NewResumeHarness(t, testharness.ApprovedStoppedState())
	request := testharness.ResumeRequest()
	request.Reason = "the AGENTS.md edit landed as PR 524, so the checkout is clean again"
	if _, err := harness.Resumer().Resume(context.Background(), request); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	reason := harness.Reload(t).IntegrationResumptions[0].Reason
	if !strings.Contains(reason, "The reasoning given to the harness when it was asked to: the AGENTS.md edit landed") {
		t.Fatalf("reason = %q, want the reasoning attributed to the command", reason)
	}
}

// A run the environment did not stop is somebody's to decide about, and every
// shape of that is refused naming what it is rather than promoted on a record
// that says something else.
func TestAResumptionIsRefusedForARunThatIsNotAnApprovedChangeTheEnvironmentStopped(t *testing.T) {
	t.Parallel()

	for name, shape := range map[string]func(runstate.State) runstate.State{
		"the environment did not stop it": func(state runstate.State) runstate.State {
			state.IntegrationStop = nil
			state.Environmental = nil
			state.Failure = "integration lost its target branch after 2 of 2 permitted retry(s)"
			return state
		},
		"it was never approved": func(state runstate.State) runstate.State {
			state.IntegrationStop = nil
			state.Environmental = nil
			state.ReviewDecision = runstate.ReviewRepair
			state.ReviewApproves = ""
			state.Phase = runstate.PhaseReviewing
			state.ReviewFindings = 1
			state.ReviewFindingDetails = []runstate.Finding{{Severity: "blocker", Message: "add the missing file"}}
			state.Blocker = "Yoyodyne stopped this item: the repair budget was spent."
			return state
		},
		"its branch was deleted": func(state runstate.State) runstate.State {
			swept := testharness.DocketedNow.Add(-30 * time.Minute)
			state.BranchRemoved = true
			state.BranchSweptAt = &swept
			return state
		},
		"the sweep captured uncommitted work off its worktree": func(state runstate.State) runstate.State {
			swept := testharness.DocketedNow.Add(-30 * time.Minute)
			state.WorktreeRemoved = true
			state.WorktreeSweptAt = &swept
			state.PreservedWorkRef = "refs/yoyodyne/preserved-work/" + testharness.DocketedRunID
			return state
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			harness := testharness.NewResumeHarness(t, shape(testharness.ApprovedStoppedState()))
			_, err := harness.Resumer().Resume(context.Background(), testharness.ResumeRequest())
			if !errors.Is(err, orchestrator.ErrNotResumable) {
				t.Fatalf("Resume() error = %v, want the run refused as not resumable", err)
			}
			harness.AssertNothingWritten(t)
		})
	}
}

// The bound on one run's resumptions is asked before anything is written,
// because the save that would refuse it comes after the item has been put back
// — which is the half-made state the ordering exists to prevent.
func TestAResumptionAtTheBoundIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	t.Parallel()

	state := testharness.ApprovedStoppedState()
	for index := 0; index < runstate.MaxIntegrationResumptions; index++ {
		state.IntegrationResumptions = append(state.IntegrationResumptions, runstate.IntegrationResumption{
			Cause: runstate.CauseDirtyPrimary, Reason: "resumed", ResumedAt: testharness.DocketedNow.Add(-time.Duration(index+1) * time.Hour),
		})
	}
	if state.ResumableIntegration() || state.ResumptionsLeft() != 0 {
		t.Fatalf("a run at the bound reads as resumable: left %d", state.ResumptionsLeft())
	}
	harness := testharness.NewResumeHarness(t, state)
	_, err := harness.Resumer().Resume(context.Background(), testharness.ResumeRequest())
	if !errors.Is(err, orchestrator.ErrNotResumable) || !strings.Contains(err.Error(), "the bound on one run's resumptions") {
		t.Fatalf("Resume() error = %v, want the bound refused by name", err)
	}
	harness.AssertNothingWritten(t)
	if reloaded := harness.Reload(t); len(reloaded.IntegrationResumptions) != runstate.MaxIntegrationResumptions {
		t.Fatalf("resumptions = %d, want the record untouched at the bound", len(reloaded.IntegrationResumptions))
	}
}

// retiredState is the approved, stopped run after the convergence sweep took
// its checkout: the directory is gone, the branch still holds the reviewed
// commit, and nothing uncommitted was captured because there was nothing.
func retiredState() runstate.State {
	state := testharness.ApprovedStoppedState()
	swept := testharness.DocketedNow.Add(-30 * time.Minute)
	state.WorktreeRemoved = true
	state.WorktreeSweptAt = &swept
	return state
}

// A worktree the sweep retired while the run stood stopped is put back from
// the branch at the reviewed commit, and the run is resumed in it. The restore
// is recorded on the run as it is made.
func TestAResumptionPutsARetiredWorktreeBackFromItsBranch(t *testing.T) {
	t.Parallel()

	harness := testharness.NewResumeHarness(t, retiredState())
	result, err := harness.Resumer().Resume(context.Background(), testharness.ResumeRequest())
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if !result.Resumed || !result.WorktreeRestored {
		t.Fatalf("result = %#v, want the run resumed in a restored worktree", result)
	}
	if len(harness.Ownership.Restored) != 1 || harness.Ownership.Restored[0].Branch != "yoyodyne/task/abc" || harness.Ownership.Restored[0].HarnessCommit != strings.Repeat("c", 40) {
		t.Fatalf("restored = %#v, want the worktree put back on its branch at the recorded commit", harness.Ownership.Restored)
	}
	state := harness.Reload(t)
	if state.WorktreeRemoved || state.WorktreeSweptAt != nil || state.Status != runstate.StatusRunning {
		t.Fatalf("resumed run = removed %t swept %v %s; want the checkout recorded as back and the run live", state.WorktreeRemoved, state.WorktreeSweptAt, state.Status)
	}
	if !strings.Contains(result.Render(), "put back from the branch") {
		t.Fatalf("rendered = %q, want the restore said", result.Render())
	}
}

// A retired worktree that cannot be put back refuses before anything else is
// written, and a restore that succeeded and was then refused for the checkout
// it produced leaves a stopped run whose record says the checkout is back.
func TestAResumptionRefusesARetiredWorktreeItCannotRestore(t *testing.T) {
	t.Parallel()

	harness := testharness.NewResumeHarness(t, retiredState())
	harness.Ownership.RestoreErr = errors.New("branch yoyodyne/task/abc is at deadbeef, not at the commit the harness recorded")
	_, err := harness.Resumer().Resume(context.Background(), testharness.ResumeRequest())
	if !errors.Is(err, orchestrator.ErrWorktreeNotRestored) || !strings.Contains(err.Error(), "deadbeef") {
		t.Fatalf("Resume() error = %v, want the restore refused naming what stopped it", err)
	}
	state := harness.Reload(t)
	if state.Status != runstate.StatusFailed || !state.WorktreeRemoved || len(state.IntegrationResumptions) != 0 {
		t.Fatalf("a refused restore changed the run: %#v", state)
	}
	if len(harness.Tracker.Calls) != 0 || len(harness.Started) != 0 {
		t.Fatalf("a refused restore wrote to the item or dispatched something: %v %#v", harness.Tracker.Calls, harness.Started)
	}

	emptied := testharness.NewResumeHarness(t, retiredState())
	emptied.Ownership.Changed = []string{}
	if _, err := emptied.Resumer().Resume(context.Background(), testharness.ResumeRequest()); !errors.Is(err, orchestrator.ErrPreservedChangeMissing) {
		t.Fatalf("Resume() error = %v, want the restored but empty worktree refused", err)
	}
	if state := emptied.Reload(t); state.Status != runstate.StatusFailed || state.WorktreeRemoved || len(state.IntegrationResumptions) != 0 {
		t.Fatalf("after a refusal past the restore: %s removed %t resumptions %d; want the run still stopped with its checkout recorded as back", state.Status, state.WorktreeRemoved, len(state.IntegrationResumptions))
	}
}

// The checkout is what stopped the run, and it is asked before anything is
// written: a run made live again into the same refusal would stop again with
// its record saying it was resumed.
func TestAResumptionIsRefusedWhileTheCheckoutIsStillNotReady(t *testing.T) {
	t.Parallel()

	harness := testharness.NewResumeHarness(t, testharness.ApprovedStoppedState())
	harness.Ownership.ReadyErr = fmt.Errorf("%w: primary repository has uncommitted changes: AGENTS.md", gitworktree.ErrPrimaryNotReady)
	_, err := harness.Resumer().Resume(context.Background(), testharness.ResumeRequest())
	if !errors.Is(err, orchestrator.ErrCheckoutNotReady) || !strings.Contains(err.Error(), "AGENTS.md") {
		t.Fatalf("Resume() error = %v, want the checkout refused naming what it carries", err)
	}
	harness.AssertNothingWritten(t)
}

// What is promoted is whatever is in the worktree, so a worktree somebody has
// been in, or one that lost the change, refuses to a person exactly as a repair
// does.
func TestAResumptionRefusesAWorktreeThatIsNotAsTheHarnessLeftItOrIsEmpty(t *testing.T) {
	t.Parallel()

	touched := testharness.NewResumeHarness(t, testharness.ApprovedStoppedState())
	touched.Ownership.Err = errors.New("worktree HEAD is deadbeef, want the commit the harness recorded")
	if _, err := touched.Resumer().Resume(context.Background(), testharness.ResumeRequest()); !errors.Is(err, orchestrator.ErrWorktreeNotAsLeft) {
		t.Fatalf("Resume() error = %v, want the touched worktree refused", err)
	}
	touched.AssertNothingWritten(t)

	emptied := testharness.NewResumeHarness(t, testharness.ApprovedStoppedState())
	emptied.Ownership.Changed = []string{}
	if _, err := emptied.Resumer().Resume(context.Background(), testharness.ResumeRequest()); !errors.Is(err, orchestrator.ErrPreservedChangeMissing) {
		t.Fatalf("Resume() error = %v, want the empty worktree refused", err)
	}
	emptied.AssertNothingWritten(t)
}

// A held intake and a full harness are waits rather than refusals, and neither
// writes anything.
func TestAResumptionWaitsOnAHeldIntakeOrAFullHarness(t *testing.T) {
	t.Parallel()

	held := testharness.NewResumeHarness(t, testharness.ApprovedStoppedState())
	if _, err := held.Intake.Hold(runstate.IntakeHolderOperator, "the queue is heading somewhere odd", testharness.DocketedNow); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	result, err := held.Resumer().Resume(context.Background(), testharness.ResumeRequest())
	if err != nil || result.Resumed || result.IntakeHeld == nil {
		t.Fatalf("Resume() = %#v, error = %v; want the hold reported and nothing resumed", result, err)
	}
	held.AssertNothingWritten(t)

	full := testharness.NewResumeHarness(t, testharness.ApprovedStoppedState())
	full.Capacity = 1
	other := testharness.ApprovedStoppedState()
	other.RunID = "run-ffffffffffffffffffffffffffffffff"
	other.WorkItemID = "yoyodyne-other"
	other.Status = runstate.StatusRunning
	other.Phase = runstate.PhaseDeveloping
	other.CompletedAt = nil
	other.IntegrationStop = nil
	other.Environmental = nil
	other.Failure = ""
	if err := full.Runs.Create(other); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	result, err = full.Resumer().Resume(context.Background(), testharness.ResumeRequest())
	if err != nil || result.Resumed || result.CapacityFull == nil {
		t.Fatalf("Resume() = %#v, error = %v; want the full harness reported and nothing resumed", result, err)
	}
	full.AssertNothingWritten(t)
}

// slotTakenAfterReading is a run store whose last free slot a fresh start takes
// after a recovery path has read it free and before the save that makes the
// stopped run live, exactly as a scheduler beside the recovery would.
type slotTakenAfterReading struct {
	orchestrator.RepairRuns
	take func()
}

func (s *slotTakenAfterReading) ReserveLive(ctx context.Context, live runstate.State, maxConcurrent int) error {
	if s.take != nil {
		s.take()
		s.take = nil
	}
	return s.RepairRuns.ReserveLive(ctx, live, maxConcurrent)
}

// A fresh start that takes the last slot after the resumption read it free
// wins it: making the stopped run live is where the slot is taken, so the
// resumption waits as it would on a full harness, leaves the run stopped, and
// tells the item why, rather than running two developers in one slot.
func TestAResumptionWaitsWhenAFreshStartTakesTheLastSlotAfterItWasReadFree(t *testing.T) {
	t.Parallel()

	harness := testharness.NewResumeHarness(t, testharness.ApprovedStoppedState())
	harness.Capacity = 1
	fresh := testharness.ApprovedStoppedState()
	fresh.RunID = "run-ffffffffffffffffffffffffffffffff"
	fresh.WorkItemID = "yoyodyne-fresh"
	fresh.Status = runstate.StatusPending
	fresh.Phase = ""
	fresh.CompletedAt = nil
	fresh.IntegrationStop = nil
	fresh.Environmental = nil
	fresh.Failure = ""
	resumer := harness.Resumer()
	resumer.Runs = &slotTakenAfterReading{RepairRuns: harness.Runs, take: func() {
		lease, err := harness.Runs.Reserve(context.Background(), fresh, 1)
		if err != nil {
			t.Errorf("Reserve() of the fresh start error = %v", err)
			return
		}
		t.Cleanup(func() { lease.Release() })
	}}

	result, err := resumer.Resume(context.Background(), testharness.ResumeRequest())
	if err != nil || result.Resumed || result.CapacityFull == nil || result.CapacityFull.Active != 1 {
		t.Fatalf("Resume() = %#v, error = %v; want the slot reported taken and nothing resumed", result, err)
	}
	if state := harness.Reload(t); state.Status != runstate.StatusFailed || len(state.IntegrationResumptions) != 0 {
		t.Fatalf("the refused resumption changed the run: %s/%s, resumptions %#v", state.Status, state.Phase, state.IntegrationResumptions)
	}
	if len(harness.Started) != 0 {
		t.Fatalf("the refused resumption dispatched something: %#v", harness.Started)
	}
	if _, closed := harness.Closure(t); closed {
		t.Fatal("the refused resumption closed the docket entry")
	}
	if !strings.Contains(harness.Tracker.Notes, "the last free developer slot went to another run") {
		t.Fatalf("item notes = %q, want the wait for a slot said", harness.Tracker.Notes)
	}
	active, err := harness.Runs.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if held := runstate.HoldingDeveloperSlots(active); held != 1 {
		t.Fatalf("developer slots held = %d, want exactly the fresh start's", held)
	}
}

// divergedStoppedState is an approved change stopped because its target would
// not catch up, which is how yoyodyne-ifd.428.16 stopped.
func divergedStoppedState() runstate.State {
	state := testharness.ApprovedStoppedState()
	state.Failure = "target branch diverged from the remote's: main cannot be brought onto origin before promoting"
	state.Environmental = nil
	state.IntegrationStop.Cause = runstate.CauseDivergedTarget
	state.IntegrationStop.Detail = state.Failure
	return state
}

// A resumption asked while the branches are still diverged refuses, writes
// nothing, and says what clears it; asked once they are settled, it resumes the
// same run charging nothing.
func TestAResumptionOfADivergedTargetRefusesUntilTheBranchesAreSettled(t *testing.T) {
	t.Parallel()

	harness := testharness.NewResumeHarness(t, divergedStoppedState())
	harness.Ownership.Held = "main on origin is at abc, which does not contain the local main at def; only a person can say which history is right"
	_, err := harness.Resumer().Resume(context.Background(), testharness.ResumeRequest())
	var stands orchestrator.CauseStandsError
	if !errors.Is(err, orchestrator.ErrCauseStands) || !errors.As(err, &stands) || stands.Cause != runstate.CauseDivergedTarget {
		t.Fatalf("Resume() error = %v, want the diverged target refused as still standing", err)
	}
	for _, want := range []string{"does not contain the local main", "Unwedging a target branch that diverged from the forge", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not say %q: %v", want, err)
		}
	}
	harness.AssertNothingWritten(t)
	if harness.Ownership.AccessAsked != 0 {
		t.Fatalf("a diverged-target stop asked the remotes about the credential %d time(s)", harness.Ownership.AccessAsked)
	}

	harness.Ownership.Held = ""
	result, err := harness.Resumer().Resume(context.Background(), testharness.ResumeRequest())
	if err != nil || !result.Resumed || len(harness.Started) != 1 {
		t.Fatalf("Resume() = %#v, %v; want the settled stop resumed", result, err)
	}
	state := harness.Reload(t)
	if state.ReviewRounds != 1 || state.RepairAttempts != 1 || len(state.IntegrationResumptions) != 1 || state.IntegrationResumptions[0].Cause != runstate.CauseDivergedTarget {
		t.Fatalf("resumed run = rounds %d attempts %d resumptions %#v; want nothing charged and the diverged target superseded", state.ReviewRounds, state.RepairAttempts, state.IntegrationResumptions)
	}
}

// A resumption asked while the remote still refuses the key refuses and says
// what clears it, and a question the remote did not answer refuses as well
// rather than making the run live on an answer nobody got.
func TestAResumptionOfARefusedKeyRefusesUntilTheRemoteTakesIt(t *testing.T) {
	t.Parallel()

	state := testharness.ApprovedStoppedState()
	state.Failure = "republish the replayed developer branch: the remote refused the credential the harness presented: git push exited 128: git@github.com: Permission denied (publickey)."
	state.Environmental = nil
	state.IntegrationStop.Cause = runstate.CauseRemoteAuthRefused
	state.IntegrationStop.Detail = state.Failure
	harness := testharness.NewResumeHarness(t, state)

	harness.Ownership.AccessErr = fmt.Errorf("list main on origin: %w: git ls-remote exited 128: git@github.com: Permission denied (publickey).", gitworktree.ErrRemoteAuthRefused)
	_, err := harness.Resumer().Resume(context.Background(), testharness.ResumeRequest())
	if !errors.Is(err, orchestrator.ErrCauseStands) {
		t.Fatalf("Resume() error = %v, want the refused key refused as still standing", err)
	}
	for _, want := range []string{"Permission denied (publickey)", "ssh-add", "gh auth login"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not say %q: %v", want, err)
		}
	}
	harness.AssertNothingWritten(t)

	harness.Ownership.AccessErr = errors.New("list main on origin: connection reset by peer")
	if _, err := harness.Resumer().Resume(context.Background(), testharness.ResumeRequest()); !errors.Is(err, orchestrator.ErrCauseStands) || !strings.Contains(err.Error(), "could not be asked") {
		t.Fatalf("Resume() error = %v, want an unanswered question refused rather than resumed on", err)
	}
	harness.AssertNothingWritten(t)

	harness.Ownership.AccessErr = nil
	result, err := harness.Resumer().Resume(context.Background(), testharness.ResumeRequest())
	if err != nil || !result.Resumed {
		t.Fatalf("Resume() = %#v, %v; want the stop resumed once the key is taken", result, err)
	}
	if harness.Ownership.TargetAsked != 0 {
		t.Fatalf("a refused-key stop asked whether the target catches up %d time(s)", harness.Ownership.TargetAsked)
	}
}

// timingOutTracker is a tracker whose Nth read dies the way a `bd` killed under
// load does, which is the second of yoyodyne-ifd.309's two stops. It refuses
// once and then answers, which is what the boundary's recovery window is for:
// everything else it answers as the tracker under it does.
type timingOutTracker struct {
	*orchestratortest.Tracker
	failShowAt int
	shows      int
}

func (f *timingOutTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	f.shows++
	if f.shows == f.failShowAt {
		return beads.WorkItem{}, errors.New("bd show failed with status timed_out and exit code -1: ")
	}
	return f.Tracker.Show(ctx, id)
}

// The 309 shape, over a real repository and a forge: the change is approved,
// the promotion is refused for an uncommitted edit in the primary checkout, and
// the resumed promotion meets the tracker read that timed out — which used to be
// 309's second stop and is now waited out — and reaches a merged pull request,
// with every counter exactly where the review left it and the run's record
// saying the resumption was a continuation rather than an attempt.
func TestAnApprovedChangeStoppedByTheEnvironmentResumesToAMergedPullRequestChargingNothing(t *testing.T) {
	t.Parallel()

	repository, remote := testharness.PublishedRepository(t)
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.ApproveVerdict)
	// The operator's uncommitted edit lands in the primary checkout while the
	// reviewer is judging the change, which is what the first stop was.
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
	checks := []string{"test -f feature.txt"}
	build := func(tracker orchestrator.WorkTracker) orchestrator.Pipeline {
		return testharness.Publishing(testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, checks), provider), forge)
	}

	outcome, err := build(tracker).Run(context.Background(), tracker.Item.ID)
	if err == nil || !errors.Is(err, gitworktree.ErrPrimaryNotReady) {
		t.Fatalf("Run() error = %v, want the promotion refused for the dirty checkout", err)
	}
	if outcome.Integration != nil || outcome.PullRequest == nil || outcome.PullRequest.Merged || outcome.ReviewDecision != "approve" {
		t.Fatalf("outcome = integration %#v, pull request %#v, review %q; want an approved, published, unpromoted change", outcome.Integration, outcome.PullRequest, outcome.ReviewDecision)
	}
	if outcome.IntegrationStop == nil || outcome.IntegrationStop.Cause != runstate.CauseDirtyPrimary || outcome.IntegrationStop.Phase != runstate.PhaseIntegrating {
		t.Fatalf("integration stop = %#v, want the dirty checkout recorded at the integrating phase", outcome.IntegrationStop)
	}
	if !strings.Contains(tracker.Notes, "Integration stop: approved, then stopped at the integrating phase by the environment: dirty-primary") ||
		!strings.Contains(tracker.Notes, "`yoyo triage resume "+outcome.RunID+"`") {
		t.Fatalf("item notes do not name the stop and the verb that resumes it:\n%s", tracker.Notes)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !stopped.ResumableIntegration() {
		t.Fatalf("the stopped run is not resumable on its own record: %#v", stopped)
	}
	// Where the review left the item's counters, which is where they have to
	// still be once the change has landed.
	left, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	docket := &testharness.MemoryDocket{}
	docketer := testharness.DocketerOverStore(docket, store, build(tracker).Config)
	if _, err := docketer.RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	if entry := docket.Entries[0]; entry.IntegrationStop == nil || entry.IntegrationStop.Cause != string(runstate.CauseDirtyPrimary) {
		t.Fatalf("docket entry = %#v, want the stop carried onto it", docket.Entries[0])
	} else if rendered := entry.Render(); !strings.Contains(rendered, "Next mover: the harness") || !strings.Contains(rendered, "yoyo triage resume "+outcome.RunID) {
		t.Fatalf("docket entry does not say the harness resumes it:\n%s", rendered)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testharness.TestGitBudget})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	intake := testharness.NewIntakeHoldStore(t)
	resumer := func(pipeline orchestrator.Pipeline, observed *[]runstate.State) orchestrator.IntegrationResumer {
		return orchestrator.IntegrationResumer{
			Docket: docket, Runs: store, Intake: intake, Items: tracker, Worktrees: worktrees,
			Capacity: pipeline.Config.Execution.MaxConcurrentDevelopers,
			Start: func(ctx context.Context, workItemID, runID string) (orchestrator.Outcome, error) {
				// What `yoyo status` would read while the promotion is going.
				live, err := store.Load(runID)
				if err != nil {
					return orchestrator.Outcome{}, err
				}
				*observed = append(*observed, live)
				return pipeline.Continue(ctx, workItemID, runID)
			},
		}
	}

	// The resumption is refused while the checkout is still dirty, and writes
	// nothing: the run is exactly as it stopped.
	if _, err := resumer(build(tracker), new([]runstate.State)).Resume(context.Background(), orchestrator.IntegrationResumeRequest{Run: outcome.RunID}); !errors.Is(err, orchestrator.ErrCheckoutNotReady) {
		t.Fatalf("Resume() error = %v, want the dirty checkout refused before anything is written", err)
	}
	if again, err := store.Load(outcome.RunID); err != nil || again.Status != runstate.StatusFailed || len(again.IntegrationResumptions) != 0 {
		t.Fatalf("a refused resumption changed the run: %#v (error %v)", again, err)
	}
	if err := os.Remove(dirtying); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	// The resumption: the promotion goes again, and the tracker read the gate
	// makes before it promotes times out — the second of 309's two stops, which
	// used to end the run here. The resumed run's own read is the second Show the
	// continued pipeline makes; the first is the continuation loading the item.
	// Since yoyodyne-ifd.428.6 that read is waited out on the same window every
	// other recoverable boundary gets, so the same resumption carries through to
	// the merged pull request instead of stopping a second time.
	var observed []runstate.State
	flaky := &timingOutTracker{Tracker: tracker, failShowAt: 2}
	result, err := resumer(build(flaky), &observed).Resume(context.Background(), orchestrator.IntegrationResumeRequest{Run: outcome.RunID})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if !result.Resumed || result.Outcome.Integration == nil || result.Outcome.PullRequest == nil || !result.Outcome.PullRequest.Merged {
		t.Fatalf("result = %#v, want the approved change promoted and its pull request merged", result)
	}
	if len(forge.Merges) != 1 || !tracker.Closed {
		t.Fatalf("forge merges = %d, item closed = %t; want one merge and the item closed on it", len(forge.Merges), tracker.Closed)
	}
	testharness.AssertRemoteCarriesPromotion(t, repository, remote, "main", result.Outcome.Integration.TargetCommit)
	if integrated := testharness.GitLine(t, repository, "show", "main:feature.txt"); integrated != "implemented" {
		t.Fatalf("integrated feature.txt = %q, want the approved content", integrated)
	}
	if len(observed) != 1 || !observed[0].ResumingIntegration() || len(observed[0].IntegrationResumptions) != 1 {
		t.Fatalf("the run in flight did not read as resuming its integration: %#v", observed)
	}
	// Nothing was invoked: the developer's attempt and the reviewer's verdict are
	// the ones the first run made.
	if developer, reviewer := provider.RequestsForRole(domain.RoleDeveloper), provider.RequestsForRole(domain.RoleReviewer); len(developer) != 1 || len(reviewer) != 1 {
		t.Fatalf("invocations = %d developer, %d reviewer; want the one attempt and the one review the first run made", len(developer), len(reviewer))
	}
	// And every counter is where the review left it.
	landed, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if !reflect.DeepEqual(landed, left) {
		t.Fatalf("the item's triage record moved across the resumption:\nleft   %#v\nlanded %#v", left, landed)
	}
	final, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if final.Status != runstate.StatusSucceeded || final.RepairAttempts != 0 || final.ReviewRounds != 1 || len(final.IntegrationResumptions) != 1 {
		t.Fatalf("final run = %s, attempts %d, rounds %d, resumptions %d; want it succeeded with no attempt or round added", final.Status, final.RepairAttempts, final.ReviewRounds, len(final.IntegrationResumptions))
	}
	if !strings.Contains(tracker.Notes, "was approved by an independent reviewer, and was integrated automatically") {
		t.Fatalf("item notes do not record the integration:\n%s", tracker.Notes)
	}
}

// A replay that conflicts is the one outcome that leaves the resumed path, and
// it leaves it exactly as a first promotion's conflict does. Its first conflict
// is answered by the run's one reconciliation attempt, and the target moves
// again while that is out; with no repair attempt left to hand the second
// conflict to its developer, the run stops for a person, both sides preserved,
// with nothing charged and no environmental stop recorded for a conflict the
// environment does not answer for.
func TestAResumedPromotionWhoseReplayConflictsStopsForAPersonChargingNothing(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := testharness.RestartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	developed := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		// The reconciliation keeps this run's answer, and main moves onto the same
		// line again while it is out.
		if developed++; developed > 1 {
			testharness.WritePipelineFile(t, repository, "feature.txt", "someone else's second version\n")
			testharness.RunPipelineGit(t, repository, "add", "feature.txt")
			testharness.RunPipelineGit(t, repository, "commit", "-m", "a second conflicting edit on main")
		}
		return nil
	}, testharness.ApproveVerdict)
	dirtying := filepath.Join(repository, "AGENTS.md")
	serve := provider.Respond
	reviewed := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer && reviewed == 0 {
			reviewed++
			if err := os.WriteFile(dirtying, []byte("an edit nobody committed\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
		}
		return serve(request)
	}
	build := func() orchestrator.Pipeline {
		pipeline := testharness.Automatic(testharness.NewSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
		pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0
		return pipeline
	}
	outcome, err := build().Run(context.Background(), tracker.Item.ID)
	if err == nil || !errors.Is(err, gitworktree.ErrPrimaryNotReady) {
		t.Fatalf("Run() error = %v, want the promotion refused for the dirty checkout", err)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	left, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	docket := &testharness.MemoryDocket{}
	if _, err := testharness.DocketerOverStore(docket, store, build().Config).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	// The checkout is put right, and the target moves onto a conflicting edit of
	// the same file: the ground moved under the approved change.
	if err := os.Remove(dirtying); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "feature.txt"), []byte("someone else's version\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	testharness.RunPipelineGit(t, repository, "add", "feature.txt")
	testharness.RunPipelineGit(t, repository, "commit", "-m", "a conflicting edit on main")
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot, Timeout: testharness.TestGitBudget})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	resumer := orchestrator.IntegrationResumer{
		Docket: docket, Runs: store, Intake: testharness.NewIntakeHoldStore(t), Items: tracker, Worktrees: worktrees,
		Capacity: 1,
		Start: func(ctx context.Context, workItemID, runID string) (orchestrator.Outcome, error) {
			return build().Continue(ctx, workItemID, runID)
		},
	}
	result, err := resumer.Resume(context.Background(), orchestrator.IntegrationResumeRequest{Run: outcome.RunID})
	if !result.Resumed || !errors.Is(err, gitworktree.ErrRebaseConflict) {
		t.Fatalf("Resume() = resumed %t, error = %v; want the resumed promotion stopped on the replay conflict", result.Resumed, err)
	}
	if !tracker.Blocked || !strings.Contains(tracker.BlockReason, "cannot be replayed") {
		t.Fatalf("blocked = %t, reason = %q; want the conflict recorded for a person", tracker.Blocked, tracker.BlockReason)
	}
	conflicted, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if conflicted.Status != runstate.StatusFailed || conflicted.Blocker == "" || conflicted.IntegrationRetries != 2 || conflicted.Reconciliation == nil {
		t.Fatalf("conflicted run = %s, blocker %q, retries %d, reconciliation %#v; want it stopped on its second replay with its reconciliation used",
			conflicted.Status, conflicted.Blocker, conflicted.IntegrationRetries, conflicted.Reconciliation)
	}
	// A conflict is a person's, so it is not recorded as a stop the harness can
	// resume past; and both sides survive.
	if conflicted.IntegrationStop != nil || conflicted.ResumableIntegration() {
		t.Fatalf("a replay conflict was recorded as an environmental stop: %#v", conflicted.IntegrationStop)
	}
	if conflicted.WorktreeRemoved || conflicted.BranchRemoved {
		t.Fatal("the conflicted run's artifacts were removed")
	}
	if theirs := testharness.GitLine(t, repository, "show", "main:feature.txt"); theirs != "someone else's second version" {
		t.Fatalf("main feature.txt = %q, want the target left where it was", theirs)
	}
	if conflicted.RepairAttempts != 0 || len(conflicted.RepairContinuations) != 0 {
		t.Fatalf("the conflict charged an attempt: %d attempts, %#v", conflicted.RepairAttempts, conflicted.RepairContinuations)
	}
	landed, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if landed.ReviewRounds != left.ReviewRounds || landed.CommittedRounds != left.CommittedRounds || !reflect.DeepEqual(landed.Decisions, left.Decisions) {
		t.Fatalf("the item's triage record moved across a resumption that conflicted:\nleft   %#v\nlanded %#v", left, landed)
	}
}

func TestResumeChecksTheRepositoryEvenWhenRemovalFlagsDisagree(t *testing.T) {
	t.Parallel()
	for _, there := range []bool{true, false} {
		state := testharness.ApprovedStoppedState()
		state.BranchRemoved, state.WorktreeRemoved = there, there
		if there {
			state.ArtifactsRetiredBy = testharness.PriorRunID
		}
		harness := testharness.NewResumeHarness(t, testharness.ApprovedStoppedState())
		if err := harness.Runs.Save(state); err != nil {
			t.Fatal(err)
		}
		resumer := harness.Resumer()
		resumer.Remains = &orchestratortest.Survival{Survival: gitworktree.Survival{BranchExists: there, WorktreePresent: there}}
		result, err := resumer.Resume(context.Background(), testharness.ResumeRequest())
		if (err == nil) != there || result.Resumed != there || (len(harness.Started) > 0) != there {
			t.Fatalf("Resume() = %#v, %v, want resumed %t", result, err, there)
		}
		if there && (result.WorktreeRestored || len(harness.Ownership.Restored) != 0) {
			t.Fatal("a present checkout was restored because of its removal flag")
		}
		if err != nil && (!strings.Contains(err.Error(), state.Branch) || !strings.Contains(err.Error(), state.WorktreePath)) {
			t.Fatalf("refusal does not name what was checked: %v", err)
		}
	}
}
