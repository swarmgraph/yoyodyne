package chat

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// TestAMessageSentDuringATurnGoesAfterItAndNotBefore is the request: while a
// turn is still being answered the operator types and sends a message, and it
// reaches the product manager as the next message once the answer has arrived,
// never alongside the turn it was typed during. A command typed in the same
// wait is carried out at once.
func TestAMessageSentDuringATurnGoesAfterItAndNotBefore(t *testing.T) {
	t.Parallel()

	provider := &heldBackend{
		fakeBackend: fakeBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: "the first answer"},
			{SessionID: "session-1", FinalText: "the second answer"},
		}},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	session := openTestSession(t, testOptions(t, provider))

	out := &transcript{}
	screen := &waitingConsole{out: out}
	screen.prompts = []string{"the first question", "/exit"}
	screen.composes = []func(interrupt <-chan struct{}) (string, error){
		// The turn is under way and has not been answered.
		func(<-chan struct{}) (string, error) {
			<-provider.started
			return "/help", nil
		},
		func(<-chan struct{}) (string, error) {
			// The command ran while the turn was still open: nothing has been
			// answered yet, and its answer is already on screen.
			if strings.Contains(out.String(), "the first answer") || !strings.Contains(out.String(), "Commands the harness carries out for you") {
				t.Errorf("/help did not run at once; transcript:\n%s", out.String())
			}
			return "and a follow-up", nil
		},
		func(interrupt <-chan struct{}) (string, error) {
			// Sent, and still only waiting: the provider has been asked one thing.
			if got := screen.waitingNow(); len(got) != 1 || got[0] != "and a follow-up" {
				t.Errorf("waiting = %#v, want the follow-up", got)
			}
			if asked := len(provider.requests); asked != 1 {
				t.Errorf("the provider was asked %d times while the first turn ran, want 1", asked)
			}
			close(provider.release)
			<-interrupt
			return "", console.ErrInterrupted
		},
		// The follow-up's own turn: nothing more is typed during it.
		func(interrupt <-chan struct{}) (string, error) {
			<-interrupt
			return "", console.ErrInterrupted
		},
	}

	if err := session.Converse(context.Background(), screen); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("the provider was asked %d times, want 2", len(provider.requests))
	}
	if strings.Contains(provider.requests[0].Prompt, "and a follow-up") {
		t.Fatalf("the follow-up reached the first turn:\n%s", provider.requests[0].Prompt)
	}
	if !strings.Contains(provider.requests[1].Prompt, "and a follow-up") {
		t.Fatalf("the follow-up was not the next message:\n%s", provider.requests[1].Prompt)
	}
	said := out.String()
	order := []string{
		"you> the first question",
		"you> /help",
		"the first answer",
		"you> and a follow-up",
		"the second answer",
	}
	at := 0
	for _, want := range order {
		found := strings.Index(said[at:], want)
		if found < 0 {
			t.Fatalf("transcript does not have %q after what came before it:\n%s", want, said)
		}
		at += found + len(want)
	}
	// The command the operator typed during the turn is not said to the product
	// manager at all.
	for _, request := range provider.requests {
		if strings.Contains(request.Prompt, "/help") {
			t.Fatalf("a command reached the product manager:\n%s", request.Prompt)
		}
	}
}

