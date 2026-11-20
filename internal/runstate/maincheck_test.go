package runstate

import (
	"strings"
	"testing"
	"time"
)

func mainCheckRun(at time.Time, commit string, outcome MainCheckOutcome) MainCheckRun {
	return MainCheckRun{SchemaVersion: MainCheckRunSchemaVersion, ProductID: "yoyodyne", Branch: "main", Commit: commit,
		StartedAt: at, EndedAt: at.Add(30 * time.Minute), Outcome: outcome,
		Checks: []MainCheckResult{{Command: "make race", Outcome: outcome, Took: 30 * time.Minute}}}
}

func TestMainCheckRunsAreKeptOldestFirstWithinTheirBound(t *testing.T) {
	store, err := NewMainCheckStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if runs, err := store.List(); err != nil || len(runs) != 0 {
		t.Fatalf("an empty store lists %v, %v", runs, err)
	}
	start := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	for index := 0; index < maxMainCheckRuns+3; index++ {
		outcome := MainCheckPassed
		if index%2 == 1 {
			outcome = MainCheckFailed
		}
		commit := strings.Repeat(string(rune('a'+index%6)), 40)
		if err := store.Append(mainCheckRun(start.Add(time.Duration(index)*4*time.Hour), commit, outcome)); err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
	}
	runs, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != maxMainCheckRuns {
		t.Fatalf("kept %d runs, want %d", len(runs), maxMainCheckRuns)
	}
	if !runs[0].StartedAt.Equal(start.Add(3 * 4 * time.Hour)) {
		t.Fatalf("oldest kept run started %s; the oldest three should have gone", runs[0].StartedAt)
	}
	// The last index is even, so the latest run passed.
	standing := ReadMainCheckStanding(runs)
	if standing.Latest.Outcome != MainCheckPassed || !standing.LatestPassed.StartedAt.Equal(standing.Latest.StartedAt) {
		t.Fatalf("latest = %+v, latest passed = %+v", standing.Latest, standing.LatestPassed)
	}
}

func TestMainCheckStandingSkipsRunsThatCouldNotRun(t *testing.T) {
	start := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	runs := []MainCheckRun{
		mainCheckRun(start, strings.Repeat("a", 40), MainCheckPassed),
		mainCheckRun(start.Add(4*time.Hour), strings.Repeat("b", 40), MainCheckFailed),
		mainCheckRun(start.Add(8*time.Hour), strings.Repeat("c", 40), MainCheckCouldNotRun),
	}
	standing := ReadMainCheckStanding(runs)
	if standing.Latest.Commit != strings.Repeat("c", 40) || standing.LatestResult.Commit != strings.Repeat("b", 40) || standing.LatestPassed.Commit != strings.Repeat("a", 40) {
		t.Fatalf("standing = latest %s, result %s, passed %s", standing.Latest.Commit, standing.LatestResult.Commit, standing.LatestPassed.Commit)
	}
}

func TestARunThatPassedOnlyOnItsSecondRunIsNotTheLastCleanCommit(t *testing.T) {
	start := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	second := mainCheckRun(start.Add(4*time.Hour), strings.Repeat("b", 40), MainCheckPassed)
	second.Checks[0].FirstRun = &MainCheckFirstRun{Took: 30 * time.Minute, FailingTests: []string{"TestQueue"}}
	runs := []MainCheckRun{mainCheckRun(start, strings.Repeat("a", 40), MainCheckPassed), second}
	standing := ReadMainCheckStanding(runs)
	if standing.Latest.Commit != strings.Repeat("b", 40) || standing.LatestResult.Commit != strings.Repeat("b", 40) {
		t.Fatalf("latest %s, result %s; want the second-try pass as both", standing.Latest.Commit, standing.LatestResult.Commit)
	}
	if standing.LatestPassed == nil || standing.LatestPassed.Commit != strings.Repeat("a", 40) {
		t.Fatalf("last clean run = %+v; want the run on aaaa that passed first time", standing.LatestPassed)
	}
	if second.PassedFirstTime() || !runs[0].PassedFirstTime() {
		t.Fatal("PassedFirstTime reads a second-try pass as clean or a first-time pass as not")
	}
	if ReadMainCheckStanding(runs[1:]).LatestPassed != nil {
		t.Fatal("a second-try pass alone reads as a clean run")
	}
}

func TestAMainCheckRunThatRanNamesItsCommit(t *testing.T) {
	run := mainCheckRun(time.Now(), "", MainCheckPassed)
	if err := run.Validate(); err == nil || !strings.Contains(err.Error(), "names the commit") {
		t.Fatalf("Validate() = %v; want a passed run with no commit refused", err)
	}
	run.Outcome = MainCheckCouldNotRun
	if err := run.Validate(); err != nil {
		t.Fatalf("Validate() = %v; a run that could not read the head has no commit", err)
	}
}
