package exchange

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// What the conductor does to a thread whose process is gone. These are the
// writes; which threads they are made against is Survey's judgment, and is
// tested there.

// A reclaimed round is a round that was spent and stays spent. Reclaiming says
// why it produced nothing and nothing else: it does not give the round back, it
// does not close the exchange, and it does not invent an answer.
func TestReclaimingARoundSaysWhyItProducedNothingAndGivesNothingBack(t *testing.T) {
	t.Parallel()

	store := &recordingStore{}
	recorded := open("exchange-"+strings.Repeat("a", 32), 3)
	recorded.Rounds = []Round{asked(1, "yoyo pid 4242")}
	store.saved = recorded

	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	conductor := Conductor{Store: store, ProductID: "yoyodyne", Now: func() time.Time { return at }}
	after, err := conductor.Reclaim(recorded, "yoyo pid 4242 was carrying it and is gone; nothing came back")
	if err != nil {
		t.Fatalf("Reclaim() = %v", err)
	}
	if after.Spent() != 1 {
		t.Errorf("Spent() = %d, want the round still spent", after.Spent())
	}
	if !after.Open() {
		t.Errorf("Outcome = %q, want the exchange still open", after.Outcome)
	}
	round := after.Rounds[0]
	if round.Answer != "" {
		t.Errorf("Answer = %q, want no answer invented", round.Answer)
	}
	if !strings.Contains(round.Problem, "yoyo pid 4242") {
		t.Errorf("Problem = %q, want the reason recorded where the answer would have been", round.Problem)
	}
	if round.AnsweredAt == nil || !round.AnsweredAt.Equal(at) {
		t.Errorf("AnsweredAt = %v, want the round closed at the moment it was reclaimed", round.AnsweredAt)
	}
	if _, interrupted := after.Interrupted(); interrupted {
		t.Errorf("the reclaimed round still reads as interrupted")
	}
	if err := after.Validate(); err != nil {
		t.Errorf("the reclaimed record does not validate: %v", err)
	}
	if store.saves != 1 {
		t.Errorf("saves = %d, want the reclaim written once", store.saves)
	}
}

// Reclaiming is refused where there is nothing to reclaim, so a pass that
// misread its own plan writes nothing rather than overwriting an answer.
func TestReclaimingIsRefusedWhereNoRoundIsWaiting(t *testing.T) {
	t.Parallel()

	store := &recordingStore{}
	conductor := Conductor{Store: store, ProductID: "yoyodyne"}

	answeredThread := open("exchange-"+strings.Repeat("b", 32), 3)
	answeredThread.Rounds = []Round{answered(1, "what I think", "")}
	if _, err := conductor.Reclaim(answeredThread, "gone"); err == nil {
		t.Errorf("Reclaim() over an answered round = nil, want a refusal")
	}
	if store.saves != 0 {
		t.Errorf("saves = %d, want nothing written by a refused reclaim", store.saves)
	}
}

// A thread that has spent every round it was opened with is closed as
// unresolved and the operator is told. That is the whole point of the cap: an
// ending nobody hears about is the silence it exists to break.
func TestEndingASpentThreadClosesItUnresolvedAndTellsTheOperator(t *testing.T) {
	t.Parallel()

	store := &recordingStore{}
	reports := &collectedReports{}
	recorded := open("exchange-"+strings.Repeat("c", 32), 2)
	recorded.RepositoryID = "yoyodyne"
	recorded.Rounds = []Round{answered(1, "one", ""), answered(2, "two", "")}
	store.saved = recorded

	at := time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)
	conductor := Conductor{Store: store, Reports: reports, ProductID: "yoyodyne", Now: func() time.Time { return at }}
	after, err := conductor.Exhaust(recorded)
	if err != nil {
		t.Fatalf("Exhaust() = %v", err)
	}
	if after.Outcome != OutcomeUnresolved {
		t.Errorf("Outcome = %q, want unresolved", after.Outcome)
	}
	if after.ClosedAt == nil || !after.ClosedAt.Equal(at) {
		t.Errorf("ClosedAt = %v, want the moment it was closed", after.ClosedAt)
	}
	if err := after.Validate(); err != nil {
		t.Errorf("the closed record does not validate: %v", err)
	}
	if len(reports.collected) != 1 {
		t.Fatalf("collected = %#v, want the operator told exactly once", reports.collected)
	}
	told := reports.collected[0]
	if told.Severity != report.SeverityWarning {
		t.Errorf("Severity = %q, want a warning: nothing is broken and two roles did not settle something", told.Severity)
	}
	if !strings.Contains(told.Message, after.ID) {
		t.Errorf("Message = %q, want the exchange named so it can be read", told.Message)
	}
}

