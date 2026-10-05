package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/codex"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// Uses the shipped adapter's measurement, rather than the conversation's own
// estimate, so adapter-added text must fit too. A test may lower the bound to
// prove that it comes from the endpoint being asked.
type sizedConversationBackend struct {
	*speakingBackend
	limit     int
	beforeRun func(backendapi.RunRequest)
	runErrors map[int]error
}

func (b *sizedConversationBackend) RequestSize(request backendapi.RunRequest) (int, int) {
	size, limit := (codex.Backend{}).RequestSize(request)
	if b.limit > 0 {
		limit = b.limit
	}
	return size, limit
}

func (b *sizedConversationBackend) Run(ctx context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	if b.beforeRun != nil {
		b.beforeRun(request)
	}
	index := len(b.requests)
	result, err := b.speakingBackend.Run(ctx, request)
	if runErr := b.runErrors[index]; runErr != nil {
		return result, runErr
	}
	return result, err
}

func sizeTestHistory(t *testing.T, options Options, provider *speakingBackend) *Session {
	t.Helper()
	session := openTestSession(t, options)
	for turn := 0; turn < 10; turn++ {
		provider.results = append(provider.results, backendapi.RunResult{SessionID: "old", FinalText: fmt.Sprintf("old-answer-%d ", turn) + strings.Repeat("x", 40<<10)})
		if _, err := session.Send(context.Background(), fmt.Sprintf("old-question-%d", turn)); err != nil {
			t.Fatal(err)
		}
	}
	return session
}

