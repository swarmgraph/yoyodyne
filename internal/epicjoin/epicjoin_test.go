package epicjoin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	product = "yoyodyne"
	epic    = "yoyodyne-ep"
	member  = "yoyodyne-ep.1"
	piece   = "yoyodyne-ep.1.1"
)

var namedAt = time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)

// bench is one named epic with two members, a tracker, and a reviewer that answers
// as it is told to.
type bench struct {
	t        *testing.T
	root     string
	items    []beads.WorkItem
	answer   string
	failed   *backend.RunResult
	err      error
	paused   bool
	unread   error
	requests []Request
	now      time.Time
}

func newBench(t *testing.T) *bench {
	t.Helper()
	b := &bench{t: t, root: t.TempDir(), now: namedAt.Add(2 * time.Hour)}
	b.says("inside")
	_, err := b.operator().Start(epicscope.Naming{Epic: epic, From: "terminal", Description: "as it read when named", Members: []string{member, "yoyodyne-ep.2"}}, namedAt)
	b.must(err)
	return b
}

func (b *bench) must(err error) {
	b.t.Helper()
	if err != nil {
		b.t.Fatal(err)
	}
}

func (b *bench) operator() *epicscope.Operator {
	b.t.Helper()
	operator, err := epicscope.ForOperator(b.root, product)
	b.must(err)
	return operator.Knowing(epicprogress.Holding(b.root, product))
}

func (b *bench) says(answer string) {
	b.answer = `{"answer":"` + answer + `","why":"because of the text"}`
}

// split is an item the development manager made by splitting another.
func (b *bench) split(id, from string) {
	b.item(id, "Created under "+from+", decomposing it by the development manager in conversation chat-0a1b, after turn 7.\n\nReason: too large.")
}

func (b *bench) item(id, notes string) {
	b.items = append(b.items, beads.WorkItem{ID: id, Title: "Title " + id, Description: "Text of " + id, Status: "open", Notes: notes, CreatedAt: namedAt.Add(time.Hour)})
}

// joiner is a process of the harness that has just started.
func (b *bench) joiner() Joiner {
	b.t.Helper()
	store, err := epicprogress.Open(b.root, product)
	b.must(err)
	log, err := runstate.NewBranchReviewStore(b.root, product)
	b.must(err)
	return Joiner{StateRoot: b.root, Product: product, Progress: store, Log: log, NewID: runstate.NewBranchReviewID,
		SplitFrom: chat.SplitByDevelopmentManager, Now: func() time.Time { return b.now }, MayStart: func(start maystart.Start) maystart.Decision { return maystart.Decide(b.root, product, start) },
		Paused: func() (bool, error) { return b.paused, b.unread },
		Items:  func(context.Context) ([]beads.WorkItem, error) { return b.items, nil },
		Judge: func(_ context.Context, request Request) (backend.RunResult, error) {
			b.requests = append(b.requests, request)
			// What a provider's own session writes to the same log changes nothing.
			event, err := execution.NewEvent(request.ID, request.LastSequence+1, b.now, execution.EventReviewCompleted, "claude", nil)
			b.must(errors.Join(err, request.Sink(event)))
			if b.failed != nil {
				return *b.failed, b.err
			}
			return backend.RunResult{FinalText: b.answer, SessionID: fmt.Sprintf("session-%d", len(b.requests))}, nil
		}}
}

func (b *bench) round() {
	b.t.Helper()
	b.must(b.joiner().Round(context.Background()))
}

func (b *bench) entry() epicprogress.Entry {
	b.t.Helper()
	record, err := epicscope.Read(b.root, product)
	b.must(err)
	view, err := epicprogress.Read(b.root, product, record)
	b.must(err)
	named, _ := record.Named(epic)
	entry, _ := view.Entry(epicprogress.Of(named))
	return entry
}

func (b *bench) starts(item string) bool {
	return maystart.Decide(b.root, product, maystart.Run(maystart.ReadyWork, item)).MayStart()
}

