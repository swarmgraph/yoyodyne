package runstate

import (
	"strings"
	"testing"
	"time"
)

func personRepair(feedbackID string) TeamFeedbackRepair {
	at := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	return TeamFeedbackRepair{
		PullRequest: 7, FeedbackID: feedbackID, WorkItemID: "yoyodyne-a", Reason: "a real gap", DecidedBy: "Development Manager", Conversation: "chat-dm", Turn: 1, DecidedAt: at,
		Feedback: ForgeFeedbackItem{ID: feedbackID, Kind: FeedbackComment, Author: "ada", At: at, Body: "why"},
	}
}

func waitingOnPerson() State {
	return State{RunID: "run-" + strings.Repeat("1", 32), WorkItemID: "yoyodyne-a", Status: StatusSucceeded,
		PullRequest: &PullRequest{Number: 7}, PersonMerge: &PersonMerge{Number: 7, Since: time.Now()}}
}

func TestAPersonMergeWaitHoldsOneDecisionPerEntry(t *testing.T) {
	t.Parallel()
	state := waitingOnPerson()
	if changed, err := (PersonMergeRepairDecided{Repair: personRepair("comment-1")}).apply(&state); !changed || err != nil {
		t.Fatalf("apply() = %v, %v, want the decision recorded", changed, err)
	}
	if _, err := (PersonMergeRepairDecided{Repair: personRepair("comment-1")}).apply(&state); err == nil || !strings.Contains(err.Error(), "already answered") {
		t.Fatalf("second apply() error = %v, want it refused as answered", err)
	}
	if len(state.PersonMerge.RepairsOwed()) != 1 {
		t.Fatalf("owed = %#v, want the one decision", state.PersonMerge.RepairsOwed())
	}
	taken := PersonMergeRepairTaken{PullRequest: 7, FeedbackID: "comment-1", At: time.Now()}
	if _, err := taken.apply(&state); err != nil {
		t.Fatalf("taken apply() error = %v", err)
	}
	if _, err := taken.apply(&state); err == nil {
		t.Fatal("taking a decision twice was allowed")
	}
	if len(state.PersonMerge.RepairsOwed()) != 0 || len(state.PersonMerge.RepairsTaken()) != 1 {
		t.Fatalf("wait = %#v, want the decision taken and not owed", state.PersonMerge)
	}
}

func TestADecisionIsRefusedOnceThePersonMergeWaitHasEnded(t *testing.T) {
	t.Parallel()
	state := waitingOnPerson()
	if _, err := (PersonMergeContinued{PullRequest: 7, RunID: "run-" + strings.Repeat("2", 32), At: time.Now()}).apply(&state); err != nil {
		t.Fatalf("continue apply() error = %v", err)
	}
	if state.WaitingForPersonMerge() {
		t.Fatal("a continued wait still counts as waiting")
	}
	if _, err := (PersonMergeRepairDecided{Repair: personRepair("comment-1")}).apply(&state); err == nil || !strings.Contains(err.Error(), "not waiting") {
		t.Fatalf("apply() error = %v, want it refused as not waiting", err)
	}
}

func TestASelectionContinuingARequestStartsFromTheKeptChange(t *testing.T) {
	t.Parallel()
	selection := Selection{By: "development-manager", Reason: "r", At: time.Now(),
		Continues: &Continuation{RunID: "run-" + strings.Repeat("1", 32), Number: 7, Branch: "yoyodyne/a/11111111", HeadCommit: strings.Repeat("a", 40)}}
	if err := selection.Validate(); err == nil || !strings.Contains(err.Error(), "starts from the change") {
		t.Fatalf("Validate() = %v, want a continuation without a lift refused", err)
	}
	selection.Lift = &Lift{RunID: selection.Continues.RunID, Branch: "yoyodyne/a/11111111"}
	if err := selection.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	if stamped, ok := selection.Stamped(time.Now()); !ok || stamped.Continues == nil {
		t.Fatalf("Stamped() dropped the continuation: %#v", stamped)
	}
}
