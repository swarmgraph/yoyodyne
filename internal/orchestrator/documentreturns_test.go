package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// stoppedDocument is a conversation whose document stopped after three returned
// runs and whose next revision is held. Each message opens the conversation
// afresh, as a later process does.
type stoppedDocument struct {
	store    *runstate.ConversationStore
	provider *orchestratortest.Backend
	runs     *runstate.Store
	// quiet makes the architect's next replies carry no document.
	quiet bool
	send  func()
}

func stopDocument(t *testing.T) *stoppedDocument {
	t.Helper()
	_, provider, pipeline, runs, _ := contestedFixture(t)
	stopped := &stoppedDocument{provider: provider, runs: runs}
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			if stopped.quiet {
				return backend.RunResult{SessionID: "author", FinalText: "Read."}, nil
			}
			turn := len(provider.RequestsForRole(domain.RoleArchitect))
			return backend.RunResult{SessionID: "author", FinalText: fmt.Sprintf("Revised.\n%s\n{\"documents\":[{\"action\":\"revise\",\"id\":%q,\"body\":\"# Revision %d\",\"reason\":\"revised by the architect\"}]}\n```", artifact.WriteFence, contestedDesign, turn)}, nil
		}
		return backend.RunResult{SessionID: "independent", FinalText: `{"decision":"repair","summary":"revise the document","findings":[{"severity":"blocker","message":"state the missing decision"}]}`}, nil
	}
	options, store := publicationConversation(t, pipeline, provider)
	stopped.store = store
	stopped.send = func() {
		t.Helper()
		session, err := chat.Open(options)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.Send(context.Background(), "Revise it."); err != nil {
			t.Fatal(err)
		}
		if len(session.Writes()) != 0 {
			t.Fatalf("a held document was put to the operator: %+v", session.Writes())
		}
	}
	for i := 0; i <= runstate.MaxDocumentReturns; i++ {
		stopped.send()
	}
	stopped.quiet = true
	stopped.send()
	return stopped
}

func (s *stoppedDocument) runCount(t *testing.T) int { return len(documentRuns(t, s.runs)) }

// held is the one revision on the development manager's list.
func (s *stoppedDocument) held(t *testing.T) readmodel.DocumentReturnsWait {
	t.Helper()
	recorded, err := s.store.Recorded()
	if err != nil {
		t.Fatal(err)
	}
	held := readmodel.HeldDocuments(recorded)
	if len(held) != 1 {
		t.Fatalf("held = %+v, want one document; record %+v", held, conversationRecord(t, s.store).PendingWrites)
	}
	return held[0]
}

func (s *stoppedDocument) decide(t *testing.T, decision, reason string) {
	t.Helper()
	conversation, write, _ := strings.Cut(s.held(t).Reference, "/")
	if _, err := s.store.DecideReturnedDocument(context.Background(), conversation, write, returnsDecision(decision, reason)); err != nil {
		t.Fatal(err)
	}
}

func returnsDecision(decision, reason string) runstate.DocumentReturnsDecision {
	return runstate.DocumentReturnsDecision{Decision: decision, Reason: reason, DecidedBy: "the development manager in conversation chat-dm at turn 4", DecidedAt: time.Now().UTC()}
}

// The revision written after three returned runs is held, and listed for the
// development manager with the document, its writer, why it is held, what the
// last run found, and its text. It starts no run however many messages pass.
func TestARevisionAfterThreeReturnedRunsIsHeldForTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	stopped := stopDocument(t)
	stopped.send()
	if got := stopped.runCount(t); got != runstate.MaxDocumentReturns {
		t.Fatalf("runs = %d, want the held revision to start none", got)
	}
	held := stopped.held(t)
	rendered := readmodel.RenderHeldDocuments([]readmodel.DocumentReturnsWait{held})
	for _, want := range []string{contestedDesign, held.Reference, "the architect's", "stopped after 3 returned runs", "same copy", "state the missing decision", "# Revision 4"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("her list does not say %q:\n%s", want, rendered)
		}
	}
	saved := conversationRecord(t, stopped.store)
	if saved.PendingWrites[0].AwaitsDecision() {
		t.Error("the held document reads as waiting for the operator")
	}
	if told := toldArchitect(stopped.provider, saved); !strings.Contains(told, "held for the development manager, who releases it") {
		t.Errorf("the writer was not told: %s", told)
	}
}

