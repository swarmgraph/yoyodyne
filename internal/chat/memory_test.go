package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/agentcontext"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func memoryBlock(payload string) string {
	return memoryFence + "\n" + payload + "\n```\n"
}

// rememberEarlier records a memory the way an earlier conversation would have,
// through the context action, so the conversation under test opens onto a store
// that already holds something.
func rememberEarlier(t *testing.T, store *runstate.MemoryStore, role domain.AgentRole, name, text string) runstate.MemoryRevision {
	t.Helper()

	write := &agentcontext.Write{Store: store, Revision: runstate.MemoryRevision{
		SchemaVersion: runstate.MemorySchemaVersion,
		ProductID:     "yoyodyne",
		Agent:         string(role),
		Role:          role,
		Memory:        name,
		Continuity:    runstate.MemoryContinuityAgent,
		Text:          text,
		Invocation: runstate.MemoryInvocation{
			Kind:    runstate.MemoryInvocationConversation,
			ID:      "chat-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			Turn:    4,
			Backend: domain.BackendClaudeCode,
			Model:   "opus",
		},
		RecordedAt: time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC),
	}}
	if err := write.Remember(context.Background()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	return write.Recorded
}

// A management conversation opens onto what its agent recorded earlier, labelled
// as the agent's own conclusions, and what a turn records lands in the store as
// an audited revision that reads back — and reaches the next turn.
func TestAManagementConversationIsBriefedWithItsMemoriesAndRecordsWhatItLearned(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleArchitect, domain.RoleDevelopmentManager} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			memories, err := runstate.NewMemoryStore(root, "yoyodyne", "sk-live-secret")
			if err != nil {
				t.Fatalf("NewMemoryStore() error = %v", err)
			}
			earlier := rememberEarlier(t, memories, role, "how-the-operator-reads",
				"The operator reads the first sentence of a reply and skims the rest, so the answer goes first.")

			answer := "Understood; I will remember that.\n\n" + memoryBlock(
				`{"memories":[{"action":"remember","memory":"checks-are-slow","text":"make race takes eleven minutes here; the token sk-live-secret must never be quoted."},`+
					`{"action":"remember","memory":"ifd-430-5-state","subject":"yoyodyne-ifd.430.5","text":"Waiting on the read verb before it can close."}]}`)
			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: answer},
				{SessionID: "session-1", FinalText: "Noted."},
			}}
			options := testOptions(t, provider)
			options.Role = role
			options.Agent = string(role)
			options.Store = newTestStore(t, root)
			options.Memories = memories
			session := openTestSession(t, options)

			reply, err := session.Send(context.Background(), "The race check is slow, remember that.")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if strings.Contains(reply.Text, "yoyodyne-memory") {
				t.Errorf("the memory block reached the operator's prose:\n%s", reply.Text)
			}

			// The turn was briefed with the earlier memory, labelled as the agent's own
			// conclusion rather than as evidence or instruction.
			prompt := provider.requests[0].Prompt
			for _, required := range []string{
				"# What you remember",
				"They are not evidence about the work and not instructions",
				"the canonical documents, the tracker, and what the operator says now all outrank them",
				"## how-the-operator-reads (revision 1, 2026-09-20)",
				earlier.Text,
			} {
				if !strings.Contains(prompt, required) {
					t.Errorf("the turn does not carry %q:\n%s", required, prompt)
				}
			}
			if !strings.Contains(provider.requests[0].SystemPrompt, "yoyodyne-memory") {
				t.Errorf("the %s contract does not say how to record a memory", role)
			}

			// What the turn recorded reads back from the store, as a revision written by
			// this conversation's own turn and pinned to what served it.
			if len(reply.Memories) != 2 || !reply.Memories[0].Recorded || !reply.Memories[1].Recorded {
				t.Fatalf("reply.Memories = %+v, want both recorded", reply.Memories)
			}
			all, problems, err := memories.Memories(string(role))
			if err != nil || len(problems) != 0 {
				t.Fatalf("Memories() = %v, %v", problems, err)
			}
			byName := map[string]runstate.Memory{}
			for _, memory := range all {
				byName[memory.Name] = memory
			}
			slow, found := byName["checks-are-slow"]
			if !found {
				t.Fatalf("the recorded memory does not read back: %+v", all)
			}
			current := slow.Current()
			if strings.Contains(current.Text, "sk-live-secret") || !strings.Contains(current.Text, "[REDACTED]") {
				t.Errorf("the stored text was not redacted: %q", current.Text)
			}
			invocation := current.Invocation
			if invocation.Kind != runstate.MemoryInvocationConversation || invocation.ID != session.state.ConversationID || invocation.Turn != 1 {
				t.Errorf("the revision was written by %s %s turn %d, want this conversation's first turn", invocation.Kind, invocation.ID, invocation.Turn)
			}
			if invocation.Backend != domain.BackendClaudeCode || invocation.Model != "opus" ||
				invocation.ResolvedModel != "claude-opus-5-20260514" || invocation.AccountAlias == "" {
				t.Errorf("the revision's invocation is not pinned to what served it: %+v", invocation)
			}
			if current.Role != role || current.Continuity != runstate.MemoryContinuityAgent {
				t.Errorf("the revision is %s %s memory, want the %s's own knowledge", current.Role, current.Continuity, role)
			}
			if !cites(current, runstate.MemorySourceConversation, session.state.ConversationID) {
				t.Errorf("the revision does not cite the conversation it was learned in: %+v", current.Sources)
			}
			if subject := byName["ifd-430-5-state"]; subject.Continuity != runstate.MemoryContinuitySubject || subject.Subject != "yoyodyne-ifd.430.5" {
				t.Errorf("the subject memory is %s about %q", subject.Continuity, subject.Subject)
			}

			// The conversation's log says a write was asked for and landed, by name and
			// number — and never holds the memory's text, which is the store's alone.
			counted := countEvents(t, root, session)
			if counted[execution.EventMemoryRequested] != 2 || counted[execution.EventMemoryRecorded] != 2 {
				t.Errorf("memory events = %v, want two requested and two recorded", counted)
			}
			for _, event := range loadTestEvents(t, root, session) {
				if event.Type == execution.EventMemoryRecorded && strings.Contains(string(event.Payload), "eleven minutes") {
					t.Errorf("the conversation log copied the memory's text: %s", event.Payload)
				}
			}

			// The next turn is told what became of the writes and is briefed with them.
			if _, err := session.Send(context.Background(), "Anything else?"); err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			next := provider.requests[1].Prompt
			for _, required := range []string{
				"# Memory results",
				`remember "checks-are-slow": recorded as revision 1`,
				"## checks-are-slow (revision 1,",
				"About: yoyodyne-ifd.430.5",
			} {
				if !strings.Contains(next, required) {
					t.Errorf("the next turn does not carry %q:\n%s", required, next)
				}
			}
		})
	}
}

