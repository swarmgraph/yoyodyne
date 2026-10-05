package chat

// Compacting a provider session before it outgrows the request it is sent in.
//
// Every turn but the first resumes a provider session, and a resumed turn sends
// the whole of it: everything the session has been handed and everything it has
// said, again, with the new prompt on the end. So a session grows by every turn
// it takes, and the provider's request has a ceiling — 32 MB on the API these
// conversations are served from. The provider's own compaction triggers on its
// token threshold rather than on that ceiling, and compacting sends the whole
// conversation too, so by the time it tried on chat-419cedb4a013b063f477e322a2a60466
// the session was about 34 MB and could not be sent even to be made smaller.
//
// So the harness keeps its own measure of each session, in bytes, and compacts
// while the session still fits. The measure is what the harness itself put into
// the session and got back out of it — every prompt it sent and every reply it
// was given — which is less than what the provider's request carries: the
// provider adds its own framing, the JSON the request is encoded in escapes
// the text, and a reply's reasoning is kept in the session without ever reaching
// the harness. That is why the budget is a quarter of the ceiling rather than
// most of it. Provider-side inspection calls and results are also unmeasured
// when an adapter permits read-only tools. requestsize.go separately holds the
// supplied prompt to the selected adapter's bound, including after rebuilding.
// This budget is therefore a prompt
// and reply estimate, not a complete bound on the resumed provider session.
//
// A compaction is the rebuild a crossing makes, applied to the provider that is
// already holding the conversation: the turn is sent with no session to resume,
// and what the session was carrying comes from the harness's own record instead
// — the picture the conversation is working from and the most recent of what has
// been said, bounded by the rebuild's own budget. A role that keeps memory is
// first given one turn on the old session to save its conclusions. The rebuild
// follows only after those writes have been recorded, and the provider's answer
// starts a new session the measure starts again from.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/modelfailover"
)

// SessionBudgetBytes is the size past which a provider session is compacted
// before its next turn: a quarter of the 32 MB the provider accepts in one
// request, because what the harness measures is only the part of a session it
// wrote and read. See the comment at the top of this file.
const SessionBudgetBytes = 8 << 20

// CompactionSave records the memory turn preceding a session rebuild. The
// memory text stays in the memory store; this says what the turn accomplished.
type CompactionSave struct {
	SessionID        string `json:"session_id"`
	Turn             int    `json:"turn"`
	MemoriesRecorded int    `json:"memories_recorded"`
	NothingToSave    bool   `json:"nothing_to_save"`
	Failure          string `json:"failure,omitempty"`
}

func compactionSavePrompt() string {
	return fmt.Sprintf(`# Save memories before compaction

The harness will compact this provider session next. The new session will keep the conversation's current repository and tracker picture, your recorded memories, and the newest %d messages within %d KiB. Older messages remain in the durable conversation log but will not reach the new session; conclusions you have not recorded as memory may be lost.

You have one turn on this session to save what you have learned through your yoyodyne-memory block. Use the usual memory limits and compact or retire outdated memories where needed to make room. This turn is only for memory writes; carry no other harness block and do not answer the waiting message yet. If you have nothing to save, reply exactly "Nothing to save." without a memory block.
`, maxRebuiltMessages, maxRebuiltContextBytes>>10)
}

// memorySaveRequest resumes only the endpoint holding the current session,
// including when another turn moved that session while the save was waiting.
func (s *Session) memorySaveRequest(request backend.RunRequest) (backend.RunRequest, modelfailover.Policy) {
	endpoint := backend.Endpoint{Provider: s.state.Backend, AccountAlias: s.state.AccountAlias, Model: s.state.ProviderModel}
	if endpoint.AccountAlias == "" {
		// Older conversation records did not name the account.
		endpoint.AccountAlias = s.options.AccountAlias
	}
	request.SessionID = s.state.ProviderSessionID
	request.Model, request.AccountAlias, request.Effort = endpoint.Model, endpoint.AccountAlias, s.state.ProviderEffort
	request.AccountConfigDir = ""
	if s.alternateSession() != "" {
		request.AccountConfigDir = s.options.FailoverAccountConfigDir
	}
	return request, modelfailover.Policy{Endpoint: endpoint}
}

