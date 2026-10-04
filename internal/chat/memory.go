package chat

// A management conversation's own memory: what its agent concluded in earlier
// turns, read into every turn, and what this turn concluded, written back.
//
// The `### Agent memory` section of `docs/designs/configurable-workflows.md`
// is the whole of the rule, and two of its sentences decide the shape here.
// Agent-authored memory is written only through typed context actions the role
// contract owns, so a turn's block is carried out through the registered
// `agent-context` actions and nothing else, with the store redacting, budgeting,
// and pinning the invocation exactly as it does for a side stream's merge. And
// memory never overrides a canonical artifact, so the briefing labels what it
// carries as the agent's own earlier conclusions — not evidence about the work
// and not instruction — and says what outranks it.
//
// Only a role that holds `agent-context.mutate` is briefed or may write: the
// product manager, the architect, and the development manager. The developer and
// the reviewer keep no memory, because their judgement is exercised inside runs
// that remember nothing between invocations, and a conversation with one of them
// carries neither the block nor the briefing.
//
// The side threads' merges are memories too, and they are left to the block that
// already carries them, filtered to this conversation. What is briefed here is
// everything else the agent knows.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/action"
	"github.com/mason-bryant/yoyodyne/internal/agentcontext"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const memoryFence = "```yoyodyne-memory"

// What one reply may ask to be remembered. Each write is bounded by the store's
// own revision limit and the whole store by its live budget; these bound the
// request itself, so one reply cannot spend a turn's worth of writes in a single
// block or hand the harness a block larger than every write it could hold.
const (
	MaxMemoryWritesPerTurn = 4
	MaxMemoryBlockBytes    = MaxMemoryWritesPerTurn * (runstate.MaxMemoryTextBytes + 1<<10)
)

// What one turn is briefed with. The live budget bounds the store, and it is
// already small; this is a bound of the turn's own, for the reason the merges
// have one: memory competes for the same context as the canonical artifacts and
// the operator's message. Half the live budget is carried, newest first, and the
// rest is named rather than silently left out.
const maxBriefedMemoryBytes = runstate.MaxMemoryLiveBytes / 2

// The three operations a block may ask for, named by the context action each
// is carried out through.
const (
	memoryRemember = "remember"
	memoryCompact  = "compact"
	memoryRetire   = "retire"
)

var memoryActions = map[string]string{
	memoryRemember: "agent-context.remember",
	memoryCompact:  "agent-context.compact",
	memoryRetire:   "agent-context.retire",
}

// MemoryWrite is one thing a reply asked to be remembered, revised, or retired.
type MemoryWrite struct {
	Action string `json:"action"`
	// Memory is what the memory is called, which is how a later turn revises or
	// retires it and how an operator asks about it.
	Memory string `json:"memory"`
	// Text is what the agent concluded. For a retirement it says why the memory
	// stopped being true, because a history that goes blank tells the next reader
	// nothing.
	Text string `json:"text"`
	// Subject is what a memory about one thing is about — a work item, a
	// document, a branch — and is empty for what the agent knows generally.
	Subject string `json:"subject,omitempty"`
	// Compacts are the earlier revisions a compaction folds together.
	Compacts []int `json:"compacts,omitempty"`
}

type memoryDocument struct {
	Memories []MemoryWrite `json:"memories"`
}

// MemoryOutcome is what became of one write: the revision the store numbered it,
// or why nothing was recorded.
type MemoryOutcome struct {
	ID       string      `json:"id"`
	Turn     int         `json:"turn"`
	Write    MemoryWrite `json:"write"`
	Recorded bool        `json:"recorded"`
	Sequence int         `json:"sequence,omitempty"`
	Failure  string      `json:"failure,omitempty"`
}

// MemoryError reports a memory block the harness could not read. Nothing in it
// was recorded, and nothing else about the turn is changed by it.
type MemoryError struct {
	Err error
}

func (e *MemoryError) Error() string {
	return "the reply carried a memory block the harness cannot read: " + e.Err.Error()
}

func (e *MemoryError) Unwrap() error { return e.Err }

