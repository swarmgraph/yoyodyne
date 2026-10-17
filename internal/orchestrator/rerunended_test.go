package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The yoyodyne-ifd.435.10 shape, taken through the docket the development
// manager reads. Her re-run of the stopped run was carried out, which closed that
// run's entry for good; the re-run then stopped when a check ran past its time
// limit, before the record said which limit stopped it, with no blocker. No other
// rule dockets such a run, and the hold reads the item as hers to decide, so the
// re-run's own stoppage has to come out on her docket as undecided.
func TestAReRunThatEndedUndocketedComesOutUndecidedOnTheDocket(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	first := harness.carryOut()
	if _, _, err := first.Carry(context.Background(), theOneOutstanding(t, first)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	harness.docket.close(triage.Key(triage.ClassStoppedRun, docketedRunID), runstate.TriageDecisionRerun, docketedNow)
	claims, err := harness.reruns.Claimed(docketedItem)
	if err != nil || len(claims) != 1 || claims[0].RunID == "" {
		t.Fatalf("Claimed() = %#v, %v; want the one re-run claimed with its run", claims, err)
	}

	later := stoppedState()
	later.RunID = claims[0].RunID
	later.Status = runstate.StatusTimedOut
	later.Phase = runstate.PhaseChecking
	later.StartedAt = docketedNow.Add(time.Hour)
	later.UpdatedAt = docketedNow.Add(3 * time.Hour)
	completed := later.UpdatedAt
	later.CompletedAt = &completed
	later.Branch = "yoyodyne/task/rerun"
	later.WorktreePath = "/state/worktrees/rerun"
	later.Blocker = ""
	later.CheckFailure = nil
	later.ReviewFindingDetails = nil
	later.ReviewFindings = 0
	later.ReviewSummary = ""
	later.CheckStage = &runstate.CheckStage{Command: "make race", StartedAt: docketedNow.Add(2 * time.Hour), BoundSeconds: 3600, ConfiguredSeconds: 3600}
	later.Failure = "verification timed out: make race ran for 31m9s and was stopped at its 30m0s execution.check_timeout budget"
	if later.StoppedUnfinishedAtChecks() {
		t.Fatalf("the fixture's re-run reads as one a check-stage rule dockets, want the shape none does")
	}
	if err := harness.runs.Create(later); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	build, err := docketerOverStore(harness.docket, harness.runs, rearmConfig()).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	var found *triage.Entry
	for index := range build.Entries {
		if build.Entries[index].RunID == docketedRunID {
			t.Fatalf("the decided run's entry is listed again, want only the re-run's own stoppage")
		}
		if build.Entries[index].RunID == later.RunID {
			found = &build.Entries[index]
		}
	}
	if found == nil {
		t.Fatalf("docket = %#v, want the re-run's stoppage %s listed for the development manager", build.Entries, later.RunID)
	}
	if found.Class != triage.ClassStoppedRun || !found.Undecided(docketedNow.Add(4*time.Hour)) {
		t.Fatalf("entry = %#v, want an undecided stopped-run entry", *found)
	}
	if found.Failure == "" {
		t.Fatalf("entry = %#v, want it to carry why the re-run stopped", *found)
	}

	// The next build does not docket it a second time.
	again, err := docketerOverStore(harness.docket, harness.runs, rearmConfig()).Build()
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if again.Added != 0 {
		t.Fatalf("second build added %d entries, want none", again.Added)
	}
}