// TestACommandThatChangesTheConversationWaitsForTheTurn checks the other kind
// of command: one that writes the conversation's own record waits with the
// messages and is carried out when the turn ends, which for /exit is ending
// the conversation without a second turn.
func TestACommandThatChangesTheConversationWaitsForTheTurn(t *testing.T) {
	t.Parallel()

	provider := &heldBackend{
		fakeBackend: fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "the only answer"}}},
		started:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	session := openTestSession(t, testOptions(t, provider))

	out := &transcript{}
	screen := &waitingConsole{out: out}
	screen.prompts = []string{"the question"}
	screen.composes = []func(interrupt <-chan struct{}) (string, error){
		func(<-chan struct{}) (string, error) {
			<-provider.started
			return "/exit", nil
		},
		func(interrupt <-chan struct{}) (string, error) {
			if got := screen.waitingNow(); len(got) != 1 || got[0] != "/exit" {
				t.Errorf("waiting = %#v, want /exit", got)
			}
			close(provider.release)
			<-interrupt
			return "", console.ErrInterrupted
		},
	}

	if err := session.Converse(context.Background(), screen); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("the provider was asked %d times, want 1", len(provider.requests))
	}
	if !strings.Contains(out.String(), "/exit waits for the answer") {
		t.Fatalf("the operator was not told /exit waits:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "the only answer") {
		t.Fatalf("the turn's answer was not shown before the conversation ended:\n%s", out.String())
	}
	if left := screen.prompts; len(left) != 0 {
		t.Fatalf("the operator was prompted after /exit: %#v", left)
	}
}

// TestAnOperatorControlTypedDuringATurnActsAtOnce is the operator's own
// control typed while an answer is still being written: /hold holds intake
// there and then, before the answer arrives, and what it writes into the
// conversation — the event in its log and the note the agent is told — is
// written once the turn has ended, numbered after the turn's own events.
func TestAnOperatorControlTypedDuringATurnActsAtOnce(t *testing.T) {
	t.Parallel()

	provider := &heldBackend{
		fakeBackend: fakeBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: "the first answer"},
			{SessionID: "session-1", FinalText: "the second answer"},
		}},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	intake := &fakeIntake{}
	options := testOptions(t, provider)
	options.Intake = intake
	session := openTestSession(t, options)

	out := &transcript{}
	screen := &waitingConsole{out: out}
	screen.prompts = []string{"the first question", "/exit"}
	screen.composes = []func(interrupt <-chan struct{}) (string, error){
		func(<-chan struct{}) (string, error) {
			<-provider.started
			return "/hold while I read the answer", nil
		},
		func(<-chan struct{}) (string, error) {
			// Held already, with the first turn still unanswered.
			if _, held, _ := intake.Held(); !held {
				t.Error("/hold typed during the turn did not hold intake at once")
			}
			if strings.Contains(out.String(), "the first answer") {
				t.Errorf("the turn had already been answered; transcript:\n%s", out.String())
			}
			return "and a follow-up", nil
		},
		func(interrupt <-chan struct{}) (string, error) {
			close(provider.release)
			<-interrupt
			return "", console.ErrInterrupted
		},
	}

	if err := session.Converse(context.Background(), screen); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	events, err := options.Store.LoadEvents(session.state.ConversationID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	firstAnswer, held := -1, -1
	var last uint64
	for index, event := range events {
		if event.Sequence <= last {
			t.Fatalf("event %d is numbered %d after %d: the log is out of order", index, event.Sequence, last)
		}
		last = event.Sequence
		switch {
		case event.Type == execution.EventAgentMessage && firstAnswer < 0:
			firstAnswer = index
		case event.Type == execution.EventIntakeHeld:
			if held >= 0 {
				t.Fatal("holding intake was recorded twice")
			}
			held = index
		}
	}
	if held < 0 {
		t.Fatal("holding intake was never recorded in the conversation")
	}
	if held < firstAnswer {
		t.Fatalf("holding intake was recorded at %d, inside the turn it was typed during (answered at %d)", held, firstAnswer)
	}
	if len(provider.requests) != 2 || !strings.Contains(provider.requests[1].Prompt, "the operator held intake") {
		t.Fatalf("the agent was not told of the hold on its next turn")
	}
}

