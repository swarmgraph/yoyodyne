package chat

// A program manager's lane, enforced where every conversation authority is: a
// scope over the tracker actions it already holds, and no new mechanism.
//
// "The lane, and how it is enforced" in `docs/designs/program-manager.md` is the
// whole of the rule. A creation carries the lane label in the write that admits
// it, whether or not the reply named it. Every other action is refused unless the
// item carries the label at the moment of the act, read from the tracker as the
// action runs, so a listing that has moved does not widen the lane. The label is
// never removed by its owner. A lane item may be made to wait on anything, and
// nothing outside the lane may be made to wait on it. A reparenting needs the new
// parent in the lane as well. And an admission is an admission: it passes
// approvals.work_items exactly as the product manager's does, and where that
// gate would refuse a direct admission the creation is put to the operator as a
// proposal naming the lane instead.
//
// Two halves, because two kinds of thing are refused. What the block alone
// decides — an instance configured with no lane, the lane label named for
// removal, a directive carried out by a creation — is refused whole in the
// authority table before anything runs. What depends on the item is refused per
// action as it runs, because only the tracker can say what the item carries now.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// lane is the tracker label this conversation's instance owns, or empty for an
// agent that owns none — which is every agent but a program manager instance.
func (s *Session) lane() string {
	return strings.TrimSpace(s.options.Lane)
}

// laneScoped reports whether this conversation may ask for an action only inside
// its lane.
func (s *Session) laneScoped(action string) bool {
	return s.authority().LaneScoped(action)
}

// refuseOutsideLane refuses, whole and before anything runs, the lane-scoped
// actions the block alone shows cannot be carried out inside the lane.
func (s *Session) refuseOutsideLane(authority Authority, action TrackerAction) error {
	if !authority.LaneScoped(action.Action) {
		return nil
	}
	lane := s.lane()
	if lane == "" {
		return &AuthorityError{
			Role:    authority.Role,
			Refused: fmt.Sprintf("the %q tracker action outside its lane", action.Action),
			Reason:  "this role's tracker writes are scoped to its own lane, and this instance is configured with no lane, so no item is inside one; it may read and survey, and says in prose or in its report what it would change",
		}
	}
	if action.Action == actionLabel && strings.TrimSpace(action.Remove) == lane {
		return &AuthorityError{
			Role:    authority.Role,
			Refused: fmt.Sprintf("the lane label %q removed from an item", lane),
			Reason:  "the lane label is never removed by the lane's owner; taking an item out of a lane is the Lead Product Manager's or the development manager's act, so ask them",
		}
	}
	if action.Action == actionCreate && strings.TrimSpace(action.Directive) != "" {
		return &AuthorityError{
			Role:    authority.Role,
			Refused: "a creation carrying out a directive",
			Reason:  "directives, and what carries them out, are the operator's and the Lead Product Manager's; admit the work without the directive and say which one you think it answers",
		}
	}
	return nil
}

// laneRefusal says why a lane-scoped action is outside the lane at the moment it
// runs, and is empty where it is inside. It reads the tracker for what it needs
// that the action's own reading did not: the parent a creation or a reparenting
// names. The item itself was read by readActionTarget as the action began, and
// that reading is the one judged, so what the lane is is what the tracker says
// now rather than what a survey said earlier.
func (s *Session) laneRefusal(ctx context.Context, outcome *TrackerOutcome) string {
	action := outcome.Action
	if !s.laneScoped(action.Action) {
		return ""
	}
	lane := s.lane()
	if action.actsOnExistingItem() {
		id := strings.TrimSpace(action.ID)
		if refusal := outsideLane(lane, id, outcome.target, outcome.TargetUnread); refusal != "" {
			if action.Action == actionLink {
				// The one refusal worth its own sentence: a lane item may wait on
				// anything, and this is the other direction.
				refusal += "; a lane item may be linked to wait on any item, and an item outside the lane may not be made to wait on one"
			}
			return refusal
		}
	}
	switch action.Action {
	case actionCreate, actionReparent:
		parent := action.parent()
		if parent == "" {
			return ""
		}
		item, err := s.options.Tracker.Show(ctx, parent)
		unread := ""
		var read *beads.WorkItem
		if err != nil {
			unread = singleLine(err.Error(), maxTrackerFailureBytes)
		} else {
			read = &item
		}
		if refusal := outsideLane(lane, parent, read, unread); refusal != "" {
			return "the parent it names is outside the lane: " + refusal
		}
	}
	return ""
}

// outsideLane says why one item is not inside the lane, from one reading of it.
// An item the tracker would not describe is outside it: the scope is the one
// thing an action here is not attempted without, because attempting it is the
// widening the scope exists to refuse.
func outsideLane(lane, id string, item *beads.WorkItem, unread string) string {
	if item == nil {
		reason := unread
		if reason == "" {
			reason = "the tracker did not answer"
		}
		return fmt.Sprintf("the tracker would not say whether %s carries the lane label %q (%s), so it is treated as outside the lane and nothing was changed", id, lane, reason)
	}
	if slices.Contains(item.Labels, lane) {
		return ""
	}
	return fmt.Sprintf("%s does not carry the lane label %q as the tracker holds it now, so it is outside this program manager's lane and nothing was changed; ask the Lead Product Manager for anything outside it", id, lane)
}

