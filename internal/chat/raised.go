package chat

// Items the operator raised on purpose (docs/designs/unmet-goal-review.md,
// section 6). The Lead Product Manager records one, and its ending, with the
// "operator_raised" action, quoting his words from a message the record shows
// he sent himself. The harness checks the quote, then puts the
// raised-by-operator label on the item, or takes it off, in the same tracker
// write as a note in the fixed form domain.OperatorRaising renders. No other
// action writes that label (errRaisedLabelElsewhere), and no reordering moves
// other work ahead of an item carrying it (raisedOrderRefusal).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The two decisions "operator_raised" takes.
const (
	raisedRecord = "record"
	raisedEnd    = "end"
)

// maxRaisedWordsBytes bounds the quote a raising keeps: enough for what he
// said about the item, short enough to be one line of every view of it.
const maxRaisedWordsBytes = 1 << 10

// maxRaisedMessagesListed is how many of his recent messages a refusal lists,
// so the role can quote the right one at the next try.
const maxRaisedMessagesListed = 5

var errRaisedLabelElsewhere = errors.New(`the label "` + domain.RaisedByOperatorLabel +
	`" is put on and taken off only by "operator_raised", which quotes the operator's own words; no other action may write it`)

// operatorRaisedProblems checks what one action can be checked for alone.
// Whether the turn is his and holds the words needs the record, so that is
// asked as the action is carried out.
func (a TrackerAction) operatorRaisedProblems() []error {
	var problems []error
	switch decision := strings.TrimSpace(a.Decision); decision {
	case raisedRecord, raisedEnd:
	case "":
		problems = append(problems, fmt.Errorf("operator_raised requires \"decision\", %q or %q", raisedRecord, raisedEnd))
	default:
		problems = append(problems, fmt.Errorf("operator_raised decision %q is not one; the decisions are %q and %q", decision, raisedRecord, raisedEnd))
	}
	switch {
	case a.Turn == nil:
		problems = append(problems, errors.New("operator_raised requires \"turn\", the turn the operator's message came after"))
	case *a.Turn < 0:
		problems = append(problems, errors.New("turn must be zero or more"))
	}
	problems = append(problems, boundTrackerText("words", a.Words, maxRaisedWordsBytes, true))
	return problems
}

// operatorMessage is one message the record holds as the operator's.
type operatorMessage struct {
	afterTurn int
	via       string
	text      string
	at        execution.Event
}

