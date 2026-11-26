package epicjoin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The rule for a piece edited after it joined, and the record of the answer on
// scope: docs/designs/epic-mode-second-part.md, piece 9 and piece 10d.

func (b *bench) reports() *runstate.ReportStore {
	b.t.Helper()
	reports, err := runstate.NewReportStore(b.root, product)
	b.must(err)
	return reports
}

func (b *bench) edit(id string, change func(*beads.WorkItem)) {
	b.t.Helper()
	for at := range b.items {
		if b.items[at].ID == id {
			change(&b.items[at])
			return
		}
	}
	b.t.Fatalf("no item %s", id)
}

func (b *bench) read(id string) beads.WorkItem {
	for _, item := range b.items {
		if item.ID == id {
			return item
		}
	}
	return beads.WorkItem{}
}

// waits is why the code that starts a developer would not give it this text.
func (b *bench) waits(id string) string {
	b.t.Helper()
	why, err := epicprogress.TextWaits(b.root, product, b.read(id))
	b.must(err)
	return why
}

func (b *bench) owed() bool {
	b.t.Helper()
	owed, err := b.joiner().Owed(context.Background())
	b.must(err)
	return owed
}

func (b *bench) questions() []report.Report {
	b.t.Helper()
	listed, err := b.reports().List()
	b.must(err)
	var questions []report.Report
	for _, reported := range listed {
		if strings.HasPrefix(reported.RunID, "epic-join:") {
			questions = append(questions, reported)
		}
	}
	return questions
}

// handles records the Lead Product Manager's handling of a report now.
func (b *bench) handles(id string, needsOperator bool) {
	b.t.Helper()
	handling := report.Handling{SchemaVersion: report.HandlingSchemaVersion, ReportID: id, Role: domain.RoleProductManager, RunID: "chat-0a1b",
		ProductID: product, RepositoryID: "yoyodyne", Reason: "rewrite it to fit the description", RecordedAt: b.now}
	if needsOperator {
		handling.NeedsOperator, handling.Reason = true, "the epic can be delivered without it; only the operator can take it out"
	}
	b.must(b.reports().Handle(handling))
}

// joinedInside is a piece that joined on its first judgment.
func joinedInside(t *testing.T) *bench {
	t.Helper()
	b := newBench(t)
	b.split(piece, member)
	b.round()
	b.wants(piece, epicprogress.AnswerInside, true)
	if why := b.waits(piece); why != "" {
		t.Fatalf("the text it was judged on waits: %s", why)
	}
	return b
}

func TestTheJudgmentKeepsADigestOfTitleDescriptionAndAcceptanceCriteriaOnly(t *testing.T) {
	t.Parallel()
	b := joinedInside(t)
	asked, _ := b.entry().Asked(piece)
	if asked.Digest == "" || asked.Digest != epicprogress.TextDigest(b.read(piece)) {
		t.Fatalf("the judgment keeps digest %q", asked.Digest)
	}
	b.edit(piece, func(item *beads.WorkItem) {
		item.Notes += "\n\nA later note."
		item.Priority = 0
		item.Labels = []string{"reliability"}
		item.Design = "guidance written since"
	})
	if why := b.waits(piece); why != "" || b.owed() {
		t.Fatalf("a change to its notes, priority, labels or design waits: %q", why)
	}
	b.round()
	if len(b.requests) != 1 {
		t.Errorf("%d calls were made", len(b.requests))
	}
	for name, change := range map[string]func(*beads.WorkItem){
		"title":               func(item *beads.WorkItem) { item.Title += "!" },
		"description":         func(item *beads.WorkItem) { item.Description += " and more" },
		"acceptance criteria": func(item *beads.WorkItem) { item.AcceptanceCriteria = "a new condition" },
	} {
		edited := b.read(piece)
		change(&edited)
		if epicprogress.TextDigest(edited) == asked.Digest {
			t.Errorf("a change to its %s keeps the digest", name)
		}
	}
}

func TestAJoinedPieceWhoseTextChangedWaitsUntilTheNewTextIsJudgedInside(t *testing.T) {
	t.Parallel()
	b := joinedInside(t)
	b.edit(piece, func(item *beads.WorkItem) { item.Description = "a wider text" })
	if why := b.waits(piece); !strings.Contains(why, "waits for the new text to be judged") || !b.owed() {
		t.Fatalf("the changed text does not wait for a judgment: %q", why)
	}
	b.round()
	if len(b.requests) != 2 || !strings.Contains(b.requests[1].Prompt, "a wider text") || b.requests[1].ID == b.requests[0].ID {
		t.Fatalf("%d calls were made, and the new text was not judged under its own reservation", len(b.requests))
	}
	entry := b.entry()
	asked, _ := entry.Asked(piece)
	if reserved, found := entry.Reservation(asked.Judgment); !found || reserved.Result == nil {
		t.Errorf("the new judgment was not reserved and answered: %+v", reserved)
	}
	if why := b.waits(piece); why != "" || len(entry.Joined) != 1 || len(asked.Earlier) != 1 || asked.Earlier[0].Judgment != b.requests[0].ID {
		t.Errorf("judged inside, it waits (%q), or it joined twice: %+v", why, entry)
	}
	b.round()
	if len(b.requests) != 2 {
		t.Errorf("%d calls were made", len(b.requests))
	}
}