// TestStoppingATurnFromThePromptBesideItKeepsWhatIsWaiting is Esc pressed at
// the prompt kept open while the product manager answers: the turn stops as it
// does from anywhere else, and a message sent before the stop still goes as the
// next message once the stop has been said.
func TestStoppingATurnFromThePromptBesideItKeepsWhatIsWaiting(t *testing.T) {
	t.Parallel()

	provider := &stoppedBackend{stop: make(chan struct{}), next: &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Here is the short answer."},
	}}}
	options := testOptions(t, provider)
	session := openTestSession(t, options)

	out := &transcript{}
	screen := &waitingConsole{out: out}
	screen.prompts = []string{"the first question", "/exit"}
	screen.composes = []func(interrupt <-chan struct{}) (string, error){
		func(<-chan struct{}) (string, error) {
			<-provider.stop
			return "the shorter version, please", nil
		},
		func(<-chan struct{}) (string, error) { return "", console.ErrStopTurn },
		func(interrupt <-chan struct{}) (string, error) {
			<-interrupt
			return "", console.ErrInterrupted
		},
	}

	if err := session.Converse(context.Background(), screen); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	said := out.String()
	at := 0
	for _, want := range []string{"You stopped the turn", "you> the shorter version, please", "Here is the short answer."} {
		found := strings.Index(said[at:], want)
		if found < 0 {
			t.Fatalf("transcript does not have %q after what came before it:\n%s", want, said)
		}
		at += found + len(want)
	}
	events, err := options.Store.LoadEvents(session.state.ConversationID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	stopped := false
	for _, event := range events {
		stopped = stopped || event.Type == execution.EventTurnStopped
	}
	if !stopped {
		t.Fatal("the stop was not recorded as the operator's")
	}
}

// TestARunStartedDuringATurnOutlivesTheTurn is /work typed while an answer is
// being written. The run belongs to the conversation, not to the turn it was
// typed during, so it is still going once that turn has ended — answered, or
// stopped with Esc — and only ending the conversation stops it.
func TestARunStartedDuringATurnOutlivesTheTurn(t *testing.T) {
	t.Parallel()

	for _, ending := range []string{"answered", "stopped"} {
		t.Run(ending, func(t *testing.T) {
			t.Parallel()

			work := &fakeWork{gate: make(chan struct{}), running: make(chan struct{}, 1)}
			var (
				provider Backend
				begun    <-chan struct{}
				finish   func(interrupt <-chan struct{}) (string, error)
			)
			if ending == "answered" {
				held := &heldBackend{
					fakeBackend: fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "the answer"}}},
					started:     make(chan struct{}),
					release:     make(chan struct{}),
				}
				provider, begun = held, held.started
				finish = func(interrupt <-chan struct{}) (string, error) {
					close(held.release)
					<-interrupt
					return "", console.ErrInterrupted
				}
			} else {
				stopped := &stoppedBackend{stop: make(chan struct{}), next: &fakeBackend{}}
				provider, begun = stopped, stopped.stop
				finish = func(<-chan struct{}) (string, error) { return "", console.ErrStopTurn }
			}
			options := testOptions(t, provider)
			options.Work = work
			session := openTestSession(t, options)

			out := &transcript{}
			screen := &waitingConsole{out: out}
			screen.prompts = []string{"the question", "/exit"}
			screen.composes = []func(interrupt <-chan struct{}) (string, error){
				func(<-chan struct{}) (string, error) {
					<-begun
					return "/work yoyodyne-1", nil
				},
				finish,
				func(interrupt <-chan struct{}) (string, error) {
					<-interrupt
					return "", console.ErrInterrupted
				},
			}
			screen.asked = func(index int) {
				if index != 1 {
					return
				}
				// The turn is over and its context with it; the run is not.
				<-work.running
				work.mu.Lock()
				run := work.runs[0]
				work.mu.Unlock()
				if run.Err() != nil {
					t.Errorf("the run started during the turn was cancelled when the turn was %s", ending)
				}
				if item, _, running := session.RunningWork(); !running || item != "yoyodyne-1" {
					t.Errorf("the conversation no longer has the run it started during the turn")
				}
			}

			if err := session.Converse(context.Background(), screen); err != nil {
				t.Fatalf("Converse() error = %v", err)
			}
			if started := work.startedRuns(); len(started) != 1 || started[0] != "yoyodyne-1" {
				t.Fatalf("started = %#v, want the run /work named", started)
			}
			// Ending the conversation is what stops it, as it always was.
			if !work.wasCancelled() {
				t.Fatal("the run outlived the conversation that owned it")
			}
		})
	}
}

