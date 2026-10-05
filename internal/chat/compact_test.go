package chat

// A provider session compacted before it outgrows the request it is sent in.
//
// These read what actually reached the provider, because a compaction is a turn
// sent without the session it would have resumed, and the only way to know that
// happened is to look at the request.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type compactionBackend struct {
	*speakingBackend
	beforeRun func(backendapi.RunRequest)
}

func (b *compactionBackend) Run(ctx context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	if b.beforeRun != nil {
		b.beforeRun(request)
	}
	result, err := b.speakingBackend.Run(ctx, request)
	if err == nil && request.ReplySink != nil {
		request.ReplySink(result.FinalText)
	}
	return result, err
}

func TestACompactionSaveIsNotPartOfTheStreamedAnswer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		save   backendapi.RunResult
		writes int
	}{
		{name: "nothing to save", save: backendapi.RunResult{SessionID: "old", FinalText: "Nothing to save."}},
		{
			name: "recorded memory",
			save: backendapi.RunResult{SessionID: "old", FinalText: "I saved a conclusion.\n\n" +
				memoryBlock(`{"memories":[{"action":"remember","memory":"slow-checks","text":"The race check needs eleven minutes."}]}`)},
			writes: 1,
		},
		{name: "failed save", save: backendapi.RunResult{IsError: true, FinalText: "I was saving a conclusion when the provider failed."}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			memories, err := runstate.NewMemoryStore(root, "yoyodyne")
			if err != nil {
				t.Fatal(err)
			}
			provider := &compactionBackend{speakingBackend: &speakingBackend{results: []backendapi.RunResult{
				{SessionID: "old", FinalText: "First answer."},
				test.save,
				{SessionID: "new", FinalText: "Here is the waiting answer.\n\nThe checks come first."},
			}}}
			options := compactingOptions(t, root, provider, 1)
			options.Memories = memories
			session := openTestSession(t, options)
			if _, err := session.Send(context.Background(), "First message."); err != nil {
				t.Fatal(err)
			}
			var displayed bytes.Buffer
			session.stream = newReplyStream(&displayed, dressedTheme())
			reply, err := session.Send(context.Background(), "Waiting message.")
			if (err != nil) != test.save.IsError {
				t.Fatalf("Send() error = %v, want failed save %v", err, test.save.IsError)
			}
			session.stream.end()
			want := ""
			if reply.Text != "" {
				want = replyOpening + reply.Text + "\n\n"
			}
			if got := escapes.ReplaceAllString(displayed.String(), ""); got != want {
				t.Fatalf("displayed answer = %q, want only the returned answer %q", got, want)
			}
			if len(reply.Saved) != test.writes || len(reply.CompactionSaves) != 1 {
				t.Fatalf("save outcome missing: saved=%+v, turns=%+v", reply.Saved, reply.CompactionSaves)
			}
			var reported bytes.Buffer
			session.reportCompactionSaves(&reported, reply)
			if !strings.HasPrefix(reported.String(), "[session] ") {
				t.Fatalf("the save outcome was not reported separately: %q", reported.String())
			}
		})
	}
}

