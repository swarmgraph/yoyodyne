package epicjoin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Whether the operator is asked about a question about scope, and what: the
// design's "What records the answer on scope", points 5 to 7.

const otherMember = "yoyodyne-ep.2"

func (b *bench) operatorQuestions() []report.Report {
	b.t.Helper()
	listed, err := b.reports().List()
	b.must(err)
	var questions []report.Report
	for _, reported := range listed {
		if strings.HasPrefix(reported.RunID, report.ScopeOperatorQuestionRun) {
			questions = append(questions, reported)
		}
	}
	return questions
}

// outsideAndAnswered is a piece judged outside whose question the Lead Product
// Manager has handled, naming the operator or not, with the round that reads it.
func (b *bench) outsideAndAnswered(id string, needsOperator bool) {
	b.t.Helper()
	b.says(string(epicprogress.AnswerOutside))
	b.round()
	asked, _ := b.entry().Asked(id)
	if asked.Question == "" {
		b.t.Fatalf("%s filed no question about scope: %+v", id, asked)
	}
	b.now = b.now.Add(time.Hour)
	b.handles(asked.Question, needsOperator)
	b.now = b.now.Add(time.Minute)
	b.round()
}

func TestAHandlingThatDoesNotNameTheOperatorSendsHimNothing(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.split(piece, member)
	b.outsideAndAnswered(piece, false)
	b.round()
	if questions := b.operatorQuestions(); len(questions) != 0 || b.owed() {
		t.Fatalf("the operator was asked %d questions", len(questions))
	}
	if b.starts(piece) || !b.starts(otherMember) {
		t.Errorf("the piece starts (%t), or the other member does not (%t)", b.starts(piece), b.starts(otherMember))
	}
}

func TestAPieceThatNeverJoinedIsOfferedOnlyTheAddOnce(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.split(piece, member)
	b.outsideAndAnswered(piece, true)
	questions := b.operatorQuestions()
	asked, _ := b.entry().Asked(piece)
	if len(questions) != 1 || asked.Answered == nil || asked.Answered.Asked == nil || asked.Answered.Asked.Report != questions[0].ID || asked.Answered.Asked.Joined {
		t.Fatalf("questions %+v, and the piece keeps %+v", questions, asked.Answered)
	}
	question := questions[0]
	if !strings.Contains(question.Message, "`yoyo epic add "+epic+" "+piece+"`") || strings.Contains(question.Message, "yoyo epic remove") ||
		!strings.Contains(question.Message, "only the operator can take it out") || !strings.Contains(question.Message, asked.Question) ||
		question.Role != report.HarnessReporter || question.WorkItemID != piece || question.DuringEpic != epic || question.Addressee() != domain.RoleProductManager {
		t.Fatalf("the question is %+v", question)
	}
	b.round()
	if len(b.operatorQuestions()) != 1 || b.owed() {
		t.Errorf("a second round asked %d questions", len(b.operatorQuestions()))
	}
	// The other members carry on meanwhile, and the piece starts only once he adds it.
	if b.starts(piece) || !b.starts(otherMember) {
		t.Fatalf("the piece starts (%t), or the other member does not (%t)", b.starts(piece), b.starts(otherMember))
	}
	_, err := b.operator().Add(epic, piece, b.now)
	b.must(err)
	if !b.starts(piece) {
		t.Error("the piece does not start once the operator added it")
	}
}