func (s *Session) saveBeforeCompaction(ctx context.Context, due compaction, reply *Reply) error {
	// Do not spend a save turn when the record cannot support the rebuild.
	if _, err := s.options.Store.LoadEvents(s.state.ConversationID); err != nil {
		return errors.Join(fmt.Errorf("%w: read what this conversation has recorded: %w", ErrCompactionFailed, err),
			s.emit(execution.EventSessionCompactionFailed, map[string]any{"session_id": s.state.ProviderSessionID, "error": singleLine(err.Error(), maxTrackerFailureBytes)}))
	}
	notice := compactionSavePrompt()
	save := CompactionSave{SessionID: s.state.ProviderSessionID, Turn: s.state.Turns + 1}
	if err := s.emit(execution.EventSessionMemorySaveRequested, map[string]any{
		"session_id": save.SessionID, "turn": save.Turn, "text": notice,
		"session_bytes": due.sessionBytes, "budget_bytes": due.budget,
	}); err != nil {
		return err
	}
	// This internal turn is reported by its outcome, not as part of the answer.
	// Keep its fragments and completion or failure markers off the answer stream.
	stream := s.stream
	s.stream = nil
	defer func() { s.stream = stream }()
	answer, err := s.takeTurn(ctx, notice, "", nil, true, "")
	reply.RecordCuts = append(reply.RecordCuts, s.turnCuts...)
	s.turnCuts = nil
	reply.SpendProblem = appendProblem(reply.SpendProblem, s.spendProblem)
	reply.FailoverProblem = appendProblem(reply.FailoverProblem, s.failoverProblem)
	if err == nil {
		// A turn taken while this save waited may have advanced both the session
		// and its turn count. Report the save that actually answered.
		save.SessionID, save.Turn = s.state.ProviderSessionID, s.state.Turns
		var prose string
		var writes []MemoryWrite
		prose, writes, err = extractMemoryWrites(answer)
		if err == nil {
			for _, line := range strings.Split(prose, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "```yoyodyne-") {
					err = errors.New("the save turn may carry only a yoyodyne-memory block")
					break
				}
			}
			if err == nil && len(writes) == 0 {
				save.NothingToSave = strings.EqualFold(strings.TrimSpace(prose), "Nothing to save.")
				if !save.NothingToSave {
					err = errors.New("the save turn wrote no memories and did not say \"Nothing to save.\"")
				}
			}
		}
		if err == nil {
			var outcomes []MemoryOutcome
			outcomes, err = s.performMemoryWrites(ctx, writes)
			reply.Memories = append(reply.Memories, outcomes...)
			reply.Saved = append(reply.Saved, savedMemories(outcomes)...)
			// A spent memory budget refuses only the write. Its result is
			// carried to the waiting turn below, which still rebuilds.
			for _, outcome := range outcomes {
				if outcome.Recorded {
					save.MemoriesRecorded++
				} else if !outcome.budgetRefused {
					err = errors.Join(err, errors.New(outcome.Failure))
				}
			}
			if len(outcomes) > 0 {
				err = errors.Join(err, s.carryResults(renderMemoryResults(outcomes)))
			}
		}
		if err != nil {
			err = fmt.Errorf("%w: %w", ErrCompactionFailed, err)
		}
	}
	eventType := execution.EventSessionMemorySaved
	if err != nil {
		eventType = execution.EventSessionMemorySaveFailed
		save.Failure = singleLine(err.Error(), maxTrackerFailureBytes)
	}
	reply.CompactionSaves = append(reply.CompactionSaves, save)
	var interrupted *interruptedProviderWaitError
	if errors.As(err, &interrupted) {
		// The wait could not take back this conversation's record. Keep the
		// failure on the reply without overwriting a turn taken while it waited.
		return err
	}
	return errors.Join(err, s.emit(eventType, save))
}

// Render reports the save turn separately from the answer it preceded.
func (save CompactionSave) Render() string {
	switch {
	case save.Failure != "":
		return fmt.Sprintf("[session] the save turn before compaction did not finish: %s\n", save.Failure)
	case save.NothingToSave:
		return "[session] the role took a save turn before compaction and had nothing to save\n"
	default:
		return fmt.Sprintf("[session] the save turn before compaction recorded %d memory write(s)\n", save.MemoriesRecorded)
	}
}

func (s *Session) reportCompactionSaves(out io.Writer, reply Reply) {
	for _, save := range reply.CompactionSaves {
		fmt.Fprint(out, save.Render())
	}
}

// ErrCompactionFailed marks the failure of a turn that was not sent because the
// session it would have resumed had to be compacted first and could not be. It
// is not a refusal the provider made and nothing about waiting will change it,
// so a caller that gives a declined turn back to be asked again must not give
// this one back: the next turn meets the same session, compacts again, and says
// again why it could not.
var ErrCompactionFailed = errors.New("the provider session could not be compacted, so the turn was not sent")