func TestEveryMemoryKeepingRoleSavesBeforeTheSessionIsRebuilt(t *testing.T) {
	t.Parallel()
	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleArchitect, domain.RoleDevelopmentManager, domain.RoleProgramManager} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			memories, err := runstate.NewMemoryStore(root, "yoyodyne")
			if err != nil {
				t.Fatal(err)
			}
			answer := "Saved what I learned.\n" + memoryBlock(`{"memories":[{"action":"remember","memory":"slow-checks","text":"The race check needs eleven minutes."}]}`)
			provider := &compactionBackend{speakingBackend: &speakingBackend{results: []backendapi.RunResult{
				{SessionID: "old-session", FinalText: strings.Repeat("earlier learning\n", 2048)},
				{SessionID: "old-session", FinalText: answer},
				{SessionID: "new-session", FinalText: "Here is the answer."},
			}}}
			options := compactingOptions(t, root, provider, 32<<10)
			options.Role, options.Agent, options.Memories = role, string(role), memories
			session := openTestSession(t, options)
			if _, err := session.Send(context.Background(), "Learn about the checks."); err != nil {
				t.Fatal(err)
			}
			if session.state.ProviderSessionBytes <= options.SessionBudgetBytes {
				t.Fatal("the first turn did not drive the session past its budget")
			}
			provider.beforeRun = func(request backendapi.RunRequest) {
				if len(provider.requests) != 2 {
					return
				}
				live, problems, err := memories.Live(string(role))
				if err != nil || len(problems) != 0 || len(live) != 1 {
					t.Fatalf("the rebuild started before the memory was stored: %v, %v, %v", live, problems, err)
				}
				if !strings.Contains(request.Prompt, live[0].Current().Text) {
					t.Fatal("the rebuilt turn lacks the memory the save turn wrote")
				}
				if counted := countEvents(t, root, session); counted[execution.EventSessionMemorySaved] != 1 || counted[execution.EventMemoryRecorded] != 1 {
					t.Fatalf("the rebuilt request preceded the save's record: %v", counted)
				}
			}
			reply, err := session.Send(context.Background(), "What should we do next?")
			if err != nil {
				t.Fatal(err)
			}
			if len(provider.requests) != 3 || provider.requests[1].SessionID != "old-session" || provider.requests[2].SessionID != "" {
				t.Fatalf("requests = %+v, want a save on the old session before a new session", provider.requests)
			}
			for _, want := range []string{"compact this provider session next", "newest 80 messages", "256 KiB", "yoyodyne-memory"} {
				if !strings.Contains(provider.requests[1].Prompt, want) {
					t.Errorf("the save prompt lacks %q", want)
				}
			}
			if strings.Contains(provider.requests[1].Prompt, "What should we do next?") {
				t.Fatal("the waiting message was sent to the save turn")
			}
			if len(reply.CompactionSaves) != 1 || reply.CompactionSaves[0].MemoriesRecorded != 1 || reply.CompactionSaves[0].NothingToSave {
				t.Fatalf("save turn's outcome = %+v", reply.CompactionSaves)
			}
			if len(reply.Saved) != 1 || len(reply.Memories) != 1 || reply.Memories[0].Turn != 2 || session.state.Turns != 3 {
				t.Fatalf("the pass would miss the save turn: saved=%+v, memories=%+v, turns=%d", reply.Saved, reply.Memories, session.state.Turns)
			}
			events, err := options.Store.LoadEvents(session.state.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			positions := map[execution.EventType]uint64{}
			for _, event := range events {
				positions[event.Type] = event.Sequence
			}
			if positions[execution.EventMemoryRecorded] >= positions[execution.EventSessionMemorySaved] || positions[execution.EventSessionMemorySaved] >= positions[execution.EventSessionCompacted] {
				t.Fatalf("save/rebuild order = %v", positions)
			}
			var saved CompactionSave
			if err := json.Unmarshal([]byte(onlyEventPayload(t, root, session, execution.EventSessionMemorySaved)), &saved); err != nil || saved.MemoriesRecorded != 1 || saved.Turn != 2 {
				t.Fatalf("the save turn was not recorded: %+v, %v", saved, err)
			}
			if !strings.Contains(provider.requests[2].Prompt, harnessSaid) || strings.Count(provider.requests[2].Prompt, "What should we do next?") != 1 {
				t.Fatal("the rebuilt history misattributes the save turn or repeats the waiting message")
			}
		})
	}
}

func TestASaveTurnWithNothingToSaveIsRecorded(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	provider := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "old", FinalText: "Learned nothing new."},
		{SessionID: "old", FinalText: "Nothing to save."},
		{SessionID: "new", FinalText: "Continuing."},
	}}
	session := openTestSession(t, compactingOptions(t, root, provider, 1))
	if _, err := session.Send(context.Background(), "First message."); err != nil {
		t.Fatal(err)
	}
	reply, err := session.Send(context.Background(), "Next message.")
	if err != nil {
		t.Fatal(err)
	}
	var saved CompactionSave
	if err := json.Unmarshal([]byte(onlyEventPayload(t, root, session, execution.EventSessionMemorySaved)), &saved); err != nil || !saved.NothingToSave || saved.MemoriesRecorded != 0 {
		t.Fatalf("save with nothing to write = %+v, %v", saved, err)
	}
	var transcript bytes.Buffer
	session.reportCompactionSaves(&transcript, reply)
	if !strings.Contains(transcript.String(), "nothing to save") {
		t.Fatalf("the transcript does not report the empty save: %s", transcript.String())
	}
}