// Ending is refused on a thread that still has rounds left, so a pass that
// misread its own plan cannot close a conversation somebody was still having.
func TestEndingIsRefusedOnAThreadThatStillHasRoundsLeft(t *testing.T) {
	t.Parallel()

	store := &recordingStore{}
	reports := &collectedReports{}
	recorded := open("exchange-"+strings.Repeat("d", 32), 3)
	recorded.Rounds = []Round{answered(1, "one", "")}

	conductor := Conductor{Store: store, Reports: reports, ProductID: "yoyodyne"}
	if _, err := conductor.Exhaust(recorded); err == nil {
		t.Errorf("Exhaust() = nil, want a refusal while rounds remain")
	}
	if store.saves != 0 || len(reports.collected) != 0 {
		t.Errorf("saves = %d, collected = %d; want nothing written and nobody told", store.saves, len(reports.collected))
	}
}

// The process taking a round is named on it before the provider is reached, so a
// round that dies mid-flight leaves a record naming what to go and look for.
func TestTheProcessTakingARoundIsNamedOnItBeforeTheProviderIsReached(t *testing.T) {
	t.Parallel()

	store := &recordingStore{}
	conductor := Conductor{
		Store:        store,
		Voice:        refusingVoice{},
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
		Holder:       "yoyo pid 777",
		MaxRounds:    3,
		NewID:        func() (string, error) { return "exchange-" + strings.Repeat("e", 32), nil },
	}
	recorded, err := conductor.Put(context.Background(),
		Ask{Role: domain.RoleArchitect, Question: "what am I missing?"},
		Party{Role: domain.RoleProductManager, Conversation: "chat-1"})
	if err == nil {
		t.Fatal("Put() = nil, want the refusing voice's failure carried back")
	}
	if len(recorded.Rounds) != 1 || recorded.Rounds[0].Holder != "yoyo pid 777" {
		t.Fatalf("Rounds = %#v, want the round naming the process that took it", recorded.Rounds)
	}
	// The first save is the round before the provider is reached, and it already
	// names the holder — which is what a pass reading a record left by a killed
	// process actually gets.
	if len(store.history) == 0 || store.history[0].Rounds[0].Holder != "yoyo pid 777" {
		t.Errorf("the round written before the invocation does not name its holder: %#v", store.history)
	}
}

// What an exchange was asked against is recorded when it opens, and that is
// what its staleness is judged against for its whole life. A follow-up names
// the records its own round rests on, which are recorded on that round and
// attached to it, and change nothing about what the thread was opened against.
func TestWhatAnExchangeRestsOnIsFixedWhenItOpensAndEachRoundNamesItsOwn(t *testing.T) {
	t.Parallel()

	store := &recordingStore{}
	conductor := Conductor{
		Store:        store,
		Voice:        speakingVoice{answer: "what I think"},
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
		MaxRounds:    3,
		NewID:        func() (string, error) { return "exchange-" + strings.Repeat("f", 32), nil },
	}
	refers := []Reference{{What: "goal", ID: "autonomy", Revision: "rev-1"}}
	opened, err := conductor.Put(context.Background(),
		Ask{Role: domain.RoleArchitect, Question: "what am I missing?", Refers: refers},
		Party{Role: domain.RoleProductManager, Conversation: "chat-1"})
	if err != nil {
		t.Fatalf("Put() = %v", err)
	}
	if len(opened.Refers) != 1 || opened.Refers[0].Revision != "rev-1" {
		t.Fatalf("Refers = %#v, want what the asker read carried onto the record", opened.Refers)
	}

	later := []Reference{{What: "run", ID: "run-1"}}
	followed, err := conductor.Put(context.Background(),
		Ask{Exchange: opened.ID, Question: "and now?", Refers: later},
		Party{Role: domain.RoleProductManager, Conversation: "chat-1"})
	if err != nil {
		t.Fatalf("Put() follow-up = %v", err)
	}
	if len(followed.Refers) != 1 || followed.Refers[0].ID != "autonomy" {
		t.Errorf("Refers = %#v, want what the thread opened against unchanged by a follow-up", followed.Refers)
	}
	if round := followed.Rounds[1]; len(round.Refers) != 1 || round.Refers[0].ID != "run-1" || len(round.Evidence) != 1 {
		t.Errorf("round 2 = refers %#v, evidence %#v; want the follow-up's own record named and accounted for", round.Refers, round.Evidence)
	}
	if round := followed.Rounds[0]; len(round.Evidence) != 1 || round.Evidence[0].ID != "autonomy" {
		t.Errorf("round 1 evidence = %#v, want only what the opening named", round.Evidence)
	}
}

