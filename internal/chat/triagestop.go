package chat

// The development manager stopping a run in flight.
//
// Until this existed, a run whose work she had decided was superseded — the one
// on yoyodyne-ifd.428.34 when yoyodyne-ifd.398 took its place — kept its
// developer slot and its review rounds until it ended on its own, because the
// only verb that could stop it was the operator's. A stop only a person can
// perform is a defect by the operator's rule of 2026-09-26, so the stop is a
// triage decision like the rest: recorded on the item's durable triage record
// with her reasoning, and carried out by the harness as it is recorded.
//
// Carrying it out is the operator's stop, made on her behalf. The harness writes
// the same request beside the run that `/stop` writes, naming her as the one who
// asked and the decision it carries out, and the run honors it at its next
// step exactly as it honors the operator's (or keeps a change whose checks
// already passed for its review, internal/orchestrator/promptstop.go): it ends itself
// cancelled, its branch and worktree are left where they are, and its developer
// slot is free the moment its record is terminal. What differs is only what it
// leaves behind — the run's record and the item's notes say she stopped it and
// why, and the stoppage is docketed already settled by her decision rather than
// put back to her as a question.
//
// Nothing else can ask. The action is a triage decision, which only a role
// holding the triage capability may record, and the hand that writes the request
// is wired into the development manager's conversation and no other: a developer
// or a reviewer has no tracker actions at all, and a process outside her
// conversation has no way to name her as the one who asked.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DecidedStops is how a stop the development manager decided reaches the run it
// is about. It is optional like the rest, and a conversation without one refuses
// a stop rather than recording a decision nothing will carry out.
type DecidedStops interface {
	// Stoppable refuses a run that is not in flight, saying what became of it. A
	// run that has already ended has a stoppage to decide about rather than a run
	// to stop, and a stop recorded against it would stop nothing.
	Stoppable(ctx context.Context, runID string) error
	// Stop asks the run to stop at its next boundary, on behalf of whoever the
	// stop names.
	Stop(ctx context.Context, stop DecidedStop) error
}

// DecidedStop is one stop the development manager decided, as the harness asks
// the run for it.
type DecidedStop struct {
	RunID      string
	WorkItemID string
	// Reason is her reasoning, with the superseding item named where there is one.
	Reason string
	// RequestedBy is who decided it, in the words the item's notes and the docket
	// attribute it with.
	RequestedBy string
}