func TestAProviderWaitPreservesTheSaveBeforeCompaction(t *testing.T) {
	for _, test := range []struct {
		name         string
		savingMemory bool
		cancel       bool
		replace      bool
		sizeRefusal  bool
	}{
		{name: "waiting answer needs a save after another turn"},
		{name: "save waits while another turn runs", savingMemory: true},
		{name: "save waits then receives a size refusal", savingMemory: true, sizeRefusal: true},
		{name: "cancelled save preserves another turn", savingMemory: true, cancel: true},
		{name: "save preserves a replacement conversation", savingMemory: true, replace: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			store := newTestStore(t, root)
			memories, err := runstate.NewMemoryStore(root, "yoyodyne")
			if err != nil {
				t.Fatal(err)
			}
			clock := &waitingClock{now: time.Now().UTC()}
			const conclusion = "The scheduled pass found that checks take eleven minutes."
			const pending = "The scheduled pass left a result for the waiting answer."
			provider := &compactionBackend{speakingBackend: &speakingBackend{results: []backendapi.RunResult{
				{SessionID: "old-session", FinalText: "Learning."},
				refusedForCapacity(clock.now.Add(30 * time.Minute)),
				{SessionID: "session-after-pass", FinalText: strings.Repeat("earlier learning\n", 5000) +
					memoryBlock(`{"memories":[{"action":"remember","memory":"slow-checks","text":"`+conclusion+`"}]}`)},
				{SessionID: "session-after-pass", FinalText: "Nothing to save."},
				{SessionID: "new-session", FinalText: "Here is the waiting answer."},
			}}}
			if test.sizeRefusal {
				// Halving removes the older large reply while retaining the
				// newer waiting message if its event number was forgotten.
				provider.results[0].FinalText = "Learning. " + strings.Repeat("x", 120<<10)
				provider.results[2].FinalText = "The scheduled pass finished.\n" +
					memoryBlock(`{"memories":[{"action":"remember","memory":"slow-checks","text":"`+conclusion+`"}]}`)
				// The save resumes the session advanced by the intervening turn,
				// but size recovery must rebuild without the waiting message.
				provider.results = append(provider.results[:3], append([]backendapi.RunResult{
					{IsError: true, FinalText: recordedCodexSizeRefusal},
					{SessionID: "save-retry-session", FinalText: "Nothing to save."},
				}, provider.results[4:]...)...)
			}
			budget := 64 << 10
			if test.savingMemory {
				budget = 1
			}
			options := waitingOptions(compactingOptions(t, root, provider, budget), clock)
			options.Store, options.Memories = store, memories
			hold, err := store.Claim(context.Background(), options.identity())
			if err != nil {
				t.Fatal(err)
			}
			defer hold.Release()
			options.Hold = hold
			provider.beforeRun = func(request backendapi.RunRequest) {
				other, err := store.TryClaim(options.identity())
				if other != nil {
					other.Release()
				}
				if !errors.Is(err, runstate.ErrConversationHeld) {
					t.Fatalf("provider invoked without the conversation hold: %v", err)
				}
			}
			waits := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var replacementID string
			options.Sleep = func(ctx context.Context, duration time.Duration) error {
				waits++
				if waits != 1 || hold.Held() {
					t.Fatal("the refused turn did not release its hold for one wait")
				}
				otherHold, err := store.Claim(ctx, options.identity())
				if err != nil {
					t.Fatal(err)
				}
				defer otherHold.Release()
				otherOptions := options
				otherOptions.Hold = otherHold
				otherOptions.UsageLimitPause = UsageLimitPause{}
				otherOptions.SessionBudgetBytes = 1 << 20
				other := openTestSession(t, otherOptions)
				if _, err := other.Send(ctx, "Scheduled pass."); err != nil {
					t.Fatal(err)
				}
				latest, err := store.Load(options.identity())
				if err != nil {
					t.Fatal(err)
				}
				latest.PendingTrackerResults = pending
				if err := store.Save(latest); err != nil {
					t.Fatal(err)
				}
				if test.replace {
					otherOptions.Fresh = true
					replacementID = openTestSession(t, otherOptions).Evidence().ConversationID
				}
				if test.cancel {
					cancel()
					return ctx.Err()
				}
				return clock.sleep(ctx, duration)
			}
			session := openTestSession(t, options)
			if _, err := session.Send(context.Background(), "First message."); err != nil {
				t.Fatal(err)
			}
			reply, err := session.Send(ctx, "Waiting message.")
			if test.cancel || test.replace {
				if test.cancel && !errors.Is(err, context.Canceled) || test.replace && (err == nil || !strings.Contains(err.Error(), "another process started a new one")) {
					t.Fatalf("interrupted save returned %v", err)
				}
				recorded, loadErr := store.Load(options.identity())
				if loadErr != nil || len(provider.requests) != 3 || len(reply.CompactionSaves) != 1 || reply.CompactionSaves[0].Failure == "" {
					t.Fatalf("interrupted save = %+v, %v; requests=%d; recorded=%+v, %v", reply, err, len(provider.requests), recorded, loadErr)
				}
				if test.cancel && (recorded.Turns != 2 || recorded.ProviderSessionID != "session-after-pass" || recorded.PendingTrackerResults != pending) {
					t.Fatal("the cancelled save overwrote the turn taken while it waited")
				}
				if test.replace && (recorded.ConversationID != replacementID || recorded.Turns != 0 || recorded.ProviderSessionID != "") {
					t.Fatal("the interrupted save overwrote a replacement conversation")
				}
				if waits, err := store.WaitingTurns(); err != nil || len(waits) != 0 {
					t.Fatalf("interrupted wait remains recorded: %+v, %v", waits, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantRequests := 5
			if test.sizeRefusal {
				wantRequests++
			}
			if waits != 1 || len(provider.requests) != wantRequests || session.state.Turns != 4 {
				t.Fatalf("waits=%d, requests=%d, turns=%d", waits, len(provider.requests), session.state.Turns)
			}
			save, answer := provider.requests[3], provider.requests[wantRequests-1]
			if test.sizeRefusal {
				retry := provider.requests[4]
				if strings.Contains(retry.Prompt, "Waiting message.") {
					t.Fatal("the rebuilt save retry replayed the waiting message")
				}
				if retry.SessionID != "" || !strings.Contains(retry.Prompt, rebuiltContextHeader) ||
					!strings.Contains(retry.Prompt, conclusion) || !strings.HasSuffix(retry.Prompt, compactionSavePrompt()) {
					t.Fatal("save retry lost the durable reconstruction, saved memory or save instructions")
				}
				if retry.LastSequence <= save.LastSequence || answer.LastSequence <= retry.LastSequence {
					t.Fatal("save recovery reused event numbers")
				}
			}
			if save.SessionID != "session-after-pass" || !strings.Contains(save.Prompt, "compact this provider session next") ||
				strings.Contains(save.Prompt, "Waiting message.") || strings.Contains(save.Prompt, rebuiltContextHeader) {
				t.Fatalf("save did not resume the latest session before rebuilding: %+v", save)
			}
			if answer.SessionID != "" || !strings.Contains(answer.Prompt, conclusion) || !strings.Contains(answer.Prompt, pending) ||
				strings.Count(answer.Prompt, "Waiting message.") != 1 {
				t.Fatal("the rebuilt answer lost the later turn's memory or results, or repeated the waiting message")
			}
			savedSession := "session-after-pass"
			if test.sizeRefusal {
				savedSession = "save-retry-session"
			}
			if len(reply.CompactionSaves) != 1 || reply.CompactionSaves[0].Turn != 3 || reply.CompactionSaves[0].SessionID != savedSession {
				t.Fatalf("save outcome does not name the turn that answered: %+v", reply.CompactionSaves)
			}
			if waits, err := store.WaitingTurns(); err != nil || len(waits) != 0 {
				t.Fatalf("finished wait remains recorded: %+v, %v", waits, err)
			}
		})
	}
}

func TestAFailedSaveLeavesTheOldSessionAndDoesNotRebuild(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]string{
		"unreadable memory":         memoryBlock(`{"memories":[{"action":"unknown"}]}`),
		"refused write":             memoryBlock(`{"memories":[{"action":"remember","memory":"new-memory","text":"A conclusion."}]}`),
		"unacknowledged empty save": "All done.",
		"other action":              "Nothing to save.\n```yoyodyne-tracker\n{\"actions\":[{\"action\":\"survey\"}]}\n```",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			provider := &speakingBackend{results: []backendapi.RunResult{
				{SessionID: "old", FinalText: "Learning."},
				{SessionID: "old", FinalText: answer},
			}}
			session := openTestSession(t, compactingOptions(t, root, provider, 1))
			if _, err := session.Send(context.Background(), "First message."); err != nil {
				t.Fatal(err)
			}
			reply, err := session.Send(context.Background(), "Next message.")
			if !errors.Is(err, ErrCompactionFailed) || len(provider.requests) != 2 || session.state.ProviderSessionID != "old" {
				t.Fatalf("Send = %+v, %v; requests=%d, session=%q", reply, err, len(provider.requests), session.state.ProviderSessionID)
			}
			counted := countEvents(t, root, session)
			if counted[execution.EventSessionMemorySaveFailed] != 1 || counted[execution.EventSessionCompacted] != 0 || counted[execution.EventTrackerActionRequested] != 0 {
				t.Fatalf("the failed save rebuilt or acted: %v", counted)
			}
		})
	}
}

