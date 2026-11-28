package epicjoin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

const (
	added      = "yoyodyne-ifd.428.218"
	addedPiece = "yoyodyne-ifd.428.218.1"
	memberSays = "Stop every process a run started when the run ends, the crash case included."
)

// addMember has the operator add a member whose text says memberSays, as the
// command does, copying it beside the member.
func (b *bench) addMember() {
	b.t.Helper()
	item := beads.WorkItem{ID: added, Title: "Stop a run's processes", Description: memberSays, Status: "open", CreatedAt: namedAt}
	b.items = append(b.items, item)
	text := epicscope.TextOf(item)
	_, err := b.operator().AddCopying(epic, added, &text, namedAt.Add(time.Minute))
	b.must(err)
}

// judgingByText is a reviewer that answers inside only where the prompt carries
// the member's text, which the epic's description does not.
func (b *bench) judgingByText(joiner Joiner, wants string) Joiner {
	judge := joiner.Judge
	joiner.Judge = func(ctx context.Context, request Request) (backend.RunResult, error) {
		b.says("outside")
		if strings.Contains(request.Prompt, wants) {
			b.says("inside")
		}
		return judge(ctx, request)
	}
	return joiner
}

func (b *bench) rewrite(id, description string) {
	b.edit(id, func(item *beads.WorkItem) { item.Description = description })
}

func TestASplitOfAMemberTheOperatorAddedIsJudgedAgainstThatMembersText(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.addMember()
	b.split(addedPiece, added)
	// The member is edited after the operator put it in; its pieces may ask only
	// for what it said then.
	b.rewrite(added, "edited since, asking for much more")
	b.must(b.judgingByText(b.joiner(), memberSays).Round(context.Background()))
	b.wants(addedPiece, epicprogress.AnswerInside, true)
	asked := b.requests[0].Prompt
	for _, want := range []string{"as it read when named", "The member it was split from, " + added + ", as it read when the operator put it in", memberSays} {
		if !strings.Contains(asked, want) {
			t.Errorf("the prompt does not say %q:\n%s", want, asked)
		}
	}
	if strings.Contains(asked, "edited since") || len(b.entry().CopiedLate) != 0 {
		t.Errorf("it was given the member as it reads now, or copied it late: %+v", b.entry().CopiedLate)
	}
	if piece, _ := b.entry().Asked(addedPiece); !piece.WithSource {
		t.Error("the judgment is not kept as given the text of what it came from")
	}
}

func TestAPieceWiderThanItsMemberIsAQuestionAboutScope(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.addMember()
	b.split(addedPiece, added)
	reports := &fakeReports{}
	joiner := b.asking(reports)
	// A reviewer that finds the piece asks for more than its member and the
	// description.
	joiner.Judge = func(ctx context.Context, request Request) (backend.RunResult, error) {
		b.says("outside")
		return b.joiner().Judge(ctx, request)
	}
	b.must(joiner.Round(context.Background()))
	b.wants(addedPiece, epicprogress.AnswerOutside, false)
	if len(reports.filed) != 1 || !strings.Contains(reports.filed[0].Message, "A question about scope: "+addedPiece) {
		t.Fatalf("filed %+v", reports.filed)
	}
	// It is not judged once more: the judgment was given the member's text.
	b.must(joiner.Round(context.Background()))
	if len(b.requests) != 1 || len(reports.filed) != 1 {
		t.Errorf("%d judgments and %d reports", len(b.requests), len(reports.filed))
	}
}

func TestAMemberPutInBeforeTextsWereCopiedIsCopiedOnceAndMarkedLate(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.items = append(b.items, beads.WorkItem{ID: member, Title: "The first part", Description: memberSays, Status: "open"})
	b.split(piece, member)
	b.must(b.judgingByText(b.joiner(), memberSays).Round(context.Background()))
	b.wants(piece, epicprogress.AnswerInside, true)
	if !strings.Contains(b.requests[0].Prompt, "as it read when a piece of it was first judged") {
		t.Errorf("the copy is not said to be late:\n%s", b.requests[0].Prompt)
	}
	b.rewrite(member, "edited since, asking for much more")
	b.split(piece+"x", member)
	b.must(b.judgingByText(b.joiner(), memberSays).Round(context.Background()))
	b.wants(piece+"x", epicprogress.AnswerInside, true)
	copies := b.entry().CopiedLate
	if len(copies) != 1 || copies[0].WorkItemID != member || copies[0].Text.Description != memberSays || strings.Contains(b.requests[1].Prompt, "edited since") {
		t.Errorf("copied late as %+v", copies)
	}
}

func TestAPieceOfAMemberTheOperatorTookOutDoesNotJoin(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.addMember()
	_, err := b.operator().Remove(epic, added, "not this one", namedAt.Add(30*time.Minute))
	b.must(err)
	b.split(addedPiece, added)
	b.round()
	if len(b.requests) != 0 || b.starts(addedPiece) || b.entry().Has(addedPiece) {
		t.Errorf("%d judgments were made, and it starts: %t", len(b.requests), b.starts(addedPiece))
	}
}