// operatorMessages reads every message this conversation's record holds as
// sent by a person, oldest first. A message recorded before the record named
// its sender is left out: nothing says it was his.
func (s *Session) operatorMessages() ([]operatorMessage, error) {
	events, err := s.options.Store.LoadEvents(s.state.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("read this conversation's record: %w", err)
	}
	var messages []operatorMessage
	for _, event := range events {
		if event.Type != execution.EventOperatorMessage || len(event.Payload) == 0 {
			continue
		}
		var payload struct {
			Text       string `json:"text"`
			AfterTurn  *int   `json:"after_turn"`
			FromPerson string `json:"from_person"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil || payload.AfterTurn == nil || strings.TrimSpace(payload.FromPerson) == "" {
			continue
		}
		messages = append(messages, operatorMessage{afterTurn: *payload.AfterTurn, via: payload.FromPerson, text: payload.Text, at: event})
	}
	return messages, nil
}

// quotedMessage finds his message after the turn named that holds the words,
// or says why none does, listing his recent messages so the next try can
// name the right one.
func (s *Session) quotedMessage(turn int, words string) (operatorMessage, string, error) {
	messages, err := s.operatorMessages()
	if err != nil {
		return operatorMessage{}, "", err
	}
	quote := oneLineWords(words)
	var atTurn []operatorMessage
	for _, message := range messages {
		if message.afterTurn != turn {
			continue
		}
		if strings.Contains(oneLineWords(message.text), quote) {
			return message, "", nil
		}
		atTurn = append(atTurn, message)
	}
	var refusal strings.Builder
	if len(atTurn) == 0 {
		fmt.Fprintf(&refusal, "the record of conversation %s holds no message the operator sent himself after turn %d, so nothing was recorded; a raising is recorded only from his own words", s.state.ConversationID, turn)
	} else {
		fmt.Fprintf(&refusal, "the operator's message after turn %d does not contain those words, so nothing was recorded; quote them exactly as he wrote them. It reads: %q", turn, singleLine(atTurn[0].text, 300))
	}
	if len(messages) > 0 {
		refusal.WriteString(". His most recent messages here are:")
		recent := messages[max(0, len(messages)-maxRaisedMessagesListed):]
		for _, message := range recent {
			fmt.Fprintf(&refusal, " after turn %d, %q;", message.afterTurn, singleLine(message.text, 80))
		}
	}
	return operatorMessage{}, strings.TrimSuffix(refusal.String(), ";"), nil
}

func oneLineWords(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// recordOperatorRaising carries out "operator_raised": the label and its note
// in one write, once the quoted turn is found to be his and to hold the words.
func (s *Session) recordOperatorRaising(ctx context.Context, outcome *TrackerOutcome) {
	action := outcome.Action
	id := outcome.WorkItemID
	if outcome.target == nil {
		outcome.fail(fmt.Errorf("cannot record a raising on %s without reading it: %s", id, outcome.TargetUnread))
		return
	}
	target := *outcome.target
	ending := strings.TrimSpace(action.Decision) == raisedEnd
	raised := domain.RaisedByOperator(target.Labels)
	switch {
	case !ending && raised:
		outcome.Failure = fmt.Sprintf("%s already carries %s, so it is already recorded as raised by the operator; nothing was changed", id, domain.RaisedByOperatorLabel)
		return
	case !ending && target.Status == closedWorkItemStatus:
		outcome.Failure = fmt.Sprintf("%s is closed and has left the backlog, so there is nothing for it to stand ahead of; nothing was recorded", id)
		return
	case ending && !raised:
		outcome.Failure = fmt.Sprintf("%s does not carry %s, so there is no raising to end; nothing was changed", id, domain.RaisedByOperatorLabel)
		return
	}
	message, refusal, err := s.quotedMessage(*action.Turn, action.Words)
	if err != nil {
		outcome.fail(err)
		return
	}
	if refusal != "" {
		outcome.Failure = refusal
		return
	}
	admitted, err := s.admittedWork(ctx)
	if err != nil {
		outcome.fail(fmt.Errorf("cannot read the queue to say what %s stands ahead of: %w", id, err))
		return
	}
	note := domain.OperatorRaising{
		Ended:        ending,
		Words:        action.Words,
		Conversation: s.state.ConversationID,
		AfterTurn:    message.afterTurn,
		At:           message.at.Timestamp,
	}
	for _, item := range admitted {
		if item.ID == target.ID || !backlog.Before(target, item) {
			continue
		}
		if len(note.Above) < domain.MaxRaisedAboveListed {
			note.Above = append(note.Above, item.ID)
		} else {
			note.AboveMore++
		}
	}
	change := beads.WorkItemChange{AppendNotes: note.Render(nil) + "\n\n" + s.trackerProvenance("Recorded", action.Reason)}
	if ending {
		change.RemoveLabels = []string{domain.RaisedByOperatorLabel}
	} else {
		change.AddLabels = []string{domain.RaisedByOperatorLabel}
	}
	if _, err := s.options.Tracker.Update(ctx, id, change); err != nil {
		outcome.fail(err)
		return
	}
	ahead := strconv.Itoa(len(note.Above)+note.AboveMore) + " item(s)"
	if ending {
		outcome.applied("ended the operator's raising of %s from his words after turn %d; the label %s is off it and it goes back to the ordinary order, where it had stood ahead of %s", id, message.afterTurn, domain.RaisedByOperatorLabel, ahead)
		return
	}
	outcome.applied("recorded %s as raised by the operator from his words after turn %d; it carries %s and stands ahead of %s, and no reordering moves work ahead of it until he ends it", id, message.afterTurn, domain.RaisedByOperatorLabel, ahead)
}

// raisedOrderRefusal says why a reordering of one item would move work ahead
// of an item the operator raised, or says nothing when it would not. before is
// the item as the tracker holds it and after is the item as the reordering
// would leave it. Moving a raised item itself back is refused for the same
// reason as moving other work past it: either way, work goes ahead of it.
//
// A queue that cannot be read refuses the reordering, because a raising the
// check could not see is exactly the one it would pass.
func (s *Session) raisedOrderRefusal(ctx context.Context, before, after beads.WorkItem) string {
	admitted, err := s.admittedWork(ctx)
	if err != nil {
		return singleLine(fmt.Sprintf("cannot read the queue to check this does not move work ahead of an item the operator raised, so nothing was changed: %v", err), maxTrackerFailureBytes)
	}
	var passed []string
	raisedItself := domain.RaisedByOperator(before.Labels)
	for _, other := range admitted {
		if other.ID == before.ID {
			continue
		}
		switch {
		case raisedItself && backlog.Before(before, other) && !backlog.Before(after, other):
			passed = append(passed, other.ID)
		case !raisedItself && domain.RaisedByOperator(other.Labels) && !backlog.Before(before, other) && backlog.Before(after, other):
			passed = append(passed, other.ID)
		}
	}
	if len(passed) == 0 {
		return ""
	}
	const remedy = `Items he raised stay where he put them. Once he says the raising is over, record that with "operator_raised", decision "end", quoting his words, and reorder after that`
	if raisedItself {
		return singleLine(fmt.Sprintf("%s was raised by the operator on purpose, and this would put %s ahead of it, so nothing was changed. %s",
			before.ID, listed(passed), remedy), maxTrackerRefusalBytes)
	}
	return singleLine(fmt.Sprintf("this would put %s ahead of %s, which the operator raised on purpose, so nothing was changed. %s",
		before.ID, listed(passed), remedy), maxTrackerRefusalBytes)
}

// listed names up to five items and counts the rest.
func listed(ids []string) string {
	const shown = 5
	if len(ids) <= shown {
		return strings.Join(ids, ", ")
	}
	return strings.Join(ids[:shown], ", ") + fmt.Sprintf(" and %d more", len(ids)-shown)
}