func TestRolesWithoutMemoryCompactWithoutASaveTurn(t *testing.T) {
	t.Parallel()
	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			provider := &speakingBackend{results: []backendapi.RunResult{
				{SessionID: "old", FinalText: "First answer."},
				{SessionID: "new", FinalText: "Next answer."},
			}}
			options := compactingOptions(t, root, provider, 1)
			options.Role, options.Agent = role, string(role)
			session := openTestSession(t, options)
			for _, message := range []string{"First message.", "Next message."} {
				if _, err := session.Send(context.Background(), message); err != nil {
					t.Fatal(err)
				}
			}
			if len(provider.requests) != 2 || provider.requests[1].SessionID != "" || countEvents(t, root, session)[execution.EventSessionMemorySaveRequested] != 0 {
				t.Fatal("a role without memory took a save turn")
			}
		})
	}
}

func TestASaveTurnResumesTheProviderThatHoldsTheSession(t *testing.T) {
	t.Parallel()
	held := &speakingBackend{results: []backendapi.RunResult{{
		IsError: true, StopReason: "usage_limit", UsageLimit: &backendapi.UsageLimit{Kind: "five_hour", ResetsAt: fixedClock{}.Now().Add(5 * time.Hour)},
	}}}
	crossed := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "alternate-session", FinalText: "Learning on the alternate."},
		{SessionID: "alternate-session", FinalText: "Nothing to save."},
		{SessionID: "fresh-alternate", FinalText: "Continuing."},
	}}
	options := crossingOptions(t, held, crossed)
	options.UsageLimits = newTestUsageLimits(t)
	options.FailoverAccountConfigDir = "/configured-alternate-home"
	options.SessionBudgetBytes = 1
	session := openTestSession(t, options)
	for _, message := range []string{"First message.", "Next message."} {
		if _, err := session.Send(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	if len(crossed.requests) != 3 || len(held.requests) != 1 {
		t.Fatalf("primary requests=%d, alternate=%d", len(held.requests), len(crossed.requests))
	}
	save := crossed.requests[1]
	if save.SessionID != "alternate-session" || save.Model != "second-model" || save.AccountAlias != "second-account" || save.AccountConfigDir != options.FailoverAccountConfigDir {
		t.Fatalf("the save turn lost the holding endpoint: %+v", save)
	}
	if crossed.requests[2].SessionID != "" {
		t.Fatal("the alternate resumed the old session after the save")
	}
}

func TestAProviderRefusingTheSaveDoesNotRebuildOrFailOver(t *testing.T) {
	t.Parallel()
	for name, refusal := range map[string]backendapi.RunResult{
		"too long":    {IsError: true, FinalText: "Prompt is too long"},
		"no capacity": {IsError: true, StopReason: "usage_limit", UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			held := &speakingBackend{results: []backendapi.RunResult{
				{SessionID: "old", FinalText: "Learning."}, refusal,
			}}
			crossed := &speakingBackend{}
			options := crossingOptions(t, held, crossed)
			options.SessionBudgetBytes = 1
			session := openTestSession(t, options)
			if _, err := session.Send(context.Background(), "First message."); err != nil {
				t.Fatal(err)
			}
			reply, err := session.Send(context.Background(), "Waiting message.")
			if err == nil || len(held.requests) != 2 || len(crossed.requests) != 0 || session.state.ProviderSessionID != "old" {
				t.Fatalf("Send = %+v, %v; primary=%d alternate=%d session=%q", reply, err, len(held.requests), len(crossed.requests), session.state.ProviderSessionID)
			}
			if name == "no capacity" && !errors.Is(err, ErrProviderCapacity) {
				t.Fatalf("the capacity refusal lost its retry signal: %v", err)
			}
			events, err := options.Store.LoadEvents(session.state.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(recordedMessages(events, maxRebuiltContextBytes), "Waiting message.") {
				t.Fatal("the failed save lost the waiting message from the record")
			}
			if len(reply.CompactionSaves) != 1 || reply.CompactionSaves[0].Failure == "" {
				t.Fatalf("the failed save was not reported: %+v", reply.CompactionSaves)
			}
		})
	}
}

func TestASaveTurnDoesNotConsumeTheWaitingRefreshOrResults(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	provider := &compactionBackend{speakingBackend: &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "old", FinalText: "Learning."},
		{SessionID: "old", FinalText: "Nothing to save."},
		{IsError: true, FinalText: "The waiting turn failed."},
	}}}
	options := compactingOptions(t, root, provider, 1)
	options.Ground = &fakeGround{briefing: Briefing{Text: "The updated repository picture.", GatheredAt: fixedClock{}.Now(), Commit: "new-commit"}}
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "First message."); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.carryResults("The pending tracker results."); err != nil {
		t.Fatal(err)
	}
	provider.beforeRun = func(request backendapi.RunRequest) {
		if len(provider.requests) == 2 {
			if session.carried == nil || session.refresh == nil || !strings.Contains(session.state.PendingTrackerResults, "pending tracker results") {
				t.Fatal("the save turn consumed context it never delivered")
			}
			if !strings.Contains(request.Prompt, "updated repository picture") || !strings.Contains(request.Prompt, "pending tracker results") {
				t.Fatal("the waiting turn did not receive the pending refresh and results")
			}
		}
	}
	if _, err := session.Send(context.Background(), "Waiting message."); err == nil {
		t.Fatal("the waiting turn was meant to fail")
	}
	if session.refresh == nil || !strings.Contains(session.state.PendingTrackerResults, "pending tracker results") {
		t.Fatal("the failed waiting turn lost its pending refresh or results")
	}
}

