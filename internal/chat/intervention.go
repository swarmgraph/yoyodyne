package chat

// The operator's hand steps, as a conversation records them (see
// internal/intervention).
//
// Two halves. The operator's own commands in a conversation — /work, /stop and
// /stop-everything, /redirect where it stops a run, /directive, and approving or
// declining what a role proposed or wrote — each write one event as the harness
// carries them out, through noteHandStep. Nothing a role does on its own account
// writes one: those are the system working, and counting them would make the
// measure say the operator did what the system did.
//
// And a role holding report.file — the program manager, whose remit may be the
// count itself — can write down a hand step it noticed the operator take outside
// the harness, with a block of its own. It is recorded as observed, under the
// role's name, and nothing else happens: the block changes no work and starts no
// round.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/intervention"
)

const interventionFence = "```yoyodyne-intervention"

// maxInterventionBlockBytes bounds the block: what was done at its limit and the
// handful of one-line fields beside it.
const maxInterventionBlockBytes = intervention.MaxSaidBytes + 4<<10

// Interventions is where the operator's hand steps are recorded. It is satisfied
// by *runstate.InterventionStore.
type Interventions interface {
	Record(event intervention.Event) error
}

// noteHandStep records one step the operator took through this conversation. A
// step the record would not take is still a step the operator took, so the
// command that took it carries on; what failed is said to the role on its next
// turn, where it is also kept in the conversation's record.
func (s *Session) noteHandStep(kind intervention.Kind, items []string, run, subject, said string) {
	if s.options.Interventions == nil {
		return
	}
	id, err := intervention.NewID()
	if err == nil {
		now := s.options.clock().Now().UTC()
		var named []string
		for _, item := range items {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				named = append(named, trimmed)
			}
		}
		err = s.options.Interventions.Record(intervention.Event{
			SchemaVersion: intervention.SchemaVersion,
			ID:            id,
			ProductID:     s.options.ProductID,
			Kind:          kind,
			At:            now,
			Items:         named,
			Run:           strings.TrimSpace(run),
			Subject:       strings.TrimSpace(subject),
			Said:          said,
			Via:           "conversation " + s.state.ConversationID,
			RecordedAt:    now,
		})
	}
	if err != nil {
		s.notice("the operator %s, and that hand step could not be recorded as one: %v", kind.Describe(), err)
	}
}

// ObservedAsk is one hand step a role noticed and asks to have recorded.
type ObservedAsk struct {
	Kind    string   `json:"kind"`
	By      string   `json:"by"`
	At      string   `json:"at,omitempty"`
	Items   []string `json:"items,omitempty"`
	Run     string   `json:"run,omitempty"`
	Subject string   `json:"subject,omitempty"`
	Said    string   `json:"said"`
}

// ObservedOutcome is what became of one ask: the event as recorded, or why
// nothing was.
type ObservedOutcome struct {
	Ask      ObservedAsk         `json:"ask"`
	Recorded bool                `json:"recorded"`
	Event    *intervention.Event `json:"event,omitempty"`
	Failure  string              `json:"failure,omitempty"`
}

// InterventionError reports an intervention block the harness could not read.
// Nothing in it was recorded, and nothing else about the turn is changed by it.
type InterventionError struct {
	Err error
}

func (e *InterventionError) Error() string {
	return "the reply carried an intervention block the harness cannot read: " + e.Err.Error()
}

func (e *InterventionError) Unwrap() error { return e.Err }

