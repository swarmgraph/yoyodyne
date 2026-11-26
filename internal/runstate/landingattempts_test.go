package runstate

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	landingBase  = "1111111111111111111111111111111111111111"
	landingMoved = "2222222222222222222222222222222222222222"
)

// Only a replay still waiting to be judged is judged, so a change handed back
// and then passing is never read as a clean replay, and only a clean one comes
// off the count of lost landings.
func TestOnlyACleanReplayComesOffTheCountOfLostLandings(t *testing.T) {
	t.Parallel()

	var state State
	at := time.Date(2026, 10, 29, 17, 0, 0, 0, time.UTC)
	state.IntegrationRetries = 2
	state.RecordLostLanding(LostLanding{TargetBranch: "main", FromCommit: landingBase, OntoCommit: landingMoved, LostAt: at})
	if !state.JudgeReplay(ReplayStopped) || state.JudgeReplay(ReplayClean) {
		t.Fatal("a replay handed back was judged again as clean")
	}
	state.RecordLostLanding(LostLanding{TargetBranch: "main", FromCommit: landingMoved, OntoCommit: landingBase, LostAt: at})
	if !state.JudgeReplay(ReplayClean) {
		t.Fatal("the pending replay was not judged clean")
	}
	if state.CleanReplays != 1 || state.CountedLostLandings() != 1 {
		t.Fatalf("clean %d, counted %d; want one clean replay off two lost landings", state.CleanReplays, state.CountedLostLandings())
	}
	// A landing that was not replayed has nothing to judge.
	state.RecordLostLanding(LostLanding{TargetBranch: "main", FromCommit: landingBase, LostAt: at})
	if state.JudgeReplay(ReplayClean) {
		t.Fatal("a landing that was never replayed was judged")
	}
	if err := state.validateLandingAttemptsError(); err != nil {
		t.Fatalf("validate = %v", err)
	}
	said := state.LostLandings[1].Says(2)
	if !strings.Contains(said, "Lost landing 2: other work landed on main first") || !strings.Contains(said, "not counted as lost") {
		t.Fatalf("Says() = %q", said)
	}
}

// The account keeps the latest MaxLostLandings, and the record refuses a clean
// count larger than the landings lost or a reconciliation without its target.
func TestTheLandingAccountIsBounded(t *testing.T) {
	t.Parallel()

	var state State
	at := time.Date(2026, 10, 29, 17, 0, 0, 0, time.UTC)
	for range MaxLostLandings + 3 {
		state.RecordLostLanding(LostLanding{TargetBranch: "main", FromCommit: landingBase, LostAt: at})
	}
	if len(state.LostLandings) != MaxLostLandings {
		t.Fatalf("accounts kept = %d, want %d", len(state.LostLandings), MaxLostLandings)
	}
	state.CleanReplays = 1
	state.Reconciliation = &Reconciliation{UsedAt: at}
	err := state.validateLandingAttemptsError()
	if err == nil || !strings.Contains(err.Error(), "clean_replays 1 exceeds") || !strings.Contains(err.Error(), "reconciliation: target_branch") {
		t.Fatalf("validate = %v, want the clean count and the reconciliation refused", err)
	}
}

func (s State) validateLandingAttemptsError() error {
	return errors.Join(s.validateLandingAttempts()...)
}

// A run gets MaxReconciliations attempts to bring its change level with the
// target, numbered on its record, and the count is read back from the record
// by a process that starts again, so a restart between the third conflict and
// the fourth leaves the fourth with none. Each attempt after the first says the
// commit it conflicted with and that no hold applied.
func TestARunGetsThreeReconciliationsAndARestartDoesNotResetThem(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.ProviderSessionID = "writer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	at := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	targets := []string{landingBase, landingMoved, "3333333333333333333333333333333333333333"}
	for index, target := range targets {
		loaded, err := store.Load(state.RunID)
		if err != nil {
			t.Fatalf("Load() before conflict %d error = %v", index+1, err)
		}
		if loaded.ReconciliationsSpent() {
			t.Fatalf("conflict %d found every attempt used after %d", index+1, loaded.ReconciliationsUsed())
		}
		conflict := ReplayConflict{TargetBranch: "main", TargetCommit: target, Paths: []string{"a.go"}, Handback: "entry-" + target[:4], Phase: PhaseIntegrating, RecordedAt: at}
		put := loaded.PutBackToCatchUpWithMergeQueue(conflict, "moved", at)
		put.Status, put.CompletedAt = StatusSucceeded, &at
		if err := store.Save(put); err != nil {
			t.Fatalf("Save() after conflict %d error = %v", index+1, err)
		}
	}
	// A process started again, on this build or a newer one, reads only the
	// record.
	restarted, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() after the restart error = %v", err)
	}
	if !restarted.ReconciliationsSpent() || restarted.ReconciliationsUsed() != MaxReconciliations {
		t.Fatalf("used %d, spent %t; want all %d used", restarted.ReconciliationsUsed(), restarted.ReconciliationsSpent(), MaxReconciliations)
	}
	all := restarted.AllReconciliations()
	if len(all) != MaxReconciliations || len(restarted.EarlierReconciliations) != MaxReconciliations-1 {
		t.Fatalf("attempts = %#v, want %d kept", all, MaxReconciliations)
	}
	for index, attempt := range all {
		if attempt.Attempt != index+1 || attempt.TargetCommit != targets[index] {
			t.Fatalf("attempt %d = %#v, want it numbered and naming the commit it conflicted with", index+1, attempt)
		}
		wantHold := HoldMissedNoHold
		if index == 0 {
			wantHold = ""
		}
		if attempt.HoldMissed != wantHold {
			t.Fatalf("attempt %d hold reason = %q, want %q", index+1, attempt.HoldMissed, wantHold)
		}
	}
	said := all[2].Says()
	if !strings.Contains(said, "Reconciliation attempt 3 of 3: used.") || !strings.Contains(said, targets[2]) ||
		!strings.Contains(said, "the merge queue held no entries that change the same files for it") {
		t.Fatalf("Says() = %q", said)
	}
	if said := all[0].Says(); strings.Contains(said, "not prevented") {
		t.Fatalf("the first attempt's Says() = %q, want no hold reason", said)
	}
}

// A record written before attempts were numbered holds one unnumbered attempt,
// which is read as the run's first, and the record refuses more earlier
// attempts than a run can have.
func TestAnUnnumberedReconciliationIsTheFirstAndTheEarlierOnesAreBounded(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	state := State{Reconciliation: &Reconciliation{TargetBranch: "main", UsedAt: at}}
	if state.ReconciliationsUsed() != 1 || state.ReconciliationsSpent() {
		t.Fatalf("used %d; want the unnumbered attempt counted once", state.ReconciliationsUsed())
	}
	state.UseReconciliation(Reconciliation{TargetBranch: "main", UsedAt: at})
	if state.Reconciliation.Attempt != 2 || state.ReconciliationsUsed() != 2 {
		t.Fatalf("next attempt = %#v, want it numbered 2", state.Reconciliation)
	}
	if err := state.validateLandingAttemptsError(); err != nil {
		t.Fatalf("validate = %v", err)
	}
	state.EarlierReconciliations = append(state.EarlierReconciliations, *state.Reconciliation, *state.Reconciliation)
	state.Reconciliation.HoldMissed = "unknown"
	err := state.validateLandingAttemptsError()
	if err == nil || !strings.Contains(err.Error(), "earlier_reconciliations holds 3 entries") || !strings.Contains(err.Error(), `hold_missed "unknown"`) {
		t.Fatalf("validate = %v, want too many earlier attempts and the unknown reason refused", err)
	}
}
