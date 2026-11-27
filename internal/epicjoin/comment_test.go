package epicjoin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

func changeRequest() Comment {
	return Comment{Member: member, PullRequest: 7, ID: "PRR_1", Author: "a-person", Body: "Handle the empty list too.", At: namedAt.Add(time.Hour)}
}

func (b *bench) judgeComment(comment Comment) (Verdict, error) {
	b.t.Helper()
	return b.joiner().JudgeComment(context.Background(), comment)
}

func (b *bench) commentQuestions() []report.Report {
	b.t.Helper()
	listed, err := b.reports().List()
	b.must(err)
	var questions []report.Report
	for _, reported := range listed {
		if strings.HasPrefix(reported.RunID, "epic-comment:") {
			questions = append(questions, reported)
		}
	}
	return questions
}

// The forge's identifier is written before the judgment is reserved, and the same
// comment read again, by a process that has just started, makes no second call.
func TestAChangeRequestIsJudgedOnceBeforeItsRepair(t *testing.T) {
	t.Parallel()
	for answer, want := range map[string]Verdict{"inside": Repair, "outside": AskedAboutScope, "cannot tell": AskedAboutScope} {
		t.Run(answer, func(t *testing.T) {
			t.Parallel()
			b := newBench(t)
			b.says(answer)
			for range 3 {
				verdict, err := b.judgeComment(changeRequest())
				if err != nil || verdict != want {
					t.Fatalf("the verdict is %v, %v; want %v", verdict, err, want)
				}
			}
			if len(b.requests) != 1 || !strings.Contains(b.requests[0].Prompt, "Handle the empty list too.") || !strings.Contains(b.requests[0].Prompt, "as it read when named") {
				t.Fatalf("%d calls were made: %+v", len(b.requests), b.requests)
			}
			entry := b.entry()
			kept, found := entry.Comment("PRR_1")
			reserved, made := entry.Reservation(kept.Judgment)
			if !found || kept.Member != member || kept.PullRequest != 7 || kept.Outcome == nil || !made || kept.Judgment != b.requests[0].ID || !reserved.At.After(kept.At.Add(-1)) {
				t.Fatalf("the comment is kept as %+v, its judgment reserved as %+v", kept, reserved)
			}
			questions := b.commentQuestions()
			if want == Repair && len(questions) != 0 {
				t.Errorf("a comment judged inside filed %+v", questions)
			}
			if want == AskedAboutScope && (len(questions) != 1 || questions[0].ID != kept.Question || questions[0].For != domain.RoleProductManager ||
				questions[0].DuringEpic != epic || !strings.Contains(questions[0].Message, "No repair was started")) {
				t.Errorf("the questions are %+v, want one to the Lead Product Manager naming %s", questions, kept.Question)
			}
		})
	}
}

// A stop between writing the comment and the call, and one between the call and
// writing its answer, both make one call.
func TestAJudgmentStoppedPartWayIsLookedUpAndNotSentAgain(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.unread = os.ErrPermission // the call is reserved and refused before it is sent
	if verdict, _ := b.judgeComment(changeRequest()); verdict != Later || len(b.requests) != 0 {
		t.Fatalf("the verdict is %v after %d calls, want later and none", verdict, len(b.requests))
	}
	b.unread = nil
	if verdict, err := b.judgeComment(changeRequest()); verdict != Repair || err != nil || len(b.requests) != 1 {
		t.Fatalf("the verdict is %v, %v after %d calls", verdict, err, len(b.requests))
	}
}

// A comment on an item in no open epic, or with epic mode off, is not judged:
// nothing is called and nothing is kept.
func TestACommentOutsideTheNamedEpicsIsNotJudged(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	outside := changeRequest()
	outside.Member = "yoyodyne-outside"
	if verdict, err := b.judgeComment(outside); verdict != NotJudged || err != nil {
		t.Errorf("an item outside is %v, %v", verdict, err)
	}
	_, err := b.operator().TurnOff()
	b.must(err)
	if verdict, err := b.judgeComment(changeRequest()); verdict != NotJudged || err != nil {
		t.Errorf("with epic mode off it is %v, %v", verdict, err)
	}
	if len(b.requests) != 0 || len(b.entry().Comments) != 0 {
		t.Errorf("%d calls were made and %+v kept", len(b.requests), b.entry().Comments)
	}
}

// With the naming ended, or its progress unreadable, no call is made and nothing
// is kept, with epic mode on and off.
func TestACommentIsNotJudgedForANamingEndedOrUnreadable(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"ended", "unreadable"} {
		for _, off := range []bool{false, true} {
			b := newBench(t)
			progress := filepath.Join(home.ProductDirectory(b.root, product), "epic-progress.json")
			switch how {
			case "ended":
				_, err := b.operator().End(epic, "", b.now)
				b.must(err)
			case "unreadable":
				b.must(os.WriteFile(progress, []byte("{"), 0o600))
			}
			if off {
				_, err := b.operator().TurnOff()
				b.must(err)
			}
			verdict, _ := b.judgeComment(changeRequest())
			if how == "unreadable" && !off && verdict != Later || verdict == Repair || verdict == AskedAboutScope {
				t.Errorf("%s, epic mode off %t: the verdict is %v", how, off, verdict)
			}
			content, err := os.ReadFile(progress)
			if len(b.requests) != 0 || how == "ended" && !os.IsNotExist(err) || how == "unreadable" && string(content) != "{" {
				t.Errorf("%s, epic mode off %t: %d calls were made and the progress reads %q, %v", how, off, len(b.requests), content, err)
			}
		}
	}
}

// What the judging session answers is kept with the epic's progress, and an
// answer of outside starts nothing that names the repair.
func TestACommentKeptInsideSaysWhatBecameOfIt(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	_, err := b.judgeComment(changeRequest())
	b.must(err)
	kept, _ := b.entry().Comment("PRR_1")
	if kept.Outcome.Answer != epicprogress.AnswerInside || !strings.Contains(kept.Became, "pull request #7") {
		t.Errorf("it is kept as %+v", kept)
	}
}
