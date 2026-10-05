package chat

// Continuing a conversation on a provider that has never held it.
//
// Every turn but the first resumes a provider session, and that is the whole of
// why a later turn's prompt carries so little: the session already holds the
// briefing, the operator's earlier messages, and everything the role has said
// back. It is an accelerator the harness does not own — the session belongs to
// the provider, expires on its clock, and means nothing to any other provider —
// so the moment a turn is served somewhere else there is nothing to resume and
// the context has to come from the record the harness does own.
//
// So a crossing rebuilds. The prompt the second provider is handed is assembled
// from the conversation's durable state — the picture it is working from, and
// the account of what has been said that its event log holds — with the turn's
// own prompt after it and no session identifier anywhere. That is the
// durable-state guarantee doing the work it exists for: a conversation whose
// provider becomes unavailable carries on, and what carries it is the record
// rather than somebody else's session.
//
// What the rebuild can say is bounded by what the record holds, and it says so
// rather than implying otherwise. The event log carries both sides of the
// exchange — what the operator said, recorded by the harness before each turn,
// and what the role answered, recorded by the provider as it wrote — so the
// reconstruction replays them in order, each side named. A conversation begun
// before the operator's side was kept has replies with no message before them,
// and the framing says what that absence means. A role that is told which it is
// reading can say it is missing something; one that is handed a gap dressed as a
// transcript will fill it in.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/modelfailover"
)

// maxRebuiltContextBytes bounds what a rebuild puts in front of the turn's own
// prompt. It is a fraction of MaxTurnInputBytes rather than the whole of it,
// because the turn still has to fit beside it: a rebuild that filled the budget
// would produce a turn refused for its own size, which is the substitution
// failing in a way that reads as the operator's message being too long. It is
// spent on what has been said, oldest dropped first, and a turn whose fresh
// session is refused on a rebuild this size is rebuilt once on half of it; see
// shrinkRebuild.
const maxRebuiltContextBytes = 256 << 10

// maxRebuiltMessages bounds how many recorded messages, either side's, the
// rebuild carries. The most recent are the ones a continuation needs, and a
// conversation somebody has held for days is one whose earliest turns are further
// from what is being said now than the budget above is worth spending on. A turn
// is two messages — the operator's and the reply — so this is around forty turns'
// worth, which is what it was when only the reply was recorded.
const maxRebuiltMessages = 80

// servingEndpoint is where a turn was actually served, as the record should say
// it. The failover answers it where it resolved an endpoint, and this
// conversation's own configuration answers it where nothing did — a policy with
// no endpoint to name is one nobody resolved, and the record is better off saying
// what was configured than saying nothing.
func (s *Session) servingEndpoint(served modelfailover.Served) backend.Endpoint {
	if served.Endpoint.Provider != "" {
		return served.Endpoint
	}
	return backend.Endpoint{
		Provider:     s.options.Provider,
		AccountAlias: s.options.AccountAlias,
		Model:        served.Model,
	}
}

// resumableSession is the provider session this turn may continue from, and
// empty where there is none to continue.
//
// A session belongs to the provider that issued it and means nothing to any
// other, so a conversation whose last turn crossed providers holds a session
// identifier the provider about to be asked has never seen. Sending it would be
// asking one provider to resume another's conversation; what happens instead is
// that this turn starts a session of its own, and the context the old one held is
// rebuilt from the record — which is the same answer the crossing itself made,
// applied to the crossing back.
//
// The comparison is against the recorded backend rather than against anything
// this process remembers, because the process that took the crossing turn is
// rarely the process that takes the next one.
func (s *Session) resumableSession() string {
	// A turn compacting the session is sent without it, whichever endpoint holds
	// it; see compact.go.
	if s.compacting {
		return ""
	}
	if s.state.Backend != "" && s.state.Backend != s.options.Provider {
		return ""
	}
	return s.state.ProviderSessionID
}

// alternateSession is the session the endpoint this conversation fails over to
// already holds for it, and empty where it holds none.
//
// A window lasts longer than one turn, so the turn after a crossing goes to the
// same alternate — and by then that provider has a session of its own, recorded
// exactly as the configured provider's is. Resuming it is what keeps an outage
// costing one reconstruction rather than one per turn, and it is the same
// question resumableSession asks, asked about the other endpoint.
func (s *Session) alternateSession() string {
	alternate := s.options.FailoverEndpoint.Provider
	if s.compacting || alternate == "" || s.state.Backend != alternate {
		return ""
	}
	return s.state.ProviderSessionID
}