// heldBackend is a provider whose first turn does not finish until the test
// lets it, so a test can do what an operator does while a turn is still being
// answered.
type heldBackend struct {
	fakeBackend
	started chan struct{}
	release chan struct{}
	held    bool
}

func (b *heldBackend) Run(ctx context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	if !b.held {
		b.held = true
		b.requests = append(b.requests, request)
		close(b.started)
		<-b.release
		b.requests = b.requests[:len(b.requests)-1]
	}
	return b.fakeBackend.Run(ctx, request)
}

// waitingConsole is a console that keeps messages waiting, driven by a script:
// prompts answers the ordinary prompt, and composes answers each prompt opened
// while a turn is being answered.
type waitingConsole struct {
	out      io.Writer
	prompts  []string
	composes []func(interrupt <-chan struct{}) (string, error)
	waiting  []string
	beside   bool
	// asked is called before each ordinary prompt with how many came before
	// it, so a test can look at what is true between one turn and the next.
	asked func(index int)
	count int
}

func (c *waitingConsole) Write(text []byte) (int, error) { return c.out.Write(text) }

func (c *waitingConsole) Prompt(_ context.Context, prompt string, interrupt <-chan struct{}) (string, error) {
	if c.beside {
		return c.compose(interrupt)
	}
	if c.asked != nil {
		c.asked(c.count)
	}
	c.count++
	fmt.Fprint(c.out, prompt)
	if len(c.prompts) == 0 {
		fmt.Fprintln(c.out)
		return "", io.EOF
	}
	line := c.prompts[0]
	c.prompts = c.prompts[1:]
	fmt.Fprintln(c.out, line)
	return line, nil
}

// BesideTurn is the turn being answered; every prompt until it ends is one of
// composes.
func (c *waitingConsole) BesideTurn() func() {
	c.beside = true
	return func() { c.beside = false }
}

// WatchForStop is never asked of a console that can keep a message waiting:
// the prompt beside the turn reads the stop keys there.
func (c *waitingConsole) WatchForStop() (<-chan struct{}, func()) { return nil, func() {} }

func (c *waitingConsole) compose(interrupt <-chan struct{}) (string, error) {
	if len(c.composes) == 0 {
		<-interrupt
		return "", console.ErrInterrupted
	}
	next := c.composes[0]
	c.composes = c.composes[1:]
	return next(interrupt)
}

func (c *waitingConsole) Wait(message string) { c.waiting = append(c.waiting, message) }

func (c *waitingConsole) NextWaiting() (string, bool) {
	if len(c.waiting) == 0 {
		return "", false
	}
	next := c.waiting[0]
	c.waiting = c.waiting[1:]
	return next, true
}

func (c *waitingConsole) waitingNow() []string { return append([]string(nil), c.waiting...) }

func (c *waitingConsole) Choose(ctx context.Context, prompt string, _ []string, interrupt <-chan struct{}) (string, error) {
	return c.Prompt(ctx, prompt, interrupt)
}

func (c *waitingConsole) Working(phase string) console.Activity {
	return testConsole(strings.NewReader(""), c.out).Working(phase)
}

func (c *waitingConsole) Status(string)        {}
func (c *waitingConsole) Theme() console.Theme { return console.Theme{} }
func (c *waitingConsole) Composing() string    { return "" }
func (c *waitingConsole) Close() error         { return nil }

// transcript is what a waiting console writes, read and written from both the
// goroutine answering the turn and the one prompting beside it.
type transcript struct {
	mu   sync.Mutex
	text strings.Builder
}

func (t *transcript) Write(text []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.text.Write(text)
}

func (t *transcript) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.text.String()
}