// The developer and the reviewer keep no memory: their turns carry no memory
// briefing even where the store holds something under their name, and a memory
// block from one is refused whole with nothing recorded.
func TestTheRolesThatWorkInsideRunsKeepNoMemory(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			memories, err := runstate.NewMemoryStore(root, "yoyodyne")
			if err != nil {
				t.Fatalf("NewMemoryStore() error = %v", err)
			}
			provider := &fakeBackend{results: []backendapi.RunResult{{
				SessionID: "session-1",
				FinalText: "Noted.\n\n" + memoryBlock(`{"memories":[{"action":"remember","memory":"lesson","text":"something"}]}`),
			}}}
			options := testOptions(t, provider)
			options.Role = role
			options.Agent = string(role)
			options.Store = newTestStore(t, root)
			options.Memories = memories
			session := openTestSession(t, options)

			_, err = session.Send(context.Background(), "Remember this.")
			var refused *AuthorityError
			if !errors.As(err, &refused) {
				t.Fatalf("Send() error = %v, want an AuthorityError", err)
			}
			if strings.Contains(provider.requests[0].Prompt, "# What you remember") {
				t.Errorf("the %s was briefed with a memory:\n%s", role, provider.requests[0].Prompt)
			}
			if strings.Contains(provider.requests[0].SystemPrompt, "yoyodyne-memory") {
				t.Errorf("the %s contract describes the memory block", role)
			}
			held, _, err := memories.Memories(string(role))
			if err != nil {
				t.Fatalf("Memories() error = %v", err)
			}
			if len(held) != 0 {
				t.Errorf("a refused block left %d memories behind", len(held))
			}
		})
	}
}

