package runstate

import (
	"strings"
	"testing"
	"time"
)

func feedbackRepairFor(item string) TeamFeedbackRepair {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	return TeamFeedbackRepair{
		PullRequest: 7, FeedbackID: "line-comment-21",
		Feedback:   ForgeFeedbackItem{ID: "line-comment-21", Kind: FeedbackLineComment, Author: "teammate", At: at, Body: "This loop never ends.", Path: "a.go", Line: 3},
		WorkItemID: item, Reason: "a real defect", DecidedBy: "Development Manager", Conversation: "chat-dm", Turn: 4, DecidedAt: at,
	}
}

func waitingRun(item string) State {
	commit := strings.Repeat("a", 40)
	return State{RunID: "run-" + strings.Repeat("2", 32), WorkItemID: item, Status: StatusSucceeded, Integration: &Integration{}, TeamMerge: &TeamMerge{
		TargetBranch: "staging", UpstreamBranch: "main", LandingCommit: commit, Since: time.Now().UTC(),
		PullRequest: &TeamPullRequest{Number: 7, TrackedAt: time.Now().UTC()},
	}}
}

func TestARepairFromFeedbackIsRecordedOnceAndThenStarted(t *testing.T) {
	t.Parallel()
	state := waitingRun("yoyodyne-a")
	commit := state.TeamMerge.LandingCommit

	if changed, err := (TeamFeedbackRepairDecided{LandingCommit: commit, Repair: feedbackRepairFor("yoyodyne-a")}).apply(&state); err != nil || !changed {
		t.Fatalf("apply() = %v, %v, want the decision recorded", changed, err)
	}
	if _, err := (TeamFeedbackRepairDecided{LandingCommit: commit, Repair: feedbackRepairFor("yoyodyne-a")}).apply(&state); err == nil || !strings.Contains(err.Error(), "already answered") {
		t.Fatalf("second apply() error = %v, want the entry refused as already answered", err)
	}
	if len(state.TeamMerge.RepairsOwed()) != 1 {
		t.Fatalf("owed = %#v, want the one decision", state.TeamMerge.RepairsOwed())
	}
	started := TeamFeedbackRepairStarted{LandingCommit: commit, PullRequest: 7, FeedbackID: "line-comment-21", RunID: "run-" + strings.Repeat("3", 32), At: time.Now().UTC()}
	if changed, err := started.apply(&state); err != nil || !changed || len(state.TeamMerge.RepairsOwed()) != 0 {
		t.Fatalf("started apply() = %v, %v, owed %#v; want it recorded and nothing owed", changed, err, state.TeamMerge.RepairsOwed())
	}
	if changed, err := started.apply(&state); err != nil || changed {
		t.Fatalf("started again = %v, %v, want no change", changed, err)
	}
	other := started
	other.RunID = "run-" + strings.Repeat("4", 32)
	if _, err := other.apply(&state); err == nil {
		t.Fatal("a second run for one decision was recorded, want it refused")
	}
}

func TestARepairFromFeedbackIsRefusedOnceTheWaitHasEnded(t *testing.T) {
	t.Parallel()
	state := waitingRun("yoyodyne-a")
	commit := state.TeamMerge.LandingCommit
	found := time.Now().UTC()
	state.TeamMerge.FoundAt, state.TeamMerge.FoundOn = &found, strings.Repeat("b", 40)
	if _, err := (TeamFeedbackRepairDecided{LandingCommit: commit, Repair: feedbackRepairFor("yoyodyne-a")}).apply(&state); err == nil || !strings.Contains(err.Error(), "not waiting for the team to merge") {
		t.Fatalf("apply() error = %v, want it refused for a wait that has ended", err)
	}
	live := waitingRun("yoyodyne-a")
	if _, err := (TeamFeedbackRepairDecided{LandingCommit: commit, Repair: feedbackRepairFor("yoyodyne-b")}).apply(&live); err == nil {
		t.Fatal("a decision about another item was recorded on this run, want it refused")
	}
}