func compactingOptions(t *testing.T, root string, provider Backend, budget int) Options {
	t.Helper()

	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.SessionBudgetBytes = budget
	return options
}

// A session with room is resumed, and what each turn put into it and got back
// out of it is added to its measure, which the record and the evidence both say
// beside the budget.
func TestASessionWithRoomIsResumedAndMeasured(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Two goals, then."},
		{SessionID: "session-1", FinalText: "The second one first."},
	}}
	session := openTestSession(t, compactingOptions(t, root, provider, 0))

	for _, message := range []string{"what should we do?", "and after that?"} {
		if _, err := session.Send(context.Background(), message); err != nil {
			t.Fatalf("Send(%q) error = %v", message, err)
		}
	}
	if len(provider.requests) != 2 || provider.requests[1].SessionID != "session-1" {
		t.Fatalf("requests = %d, second resuming %q; want the session resumed", len(provider.requests), provider.requests[1].SessionID)
	}
	want := 0
	for index, request := range provider.requests {
		want += len(request.Prompt) + len(provider.results[index].FinalText)
	}
	evidence := session.Evidence()
	if evidence.SessionBytes != want || evidence.SessionBudgetBytes != SessionBudgetBytes {
		t.Fatalf("evidence says %d of %d bytes, want %d of %d", evidence.SessionBytes, evidence.SessionBudgetBytes, want, SessionBudgetBytes)
	}
	recorded, err := newTestStore(t, root).Load(session.options.identity())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.ProviderSessionBytes != want || recorded.ProviderSessionBudgetBytes != SessionBudgetBytes {
		t.Fatalf("recorded %d of %d bytes, want %d of %d", recorded.ProviderSessionBytes, recorded.ProviderSessionBudgetBytes, want, SessionBudgetBytes)
	}
	if counted := countEvents(t, root, session); counted[execution.EventSessionCompacted] != 0 {
		t.Fatalf("session.compacted events = %d, want none for a session with room", counted[execution.EventSessionCompacted])
	}
}