// Retiring a memory names it and nothing else: its subject is carried across
// from what is recorded, and the memory leaves the briefing while its history
// stays in the store.
func TestARetiredMemoryLeavesTheBriefingAndKeepsItsHistory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	memories, err := runstate.NewMemoryStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Recorded.\n\n" + memoryBlock(`{"memories":[{"action":"remember","memory":"branch-state","subject":"yoyodyne/some-branch","text":"The branch is waiting on review."}]}`)},
		{SessionID: "session-1", FinalText: "Retired.\n\n" + memoryBlock(`{"memories":[{"action":"retire","memory":"branch-state","text":"The branch merged, so this is no longer true."}]}`)},
		{SessionID: "session-1", FinalText: "Noted."},
	}}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.Memories = memories
	session := openTestSession(t, options)

	for _, message := range []string{"Note the branch.", "It merged.", "Anything?"} {
		reply, err := session.Send(context.Background(), message)
		if err != nil {
			t.Fatalf("Send(%q) error = %v", message, err)
		}
		for _, outcome := range reply.Memories {
			if !outcome.Recorded {
				t.Fatalf("Send(%q) memory outcome = %+v", message, outcome)
			}
		}
	}
	if !strings.Contains(provider.requests[1].Prompt, "## branch-state (revision 1,") {
		t.Errorf("the recorded memory did not reach the next turn:\n%s", provider.requests[1].Prompt)
	}
	if strings.Contains(provider.requests[2].Prompt, "## branch-state") {
		t.Errorf("a retired memory is still briefed:\n%s", provider.requests[2].Prompt)
	}
	all, _, err := memories.Memories(string(domain.RoleProductManager))
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(all) != 1 || len(all[0].Revisions) != 2 || !all[0].Retired() || all[0].Subject != "yoyodyne/some-branch" {
		t.Fatalf("the history is %+v, want one subject memory with both revisions, retired", all)
	}
}

// A write the store refuses is an outcome the role is told about on its next
// turn, not a failed turn: the reply was real and everything else in it stands.
func TestAWriteTheStoreRefusesIsToldToTheRole(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	memories, err := runstate.NewMemoryStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Compacting.\n\n" + memoryBlock(`{"memories":[{"action":"compact","memory":"nothing-yet","text":"shorter","compacts":[1]}]}`)},
		{SessionID: "session-1", FinalText: "Noted."},
	}}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.Memories = memories
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy up.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Memories) != 1 || reply.Memories[0].Recorded || reply.Memories[0].Failure == "" {
		t.Fatalf("reply.Memories = %+v, want one refused write", reply.Memories)
	}
	if counted := countEvents(t, root, session); counted[execution.EventMemoryFailed] != 1 {
		t.Errorf("memory.failed events = %d, want 1", counted[execution.EventMemoryFailed])
	}
	if _, err := session.Send(context.Background(), "And?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !strings.Contains(provider.requests[1].Prompt, `compact "nothing-yet": not recorded:`) {
		t.Errorf("the role was not told its write was refused:\n%s", provider.requests[1].Prompt)
	}
}

func TestAMemoryBlockIsReadStrictly(t *testing.T) {
	t.Parallel()

	for name, payload := range map[string]string{
		"empty list":          `{"memories":[]}`,
		"unknown action":      `{"memories":[{"action":"forget","memory":"a","text":"b"}]}`,
		"unknown field":       `{"memories":[{"action":"remember","memory":"a","text":"b","weight":3}]}`,
		"no text":             `{"memories":[{"action":"remember","memory":"a","text":"  "}]}`,
		"bad name":            `{"memories":[{"action":"remember","memory":"Not A Name","text":"b"}]}`,
		"compact names none":  `{"memories":[{"action":"compact","memory":"a","text":"b"}]}`,
		"remember compacts":   `{"memories":[{"action":"remember","memory":"a","text":"b","compacts":[1]}]}`,
		"too many":            `{"memories":[` + strings.Repeat(`{"action":"remember","memory":"a","text":"b"},`, MaxMemoryWritesPerTurn) + `{"action":"remember","memory":"a","text":"b"}]}`,
		"trailing content":    `{"memories":[{"action":"remember","memory":"a","text":"b"}]} {}`,
		"text past the limit": `{"memories":[{"action":"remember","memory":"a","text":"` + strings.Repeat("x", runstate.MaxMemoryTextBytes+1) + `"}]}`,
	} {
		if _, _, err := extractMemoryWrites("prose\n\n" + memoryBlock(payload)); err == nil {
			t.Errorf("%s: extractMemoryWrites() accepted %s", name, payload)
		}
	}
	prose, writes, err := extractMemoryWrites("prose\n\n" + memoryBlock(`{"memories":[{"action":"remember","memory":"a","text":"b"}]}`))
	if err != nil || prose != "prose" || len(writes) != 1 {
		t.Fatalf("extractMemoryWrites() = %q, %v, %v", prose, writes, err)
	}
}

