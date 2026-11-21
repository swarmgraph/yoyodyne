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

// A wait a later run of the item superseded is over without a merge, records who
// superseded it and when together, and is never both superseded and merged.
func TestPersonMergeSupersededByALaterRunIsOverAndRecordsWho(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 11, 21, 1, 0, 0, 0, time.UTC)
	later := since.Add(time.Hour)
	superseder := "run-" + strings.Repeat("b", 32)

	superseded := PersonMerge{Number: 7, Since: since, SupersededBy: superseder, SupersededAt: &later}
	if err := superseded.Validate(); err != nil || superseded.Waiting() || !superseded.Superseded() {
		t.Fatalf("superseded = %v (waiting %t), want a valid ended wait", err, superseded.Waiting())
	}
	for name, invalid := range map[string]PersonMerge{
		"no time":         {Number: 7, Since: since, SupersededBy: superseder},
		"no run":          {Number: 7, Since: since, SupersededAt: &later},
		"not a run":       {Number: 7, Since: since, SupersededBy: "elsewhere", SupersededAt: &later},
		"also merged":     {Number: 7, Since: since, SupersededBy: superseder, SupersededAt: &later, MergedAt: &later},
		"a repeated pair": {Number: 7, Since: since, Repairs: []TeamFeedbackRepair{{PullRequest: 7, FeedbackID: "comment-1"}, {PullRequest: 7, FeedbackID: "comment-1"}}},
	} {
		if err := invalid.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want it refused", name)
		}
	}

	state := State{RunID: "run-" + strings.Repeat("a", 32), Status: StatusSucceeded, PullRequest: &PullRequest{Number: 7}, PersonMerge: &PersonMerge{Number: 7, Since: since}}
	changed, err := PersonMergeSuperseded{Number: 7, RunID: superseder, At: later}.apply(&state)
	if err != nil || !changed || state.WaitingForPersonMerge() || state.PersonMerge.SupersededBy != superseder {
		t.Fatalf("apply() = %v, %v; want the wait ended in favour of %s", changed, err, superseder)
	}
	if changed, err := (PersonMergeSuperseded{Number: 7, RunID: superseder, At: later}).apply(&state); err != nil || changed {
		t.Fatalf("second apply() = %v, %v; want it a no-op", changed, err)
	}
	if _, err := (PersonMergeSuperseded{Number: 7, RunID: state.RunID, At: later}).apply(&State{RunID: state.RunID, Status: StatusSucceeded, PullRequest: &PullRequest{Number: 7}, PersonMerge: &PersonMerge{Number: 7, Since: since}}); err == nil {
		t.Fatal("a run was superseded by itself, want it refused")
	}
}

// A decision to repair from feedback is kept on the wait of a run whose change
// waits on a pull request the harness opened, and a run in flight on it is read.
func TestPersonMergeKeepsRepairsDecidedFromFeedback(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 21, 1, 0, 0, 0, time.UTC)
	repair := TeamFeedbackRepair{
		PullRequest: 7, FeedbackID: "comment-41", WorkItemID: "yoyodyne-a", Reason: "fair", DecidedBy: "Development Manager", Conversation: "chat-dm", Turn: 3, DecidedAt: at,
		Feedback: ForgeFeedbackItem{ID: "comment-41", Kind: FeedbackComment, Author: "teammate", At: at, Body: "why?"},
	}
	state := State{RunID: "run-" + strings.Repeat("a", 32), WorkItemID: "yoyodyne-a", Status: StatusSucceeded, PullRequest: &PullRequest{Number: 7}, PersonMerge: &PersonMerge{Number: 7, Since: at}}
	if changed, err := (TeamFeedbackRepairDecided{Repair: repair}).apply(&state); err != nil || !changed {
		t.Fatalf("decide = %v, %v", changed, err)
	}
	if _, err := (TeamFeedbackRepairDecided{Repair: repair}).apply(&state); err == nil || !strings.Contains(err.Error(), "already answered") {
		t.Fatalf("second decision = %v, want it refused as already answered", err)
	}
	other := repair
	other.WorkItemID = "yoyodyne-b"
	other.FeedbackID, other.Feedback.ID = "comment-42", "comment-42"
	if _, err := (TeamFeedbackRepairDecided{Repair: other}).apply(&state); err == nil {
		t.Fatal("a decision about another item was recorded on this run's wait")
	}
	if state.PersonMerge.RepairInFlight() || len(state.PersonMerge.RepairsOwed()) != 1 {
		t.Fatalf("wait = %#v, want one decision owed and none in flight", state.PersonMerge)
	}
	if _, err := (TeamFeedbackRepairTaken{PullRequest: 7, FeedbackID: "comment-41", At: at}).apply(&state); err != nil {
		t.Fatalf("taken = %v", err)
	}
	if !state.PersonMerge.RepairInFlight() || state.WaitingOnAPersonsMerge() {
		t.Fatalf("wait = %#v, want a repair in flight and the run's feedback left to it", state.PersonMerge)
	}
	started := TeamFeedbackRepairStarted{PullRequest: 7, FeedbackID: "comment-41", RunID: "run-" + strings.Repeat("c", 32), At: at}
	if changed, err := started.apply(&state); err != nil || !changed || state.PersonMerge.RepairInFlight() {
		t.Fatalf("started = %v, %v; want the repair recorded and no longer in flight", changed, err)
	}
	if changed, err := started.apply(&state); err != nil || changed {
		t.Fatalf("started again = %v, %v; want a no-op", changed, err)
	}
	// A decision after the run's wait ended is refused.
	merged := at.Add(time.Hour)
	state.PersonMerge.MergedAt = &merged
	if _, err := (TeamFeedbackRepairDecided{Repair: other}).apply(&State{RunID: state.RunID, WorkItemID: "yoyodyne-b", PullRequest: state.PullRequest, PersonMerge: state.PersonMerge}); err == nil {
		t.Fatal("a decision was recorded on a wait that ended")
	}
}