// carryOutStop asks the run a stop names to stop, and records the decision.
//
// The run is asked about first, because a stop recorded against a run that has
// already ended would read as a decided stoppage the run never had.
//
// The request is written before the decision, which is the opposite of the order
// the other decisions keep, and for the reason that order exists: whichever write
// fails must leave the record saying no more than happened. A decision recorded
// and then not carried out would stand on the item's triage record against a run
// that goes on, as the item's latest decision, for a stop that never happened.
// (Whatever stoppage that run later reached is still read as undecided: a stop
// decides only the stoppage it causes.) Nor can it be taken back afterwards, because recording it
// supersedes whatever stood about the run before — a repair grant being
// re-entered, with the rounds it reserved released — and removing the stop would
// not put that back. The request does not have that problem in either
// direction: a request that could not be written leaves nothing recorded and the
// same decision can simply be recorded again, and a request that was written
// carries her decision to the docket by itself, so a decision the triage record
// then refused still reaches the stoppage as decided.
func (s *Session) carryOutStop(ctx context.Context, outcome *TrackerOutcome, workItemID, runID string) {
	action := outcome.Action
	if s.options.Stops == nil {
		outcome.fail(errors.New("no way to stop a run is wired to this conversation, so nothing was recorded and the run goes on"))
		return
	}
	if err := s.options.Stops.Stoppable(ctx, runID); err != nil {
		outcome.refused(err)
		return
	}
	superseded := strings.TrimSpace(action.SupersededBy)
	reason := stopDecisionReason(action.Reason, superseded)
	if err := s.options.Stops.Stop(ctx, DecidedStop{
		RunID:      runID,
		WorkItemID: workItemID,
		Reason:     reason,
		// The same words closeDocketEntry attributes a decision with, so the
		// run's record, the docket, and the item's notes name one answerable thing.
		RequestedBy: fmt.Sprintf("the %s in conversation %s", RoleTitle(s.state.Role), s.state.ConversationID),
	}); err != nil {
		outcome.fail(fmt.Errorf("run %s on %s could not be asked to stop, so nothing was recorded and the run goes on; recording the same decision again asks it again: %w",
			runID, workItemID, err))
		return
	}
	outcome.noteLanded("run %s is asked to stop, and stops at its next boundary with its change preserved; the request carries this decision to the docket", runID)
	if _, err := s.recordTriageDecision(ctx, workItemID, runstate.TriageDecision{
		Decision:     decisionStop,
		RunID:        runID,
		Reason:       action.Reason,
		SupersededBy: superseded,
		DecidedBy:    RoleTitle(s.state.Role),
		Conversation: s.state.ConversationID,
		Turn:         s.state.Turns,
	}); err != nil {
		outcome.fail(fmt.Errorf("the stop was not recorded on %s's triage record, though run %s is asked to stop; recording the same decision again while the run is still in flight records it: %w",
			workItemID, runID, err))
		return
	}
	note := s.trackerProvenance(triageVerbs[decisionStop]+", run "+runID, reason)
	if _, err := s.options.Tracker.Update(ctx, workItemID, beads.WorkItemChange{AppendNotes: note}); err != nil {
		outcome.fail(err)
		s.settleTrackerNote(ctx, outcome, workItemID, note, "the stop recorded on the item")
		return
	}
	outcome.applied("asked run %s on %s to stop; it stops at its next boundary with its branch and worktree preserved, frees its developer slot as it ends, and is docketed as a stoppage you have already decided%s",
		runID, workItemID, s.closeDocketEntry(ctx, decisionStop, runID, action.Reason))
}

// carryOutProceed records that a run in flight is to finish, which is the other
// answer to the Lead Product Manager's decision about its item. Nothing is asked
// of the run: it goes on exactly as it would have, and what the decision leaves
// is the record that she looked and chose not to stop it — on the item's triage
// record, in its notes, and as the closure of the docket entry that put the
// question to her.
//
// The run has to be in flight, for the reason a stop's has: letting a run that
// has already ended finish would record a decision about a run that no longer
// exists, over whatever stoppage it left.
func (s *Session) carryOutProceed(ctx context.Context, outcome *TrackerOutcome, workItemID, runID string) {
	action := outcome.Action
	if s.options.Stops == nil {
		outcome.fail(errors.New("no way to read whether a run is in flight is wired to this conversation, so nothing was recorded"))
		return
	}
	if err := s.options.Stops.Stoppable(ctx, runID); err != nil {
		outcome.refused(err)
		return
	}
	if _, err := s.recordTriageDecision(ctx, workItemID, runstate.TriageDecision{
		Decision:     decisionProceed,
		RunID:        runID,
		Reason:       action.Reason,
		DecidedBy:    RoleTitle(s.state.Role),
		Conversation: s.state.ConversationID,
		Turn:         s.state.Turns,
	}); err != nil {
		outcome.fail(fmt.Errorf("that run %s on %s is to finish was not recorded on the item's triage record, and the run goes on either way: %w", runID, workItemID, err))
		return
	}
	note := s.trackerProvenance(triageVerbs[decisionProceed]+", run "+runID, action.Reason)
	if _, err := s.options.Tracker.Update(ctx, workItemID, beads.WorkItemChange{AppendNotes: note}); err != nil {
		outcome.fail(err)
		s.settleTrackerNote(ctx, outcome, workItemID, note, "the decision recorded on the item")
		return
	}
	outcome.applied("recorded that run %s on %s is to finish; nothing is asked of it, and it is reviewed and promoted as it would have been%s",
		runID, workItemID, s.closeDocketEntry(ctx, decisionProceed, runID, action.Reason))
}

// stopDecisionReason is the reasoning a stop carries to the run, with the item
// doing the work instead named where there is one.
func stopDecisionReason(reason, superseded string) string {
	stated := strings.TrimSpace(reason)
	if superseded == "" {
		return stated
	}
	return stated + " (superseded by " + superseded + ")"
}
