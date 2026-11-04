package runstate

import (
	"strings"
	"testing"
	"time"
)

// A wait for a person's merge records at most one ending, and each ending
// carries what it is recorded with.
func TestPersonMergeRecordsOneEndingWithWhatItNeeds(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 10, 30, 1, 0, 0, 0, time.UTC)
	later := since.Add(time.Hour)
	commit := strings.Repeat("c", 40)

	waiting := PersonMerge{Number: 7, Since: since}
	if err := waiting.Validate(); err != nil || !waiting.Waiting() {
		t.Fatalf("waiting = %v (waiting %t), want a valid wait", err, waiting.Waiting())
	}
	if got := waiting.Says(); got != "waiting for a person to merge pull request #7" {
		t.Errorf("Says() = %q", got)
	}
	merged := PersonMerge{Number: 7, Since: since, MergedAt: &later, MergeCommit: commit}
	if err := merged.Validate(); err != nil || merged.Waiting() {
		t.Fatalf("merged = %v (waiting %t), want a valid ended wait", err, merged.Waiting())
	}
	returned := PersonMerge{Number: 7, Since: since, ClosedAt: &later, ClosedBy: "octo", ReturnedAt: &later}
	if err := returned.Validate(); err != nil || returned.Waiting() {
		t.Fatalf("returned = %v (waiting %t), want a valid ended wait", err, returned.Waiting())
	}
	for name, invalid := range map[string]PersonMerge{
		"no number":             {Since: since},
		"no start":              {Number: 7},
		"both endings":          {Number: 7, Since: since, MergedAt: &later, ClosedAt: &later, ReturnedAt: &later},
		"a commit before merge": {Number: 7, Since: since, MergeCommit: commit},
		"returned with no time": {Number: 7, Since: since, ReturnedAt: &later},
		"closer while waiting":  {Number: 7, Since: since, ClosedBy: "octo"},
	} {
		if err := invalid.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want it refused", name)
		}
	}

	state := State{Status: StatusSucceeded, PullRequest: &PullRequest{Number: 7}, PersonMerge: &waiting}
	if !state.WaitingForPersonMerge() {
		t.Fatal("a succeeded run with an open wait does not read as waiting")
	}
	state.PersonMerge = &merged
	if state.WaitingForPersonMerge() {
		t.Fatal("a run whose wait ended still reads as waiting")
	}
}

// Bringing a waiting request up to date does not spend the bound on a run's
// integration resumptions: however often the target moves, the record keeps one
// such resumption, and a record already full of others stays within the bound.
func TestBringingAWaitingRequestUpToDateNeverSpendsTheResumptionBound(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	waiting := State{Status: StatusSucceeded, PullRequest: &PullRequest{Number: 7}, PersonMerge: &PersonMerge{Number: 7, Since: at}}
	for update := range 40 {
		waiting = waiting.PutBackToBringUpToDate("the target moved", at.Add(time.Duration(update)*time.Minute))
		if waiting.ResumptionsLeft() != MaxIntegrationResumptions-1 || !waiting.BringingPersonMergeUpToDate() {
			t.Fatalf("update %d: %d resumption(s) left, want one spent however many updates", update+1, waiting.ResumptionsLeft())
		}
		// The run publishes and waits again, as it does after each update.
		waiting.Status, waiting.PersonMerge = StatusSucceeded, &PersonMerge{Number: 7, Since: at}
	}

	full := waiting
	full.IntegrationResumptions = nil
	for range MaxIntegrationResumptions {
		full.IntegrationResumptions = append(full.IntegrationResumptions, IntegrationResumption{Cause: CauseTransportFailure, Reason: "the forge did not answer", ResumedAt: at})
	}
	updated := full.PutBackToBringUpToDate("the target moved", at)
	if len(updated.IntegrationResumptions) != MaxIntegrationResumptions || !updated.BringingPersonMergeUpToDate() {
		t.Fatalf("%d resumptions recorded, latest %v; want the bound kept and the update recorded last", len(updated.IntegrationResumptions), updated.IntegrationResumptions[len(updated.IntegrationResumptions)-1].Cause)
	}
}

// The limit counts each pull request still waiting once, leaves out a wait
// that has ended, and is never reached at zero.
func TestWaitingPullRequestsCountOnlyTheWaitsStillStanding(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	waiting := func(runID string, number int, workItemID, title string) State {
		return State{
			RunID: runID, WorkItemID: workItemID, WorkItemTitle: title, Status: StatusSucceeded,
			PullRequest: &PullRequest{Number: number},
			PersonMerge: &PersonMerge{Number: number, Since: since.Add(time.Duration(number) * time.Minute)},
		}
	}
	merged := waiting("run-merged", 3, "ex-3", "Merged already")
	mergedAt := since.Add(time.Hour)
	merged.PersonMerge.MergedAt = &mergedAt
	states := []State{waiting("run-2", 2, "ex-2", "Second"), waiting("run-1", 1, "ex-1", ""), waiting("run-1-again", 1, "ex-1", ""), merged}

	reading := WaitingPullRequestsOf(2, states)
	if len(reading.Waiting) != 2 || reading.Waiting[0].Number != 1 || reading.Waiting[1].Number != 2 || !reading.Reached() {
		t.Fatalf("reading = %#v, want pull requests 1 and 2, oldest first, at the limit", reading)
	}
	want := "Not starting new work because 2 pull requests are waiting for a person to merge (the limit is 2): #1 for ex-1; #2 for Second (ex-2). New work starts again by itself once one of them is merged or closed."
	if got := reading.Says(); got != want {
		t.Fatalf("says %q, want %q", got, want)
	}
	if single := WaitingPullRequestsOf(1, states[:1]); !strings.Contains(single.Says(), "1 pull request is waiting") {
		t.Fatalf("says %q, want one pull request said in the singular", single.Says())
	}
	for _, limit := range []int{0, 3} {
		if under := WaitingPullRequestsOf(limit, states); under.Reached() || under.Says() != "" {
			t.Fatalf("limit %d: reached = %v, says %q; want nothing", limit, under.Reached(), under.Says())
		}
	}
}