// rebuiltContextHeader opens the reconstruction, and is how a request that has
// already been rebuilt is recognized as one. A turn can reach the rebuild twice —
// prepared here for the endpoint it was going to and then moved onto the other
// one by a refusal nobody could have known about in advance — and two
// reconstructions in one prompt is the conversation told to itself twice.
const rebuiltContextHeader = "# This conversation, rebuilt from its record"

// rebuildForOwnEndpoint prepares a turn going to the provider this conversation
// is configured for, where that provider holds no session to resume — which is
// the turn after a crossing, once the window it was waiting out has lifted. The
// alternate's session is no answer there: it belongs to the provider not being
// asked.
//
// It is also the turn after one whose fresh session failed: the session the
// provider refused was set aside before that attempt, so there is nothing to
// resume here either. Which of the two it is comes from the record — a set-aside
// session is recorded as one — and a conversation with no session for any other
// reason is told only that there is none.
func (s *Session) rebuildForOwnEndpoint(request backend.RunRequest) (backend.RunRequest, error) {
	switch {
	case s.state.SessionSetAside != "":
		return s.rebuildFromRecord(request, sessionSetAside)
	case s.state.Backend != "" && s.state.Backend != s.options.Provider:
		return s.rebuildFromRecord(request, crossedProviders)
	default:
		return s.rebuildFromRecord(request, noSessionHeld)
	}
}

// rebuildForAlternate prepares a turn the failover is moving onto the alternate,
// and is what the policy calls.
//
// It adds nothing where the alternate is already holding a session for this
// conversation, which is every turn of an outage after the first: that session
// carries the context, so reconstructing it would be telling the provider what it
// already knows, once per turn, for as long as the window stands.
func (s *Session) rebuildForAlternate(request backend.RunRequest) (backend.RunRequest, error) {
	if s.alternateSession() != "" {
		return request, nil
	}
	return s.rebuildFromRecord(request, crossedProviders)
}

// rebuildFromRecord assembles the request a provider that holds no session for
// this conversation is asked, from the conversation's own durable state.
//
// The request it is given is the one the turn was built with, so the system
// prompt, the role, the tools, and the turn's own prompt are already right — none
// of those came from a provider session. What is added in front of the prompt is
// what a session would have been carrying: the picture, and what has been said.
//
// A request that already carries the reconstruction is returned as it is. A turn
// can reach this twice — prepared for the endpoint it was nominally on, then
// moved onto the other one by a refusal nobody could have known about in advance
// — and the conversation told to itself twice is worse than either endpoint
// getting it once.
// why is the sentence telling the provider why it holds no session, which is
// a crossing, a session the provider refused to continue, or one the harness
// compacted.
func (s *Session) rebuildFromRecord(request backend.RunRequest, why string) (backend.RunRequest, error) {
	// A failure hands the request back as it came rather than as a zero value. The
	// caller discards it either way, and nothing here is a provider invocation —
	// this assembles what one will be asked, and the invocation itself is made by
	// whoever called for the rebuild.
	prompt, err := s.rebuiltPrompt(request.SystemPrompt, request.Prompt, why)
	if err != nil {
		return request, err
	}
	request.Prompt = prompt
	return request, nil
}

// rebuiltPrompt is the turn's prompt with the conversation rebuilt from its
// record in front of it, which is the whole of what rebuildFromRecord changes
// about a request. The system prompt is read only to hold the two to the turn's
// bound together.
func (s *Session) rebuiltPrompt(systemPrompt, prompt, why string) (string, error) {
	if strings.HasPrefix(prompt, rebuiltContextHeader) {
		return prompt, nil
	}
	events, err := s.options.Store.LoadEvents(s.state.ConversationID)
	if err != nil {
		return prompt, fmt.Errorf("read what this conversation has recorded: %w", err)
	}
	// The waiting operator message is already the prompt. Any save turn following
	// it is history, while a refused attempt's events belong to the invocation
	// being replaced rather than something said before it.
	events = recordedBefore(events, s.turnBegan)
	for index, event := range events {
		if event.Sequence == s.turnOperatorSequence {
			events = append(events[:index], events[index+1:]...)
			break
		}
	}
	// The turn's own prompt was redacted before it reached here, and this is
	// assembled afterwards out of the briefing and the event log, so it is redacted
	// here rather than inheriting a pass it was not part of. Anything recognizably
	// sensitive leaves the harness once per invocation, and a crossing is one more
	// invocation to a provider that has never seen any of it.
	rebuilt := execution.NewRedactor(s.options.RedactValues...).Redact(s.rebuiltContext(events, why))
	if rebuilt == "" {
		// A conversation with nothing recorded is one whose first turn is being
		// taken, and its prompt already carries the briefing. There is nothing to
		// rebuild and nothing missing, so the request stands as it is.
		return prompt, nil
	}
	if len(rebuilt)+len(systemPrompt)+len(prompt) > MaxTurnInputBytes {
		return prompt, fmt.Errorf(
			"the rebuilt context and this turn are %d bytes together, limit is %d",
			len(rebuilt)+len(systemPrompt)+len(prompt), MaxTurnInputBytes)
	}
	s.rebuiltFrom = &rebuildInput{systemPrompt: systemPrompt, prompt: prompt, why: why}
	return rebuilt + prompt, nil
}