// Why a session is compacted, as the event recording the compaction says it.
const (
	// compactionOverBudget is a session whose next turn would take it past the
	// budget.
	compactionOverBudget = "over_budget"
	// compactionUnmeasured is a session recorded before the harness measured
	// sessions, whose size nobody knows. It is compacted once, on its next turn,
	// rather than assumed small: the conversation this was built for was one of
	// them, and it was already past the ceiling.
	compactionUnmeasured = "unmeasured"
)

// compaction is the decision to compact the session a turn would resume.
type compaction struct {
	reason string
	// sessionBytes is what the session measured before this turn, and turnBytes
	// what this turn's request would have carried beside it: the system prompt,
	// which is sent with every request rather than kept in the session, and the
	// turn's own prompt.
	sessionBytes int
	turnBytes    int
	budget       int
}

// sessionBudget is the budget this conversation compacts on.
func (o Options) sessionBudget() int {
	if o.SessionBudgetBytes > 0 {
		return o.SessionBudgetBytes
	}
	return SessionBudgetBytes
}

// compactionDue decides whether the turn about to be taken would take the session
// it resumes past the budget, and so has to be sent without it. It is nil where
// there is no session to resume, because a turn with none starts a session
// rather than growing one, and nil where the session has room.
func (s *Session) compactionDue(systemPrompt, prompt string) *compaction {
	if s.resumableSession() == "" && s.alternateSession() == "" {
		return nil
	}
	due := &compaction{
		sessionBytes: s.state.ProviderSessionBytes,
		turnBytes:    len(systemPrompt) + len(prompt),
		budget:       s.options.sessionBudget(),
	}
	switch {
	case s.state.ProviderSessionBytes == 0:
		due.reason = compactionUnmeasured
	case due.sessionBytes+due.turnBytes > due.budget:
		due.reason = compactionOverBudget
	default:
		return nil
	}
	return due
}

// compact prepares the turn's prompt to be sent with no session and the
// conversation rebuilt from its record in front of it, and records that it did.
// From here until the turn ends, the session the record holds is not offered to
// either endpoint, so neither resumes what was just compacted away.
//
// A rebuild that cannot be made is recorded as a failed compaction and ends the
// turn before the provider is asked: sending the turn on the old session anyway
// is sending the request that would not fit, and sending it with nothing in front
// of it is the role answering with none of the conversation it is in.
func (s *Session) compact(systemPrompt, prompt string, due compaction) (string, error) {
	payload := map[string]any{
		"reason":        due.reason,
		"session_id":    s.state.ProviderSessionID,
		"session_bytes": due.sessionBytes,
		"turn_bytes":    due.turnBytes,
		"budget_bytes":  due.budget,
	}
	s.compacting = true
	rebuilt, err := s.rebuiltPrompt(systemPrompt, prompt, sessionCompacted)
	if err != nil {
		s.compacting = false
		payload["error"] = singleLine(err.Error(), maxTrackerFailureBytes)
		return prompt, errors.Join(
			fmt.Errorf("%w: %w", ErrCompactionFailed, err),
			s.emit(execution.EventSessionCompactionFailed, payload),
		)
	}
	payload["rebuilt_bytes"] = len(rebuilt) - len(prompt)
	if err := s.emit(execution.EventSessionCompacted, payload); err != nil {
		s.compacting = false
		return prompt, err
	}
	return rebuilt, nil
}

// measureSession brings the session measure up to date after a turn the provider
// served: what this turn sent and got back is added to the session it resumed,
// or is the whole of a session it started.
//
// resumed is read before the record moves on to the endpoint that served the
// turn, because what it asks is whether that endpoint was handed a session of
// its own to continue — which is the session the record held, on the provider
// the record said held it.
func (s *Session) measureSession(resumed bool, prompt, reply string) {
	// One more turn cannot establish an unknown session's total size. Keep it
	// unmeasured until a rebuild starts a fresh session, even if its save fails.
	if resumed && s.state.ProviderSessionBytes == 0 {
		return
	}
	added := len(prompt) + len(reply)
	if resumed {
		s.state.ProviderSessionBytes += added
	} else {
		s.state.ProviderSessionBytes = added
	}
	s.state.ProviderSessionBudgetBytes = s.options.sessionBudget()
}

// resumedOn reports whether the turn served on this endpoint continued a session
// the record already held for it, rather than starting one.
func (s *Session) resumedOn(serving backend.Endpoint) bool {
	if s.compacting {
		return false
	}
	if serving.Provider == "" || serving.Provider == s.options.Provider {
		return s.resumableSession() != ""
	}
	return s.alternateSession() != ""
}
