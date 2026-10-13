package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// passOptions is an architect's conversation keeping memory, which is the role
// whose recurring pass this was built for.
func passOptions(t *testing.T, provider Backend) Options {
	t.Helper()
	options := testOptions(t, provider)
	memories, err := runstate.NewMemoryStore(t.TempDir(), options.ProductID)
	if err != nil {
		t.Fatal(err)
	}
	options.Role, options.Agent, options.Memories = domain.RoleArchitect, string(domain.RoleArchitect), memories
	return options
}

// A recurring turn starts a provider session of its own from the role's
// memories, the briefing, the previous recurring turn's account and its own
// message, and nothing else said in the conversation. Its later rounds continue
// that session, and the conversation's own session is what the operator's turns
// resume before and after it.
func TestARecurringTurnStartsASessionOfItsOwnAndTheOperatorsIsPutBack(t *testing.T) {
	t.Parallel()
	provider := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "operator-session", FinalText: "Hello."},
		{SessionID: "pass-session-1", FinalText: "Pass one: I will take the routing design first on the next pass.\n" +
			memoryBlock(`{"memories":[{"action":"remember","memory":"routing-first","text":"The routing design goes first."}]}`)},
		{SessionID: "operator-session", FinalText: "Back to you."},
		{SessionID: "operator-session", FinalText: "Still here."},
		{SessionID: "pass-session-2", FinalText: "Pass two took the routing design."},
		{SessionID: "pass-session-2", FinalText: "The closing report."},
	}}
	session := openTestSession(t, passOptions(t, provider))
	ctx := context.Background()
	if _, err := session.Send(ctx, "Something only the operator said."); err != nil {
		t.Fatal(err)
	}

	session.ForPass("architect-pass#1")
	reply, err := session.Send(ctx, "The first pass's message.")
	if err != nil {
		t.Fatal(err)
	}
	first := provider.requests[1]
	if first.SessionID != "" {
		t.Fatalf("the recurring turn resumed session %q", first.SessionID)
	}
	for _, want := range []string{testBriefing, "This recurring turn starts afresh", "The first pass's message."} {
		if !strings.Contains(first.Prompt, want) {
			t.Errorf("the recurring turn's start lacks %q", want)
		}
	}
	if strings.Contains(first.Prompt, "Something only the operator said.") || strings.Contains(first.Prompt, rebuiltContextHeader) {
		t.Fatal("the recurring turn was handed the conversation's history")
	}
	if reply.PassStart == nil || reply.PassStart.Bytes == 0 || reply.PassStart.Bytes > reply.PassStart.BoundBytes ||
		reply.PassStart.BoundBytes != backendapi.DefaultContextWindowTokens*bytesPerToken/passContextShare {
		t.Fatalf("what the turn started from = %+v", reply.PassStart)
	}
	if session.state.ProviderSessionID != "pass-session-1" || session.state.SessionAsideForPass == nil ||
		session.state.SessionAsideForPass.SessionID != "operator-session" {
		t.Fatalf("during the pass the record holds %q with %+v set aside", session.state.ProviderSessionID, session.state.SessionAsideForPass)
	}
	if err := session.EndPass(); err != nil {
		t.Fatal(err)
	}
	if session.state.ProviderSessionID != "operator-session" || session.state.SessionAsideForPass != nil {
		t.Fatalf("after the pass the record holds %q", session.state.ProviderSessionID)
	}
	stored, err := session.options.Store.Load(session.options.identity())
	if err != nil || stored.ProviderSessionID != "operator-session" || stored.SessionAsideForPass != nil {
		t.Fatalf("the put-back session was not recorded: %+v, %v", stored.ProviderSessionID, err)
	}

	// The operator's next turn resumes the conversation's session, and is told
	// once what the recurring turn it never saw said.
	session.ForPass("")
	if _, err := session.Send(ctx, "Back again."); err != nil {
		t.Fatal(err)
	}
	if provider.requests[2].SessionID != "operator-session" || !strings.Contains(provider.requests[2].Prompt, "I will take the routing design first") {
		t.Fatalf("the operator's turn resumed %q without the pass's account", provider.requests[2].SessionID)
	}
	if _, err := session.Send(ctx, "And again."); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(provider.requests[3].Prompt, "I will take the routing design first") {
		t.Fatal("the pass's account was given to the operator's session twice")
	}

	// What the first pass agreed to do next, and what it remembered, reach the
	// second pass; its later message continues the second pass's own session.
	session.ForPass("architect-pass#2")
	if _, err := session.Send(ctx, "The second pass's message."); err != nil {
		t.Fatal(err)
	}
	second := provider.requests[4]
	if second.SessionID != "" {
		t.Fatalf("the second recurring turn resumed session %q", second.SessionID)
	}
	for _, want := range []string{"What your previous recurring turn said", "architect-pass#1", "I will take the routing design first on the next pass.", "The routing design goes first."} {
		if !strings.Contains(second.Prompt, want) {
			t.Errorf("the second pass's start lacks %q", want)
		}
	}
	// The briefing goes first, so a provider caching what requests open with can
	// serve it to the next pass's session; memories change more often.
	if strings.Index(second.Prompt, testBriefing) != 0 || strings.Index(second.Prompt, "The routing design goes first.") < len(testBriefing) {
		t.Fatal("the second pass's start does not open with the briefing ahead of its memories")
	}
	if strings.Contains(second.Prompt, "Back again.") {
		t.Fatal("the second pass was handed the operator's turns")
	}
	if _, err := session.Send(ctx, "Give the closing report."); err != nil {
		t.Fatal(err)
	}
	if provider.requests[5].SessionID != "pass-session-2" {
		t.Fatalf("the pass's later message resumed %q rather than its own session", provider.requests[5].SessionID)
	}
	if !strings.Contains(session.state.PassCarriedForward, "Pass two took the routing design.") || !strings.Contains(session.state.PassCarriedForward, "The closing report.") {
		t.Fatalf("the second pass's account carried forward = %q", session.state.PassCarriedForward)
	}
}

