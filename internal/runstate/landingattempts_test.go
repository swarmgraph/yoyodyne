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
