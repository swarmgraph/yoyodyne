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

// A run stops, the development manager decides a repair, the harness carries it
// out, and the run stops again. The later stop is classified by the recovery
// rules as a first one would be: the earlier decision reads as carried out and
// never as owed, the stop the rules continue is the harness's with nothing asked
// of anybody, and every other stop waits on her with its own cause. Explicit
// Codex effort's run (yoyodyne-ifd.435.15) had its repair handed back as an
// owed decision three times after its checks timed out, because the round the
// timeout gave back made the decision read as never carried out.
func TestASecondStopAfterACarriedOutRepairIsClassifiedAfresh(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		// returned is the round the second stop gave back, as a stop from
		// outside the work does.
		returned bool
		// stage is a second stop at the check stage's bound; spent is its
		// reruns on this candidate already used up.
		stage, spent bool
		wantHarness  bool
	}{
		{name: "review still asks for repair"},
		{name: "check stage bound with reruns left", stage: true, wantHarness: true},
		{name: "check stage bound with reruns left after the round was given back", stage: true, returned: true, wantHarness: true},
		{name: "check stage bound with reruns spent", stage: true, spent: true, returned: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			h := newUndecidedHarness(t, continuableState())
			grantedAgainstTheStoppage(t, h)
			h.docket.close(triage.Key(triage.ClassStoppedRun, docketedRunID), runstate.TriageDecisionRepair, docketedNow)
			carrying := h.carryOut()
			task := theOneOutstanding(t, carrying)
			if carried, _, err := carrying.Carry(ctx, task); err != nil || !carried.Carried {
				t.Fatalf("carrying the repair out = %#v, %v", carried, err)
			}
			first := h.reload(t)
			if len(first.RepairContinuations) != 1 {
				t.Fatalf("repair continuations = %#v, want the one decided", first.RepairContinuations)
			}
			budgets := h.spent(t)

			stopped := first
			stopped.RepairContinuations = append([]runstate.RepairContinuation(nil), first.RepairContinuations...)
			stopped.RepairContinuations[0].Returned = test.returned
			ended := first.UpdatedAt.Add(time.Minute)
			stopped.CompletedAt, stopped.UpdatedAt = &ended, ended
			stopped.Status, stopped.Phase = runstate.StatusFailed, runstate.PhaseReviewing
			stopped.StopClass = runstate.StopClass("repair-budget")
			stopped.Failure = "the independent reviewer still required repair after the granted attempts"
			stopped.Blocker = "Yoyodyne stopped this item: " + stopped.Failure
			if test.stage {
				stopped.Status, stopped.Phase = runstate.StatusTimedOut, runstate.PhaseChecking
				stopped.StopClass = runstate.StopClass("check-stage-bound")
				stopped.CheckFailure = nil
				stopped.Failure = "the check stage reached its execution.check_stage_timeout bound during make race on the repaired change"
				stopped.Blocker = "The check stage was stopped at its bound."
				stopped.CheckStage = &runstate.CheckStage{StartedAt: first.UpdatedAt, FinishedAt: &ended, BoundSeconds: 1800, Command: "make race", StoppedAtBound: true}
				if test.spent {
					rerun := runstate.CheckStageContinuation{Command: "make race", ContinuedAt: first.UpdatedAt, Reason: "the harness ran the stopped checks again", Attempt: stopped.RepairAttempts, Commit: stopped.HarnessCommit}
					stopped.CheckStageContinuations = []runstate.CheckStageContinuation{rerun, rerun}
				}
			}
			h.save(t, stopped)
			if got := stopped.HarnessContinuesCheckStage(); got != test.wantHarness {
				t.Fatalf("the recovery rules continue this stop: %t, want %t", got, test.wantHarness)
			}
			docketer := Docketer{Docket: h.docket, Runs: h.runs, Decisions: h.runs.Triage(), Reruns: h.runs.Reruns(), Caps: continueCaps, Triage: docketedTriage, Clock: &steppingClock{now: ended}}
			if _, err := docketer.RecordStoppedRun(stopped); err != nil {
				t.Fatal(err)
			}
			built, err := docketer.Build()
			if err != nil || len(built.Entries) != 1 {
				t.Fatalf("the second stop's docket = %#v, %v", built, err)
			}

			// The docket: the later stop is undecided and carries its own cause; the
			// earlier repair is recorded as carried out and not as owed.
			entry := built.Entries[0]
			standing := entry.Counters.Standing
			if entry.Closed != nil || entry.StopClass != string(stopped.StopClass) || standing.Decided || !standing.CarriedOut || entry.Counters.AwaitingCarryOut() {
				t.Fatalf("the second stop inherited the earlier decision: closed %#v, cause %q, standing %#v", entry.Closed, entry.StopClass, standing)
			}
			rendered := entry.Render()
			mover := "Next mover: you — nothing the harness has still to carry out"
			if test.wantHarness {
				mover = "Next mover: the harness — load stopped this run's check stage"
			}
			for _, want := range []string{"The repair decided earlier about this run was carried out", mover} {
				if !strings.Contains(rendered, want) {
					t.Fatalf("the second stop's entry does not say %q:\n%s", want, rendered)
				}
			}
			for _, refused := range []string{"has not been carried out, so what is outstanding is the carry-out", "may be handed back on the decision that stands"} {
				if strings.Contains(rendered, refused) {
					t.Fatalf("the second stop's entry still says the earlier decision is owed (%q):\n%s", refused, rendered)
				}
			}

			// The hold says which of the two applies, and never that the earlier
			// decision is waiting to be carried out.
			holds, err := readmodel.HeldForAPerson(ctx, h.runs, h.runs.Triage(), nil)
			if err != nil {
				t.Fatal(err)
			}
			reason, found := holds.Reason(docketedItem)
			if !found || holds.Decided(docketedItem) != test.wantHarness {
				t.Fatalf("the second stop's hold = %q (harness's: %t), want the harness's %t", reason, holds.Decided(docketedItem), test.wantHarness)
			}
			clause := "the development manager decides what happens to it"
			if test.wantHarness {
				clause = "the harness continues it itself"
			}
			if !strings.Contains(reason, clause) || !strings.Contains(reason, "was carried out") || strings.Contains(reason, "already decided what happens to it") {
				t.Fatalf("the second stop's hold says %q, want %q and the earlier repair carried out", reason, clause)
			}

			// The carry-out: the earlier repair is never offered again; the
			// recovery rules' own continuation is, where they give one.
			carrying.CheckStages = checkStageRecoveryContinuer(h, h.ownership)
			tasks, err := carrying.Outstanding()
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range tasks {
				if task.Decision == runstate.TriageDecisionRepair {
					t.Fatalf("the carried-out repair was offered again: %#v", task)
				}
			}
			if test.wantHarness != (len(tasks) == 1 && tasks[0].Decision == DecisionContinueChecks) || (!test.wantHarness && len(tasks) != 0) {
				t.Fatalf("outstanding = %#v, want the harness's check continuation %t and nothing else", tasks, test.wantHarness)
			}
			// And the repair action refuses a repeat asked for directly: with spent
			// reruns the shared rerun bound refuses it first.
			if _, err := h.continuer().Continue(ctx, continueRequest()); err == nil || (!test.spent && !strings.Contains(err.Error(), "was already carried out")) {
				t.Fatalf("repeating the carried-out repair = %v, want it refused", err)
			}
			if len(h.started) != 1 {
				t.Fatalf("started = %#v, want the one continuation the decision bought", h.started)
			}

			// Nothing here reset or spent a budget or a counter.
			if after := h.spent(t); !reflect.DeepEqual(after.Decisions, budgets.Decisions) || after.RepairGrants != budgets.RepairGrants ||
				after.GrantedRounds != budgets.GrantedRounds || after.CommittedRounds != budgets.CommittedRounds || after.ReviewRounds != budgets.ReviewRounds || after.Reruns != budgets.Reruns {
				t.Fatalf("budgets changed: before %#v, after %#v", budgets, after)
			}
			after := h.reload(t)
			if after.RepairAttempts != stopped.RepairAttempts || after.ReviewRounds != stopped.ReviewRounds || after.IntegrationRetries != stopped.IntegrationRetries ||
				len(after.RepairContinuations) != len(stopped.RepairContinuations) || len(after.CheckStageContinuations) != len(stopped.CheckStageContinuations) {
				t.Fatalf("the run's counters moved: %#v", after)
			}
		})
	}
}