func TestProviderRequestSizeShortensAnOversizedCompactionAndKeepsDurableRecords(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name               string
		retrySave, asError bool
	}{{name: "ordinary save"}, {name: "save refusal", retrySave: true}, {name: "save error", retrySave: true, asError: true}} {
		t.Run(test.name, func(t *testing.T) {
			provider := &sizedConversationBackend{speakingBackend: &speakingBackend{}}
			options := testOptions(t, provider)
			options.Role, options.Agent = domain.RoleDevelopmentManager, string(domain.RoleDevelopmentManager)
			options.Provider, options.Model = domain.BackendCodex, "gpt-6.1-sol"
			options.Briefing.Text = "Current product instructions.\n" + strings.Repeat("b", 740<<10)
			root := t.TempDir()
			memories, err := runstate.NewMemoryStore(root, options.ProductID)
			if err != nil {
				t.Fatal(err)
			}
			options.Memories = memories
			session := sizeTestHistory(t, options, provider.speakingBackend)
			outcomes, err := session.performMemoryWrites(context.Background(), []MemoryWrite{{Action: "remember", Memory: "checks", Text: "Keep independent review before integration."}})
			if err != nil || len(outcomes) != 1 || !outcomes[0].Recorded {
				t.Fatalf("memory seed: %+v, %v", outcomes, err)
			}
			memoryBefore, _, err := memories.Live(options.Agent)
			if err != nil {
				t.Fatal(err)
			}

			decisions := newTriageBudgetGate(t, runstate.TriageCaps{}, 0)
			decision := runstate.TriageDecision{Decision: runstate.TriageDecisionRescope, RunID: stoppedRun, Reason: "The change must preserve the role's current instructions.", DecidedBy: "development manager", Conversation: session.state.ConversationID, Turn: session.state.Turns}
			if _, err := decisions.RecordDecision(context.Background(), "yoyodyne-ifd.70", decision); err != nil {
				t.Fatal(err)
			}
			decisionBefore := decisions.counters(t, "yoyodyne-ifd.70")
			session.options.Triage = decisions
			docket, err := runstate.NewDocketStore(root, options.ProductID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := docket.RecordOnce(triage.Entry{SchemaVersion: triage.SchemaVersion, Key: triage.Key(triage.ClassStoppedRun, stoppedRun), Class: triage.ClassStoppedRun, ProductID: options.ProductID, WorkItemID: "yoyodyne-ifd.70", RunID: stoppedRun, RecordedAt: fixedClock{}.Now(), Blocker: "The checks need repair."}); err != nil {
				t.Fatal(err)
			}
			docketBefore, err := docket.List()
			if err != nil {
				t.Fatal(err)
			}
			eventsBefore, err := options.Store.LoadEvents(session.state.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			session.state.PendingTrackerResults = "A recorded decision is waiting to be delivered.\n"
			// Reproduce compaction plus a large current docket. The save must finish
			// before shortening, and neither the docket nor new evidence may be cut.
			session.options.SessionBudgetBytes = 1
			session.ForPass("development-manager-sweep")
			waiting := "Current docket and evidence: " + strings.Repeat("e", 90<<10)
			before := len(provider.requests)
			if test.retrySave {
				provider.results = append(provider.results, backendapi.RunResult{IsError: true, FinalText: recordedCodexSizeRefusal})
				if test.asError {
					provider.runErrors = map[int]error{before: errors.New(recordedCodexSizeRefusal)}
				}
			}
			saveAnswer := "Nothing to save."
			if test.retrySave {
				saveAnswer = memoryBlock(`{"memories":[{"action":"remember","memory":"recovered-memory","text":"Keep the waiting evidence intact."}]}`)
			}
			provider.results = append(provider.results, backendapi.RunResult{SessionID: "saved", FinalText: saveAnswer}, backendapi.RunResult{SessionID: "new", FinalText: "The pass carried on."})
			provider.beforeRun = func(request backendapi.RunRequest) {
				size, limit := provider.RequestSize(request)
				if size > limit-limit/20 {
					t.Fatalf("provider handed %d bytes, safe limit %d", size, limit-limit/20)
				}
			}
			reply, err := session.Send(context.Background(), waiting)
			if err != nil {
				t.Fatal(err)
			}
			wantAttempts := 2
			if test.retrySave {
				wantAttempts++
				if len(provider.requests) != before+3 {
					t.Fatalf("memory save recovery sent %d attempts, want save, retry, waiting pass", len(provider.requests)-before)
				}
				freshSave := provider.requests[before+1]
				if strings.Contains(freshSave.Prompt, waiting) {
					t.Fatal("memory save retry replayed the waiting message")
				}
				if !strings.Contains(provider.requests[before+2].Prompt, "Keep the waiting evidence intact.") {
					t.Fatal("waiting pass did not receive the memory recorded by the retry")
				}
				if freshSave.SessionID != "" || !strings.Contains(freshSave.Prompt, sessionSetAside) || !strings.HasSuffix(freshSave.Prompt, compactionSavePrompt()) {
					t.Fatal("memory save was not retried from the record with its own instructions")
				}
				if !strings.Contains(freshSave.Prompt, memoryBefore[0].Current().Text) || freshSave.SystemPrompt != provider.requests[before].SystemPrompt {
					t.Fatal("memory save retry lost saved memories or role instructions")
				}
			}
			wantMemories := 0
			if test.retrySave {
				wantMemories = 1
			}
			if len(reply.CompactionSaves) != 1 || reply.CompactionSaves[0].NothingToSave == test.retrySave || reply.CompactionSaves[0].MemoriesRecorded != wantMemories || reply.CompactionSaves[0].Failure != "" {
				t.Fatalf("save outcome: %+v", reply.CompactionSaves)
			}
			if reply.Text != "The pass carried on." || len(provider.requests) != before+wantAttempts {
				t.Fatalf("reply=%q, requests=%d", reply.Text, len(provider.requests)-before)
			}
			sent := provider.requests[len(provider.requests)-1]
			if sent.SessionID != "" {
				t.Fatal("compacted request resumed the old session")
			}
			for _, want := range []string{waiting, options.Briefing.Text, memoryBefore[0].Current().Text, "A recorded decision is waiting to be delivered.", "earlier message(s) are not carried here.", "remain in their stores", "old-answer-9"} {
				if !strings.Contains(sent.Prompt, want) {
					t.Fatalf("shortened request missing %q", want[:min(len(want), 80)])
				}
			}
			if strings.Contains(sent.Prompt, "old-answer-0") {
				t.Fatal("oldest content was retained ahead of newer content")
			}
			if sent.SystemPrompt != provider.requests[0].SystemPrompt {
				t.Fatal("role instructions changed")
			}
			eventsAfter, err := options.Store.LoadEvents(session.state.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(eventsBefore, eventsAfter[:len(eventsBefore)]) {
				t.Fatal("old durable conversation events were changed")
			}
			var guarded bool
			for i, event := range eventsAfter {
				if i > 0 && event.Sequence <= eventsAfter[i-1].Sequence {
					t.Fatal("compaction and provider events reused sequence numbers")
				}
				if event.Type == execution.EventSessionCompacted && strings.Contains(string(event.Payload), "request_size") {
					var measured struct {
						RequestBytes int `json:"request_bytes"`
						LimitBytes   int `json:"limit_bytes"`
					}
					if err := json.Unmarshal(event.Payload, &measured); err != nil {
						t.Fatal(err)
					}
					guarded = guarded || (measured.LimitBytes > 0 && measured.RequestBytes > measured.LimitBytes)
				}
			}
			if !guarded {
				t.Fatal("fixture never exceeded the endpoint's safe size")
			}
			memoryAfter, _, err := memories.Live(options.Agent)
			if err != nil || len(memoryAfter) != len(memoryBefore)+wantMemories || !reflect.DeepEqual(memoryBefore, memoryAfter[:len(memoryBefore)]) {
				t.Fatalf("saved memories changed or retry's new memory was not recorded: %v", err)
			}
			if !reflect.DeepEqual(decisionBefore, decisions.counters(t, "yoyodyne-ifd.70")) {
				t.Fatal("recorded decision changed")
			}
			docketAfter, err := docket.List()
			if err != nil || !reflect.DeepEqual(docketBefore, docketAfter) {
				t.Fatalf("docket changed: %v", err)
			}
			if session.state.ProviderSessionBytes != len(sent.Prompt)+len(reply.Text) {
				t.Fatal("session measured the unshortened request")
			}
		})
	}
}

const recordedCodexSizeRefusal = `Error: turn/start: turn/start failed: Input exceeds the maximum length of 1048576 characters. (code -32602), data: {"input_error_code":"input_too_large","max_chars":1048576,"actual_chars":1055440}`

func TestProviderRequestSizeRejectionRetriesAPassOnceWithAShorterRequest(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ rejectsAgain, asError bool }{{}, {asError: true}, {rejectsAgain: true}, {rejectsAgain: true, asError: true}} {
		t.Run(fmt.Sprint(test), func(t *testing.T) {
			rejectsAgain := test.rejectsAgain
			provider := &sizedConversationBackend{speakingBackend: &speakingBackend{}}
			session := sizeTestHistory(t, testOptions(t, provider), provider.speakingBackend)
			session.state.ProviderSessionID = ""
			session.ForPass("test-sweep")
			last := backendapi.RunResult{SessionID: "new", FinalText: "Pass completed."}
			if rejectsAgain {
				last = backendapi.RunResult{IsError: true, FinalText: recordedCodexSizeRefusal}
			}
			provider.results = append(provider.results, backendapi.RunResult{IsError: true, FinalText: recordedCodexSizeRefusal}, last)
			before := len(provider.requests)
			if test.asError {
				provider.runErrors = map[int]error{before: errors.New(recordedCodexSizeRefusal)}
			}
			reply, err := session.Send(context.Background(), "Decide the current docket.")
			if (err != nil) != rejectsAgain {
				t.Fatalf("Send error=%v, repeated rejection=%v", err, rejectsAgain)
			}
			if len(provider.requests) != before+2 {
				t.Fatalf("sent %d attempts, want exactly one retry", len(provider.requests)-before)
			}
			full, shorter := provider.requests[before], provider.requests[before+1]
			if len(shorter.Prompt) >= len(full.Prompt) || !strings.HasSuffix(shorter.Prompt, "Decide the current docket.") || shorter.SystemPrompt != full.SystemPrompt {
				t.Fatal("retry did not shorten only old context")
			}
			if !rejectsAgain && (reply.Text != "Pass completed." || session.state.Turns != 11) {
				t.Fatal("retried pass was not recorded as completed")
			}
			if shorter.LastSequence <= full.LastSequence {
				t.Fatal("retry reused the refused attempt's event sequence")
			}
		})
	}
}

func TestProviderRequestSizeProtectsTheSelectedAlternate(t *testing.T) {
	t.Parallel()
	held := &speakingBackend{}
	alternate := &sizedConversationBackend{speakingBackend: &speakingBackend{}, limit: 160 << 10}
	options := crossingOptions(t, held, alternate)
	options.UsageLimits = newTestUsageLimits(t)
	session := sizeTestHistory(t, options, held)
	held.results = append(held.results, backendapi.RunResult{IsError: true, UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"}})
	alternate.results = []backendapi.RunResult{{SessionID: "alternate", FinalText: "Continuing elsewhere."}}
	alternate.beforeRun = func(request backendapi.RunRequest) {
		size, limit := alternate.RequestSize(request)
		if size > limit-limit/20 {
			t.Fatalf("alternate got %d bytes, safe limit %d", size, limit-limit/20)
		}
	}
	reply, err := session.Send(context.Background(), "Current evidence stays.")
	if err != nil || reply.Text != "Continuing elsewhere." {
		t.Fatalf("Send: %q, %v", reply.Text, err)
	}
	if len(alternate.requests) != 1 || !strings.Contains(alternate.requests[0].Prompt, "earlier message(s) are not carried here.") {
		t.Fatal("alternate was not protected before sending")
	}
}

func TestProviderRequestSizeRefusesFixedEvidenceWithoutSending(t *testing.T) {
	t.Parallel()
	provider := &sizedConversationBackend{speakingBackend: &speakingBackend{}, limit: 64 << 10}
	options := testOptions(t, provider)
	options.Briefing.Text = strings.Repeat("b", 80<<10)
	session := openTestSession(t, options)
	_, err := session.Send(context.Background(), "Keep all the current evidence.")
	var tooLarge *backendapi.RequestTooLarge
	if !errors.As(err, &tooLarge) || len(provider.requests) != 0 {
		t.Fatalf("error=%v, requests=%d", err, len(provider.requests))
	}
}

func TestProviderRequestSizeReadsTheRecordedCodexRefusalButNotAServedAnswer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		result backendapi.RunResult
		err    error
		want   bool
	}{
		{err: errors.New(recordedCodexSizeRefusal), want: true},
		{result: backendapi.RunResult{IsError: true, FinalText: recordedCodexSizeRefusal}, want: true},
		{result: backendapi.RunResult{FinalText: "I fixed input_too_large by shortening the conversation."}},
	} {
		if got := requestRejectedForSize(test.result, test.err); got != test.want {
			t.Fatalf("size refusal=%v, want %v", got, test.want)
		}
	}
}

// Refuse reads only after the provider's refusal has been recorded, so replacing
// the session succeeds but the reconstruction of that replacement does not.
type requestSizeUnreadableStore struct {
	Store
	readError error
}

func (s *requestSizeUnreadableStore) LoadEvents(id string) ([]execution.Event, error) {
	if s.readError != nil {
		return nil, s.readError
	}
	return s.Store.LoadEvents(id)
}

func TestProviderRequestSizeReplacementReadFailureKeepsEventNumbers(t *testing.T) {
	t.Parallel()
	provider := &sizedConversationBackend{speakingBackend: &speakingBackend{}}
	options := testOptions(t, provider)
	store := &requestSizeUnreadableStore{Store: options.Store}
	options.Store = store
	session := sizeTestHistory(t, options, provider.speakingBackend)
	before := len(provider.requests)
	readError := errors.New("conversation log cannot be read")
	provider.beforeRun = func(backendapi.RunRequest) { store.readError = readError }
	provider.results = append(provider.results, backendapi.RunResult{IsError: true, FinalText: recordedCodexSizeRefusal})
	_, err := session.Send(context.Background(), "Decide the current docket.")
	if !errors.Is(err, readError) {
		t.Errorf("Send error=%v, want the reconstruction's read failure", err)
	}
	if len(provider.requests) != before+1 {
		t.Fatal("a failed reconstruction was sent to the provider")
	}
	store.readError = nil
	events, err := store.LoadEvents(session.state.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	var replaced uint64
	for _, event := range events {
		if event.Type == execution.EventSessionReplaced {
			replaced = event.Sequence
		}
	}
	if replaced == 0 {
		t.Fatal("session replacement was not recorded")
	}
	recorded, err := store.Load(options.identity())
	if err != nil {
		t.Fatal(err)
	}
	if recorded.LastSequence < replaced || session.state.LastSequence < replaced {
		t.Errorf("record position=%d, session position=%d, replacement=%d", recorded.LastSequence, session.state.LastSequence, replaced)
	}
	provider.beforeRun = nil
	provider.results = append(provider.results, backendapi.RunResult{SessionID: "new", FinalText: "The pass continued."})
	if _, err := session.Send(context.Background(), "Continue the waiting decision."); err != nil {
		t.Fatal(err)
	}
	next := provider.requests[len(provider.requests)-1]
	if next.SessionID != "" || next.LastSequence <= replaced {
		t.Errorf("next request session=%q, event position=%d, replacement=%d", next.SessionID, next.LastSequence, replaced)
	}
	after, err := store.LoadEvents(session.state.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, after[:len(events)]) {
		t.Fatal("replacement history changed")
	}
	for i := 1; i < len(after); i++ {
		if after[i].Sequence <= after[i-1].Sequence {
			t.Fatal("the next turn reused recorded event numbers")
		}
	}
}

func TestProviderRequestSizeMemorySaveRetriesOnlyOnce(t *testing.T) {
	t.Parallel()
	provider := &sizedConversationBackend{speakingBackend: &speakingBackend{}}
	options := testOptions(t, provider)
	memories, err := runstate.NewMemoryStore(t.TempDir(), options.ProductID)
	if err != nil {
		t.Fatal(err)
	}
	options.Memories = memories
	session := sizeTestHistory(t, options, provider.speakingBackend)
	session.options.SessionBudgetBytes = 1
	session.ForPass("test-sweep")
	before := len(provider.requests)
	refusal := backendapi.RunResult{IsError: true, FinalText: recordedCodexSizeRefusal}
	provider.results = append(provider.results, refusal, refusal)
	reply, err := session.Send(context.Background(), "Decide the waiting docket.")
	if err == nil || len(provider.requests) != before+2 {
		t.Fatalf("Send error=%v, attempts=%d, want a failed save with exactly one retry", err, len(provider.requests)-before)
	}
	if len(reply.CompactionSaves) != 1 || reply.CompactionSaves[0].Failure == "" {
		t.Fatalf("save failure not recorded: %+v", reply.CompactionSaves)
	}
	if session.state.ProviderSessionID != "" || session.state.SessionSetAside == "" {
		t.Fatal("the next pass would resume the oversized session again")
	}
	// No third save or waiting turn was sent; a later pass can rebuild instead
	// of encountering the same rejected native session.
	provider.results = append(provider.results, backendapi.RunResult{SessionID: "new", FinalText: "Pass completed."})
	if _, err := session.Send(context.Background(), "Continue the waiting docket."); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != before+3 || provider.requests[before+2].SessionID != "" {
		t.Fatal("the next pass did not rebuild from the record")
	}
}
