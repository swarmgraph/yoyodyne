// Package costevent is the one reader of the events in an event log that say
// what a provider invocation cost, and the one rule for when a total read from
// them is complete.
//
// Every reader that adds up what a log spent -- the price of a run and of a
// branch review, and the spend report over every stream -- reads through
// Reader, so they cannot disagree about which lines are cost evidence or about
// what a line that will not decode means. Before they shared it, the price of a
// run called such a line a reason the whole run was unknown, while the spend
// report skipped it without a word; the same log was a missing price on one
// surface and a complete total on another.
//
// The rule is this. A line that may carry a cost (MayCarry) and decodes is
// evidence and is handed back. One that may carry a cost and will not decode is
// evidence that is missing: it is counted, the invocations around it are still
// read, and the total they make is marked incomplete, saying what is missing.
// Nothing that could be a cost is skipped silently, and one unreadable line
// never costs the reader the lines it can read.
//
// It sits under internal/cost rather than in it because the run store reads it,
// and internal/cost reads the run store.
package costevent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Terminals are the events that carry what one provider invocation cost. Both
// end a single invocation and both carry the cost, so an invocation that ended
// in an error is priced with the money that error spent rather than as though
// it had been free.
var Terminals = []execution.EventType{execution.EventRunCompleted, execution.EventRunFailed}

// evidenceEvents are every event a price is read from: the terminals above, and
// the review that brackets one of them in a log recorded before a terminal
// named its own role.
var evidenceEvents = append([]execution.EventType{execution.EventReviewStarted}, Terminals...)

// MayCarry is the cheap test that decides whether a line is worth decoding. It
// over-matches deliberately: what it must never do is pass over a line that
// carries a cost or opens a review, and the decoded type rejects everything
// else. A duplicate terminal carries a cost under an event type everything else
// is recorded under too, so it is matched by its anomaly instead.
func MayCarry(line []byte) bool {
	for _, candidate := range evidenceEvents {
		if bytes.Contains(line, []byte(`"`+string(candidate)+`"`)) {
			return true
		}
	}
	return bytes.Contains(line, []byte(`"`+execution.DuplicateTerminalAnomaly+`"`))
}

// Event is the little of an event a price needs: which event it is, whose
// invocation it ended, what the provider said that invocation cost and read to
// serve it, and the schema it was written at -- which is what says whether a
// missing role is an omission or a field that did not exist yet.
type Event struct {
	SchemaVersion int                 `json:"schema_version"`
	Type          execution.EventType `json:"type"`
	// Timestamp is when the invocation ended, which is the day its money is
	// counted on by the readers that group spend by day.
	Timestamp time.Time `json:"timestamp"`
	Payload   Payload   `json:"payload"`
}