// One exchange takes its rounds one at a time. A second process asking on a
// thread somebody is carrying is refused rather than queued: what is owned is
// one thread's turn, and queueing for it would mean two processes taking turns
// writing the same round instead of one process having it.
func TestARoundIsRefusedOnAThreadAnotherProcessIsCarrying(t *testing.T) {
	t.Parallel()

	store := &recordingStore{}
	recorded := open("exchange-"+strings.Repeat("9", 32), 3)
	recorded.Rounds = []Round{answered(1, "one", "")}
	store.saved = recorded

	conductor := Conductor{
		Store:        store,
		Leases:       heldElsewhere{},
		Voice:        speakingVoice{answer: "what I think"},
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
	}
	_, err := conductor.Put(context.Background(),
		Ask{Exchange: recorded.ID, Question: "and now?"},
		Party{Role: domain.RoleProductManager, Conversation: "chat-1"})
	if err == nil || !strings.Contains(err.Error(), "one at a time") {
		t.Fatalf("Put() = %v, want the round refused while another process carries the thread", err)
	}
	if store.saves != 0 {
		t.Errorf("saves = %d, want nothing written over the process that holds it", store.saves)
	}
}

// A conductor with no leases wired takes nothing and works exactly as it did,
// which is what a caller conducting a thread on its own gets.
func TestAConductorWithNoLeasesWiredStillTakesItsRound(t *testing.T) {
	t.Parallel()

	store := &recordingStore{}
	recorded := open("exchange-"+strings.Repeat("8", 32), 3)
	recorded.Rounds = []Round{answered(1, "one", "")}
	store.saved = recorded

	conductor := Conductor{
		Store:        store,
		Voice:        speakingVoice{answer: "what I think"},
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
	}
	after, err := conductor.Put(context.Background(),
		Ask{Exchange: recorded.ID, Question: "and now?"},
		Party{Role: domain.RoleProductManager, Conversation: "chat-1"})
	if err != nil {
		t.Fatalf("Put() = %v", err)
	}
	if after.Spent() != 2 {
		t.Errorf("Spent() = %d, want the round taken", after.Spent())
	}
}

// heldElsewhere is a lease a live process already has.
type heldElsewhere struct{}

func (heldElsewhere) Hold(string) (Release, bool, error) { return nil, false, nil }

// recordingStore is the durable half of a conductor, in memory, keeping every
// state it was asked to write so the order of the writes can be read back.
type recordingStore struct {
	saved   Exchange
	history []Exchange
	saves   int
}

func (s *recordingStore) Save(recorded Exchange) error {
	if err := recorded.Validate(); err != nil {
		return err
	}
	s.saves++
	s.saved = recorded
	s.history = append(s.history, recorded)
	return nil
}

func (s *recordingStore) Load(id string) (Exchange, error) {
	if s.saved.ID != id {
		return Exchange{}, ErrNoExchange
	}
	return s.saved, nil
}

type collectedReports struct{ collected []report.Report }

func (r *collectedReports) Append(reported report.Report) error {
	r.collected = append(r.collected, reported)
	return nil
}

type refusingVoice struct{}

func (refusingVoice) Answer(context.Context, Question) (Spoken, error) {
	return Spoken{}, errors.New("there is nobody to ask")
}

type speakingVoice struct{ answer string }

func (v speakingVoice) Answer(context.Context, Question) (Spoken, error) {
	return Spoken{Answer: v.answer}, nil
}