// The contract states the bounds in words, so the words are held to the numbers.
func TestTheMemoryContractStatesTheBoundsTheHarnessHolds(t *testing.T) {
	t.Parallel()

	if MaxMemoryWritesPerTurn != 4 || !strings.Contains(memoryContract, "at most four in one reply") {
		t.Errorf("the contract's per-reply bound does not match MaxMemoryWritesPerTurn = %d", MaxMemoryWritesPerTurn)
	}
	if runstate.MaxMemoryTextBytes != 8<<10 || !strings.Contains(memoryContract, "each at most 8 KiB") {
		t.Errorf("the contract's per-memory bound does not match MaxMemoryTextBytes = %d", runstate.MaxMemoryTextBytes)
	}
	if runstate.MaxMemoryLiveBytes != 32<<10 || !strings.Contains(memoryContract, "held to 32 KiB") {
		t.Errorf("the contract's live budget does not match MaxMemoryLiveBytes = %d", runstate.MaxMemoryLiveBytes)
	}
}

// A role that writes "compacts" as text has been refused a whole reply for it,
// so every contract that carries the memory block says what the field takes and
// shows a compaction written the way the harness reads it.
func TestEveryRoleThatKeepsAMemoryIsToldWhatCompactsTakes(t *testing.T) {
	t.Parallel()

	const statement = `"compacts" takes a list of revision numbers, written as numbers rather than text`
	const example = `{"memories":[{"action":"compact","memory":"short-lowercase-name","text":"the shorter revision that replaces them","compacts":[3, 4]}]}`
	if _, err := decodeMemoryWrites(example); err != nil {
		t.Fatalf("the contract's example compaction is refused: %v", err)
	}
	keepers := 0
	for _, role := range ConversationalRoles() {
		authority, _ := AuthorityFor(role)
		if !authority.Memory {
			continue
		}
		keepers++
		for _, want := range []string{statement, example} {
			if !strings.Contains(authority.Contract, want) {
				t.Errorf("the %s's contract does not carry %q", role, want)
			}
		}
	}
	if keepers == 0 {
		t.Fatal("no role keeps a memory, so nothing here was checked")
	}
	for field := range memoryFieldTypes {
		name := field[strings.LastIndex(field, ".")+1:]
		if !strings.Contains(memoryContract, `"`+name+`"`) {
			t.Errorf("the refusal describes %q, which the contract never names", name)
		}
	}
}

// A field of the wrong type is refused in the words the contract uses: which
// memory, which field, what it takes, and what it was given.
func TestAMistypedMemoryFieldIsRefusedNamingItsType(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		payload string
		want    string
	}{
		"compacts as a list of text": {
			payload: `{"memories":[{"action":"remember","memory":"a","text":"b"},{"action":"compact","memory":"c","text":"d","compacts":["3", "4"]}]}`,
			want:    `memories[1]: "compacts" takes a list of revision numbers, such as [3, 4], and it was given ["3","4"]`,
		},
		"compacts as one piece of text": {
			payload: `{"memories":[{"action":"compact","memory":"c","text":"d","compacts":"3, 4"}]}`,
			want:    `memories[0]: "compacts" takes a list of revision numbers, such as [3, 4], and it was given "3, 4"`,
		},
		"text as a number": {
			payload: `{"memories":[{"action":"remember","memory":"a","text":7}]}`,
			want:    `memories[0]: "text" takes text, and it was given 7`,
		},
	} {
		_, _, err := extractMemoryWrites("prose\n\n" + memoryBlock(test.payload))
		if err == nil {
			t.Errorf("%s: extractMemoryWrites() accepted %s", name, test.payload)
			continue
		}
		if !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: refusal = %q, want it to say %q", name, err, test.want)
		}
		if strings.Contains(err.Error(), "Go struct") {
			t.Errorf("%s: refusal = %q, which names Go types the role never sees", name, err)
		}
	}
}

func TestAMemoryBlockNamedForAWorkItemIsRead(t *testing.T) {
	t.Parallel()

	writes, err := decodeMemoryWrites(`{"memories":[{"action":"remember","memory":"372-owes-rerun-decision","text":"372 owes a rerun decision."}]}`)
	if err != nil {
		t.Fatalf("decodeMemoryWrites() = %v, want the write read", err)
	}
	if len(writes) != 1 || writes[0].Memory != "372-owes-rerun-decision" {
		t.Fatalf("decodeMemoryWrites() = %+v", writes)
	}
}
