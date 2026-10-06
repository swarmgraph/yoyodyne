package orchestrator

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func missingRecoveryState() runstate.State {
	s := continuableState()
	s.HarnessCommit = strings.Repeat("c", 40)
	s.WorktreeRemoved = true
	s.WorktreeSweptAt = &s.UpdatedAt
	return s
}

func TestRepairRestoresAStoppedIntegratedRunWithoutItsEarlierApproval(t *testing.T) {
	t.Parallel()
	s := missingRecoveryState()
	s.Phase = runstate.PhaseCleaningUp
	s.CheckFailure = nil
	s.ReviewDecision, s.ReviewApproves = runstate.ReviewApprove, runstate.ApprovesImplementation
	s.ProviderModel, s.ReviewModel = "opus", "opus"
	s.ReviewSessionID = "independent-reviewer"
	s.Integration = &runstate.Integration{TargetBranch: s.TargetBranch, SourceCommit: s.HarnessCommit, TargetCommit: s.HarnessCommit, PreviousTargetCommit: s.BaseCommit}
	s.ChecksPassed = &runstate.ChecksPassed{Content: orchestratortest.PartialContentIdentity, Attempt: s.RepairAttempts, Commit: s.HarnessCommit, At: docketedNow}
	// Store the pre-fix shape through the normal store, then carry out its
	// recorded repair decision without rewriting or migrating that record.
	h := newContinueHarness(t, s)
	w := &orchestratortest.RecoveryCheckout{Ownership: h.ownership, Branch: true}
	w.BeforeRestore = func() {
		stopped := h.reload(t)
		if stopped.Integration != nil || stopped.ReviewDecision != "" || stopped.ChecksPassed != nil || !stopped.Status.Terminal() {
			t.Fatalf("restoration retained promotion authority: %#v", stopped)
		}
	}
	c := h.continuer()
	c.Worktrees, c.Remains = w, w
	result, err := c.Continue(context.Background(), continueRequest())
	if err != nil || !result.Continued || !result.WorktreeRestored || len(h.started) != 1 || h.started[0].runID != s.RunID {
		t.Fatalf("repair = %#v, %v; starts = %#v", result, err, h.started)
	}
	after := h.reload(t)
	if after.Branch != s.Branch || after.HarnessCommit != s.HarnessCommit || after.ProviderSessionID != s.ProviderSessionID || after.ReviewRounds != s.ReviewRounds || after.RepairAttempts != s.RepairAttempts+1 || after.Phase != runstate.PhaseDeveloping || after.Integration != nil || after.ReviewDecision != "" || after.ReviewApproves != "" {
		t.Fatalf("repair did not preserve work and require a new review: %#v", after)
	}
	run := &activeRun{state: after}
	if err := run.integrationEarned(context.Background()); !errors.Is(err, ErrIntegrationUnearned) {
		t.Fatalf("promotion without fresh checks = %v", err)
	}
	// Even after new checks pass, the earlier approval cannot authorize this
	// attempt: the independent reviewer must return a new verdict.
	passed := *s.ChecksPassed
	passed.Attempt = after.RepairAttempts
	run.state.ChecksPassed = &passed
	if err := run.integrationEarned(context.Background()); !errors.Is(err, ErrIntegrationUnearned) || !strings.Contains(err.Error(), "rather than an approval") {
		t.Fatalf("promotion without fresh review = %v", err)
	}
}