// Many recurring turns in a row, each saying a great deal, do not grow what
// the next one starts from: the conversation grows and the start does not.
func TestManyRecurringTurnsDoNotGrowWhatEachStartsFrom(t *testing.T) {
	t.Parallel()
	const passes = 12
	long := strings.Repeat("A long account of what this pass found and decided.\n", 1<<10)
	provider := &speakingBackend{}
	for pass := 0; pass < passes; pass++ {
		provider.results = append(provider.results, backendapi.RunResult{SessionID: fmt.Sprintf("pass-%02d", pass), FinalText: long})
	}
	session := openTestSession(t, passOptions(t, provider))
	var starts []int
	for pass := 0; pass < passes; pass++ {
		session.ForPass(fmt.Sprintf("architect-pass#%02d", pass))
		reply, err := session.Send(context.Background(), "This pass's message.")
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if err := session.EndPass(); err != nil {
			t.Fatal(err)
		}
		request := provider.requests[pass]
		if request.SessionID != "" {
			t.Fatalf("pass %d resumed session %q", pass, request.SessionID)
		}
		starts = append(starts, len(request.SystemPrompt)+len(request.Prompt))
		if reply.PassStart == nil || reply.PassStart.Bytes > reply.PassStart.BoundBytes {
			t.Fatalf("pass %d started from %+v", pass, reply.PassStart)
		}
	}
	events, err := session.options.Store.LoadEvents(session.state.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	said := 0
	for _, event := range events {
		said += len(messageText(event))
	}
	if said < passes*len(long) {
		t.Fatalf("the conversation recorded %d bytes of what was said, want at least %d", said, passes*len(long))
	}
	// The first pass has no account to carry; every pass after it carries one
	// of the same bounded size.
	for pass := 2; pass < passes; pass++ {
		if starts[pass] != starts[1] {
			t.Fatalf("what each pass started from grew: %v", starts)
		}
	}
	if starts[1]-starts[0] > runstate.MaxPassCarriedForwardBytes+4<<10 {
		t.Fatalf("the carried account added %d bytes, more than its bound", starts[1]-starts[0])
	}
}

// A recurring turn starts from the product intent whole, and the role's own
// documents and the rest of the documentation are left out of it and listed
// for reading on demand, however much room the bound would leave.
func TestARecurringTurnStartsFromTheIntentWithDocumentsLeftOut(t *testing.T) {
	t.Parallel()
	briefing := assembledBriefing(t, 200<<10, 100<<10, 50<<10)
	provider := &sizedConversationBackend{speakingBackend: &speakingBackend{results: []backendapi.RunResult{{SessionID: "pass", FinalText: "Done."}}}}
	options := passOptions(t, provider)
	options.Briefing.Text = briefing
	session := openTestSession(t, options)
	session.ForPass("architect-pass#1")
	reply, err := session.Send(context.Background(), "This pass's message.")
	if err != nil {
		t.Fatal(err)
	}
	sent := provider.requests[0]
	for _, want := range []string{"The product turns intent into software.", standingGoalsLine, "yoyodyne-ifd.7", contextbundle.PassHeading, "docs/designs/large-design.md", "This pass's message."} {
		if !strings.Contains(sent.Prompt, want) {
			t.Errorf("the start lacks %q", want)
		}
	}
	if strings.Contains(sent.Prompt, strings.Repeat("design ", 15)) || strings.Contains(sent.Prompt, strings.Repeat("guide ", 15)) {
		t.Fatal("a document that is not product intent was carried in the start")
	}
	if reply.PassStart == nil || reply.PassStart.Bytes >= len(briefing) {
		t.Fatalf("what the turn started from = %+v, briefing %d bytes", reply.PassStart, len(briefing))
	}
	kinds := map[string]bool{}
	for _, section := range reply.PassStart.GaveWay {
		kinds[section.Kind] = true
	}
	if !kinds["Design"] || !kinds["Shipped documentation"] || kinds[contextbundle.IntentHeading] {
		t.Fatalf("left out = %+v", reply.PassStart.GaveWay)
	}
	if counted := storedEvents(t, session); counted[execution.EventSessionPassStarted] != 1 {
		t.Fatalf("events = %v", counted)
	}
}

// What never gives way, over the bound on its own, refuses the turn before the
// provider is asked.
func TestARecurringTurnThatCannotFitIsRefusedBeforeTheProvider(t *testing.T) {
	t.Parallel()
	provider := &speakingBackend{}
	options := passOptions(t, provider)
	options.PassContextBytes = 512
	session := openTestSession(t, options)
	session.ForPass("architect-pass#1")
	_, err := session.Send(context.Background(), "This pass's message.")
	var tooLarge *PassContextTooLarge
	if !errors.Is(err, ErrTurnUnassembled) || !errors.As(err, &tooLarge) || tooLarge.BoundBytes != 512 || tooLarge.InstructionsBytes == 0 {
		t.Fatalf("Send error = %v", err)
	}
	if len(provider.requests) != 0 {
		t.Fatal("the provider was asked anyway")
	}
	if !strings.Contains(err.Error(), "a third of the") || !strings.Contains(err.Error(), "not tried again") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
}

// A compaction the provider made during a turn is on the reply and in the
// conversation's log, with its time and sizes.
func TestAProviderCompactionDuringARecurringTurnIsRecorded(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 12, 15, 4, 5, 0, time.UTC)
	provider := &speakingBackend{results: []backendapi.RunResult{{SessionID: "pass", FinalText: "Done.", Compactions: []backendapi.ProviderCompaction{
		{At: at, Trigger: "auto", BeforeTokens: 250_000},
		{At: at.Add(time.Minute), BeforeTokens: 240_000, AfterTokens: 80_000},
	}}}}
	session := openTestSession(t, passOptions(t, provider))
	session.ForPass("architect-pass#1")
	reply, err := session.Send(context.Background(), "This pass's message.")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.ProviderCompactions) != 2 || reply.ProviderCompactions[1].AfterTokens != 80_000 {
		t.Fatalf("compactions on the reply = %+v", reply.ProviderCompactions)
	}
	if counted := storedEvents(t, session); counted[execution.EventSessionProviderCompacted] != 2 {
		t.Fatalf("events = %v", counted)
	}
}

