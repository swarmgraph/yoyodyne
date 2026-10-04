package execution

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

// EventSchemaVersion is what a event is written at now, and
// MinReadableEventSchemaVersion the oldest an event log may be read at. A run's
// log is append-only and never rewritten, so a repository that has been running
// holds logs at every version it has ever written; reading is a range for that
// reason, and writing is one number because there is only one shape to write.
const (
	EventSchemaVersion            = 2
	MinReadableEventSchemaVersion = 1
)

// TerminalRoleSchemaVersion is the first version whose terminals name the role
// that made the invocation. It is what lets a reader of a mixed history tell a
// terminal that failed to say whose it was from one recorded before there was
// anything to say: below this version the absence means the former did not exist
// yet, and at or above it the absence is the omission itself. A fixed number is
// what makes that decidable rather than a guess about when a log was written,
// and it is separate from EventSchemaVersion so that the next version to be
// added does not silently move it.
const TerminalRoleSchemaVersion = 2

// DuplicateTerminalAnomaly is the value of the "anomaly" key on the
// process.output event a backend records when the provider ends one invocation
// twice. The second terminal is not a second invocation, so it is never recorded
// as run.completed or run.failed, but it still carries what the provider charged
// for the turn that produced it: the pricing in internal/runstate reads this
// event as more cost for the invocation whose terminal it followed, by the same
// session-increment rule every terminal is priced by. It is named here, beside
// the event types, because the backend that writes it and the pricing that reads
// it share nothing else.
const DuplicateTerminalAnomaly = "duplicate_terminal_result"

type EventType string