// Payload is the part of a terminal's payload a price is read from.
type Payload struct {
	Role string `json:"role"`
	// SessionID is the provider session the invocation ran in, and it is what
	// says whether TotalCostUSD beside it is this invocation's cost or the
	// session's running total: a provider asked to resume reports the latter.
	// A terminal that named no session is priced at what it reported, which is
	// what an invocation with no session to have resumed cost.
	SessionID    string  `json:"session_id"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	CostReported *bool   `json:"cost_reported"`
	// Usage is the provider's own usage object, recorded verbatim on every
	// terminal. It is a pointer so that a terminal carrying no usage at all is
	// distinguishable from one that reported zeros: the first is an invocation
	// nobody has a measurement for, the second is a measurement of nothing, and
	// a share that treated them alike would be wrong by the first.
	Usage *Usage `json:"usage"`
	// Anomaly marks the process output a backend records for a terminal that
	// arrived after the invocation's first, which carries a cost of its own.
	Anomaly string `json:"anomaly"`
}

// UnmarshalJSON reads an event, and works out for an event recorded before the
// backends said so whether the provider reported a cost: it did where the
// payload carries a figure at all.
func (e *Event) UnmarshalJSON(data []byte) error {
	type plain Event
	if err := json.Unmarshal(data, (*plain)(e)); err != nil {
		return err
	}
	var presence struct {
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return err
	}
	if e.Payload.CostReported == nil {
		raw, exists := presence.Payload["total_cost_usd"]
		reported := exists && string(raw) != "null"
		e.Payload.CostReported = &reported
	}
	return nil
}

// Terminal reports an event that ended one invocation and carries its cost.
func (e Event) Terminal() bool {
	for _, candidate := range Terminals {
		if e.Type == candidate {
			return true
		}
	}
	return false
}

// DuplicateTerminal reports the record of a second terminal to one invocation.
// It is not a terminal, because it is not an invocation, but the provider
// charged for the turn behind it and the readers add that to the invocation it
// followed.
func (e Event) DuplicateTerminal() bool {
	return e.Type == execution.EventProcessOutput && e.Payload.Anomaly == execution.DuplicateTerminalAnomaly
}

// Session is the session a duplicate terminal's figure is a running total of.
// It names its own from the time the backend recorded one on it; before that
// it is the session of the terminal it followed, since both ended the same
// invocation.
func (e Event) Session(previous string) string {
	if session := strings.TrimSpace(e.Payload.SessionID); session != "" {
		return session
	}
	return previous
}

// NoCost reports a terminal whose provider said nothing about what the
// invocation cost. It reads the backend's explicit flag; an event recorded
// before the flag keeps the meaning UnmarshalJSON gave it.
func (e Event) NoCost() bool {
	return e.Payload.CostReported != nil && !*e.Payload.CostReported
}

// Role is whose invocation this terminal ended. A terminal names its own role
// from execution.TerminalRoleSchemaVersion on, and one recorded before that is
// read the way it was written, with the review bracket -- announced, whether a
// review opened one in front of it -- deciding between the only two roles that
// ever wrote into a run's log. A terminal that could have named itself and did
// not is nobody's, which is what the empty role says.
func (e Event) Role(announced bool) domain.AgentRole {
	named := domain.AgentRole(strings.TrimSpace(e.Payload.Role))
	if named != "" || e.SchemaVersion >= execution.TerminalRoleSchemaVersion {
		return named
	}
	if announced {
		return domain.RoleReviewer
	}
	return domain.RoleDeveloper
}

// Usage is the part of the provider's usage object a share is computed from.
// The field names are the provider's rather than the harness's, because this
// decodes what the provider wrote: the backend records the object verbatim
// under "usage" on the terminal's payload, and everything beside these -- the
// per-iteration array, the service tier -- is left where it is rather than
// descended into.
//
// That this is where the writer actually puts it is not something this file can
// state on its own, and a fixture written on this side would agree with itself
// whatever the writer did. TestATerminalCarriesTheProvidersUsageWhereThePriceReaderLooksForIt
// in internal/backend/claudecode is what holds the two together, from the side
// that writes.
type Usage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
	// CacheCreation is the one nested object that is descended into: the
	// provider's split of the write above by the lifetime it was written at,
	// which is what a cost split needs because the two lifetimes carry different
	// premiums. It is a pointer so a terminal recorded before the provider
	// reported the split is told apart from one that reported a split of nothing.
	CacheCreation *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// DecodeUsage reads a usage object recorded on its own, as the cost log and an
// exchange's rounds keep one. It reports false for one that is absent or will
// not decode, which is an invocation nobody has a measurement for.
func DecodeUsage(raw json.RawMessage) (Usage, bool) {
	var usage Usage
	if len(raw) == 0 || json.Unmarshal(raw, &usage) != nil {
		return Usage{}, false
	}
	return usage, true
}

// Reader reads the cost evidence out of one event log, a line at a time, and
// keeps the count of evidence it could not read. One Reader is one log: the
// count is what says whether the total read from that log is complete.
type Reader struct {
	missing int
	first   string
}

// Read decodes one line of an event log. It reports false for a line that is
// not cost evidence, and for one that may be and will not decode -- which it
// counts as missing evidence rather than passing over.
func (r *Reader) Read(line []byte) (Event, bool) {
	if !MayCarry(line) {
		return Event{}, false
	}
	var event Event
	if err := json.Unmarshal(line, &event); err != nil {
		r.missing++
		if r.first == "" {
			r.first = err.Error()
		}
		return Event{}, false
	}
	return event, true
}

// Complete reports a log whose every line of cost evidence could be read, so
// the total read from it is the whole of what it recorded.
func (r Reader) Complete() bool { return r.missing == 0 }

// Unreadable is how many lines that may carry a cost could not be read.
func (r Reader) Unreadable() int { return r.missing }

// Missing says what an incomplete total is missing, in words a person reads,
// and is empty for a complete one. The figure beside it covers the lines that
// could be read, so it is at least what the log recorded and may be less than
// what was spent.
func (r Reader) Missing() string {
	if r.missing == 0 {
		return ""
	}
	lines := "lines"
	if r.missing == 1 {
		lines = "line"
	}
	return fmt.Sprintf("%d %s of the event log that may say what an invocation cost could not be read (%s), so the cost shown is at least what was spent rather than all of it", r.missing, lines, r.first)
}