// The case this exists for: a turn that would take the session past its budget
// is sent without it, with the conversation rebuilt from the record in front of
// it, and the new session the provider starts is measured from nothing.
func TestATurnThatWouldPassTheBudgetCompactsTheSessionFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Two goals, then."},
		{SessionID: "session-1", FinalText: "Nothing to save."},
		{SessionID: "session-2", FinalText: "The second one first."},
	}}
	session := openTestSession(t, compactingOptions(t, root, provider, 1))

	if _, err := session.Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v on the first turn", err)
	}
	if _, err := session.Send(context.Background(), "and after that?"); err != nil {
		t.Fatalf("Send() error = %v, want the turn served on a compacted session", err)
	}

	asked := provider.requests[2]
	if asked.SessionID != "" {
		t.Fatalf("session asked for = %q, want none: the session was past its budget", asked.SessionID)
	}
	if !strings.HasPrefix(asked.Prompt, rebuiltContextHeader) || !strings.Contains(asked.Prompt, "compacted it") {
		t.Fatalf("prompt = %q, want the conversation rebuilt in front of the turn and saying it was compacted", asked.Prompt)
	}
	if !strings.Contains(asked.Prompt, "Two goals, then.") || !strings.Contains(asked.Prompt, "and after that?") {
		t.Fatalf("prompt = %q, want what was said and the message being answered", asked.Prompt)
	}
	if strings.Count(asked.Prompt, rebuiltContextHeader) != 1 {
		t.Fatalf("prompt carries the rebuild %d times, want once", strings.Count(asked.Prompt, rebuiltContextHeader))
	}

	var compacted struct {
		Reason       string `json:"reason"`
		SessionID    string `json:"session_id"`
		SessionBytes int    `json:"session_bytes"`
		BudgetBytes  int    `json:"budget_bytes"`
	}
	if err := json.Unmarshal([]byte(onlyEventPayload(t, root, session, execution.EventSessionCompacted)), &compacted); err != nil {
		t.Fatalf("decode session.compacted: %v", err)
	}
	firstTurn := len(provider.requests[0].Prompt) + len("Two goals, then.")
	if compacted.Reason != compactionOverBudget || compacted.SessionID != "session-1" ||
		compacted.SessionBytes != firstTurn+len(provider.requests[1].Prompt)+len("Nothing to save.") || compacted.BudgetBytes != 1 {
		t.Fatalf("session.compacted = %+v, want the first session, its %d bytes, and the budget", compacted, firstTurn)
	}

	evidence := session.Evidence()
	if evidence.SessionID != "session-2" || evidence.SessionBytes != len(asked.Prompt)+len("The second one first.") {
		t.Fatalf("evidence = %+v, want the new session measured from the turn that started it", evidence)
	}
}

// A compaction that cannot be made is recorded as one and the turn is not sent:
// neither on the session that would not fit nor with nothing in front of it. And
// it is not a refusal the provider made, so nothing gives it back to be retried.
func TestAFailedCompactionIsRecordedAndTheTurnIsNotSent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Two goals, then."},
	}}
	session := openTestSession(t, compactingOptions(t, root, provider, 1))
	if _, err := session.Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v on the first turn", err)
	}
	// A line the store cannot read is what makes the record unreadable, and a
	// rebuild from an unreadable record is one that cannot be made.
	corruptEventLog(t, root)

	_, err := session.Send(context.Background(), "and after that?")
	if !errors.Is(err, ErrCompactionFailed) {
		t.Fatalf("Send() error = %v, want a failed compaction", err)
	}
	if errors.Is(err, ErrProviderCapacity) || errors.Is(err, ErrProviderAway) || errors.Is(err, ErrTurnAbandoned) {
		t.Fatalf("Send() error = %v, want nothing a caller gives back to be asked again", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("the provider was asked %d times, want only the first turn", len(provider.requests))
	}
	if session.Evidence().SessionID != "session-1" {
		t.Fatalf("session = %q, want the record still naming the session nothing replaced", session.Evidence().SessionID)
	}
	// The event log cannot be read back whole, so the failure is found in its raw
	// lines: that is where a reader of a damaged log would look for it too.
	if !strings.Contains(readEventLog(t, root), string(execution.EventSessionCompactionFailed)) {
		t.Fatal("the event log holds no session.compaction_failed, want the failed compaction recorded")
	}
}

