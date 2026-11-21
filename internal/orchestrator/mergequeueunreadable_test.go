package orchestrator

// A merge queue candidate's reviewer that answers with something other than a
// bare verdict: an approval with a sentence before it is the approval; a reply
// with no verdict in it is asked for once more, told what was wrong; and after
// maxMergeQueueUnreadableReviews such replies the entry is handed back and the
// queue goes on to the entries behind it, rather than stopping on the entry
// and asking again on every pass.

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestTheQueueReadsAnApprovalWithASentenceBeforeIt(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t, "The change does what the item asks and its checks passed.\n\n"+approveVerdict)
	verification, err := f.work()
	if err != nil || !verification.Verified {
		t.Fatalf("Work() = %#v, %v; want the candidate verified on the approval after the sentence", verification, err)
	}
	if f.reviewer.calls() != 1 {
		t.Fatalf("reviewer asked %d times, want once", f.reviewer.calls())
	}
}

func TestAnUnreadableQueueReviewIsAskedForOnceMoreNamingTheProblem(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t, "I approve this change.", approveVerdict)
	verification, err := f.work()
	if err != nil || !verification.Verified {
		t.Fatalf("Work() = %#v, %v; want the second, readable reply to verify the candidate", verification, err)
	}
	if f.reviewer.calls() != 2 {
		t.Fatalf("reviewer asked %d times, want the first asking and one more", f.reviewer.calls())
	}
	// The second asking is the reviewer's own session, told what was wrong.
	reask := f.reviewer.last().Reask
	if reask == nil || reask.SessionID != "reviewer-session" || !strings.Contains(reask.Problem, "decode review verdict") ||
		strings.HasPrefix(reask.Problem, unreadableReplyProblem) {
		t.Fatalf("second asking = %#v, want the reviewer's session told what was wrong with its reply", reask)
	}
	generation := verification.Generation
	if len(generation.Interruptions) != 1 || !generation.Interruptions[0].Unreadable ||
		generation.Interruptions[0].Stage != runstate.MergeQueueStageReview ||
		!strings.HasPrefix(generation.Interruptions[0].Problem, unreadableReplyProblem) {
		t.Fatalf("interruptions = %#v, want the unreadable reply kept as one", generation.Interruptions)
	}
	if generation.Review == nil || generation.Review.Unreadable || generation.Review.Decision != "approve" {
		t.Fatalf("review = %#v, want the readable approval standing", generation.Review)
	}
}

func TestTheQueueStopsAskingAfterTheLimitOfUnreadableReplies(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t, "No verdict here.", "Still no verdict.", approveVerdict)
	verification, err := f.work()
	if err != nil {
		t.Fatalf("Work() error = %v, want the unreadable replies reported rather than a failure", err)
	}
	if verification.Verified || verification.UnreadableReview == "" ||
		!strings.Contains(verification.UnreadableReview, "answered 2 times with no verdict that could be read") ||
		!strings.Contains(verification.UnreadableReview, "decode review verdict") {
		t.Fatalf("Work() = %#v, want the entry to be handed back for its unreadable replies", verification)
	}
	if f.reviewer.calls() != maxMergeQueueUnreadableReviews {
		t.Fatalf("reviewer asked %d times, want %d", f.reviewer.calls(), maxMergeQueueUnreadableReviews)
	}
	if count := verification.Generation.UnreadableReviews(); count != maxMergeQueueUnreadableReviews {
		t.Fatalf("unreadable replies recorded = %d, want %d", count, maxMergeQueueUnreadableReviews)
	}
	// Asked again, from the record alone, it asks the reviewer nothing more.
	again, err := f.restart()
	if err != nil || again.UnreadableReview == "" || f.reviewer.calls() != maxMergeQueueUnreadableReviews {
		t.Fatalf("Work() again = %#v, %v after %d askings; want the same answer and no further review", again, err, f.reviewer.calls())
	}
}

func TestAnEntryWhoseReviewerNeverAnswersReadablyIsHandedBackAndTheQueueGoesOn(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally, "No verdict here.", "Still no verdict.", approveVerdict)
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	behind := f.admitAnother(queueKey, "behind", "feature.txt", "implemented by the change behind\n")

	pass := f.pass(queueKey)
	if pass.Outcome == runstate.MergeQueuePassStopped {
		t.Fatalf("Pass() = %#v, want the queue to go on past the entry its reviewer could not answer readably", pass)
	}
	if !strings.Contains(pass.Says, "landed 1 and handed back 1") {
		t.Fatalf("Pass() says %q, want the hand-back and the landing behind it said", pass.Says)
	}
	handback := f.landing().Handback
	if handback == nil || handback.Continuation != runstate.MergeQueueReleased || handback.Mover != ownership.MoverDevelopmentManager ||
		!strings.Contains(handback.Reason, "no verdict that could be read") {
		t.Fatalf("handback = %#v, want the entry handed back for its unreadable reviews, kept for the development manager", handback)
	}
	if repair.count() != 1 || !strings.Contains(repair.given[0].Reason, "no verdict that could be read") {
		t.Fatalf("repair given %#v, want the hand-back given to the entry's run once", repair.given)
	}
	if landing, _, err := f.queue.Landing(queueKey, behind.EntryID); err != nil || landing.Completion == nil {
		t.Fatalf("landing of the entry behind = %#v, %v; want it landed", landing, err)
	}
	if f.reviewer.calls() != maxMergeQueueUnreadableReviews+1 {
		t.Fatalf("reviewer asked %d times, want %d for the first entry and once for the one behind", f.reviewer.calls(), maxMergeQueueUnreadableReviews+1)
	}
}

// Each candidate the queue builds is recorded with the rule that chose its
// entry and why.
func TestTheQueueRecordsWhichRuleChoseTheEntryOnItsCandidate(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t, approveVerdict)
	if _, err := f.work(); err != nil {
		t.Fatalf("Work() error = %v", err)
	}
	generations := f.generations()
	if len(generations) == 0 || generations[0].Selection == nil {
		t.Fatalf("generations = %#v, want the first to record how its entry was chosen", generations)
	}
	selection := generations[0].Selection
	if selection.Rule != MergeQueueRuleOnlyWaiting || !strings.Contains(selection.Why, "only entry waiting") {
		t.Fatalf("selection = %#v, want the only-entry rule and its reason", selection)
	}
}
