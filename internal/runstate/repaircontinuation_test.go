package runstate

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// continuedAt is the moment a grant is recorded in these tests, fixed so what
// they measure is the record rather than the clock.
var continuedAt = time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)

func grantedContinuation() RepairContinuation {
	return RepairContinuation{
		GrantedAttempts:   2,
		Reason:            "the reviewer's findings are both right and small; the developer that wrote the change is the one to answer them",
		ContinuedAt:       continuedAt,
		SupersededBlocker: "Yoyodyne stopped this item: the repair budget was spent.",
	}
}

// What a grant is for: the run's loop spends the configured budget and the
// grants together, so a continued run may make exactly the attempts somebody
// wrote down.
func TestRepairBudgetIsTheConfiguredAttemptsPlusEveryGrant(t *testing.T) {
	t.Parallel()

	state := testState(t, StatusRunning)
	if granted := state.GrantedRepairAttempts(); granted != 0 {
		t.Fatalf("granted = %d, want a run nothing continued to carry no grant", granted)
	}
	if budget := state.RepairBudget(2); budget != 2 {
		t.Fatalf("budget = %d, want the configured budget unchanged", budget)
	}

	first := grantedContinuation()
	second := grantedContinuation()
	second.GrantedAttempts = 1
	state.RepairContinuations = []RepairContinuation{first, second}
	if granted := state.GrantedRepairAttempts(); granted != 3 {
		t.Fatalf("granted = %d, want both grants counted", granted)
	}
	if budget := state.RepairBudget(2); budget != 5 {
		t.Fatalf("budget = %d, want the configured two plus the granted three", budget)
	}
	// A project that configured no repairs at all still gets what it granted,
	// and a nonsensical configured budget is read as none rather than as a debt
	// against the grant.
	if budget := state.RepairBudget(-1); budget != 3 {
		t.Fatalf("budget = %d, want a negative configured budget read as none", budget)
	}
}

// A continuation is the run's half of a triage decision, so a record that could
// not say what was granted, why, or what it superseded is refused rather than
// stored: every one of those is what a later reader has instead of the blocker
// the re-entry cleared.
func TestRepairContinuationRefusesARecordThatSaysNothing(t *testing.T) {
	t.Parallel()

	if err := grantedContinuation().Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want a whole grant to be storable", err)
	}
	for _, test := range []struct {
		name    string
		mutate  func(*RepairContinuation)
		problem string
	}{
		{
			name:    "granting nothing",
			mutate:  func(c *RepairContinuation) { c.GrantedAttempts = 0 },
			problem: "at least one repair attempt",
		},
		{
			name:    "no reasoning",
			mutate:  func(c *RepairContinuation) { c.Reason = "  " },
			problem: "triage reasoning",
		},
		{
			name:    "reasoning past the bound",
			mutate:  func(c *RepairContinuation) { c.Reason = strings.Repeat("a", MaxSelectionReasonBytes+1) },
			problem: "exceeds the",
		},
		{
			name:    "no moment",
			mutate:  func(c *RepairContinuation) { c.ContinuedAt = time.Time{} },
			problem: "continued_at is required",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			broken := grantedContinuation()
			test.mutate(&broken)
			if err := broken.Validate(); err == nil || !strings.Contains(err.Error(), test.problem) {
				t.Fatalf("Validate() error = %v, want one mentioning %q", err, test.problem)
			}
		})
	}
}

// The grants travel on the run's own record, so they survive the process that
// wrote them: a continued run picked up by a later invocation has to find the
// budget it was granted rather than the one the file was configured with.
func TestStoreRoundTripsTheGrantsARunWasContinuedOn(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	state.RepairAttempts = 3
	state.RepairContinuations = []RepairContinuation{grantedContinuation()}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.RepairContinuations) != 1 || loaded.RepairContinuations[0] != grantedContinuation() {
		t.Fatalf("loaded continuations = %#v, want the grant as it was recorded", loaded.RepairContinuations)
	}
	if budget := loaded.RepairBudget(2); budget != 4 {
		t.Fatalf("loaded budget = %d, want the grant to have survived the write", budget)
	}

	// A record that could not have been written by the only thing that writes
	// them is refused where it is read, exactly as every other bounded list here
	// is.
	broken := state
	broken.RepairContinuations = make([]RepairContinuation, MaxRepairContinuations+1)
	for index := range broken.RepairContinuations {
		broken.RepairContinuations[index] = grantedContinuation()
	}
	if err := broken.Validate(); err == nil || !strings.Contains(err.Error(), "exceeds the bound") {
		t.Fatalf("Validate() error = %v, want the bound on recorded continuations enforced", err)
	}
}

func TestARepairContinuationKeepsTheFailedLocalPromotionAsHistory(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	state := testState(t, StatusRunning)
	state.RepairAttempts = 1
	continuation := grantedContinuation()
	promotion := integratedState(t, PhaseComplete).Integration
	continuation.SupersededCheckFailure = &CheckFailure{
		Command: "build", Output: "build failed on feature.txt", ForgeHeadCommit: promotion.SourceCommit,
		LocalPromotion: promotion,
	}
	state.RepairContinuations = []RepairContinuation{continuation}
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(state.RunID)
	if err != nil || !reflect.DeepEqual(loaded.RepairContinuations, state.RepairContinuations) {
		t.Fatalf("recorded history = %#v, %v; want the check and its original local promotion", loaded.RepairContinuations, err)
	}
	// History alone cannot justify removing the artifacts used by this attempt.
	loaded.WorktreeRemoved, loaded.BranchRemoved = true, true
	if err := loaded.Validate(); err == nil || !strings.Contains(err.Error(), "removed artifacts require recorded integration") {
		t.Fatalf("cleanup authorized by history alone: %v", err)
	}
	// The historical promotion still has to belong to the failed revision.
	continuation.SupersededCheckFailure.LocalPromotion.SourceCommit = strings.Repeat("c", 40)
	if err := continuation.Validate(); err == nil || !strings.Contains(err.Error(), "local promotion of the failed forge revision") {
		t.Fatalf("mismatched historical promotion accepted: %v", err)
	}
}