// laneLabels is what a lane creation is labelled with: the lane label first,
// whether or not the reply named it, and any others it named beside it.
func laneLabels(lane string, named []string) []string {
	labels := []string{lane}
	for _, label := range trimmedLabels(named) {
		if !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}
	return labels
}

// laneNote is what a lane admission records about the lane and the instance that
// admitted into it, so the item says whose lane it is in long after the
// conversation that admitted it is gone.
func (s *Session) laneNote() string {
	return fmt.Sprintf("Lane: %s, admitted by the program manager instance %s.", s.lane(), strings.TrimSpace(s.options.Agent))
}

// proposeLaneCreation puts a lane creation the admission gate would refuse to
// the operator as a proposal naming the lane, which is what the product manager
// would have done with the same work. It is held to what a proposal is held to —
// the goal, the done-conditions, the placement, and what the work resembles —
// and it is recorded before the operator is shown it, like every proposal.
//
// A proposal carries the title, the description, the goal, the parent, the
// class, and the kind. The rest of a creation's arguments have nowhere to go on one, so they
// are named in the outcome rather than dropped quietly, for the instance to set
// once the operator has admitted the item into its lane.
func (s *Session) proposeLaneCreation(ctx context.Context, outcome *TrackerOutcome, gate string) {
	action := outcome.Action
	proposal := Proposal{
		Title:         strings.TrimSpace(action.Title),
		Description:   strings.TrimSpace(action.Description),
		Rationale:     strings.TrimSpace(action.Reason),
		Goal:          strings.TrimSpace(action.Goal),
		RelevantGoals: action.RelevantGoals,
		Parent:        action.parent(),
		Class:         action.Class,
		Kind:          action.kind(),
	}
	if err := proposal.Validate(); err != nil {
		outcome.fail(err)
		return
	}
	if err := s.verifyProposalConditions([]Proposal{proposal}); err != nil {
		outcome.fail(err)
		return
	}
	if err := s.verifyProposalReferences(ctx, []Proposal{proposal}); err != nil {
		outcome.fail(err)
		return
	}
	pending := PendingProposal{
		ID:             s.nextProposalID(),
		ConversationID: s.state.ConversationID,
		Turn:           s.state.Turns,
		Proposal:       proposal,
		Asking:         s.proposalGate(proposal, resemblanceAt(s.resemblingProposals(ctx, []Proposal{proposal}), 0)),
		Lane:           s.lane(),
		Asker:          s.admissionAsker(),
	}
	if err := s.emit(execution.EventProposalRecorded, pending); err != nil {
		outcome.fail(fmt.Errorf("record the proposal: %w", err))
		return
	}
	s.proposals = append(s.proposals, &proposalRecord{pending: pending})
	if err := s.record(); err != nil {
		outcome.fail(fmt.Errorf("record the proposal: %w", err))
		return
	}
	s.laneProposals = append(s.laneProposals, pending)
	outcome.applied("put to the operator as proposal %s in lane %s rather than admitted, because %s: %s%s",
		pending.ID, pending.Lane, gate, singleLine(proposal.Title, maxSurveyTitleBytes), uncarriedClause(action))
}

// uncarriedClause names the arguments of a creation a proposal cannot carry, and
// is empty for a creation that named none of them.
func uncarriedClause(action TrackerAction) string {
	var dropped []string
	for _, argument := range action.arguments() {
		switch argument {
		case "title", "description", "goal", "relevant_goals", "parent", "class", "kind":
		default:
			dropped = append(dropped, argument)
		}
	}
	if len(dropped) == 0 {
		return ""
	}
	return "; a proposal does not carry " + strings.Join(dropped, ", ") + ", so set those once the item is admitted"
}

// nextProposalID numbers a proposal made by a tracker action among the proposals
// this turn has already made, so it never takes an identifier one of them holds.
func (s *Session) nextProposalID() string {
	position := 0
	for _, record := range s.proposals {
		if record.pending.Turn != s.state.Turns {
			continue
		}
		var turn, at int
		if _, err := fmt.Sscanf(record.pending.ID, "%d.%d", &turn, &at); err == nil && at > position {
			position = at
		}
	}
	return fmt.Sprintf("%d.%d", s.state.Turns, position+1)
}

// takeLaneProposals hands over the proposals this round's tracker actions made,
// for the reply to put to the operator beside any other.
func (s *Session) takeLaneProposals() []PendingProposal {
	taken := s.laneProposals
	s.laneProposals = nil
	return taken
}