func (b *bench) wants(item string, answer epicprogress.Answer, starts bool) epicprogress.Piece {
	b.t.Helper()
	asked, found := b.entry().Asked(item)
	if !found || asked.Outcome == nil || asked.Outcome.Answer != answer || b.starts(item) != starts || b.entry().Has(item) != starts {
		b.t.Fatalf("%s is kept as %+v with outcome %+v, starts: %t; want %q, starts: %t", item, asked, asked.Outcome, b.starts(item), answer, starts)
	}
	return asked
}

func TestASplitOfAMemberJoinsOnlyWhereItIsJudgedInside(t *testing.T) {
	t.Parallel()
	for answer, joins := range map[epicprogress.Answer]bool{epicprogress.AnswerInside: true, epicprogress.AnswerOutside: false, epicprogress.AnswerCannotTell: false} {
		t.Run(string(answer), func(t *testing.T) {
			t.Parallel()
			b := newBench(t)
			b.split(piece, member)
			b.says(string(answer))
			if b.starts(piece) {
				t.Fatal("it starts before it is judged")
			}
			b.round()
			b.wants(piece, answer, joins)
			if joined := b.entry().Joined; joins && (joined[0].From != member || joined[0].Session != "session-1" || !strings.Contains(joined[0].Judgment, b.requests[0].ID)) {
				t.Errorf("it joined as %+v", joined[0])
			}
			b.round()
			if owed, err := b.joiner().Owed(context.Background()); err != nil || owed || len(b.requests) != 1 {
				t.Errorf("it was judged %d times and is owed: %t, %v", len(b.requests), owed, err)
			}
		})
	}
}

// A replacement is written under the member it stands for, whether or not that
// member is closed, and a piece that joined may be split in turn.
func TestAReplacementOfAClosedMemberAndASplitOfAPieceJoin(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.items = append(b.items, beads.WorkItem{ID: member, Status: "closed"})
	b.split(piece, member)
	b.round()
	b.wants(piece, epicprogress.AnswerInside, true)
	b.split(piece+".1", piece)
	b.items[len(b.items)-1].CreatedAt = time.Now().Add(time.Hour)
	b.round()
	b.wants(piece+".1", epicprogress.AnswerInside, true)
}

func TestAnAnswerThatCannotBeReadIsCannotTell(t *testing.T) {
	t.Parallel()
	for _, answer := range []string{"yes", `{"answer":"yes","why":"it does"}`, `{"answer":"inside"}`} {
		b := newBench(t)
		b.split(piece, member)
		b.answer = answer
		b.round()
		b.wants(piece, epicprogress.AnswerCannotTell, false)
	}
}

func TestAnItemNotMadeByASplitOfAMemberIsNeverJudged(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.split("yoyodyne-attached", epic)
	b.split("yoyodyne-other", "yoyodyne-outside")
	b.item("yoyodyne-admitted", "Admitted to the backlog by the Lead Product Manager in conversation chat-0a1b, after turn 7.")
	b.item("yoyodyne-architect", "Created under "+member+", decomposing it by the architect in conversation chat-0a1b, after turn 7.")
	// A role's later note cannot say it, because it only follows what opens them.
	b.item("yoyodyne-noted", "Admitted to the backlog.\n\nCreated under "+member+", decomposing it by the development manager in conversation chat-0a1b, after turn 7.")
	b.split("yoyodyne-before", member)
	b.items[len(b.items)-1].CreatedAt = namedAt.Add(-time.Hour)
	for _, id := range []string{"yoyodyne-moved", "yoyodyne-prerequisite"} {
		b.item(id, "Admitted to the backlog.")
	}
	b.items[len(b.items)-2].Parent = member
	b.items = append(b.items, beads.WorkItem{ID: member, Status: "open", Dependencies: []beads.Dependency{{IssueID: member, ID: "yoyodyne-prerequisite", Type: "blocks"}}})
	b.round()
	for _, item := range b.items {
		if item.ID != member && b.starts(item.ID) {
			t.Errorf("%s starts", item.ID)
		}
	}
	if len(b.requests) != 0 || len(b.entry().Pieces) != 0 {
		t.Errorf("%d judgments were made and %+v is kept", len(b.requests), b.entry().Pieces)
	}
}