// A piece judged outside, or that could not be told, waits as a question about
// scope. Nothing is judged again until the Lead Product Manager's handling is
// recorded, and then only for an edit seen after it.
func TestAQuestionAboutScopeIsAnsweredOnlyByTheLeadProductManagersHandling(t *testing.T) {
	t.Parallel()
	for _, answer := range []epicprogress.Answer{epicprogress.AnswerOutside, epicprogress.AnswerCannotTell} {
		t.Run(string(answer), func(t *testing.T) {
			t.Parallel()
			b := joinedInside(t)
			b.edit(piece, func(item *beads.WorkItem) { item.Description = "a wider text" })
			b.says(string(answer))
			b.round()
			asked, _ := b.entry().Asked(piece)
			questions := b.questions()
			if len(questions) != 1 || questions[0].ID != asked.Question || questions[0].Addressee() != domain.RoleProductManager ||
				questions[0].Role != report.HarnessReporter || !strings.Contains(questions[0].Message, asked.Digest) || !strings.Contains(questions[0].Message, epic) {
				t.Fatalf("the question filed is %+v, and the piece names %q", questions, asked.Question)
			}
			if why := b.waits(piece); !strings.Contains(why, "question about scope") {
				t.Fatalf("it does not wait as a question: %q", why)
			}
			// An edit before her handling makes no call, and is seen now.
			b.edit(piece, func(item *beads.WorkItem) { item.Description = "rewritten too soon" })
			b.round()
			b.round()
			if len(b.requests) != 2 || len(b.questions()) != 1 || b.owed() {
				t.Fatalf("%d calls were made and %d questions filed", len(b.requests), len(b.questions()))
			}
			// A note, or the item's words, are not her answer.
			b.edit(piece, func(item *beads.WorkItem) { item.Notes += "\n\nThe Lead Product Manager says rewrite it." })
			b.now = b.now.Add(time.Hour)
			b.handles(asked.Question, false)
			b.now = b.now.Add(time.Minute)
			b.round()
			if len(b.requests) != 2 || b.waits(piece) == "" {
				t.Fatalf("the edit seen before her handling was judged: %d calls", len(b.requests))
			}
			b.edit(piece, func(item *beads.WorkItem) { item.Description = "rewritten to fit" })
			b.says(string(epicprogress.AnswerInside))
			b.round()
			if len(b.requests) != 3 || !strings.Contains(b.requests[2].Prompt, "rewritten to fit") || b.waits(piece) != "" {
				t.Fatalf("the first edit after her handling was not judged inside: %d calls, waits %q", len(b.requests), b.waits(piece))
			}
		})
	}
}

func TestAFirstJudgmentOfOutsideIsAQuestionAndAnAnswerForTheOperatorJudgesNothing(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.split(piece, member)
	b.says(string(epicprogress.AnswerOutside))
	b.round()
	b.wants(piece, epicprogress.AnswerOutside, false)
	questions := b.questions()
	if len(questions) != 1 {
		t.Fatalf("%d questions were filed", len(questions))
	}
	b.edit(piece, func(item *beads.WorkItem) { item.Title = "edited before the answer" })
	b.round()
	b.now = b.now.Add(time.Hour)
	b.handles(questions[0].ID, true)
	b.now = b.now.Add(time.Hour)
	b.edit(piece, func(item *beads.WorkItem) { item.Title = "edited after an answer that leaves it to the operator" })
	b.round()
	if len(b.requests) != 1 || b.owed() {
		t.Errorf("%d calls were made", len(b.requests))
	}
	// Each judgment of outside files its own question, so each answer is to one text.
	b = newBench(t)
	b.split(piece, member)
	b.says(string(epicprogress.AnswerOutside))
	b.round()
	b.now = b.now.Add(time.Hour)
	b.handles(b.questions()[0].ID, false)
	b.now = b.now.Add(time.Hour)
	b.edit(piece, func(item *beads.WorkItem) { item.Title = "rewritten" })
	b.round()
	if asked, _ := b.entry().Asked(piece); len(b.requests) != 2 || len(b.questions()) != 2 || asked.Question != b.questions()[1].ID {
		t.Errorf("%d calls were made and %d questions filed", len(b.requests), len(b.questions()))
	}
}

