package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Cross-role recurring passes (yoyodyne-ifd.428.56) had a standing automatic
// check continuation and a surviving branch when its checkout was found gone.
// This fixture models that recorded obligation without touching the real run.
func crossRoleCheckStageState() runstate.State {
	s := continuableState()
	s.RunID = "run-430d92d27924bcc65c0c2b75534ac38d"
	s.WorkItemID = "yoyodyne-ifd.428.56"
	s.WorkItemTitle = "Cross-role recurring passes"
	s.Branch = "yoyodyne/yoyodyne-ifd-428-56/430d92d2"
	s.WorktreePath = "/state/worktrees/yoyodyne-ifd-428-56-430d92d2"
	s.HarnessCommit = strings.Repeat("c", 40)
	s.Status, s.Phase = runstate.StatusTimedOut, runstate.PhaseChecking
	s.CheckFailure = nil
	s.ReviewSummary, s.ReviewFindings, s.ReviewFindingDetails = "", 0, nil
	s.Failure = "the check stage reached its execution.check_stage_timeout bound during make race"
	s.Blocker = "The check stage was stopped at its bound."
	s.CheckStage = &runstate.CheckStage{StartedAt: s.StartedAt, FinishedAt: s.CompletedAt, BoundSeconds: 1800, Command: "make race", StoppedAtBound: true}
	s.IntegrationRetries = 1
	s.CheckStageContinuations = []runstate.CheckStageContinuation{{Command: "make race", ContinuedAt: s.StartedAt.Add(time.Minute), Reason: "continue the same run at its checks"}}
	return s
}

func checkStageRecoveryContinuer(h *continueHarness, w RepairWorktrees) CheckStageContinuer {
	return CheckStageContinuer{
		Docket: h.docket, Runs: h.runs, Intake: h.intake, Items: h.tracker,
		Worktrees: w, Capacity: h.capacity, Clock: docketClock{},
		Start: func(_ context.Context, workItemID, runID string) (Outcome, error) {
			h.started = append(h.started, continuedRun{workItemID: workItemID, runID: runID})
			return h.outcome, h.failure
		},
	}
}

