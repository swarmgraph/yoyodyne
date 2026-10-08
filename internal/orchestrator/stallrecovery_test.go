package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestAutomaticStallContinuationKeepsUncommittedWorkBeyondTheTailWhileDelayed(t *testing.T) {
	t.Parallel()
	for _, gate := range []string{"intake", "capacity"} {
		t.Run(gate, func(t *testing.T) {
			repository, worktreeRoot, store := restartableFixture(t)
			worktrees := newSweepManager(t, repository, worktreeRoot)
			s := settledRunWithCheckout(t, worktrees, store, 0)
			for i := 1; i <= settledWorktreeTail; i++ {
				settledRunWithCheckout(t, worktrees, store, i)
			}
			s.ProviderSessionID = "stalled-developer-session"
			s.Environmental = &runstate.EnvironmentalRefusal{
				Cause: runstate.CauseProcessVanished, ProviderStop: runstate.ProviderStopStalled,
				RecordedAt: *s.CompletedAt, Settled: true,
			}
			s.Blocker = "The developer's first attempt stopped after its provider stream went silent."
			if err := store.Save(s); err != nil {
				t.Fatal(err)
			}
			s = loadRecoveryRun(t, store, s.RunID)
			continuedAt := s.CompletedAt.Add(time.Duration(settledWorktreeTail+1) * time.Minute)
			// This is an interrupted developer, so the branch alone cannot
			// reconstruct the work its continuation needs.
			unfinished := filepath.Join(s.WorktreePath, "half-done.txt")
			const content = "the developer got this far before its stream went silent\n"
			writeSweepFile(t, unfinished, content)
			docket := &memoryDocket{}
			if _, err := docketerOver(nil, docket).RecordStoppedRun(s); err != nil {
				t.Fatal(err)
			}
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: s.WorkItemID, Title: "Interrupted developer", Status: "blocked"}}
			intake := newIntakeHoldStore(t)
			starts := 0
			continuer := StallContinuer{
				Docket: docket, Runs: store, Intake: intake, Items: tracker, Worktrees: worktrees,
				Capacity: 1, Clock: docketClockAt{at: continuedAt},
				Start: func(_ context.Context, workItemID, runID string) (Outcome, error) {
					starts++
					if workItemID != s.WorkItemID || runID != s.RunID {
						t.Fatalf("continued %s for %s instead of the recorded run", runID, workItemID)
					}
					return Outcome{RunID: runID, WorkItemID: workItemID}, nil
				},
			}
			var live runstate.State
			if gate == "intake" {
				if _, err := intake.Hold(runstate.IntakeHolderOperator, "held for this test", continuedAt); err != nil {
					t.Fatal(err)
				}
			} else {
				live = continuableState()
				live.Status, live.Phase, live.CompletedAt = runstate.StatusRunning, runstate.PhaseDeveloping, nil
				live.StartedAt, live.UpdatedAt = s.UpdatedAt, s.UpdatedAt
				if err := store.Create(live); err != nil {
					t.Fatal(err)
				}
			}
			before, err := store.Triage().Counters(s.WorkItemID)
			if err != nil {
				t.Fatal(err)
			}
			result, err := continuer.Continue(context.Background(), StallContinueRequest{Run: s.RunID})
			if err != nil || result.Continued || (gate == "intake" && result.IntakeHeld == nil) || (gate == "capacity" && result.CapacityFull == nil) {
				t.Fatalf("delayed continuation = %#v, %v", result, err)
			}
			convergence, err := (Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}).Converge(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(convergence.Worktrees) != 1 {
				t.Fatalf("worktree sweeps = %#v; want only the run beyond the tail", convergence.Worktrees)
			}
			checkout := convergence.Worktrees[0]
			if checkout.RunID != s.RunID || checkout.Removed || checkout.Failure != "" || checkout.PreservedWork != "" || !strings.Contains(checkout.Kept, "resume this run in the same AI session") {
				t.Fatalf("checkout sweep = %#v; want the unfinished checkout kept in place", checkout)
			}
			var branch BranchSweep
			for _, swept := range convergence.Branches {
				if swept.RunID == s.RunID {
					branch = swept
				}
			}
			if branch.RunID != s.RunID || branch.Removed || branch.Failure != "" || !strings.Contains(branch.Kept, "resume this run in the same AI session") {
				t.Fatalf("branch sweep = %#v; want the recorded branch kept", branch)
			}
			if !reflect.DeepEqual(s, loadRecoveryRun(t, store, s.RunID)) || starts != 0 {
				t.Fatal("waiting or retention changed the run or spent its continuation")
			}
			if got, err := os.ReadFile(unfinished); err != nil || string(got) != content {
				t.Fatalf("unfinished work = %q, %v; want the original file in its checkout", got, err)
			}
			if got := strings.TrimSpace(gitOutput(t, repository, "rev-parse", s.Branch)); got != s.BaseCommit {
				t.Fatalf("branch = %s; want the recorded commit %s", got, s.BaseCommit)
			}
			// When the delay ends, the existing continuation still takes this
			// run and session with its uncommitted work and no new budget spent.
			if gate == "intake" {
				if _, _, err := intake.Release(); err != nil {
					t.Fatal(err)
				}
			} else {
				live.Status, live.CompletedAt, live.UpdatedAt = runstate.StatusCancelled, &continuedAt, continuedAt
				if err := store.Save(live); err != nil {
					t.Fatal(err)
				}
			}
			result, err = continuer.Continue(context.Background(), StallContinueRequest{Run: s.RunID})
			if err != nil || !result.Continued || starts != 1 {
				t.Fatalf("continuation after the delay = %#v, %v; starts = %d", result, err, starts)
			}
			after := loadRecoveryRun(t, store, s.RunID)
			if after.RunID != s.RunID || after.ProviderSessionID != s.ProviderSessionID || after.WorktreePath != s.WorktreePath || after.Phase != s.Phase || after.RepairAttempts != s.RepairAttempts || after.ReviewRounds != s.ReviewRounds || after.IntegrationRetries != s.IntegrationRetries || after.HarnessStallContinuations() != 1 {
				t.Fatalf("continued state = %#v; want the same run, session and budgets", after)
			}
			spent, err := store.Triage().Counters(s.WorkItemID)
			if err != nil || !reflect.DeepEqual(before, spent) {
				t.Fatalf("triage counters changed: before %#v, after %#v, %v", before, spent, err)
			}
			if got, err := os.ReadFile(unfinished); err != nil || string(got) != content {
				t.Fatalf("continued developer's unfinished work = %q, %v", got, err)
			}
		})
	}
}