// A session recorded before the harness measured sessions is of unknown size,
// and the conversation this was built for was one of them and already past the
// provider's limit. So the next turn compacts it rather than assuming it small.
func TestAnUnmeasuredSessionIsCompactedOnItsNextTurn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := &speakingBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "Two goals, then."}}}
	options := compactingOptions(t, root, first, 0)
	if _, err := openTestSession(t, options).Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v on the first turn", err)
	}
	store := newTestStore(t, root)
	recorded, err := store.Load(options.identity())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	recorded.ProviderSessionBytes, recorded.ProviderSessionBudgetBytes = 0, 0
	recorded.AccountAlias = ""
	if err := store.Save(recorded); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	later := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Nothing to save."},
		{SessionID: "session-2", FinalText: "The second one first."},
	}}
	resumed := openTestSession(t, compactingOptions(t, root, later, 0))
	if _, err := resumed.Send(context.Background(), "and after that?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if later.requests[1].SessionID != "" {
		t.Fatalf("session asked for = %q, want none: nobody knows how large it is", later.requests[1].SessionID)
	}
	if later.requests[0].AccountAlias != options.AccountAlias {
		t.Fatal("the save turn did not name the account missing from the older record")
	}
	if payload := onlyEventPayload(t, root, resumed, execution.EventSessionCompacted); !strings.Contains(payload, `"reason":"unmeasured"`) {
		t.Fatalf("session.compacted = %s, want it to say the session was unmeasured", payload)
	}
	if resumed.Evidence().SessionBytes == 0 {
		t.Fatal("the new session is unmeasured, want it measured from the turn that started it")
	}
}

func TestAFailedSaveOfAnUnmeasuredSessionStillCompactsAfterReopening(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]string{
		"invalid memory block": memoryBlock(`{"memories":[{"action":"unknown"}]}`),
		"refused memory write": memoryBlock(`{"memories":[{"action":"compact","memory":"nothing-yet","text":"shorter","compacts":[1]}]}`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			memories, err := runstate.NewMemoryStore(root, "yoyodyne")
			if err != nil {
				t.Fatal(err)
			}
			provider := &speakingBackend{results: []backendapi.RunResult{
				{SessionID: "old", FinalText: "Earlier learning."},
				{SessionID: "old", FinalText: answer},
			}}
			options := compactingOptions(t, root, provider, SessionBudgetBytes)
			options.Memories = memories
			session := openTestSession(t, options)
			if _, err := session.Send(context.Background(), "First message."); err != nil {
				t.Fatal(err)
			}
			older, err := options.Store.Load(options.identity())
			if err != nil {
				t.Fatal(err)
			}
			older.ProviderSessionBytes, older.ProviderSessionBudgetBytes = 0, 0
			if err := options.Store.Save(older); err != nil {
				t.Fatal(err)
			}
			session = openTestSession(t, options)
			if _, err := session.Send(context.Background(), "Waiting message."); !errors.Is(err, ErrCompactionFailed) {
				t.Fatalf("Send() error = %v, want the save to fail", err)
			}
			failed, err := options.Store.Load(options.identity())
			if err != nil {
				t.Fatal(err)
			}
			if failed.ProviderSessionBytes != 0 {
				t.Errorf("the failed save changed the old session's unknown size to %d bytes", failed.ProviderSessionBytes)
			}
			if failed.ProviderSessionID != "old" {
				t.Fatalf("the failed save replaced the old session with %q", failed.ProviderSessionID)
			}
			retry := &compactionBackend{speakingBackend: &speakingBackend{results: []backendapi.RunResult{
				{SessionID: "old", FinalText: memoryBlock(`{"memories":[{"action":"remember","memory":"slow-checks","text":"The race check needs eleven minutes."}]}`)},
				{SessionID: "new", FinalText: "The waiting answer."},
			}}}
			options.Backend = retry
			reopened := openTestSession(t, options)
			retry.beforeRun = func(request backendapi.RunRequest) {
				if len(retry.requests) == 1 {
					counted := countEvents(t, root, reopened)
					if counted[execution.EventMemoryRecorded] != 1 || counted[execution.EventSessionMemorySaved] != 1 {
						t.Fatalf("the rebuild preceded the retried save's writes and record: %v", counted)
					}
				}
			}
			reply, err := reopened.Send(context.Background(), "Waiting message.")
			if err != nil {
				t.Fatal(err)
			}
			if len(retry.requests) != 2 || retry.requests[0].SessionID != "old" || retry.requests[1].SessionID != "" {
				t.Fatal("the reopened conversation did not save on the old session before rebuilding")
			}
			if !strings.Contains(retry.requests[0].Prompt, "compact this provider session next") || len(reply.CompactionSaves) != 1 || reply.CompactionSaves[0].MemoriesRecorded != 1 {
				t.Fatalf("the reopened conversation did not record the save turn: %+v", reply.CompactionSaves)
			}
			if payload := onlyEventPayload(t, root, reopened, execution.EventSessionCompacted); !strings.Contains(payload, `"reason":"unmeasured"`) {
				t.Fatalf("the rebuild forgot why the old session was compacted: %s", payload)
			}
			if reopened.Evidence().SessionID != "new" || reopened.Evidence().SessionBytes == 0 || reopened.Evidence().SessionBudgetBytes != SessionBudgetBytes {
				t.Fatalf("the fresh session was not measured against the normal budget: %+v", reopened.Evidence())
			}
		})
	}
}

