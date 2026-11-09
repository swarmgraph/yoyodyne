package runstate

import (
	"testing"
	"time"
)

// The cursor marks feedback acted on once, including feedback left in the same
// second as the latest acted on, and never marks feedback it has not seen.
func TestTheFeedbackCursorActsOnEachPieceOnce(t *testing.T) {
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	first := ForgeFeedbackItem{ID: FeedbackID("comment", 1), Kind: FeedbackComment, Author: "ada", At: at}
	same := ForgeFeedbackItem{ID: FeedbackID("comment", 2), Kind: FeedbackComment, Author: "grace", At: at}
	later := ForgeFeedbackItem{ID: FeedbackID("review", 3), Kind: FeedbackApproval, Author: "linus", At: at.Add(time.Minute)}
	var cursor *PullRequestFeedback
	if cursor.Handled(first) {
		t.Fatal("feedback nothing read was handled")
	}
	cursor = &PullRequestFeedback{}
	cursor.Advance([]ForgeFeedbackItem{first})
	if !cursor.Handled(first) || cursor.Handled(same) || cursor.Handled(later) {
		t.Fatalf("cursor %#v, want only the first handled", cursor)
	}
	cursor.Advance([]ForgeFeedbackItem{same, later})
	if !cursor.Handled(same) || !cursor.Handled(later) {
		t.Fatalf("cursor %#v, want every piece handled", cursor)
	}
	if err := cursor.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

// A read alone refuses feedback that asks for a repair, and a repair round is
// refused on a run whose budget is spent, so neither can answer a request for
// changes by moving the cursor past it.
func TestFeedbackAskingForARepairIsNeverReadPast(t *testing.T) {
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	state := State{RunID: "run-x", Status: StatusSucceeded, StopClass: StopIntegrationPolicy, WorktreePath: "/w", Branch: "b",
		PullRequest: &PullRequest{Number: 7, State: "OPEN"}, RepairAttempts: 2}
	asks := ForgeFeedbackItem{ID: FeedbackID("review", 1), Kind: FeedbackChangesRequested, Author: "ada", At: at, Body: "rename it"}
	if _, err := (FeedbackRead{Number: 7, Read: []ForgeFeedbackItem{asks}, At: at}).apply(&state); err == nil {
		t.Fatal("a read moved the cursor past a requested change")
	}
	round := ForgeFeedback{PullRequest: 7, FoundAt: at, Items: []ForgeFeedbackItem{asks}}
	if _, err := state.PutBackForFeedback(round, []ForgeFeedbackItem{asks}, 2, at); err == nil {
		t.Fatal("a round started on a run whose repair budget is spent")
	}
	live, err := state.PutBackForFeedback(round, []ForgeFeedbackItem{asks}, 3, at)
	if err != nil || live.Status != StatusRunning || live.RepairAttempts != 3 || !live.ForgeFeedback.DispatchPending || !live.PullRequest.Feedback.Handled(asks) {
		t.Fatalf("PutBackForFeedback() = %s attempts %d, %v; want the run live with one more attempt and the feedback read", live.Status, live.RepairAttempts, err)
	}
	if state.PullRequest.Feedback != nil || state.Status != StatusSucceeded {
		t.Fatal("PutBackForFeedback changed the record it was given")
	}
}

// The list of what the harness wrote on a request keeps each forge identifier
// once, refuses what is not one, keeps the newest within its bound, and never
// changes a copy of the record taken before a write.
func TestARequestsListOfHarnessWritesKeepsEachIdentifierOnce(t *testing.T) {
	t.Parallel()

	var published PullRequest
	before := published
	published.RecordWrite(FeedbackID("comment", 7))
	published.RecordWrite(FeedbackID("comment", 7))
	published.RecordWrite("not an identifier")
	if !published.WroteIt("comment-7") || published.WroteIt("review-7") || len(published.Wrote.IDs) != 1 || before.Wrote != nil {
		t.Fatalf("Wrote = %+v (copy before: %+v), want comment-7 alone and the copy untouched", published.Wrote, before.Wrote)
	}
	kept := published
	published.RecordWrite(FeedbackID("review", 8))
	if len(kept.Wrote.IDs) != 1 || !published.WroteIt("review-8") {
		t.Fatalf("a write changed a copy of the record taken before it: %+v", kept.Wrote)
	}
	for number := int64(100); number < 100+MaxForgeWrites+5; number++ {
		published.RecordWrite(FeedbackID("comment", number))
	}
	if len(published.Wrote.IDs) != MaxForgeWrites || published.WroteIt("comment-7") || !published.WroteIt(FeedbackID("comment", 100+MaxForgeWrites+4)) {
		t.Fatalf("Wrote holds %d, want the newest %d", len(published.Wrote.IDs), MaxForgeWrites)
	}
	if err := published.validateWrote(); err != nil {
		t.Fatalf("validateWrote() = %v", err)
	}
	published.Wrote.IDs = append(published.Wrote.IDs[1:], "pull-7")
	if err := published.validateWrote(); err == nil {
		t.Fatal("validateWrote() accepted an identifier that is not a forge comment or review")
	}
}