// A decision about the later stop itself still stands over the recovery rules'
// own continuation: only the carried-out repair has stopped deciding anything.
func TestADecisionAboutTheSecondStopStillStandsOverTheHarness(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := newUndecidedHarness(t, continuableState())
	grantedAgainstTheStoppage(t, h)
	h.docket.close(triage.Key(triage.ClassStoppedRun, docketedRunID), runstate.TriageDecisionRepair, docketedNow)
	carrying := h.carryOut()
	if carried, _, err := carrying.Carry(ctx, theOneOutstanding(t, carrying)); err != nil || !carried.Carried {
		t.Fatalf("carrying the repair out = %#v, %v", carried, err)
	}
	stopped := h.reload(t)
	ended := stopped.UpdatedAt.Add(time.Minute)
	stopped.CompletedAt, stopped.UpdatedAt = &ended, ended
	stopped.Status, stopped.Phase, stopped.CheckFailure = runstate.StatusTimedOut, runstate.PhaseChecking, nil
	stopped.Failure = "the check stage reached its bound during make race"
	stopped.CheckStage = &runstate.CheckStage{StartedAt: ended.Add(-time.Minute), FinishedAt: &ended, BoundSeconds: 1800, Command: "make race", StoppedAtBound: true}
	h.save(t, stopped)
	docketer := Docketer{Docket: h.docket, Runs: h.runs, Decisions: h.runs.Triage(), Reruns: h.runs.Reruns(), Caps: continueCaps, Triage: docketedTriage, Clock: &steppingClock{now: ended}}
	if _, err := docketer.RecordStoppedRun(stopped); err != nil {
		t.Fatal(err)
	}
	if _, err := h.runs.Triage().RecordDecision(ctx, docketedItem, triageDecided(runstate.TriageDecisionWait, stopped.RunID), ended.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	carrying.CheckStages = checkStageRecoveryContinuer(h, h.ownership)
	if tasks, err := carrying.Outstanding(); err != nil || len(tasks) != 0 {
		t.Fatalf("a wait decided about the later stop still offered the harness's continuation: %#v, %v", tasks, err)
	}
	holds, err := readmodel.HeldForAPerson(ctx, h.runs, h.runs.Triage(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if reason, _ := holds.Reason(docketedItem); holds.Decided(docketedItem) || strings.Contains(reason, "the harness continues it itself") {
		t.Fatalf("a wait decided about the later stop names the harness: %q", reason)
	}
}
