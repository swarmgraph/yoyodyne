package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// stoppedBackend is a provider the operator stops part way through its first
// turn. It presses the stop itself once the turn has reached it, which is the
// moment Esc means something, and then answers nothing until the turn is taken
// away from it, exactly as a provider process killed mid-answer does. Every
// turn after that is the fake provider's.
type stoppedBackend struct {
	mu      sync.Mutex
	stop    chan struct{}
	stopped bool
	// meanwhile runs as the first turn begins, before the stop: what else is
	// true while the provider is working, such as the conversation having been
	// put down to wait out a provider.
	meanwhile func()
	next      *fakeBackend
}

func (b *stoppedBackend) Run(ctx context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	b.mu.Lock()
	first := !b.stopped
	b.stopped = true
	b.mu.Unlock()
	if !first {
		return b.next.Run(ctx, request)
	}
	if b.meanwhile != nil {
		b.meanwhile()
	}
	close(b.stop)
	<-ctx.Done()
	return backendapi.RunResult{Process: execution.ProcessResult{Status: execution.ProcessCancelled}}, ctx.Err()
}

// TestStoppingATurnKeepsTheConversation is Esc while the product manager is
// answering: the turn ends where it is, the conversation records that the
// operator stopped it, and the next message is answered as though nothing had
// gone wrong — because, as far as the conversation is concerned, nothing did.
func TestStoppingATurnKeepsTheConversation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &stoppedBackend{stop: make(chan struct{}), next: &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Here is the short answer."},
	}}}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	session := openTestSession(t, options)

	var out strings.Builder
	screen := &scriptedConsole{out: &out, stop: provider.stop, steps: []scriptedStep{
		{line: "write me a long plan"},
		{line: "just the short answer, please"},
	}}
	if err := session.Converse(context.Background(), screen); err != nil {
		t.Fatalf("Converse() error = %v, want the conversation to carry on past a stopped turn", err)
	}

	transcript := out.String()
	stopped := strings.Index(transcript, "You stopped the turn")
	if stopped < 0 {
		t.Fatalf("transcript = %q, want it to say the turn was stopped", transcript)
	}
	answered := strings.Index(transcript, "Here is the short answer.")
	if answered < stopped {
		t.Fatalf("transcript = %q, want the next message answered after the stop", transcript)
	}
	if counted := countEvents(t, root, session); counted[execution.EventTurnStopped] != 1 {
		t.Fatalf("recorded %d turn.stopped event(s), want 1: %#v", counted[execution.EventTurnStopped], counted)
	}
	if payload := onlyEventPayload(t, root, session, execution.EventTurnStopped); !strings.Contains(payload, `"by":"operator"`) {
		t.Fatalf("turn.stopped = %s, want it to say the operator stopped it", payload)
	}
	// The turn that was answered is the only one the conversation counts, and
	// it is the one whose provider session the record now holds.
	if session.state.Turns != 1 || session.state.ProviderSessionID != "session-1" {
		t.Fatalf("turns = %d, session = %q; want the answered turn alone recorded", session.state.Turns, session.state.ProviderSessionID)
	}
}

// heldHold is a claim on the conversation that knows whether it is held, as the
// real one does, and refuses to put down what it does not hold.
type heldHold struct {
	mu      sync.Mutex
	held    bool
	retakes int
	misuse  error
}

func (h *heldHold) Release() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.held {
		h.misuse = errors.New("the conversation was put down by a process that did not hold it")
	}
	h.held = false
	return nil
}

func (h *heldHold) Retake(context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.held = true
	h.retakes++
	return nil
}

func (h *heldHold) Held() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.held
}

// TestATurnStoppedWhileWaitingTakesTheConversationBack is the stop pressed while
// the turn is waiting out a provider, which it does with the conversation put
// down for anybody else to use. Recording the stop is a write to the
// conversation, so the conversation is taken back before it is made.
func TestATurnStoppedWhileWaitingTakesTheConversationBack(t *testing.T) {
	t.Parallel()

	hold := &heldHold{held: true}
	provider := &stoppedBackend{stop: make(chan struct{}), next: &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Answered."},
	}}}
	provider.meanwhile = func() { hold.Release() }
	options := testOptions(t, provider)
	options.Hold = hold
	session := openTestSession(t, options)

	var out strings.Builder
	screen := &scriptedConsole{out: &out, stop: provider.stop, steps: []scriptedStep{
		{line: "write me a long plan"},
		{line: "again"},
	}}
	if err := session.Converse(context.Background(), screen); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if hold.misuse != nil {
		t.Fatal(hold.misuse)
	}
	if !strings.Contains(out.String(), "Answered.") {
		t.Fatalf("transcript = %q, want the next message answered", out.String())
	}
}