func TestTheThirteenthPieceDoesNotJoinAndARemovedOneStillCounts(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	// What the operator adds is not counted.
	_, err := b.operator().Add(epic, "yoyodyne-added", namedAt.Add(time.Hour))
	b.must(err)
	for at := range 11 {
		b.split(fmt.Sprintf("yoyodyne-ep.1.%d", at+1), member)
	}
	b.round()
	_, err = b.operator().Remove(epic, piece, "not needed", b.now)
	b.must(err)
	b.split("yoyodyne-ep.1.12", member)
	b.split("yoyodyne-ep.1.13", member)
	b.round()
	b.wants("yoyodyne-ep.1.12", epicprogress.AnswerInside, true)
	b.wants("yoyodyne-ep.1.13", epicprogress.AnswerLimit, false)
	if len(b.requests) != 12 {
		t.Errorf("%d judgments were paid for, want 12", len(b.requests))
	}
	if owed, err := b.joiner().Owed(context.Background()); owed || err != nil || b.starts(piece) {
		t.Errorf("the removed piece is judged again: %t, or starts: %v", owed, err)
	}
	_, err = b.operator().Add(epic, "yoyodyne-ep.1.13", b.now)
	b.must(err)
	if !b.starts("yoyodyne-ep.1.13") || len(b.entry().Joined) != epicprogress.MaxJoined {
		t.Error("the operator's own act was refused or counted")
	}
}

func TestTheJudgingSessionIsFreshAndIsGivenTheCopyAndWhatWasTakenOut(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.items = append(b.items, beads.WorkItem{ID: epic, Status: "open", Description: "edited since"}, beads.WorkItem{ID: "yoyodyne-ep.2", Title: "The one taken out", Status: "open"})
	_, err := b.operator().Remove(epic, "yoyodyne-ep.2", "we do not need the second part", b.now)
	b.must(err)
	b.split(piece, member)
	b.items[len(b.items)-1].AcceptanceCriteria = "what done means"
	b.round()
	asked := b.requests[0]
	for _, want := range []string{"as it read when named", "yoyodyne-ep.2", "The one taken out", "we do not need the second part", "Title " + piece, "Text of " + piece, "what done means"} {
		if !strings.Contains(asked.Prompt, want) {
			t.Errorf("the prompt does not say %q:\n%s", want, asked.Prompt)
		}
	}
	if strings.Contains(asked.Prompt, "edited since") || !asked.Permission.Granted() {
		t.Error("it was given the description as it reads now, or nothing permitted the call")
	}
}

// cutOff stops the process at a step of the one call.
type cutOff struct {
	Log
	at execution.EventType
}

func (c cutOff) AppendEvent(event execution.Event) error {
	if event.Type == c.at && event.Source == source {
		return errors.New("the process stopped")
	}
	return c.Log.AppendEvent(event)
}

func TestAfterARestartACallIsLookedUpBeforeItIsMadeAgain(t *testing.T) {
	t.Parallel()
	t.Run("reserved and never sent", func(t *testing.T) {
		t.Parallel()
		b := newBench(t)
		b.split(piece, member)
		stopped := b.joiner()
		stopped.Log = cutOff{stopped.Log, execution.EventReviewStarted}
		if stopped.Round(context.Background()) == nil || len(b.requests) != 0 || len(b.entry().Reservations) != 1 {
			t.Fatal("it was not stopped between the reservation and the call")
		}
		b.round()
		b.wants(piece, epicprogress.AnswerInside, true)
		if len(b.requests) != 1 {
			t.Errorf("%d calls were made", len(b.requests))
		}
	})
	t.Run("sent and nothing shows the answer", func(t *testing.T) {
		t.Parallel()
		b := newBench(t)
		b.split(piece, member)
		stopped := b.joiner()
		stopped.Log = cutOff{stopped.Log, execution.EventReviewCompleted}
		if stopped.Round(context.Background()) == nil {
			t.Fatal("it was not stopped")
		}
		b.round()
		b.round()
		kept, _ := b.entry().Reservation(b.requests[0].ID)
		if len(b.requests) != 1 || b.starts(piece) || kept.Result == nil || !kept.Result.Unknown {
			t.Errorf("%d calls were made, and the reservation is %+v", len(b.requests), kept)
		}
	})
	t.Run("answered and not yet kept", func(t *testing.T) {
		t.Parallel()
		b := newBench(t)
		b.split(piece, member)
		stopped := b.joiner()
		stopped.Progress = forgetful{stopped.Progress}
		if stopped.Round(context.Background()) == nil || b.starts(piece) {
			t.Fatal("it was not stopped before the answer was kept")
		}
		b.round()
		b.wants(piece, epicprogress.AnswerInside, true)
		if len(b.requests) != 1 {
			t.Errorf("%d calls were made", len(b.requests))
		}
	})
}