func TestAutomaticStallContinuationRetentionEndsWhenTheObligationEnds(t *testing.T) {
	t.Parallel()
	for _, end := range []string{"continuation spent", "continuation refused", "superseded", "management decision"} {
		t.Run(end, func(t *testing.T) {
			s := continuableState()
			s.RepairAttempts = 0
			s.CheckFailure, s.ReviewFindingDetails = nil, nil
			s.Environmental = &runstate.EnvironmentalRefusal{
				Cause: runstate.CauseProcessVanished, ProviderStop: runstate.ProviderStopStalled,
				RecordedAt: *s.CompletedAt, Settled: true,
			}
			h := newUndecidedHarness(t, s)
			if !s.HarnessContinuesStall() {
				t.Fatal("the fixture has no outstanding automatic stall continuation")
			}
			switch end {
			case "continuation spent":
				s.RepairContinuations = []runstate.RepairContinuation{{ByHarness: true, Stall: true}}
			case "continuation refused":
				s.StallContinuationRefused = "the recorded checkout could not be verified"
			case "superseded":
				s.ArtifactsRetiredBy = "run-" + strings.Repeat("b", 32)
			case "management decision":
				if _, err := h.runs.Triage().RecordDecision(context.Background(), s.WorkItemID, triageDecided(runstate.TriageDecisionWait, s.RunID), docketedNow); err != nil {
					t.Fatal(err)
				}
			}
			kept, release := (Reconciler{Store: h.runs}).recoveryNeedsArtifacts(context.Background(), s)
			defer release()
			if kept != "" {
				t.Fatalf("an ended automatic continuation keeps its artifacts: %q", kept)
			}
		})
	}
}