// rebuildInput is what a turn's rebuild was made from: the turn's own prompt
// and system prompt, and the reason the provider was given for holding no
// session. It is kept so the rebuild can be made again on a smaller bound
// without unpicking the prompt it was put in front of.
type rebuildInput struct {
	systemPrompt string
	prompt       string
	why          string
}

// rebuildMessageBudget is what this turn's rebuild may spend on what has been
// said.
func (s *Session) rebuildMessageBudget() int {
	if s.rebuiltMessageBytes < 0 {
		return 0
	}
	if s.rebuiltMessageBytes > 0 {
		return s.rebuiltMessageBytes
	}
	return maxRebuiltContextBytes
}

// shrinkRebuild is the turn rebuilt again on half the bound it was just refused
// on, and false where there is nothing to shrink.
//
// The byte bound is a guess at what a provider will take rather than something
// any provider states, so a fresh session can be refused as too long on a
// rebuild the bound allowed. That refusal is the one every later turn would meet
// as well, because each of them rebuilds the same record for the same fresh
// session; what gets the conversation past it is a smaller rebuild, and the
// oldest of what it carried is what goes, said in its framing as any other drop
// is. It is halved once rather than until something fits: a rebuild half the
// size refused again is a provider refusing something other than this, and
// guessing further down only hands the role less of its conversation.
//
// A rebuild the halving leaves as it was — everything said already fits in
// half — is not asked again, because the same request would be refused the same
// way.
func (s *Session) shrinkRebuild(request backend.RunRequest) (backend.RunRequest, bool) {
	from := s.rebuiltFrom
	if from == nil || !strings.HasPrefix(request.Prompt, rebuiltContextHeader) || !strings.HasSuffix(request.Prompt, from.prompt) {
		return request, false
	}
	s.rebuiltMessageBytes = s.rebuildMessageBudget() / 2
	prompt, err := s.rebuiltPrompt(from.systemPrompt, from.prompt, from.why)
	if err != nil || prompt == request.Prompt {
		return request, false
	}
	request.Prompt = prompt
	return request, true
}

// Why a provider is being handed a reconstruction rather than a session, as the
// reconstruction tells it. A role that knows its session was set aside for length
// knows the earliest of what it said is gone for good, which is different from
// knowing another provider was holding it.
const (
	crossedProviders = "The provider that was holding it is not the one serving this turn, so none of its session reaches you."
	sessionSetAside  = "The provider session that was holding it was refused as too long to continue, or could not be compacted, so this turn is served in a fresh session and none of the old one reaches you."
	noSessionHeld    = "No provider session is held for it, so none of an earlier one reaches you."
	sessionCompacted = "Its provider session had grown too large to keep sending, so the harness compacted it: this turn is served in a fresh session and none of the old one reaches you."
)

// rebuiltContext is what the provider is told before the turn itself: which
// conversation this is and why it is being handed this at all, the picture the
// conversation is working from, and the account of what has been said.
func (s *Session) rebuiltContext(events []execution.Event, why string) string {
	said := recordedMessages(events, s.rebuildMessageBudget())
	briefing := s.workingBriefing()
	if said == "" && briefing == "" {
		return ""
	}
	var rebuilt strings.Builder
	rebuilt.WriteString(rebuiltContextHeader + "\n\n")
	rebuilt.WriteString(fmt.Sprintf(
		"You are continuing conversation %s, which has taken %d turn(s) so far. %s What follows is assembled from the harness's own durable record of the conversation, and it is the whole of what you have.\n\n",
		s.state.ConversationID, s.state.Turns, why))
	if briefing != "" {
		rebuilt.WriteString(briefing)
		rebuilt.WriteString("\n")
	}
	if said != "" {
		rebuilt.WriteString("## What has been said so far\n\n")
		rebuilt.WriteString("This is the exchange in order, as the harness recorded it: what the operator said, and what you replied. A reply with no operator message before it answers one the harness did not record, which is how conversations begun before the operator's side was kept read; where that leaves you unsure what was asked, say so rather than assuming.\n\n")
		rebuilt.WriteString(said)
		rebuilt.WriteString("\n")
	}
	return rebuilt.String()
}