func TestAJoinedPieceJudgedOutsideAfterAnEditIsOfferedTheAddAndTheRemove(t *testing.T) {
	t.Parallel()
	b := joinedInside(t)
	b.edit(piece, func(item *beads.WorkItem) { item.Description = "a wider text" })
	b.outsideAndAnswered(piece, true)
	questions := b.operatorQuestions()
	if len(questions) != 1 {
		t.Fatalf("%d questions were put to the operator", len(questions))
	}
	add, remove := "`yoyo epic add "+epic+" "+piece+"`", "`yoyo epic remove "+epic+" "+piece+" --words <text>`"
	message := questions[0].Message
	if !strings.Contains(message, add) || !strings.Contains(message, remove) || strings.Index(message, add) > strings.Index(message, remove) {
		t.Fatalf("the question does not offer the add and the remove as two answers: %s", message)
	}
	if asked, _ := b.entry().Asked(piece); !asked.Answered.Asked.Joined {
		t.Errorf("the question keeps %+v", asked.Answered.Asked)
	}
	if why := b.waits(piece); !strings.Contains(why, "yoyo epic add") || !strings.Contains(why, "yoyo epic remove") {
		t.Errorf("it waits saying %q", why)
	}
	// His add keeps it as it reads, and it is never compared again.
	_, err := b.operator().Add(epic, piece, b.now)
	b.must(err)
	b.edit(piece, func(item *beads.WorkItem) { item.Description = "edited after he added it" })
	b.round()
	if why := b.waits(piece); why != "" || len(b.requests) != 2 || len(b.operatorQuestions()) != 1 {
		t.Errorf("after his add it waits (%q), with %d calls and %d questions", why, len(b.requests), len(b.operatorQuestions()))
	}
}

// failingOperatorQuestion is the pile of reports with a stop just before the
// question to the operator is filed.
type failingOperatorQuestion struct{ Reports }

func (f failingOperatorQuestion) Append(reported report.Report) error {
	if strings.HasPrefix(reported.RunID, report.ScopeOperatorQuestionRun) {
		return errors.New("stopped before the question was filed")
	}
	return f.Reports.Append(reported)
}

func TestARestartAfterTheQuestionsIdentifierIsWrittenSendsNoSecondQuestion(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.split(piece, member)
	b.says(string(epicprogress.AnswerOutside))
	b.round()
	asked, _ := b.entry().Asked(piece)
	b.now = b.now.Add(time.Hour)
	b.handles(asked.Question, true)
	joiner := b.joiner()
	joiner.Evidence.Reports = failingOperatorQuestion{joiner.Evidence.Reports}
	if err := joiner.Round(context.Background()); err == nil {
		t.Fatal("the stopped filing was not said")
	}
	kept, _ := b.entry().Asked(piece)
	if kept.Answered == nil || kept.Answered.Asked == nil || len(b.operatorQuestions()) != 0 || !b.owed() {
		t.Fatalf("before the restart the piece keeps %+v and %d questions were filed", kept.Answered, len(b.operatorQuestions()))
	}
	b.round()
	b.round()
	if questions := b.operatorQuestions(); len(questions) != 1 || questions[0].ID != kept.Answered.Asked.Report || b.owed() {
		t.Errorf("after the restart %d questions were filed, want the one written before", len(questions))
	}
}

// A piece written for a review comment that was judged outside is rewritten by
// the development manager after her answer, and the rewrite is judged again.
func TestAPieceFromAReviewCommentRewrittenAfterHerAnswerIsJudgedAgain(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	store, err := epicprogress.Open(b.root, product)
	b.must(err)
	record, err := epicscope.Read(b.root, product)
	b.must(err)
	named, _ := record.Named(epic)
	_, _, err = store.MergedCommentRead(epicprogress.Of(named), epicprogress.Comment{ID: "comment-21", Member: member, PullRequest: 7}, "teammate said: also add a new command")
	b.must(err)
	_, err = store.CorrectingComment(epic, "comment-21", correcting)
	b.must(err)
	b.split(correcting, epic)
	b.outsideAndAnswered(correcting, false)
	if b.starts(correcting) || len(b.requests) != 1 || len(b.operatorQuestions()) != 0 {
		t.Fatalf("judged outside it starts (%t), with %d calls", b.starts(correcting), len(b.requests))
	}
	b.edit(correcting, func(item *beads.WorkItem) { item.Description = "rewritten to fit the member" })
	b.says(string(epicprogress.AnswerInside))
	b.round()
	if len(b.requests) != 2 || !strings.Contains(b.requests[1].Prompt, "rewritten to fit the member") || !strings.Contains(b.requests[1].Prompt, "also add a new command") {
		t.Fatalf("%d calls were made, the last given %q", len(b.requests), b.requests[len(b.requests)-1].Prompt)
	}
	b.wants(correcting, epicprogress.AnswerInside, true)
}