// laneTrackerClause is the program manager's tracker authority: the reads every
// role has, and the writes its lane bounds, stated with the rules the harness
// holds each of them to.
const laneTrackerClause = relevantGoalsClause + "\n\n" + workItemKindClause + "\n\n" + itemReadClause + "\n\n" + `The state you were given lists work items by title only, and it is a snapshot: it was gathered when this conversation opened and it does not move. Read an item before you act on it, and survey before you conclude anything about the queue. To act on the work tracker, end your reply with exactly one block, after the prose:

` + "```" + `yoyodyne-tracker
{"actions":[
  {"action":"read","id":"beads-id"},
  {"action":"survey"},
  {"action":"create","title":"one line","description":"what the work is and what done means","goal":"the goal this work serves","relevant_goals":["other goals the change must not break"],"parent":"beads-id","priority":2,"executor":"conversation:architect","parked":"why nothing should pull it yet","report":"report-id","labels":["bug"],"corrects":["the closed beads-id a standing-goal correction corrects"],"reason":"why you are admitting it"},
  {"action":"attribute","id":"beads-id","goal":"the goal it serves","reason":"why"},
  {"action":"update","id":"beads-id","title":"one line","description":"replacement text","note":"text appended to the item's notes","relevant_goals":["goals the change must not break"],"kind":"feature","executor":"conversation:architect","corrects":["a further closed beads-id this correction covers"],"reason":"why"},
  {"action":"label","id":"beads-id","add":"bug","reason":"why this item carries the label"},
  {"action":"label","id":"beads-id","remove":"bug","reason":"why it no longer does"},
  {"action":"reparent","id":"beads-id","parent":"beads-id","reason":"why"},
  {"action":"reprioritize","id":"beads-id","priority":1,"reason":"why"},
  {"action":"park","id":"beads-id","reason":"why nothing should pull it yet"},
  {"action":"unpark","id":"beads-id","reason":"why it may be pulled again"},
  {"action":"link","id":"beads-id","depends_on":"the item this one waits for","reason":"why"},
  {"action":"unlink","id":"beads-id","depends_on":"beads-id","reason":"why"}
]}
` + "```" + `

That example lists every action you have. There is no close and no retire: closing is the harness's when work lands, and withdrawing admitted scope is the Lead Product Manager's, so ask the Lead Product Manager. One block carries only the actions you want, at most ` + maxTrackerActionsPerTurnText + ` of them, and each takes only the arguments described here. "reason" is required on everything but "read" and "survey". "read" and "survey" reach the whole tracker; every other action is held to your lane, by these rules:

- A "create" is admitted into your lane: the harness puts your lane label on it in the same write, whether or not "labels" names it, and other labels may go beside it. Its notes record your lane and this instance. A parent it names must itself carry your lane label. It names no "directive": carrying a directive out is the Lead Product Manager's.
- Every other action is refused unless the item carries your lane label at the moment the action runs, read from the tracker as it runs. A listing or survey that showed the label earlier is not the item as it stands, so read before you act on anything you have not just seen.
- You never remove your own lane label: a "label" removing it is refused, and taking an item out of your lane is the Lead Product Manager's or the development manager's act.
- A "link" may make your lane item wait on any item, inside the lane or out. Making an item outside your lane wait on one of yours is refused.
- A "reparent" needs the item and its new parent both to carry your lane label.
- "priority" is yours to set freely inside your lane, and a "create" that names one is placed at it.
- Where your lane is a standing goal, a "create" naming in "corrects" the closed items whose landed work breaks it is a standing-goal correction: it is admitted at priority 0, its notes record each closed item it corrects, a correction naming an item an open correction already names is refused so you widen that one with an "update" naming the further closed items in "corrects", and one pass admits at most three.

A refused action changes nothing and its result says why; report it as refused, and ask the Lead Product Manager for anything outside your lane. The harness carries out your actions, records each one, tells the operator, and then tells you what each actually did; never describe any of it as done before you have been told that it was.`

// laneAdmissionClause is what a program manager is told about admitting into its
// lane, which is the same door the product manager's admission goes through and
// is stated in the same terms.
func laneAdmissionClause(admission Admission) string {
	if admission.PerItemApproval() {
		return `This project asks the operator about every work item before it is admitted, and your lane is no exception. A "create" is not refused for it: the harness puts it to the operator as a proposal naming your lane, and admits it into the lane only if they approve. Say that is what you did rather than describing the work as admitted.` +
			exemptionClause(admission)
	}
	return `This project admits work that traces to a goal the operator approved, without asking them again, and a "create" in your lane is admitted on that basis, exactly as the Lead Product Manager's own admission is. Work naming a goal that resolves to nothing is refused; work naming a goal whose document nobody approved, or one amended since it was approved, is put to the operator as a proposal naming your lane rather than admitted.`
}

// proposalLabels is what an approved proposal is created with: its lane label,
// where it was proposed in one, and nothing otherwise.
func proposalLabels(lane string) []string {
	if lane := strings.TrimSpace(lane); lane != "" {
		return []string{lane}
	}
	return nil
}
