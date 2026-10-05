package orchestrator

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A continuation is evidence that a decision was carried out even when its
// round was later refunded. The next stop is answered on its own evidence.
func TestASecondStopAfterACarriedOutRepairHasItsOwnMover(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		returned    bool
		stage       bool
		spent       bool
		silent      bool
		wantHarness bool
	}{
		{name: "individual check timeout"},
		{name: "individual check timeout after refund", returned: true},
		{name: "stage bound with allowance", stage: true, wantHarness: true},
		{name: "stage bound after refund", stage: true, returned: true, wantHarness: true},
		{name: "stage bound allowance spent", stage: true, spent: true},
		{name: "silent stream with allowance", silent: true, wantHarness: true},
		{name: "silent stream allowance spent", silent: true, spent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			h := newUndecidedHarness(t, continuableState())
			grantedAgainstTheStoppage(t, h)
			h.docket.close(triage.Key(triage.ClassStoppedRun, docketedRunID), runstate.TriageDecisionRepair, docketedNow)
			carrying := h.carryOut()
			task := theOneOutstanding(t, carrying)
			carried, _, err := carrying.Carry(ctx, task)
			if err != nil || !carried.Carried {
				t.Fatalf("carry repair = %#v, %v", carried, err)
			}
			first := h.reload(t)
			if len(first.RepairContinuations) != 1 {
				t.Fatalf("repair history = %#v", first.RepairContinuations)
			}
			budgets := h.spent(t)
			// Keep the ledger's reservations exactly as they were. A refund on the
			// run's continuation is the incident's distinguishing evidence.
			stopped := first
			stopped.RepairContinuations[0].Returned = test.returned
			ended := first.UpdatedAt.Add(time.Minute)
			stopped.CompletedAt = &ended
			stopped.UpdatedAt = ended
			stopped.Status = runstate.StatusTimedOut
			stopped.Phase = runstate.PhaseChecking
			stopped.StopClass = "check-timeout"
			stopped.Failure = "make race reached its individual check timeout on the repaired change"
			stopped.Blocker = stopped.Failure
			stopped.CheckFailure = nil
			if test.stage {
				stopped.StopClass = "check-stage-bound"
				stopped.Failure = "the whole check stage reached its bound during make race on the repaired change"
				stopped.Blocker = stopped.Failure
				stopped.CheckStage = &runstate.CheckStage{StartedAt: first.UpdatedAt, FinishedAt: &ended, BoundSeconds: 1800, Command: "make race", StoppedAtBound: true}
				if test.spent {
					stopped.CheckStageContinuations = []runstate.CheckStageContinuation{{ContinuedAt: first.UpdatedAt, Reason: "the harness continued the stopped checks"}, {ContinuedAt: first.UpdatedAt, Reason: "the harness continued the stopped checks"}}
				}
			}
			if test.silent {
				stopped.Status = runstate.StatusFailed
				stopped.Phase = runstate.PhaseDeveloping
				stopped.StopClass = "provider-idle"
				stopped.Failure = "the repaired developer session produced no output for five minutes"
				stopped.Blocker = stopped.Failure
				stopped.Environmental = &runstate.EnvironmentalRefusal{Cause: runstate.CauseProcessVanished, Detail: stopped.Failure, ProviderStop: runstate.ProviderStopStalled, RecordedAt: ended, Settled: true}
				if test.spent {
					stopped.RepairContinuations = append(stopped.RepairContinuations, runstate.RepairContinuation{ByHarness: true, Stall: true, ContinuedAt: first.UpdatedAt, Reason: "the harness continued the silent session"})
				}
			}
			h.save(t, stopped)
			docketer := Docketer{Docket: h.docket, Runs: h.runs, Decisions: h.runs.Triage(), Reruns: h.runs.Reruns(), Caps: continueCaps, Triage: docketedTriage, Clock: &steppingClock{now: ended}}
			if _, err := docketer.RecordStoppedRun(stopped); err != nil {
				t.Fatal(err)
			}
			built, err := docketer.Build()
			if err != nil || len(built.Entries) != 1 {
				t.Fatalf("second stop docket = %#v, %v", built, err)
			}
			entry := built.Entries[0]
			if entry.Closed != nil || entry.StopClass != string(stopped.StopClass) || entry.Counters.Standing.Decided || !entry.Counters.Standing.CarriedOut {
				t.Fatalf("second stop inherited the earlier decision: %#v", entry)
			}
			rendered := entry.Render()
			mover := "the development manager"
			if test.wantHarness {
				mover = "the harness"
			}
			for _, want := range []string{stopped.Failure, "earlier repair decision was carried out", "Next mover: " + mover} {
				if !strings.Contains(rendered, want) {
					t.Fatalf("second stop missing %q:\n%s", want, rendered)
				}
			}
			if strings.Contains(rendered, "this stoppage may be handed back on the decision that stands") {
				t.Fatal("the earlier grant still authorizes this stop")
			}
			holds, err := readmodel.HeldForAPerson(ctx, h.runs, h.runs.Triage(), nil)
			if err != nil {
				t.Fatal(err)
			}
			reason, found := holds.Reason(docketedItem)
			if !found || holds.Decided(docketedItem) != test.wantHarness {
				t.Fatalf("second stop hold = %q, want harness %t", reason, test.wantHarness)
			}
			clause := "the development manager decides"
			if test.wantHarness {
				clause = "the harness continues"
			}
			if !strings.Contains(reason, clause) || strings.Contains(reason, "already decided") {
				t.Fatalf("second stop hold says %q", reason)
			}
			carrying.CheckStages = checkStageRecoveryContinuer(h, h.ownership)
			carrying.Stalls = StallContinuer{Docket: h.docket, Runs: h.runs, Intake: h.intake, Items: h.tracker, Worktrees: h.ownership, Capacity: h.capacity}
			if tasks, err := carrying.Outstanding(); err != nil {
				t.Fatal(err)
			} else {
				if test.wantHarness && len(tasks) != 1 {
					t.Fatalf("automatic continuation not offered: %#v", tasks)
				}
				if !test.wantHarness && len(tasks) != 0 {
					t.Fatalf("exhausted allowance still offered work: %#v", tasks)
				}
				for _, task := range tasks {
					if task.Decision == runstate.TriageDecisionRepair {
						t.Fatalf("old repair offered again: %#v", task)
					}
				}
			}
			if after := h.spent(t); !reflect.DeepEqual(after, budgets) {
				t.Fatalf("reading the second stop changed budgets: before %#v, after %#v", budgets, after)
			}
			after := h.reload(t)
			if after.RepairAttempts != first.RepairAttempts || after.ReviewRounds != first.ReviewRounds || after.IntegrationRetries != first.IntegrationRetries {
				t.Fatalf("second stop reset consumed run counters: %#v", after)
			}
			// The action itself refuses a second use, even if someone bypasses the
			// scheduler's offer and the refundable grant still has rounds left.
			if _, err := h.continuer().Continue(ctx, continueRequest()); err == nil || !strings.Contains(err.Error(), "already carried out") {
				t.Fatalf("repeated repair error = %v", err)
			}
			if len(h.started) != 1 {
				t.Fatalf("old decision started %d continuations", len(h.started))
			}
			if after := h.spent(t); !reflect.DeepEqual(after, budgets) {
				t.Fatalf("refused repeat changed budgets: %#v", after)
			}
			if test.stage && test.wantHarness {
				// A decision about this stop still takes precedence over automatic
				// recovery; only the already carried-out repair has lost authority.
				if _, err := h.runs.Triage().RecordDecision(ctx, docketedItem, triageDecided(runstate.TriageDecisionWait, stopped.RunID), ended.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				if tasks, err := carrying.Outstanding(); err != nil || len(tasks) != 0 {
					t.Fatalf("a current wait still offered automatic continuation: %#v, %v", tasks, err)
				}
				holds, err := readmodel.HeldForAPerson(ctx, h.runs, h.runs.Triage(), nil)
				if err != nil || holds.Decided(docketedItem) {
					t.Fatalf("a current wait names the harness as mover: %#v, %v", holds, err)
				}
			}
		})
	}
}