// A release starts one run of the held text and the writer is told why. That
// run handed back, the next revision is held again and starts none.
func TestAReleasedRevisionRunsOnceAndItsReturnHoldsTheNext(t *testing.T) {
	t.Parallel()

	stopped := stopDocument(t)
	stopped.decide(t, runstate.DocumentReturnsRelease, "it now states the missing decision")
	before := len(stopped.provider.RequestsForRole(domain.RoleArchitect))
	stopped.send()
	requests := stopped.provider.RequestsForRole(domain.RoleArchitect)
	if len(requests) != before+1 || !strings.Contains(requests[before].Prompt, "released by the development manager in conversation chat-dm") || !strings.Contains(requests[before].Prompt, "it now states the missing decision") {
		t.Fatalf("the writer's next turn did not carry the release")
	}
	recorded := documentRuns(t, stopped.runs)
	if len(recorded) != runstate.MaxDocumentReturns+1 {
		t.Fatalf("runs = %d, want one for the release", len(recorded))
	}
	released := 0
	for _, state := range recorded {
		if state.Document.Released != nil {
			released++
			if !strings.Contains(state.Document.Candidate.Content, "# Revision 4") {
				t.Errorf("the released run carries %q, want the held text", state.Document.Candidate.Content)
			}
			if err := state.Validate(); err != nil {
				t.Error(err)
			}
		}
	}
	if released != 1 {
		t.Fatalf("%d runs record the release, want 1", released)
	}
	if got := conversationRecord(t, stopped.store).DocumentReturns[contestedDesign]; got != runstate.MaxDocumentReturns {
		t.Fatalf("returns = %d, want the stop still applied", got)
	}

	stopped.quiet = false
	stopped.send()
	stopped.quiet = true
	stopped.send()
	if got := stopped.runCount(t); got != runstate.MaxDocumentReturns+1 {
		t.Fatalf("runs = %d, want the next revision held", got)
	}
	stopped.held(t)
}

// A decline sends nothing, gives the waiting place back, and the writer reads
// the reason at their next turn; the revision after it is held in its turn.
func TestADeclinedRevisionIsNotSentAndItsWriterIsToldWhy(t *testing.T) {
	t.Parallel()

	stopped := stopDocument(t)
	stopped.decide(t, runstate.DocumentReturnsDecline, "it still leaves the decision out")
	before := len(stopped.provider.RequestsForRole(domain.RoleArchitect))
	stopped.send()
	requests := stopped.provider.RequestsForRole(domain.RoleArchitect)
	if len(requests) != before+1 || !strings.Contains(requests[before].Prompt, "it still leaves the decision out") {
		t.Fatalf("the writer's next turn did not carry the decline")
	}
	saved := conversationRecord(t, stopped.store)
	if len(saved.PendingWrites) != 0 || len(saved.HandedBack) != 1 || !strings.Contains(saved.HandedBack[0].Reason, "leaves the decision out") {
		t.Fatalf("waiting = %+v, handed back = %+v", saved.PendingWrites, saved.HandedBack)
	}
	stopped.quiet = false
	stopped.send()
	stopped.quiet = true
	stopped.send()
	if held := stopped.held(t); !strings.Contains(held.Findings, "leaves the decision out") {
		t.Errorf("findings = %q, want the decline's reason beside the next revision", held.Findings)
	}
	if got := stopped.runCount(t); got != runstate.MaxDocumentReturns {
		t.Fatalf("runs = %d, want none started", got)
	}
}

// A decision is recorded once, on a revision that is held, and not while the
// writer's conversation is in the middle of a turn.
func TestADecisionAboutAHeldDocumentIsRefusedWhereItCannotBeRecorded(t *testing.T) {
	t.Parallel()

	stopped := stopDocument(t)
	conversation, write, _ := strings.Cut(stopped.held(t).Reference, "/")
	decide := func(ctx context.Context, conversation, write string) error {
		_, err := stopped.store.DecideReturnedDocument(ctx, conversation, write, returnsDecision(runstate.DocumentReturnsRelease, "ready"))
		return err
	}
	if err := decide(context.Background(), conversation, "document-99.1"); err == nil || !strings.Contains(err.Error(), "not waiting") {
		t.Errorf("an unknown write: %v", err)
	}
	if err := decide(context.Background(), "chat-none", write); err == nil {
		t.Error("an unknown conversation was decided")
	}
	hold, err := stopped.store.TryClaim(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := decide(short, conversation, write); !errors.Is(err, runstate.ErrConversationHeld) || !strings.Contains(err.Error(), "nothing was recorded") {
		t.Errorf("a conversation mid-turn: %v", err)
	}
	hold.Release()
	stopped.held(t)
	if err := decide(context.Background(), conversation, write); err != nil {
		t.Fatal(err)
	}
	if err := decide(context.Background(), conversation, write); err == nil || !strings.Contains(err.Error(), "already decided") {
		t.Errorf("a second decision: %v", err)
	}
}
