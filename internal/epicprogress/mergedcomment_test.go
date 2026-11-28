package epicprogress

import (
	"testing"
	"time"
)

// Feedback on a member's merged pull request is written with the one correction it
// owes in the write that first records it. Read twice, or again by a store opened
// after a restart, it owes that one correction still.
func TestAMergedCommentOwesOneCorrectionHoweverOftenItIsRead(t *testing.T) {
	t.Parallel()
	root, a, _ := twoNamed(t)
	comment := Comment{ID: "comment-21", Member: memberA, PullRequest: 7}
	kept, made, err := storeAt(t, root).MergedCommentRead(a, comment, "teammate said: the flag still has the old name")
	must(t, err)
	if !made || kept.Owed == nil || kept.Owed.Piece != "" || kept.Judgment != "" {
		t.Fatalf("kept = %#v, made %t; want one correction owed and no judgment", kept, made)
	}
	if _, made, err := storeAt(t, root).MergedCommentRead(a, comment, "said again"); err != nil || made {
		t.Fatalf("read again: made %t, %v; want the comment that stands", made, err)
	}
	if comments := entry(t, root, a).Comments; len(comments) != 1 || comments[0].Owed.Missing != "teammate said: the flag still has the old name" {
		t.Fatalf("comments = %#v, want one, as first read", comments)
	}
	_, _, err = storeAt(t, root).MergedCommentRead(a, Comment{ID: "comment-22", Member: memberB, PullRequest: 8}, "on another epic's member")
	wantError(t, err, "is not in epic")
}

// The development manager's piece is linked to the comment's owed correction, once:
// a comment with no correction owed, or a second piece for one, is refused with
// the reason. The piece joins only once a judgment answers inside.
func TestAPieceForAMergedCommentJoinsOnlyAfterItsJudgment(t *testing.T) {
	t.Parallel()
	root, a, _ := twoNamed(t)
	store := storeAt(t, root)
	_, _, err := store.MergedCommentRead(a, Comment{ID: "comment-21", Member: memberA, PullRequest: 7}, "teammate said: rename the flag")
	must(t, err)
	wantError(t, store.MayCorrectComment(epicA, "comment-99"), "has read no review comment comment-99")
	must(t, store.MayCorrectComment(epicA, "comment-21"))
	note, err := store.CorrectingComment(epicA, "comment-21", piece)
	must(t, err)
	if want := "Written as the correction owed by review comment comment-21 on merged pull request #7 of " + memberA + ", in the epic " + epicA + "."; note != want {
		t.Fatalf("note = %q, want %q", note, want)
	}
	wantError(t, store.MayCorrectComment(epicA, "comment-21"), "has already been written, as "+piece)
	_, err = store.CorrectingComment(epicA, "comment-21", "yoyodyne-other")
	wantError(t, err, "has already been written, as "+piece)
	must(t, store.NotedComment(epicA, "comment-21"))

	got := entry(t, root, a)
	if comment, correcting := got.CorrectingComment(piece); !correcting || !comment.Owed.Noted {
		t.Fatalf("correcting = %#v, %t; want the piece named and its note recorded", comment, correcting)
	}
	// Recorded as asked from the comment, whatever From the caller gave.
	must(t, store.Ask(a, Piece{WorkItemID: piece, From: "anything", Judgment: "judgment-1"}))
	asked, _ := entry(t, root, a).Asked(piece)
	if asked.From != FromComment("comment-21") || entry(t, root, a).Has(piece) {
		t.Fatalf("asked = %#v; want it made from the comment and not yet joined", asked)
	}
	must(t, store.Judged(a, piece, Outcome{Answer: AnswerInside, Why: "it fixes what the member was for", Session: "session-1", At: namedAt.Add(time.Hour)}))
	if !entry(t, root, a).Has(piece) {
		t.Fatal("the piece judged inside did not join")
	}
}

// A piece for a merged comment judged outside does not join.
func TestAPieceForAMergedCommentJudgedOutsideDoesNotJoin(t *testing.T) {
	t.Parallel()
	root, a, _ := twoNamed(t)
	store := storeAt(t, root)
	_, _, err := store.MergedCommentRead(a, Comment{ID: "comment-21", Member: memberA, PullRequest: 7}, "teammate said: add a dashboard")
	must(t, err)
	_, err = store.CorrectingComment(epicA, "comment-21", piece)
	must(t, err)
	must(t, store.Ask(a, Piece{WorkItemID: piece, Judgment: "judgment-1"}))
	must(t, store.Judged(a, piece, Outcome{Answer: AnswerOutside, Why: "a dashboard is new work", Session: "session-1", At: namedAt.Add(time.Hour)}))
	if entry(t, root, a).Has(piece) {
		t.Fatal("a piece judged outside joined")
	}
}
