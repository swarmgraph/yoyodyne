package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
)

// A comment on a member's merged pull request is the development manager's piece
// to write, then the reviewer's to judge, then nobody's once the piece joined; a
// piece judged outside waits on the Lead Product Manager.
func TestACorrectionOwedByAMergedCommentNamesWhoseStepItIs(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	comment := epicprogress.Comment{ID: "comment-21", Member: "yoyodyne-ep.1", PullRequest: 7, Owed: &epicprogress.Correction{Missing: "rename the flag", At: at}}
	entry := epicprogress.Entry{Comments: []epicprogress.Comment{comment}}
	if waits := commentsOwed(entry); len(waits) != 1 || waits[0].Owner != MoverDevelopmentManager || !strings.Contains(waits[0].Says, "comment-21") || !strings.Contains(waits[0].Says, "rename the flag") {
		t.Fatalf("waits = %#v, want the development manager to write the piece", waits)
	}
	entry.Comments[0].Owed.Piece = "yoyodyne-ep.9"
	if waits := commentsOwed(entry); len(waits) != 1 || waits[0].Owner == MoverDevelopmentManager {
		t.Fatalf("waits = %#v, want its judgment owed", waits)
	}
	entry.Pieces = []epicprogress.Piece{{WorkItemID: "yoyodyne-ep.9", Outcome: &epicprogress.Outcome{Answer: epicprogress.AnswerOutside, Why: "new work", At: at}}}
	if waits := commentsOwed(entry); len(waits) != 1 || waits[0].Owner != MoverProductManager {
		t.Fatalf("waits = %#v, want the question about scope", waits)
	}
	entry.Joined = []epicprogress.Joined{{WorkItemID: "yoyodyne-ep.9"}}
	if waits := commentsOwed(entry); len(waits) != 0 {
		t.Fatalf("waits = %#v, want none once the piece joined", waits)
	}
}
