package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	for _, want := range []string{"This recurring turn starts afresh", "The first pass's message."} {
		if !strings.Contains(first.Prompt, want) {
			t.Errorf("the recurring turn's start lacks %q", want)
		}
	}
	// The briefing's unchanging opening ends the instructions block, where the
	// next pass's new session reads it from the provider's cache.
	if !strings.HasSuffix(first.SystemPrompt, passBriefingFraming+testBriefing) || strings.Contains(first.Prompt, testBriefing) {
		t.Fatal("the recurring turn's briefing does not end its instructions")
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
	// The briefing's opening goes with the instructions, byte for byte as the
	// first pass sent it, so the provider can serve it from its cache; memories
	// change more often and stay in the message.
	if second.SystemPrompt != first.SystemPrompt || strings.Contains(second.SystemPrompt, "The routing design goes first.") {
		t.Fatal("the second pass's instructions block differs from the first's, or carries its memories")
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
	// A later round of the pass resumes its session with the same instructions,
	// so the briefing its first round was given is not lost from it.
	if provider.requests[5].SystemPrompt != second.SystemPrompt {
		t.Fatal("the pass's later message was sent without the briefing its session started from")
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
	for _, want := range []string{"The product turns intent into software.", standingGoalsLine} {
		if !strings.Contains(sent.SystemPrompt, want) || strings.Contains(sent.Prompt, want) {
			t.Errorf("the product intent %q is not sent after the instructions alone", want)
		}
	}
	for _, want := range []string{"yoyodyne-ifd.7", contextbundle.PassHeading, "docs/designs/large-design.md", "This pass's message."} {
		if !strings.Contains(sent.Prompt, want) || strings.Contains(sent.SystemPrompt, want) {
			t.Errorf("%q is not in the message alone", want)
		}
	}
	if start := sent.SystemPrompt + sent.Prompt; strings.Contains(start, strings.Repeat("design ", 15)) || strings.Contains(start, strings.Repeat("guide ", 15)) {
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

// A long account is cut to its end on a whole character, within its bound.
func TestALongAccountIsCarriedOnAWholeCharacter(t *testing.T) {
	t.Parallel()
	session := &Session{pass: "architect-pass#1"}
	session.carryPassForward(true, strings.Repeat("é", runstate.MaxPassCarriedForwardBytes))
	carried := session.state.PassCarriedForward
	if len(carried) > runstate.MaxPassCarriedForwardBytes || !utf8.ValidString(carried) || !strings.HasSuffix(carried, "é") {
		t.Fatalf("carried %d bytes, valid %v", len(carried), utf8.ValidString(carried))
	}
}

// Two recurring turns given the same instructions and the same product intent
// send the same instructions block byte for byte, ending with the briefing's
// unchanging opening, so the second turn's new provider session reads it from
// the cache the first one wrote. Work landing between them — a work item that
// changes, a design that grows — moves only the message, and the turn records
// which part of its start first differed from the last.
func TestTwoRecurringTurnsWithTheSameIntentSendTheSameInstructionsBlock(t *testing.T) {
	t.Parallel()
	before := assembledBriefing(t, 200<<10, 100<<10, 50<<10)
	after := strings.Replace(assembledBriefing(t, 210<<10, 100<<10, 50<<10), "yoyodyne-ifd.7", "yoyodyne-ifd.8", 1)
	if before == after {
		t.Fatal("the test's two briefings are the same")
	}
	provider := &sizedConversationBackend{speakingBackend: &speakingBackend{}}
	for pass := 0; pass < 4; pass++ {
		provider.results = append(provider.results, backendapi.RunResult{SessionID: fmt.Sprintf("pass-%d", pass), FinalText: "Nothing new this pass."})
	}
	options := passOptions(t, provider)
	options.Briefing.Text = before
	session := openTestSession(t, options)
	var starts []*PassStart
	for pass, briefing := range []string{before, before, before, after} {
		session.options.Briefing.Text = briefing
		session.ForPass("architect-pass")
		reply, err := session.Send(context.Background(), "This pass's message.")
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if err := session.EndPass(); err != nil {
			t.Fatal(err)
		}
		starts = append(starts, reply.PassStart)
	}
	first := provider.requests[0]
	unchanging, _ := contextbundle.SplitUnchanging(contextbundle.PassContext(before).Text)
	if unchanging == "" || !strings.HasSuffix(first.SystemPrompt, passBriefingFraming+unchanging) {
		t.Fatal("the first pass's instructions block does not end with the briefing's unchanging opening")
	}
	for pass, request := range provider.requests {
		if request.SystemPrompt != first.SystemPrompt {
			t.Fatalf("pass %d's instructions block differs from the first pass's, so its new session cannot read it from the cache", pass)
		}
	}
	if !strings.Contains(provider.requests[3].Prompt, "yoyodyne-ifd.8") || strings.Contains(provider.requests[3].Prompt, "yoyodyne-ifd.7") {
		t.Fatal("the last pass was not sent the work as it now stands")
	}
	if starts[0].FirstChangedPart != "" || starts[0].SameAsPrevious || starts[0].UnchangingBytes != len(unchanging) {
		t.Fatalf("the first pass, with nothing to compare, recorded %+v", starts[0])
	}
	if starts[1].FirstChangedPart != "memories, previous account and message" {
		t.Fatalf("the second pass, the first carrying an account, recorded its first change as %q", starts[1].FirstChangedPart)
	}
	if !starts[2].SameAsPrevious || starts[2].FirstChangedPart != "" {
		t.Fatalf("the third pass, given what the second was, recorded %+v", starts[2])
	}
	if !strings.HasPrefix(starts[3].FirstChangedPart, "briefing, second part: ") {
		t.Fatalf("the last pass recorded its first change as %q, not in the briefing's second part", starts[3].FirstChangedPart)
	}
	if len(session.state.PassStartParts) == 0 || session.state.PassStartParts[0].Name != "instructions" {
		t.Fatalf("the record keeps %+v as what the last pass started from", session.state.PassStartParts)
	}
}

// Two consecutive recurring turns whose briefings are the same, the second
// differing only in what follows the briefing — the previous turn's account and
// the turn's own message — mark the same opening of their message for the
// provider to cache, and that opening carries the work-item list and the rest
// of the briefing's second part. So the second turn's new session reads the
// work items back from the cache the first wrote, and only the memories,
// account and message are written fresh. The turn still records the first part
// that changed, and a later round resuming the session marks nothing.
func TestTheWorkItemListIsInTheCachedPartWhenOnlyTheMessageChanges(t *testing.T) {
	t.Parallel()
	briefing := assembledBriefing(t, 200<<10, 100<<10, 50<<10)
	provider := &sizedConversationBackend{speakingBackend: &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "pass-1", FinalText: "I read the work items and will take yoyodyne-ifd.7 next."},
		{SessionID: "pass-2", FinalText: "Nothing new this pass."},
		{SessionID: "pass-2", FinalText: "The closing report."},
	}}}
	options := passOptions(t, provider)
	options.Briefing.Text = briefing
	session := openTestSession(t, options)
	var starts []*PassStart
	for pass, message := range []string{"The first pass's message.", "A different message for the second pass."} {
		session.ForPass(fmt.Sprintf("architect-pass#%d", pass+1))
		reply, err := session.Send(context.Background(), message)
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		starts = append(starts, reply.PassStart)
		if pass == 1 {
			if _, err := session.Send(context.Background(), "Give the closing report."); err != nil {
				t.Fatal(err)
			}
		}
		if err := session.EndPass(); err != nil {
			t.Fatal(err)
		}
	}
	first, second, later := provider.requests[0], provider.requests[1], provider.requests[2]
	_, secondPart := contextbundle.SplitUnchanging(contextbundle.PassContext(briefing).Text)
	if first.CachedPromptBytes != len(secondPart) || second.CachedPromptBytes != len(secondPart) {
		t.Fatalf("the passes marked %d and %d bytes of their message for the cache, not the briefing's %d-byte second part",
			first.CachedPromptBytes, second.CachedPromptBytes, len(secondPart))
	}
	cachedFirst, cachedSecond := first.Prompt[:first.CachedPromptBytes], second.Prompt[:second.CachedPromptBytes]
	if cachedFirst != cachedSecond {
		t.Fatal("the two passes' cached openings differ, so the second cannot read the first's from the cache")
	}
	for _, workItem := range []string{"yoyodyne-ifd.7", "Current work stays in the briefing"} {
		if !strings.Contains(cachedSecond, workItem) {
			t.Fatalf("the work-item list (%q) is not inside the part of the message marked for the cache", workItem)
		}
	}
	freshSecond := second.Prompt[second.CachedPromptBytes:]
	for _, changed := range []string{"A different message for the second pass.", "will take yoyodyne-ifd.7 next"} {
		if strings.Contains(cachedSecond, changed) || !strings.Contains(freshSecond, changed) {
			t.Fatalf("%q is not in the part of the second pass's message written fresh", changed)
		}
	}
	if first.Prompt[first.CachedPromptBytes:] == freshSecond {
		t.Fatal("the test's two passes sent the same message, so it shows nothing about what changed")
	}
	if starts[1].FirstChangedPart != "memories, previous account and message" || starts[1].MessageCachedBytes != len(secondPart) {
		t.Fatalf("the second pass recorded %+v", starts[1])
	}
	if later.SessionID != "pass-2" || later.CachedPromptBytes != 0 {
		t.Fatalf("a later round resuming %q marked %d bytes of its own message for the cache", later.SessionID, later.CachedPromptBytes)
	}
}

// instructionsKeepingBackend is an adapter like Codex's, which puts the
// instructions inside the message, so its session keeps them.
type instructionsKeepingBackend struct{ *speakingBackend }

func (instructionsKeepingBackend) SessionKeepsInstructions() bool { return true }

// A later round of a recurring turn resumes the session its first round began.
// Where the adapter sends instructions apart from the message, as Claude Code
// does, that session does not keep them, so the later round is sent the
// briefing's first part again with them. Where the adapter folds the
// instructions into the message, as Codex does, the session already holds the
// briefing, and sending it again would add the whole product intent to the
// session once a round, so the later round is sent the role's instructions
// alone.
func TestALaterRoundOfAPassResendsTheBriefingOnlyWhereTheSessionDoesNotKeepIt(t *testing.T) {
	t.Parallel()
	briefing := assembledBriefing(t, 20<<10, 10<<10, 5<<10)
	unchanging, _ := contextbundle.SplitUnchanging(contextbundle.PassContext(briefing).Text)
	for _, keeps := range []bool{false, true} {
		speaking := &speakingBackend{results: []backendapi.RunResult{
			{SessionID: "pass-session", FinalText: "The first round."},
			{SessionID: "pass-session", FinalText: "The closing report."},
		}}
		var provider Backend = speaking
		if keeps {
			provider = instructionsKeepingBackend{speaking}
		}
		options := passOptions(t, provider)
		options.Briefing.Text = briefing
		session := openTestSession(t, options)
		session.ForPass("architect-pass#1")
		ctx := context.Background()
		if _, err := session.Send(ctx, "The pass's message."); err != nil {
			t.Fatal(err)
		}
		if _, err := session.Send(ctx, "Give the closing report."); err != nil {
			t.Fatal(err)
		}
		if err := session.EndPass(); err != nil {
			t.Fatal(err)
		}
		first, later := speaking.requests[0], speaking.requests[1]
		if first.SessionID != "" || later.SessionID != "pass-session" {
			t.Fatalf("the rounds resumed %q and %q", first.SessionID, later.SessionID)
		}
		if !strings.HasSuffix(first.SystemPrompt, passBriefingFraming+unchanging) {
			t.Fatalf("keeps=%v: the round starting the session was not sent the briefing's first part", keeps)
		}
		instructions := strings.TrimSuffix(first.SystemPrompt, passBriefingFraming+unchanging)
		if keeps && later.SystemPrompt != instructions {
			t.Fatal("a later round resuming a session that keeps its instructions was sent the product intent again")
		}
		if !keeps && later.SystemPrompt != first.SystemPrompt {
			t.Fatal("a later round resuming a session that does not keep its instructions was sent without the briefing it started from")
		}
	}
}
