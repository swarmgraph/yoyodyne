package runstate

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A wait naming no work item, recorded on a run whose repair is decided and not
// yet carried out, leaves the repair standing; once the repair has been carried
// out on the run, the same wait is a decision about the stop after it and is
// recorded.
func TestAWaitNamingNoWorkKeepsARepairStillToBeCarriedOut(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	run := testState(t, StatusFailed)
	run.Blocker = "Yoyodyne stopped this item: the landing failed five times."
	if err := store.Create(run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	decided := time.Date(2026, 10, 21, 8, 15, 0, 0, time.UTC)
	caps := TriageCaps{ReviewRounds: 8, RepairGrants: 2, Reruns: 2, MergeRearms: 1}
	if _, err := store.Triage().GrantRepair(ctx, run.WorkItemID, triageDecided(TriageDecisionRepair, run.RunID), 2, decided, caps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}

	waited := triageDecided(TriageDecisionWait, run.RunID)
	counters, err := store.RecordTriageDecision(ctx, run.WorkItemID, waited, decided.Add(95*time.Minute))
	if err != nil {
		t.Fatalf("RecordTriageDecision() error = %v", err)
	}
	if standing, _ := counters.DecisionOf(run.RunID); standing.Decision != TriageDecisionRepair {
		t.Fatalf("decision = %#v, want the repair still standing", standing)
	}
	if !counters.AwaitingCarryOutOf(run) {
		t.Fatal("the repair no longer waits for the harness to carry it out")
	}

	// The ledger alone, recording the same wait, supersedes the repair as it
	// always has: the rule is the conversation's, which reads the run beside it.
	ledgerOnly := newTriageStore(t)
	if _, err := ledgerOnly.GrantRepair(ctx, run.WorkItemID, triageDecided(TriageDecisionRepair, run.RunID), 2, decided, caps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if superseded, err := ledgerOnly.RecordDecision(ctx, run.WorkItemID, waited, decided.Add(time.Hour)); err != nil {
		t.Fatalf("RecordDecision() error = %v", err)
	} else if standing, _ := superseded.DecisionOf(run.RunID); standing.Decision != TriageDecisionWait {
		t.Fatalf("decision = %#v, want the ledger's own rule unchanged", standing)
	}

	// The repair carried out on the run: a continuation since the decision.
	run.RepairContinuations = []RepairContinuation{{GrantedAttempts: 2, ContinuedAt: decided.Add(2 * time.Hour), Reason: "the repair decided at 08:15"}}
	if err := store.Save(run); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	counters, err = store.RecordTriageDecision(ctx, run.WorkItemID, waited, decided.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("RecordTriageDecision() error = %v", err)
	}
	if standing, _ := counters.DecisionOf(run.RunID); standing.Decision != TriageDecisionWait {
		t.Fatalf("decision = %#v, want the wait recorded once the repair was carried out", standing)
	}
}

// The same for a re-run: kept while nothing has claimed it, and replaced once a
// claim taken since the decision says it was carried out. A wait naming the
// work it depends on replaces a re-run still to be carried out, because it says
// what happens instead.
func TestAWaitNamingNoWorkKeepsAReRunStillToBeCarriedOut(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	run := testState(t, StatusFailed)
	run.Blocker = "Yoyodyne stopped this item: its target branch moved."
	if err := store.Create(run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	decided := time.Date(2026, 11, 19, 21, 15, 0, 0, time.UTC)
	caps := TriageCaps{ReviewRounds: 8, RepairGrants: 2, Reruns: 3, MergeRearms: 1}
	rerun := triageDecided(TriageDecisionRerun, run.RunID)
	if _, err := store.Triage().RecordRerun(ctx, run.WorkItemID, rerun, decided, caps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}

	waited := triageDecided(TriageDecisionWait, run.RunID)
	counters, err := store.RecordTriageDecision(ctx, run.WorkItemID, waited, decided.Add(time.Minute))
	if err != nil {
		t.Fatalf("RecordTriageDecision() error = %v", err)
	}
	if standing, _ := counters.DecisionOf(run.RunID); standing.Decision != TriageDecisionRerun {
		t.Fatalf("decision = %#v, want the re-run still standing", standing)
	}

	named := waited
	named.WaitsOn = "yoyodyne-ifd.398"
	if counters, err = store.RecordTriageDecision(ctx, run.WorkItemID, named, decided.Add(2*time.Minute)); err != nil {
		t.Fatalf("RecordTriageDecision() error = %v", err)
	}
	if standing, _ := counters.DecisionOf(run.RunID); standing.Decision != TriageDecisionWait || standing.WaitsOn != named.WaitsOn {
		t.Fatalf("decision = %#v, want the wait on named work in place of the re-run", standing)
	}

	// Decided again, and this time carried out: the claim is taken.
	if _, err := store.Triage().RecordRerun(ctx, run.WorkItemID, rerun, decided.Add(3*time.Minute), caps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	if _, err := store.Reruns().Claim(ctx, Rerun{
		DocketKey:  triage.Key(triage.ClassStoppedRun, run.RunID),
		PriorRunID: run.RunID,
		WorkItemID: run.WorkItemID,
		Reason:     rerun.Reason,
		ClaimedAt:  decided.Add(4 * time.Minute),
	}); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if counters, err = store.RecordTriageDecision(ctx, run.WorkItemID, waited, decided.Add(5*time.Minute)); err != nil {
		t.Fatalf("RecordTriageDecision() error = %v", err)
	}
	if standing, _ := counters.DecisionOf(run.RunID); standing.Decision != TriageDecisionWait {
		t.Fatalf("decision = %#v, want the wait recorded once the re-run was carried out", standing)
	}
}