// recordedBefore is the part of the log recorded up to and including sequence
// through — the record as it stood when the turn in flight began. The log is
// read back in sequence order, so this is a prefix of it.
func recordedBefore(events []execution.Event, through uint64) []execution.Event {
	for index, event := range events {
		if event.Sequence > through {
			return events[:index]
		}
	}
	return events
}

// recordedMessage is one thing one side said, as the event log holds it.
type recordedMessage struct {
	operator bool
	harness  bool
	text     string
}

// How the rebuild names each side of the exchange. The operator's messages and
// the role's replies are the same event shape, so the order they were recorded in
// is the order they were said in, and a line naming the side is all the rebuild
// adds to each.
const (
	operatorSaid = "**The operator said:**"
	roleReplied  = "**You replied:**"
	harnessSaid  = "**The harness asked you to save memories:**"
)

// workingBriefing is the picture this conversation is working from: the one a
// refresh replaced it with where the operator asked for one, and the one it was
// opened on otherwise. A later turn does not carry it, because the session it
// resumes already held it — which is exactly why a rebuild has to.
//
// It is nothing on a turn whose own prompt already carries the picture: the first
// turn, and any turn the operator asked for a refresh on. Repeating it there
// would hand the provider the same document twice, which spends context to say
// the same thing and reads as two pictures to reconcile.
//
// A refresh carried as changes is the exception: what moved means nothing to a
// provider that never held the picture it moved from, so the rebuild carries the
// whole of the new picture and the turn's own prompt says what in it is new.
func (s *Session) workingBriefing() string {
	if s.refresh != nil && s.carriedChanges {
		return strings.TrimSpace(s.refresh.briefing.Text)
	}
	if s.refresh != nil || s.state.Turns == 0 {
		return ""
	}
	if s.carried != nil {
		return strings.TrimSpace(s.carried.Text)
	}
	return strings.TrimSpace(s.options.Briefing.Text)
}

// recordedMessages is what has been said on both sides, read back out of the
// conversation's event log in the order it was recorded and bounded twice: to the
// most recent messages, and to budget, the bytes a rebuild may spend. Both bounds
// count the operator's messages and the role's replies alike, because the two are
// one exchange and a budget spent on one side alone would keep answers whose
// questions were dropped. An account that dropped something says so, because a
// role told it has everything and given part of it will reason as though the
// missing part never happened — and that holds where it dropped everything, too.
func recordedMessages(events []execution.Event, budget int) string {
	spoken := make([]recordedMessage, 0, len(events))
	for _, event := range events {
		var fromOperator bool
		var fromHarness bool
		switch event.Type {
		case execution.EventAgentMessage:
		case execution.EventOperatorMessage:
			fromOperator = true
		case execution.EventSessionMemorySaveRequested:
			fromHarness = true
		default:
			continue
		}
		if text := messageText(event); text != "" {
			spoken = append(spoken, recordedMessage{operator: fromOperator, harness: fromHarness, text: text})
		}
	}
	if len(spoken) == 0 {
		return ""
	}
	dropped := 0
	if len(spoken) > maxRebuiltMessages {
		dropped = len(spoken) - maxRebuiltMessages
		spoken = spoken[dropped:]
	}
	// The budget is spent from the most recent backwards, so what a long
	// conversation keeps is the part nearest to what is being said now.
	kept := make([]recordedMessage, 0, len(spoken))
	for index := len(spoken) - 1; index >= 0; index-- {
		if len(spoken[index].text) > budget {
			dropped += index + 1
			break
		}
		budget -= len(spoken[index].text)
		kept = append([]recordedMessage{spoken[index]}, kept...)
	}
	var rendered strings.Builder
	if dropped > 0 {
		rendered.WriteString(fmt.Sprintf("- %d earlier message(s) are not carried here.\n\n", dropped))
		rendered.WriteString("Only this request is shortened. Earlier messages remain in the durable conversation log; recorded decisions, memories and docket entries remain in their stores.\n\n")
	}
	for _, message := range kept {
		if message.operator {
			rendered.WriteString(operatorSaid)
		} else if message.harness {
			rendered.WriteString(harnessSaid)
		} else {
			rendered.WriteString(roleReplied)
		}
		rendered.WriteString("\n\n")
		rendered.WriteString(message.text)
		rendered.WriteString("\n\n")
	}
	return rendered.String()
}