// A process that died during a recurring turn left the conversation's session
// set aside; the next one to open the conversation puts it back.
func TestTheSessionSetAsideByAPassThatDiedIsPutBack(t *testing.T) {
	t.Parallel()
	provider := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "operator-session", FinalText: "Hello."},
		{SessionID: "pass-session", FinalText: "The pass, interrupted."},
	}}
	options := passOptions(t, provider)
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "Hello."); err != nil {
		t.Fatal(err)
	}
	session.ForPass("architect-pass#1")
	if _, err := session.Send(context.Background(), "The pass's message."); err != nil {
		t.Fatal(err)
	}
	// No EndPass: the process holding the pass is gone.
	reopened := openTestSession(t, options)
	if reopened.state.ProviderSessionID != "operator-session" || reopened.state.SessionAsideForPass != nil {
		t.Fatalf("the reopened conversation holds %q with %+v set aside", reopened.state.ProviderSessionID, reopened.state.SessionAsideForPass)
	}
}

// storedEvents counts what the session's own store recorded, by type.
func storedEvents(t *testing.T, session *Session) map[execution.EventType]int {
	t.Helper()
	events, err := session.options.Store.LoadEvents(session.state.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	counted := map[execution.EventType]int{}
	for _, event := range events {
		counted[event.Type]++
	}
	return counted
}