// forgetful stops the process once the call has answered and before the answer is
// kept beside its reservation.
type forgetful struct{ Progress }

func (f forgetful) Call(naming epicprogress.Naming, id, purpose string, lookUp func() (string, epicprogress.Looked, error), send func() (string, error)) (string, error) {
	return f.Progress.Call(naming, id, purpose, lookUp, func() (string, error) {
		_, err := send()
		return "", errors.Join(err, errors.New("the process stopped"))
	})
}

func TestACallTheProviderRefusedIsMadeAgainLater(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.split(piece, member)
	b.failed = &backend.RunResult{UsageLimit: &backend.UsageLimit{}, IsError: true, SessionID: "session-x"}
	if b.joiner().Round(context.Background()) == nil {
		t.Fatal("the refusal was not said")
	}
	b.failed = nil
	b.round()
	if len(b.requests) != 1 {
		t.Fatal("it was made again at once")
	}
	b.now = b.now.Add(RetryAfter)
	b.round()
	b.wants(piece, epicprogress.AnswerInside, true)
	if first, second := b.requests[0], b.requests[1]; first.ID != second.ID || second.LastSequence <= first.LastSequence {
		t.Error("the second call was not the one reserved")
	}
}

func TestNothingIsJudgedForAnEpicThatEndedOrWithEpicModeOff(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.split(piece, member)
	found, items, err := b.joiner().owed(context.Background())
	b.must(err)
	_, err = b.operator().TurnOff()
	b.must(err)
	b.round()
	// Turned off between its being found and its being made.
	one := found[0]
	one.asked, one.piece.Judgment = true, "review-"+strings.Repeat("a", 32)
	b.must(b.joiner().judge(context.Background(), one, items))
	if len(b.requests) != 0 {
		t.Error("a judgment was made with epic mode off")
	}
}

func TestAPieceOfAMemberTakenOutMeanwhileDoesNotJoin(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.split(piece, member)
	joiner := b.joiner()
	judge := joiner.Judge
	joiner.Judge = func(ctx context.Context, request Request) (backend.RunResult, error) {
		_, err := b.operator().Remove(epic, member, "not this", b.now)
		b.must(err)
		return judge(ctx, request)
	}
	b.must(joiner.Round(context.Background()))
	b.wants(piece, epicprogress.AnswerOutside, false)
}

func TestTwoProcessesDoNotMakeTheSameCall(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.split(piece, member)
	joiner := b.joiner()
	judge := joiner.Judge
	joiner.Judge = func(ctx context.Context, request Request) (backend.RunResult, error) {
		b.round() // another process, while this one's call is under way
		return judge(ctx, request)
	}
	b.must(joiner.Round(context.Background()))
	if len(b.requests) != 1 {
		t.Errorf("%d calls were made", len(b.requests))
	}
}