// messageText is the prose one recorded message carries, either side's. An event
// whose payload is not one the harness wrote is skipped rather than guessed at:
// what a rebuild puts in front of a role has to be what the record actually says.
func messageText(event execution.Event) string {
	if len(event.Payload) == 0 {
		return ""
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Text)
}

// sessionTooLong is how a provider says the session a turn resumed can no longer
// be continued: the conversation has grown past what one request may carry, or
// past the context window, and the compaction that would have shrunk it failed or
// could not be attempted. The words are the providers' own — Claude Code's "Prompt
// is too long" and its API's request_too_large, the compaction that errored,
// Codex's context_length_exceeded — because none of them is a status a dialect
// reads into an answer of its own: every one arrives as a refusal like any other.
var sessionTooLong = regexp.MustCompile(`(?i)prompt is too long|conversation (is )?too long|request_too_large|request exceeds the maximum size|input_too_large|input exceeds the maximum length|context_length_exceeded|exceeds (the|its) context window|context window (is )?(exceeded|full)|error during compaction|compaction failed|failed to compact`)

// unflaggedTooLong is the same refusal where the provider ended the turn without
// flagging it as a failure, which it has been seen to do with its notice as the
// whole of the result. Only the notice on its own is read that way: a served
// reply that mentions one of these phrases is a role talking about length, and
// reading it as a refusal would throw away a healthy session and a finished turn.
var unflaggedTooLong = regexp.MustCompile(`(?i)^(prompt is too long|conversation (is )?too long|request_too_large|context_length_exceeded)[.!]?$`)

// refusedAsTooLong is what the provider said where it refused this turn's session
// as too long to continue, and empty where it did anything else.
func refusedAsTooLong(result backend.RunResult, err error) string {
	switch {
	case err != nil:
		if sessionTooLong.MatchString(err.Error()) {
			return err.Error()
		}
		if described := result.DescribeFailure(); result.IsError && sessionTooLong.MatchString(described) {
			return described
		}
	case result.IsError:
		if described := result.DescribeFailure(); sessionTooLong.MatchString(described) {
			return described
		}
	default:
		if text := strings.TrimSpace(result.FinalText); unflaggedTooLong.MatchString(text) {
			return text
		}
	}
	return ""
}

// replaceSession sets aside the provider session a turn was refused on as too
// long, records that it did and why, and hands back the turn rebuilt from the
// record for a fresh session under the same conversation. refusedOn and replaced
// are the endpoint the refused attempt actually went to and the session it
// resumed there, which is the alternate's where the failover had moved the turn.
//
// Nothing about the conversation but the session changes. The identifier, the
// memory store keyed to the role, the report position, and the picture are all
// the harness's own and carry over as they are; the only thing lost is a session
// that was already unusable, and what stands in for it is the same rebuild a
// crossing makes. The session is cleared on the record before the fresh attempt is
// made, so a fresh attempt that fails leaves the next turn rebuilding again rather
// than resuming the session that was refused — which is what keeps this from
// dead-ending into a conversation somebody has to replace by hand.
//
// A failed replacement or rebuild is returned and named on the reply. Request
// size recovery stops on it; recovery from other context refusals may still
// try the original prompt without the unusable session.
func (s *Session) replaceSession(request backend.RunRequest, refusedOn backend.Endpoint, replaced, why string) (backend.RunRequest, error) {
	s.state.ProviderSessionID = ""
	s.state.SessionSetAside = singleLine(why, maxTrackerFailureBytes)
	recordErr := s.emit(execution.EventSessionReplaced, map[string]any{
		"replaced_session": replaced,
		"provider":         refusedOn.Provider,
		"account":          refusedOn.AccountAlias,
		"model":            refusedOn.Model,
		"reason":           s.state.SessionSetAside,
	})
	if recordErr != nil {
		s.failoverProblem = appendProblem(s.failoverProblem, singleLine(
			"the provider session set aside as too long was not recorded: "+recordErr.Error(), maxTrackerFailureBytes))
	}
	request.SessionID = ""
	request.LastSequence = s.state.LastSequence
	rebuilt, err := s.rebuildFromRecord(request, sessionSetAside)
	if err != nil {
		s.failoverProblem = appendProblem(s.failoverProblem, singleLine(err.Error(), maxTrackerFailureBytes))
		return request, errors.Join(recordErr, err)
	}
	return rebuilt, recordErr
}