const (
	EventRunStarted    EventType = "run.started"
	EventToolRequested EventType = "tool.requested"
	EventToolPerformed EventType = "tool.performed"
	EventToolRefused   EventType = "tool.refused"
	// One provider invocation's terminal, whichever way it ended, carrying what
	// the provider said it cost. A log can hold several of them — the developer's
	// attempts and the reviewer's invocations share one — so from
	// TerminalRoleSchemaVersion each must name the role it was made as, under the
	// payload key "role". A backend that omits it leaves money nothing can
	// attribute: the phase split in internal/runstate places such a terminal
	// nowhere rather than guessing, so the cost stays in the run's total and out
	// of every phase. Where a terminal sits relative to the others is not a
	// substitute — that is a fact about the order the harness happened to do
	// things in, and anything reading it as a phase is guessing.
	EventRunCompleted  EventType = "run.completed"
	EventRunFailed     EventType = "run.failed"
	EventProcessOutput EventType = "process.output"
	EventAgentMessage  EventType = "agent.message"
	// The other side of a conversation's exchange: what the operator said, as the
	// harness recorded it before asking the role to answer. It is the harness's
	// event rather than the backend's because no provider ever sees the message
	// as anything but prompt, and a log holding only agent.message is half a
	// conversation — a rebuild from it, on another provider or after the session
	// expired, read the questions off the answers. The payload is the same shape
	// as agent.message, "text", redacted and cut to the same bound.
	EventOperatorMessage  EventType = "operator.message"
	EventCommandStarted   EventType = "command.started"
	EventCommandCompleted EventType = "command.completed"
	EventFileChanged      EventType = "file.changed"
	EventReviewStarted    EventType = "review.started"
	EventReviewCompleted  EventType = "review.completed"
	// A reviewer that answered with a field the verdict schema does not define
	// has drifted from the contract it was given. The verdict is decoded without
	// the extra fields rather than refused, and what the reviewer invented is
	// recorded here instead: it costs the run nothing, and it is what a prompt
	// regression is diagnosed from afterwards.
	EventReviewDrift EventType = "review.drift"
	// A proposal and the operator's decision on it are separate events, because
	// what was proposed is evidence whether or not it was ever created.
	EventProposalRecorded EventType = "proposal.recorded"
	EventProposalApproved EventType = "proposal.approved"
	EventProposalRejected EventType = "proposal.rejected"
	// A proposal the harness admitted itself, on the strength of the operator's
	// approval of the goal it serves. It is its own event rather than an approval
	// with a different reason: nobody approved this item, and a record that read
	// as though somebody had would be the one claim this arrangement cannot make.
	EventProposalAdmitted EventType = "proposal.admitted"
	EventProposalCreated  EventType = "proposal.created"
	// A proposal its proposer took back before anybody decided it. It is not a
	// rejection: nobody turned it down, and a record reading as though the
	// operator had would put words in his mouth.
	EventProposalWithdrawn EventType = "proposal.withdrawn"
	// A document an owning role wrote, the operator's decision about it, and the
	// write itself are three events rather than one. What was drafted is evidence
	// whether or not it was ever filed, an approval is the operator's and is
	// recorded before anything is written so a failed write cannot erase it, and
	// only the last of these says the repository changed.
	EventDocumentDrafted  EventType = "document.drafted"
	EventDocumentApproved EventType = "document.approved"
	EventDocumentDeclined EventType = "document.declined"
	EventDocumentWritten  EventType = "document.written"
	// A concern is work the product manager judged against the goals and put to
	// the operator as a question instead of proposing. What it raised and what
	// it was told are separate events for the same reason a proposal and its
	// decision are: the concern is evidence whether or not anybody answered it.
	EventConcernRaised   EventType = "concern.raised"
	EventConcernAnswered EventType = "concern.answered"
	// A tracker action the product manager takes is recorded as what was asked
	// for and what came of it, separately, so an action that failed is never
	// readable as one that was carried out.
	EventTrackerActionRequested EventType = "tracker.action.requested"
	EventTrackerActionApplied   EventType = "tracker.action.applied"
	EventTrackerActionFailed    EventType = "tracker.action.failed"
	// A whole block of tracker actions the harness would not read, which is
	// recorded even though no action in it was requested: the three events above
	// are written per action and a refused block never reaches them, so without
	// this the only trace of a dozen lost admissions was a line on whichever
	// terminal happened to be watching. It carries the role that asked, how many
	// actions the block asked for where the harness could count them, and the
	// refusal in the words the role is given back.
	EventTrackerBlockRefused EventType = "tracker.block.refused"
	// A refused block the harness has stopped trying to have corrected: the turn
	// it started to get the actions re-issued had its own block refused, or the
	// refusal before this one was still unanswered when this one arrived. It is
	// separate from the refusal above because it is the opposite news — the
	// self-correction was attempted and did not take — and a log that recorded the
	// two the same way would say a role had lost two blocks and nothing about the
	// harness having already tried. It carries both refusals and whether the
	// harness woke the conversation for the first.
	EventTrackerRefusalUnresolved EventType = "tracker.refusal.unresolved"
	// A tracker call a conversation waited out and asked again, recorded before
	// the wait is taken. It is the conversation's copy of the retry a run writes
	// into its own state: a turn that took two minutes because the store was
	// contended is otherwise indistinguishable from one that took two minutes
	// thinking, and the reason four runs' worth of lost work took a day to
	// diagnose is that nothing anywhere said a connection had been reset. It
	// carries the boundary, which attempt this was, the wait in seconds, and the
	// failure in the tracker's own words, bounded.
	EventTrackerRetried EventType = "tracker.retried"
	// What an agent reports while its work continues is recorded in that
	// invocation's own log as well as in the collected pile: the run or
	// conversation says a report was made, and the pile says what it was. A
	// block the harness could not read is recorded too, because the work it
	// accompanied is unaffected by it and the report would otherwise leave no
	// trace at all.
	EventReportRecorded   EventType = "report.recorded"
	EventReportUnreadable EventType = "report.unreadable"
	// Work a conversation steers is recorded in that conversation's own log, so
	// what the operator asked the harness to do is evidence beside what was said
	// to arrive at it. The run these describe keeps its own separate log.
	EventWorkStarted  EventType = "work.started"
	EventWorkFinished EventType = "work.finished"
	EventWorkStopped  EventType = "work.stopped"
	EventWorkDirected EventType = "work.directed"
	// The operator's hold on the work the harness chooses for itself passes
	// through a conversation the same way. It lives in the product's own record
	// and is enforced from there, so these say only that the operator placed or
	// lifted it here — which is the thing that would otherwise be missing from an
	// account of a queue that suddenly stopped moving.
	EventIntakeHeld     EventType = "intake.held"
	EventIntakeReleased EventType = "intake.released"
	// A conversation's picture of the repository and the tracker is taken once
	// and can be taken again on the operator's instruction. The refresh is
	// recorded because it changes what the agent is reasoning from, which is
	// otherwise the one thing about a conversation its log would not say.
	EventContextRefreshed EventType = "context.refreshed"
	// How old that picture was, in landings on the target branch, measured
	// before each reply and recorded with what the harness did about it: nothing,
	// a refresh, or a reply that states its own age because the refresh could
	// not be made. It is recorded every reply rather than only when something was
	// done, because the age of the picture a reply was built from is what a
	// reader holding the reply against the repository needs, and a log that said
	// so only when the age was past a threshold would leave every other reply's
	// unstated.
	EventContextMeasured EventType = "context.measured"
	// A conversation's provider session set aside because the provider refused
	// it as too long to continue, or could not compact it, and the turn carried
	// on in a fresh session with its context rebuilt from the record. It names the
	// session that was set aside and what the provider said, because otherwise
	// the log would show a conversation whose context quietly shrank.
	EventSessionReplaced EventType = "session.replaced"
	// A directive the operator gave is recorded for the whole product rather than
	// for this conversation, and enforced from there. These say that it passed
	// through here: what was directed, and what settled it afterwards. Neither is
	// where the directive lives, which is exactly why the conversation's own log
	// has to say that the operator gave one.
	EventDirectiveRecorded EventType = "directive.recorded"
	EventDirectiveResolved EventType = "directive.resolved"
	// A directive the operator took back. It is separate from the two above
	// because it is the opposite fact from a settlement: what was directed was not
	// carried out or answered, it stopped being meant, and a log that recorded the
	// two the same way could not say which of them ended a standing instruction.
	EventDirectiveWithdrawn EventType = "directive.withdrawn"
	// An ask this conversation put to another role is recorded in this
	// conversation's own log as well as in the exchange itself, for the reason a
	// report is recorded in both places: the exchange holds the thread, and the
	// conversation has to say that its own reasoning went and asked somebody. The
	// round and its closing are separate events because a round that produced no
	// answer still happened and still spent the exchange's budget.
	EventExchangeRound  EventType = "exchange.round"
	EventExchangeClosed EventType = "exchange.closed"
	// A repository path the harness read or listed for a management conversation,
	// recorded as the commit it was read at, the path, and the time. It is in the
	// conversation's own log because it is what the role's advice was built from:
	// a reply that rests on a file is a reply somebody may later need to hold
	// against the commit the file was read at. One event per path, and a path
	// that was refused is recorded with the refusal rather than left out.
	EventRepositoryRead EventType = "repository.read"
	// A memory a management conversation recorded for its own agent, recorded as
	// what was asked for and what came of it, separately, for the reason a tracker
	// action is: a write that was refused is never readable as one that landed.
	// They carry the memory's name, the operation, and the revision the store
	// numbered it, and never its text: the memory store holds what the agent
	// knows, and a copy of it in the conversation record is the second store the
	// agent-memory design refuses.
	EventMemoryRequested EventType = "memory.requested"
	EventMemoryRecorded  EventType = "memory.recorded"
	EventMemoryFailed    EventType = "memory.failed"
	// A program manager's lane report, rewritten whole or refused whole. They
	// carry the turn, the pass that woke it where one did, the version the store
	// numbered it, and why one was refused, and never the report's text: the lane
	// report store holds what the report says, and a copy of it here would be a
	// second one nothing rewrites.
	EventLaneReportRecorded EventType = "lane_report.recorded"
	EventLaneReportRefused  EventType = "lane_report.refused"
	// A management conversation's provider session left behind because its next
	// turn would have taken it past the harness's byte budget, with the turn sent
	// on a new session rebuilt from the record instead — and the same compaction
	// when the rebuild could not be made, in which case the turn was not sent.
	// They carry the session's measured size, the budget, and why it was
	// compacted, so a reader can see how close the session came to the
	// provider's request limit.
	EventSessionCompacted        EventType = "session.compacted"
	EventSessionCompactionFailed EventType = "session.compaction_failed"
	// The role's opportunity to save memories on the old session, and what it
	// recorded before the session was rebuilt. No event copies memory text.
	EventSessionMemorySaveRequested EventType = "session.memory_save_requested"
	EventSessionMemorySaved         EventType = "session.memory_saved"
	EventSessionMemorySaveFailed    EventType = "session.memory_save_failed"
)