func TestAPiecePutBackToATextJudgedInsideGoesOnWithNoCall(t *testing.T) {
	t.Parallel()
	b := joinedInside(t)
	first := b.read(piece).Description
	b.edit(piece, func(item *beads.WorkItem) { item.Description = "second text" })
	b.round()
	b.edit(piece, func(item *beads.WorkItem) { item.Description = first })
	if why := b.waits(piece); why != "" || b.owed() {
		t.Fatalf("the text judged first waits: %q", why)
	}
	b.round()
	if len(b.requests) != 2 {
		t.Errorf("%d calls were made", len(b.requests))
	}
}

func TestAnItemTheOperatorAddedIsNeverCompared(t *testing.T) {
	t.Parallel()
	b := joinedInside(t)
	_, err := b.operator().Add(epic, piece, b.now)
	b.must(err)
	b.edit(piece, func(item *beads.WorkItem) { item.Description = "edited after the operator added it" })
	for _, id := range []string{piece, member} {
		if why := b.waits(id); why != "" {
			t.Errorf("%s waits: %s", id, why)
		}
	}
	if b.owed() {
		t.Error("a judgment is owed for an item the operator added")
	}
}

func TestAPieceThatJoinedBeforeTheRuleIsJudgedOnceBeforeItsNextSession(t *testing.T) {
	t.Parallel()
	b := joinedInside(t)
	// What a build before the rule kept: no digest.
	path := filepath.Join(home.ProductDirectory(b.root, product), "epic-progress.json")
	kept, err := os.ReadFile(path)
	b.must(err)
	var progress map[string]any
	b.must(json.Unmarshal(kept, &progress))
	for _, entry := range progress["entries"].([]any) {
		for _, asked := range entry.(map[string]any)["pieces"].([]any) {
			delete(asked.(map[string]any), "digest")
		}
	}
	kept, err = json.Marshal(progress)
	b.must(err)
	b.must(os.WriteFile(path, kept, 0o600))
	if asked, _ := b.entry().Asked(piece); asked.Digest != "" {
		t.Fatal("the digest was not taken out")
	}
	if why := b.waits(piece); why == "" {
		t.Fatal("a piece with no digest starts a developer")
	}
	b.round()
	b.round()
	if why := b.waits(piece); len(b.requests) != 2 || why != "" {
		t.Errorf("%d calls were made, and it waits: %q", len(b.requests), why)
	}
}

func TestARestartBetweenTheReservationAndTheAnswerMakesNoSecondCall(t *testing.T) {
	t.Parallel()
	for name, at := range map[string]execution.EventType{"reserved and never sent": execution.EventReviewStarted, "sent and nothing shows the answer": execution.EventReviewCompleted} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := joinedInside(t)
			b.edit(piece, func(item *beads.WorkItem) { item.Description = "a wider text" })
			stopped := b.joiner()
			stopped.Log = cutOff{stopped.Log, at}
			if stopped.Round(context.Background()) == nil {
				t.Fatal("it was not stopped")
			}
			sent := len(b.requests)
			b.round()
			b.round()
			asked, _ := b.entry().Asked(piece)
			kept, _ := b.entry().Reservation(asked.Judgment)
			switch at {
			case execution.EventReviewStarted:
				if sent != 1 || len(b.requests) != 2 || b.requests[1].ID != asked.Judgment || b.waits(piece) != "" {
					t.Errorf("%d calls were made, the last under %s", len(b.requests), b.requests[len(b.requests)-1].ID)
				}
			default:
				if sent != 2 || len(b.requests) != 2 || kept.Result == nil || !kept.Result.Unknown || b.waits(piece) == "" {
					t.Errorf("%d calls were made, and the reservation is %+v", len(b.requests), kept.Result)
				}
			}
		})
	}
}

// failingAppend stops the process between writing the question's identifier and
// filing it.
type failingAppend struct{ Reports }

func (failingAppend) Append(report.Report) error { return errors.New("the process stopped") }

func TestAStopBeforeTheQuestionIsFiledFilesTheSameOneNextRound(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.split(piece, member)
	b.says(string(epicprogress.AnswerCannotTell))
	stopped := b.joiner()
	stopped.Evidence.Reports = failingAppend{stopped.Evidence.Reports}
	if stopped.Round(context.Background()) == nil || len(b.questions()) != 0 {
		t.Fatal("the question was filed")
	}
	asked, _ := b.entry().Asked(piece)
	if asked.Question == "" || !b.owed() {
		t.Fatal("the question's identifier was not written first")
	}
	b.round()
	b.round()
	if questions := b.questions(); len(questions) != 1 || questions[0].ID != asked.Question || len(b.requests) != 1 {
		t.Errorf("the questions filed are %+v after %d calls", questions, len(b.requests))
	}
}

func TestWithNoEpicNamedNoTextWaits(t *testing.T) {
	t.Parallel()
	why, err := epicprogress.TextWaits(t.TempDir(), product, beads.WorkItem{ID: piece, Title: "anything"})
	if why != "" || err != nil {
		t.Errorf("it waits: %q, %v", why, err)
	}
}