func loadRecoveryRun(t *testing.T, store *runstate.Store, runID string) runstate.State {
	t.Helper()
	s, err := store.Load(runID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAutomaticCheckContinuationKeepsArtifactsBeyondTheTailWhileDelayed(t *testing.T) {
	t.Parallel()
	for _, gate := range []string{"intake", "capacity"} {
		t.Run(gate, func(t *testing.T) {
			s := crossRoleCheckStageState()
			h := newUndecidedHarness(t, s)
			if gate == "intake" {
				if _, err := h.intake.Hold(runstate.IntakeHolderOperator, "held for this test", docketedNow); err != nil {
					t.Fatal(err)
				}
			} else {
				h.capacity = 1
				live := continuableState()
				live.Status, live.Phase, live.CompletedAt = runstate.StatusRunning, runstate.PhaseDeveloping, nil
				if err := h.runs.Create(live); err != nil {
					t.Fatal(err)
				}
			}
			w := &orchestratortest.RecoveryCheckout{Ownership: h.ownership, Branch: true, Present: true}
			c := checkStageRecoveryContinuer(h, w)
			result, err := c.Continue(context.Background(), CheckStageContinueRequest{Run: s.RunID})
			if err != nil || result.Continued || (gate == "intake" && result.IntakeHeld == nil) || (gate == "capacity" && result.CapacityFull == nil) {
				t.Fatalf("delayed continuation = %#v, %v", result, err)
			}
			// Enough newer settled runs put this checkout outside the retained
			// tail. The sweep must still keep both artifacts for its obligation.
			recorded := []runstate.State{s}
			for i := range settledWorktreeTail {
				newer := continuableState()
				newer.RunID = fmt.Sprintf("run-%032x", i+1)
				completed := s.CompletedAt.Add(time.Duration(i+1) * time.Minute)
				newer.CompletedAt = &completed
				recorded = append(recorded, newer)
			}
			candidates := sweepableWorktrees(recorded)
			if len(candidates) != 1 || candidates[0].RunID != s.RunID {
				t.Fatalf("sweep candidates = %#v", candidates)
			}
			r := Reconciler{Store: h.runs}
			// No manager: an attempt to retire instead of retaining would panic.
			checkout, swept := r.sweepWorktree(context.Background(), candidates[0])
			if !swept || checkout.Removed || !strings.Contains(checkout.Kept, "automatic check continuation") {
				t.Fatalf("checkout sweep = %#v", checkout)
			}
			branch, swept := r.sweepBranch(context.Background(), s, nil)
			if !swept || branch.Removed || !strings.Contains(branch.Kept, "automatic check continuation") {
				t.Fatalf("branch sweep = %#v", branch)
			}
			if !reflect.DeepEqual(s, loadRecoveryRun(t, h.runs, s.RunID)) || len(h.started) != 0 || w.Restores != 0 {
				t.Fatal("waiting or retention changed the run or spent a continuation")
			}
		})
	}
}

func TestAutomaticCheckContinuationRetentionEndsWhenTheObligationEnds(t *testing.T) {
	t.Parallel()
	s := crossRoleCheckStageState()
	h := newUndecidedHarness(t, s)
	r := Reconciler{Store: h.runs}
	for _, end := range []string{"continuations spent", "continuation refused", "superseded"} {
		t.Run(end, func(t *testing.T) {
			ended := s
			switch end {
			case "continuations spent":
				ended.CheckStageContinuations = append(append([]runstate.CheckStageContinuation{}, s.CheckStageContinuations...), s.CheckStageContinuations[0])
			case "continuation refused":
				ended.CheckStageContinuationRefused = "the recorded branch could not be verified"
			case "superseded":
				ended.ArtifactsRetiredBy = docketedRunID
			}
			kept, release := r.recoveryNeedsArtifacts(context.Background(), ended)
			defer release()
			if kept != "" || ended.HarnessContinuesCheckStage() {
				t.Fatalf("an ended continuation keeps its artifacts: %q", kept)
			}
		})
	}
}

func TestDocketRefreshesAutomaticCheckRecoveryFromTheRun(t *testing.T) {
	t.Parallel()
	s := crossRoleCheckStageState()
	s.WorktreeRemoved, s.BranchRemoved = true, true
	s.WorktreeSweptAt, s.BranchSweptAt = &s.UpdatedAt, &s.UpdatedAt
	h := newUndecidedHarness(t, s)
	h.docket.entries[0].HarnessContinuesChecks = false
	d := docketerOver([]runstate.State{s}, h.docket)
	d.Remains = &orchestratortest.RecoveryCheckout{Branch: true}
	built, err := d.Build()
	if err != nil || len(built.Entries) != 1 || !built.Entries[0].HarnessContinuesChecks || built.Entries[0].Artifacts.Found.WorktreeThere {
		t.Fatalf("restorable docket = %#v, %v", built, err)
	}
	// The projection changes what the role reads, leaving the historical entry
	// as it was written and applying a later refusal from the same run.
	if h.docket.entries[0].HarnessContinuesChecks {
		t.Fatal("refresh rewrote the docket log")
	}
	s.CheckStageContinuationRefused = "the recorded branch changed"
	d.Runs = recordedRuns{states: []runstate.State{s}}
	built, err = d.Build()
	if err != nil || len(built.Entries) != 1 || built.Entries[0].HarnessContinuesChecks || !strings.Contains(built.Entries[0].CheckStageStop, "development manager's decision") {
		t.Fatalf("refused docket = %#v, %v", built, err)
	}
}

func TestAutomaticCheckContinuationRestoresTheRecordedCrossRoleRunAcrossRestart(t *testing.T) {
	t.Parallel()
	for _, removed := range []bool{false, true} {
		t.Run(fmt.Sprintf("removal recorded %t", removed), func(t *testing.T) {
			s := crossRoleCheckStageState()
			s.WorktreeRemoved, s.BranchRemoved = removed, removed
			if removed {
				s.WorktreeSweptAt, s.BranchSweptAt = &s.UpdatedAt, &s.UpdatedAt
			}
			s.ChecksPassed = &runstate.ChecksPassed{Content: "old-content", Attempt: s.RepairAttempts, Commit: s.HarnessCommit, At: docketedNow}
			h := newUndecidedHarness(t, s)
			// A new process reads the same durable run and budgets.
			root := filepath.Dir(filepath.Dir(filepath.Dir(h.runs.Root())))
			var err error
			h.runs, err = runstate.NewStore(root, s.ProductID)
			if err != nil {
				t.Fatal(err)
			}
			before, err := h.runs.Triage().Counters(s.WorkItemID)
			if err != nil {
				t.Fatal(err)
			}
			w := &orchestratortest.RecoveryCheckout{Ownership: h.ownership, Branch: true}
			w.BeforeRestore = func() {
				durable := loadRecoveryRun(t, h.runs, s.RunID)
				if durable.ChecksPassed != nil || !durable.CheckoutRestorePending || !durable.Status.Terminal() || !reflect.DeepEqual(durable.CheckStageContinuations, s.CheckStageContinuations) {
					t.Fatalf("before restoration = %#v", durable)
				}
			}
			c := checkStageRecoveryContinuer(h, w)
			if due, err := c.Due(s.RunID); err != nil || !due {
				t.Fatalf("restorable continuation due = %t, %v", due, err)
			}
			result, err := c.Continue(context.Background(), CheckStageContinueRequest{Run: s.RunID})
			if err != nil || !result.Continued || !result.WorktreeRestored || w.Restores != 1 || len(h.started) != 1 || h.started[0].runID != s.RunID {
				t.Fatalf("restored continuation = %#v, %v; starts = %#v", result, err, h.started)
			}
			after := loadRecoveryRun(t, h.runs, s.RunID)
			if after.ProviderSessionID != s.ProviderSessionID || after.RepairAttempts != s.RepairAttempts || after.ReviewRounds != s.ReviewRounds || after.IntegrationRetries != s.IntegrationRetries || !reflect.DeepEqual(after.CheckStage, s.CheckStage) || len(after.CheckStageContinuations) != len(s.CheckStageContinuations)+1 || !reflect.DeepEqual(after.CheckStageContinuations[:len(s.CheckStageContinuations)], s.CheckStageContinuations) {
				t.Fatalf("restoration reset identity or consumed budgets: %#v", after)
			}
			if after.ChecksPassed != nil || after.CheckoutRestorePending || after.WorktreeRemoved || after.BranchRemoved || after.WorktreeSweptAt != nil || after.BranchSweptAt != nil || after.Phase != runstate.PhaseChecking || after.Status != runstate.StatusRunning {
				t.Fatalf("restored run = %#v", after)
			}
			counters, err := h.runs.Triage().Counters(s.WorkItemID)
			if err != nil || !reflect.DeepEqual(before, counters) {
				t.Fatalf("triage budgets changed: %#v, %v", counters, err)
			}
			recorded, err := h.runs.Recorded()
			if err != nil || len(recorded) != 1 {
				t.Fatalf("restoration created a second run: %#v, %v", recorded, err)
			}
		})
	}
}

func TestAutomaticCheckRestorationRestartVerifiesTheCompletedCheckout(t *testing.T) {
	t.Parallel()
	for _, dirty := range []bool{false, true} {
		t.Run(fmt.Sprintf("partly restored %t", dirty), func(t *testing.T) {
			s := crossRoleCheckStageState()
			s.WorktreeRemoved, s.CheckoutRestorePending = true, true
			s.WorktreeSweptAt = &s.UpdatedAt
			h := newUndecidedHarness(t, s)
			w := &orchestratortest.RecoveryCheckout{Ownership: h.ownership, Branch: true, Present: true, Dirty: dirty}
			c := checkStageRecoveryContinuer(h, w)
			result, err := c.Continue(context.Background(), CheckStageContinueRequest{Run: s.RunID})
			after := loadRecoveryRun(t, h.runs, s.RunID)
			if w.Restores != 0 {
				t.Fatal("restart repeated an already completed filesystem restoration")
			}
			if dirty {
				if err == nil || result.Refused == "" || !after.CheckoutRestorePending || len(h.started) != 0 || h.tracker.Claimed || !reflect.DeepEqual(after.CheckStageContinuations, s.CheckStageContinuations) {
					t.Fatalf("partial checkout continued: %#v, %v", result, err)
				}
			} else if err != nil || !result.Continued || !result.WorktreeRestored || after.CheckoutRestorePending || after.WorktreeRemoved || len(h.started) != 1 {
				t.Fatalf("completed restoration refused: %#v, %v", result, err)
			}
		})
	}
}

func TestAutomaticCheckRestorationRefusesUnrecoverableStateWithoutSpending(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*runstate.State, *orchestratortest.RecoveryCheckout)
	}{
		{"branch missing", func(_ *runstate.State, w *orchestratortest.RecoveryCheckout) { w.Branch = false }},
		{"path conflict", func(_ *runstate.State, w *orchestratortest.RecoveryCheckout) {
			w.RestoreErr = errors.New("worktree path already exists")
		}},
		{"revision changed", func(_ *runstate.State, w *orchestratortest.RecoveryCheckout) {
			w.RestoreErr = errors.New("branch is not at the recorded commit")
		}},
		{"unverifiable repository", func(_ *runstate.State, w *orchestratortest.RecoveryCheckout) {
			w.SurviveErr = errors.New("repository could not be read")
		}},
		{"no recorded commit", func(s *runstate.State, _ *orchestratortest.RecoveryCheckout) { s.HarnessCommit = "" }},
		{"captured uncommitted work", func(s *runstate.State, _ *orchestratortest.RecoveryCheckout) {
			s.PreservedWorkRef = gitworktree.PreservedWorkRef(s.RunID)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := crossRoleCheckStageState()
			s.WorktreeRemoved = true
			s.WorktreeSweptAt = &s.UpdatedAt
			w := &orchestratortest.RecoveryCheckout{Branch: true}
			tc.change(&s, w)
			h := newUndecidedHarness(t, s)
			w.Ownership = h.ownership
			c := checkStageRecoveryContinuer(h, w)
			result, err := c.Continue(context.Background(), CheckStageContinueRequest{Run: s.RunID})
			after := loadRecoveryRun(t, h.runs, s.RunID)
			if err == nil || result.Continued || result.Refused == "" || !after.Status.Terminal() || h.tracker.Claimed || len(h.started) != 0 {
				t.Fatalf("unrecoverable state continued: %#v, %v", result, err)
			}
			if after.RepairAttempts != s.RepairAttempts || after.ReviewRounds != s.ReviewRounds || after.IntegrationRetries != s.IntegrationRetries || !reflect.DeepEqual(after.CheckStageContinuations, s.CheckStageContinuations) || after.HarnessContinuesCheckStage() || !after.WorktreeRemoved {
				t.Fatalf("refusal lost the stop or spent a continuation: %#v", after)
			}
		})
	}
}