// MaxEventTextBytes bounds the text one recorded event carries — a command's
// output, a provider's result. What either side of a conversation said is the
// exception and is held to MaxReplyTextBytes instead, by the backend parsers and
// by the harness alike, because the two halves of an exchange held to two bounds
// is a record that cannot be read back as one exchange.
const MaxEventTextBytes = 16 << 10

// TruncateEventText cuts text to MaxEventTextBytes and marks the cut where it
// made one, so a reader is never shown a truncated record as a complete one. The
// cut falls on a rune boundary, so the record it keeps is still text.
func TruncateEventText(value string) string {
	if len(value) <= MaxEventTextBytes {
		return value
	}
	cut := MaxEventTextBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "…[truncated]"
}

type Event struct {
	SchemaVersion int             `json:"schema_version"`
	RunID         string          `json:"run_id"`
	Sequence      uint64          `json:"sequence"`
	Timestamp     time.Time       `json:"timestamp"`
	Type          EventType       `json:"type"`
	Source        string          `json:"source"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

func NewEvent(runID string, sequence uint64, timestamp time.Time, eventType EventType, source string, payload any) (Event, error) {
	event := Event{
		SchemaVersion: EventSchemaVersion,
		RunID:         runID,
		Sequence:      sequence,
		Timestamp:     timestamp.UTC(),
		Type:          eventType,
		Source:        source,
	}
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return Event{}, fmt.Errorf("encode event payload: %w", err)
		}
		event.Payload = encoded
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func DecodeEvent(data []byte) (Event, error) {
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		return Event{}, fmt.Errorf("decode event: %w", err)
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (e Event) Validate() error {
	var problems []error
	// Reading accepts every version this harness has ever written, because a run
	// log is appended to and never rewritten: refusing an older one would not
	// upgrade it, it would lose it, and what is in those logs is the only record
	// of what the harness has already done.
	if e.SchemaVersion < MinReadableEventSchemaVersion || e.SchemaVersion > EventSchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be between %d and %d", MinReadableEventSchemaVersion, EventSchemaVersion))
	}
	if e.RunID == "" {
		problems = append(problems, errors.New("run_id is required"))
	}
	if e.Sequence == 0 {
		problems = append(problems, errors.New("sequence must be greater than zero"))
	}
	if e.Timestamp.IsZero() {
		problems = append(problems, errors.New("timestamp is required"))
	}
	if e.Type == "" {
		problems = append(problems, errors.New("type is required"))
	}
	if e.Source == "" {
		problems = append(problems, errors.New("source is required"))
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid event: %w", errors.Join(problems...))
	}
	return nil
}

type Sequence struct {
	next uint64
}

func NewSequence(last uint64) *Sequence {
	return &Sequence{next: last + 1}
}

func (s *Sequence) Next() uint64 {
	value := s.next
	s.next++
	return value
}

func (s *Sequence) Last() uint64 {
	return s.next - 1
}