// extractMemoryWrites takes the memory block out of a reply, leaving its prose.
func extractMemoryWrites(reply string) (string, []MemoryWrite, error) {
	prose, payload, found, err := splitFencedBlock(reply, memoryFence, "memory")
	if err != nil {
		return "", nil, err
	}
	if !found {
		return strings.TrimSpace(reply), nil, nil
	}
	writes, err := decodeMemoryWrites(payload)
	if err != nil {
		return "", nil, err
	}
	return prose, writes, nil
}

// decodeMemoryWrites strictly decodes the block. Unknown fields, trailing
// content, and oversized input are refused: what reaches the store has to be
// exactly what the role wrote.
func decodeMemoryWrites(payload string) ([]MemoryWrite, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil, errors.New("decode memory writes: the memory block is empty")
	}
	if len(trimmed) > MaxMemoryBlockBytes {
		return nil, fmt.Errorf("decode memory writes: block is %d bytes, limit is %d", len(trimmed), MaxMemoryBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var document memoryDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode memory writes: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode memory writes: unexpected trailing content after the memories")
	}
	if len(document.Memories) == 0 {
		return nil, errors.New("decode memory writes: a memory block must record at least one memory")
	}
	if len(document.Memories) > MaxMemoryWritesPerTurn {
		return nil, fmt.Errorf("decode memory writes: %d memories in one reply, limit is %d", len(document.Memories), MaxMemoryWritesPerTurn)
	}
	var problems []error
	for i, write := range document.Memories {
		if err := write.validate(); err != nil {
			problems = append(problems, fmt.Errorf("memories[%d]: %w", i, err))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid memory writes: %w", errors.Join(problems...))
	}
	return document.Memories, nil
}

// validate is the shape of one write. What depends on the history — the next
// number, whether a compacted revision exists, whether the budget has room — is
// the store's to decide when the write is carried out, and a refusal there is an
// outcome rather than an unreadable block.
func (w MemoryWrite) validate() error {
	var problems []error
	if _, known := memoryActions[w.Action]; !known {
		problems = append(problems, fmt.Errorf("%q is not a memory action; the actions are %q, %q, and %q", w.Action, memoryRemember, memoryCompact, memoryRetire))
	}
	if err := domain.ValidateMemoryName(w.Memory); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(w.Text) == "" {
		problems = append(problems, errors.New("text is required: it is what you concluded, or why a retired memory stopped being true"))
	}
	if len(w.Text) > runstate.MaxMemoryTextBytes {
		problems = append(problems, fmt.Errorf("text is %d bytes, limit is %d", len(w.Text), runstate.MaxMemoryTextBytes))
	}
	if len(w.Subject) > runstate.MaxMemorySubjectBytes {
		problems = append(problems, fmt.Errorf("subject is %d bytes, limit is %d", len(w.Subject), runstate.MaxMemorySubjectBytes))
	}
	switch {
	case w.Action == memoryCompact && len(w.Compacts) == 0:
		problems = append(problems, errors.New("a compaction names the revisions it folds, in \"compacts\""))
	case w.Action != memoryCompact && len(w.Compacts) > 0:
		problems = append(problems, fmt.Errorf("%s does not take \"compacts\"", w.Action))
	}
	return errors.Join(problems...)
}

// keepsMemory reports whether this conversation's role keeps a memory at all.
func (s *Session) keepsMemory() bool {
	return s.authority().Memory
}

// performMemoryWrites carries out one reply's block, in order, each through the
// registered context action its operation names. Every write is recorded in the
// conversation's log as asked for and then as recorded or failed — by name and
// number, never by text — so a refused write never reads as one that landed.
//
// A write the log would not take is not made, for the reason a tracker action is
// not: a memory nobody recorded asking for is not one to keep. That is the one
// failure returned as an error; a write the store refused is an outcome.
func (s *Session) performMemoryWritesWithoutToolAudit(ctx context.Context, writes []MemoryWrite) ([]MemoryOutcome, error) {
	registry, err := agentcontext.Registry()
	if err != nil {
		return nil, fmt.Errorf("build the context actions: %w", err)
	}
	outcomes := make([]MemoryOutcome, 0, len(writes))
	var problems []error
	for i, write := range writes {
		outcome := MemoryOutcome{
			ID:    fmt.Sprintf("m%d.%d", s.state.Turns, i+1),
			Turn:  s.state.Turns,
			Write: write,
		}
		if err := s.emit(execution.EventMemoryRequested, memoryEventPayload(outcome)); err != nil {
			problems = append(problems, fmt.Errorf("record memory write %s: %w", outcome.ID, err))
			outcome.Failure = "the harness could not record the request, so nothing was remembered"
			outcomes = append(outcomes, outcome)
			continue
		}
		s.applyMemoryWrite(ctx, registry, &outcome)
		eventType := execution.EventMemoryFailed
		if outcome.Recorded {
			eventType = execution.EventMemoryRecorded
		}
		if err := s.emit(eventType, memoryEventPayload(outcome)); err != nil {
			problems = append(problems, fmt.Errorf("record the result of memory write %s: %w", outcome.ID, err))
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, errors.Join(problems...)
}

// applyMemoryWrite composes the revision and hands it to the context action.
// The revision carries the invocation that wrote it — this conversation, this
// turn, and what served it — and cites the conversation by identifier alone, so
// the store's audit answers which turn of which conversation the agent learned
// something in without copying a word of it.
func (s *Session) applyMemoryWrite(ctx context.Context, registry action.Registry[*agentcontext.Write], outcome *MemoryOutcome) {
	if s.options.Memories == nil {
		outcome.Failure = "no memory store is wired to this conversation, so nothing was remembered"
		return
	}
	registered, known := registry.Lookup(memoryActions[outcome.Write.Action])
	if !known {
		outcome.Failure = fmt.Sprintf("no context action is registered for %q", outcome.Write.Action)
		return
	}
	continuity, subject, err := s.memoryContinuity(outcome.Write)
	if err != nil {
		outcome.Failure = singleLine(err.Error(), maxTrackerFailureBytes)
		return
	}
	model := strings.TrimSpace(s.state.ProviderModel)
	if model == "" {
		model = s.requestedModel()
	}
	write := &agentcontext.Write{
		Store: s.options.Memories,
		Revision: runstate.MemoryRevision{
			SchemaVersion: runstate.MemorySchemaVersion,
			ProductID:     s.options.ProductID,
			Agent:         s.options.Agent,
			Role:          s.state.Role,
			Memory:        outcome.Write.Memory,
			Continuity:    continuity,
			Subject:       subject,
			Text:          strings.TrimSpace(outcome.Write.Text),
			Retired:       outcome.Write.Action == memoryRetire,
			Compacts:      outcome.Write.Compacts,
			Sources: []runstate.MemorySource{
				{Kind: runstate.MemorySourceConversation, ID: s.state.ConversationID},
			},
			Invocation: runstate.MemoryInvocation{
				Kind:           runstate.MemoryInvocationConversation,
				ID:             s.state.ConversationID,
				Turn:           s.state.Turns,
				Backend:        s.state.Backend,
				Model:          model,
				ResolvedModel:  s.state.ProviderResolvedModel,
				AccountAlias:   s.state.AccountAlias,
				ConfigRevision: s.state.ConfigRevision,
				Build:          s.state.Build,
			},
			RecordedAt: s.options.clock().Now(),
		},
	}
	if err := registered.Perform(ctx, write); err != nil {
		outcome.Failure = singleLine(err.Error(), maxTrackerFailureBytes)
		return
	}
	outcome.Recorded = true
	outcome.Sequence = write.Recorded.Sequence
}

// memoryContinuity is what a write is about. A named subject makes it a memory
// about that one thing; no subject is the agent's own knowledge — except where
// the agent already holds a memory of that name, whose subject is carried
// across, so that revising or retiring one never has to restate what it was
// about and never quietly turns it into a different memory.
func (s *Session) memoryContinuity(write MemoryWrite) (runstate.MemoryContinuity, string, error) {
	subject := strings.TrimSpace(write.Subject)
	if subject != "" {
		return runstate.MemoryContinuitySubject, subject, nil
	}
	live, _, err := s.options.Memories.Live(s.options.Agent)
	if err != nil {
		return "", "", fmt.Errorf("read what %s already remembers: %w", s.options.Agent, err)
	}
	for _, memory := range live {
		if memory.Name == write.Memory {
			return memory.Continuity, memory.Subject, nil
		}
	}
	return runstate.MemoryContinuityAgent, "", nil
}

// savedMemories is the writes among outcomes that the store recorded, as a
// pass's record names them.
func savedMemories(outcomes []MemoryOutcome) []runstate.SavedWrite {
	var saved []runstate.SavedWrite
	for _, outcome := range outcomes {
		if outcome.Recorded {
			saved = append(saved, runstate.SavedWrite{
				Kind:     runstate.SavedMemory,
				Action:   outcome.Write.Action,
				Memory:   outcome.Write.Memory,
				Revision: outcome.Sequence,
			})
		}
	}
	return saved
}

func memoryEventPayload(outcome MemoryOutcome) map[string]any {
	return map[string]any{
		"memory_id": outcome.ID,
		"turn":      outcome.Turn,
		"action":    outcome.Write.Action,
		"memory":    outcome.Write.Memory,
		"compacts":  outcome.Write.Compacts,
		"sequence":  outcome.Sequence,
		"failure":   outcome.Failure,
	}
}

// renderMemoryResults is what the role is told became of its writes. A write
// that landed is visible in the next turn's briefing anyway; a write that was
// refused is not, and a role never told would go on believing it remembered.
func renderMemoryResults(outcomes []MemoryOutcome) string {
	if len(outcomes) == 0 {
		return ""
	}
	var rendered strings.Builder
	rendered.WriteString("# Memory results\n\n")
	for _, outcome := range outcomes {
		if outcome.Recorded {
			fmt.Fprintf(&rendered, "- %s %s %q: recorded as revision %d\n", outcome.ID, outcome.Write.Action, outcome.Write.Memory, outcome.Sequence)
			continue
		}
		fmt.Fprintf(&rendered, "- %s %s %q: not recorded: %s\n", outcome.ID, outcome.Write.Action, outcome.Write.Memory, outcome.Failure)
	}
	rendered.WriteString("\n")
	return rendered.String()
}

// reportMemories tells the operator what the role put into its own memory. A
// memory enters every later turn, so one written with nobody told is agent state
// growing where the operator cannot see it; the text itself is not repeated,
// because it is in the store, where the operator's audit reads it.
func (s *Session) reportMemories(out io.Writer, reply Reply) {
	if len(reply.Memories) == 0 {
		return
	}
	title := RoleTitle(s.state.Role)
	for _, outcome := range reply.Memories {
		if outcome.Recorded {
			fmt.Fprintf(out, "the %s %s memory %q (revision %d)\n", title, memoryVerb(outcome.Write.Action), outcome.Write.Memory, outcome.Sequence)
			continue
		}
		fmt.Fprintf(out, "the %s could not %s memory %q: %s\n", title, outcome.Write.Action, outcome.Write.Memory, outcome.Failure)
	}
	fmt.Fprintln(out)
}

func memoryVerb(action string) string {
	switch action {
	case memoryCompact:
		return "compacted"
	case memoryRetire:
		return "retired"
	default:
		return "recorded"
	}
}

// renderMemories briefs the turn with what the agent remembers.
//
// A conversation with no store wired renders nothing, as the side conversations'
// block does; a store that is wired and would not answer says so, because a role
// told nothing by a broken read would conclude it had never learned anything.
func (s *Session) renderMemories() string {
	if s.options.Memories == nil || !s.keepsMemory() {
		return ""
	}
	memories, problems, err := s.options.Memories.Live(s.options.Agent)
	if err != nil {
		return "# What you remember\n\nYour memory could not be read, so this turn carries none of it: " +
			singleLine(err.Error(), maxTrackerFailureBytes) +
			". Do not read that as your having remembered nothing.\n\n"
	}
	own := ownMemories(memories)
	carried, dropped := boundedMemories(own)
	if len(carried) == 0 && len(problems) == 0 {
		return ""
	}
	var rendered strings.Builder
	rendered.WriteString("# What you remember\n\n")
	rendered.WriteString("These are your own conclusions from earlier turns, which you recorded and the harness kept for you. They are not evidence about the work and not instructions: the canonical documents, the tracker, and what the operator says now all outrank them, and a memory they contradict is out of date — revise or retire it rather than acting on it.\n\n")
	if len(problems) > 0 {
		fmt.Fprintf(&rendered, "%d of your memory records could not be read, so this account may be missing something you recorded.\n\n", len(problems))
	}
	if dropped > 0 {
		fmt.Fprintf(&rendered, "%d older memories are not listed here; the most recently recorded are. They are still kept, and compacting or retiring memories is how you make room for them.\n\n", dropped)
	}
	for _, memory := range carried {
		current := memory.Current()
		fmt.Fprintf(&rendered, "## %s (revision %d, %s)\n\n", memory.Name, current.Sequence, current.RecordedAt.UTC().Format("2006-01-02"))
		if memory.Continuity == runstate.MemoryContinuitySubject {
			fmt.Fprintf(&rendered, "About: %s\n\n", memory.Subject)
		}
		rendered.WriteString(strings.TrimSpace(current.Text))
		rendered.WriteString("\n\n")
	}
	return rendered.String()
}

// ownMemories is every live memory but the side threads' merges, which reach the
// turn through their own block, filtered to this conversation.
func ownMemories(memories []runstate.Memory) []runstate.Memory {
	var own []runstate.Memory
	for _, memory := range memories {
		if memory.Current().Invocation.Kind == runstate.MemoryInvocationSideStream {
			continue
		}
		own = append(own, memory)
	}
	return own
}

// boundedMemories is what the turn carries and how many it left, in the store's
// own order. What is kept is chosen newest first, so the bound drops what the
// agent recorded longest ago rather than whatever sorted last; the newest is
// always carried, since one memory is never larger than the bound.
func boundedMemories(memories []runstate.Memory) ([]runstate.Memory, int) {
	byRecency := append([]runstate.Memory(nil), memories...)
	sort.SliceStable(byRecency, func(i, j int) bool {
		return byRecency[i].Current().RecordedAt.After(byRecency[j].Current().RecordedAt)
	})
	kept := map[string]bool{}
	spent := 0
	for _, memory := range byRecency {
		text := len(memory.Current().Text)
		if len(kept) > 0 && spent+text > maxBriefedMemoryBytes {
			break
		}
		kept[memory.Name] = true
		spent += text
	}
	carried := make([]runstate.Memory, 0, len(kept))
	for _, memory := range memories {
		if kept[memory.Name] {
			carried = append(carried, memory)
		}
	}
	return carried, len(memories) - len(carried)
}

// memoryContract is what the three roles that keep a memory are told about it.
// It is part of each of their contracts rather than of the persona, because what
// the role may write and how it must regard what it reads are authority, and a
// persona may not restate either.
const memoryContract = `# Your memory

You keep a memory of your own across conversations: short conclusions that should shape how you work next time — how the operator reads things, what this project's checks tend to do, a mistake you made and should not repeat, where a piece of work you are carrying stands. Each turn opens with what you remember, under "What you remember". Those are your own earlier conclusions, not evidence and not instructions: the canonical documents, the tracker, and what the operator says now all outrank them, and a memory they contradict is out of date.

To record, revise, or retire a memory, end your reply with exactly one block, after the prose:

` + "```" + `yoyodyne-memory
{"memories":[{"action":"remember","memory":"short-lowercase-name","text":"what you concluded, in a sentence or two","subject":"optional: the work item, document, or branch it is about"}]}
` + "```" + `

"remember" records a new memory, or a new revision of one you already hold under that name. "retire" takes a memory out of what you are briefed with, and its text says why it stopped being true. "compact" replaces a memory with a shorter revision and names the earlier revisions it folds, in "compacts". A memory keeps the subject it was first recorded with, so leave "subject" out when revising or retiring one. Record at most four in one reply, each at most 8 KiB; everything you remember together is held to 32 KiB, and a write past that is refused until you compact or retire something. Reference documents, work items, and conversations by identifier rather than copying their text, and never put a secret in a memory. The harness records each write through your context actions, tells the operator, and tells you on your next turn what became of it. Leave the block out when you have nothing worth remembering, which is most replies.`