// A session that did not complete answered nothing, so it is no question about
// scope. After several the development manager decides.
func TestASessionThatFailedIsNotAnAnswer(t *testing.T) {
	t.Parallel()
	ended := func(status execution.ProcessStatus) backend.RunResult {
		return backend.RunResult{IsError: true, SessionID: "session-x", Process: execution.ProcessResult{Status: status}}
	}
	for name, test := range map[string]struct {
		result backend.RunResult
		err    error
	}{
		"stalled":           {result: ended(execution.ProcessStalled)},
		"timed out":         {result: ended(execution.ProcessTimedOut)},
		"model unavailable": {result: backend.RunResult{IsError: true, SessionID: "session-x", ModelUnavailable: &backend.ModelUnavailable{}}},
		"process failed":    {result: backend.RunResult{SessionID: "session-x"}, err: errors.New("exit status 1")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := newBench(t)
			b.split(piece, member)
			b.failed, b.err = &test.result, test.err
			if b.joiner().Round(context.Background()) == nil {
				t.Fatal("the failure was not said")
			}
			if asked, _ := b.entry().Asked(piece); asked.Outcome != nil || b.starts(piece) {
				t.Fatalf("it is kept as %+v", asked.Outcome)
			}
			again := *b
			again.now, again.failed = b.now.Add(RetryAfter), nil
			again.round()
			again.wants(piece, epicprogress.AnswerInside, true)
		})
	}
	b := newBench(t)
	b.split(piece, member)
	failed := ended(execution.ProcessStalled)
	b.failed = &failed
	for range maxFailures + 3 {
		_ = b.joiner().Round(context.Background())
		b.now = b.now.Add(RetryAfter)
	}
	asked, _ := b.entry().Asked(piece)
	kept, _ := b.entry().Reservation(asked.Judgment)
	if len(b.requests) != maxFailures || asked.Outcome != nil || kept.Result == nil || !kept.Result.Unknown || kept.Result.Says != "6 sessions ended without answering" {
		t.Errorf("%d calls were made, the outcome is %+v and the reservation %+v", len(b.requests), asked.Outcome, kept.Result)
	}
	if owed, err := b.joiner().Owed(context.Background()); owed || err != nil {
		t.Errorf("it is still owed: %t, %v", owed, err)
	}
}

// A refusal costs nothing, so a long one never uses up what may fail.
func TestARefusalIsNotCountedAsAFailure(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.split(piece, member)
	b.failed = &backend.RunResult{UsageLimit: &backend.UsageLimit{}, IsError: true, SessionID: "session-x"}
	for range maxFailures + 3 {
		_ = b.joiner().Round(context.Background())
		b.now = b.now.Add(RetryAfter)
	}
	b.failed = nil
	b.round()
	b.wants(piece, epicprogress.AnswerInside, true)
}

func TestAPausePlacedDuringARoundStopsTheRestOfIt(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.split(piece, member)
	b.split("yoyodyne-ep.1.2", member)
	joiner := b.joiner()
	judge := joiner.Judge
	joiner.Judge = func(ctx context.Context, request Request) (backend.RunResult, error) {
		b.paused = true
		return judge(ctx, request)
	}
	b.must(joiner.Round(context.Background()))
	b.wants(piece, epicprogress.AnswerInside, true)
	if len(b.requests) != 1 || b.starts("yoyodyne-ep.1.2") {
		t.Fatalf("%d calls were made", len(b.requests))
	}
	// A pause that cannot be read makes no call either.
	b.paused, b.unread = false, errors.New("cannot be read")
	if b.joiner().Round(context.Background()) == nil || len(b.requests) != 1 {
		t.Fatalf("%d calls were made", len(b.requests))
	}
	b.unread = nil
	b.round()
	b.wants("yoyodyne-ep.1.2", epicprogress.AnswerInside, true)
	if len(b.requests) != 2 {
		t.Errorf("%d calls were made", len(b.requests))
	}
}

// endsFirst has the operator end the epic once the call is reserved and before it
// is sent.
type endsFirst struct {
	Progress
	end func()
}

func (e endsFirst) Call(naming epicprogress.Naming, id, purpose string, lookUp func() (string, epicprogress.Looked, error), send func() (string, error)) (string, error) {
	return e.Progress.Call(naming, id, purpose, lookUp, func() (string, error) {
		e.end()
		return send()
	})
}

func TestTheOneDecisionIsAskedBeforeACallIsSent(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.split(piece, member)
	joiner := b.joiner()
	joiner.Progress = endsFirst{joiner.Progress, func() {
		_, err := b.operator().End(epic, "", b.now)
		b.must(err)
	}}
	err := joiner.Round(context.Background())
	if err == nil || !strings.Contains(err.Error(), "has ended") || len(b.requests) != 0 {
		t.Fatalf("%d calls were made: %v", len(b.requests), err)
	}
	// Nothing was written as sent.
	asked, _ := b.entry().Asked(piece)
	if events, err := joiner.Log.LoadEvents(asked.Judgment); err != nil || len(events) != 0 {
		t.Errorf("its log holds %+v, %v", events, err)
	}
}