// judgedBefore is a piece of the added member judged outside before members'
// texts were given, with its question filed and not handled.
func (b *bench) judgedBefore(reports *fakeReports) string {
	b.t.Helper()
	b.addMember()
	b.split(addedPiece, added)
	store, err := b.store()
	b.must(err)
	record, err := epicscope.Read(b.root, product)
	b.must(err)
	named, _ := record.Named(epic)
	naming := epicprogress.Of(named)
	item := b.items[len(b.items)-1]
	b.must(store.Ask(naming, epicprogress.Piece{WorkItemID: addedPiece, From: added, Judgment: "review-" + strings.Repeat("b", 32), Digest: epicprogress.TextDigest(item)}))
	b.must(store.Judged(naming, addedPiece, epicprogress.Outcome{Answer: epicprogress.AnswerOutside, Why: "the description does not ask for it", Session: "session-old"}))
	b.must(b.asking(reports).Round(context.Background()))
	asked, _ := b.entry().Asked(addedPiece)
	if len(reports.filed) != 1 || asked.Question != reports.filed[0].ID || len(b.requests) != 0 {
		b.t.Fatalf("the earlier question is %+v, with %d judgments", asked, len(b.requests))
	}
	return asked.Question
}

func TestAPieceJudgedOutsideBeforeIsJudgedOnceMoreAndJoins(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	reports := &fakeReports{}
	question := b.judgedBefore(reports)
	joiner := b.judgingByText(b.asking(reports), memberSays)
	b.must(joiner.Round(context.Background()))
	asked := b.wants(addedPiece, epicprogress.AnswerInside, true)
	if asked.Reread == nil || asked.Reread.Question != question || !asked.Reread.Answered || len(reports.filed) != 1 {
		t.Errorf("kept as %+v, with %d reports", asked.Reread, len(reports.filed))
	}
	b.must(joiner.Round(context.Background()))
	if len(b.requests) != 1 {
		t.Errorf("%d judgments were made", len(b.requests))
	}
}

func TestAPieceJudgedOutsideAgainKeepsItsOneQuestion(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	reports := &fakeReports{}
	question := b.judgedBefore(reports)
	b.says("outside")
	joiner := b.asking(reports)
	b.must(joiner.Round(context.Background()))
	b.must(joiner.Round(context.Background()))
	asked := b.wants(addedPiece, epicprogress.AnswerOutside, false)
	if asked.Question != question || asked.Reread == nil || asked.Reread.Answered || len(reports.filed) != 1 || len(b.requests) != 1 {
		t.Errorf("kept as %+v, with %d reports and %d judgments", asked, len(reports.filed), len(b.requests))
	}
}

func TestAPieceWhoseQuestionWasHandledIsNotJudgedOnceMore(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	reports := &fakeReports{}
	question := b.judgedBefore(reports)
	reports.handle(question, "rewrite it to fit", b.now, ownership.OperatorReason(""))
	b.must(b.asking(reports).Round(context.Background()))
	b.must(b.asking(reports).Round(context.Background()))
	if len(b.requests) != 0 {
		t.Errorf("%d judgments were made", len(b.requests))
	}
}

func TestARestartDuringAJudgmentMadeOnceMoreMakesNoSecondCall(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	reports := &fakeReports{}
	b.judgedBefore(reports)
	stopped := b.asking(reports)
	stopped.Progress = forgetful{stopped.Progress}
	if stopped.Round(context.Background()) == nil || len(b.requests) != 1 {
		t.Fatalf("it was not stopped after the call: %d calls", len(b.requests))
	}
	b.must(b.asking(reports).Round(context.Background()))
	b.wants(addedPiece, epicprogress.AnswerInside, true)
	if len(b.requests) != 1 {
		t.Errorf("%d calls were made", len(b.requests))
	}
}

// answeredByRereading is what the Lead Product Manager's pass is shown beside
// each question about scope.
func (b *bench) answeredByRereading() map[string]string {
	b.t.Helper()
	store, err := b.store()
	b.must(err)
	answered, err := store.AnsweredByRereading()
	b.must(err)
	return answered
}

func TestTheQuestionAJudgmentMadeOnceMoreAnsweredIsMarkedForTheLeadProductManager(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	reports := &fakeReports{}
	question := b.judgedBefore(reports)
	if len(b.answeredByRereading()) != 0 {
		t.Fatal("a question is marked answered before it was judged once more")
	}
	b.must(b.judgingByText(b.asking(reports), memberSays).Round(context.Background()))
	says, marked := b.answeredByRereading()[question]
	if !marked || !strings.Contains(says, addedPiece) || !strings.Contains(says, "joined it") || !strings.Contains(says, "Nothing more is needed") {
		t.Errorf("the question is marked %t: %q", marked, says)
	}
}

func TestAQuestionJudgedOutsideAgainIsNotMarkedAnswered(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	reports := &fakeReports{}
	b.judgedBefore(reports)
	b.says("outside")
	b.must(b.asking(reports).Round(context.Background()))
	if answered := b.answeredByRereading(); len(answered) != 0 {
		t.Errorf("marked %+v", answered)
	}
}