func TestRepairRestoresTheRecordedRunWithoutResettingItsSpend(t *testing.T) {
	t.Parallel()
	s := missingRecoveryState()
	s.CheckFailure = nil
	s.ChecksPassed = &runstate.ChecksPassed{Content: "old-content", Attempt: s.RepairAttempts, Commit: s.HarnessCommit, At: docketedNow}
	h := newContinueHarness(t, s)
	decision, err := h.runs.Triage().Counters(s.WorkItemID)
	if err != nil {
		t.Fatal(err)
	}
	w := &orchestratortest.RecoveryCheckout{Ownership: h.ownership, Branch: true}
	w.BeforeRestore = func() {
		stopped := h.reload(t)
		if stopped.ChecksPassed != nil || !stopped.Status.Terminal() || stopped.RepairAttempts != s.RepairAttempts {
			t.Fatalf("before restoration = %#v; want verification cleared while the run stays stopped and unspent", stopped)
		}
	}
	c := h.continuer()
	c.Worktrees, c.Remains = w, w
	result, err := c.Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Continued || !result.WorktreeRestored || w.Restores != 1 || len(h.started) != 1 || h.started[0].runID != s.RunID {
		t.Fatalf("result = %#v, restores = %d, starts = %#v", result, w.Restores, h.started)
	}
	after := h.reload(t)
	if after.RunID != s.RunID || after.ProviderSessionID != s.ProviderSessionID || after.ReviewRounds != s.ReviewRounds || after.RepairAttempts != s.RepairAttempts+1 || after.WorktreeRemoved || after.WorktreeSweptAt != nil || after.ChecksPassed != nil {
		t.Fatalf("continued state = %#v; identity, session and spend must survive restoration", after)
	}
	landed, err := h.runs.Triage().Counters(s.WorkItemID)
	if err != nil || !reflect.DeepEqual(decision.Decisions, landed.Decisions) || decision.GrantedRounds != landed.GrantedRounds || decision.RepairGrants != landed.RepairGrants {
		t.Fatalf("restoration changed the standing decision or its grant: %#v, %v", landed, err)
	}
	if !strings.Contains(result.Render(), "restored the recorded checkout") {
		t.Fatal(result.Render())
	}
}

func TestRepairRestorationRefusesWithoutSpendingTheContinuation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*runstate.State, *orchestratortest.RecoveryCheckout)
	}{
		{"branch missing", func(_ *runstate.State, w *orchestratortest.RecoveryCheckout) { w.Branch = false }},
		{"path conflict", func(_ *runstate.State, w *orchestratortest.RecoveryCheckout) {
			w.RestoreErr = errors.New("worktree path already exists")
		}},
		{"revision not verifiable", func(_ *runstate.State, w *orchestratortest.RecoveryCheckout) {
			w.RestoreErr = errors.New("branch is not at the recorded commit")
		}},
		{"no recorded commit", func(s *runstate.State, _ *orchestratortest.RecoveryCheckout) { s.HarnessCommit = "" }},
		{"captured uncommitted work", func(s *runstate.State, _ *orchestratortest.RecoveryCheckout) {
			s.PreservedWorkRef = gitworktree.PreservedWorkRef(s.RunID)
		}},
		{"unfinished developer", func(s *runstate.State, _ *orchestratortest.RecoveryCheckout) { s.Phase = runstate.PhaseDeveloping }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := missingRecoveryState()
			w := &orchestratortest.RecoveryCheckout{Branch: true}
			tc.change(&s, w)
			h := newContinueHarness(t, s)
			w.Ownership = h.ownership
			c := h.continuer()
			c.Worktrees, c.Remains = w, w
			result, err := c.Continue(context.Background(), continueRequest())
			if err == nil || result.Continued || h.tracker.Claimed || len(h.started) != 0 {
				t.Fatalf("result = %#v, error = %v; want a refusal", result, err)
			}
			stopped := h.reload(t)
			if stopped.RepairAttempts != s.RepairAttempts || len(stopped.RepairContinuations) != 0 || stopped.Blocker != s.Blocker || !stopped.Status.Terminal() || h.carried(t) != 0 {
				t.Fatalf("refusal spent or superseded the stopped run: %#v", stopped)
			}
		})
	}
}