// extractIntervention takes the intervention block out of a reply, leaving its
// prose.
func extractIntervention(reply string) (string, *ObservedAsk, error) {
	prose, payload, found, err := splitFencedBlock(reply, interventionFence, "intervention")
	if err != nil {
		return "", nil, err
	}
	if !found {
		return strings.TrimSpace(reply), nil, nil
	}
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return "", nil, errors.New("decode intervention: the intervention block is empty")
	}
	if len(trimmed) > maxInterventionBlockBytes {
		return "", nil, fmt.Errorf("decode intervention: block is %d bytes, limit is %d", len(trimmed), maxInterventionBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var ask ObservedAsk
	if err := decoder.Decode(&ask); err != nil {
		return "", nil, fmt.Errorf("decode intervention: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return "", nil, errors.New("decode intervention: unexpected trailing content after the step")
	}
	return prose, &ask, nil
}

// performObserved records the step a reply asked to have recorded. A step the
// record refuses — an unknown kind, nobody named as having taken it, a time in
// the future — is an outcome the role is told, never a failed turn.
func (s *Session) performObserved(ask ObservedAsk) ObservedOutcome {
	outcome := ObservedOutcome{Ask: ask}
	if s.options.Interventions == nil {
		outcome.Failure = "no record of hand steps is wired to this conversation, so nothing was recorded"
		return outcome
	}
	now := s.options.clock().Now().UTC()
	at := now
	if when := strings.TrimSpace(ask.At); when != "" {
		parsed, err := time.Parse(time.RFC3339, when)
		if err != nil {
			outcome.Failure = fmt.Sprintf("%q is not a time; give one as 2026-10-05T09:30:00-07:00", when)
			return outcome
		}
		at = parsed.UTC()
	}
	id, err := intervention.NewID()
	if err != nil {
		outcome.Failure = singleLine(err.Error(), maxTrackerFailureBytes)
		return outcome
	}
	var items []string
	for _, item := range ask.Items {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	event := intervention.Event{
		SchemaVersion: intervention.SchemaVersion,
		ID:            id,
		ProductID:     s.options.ProductID,
		Kind:          intervention.Kind(strings.TrimSpace(ask.Kind)),
		At:            at,
		Items:         items,
		Run:           strings.TrimSpace(ask.Run),
		Subject:       strings.TrimSpace(ask.Subject),
		Said:          strings.TrimSpace(ask.Said),
		Observed:      true,
		By:            strings.TrimSpace(ask.By),
		RecordedBy:    s.options.Agent,
		RecordedRole:  s.state.Role,
		RecordedAt:    now,
	}
	if err := s.options.Interventions.Record(event); err != nil {
		outcome.Failure = singleLine(err.Error(), maxTrackerFailureBytes)
		return outcome
	}
	outcome.Recorded = true
	outcome.Event = &event
	return outcome
}

// renderObservedResult is what the role is told became of its block.
func renderObservedResult(outcome ObservedOutcome) string {
	var rendered strings.Builder
	rendered.WriteString("# Hand step\n\n")
	if outcome.Recorded {
		fmt.Fprintf(&rendered, "Recorded as %s: %s %s, observed and recorded by you. It is counted with the operator's other hand steps from now on, against the work items and run it names. Nothing else was done.\n\n",
			outcome.Event.ID, outcome.Event.By, outcome.Event.Kind.Describe())
		return rendered.String()
	}
	fmt.Fprintf(&rendered, "Not recorded: %s\n\n", outcome.Failure)
	return rendered.String()
}

// reportObserved tells the operator what the role wrote down about them.
func (s *Session) reportObserved(out io.Writer, reply Reply) {
	if reply.Observed == nil {
		return
	}
	title := RoleTitle(s.state.Role)
	outcome := *reply.Observed
	if outcome.Recorded {
		fmt.Fprintf(out, "the %s recorded a hand step it noticed: %s %s (%s)\n\n", title, outcome.Event.By, outcome.Event.Kind.Describe(), outcome.Event.ID)
		return
	}
	fmt.Fprintf(out, "the %s's record of a hand step was not kept: %s\n\n", title, outcome.Failure)
}

// interventionContract is what a role holding report.file is told about
// recording a hand step it noticed.
var interventionContract = `# Recording a hand step taken outside the harness

The operator's hand steps are counted: every one taken through the harness is recorded as it happens, and the count per merged change is read from that record. A step taken outside the harness — the scheduler restarted by hand, the target branch reset by hand, a tracker status edited by hand — is in the count only if somebody writes it down. Where you can see that one was taken and it is not already recorded, write it down by ending your reply with exactly one block, after the prose:

` + "```" + `yoyodyne-intervention
{"kind":"restart","by":"who took the step","at":"2026-10-05T09:30:00-07:00","items":["beads-id"],"run":"run-id","subject":"what else it touched","said":"what was done, in a sentence"}
` + "```" + `

"kind" is one of ` + strings.Join(intervention.KindNames(), ", ") + `; "by" and "said" are required; "at" is when it was taken and defaults to now; "items", "run", and "subject" name what it touched, and naming the work item or run it was for is what counts it against that change. It is recorded as observed, under your name, and does nothing else. Record only what you have evidence for, and never a step an agent or the harness took: the count is of what the operator did by hand.`