func eventLogPath(t *testing.T, root string) string {
	t.Helper()

	var found string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(entry.Name(), ".events.jsonl") {
			found = path
		}
		return err
	})
	if err != nil || found == "" {
		t.Fatalf("no event log under %s (err %v)", root, err)
	}
	return found
}

func corruptEventLog(t *testing.T, root string) {
	t.Helper()

	file, err := os.OpenFile(eventLogPath(t, root), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	defer file.Close()
	if _, err := file.WriteString("not an event\n"); err != nil {
		t.Fatalf("write event log: %v", err)
	}
}

func readEventLog(t *testing.T, root string) string {
	t.Helper()

	data, err := os.ReadFile(eventLogPath(t, root))
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}
	return string(data)
}

// A compacted turn left the old session behind, so a provider that names no new
// one leaves the conversation holding none: the next turn is rebuilt rather than
// resuming the oversized session under a measure the compaction reset.
func TestACompactedTurnThatNamesNoSessionLeavesNoneToResume(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Two goals, then."},
		{SessionID: "session-1", FinalText: "Nothing to save."},
		{FinalText: "The second one first."},
		{SessionID: "session-3", FinalText: "Then the third."},
	}}
	session := openTestSession(t, compactingOptions(t, root, provider, 1))

	if _, err := session.Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v on the first turn", err)
	}
	// The answer is real, and the turn says the provider left nothing to resume.
	if reply, err := session.Send(context.Background(), "and after that?"); err == nil || reply.Text == "" {
		t.Fatalf("Send() = %q, %v on the compacted turn; want the reply and the missing session reported", reply.Text, err)
	}
	if _, err := session.Send(context.Background(), "and then?"); err != nil {
		t.Fatalf("Send() error = %v on the third turn", err)
	}
	if resumed := provider.requests[3].SessionID; resumed != "" {
		t.Fatalf("third turn resumed %q, want no session: the compacted one was left behind", resumed)
	}
	if !strings.HasPrefix(provider.requests[3].Prompt, rebuiltContextHeader) {
		t.Fatalf("third prompt = %q, want the conversation rebuilt in front of it", provider.requests[3].Prompt)
	}
}

func TestOverBudgetMemoryCompactionDoesNotStopTheWaitingReply(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	memories, err := runstate.NewMemoryStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	role := domain.RoleDevelopmentManager
	for _, name := range []string{"largest-a", "largest-b", "largest-c"} {
		rememberEarlier(t, memories, role, name, strings.Repeat("a", 8192))
	}
	target := rememberEarlier(t, memories, role, "target", strings.Repeat("b", 4096))
	rememberEarlier(t, memories, role, "retire-me", strings.Repeat("c", 4096))
	before, _, err := memories.Memories(string(role))
	if err != nil {
		t.Fatal(err)
	}
	block, err := json.Marshal(map[string]any{"memories": []map[string]any{{"action": "compact", "memory": "target", "text": strings.Repeat("d", 4097), "compacts": []int{target.Sequence}}}})
	if err != nil {
		t.Fatal(err)
	}
	provider := &compactionBackend{speakingBackend: &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "old", FinalText: "Earlier answer."},
		{SessionID: "old", FinalText: memoryBlock(string(block))},
		{SessionID: "new", FinalText: "The waiting pass carried on.\n" + memoryBlock(`{"memories":[{"action":"retire","memory":"retire-me","text":"Outdated."}]}`)},
	}}}
	options := compactingOptions(t, root, provider, 1)
	options.Role, options.Agent, options.Memories = role, string(role), memories
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "First message."); err != nil {
		t.Fatal(err)
	}
	provider.beforeRun = func(request backendapi.RunRequest) {
		if len(provider.requests) != 2 {
			return
		}
		after, _, err := memories.Memories(string(role))
		if err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(before)
		b, _ := json.Marshal(after)
		if string(a) != string(b) {
			t.Fatal("refused compaction changed the memory store")
		}
		for _, want := range []string{"not recorded", "32769", "budget is 32768", "current total is 32768", `"largest-a" (8192 bytes)`, `"largest-b" (8192 bytes)`, `"largest-c" (8192 bytes)`} {
			if !strings.Contains(request.Prompt, want) {
				t.Errorf("next turn missing %q", want)
			}
		}
	}
	reply, err := session.Send(context.Background(), "Waiting pass.")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "The waiting pass carried on." || session.Evidence().SessionID != "new" {
		t.Fatalf("waiting reply was lost: %+v", reply)
	}
	if len(reply.Memories) != 2 || reply.Memories[0].Recorded || !reply.Memories[1].Recorded {
		t.Fatalf("rest of reply not carried out: %+v", reply.Memories)
	}
	if len(reply.CompactionSaves) != 1 || reply.CompactionSaves[0].Failure != "" {
		t.Fatalf("budget refusal failed session save: %+v", reply.CompactionSaves)
	}
}