// The process died after Git restored the directory, before the flags were
// saved. The next invocation finds the checkout and finishes the same decision
// once, without recreating it or regaining verification credit.
func TestRepairRestartAfterCheckoutRestorationUsesTheSameDecisionOnce(t *testing.T) {
	t.Parallel()
	s := missingRecoveryState()
	s.CheckoutRestorePending = true
	h := newContinueHarness(t, s)
	w := &orchestratortest.RecoveryCheckout{Ownership: h.ownership, Branch: true, Present: true}
	c := h.continuer()
	c.Worktrees, c.Remains = w, w
	result, err := c.Continue(context.Background(), continueRequest())
	if err != nil || !result.Continued || w.Restores != 0 {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	state := h.reload(t)
	if state.CheckoutRestorePending || state.WorktreeRemoved || state.WorktreeSweptAt != nil || len(state.RepairContinuations) != 1 || state.RepairAttempts != s.RepairAttempts+1 {
		t.Fatalf("state = %#v", state)
	}
	// A later stoppage cannot carry the same decision out again.
	state.Status, state.Phase, state.Blocker = s.Status, s.Phase, s.Blocker
	state.CompletedAt = s.CompletedAt
	h.save(t, state)
	if _, err := c.Continue(context.Background(), continueRequest()); err == nil || len(h.started) != 1 {
		t.Fatalf("second carry-out error = %v, starts = %#v", err, h.started)
	}
}

func TestRepairRestartRefusesAPartlyRestoredCheckout(t *testing.T) {
	t.Parallel()
	s := missingRecoveryState()
	s.CheckoutRestorePending = true
	h := newContinueHarness(t, s)
	w := &orchestratortest.RecoveryCheckout{Ownership: h.ownership, Branch: true, Present: true, Dirty: true}
	c := h.continuer()
	c.Worktrees, c.Remains = w, w
	if _, err := c.Continue(context.Background(), continueRequest()); err == nil || !strings.Contains(err.Error(), "unfinished") {
		t.Fatalf("partial restoration error = %v", err)
	}
	if len(h.started) != 0 || h.tracker.Claimed || !reflect.DeepEqual(s, h.reload(t)) {
		t.Fatal("partial restoration was continued or changed")
	}
}

func TestMissingCheckoutRecoveryWaitsForIntakeAndKeepsCheckStageSpend(t *testing.T) {
	t.Parallel()
	s := missingRecoveryState()
	s.Status, s.Phase = runstate.StatusTimedOut, runstate.PhaseChecking
	s.Blocker = ""
	s.Failure = "check stage reached its bound"
	s.ReviewFindingDetails, s.CheckFailure = nil, nil
	s.ReviewFindings, s.ReviewDecision, s.ReviewSummary = 0, "", ""
	s.CheckStage = &runstate.CheckStage{StartedAt: s.StartedAt, BoundSeconds: 1800, Command: "make race", StoppedAtBound: true, ElapsedSeconds: 1800}
	h := newContinueHarness(t, s)
	w := &orchestratortest.RecoveryCheckout{Ownership: h.ownership, Branch: true}
	c := h.continuer()
	c.Worktrees, c.Remains = w, w
	if _, err := h.intake.Hold(runstate.IntakeHolderOperator, "wait", docketedNow); err != nil {
		t.Fatal(err)
	}
	result, err := c.Continue(context.Background(), continueRequest())
	if err != nil || result.IntakeHeld == nil || w.Restores != 0 {
		t.Fatalf("held recovery = %#v, %v", result, err)
	}
	if !reflect.DeepEqual(s, h.reload(t)) {
		t.Fatal("held recovery mutated the run")
	}
	if _, _, err := h.intake.Release(); err != nil {
		t.Fatal(err)
	}
	result, err = c.Continue(context.Background(), continueRequest())
	if err != nil || !result.WorktreeRestored || !result.Checks || result.ResumesAt != runstate.PhaseChecking {
		t.Fatalf("recovery = %#v, %v", result, err)
	}
	continued := h.reload(t)
	if continued.RepairAttempts != s.RepairAttempts || continued.ReviewRounds != s.ReviewRounds || !reflect.DeepEqual(continued.CheckStage, s.CheckStage) {
		t.Fatalf("stage recovery reset spend: %#v", continued)
	}
}

func TestCheckoutSweepKeepsOutstandingAndRefusedRecovery(t *testing.T) {
	t.Parallel()
	s := continuableState()
	h := newContinueHarness(t, s)
	r := Reconciler{Store: h.runs}
	// No worktree manager is supplied: trying to retire anything would panic.
	sweep, swept := r.sweepWorktree(context.Background(), s)
	if !swept || sweep.Kept == "" || sweep.Removed {
		t.Fatalf("sweep = %#v", sweep)
	}
	before := h.reload(t)
	if !reflect.DeepEqual(s, before) {
		t.Fatal("retention changed the run")
	}
	// The grant has not been carried out, even though a gate previously refused.
	if _, err := h.runs.Triage().RecordCarryOutRefusal(context.Background(), s.WorkItemID, runstate.TriageCarryOut{RunID: s.RunID, Decision: runstate.TriageDecisionRepair, Gate: runstate.TriageGatePreservedWork, Refusal: "checkout missing", Clears: "restore the recorded checkout"}, docketedNow); err != nil {
		t.Fatal(err)
	}
	sweep, swept = r.sweepWorktree(context.Background(), s)
	if !swept || sweep.Kept == "" || sweep.Removed {
		t.Fatalf("refused sweep = %#v", sweep)
	}
	branch, swept := r.sweepBranch(context.Background(), s, nil)
	if !swept || branch.Kept == "" || branch.Removed {
		t.Fatalf("branch sweep = %#v", branch)
	}
}
