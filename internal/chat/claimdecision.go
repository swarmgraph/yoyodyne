package chat

// The development manager's decision about a spent re-run claim.
//
// A re-run decision is carried out once, by a claim the harness takes against
// the stoppage before the fresh run starts. The harness gives a claim back by
// itself only where it can prove the fresh run bought nothing. Where it cannot —
// a fresh run that died at its tracker claim, or while its worktree was being
// prepared, with nothing classifying why — the docket entry of that fresh run
// says which claim it spent and what spending it cost, and the decision whether
// the claim goes back is hers: "give-back-claim" or "leave-claim-spent", naming
// the run the claim started (or, where none was recorded, the stopped run the
// claim was taken for).
//
// Neither is one of the item's triage decisions. The item's record holds one
// decision per run and the re-run decision this claim carried out is still the
// one standing about the stopped run, so this is written onto the claim itself
// (runstate.RerunStore.DecideClaim). Recording it gives nothing back: the
// harness carries a give-back out at the watching session's next pull, or by
// `yoyo triage give-back`, and the claim's record then says who gave it back,
// on her decision, and why.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	// decisionGiveBackClaim gives a spent re-run claim back, so the re-run she
	// decided about the stopped run is carried out again.
	decisionGiveBackClaim = "give-back-claim"
	// decisionLeaveClaimSpent leaves a spent re-run claim spent, and records that
	// she looked: a further re-run of the item needs a further decision.
	decisionLeaveClaimSpent = "leave-claim-spent"
)

// RerunClaims is where a decision about a spent re-run claim is found and
// recorded. It is satisfied by *runstate.RerunStore, and is wired into the
// development manager's conversation and no other.
type RerunClaims interface {
	SpentOn(runID string) (runstate.Rerun, bool, error)
	DecideClaim(ctx context.Context, docketKey string, decision runstate.RerunClaimDecision) (runstate.Rerun, error)
}

// decidesClaim reports an action deciding about a spent re-run claim.
func decidesClaim(decision string) bool {
	return decision == decisionGiveBackClaim || decision == decisionLeaveClaimSpent
}

// carryOutClaimDecision records her decision about the spent claim named by
// run onto the claim, notes it on the item, and, for a give-back, closes the
// fresh run's docket entries: what the claim bought is the question that entry
// put to her, and giving it back answers it by running the stopped work again.
func (s *Session) carryOutClaimDecision(ctx context.Context, outcome *TrackerOutcome, workItemID, run, decision string) {
	action := outcome.Action
	if s.options.Claims == nil {
		outcome.fail(errors.New("no record of re-run claims is wired to this conversation, so nothing was recorded about the claim"))
		return
	}
	claim, found, err := s.options.Claims.SpentOn(run)
	if err != nil {
		outcome.fail(fmt.Errorf("read the re-run claims: %w", err))
		return
	}
	if !found {
		outcome.refused(fmt.Errorf("no spent re-run claim names run %s, as the fresh run it started or as the stopped run it was taken for, so there is no claim to decide about; one already given back is not spent", run))
		return
	}
	if claim.WorkItemID != workItemID {
		outcome.refused(fmt.Errorf("the re-run claim spent on run %s belongs to %s rather than %s; name the item the claim was taken for", run, claim.WorkItemID, workItemID))
		return
	}
	word := runstate.RerunClaimLeaveSpent
	if decision == decisionGiveBackClaim {
		word = runstate.RerunClaimGiveBack
	}
	decided, err := s.options.Claims.DecideClaim(ctx, claim.DocketKey, runstate.RerunClaimDecision{
		Decision:     word,
		Reason:       action.Reason,
		DecidedBy:    RoleTitle(s.state.Role),
		Conversation: s.state.ConversationID,
		Turn:         s.state.Turns,
	})
	if err != nil {
		outcome.refused(err)
		return
	}
	spentOn := "no fresh run"
	if decided.RunID != "" {
		spentOn = "run " + decided.RunID
	}
	landed := fmt.Sprintf("decided %q about the re-run claim of the stoppage of run %s, spent on %s", word, decided.PriorRunID, spentOn)
	if word == runstate.RerunClaimGiveBack {
		landed += "; the harness gives it back at its next pull, and the re-run then stands to be carried out again"
	}
	outcome.noteLanded("%s", landed)
	note := s.trackerProvenance(triageVerbs[decision]+", the re-run claim of the stoppage of run "+decided.PriorRunID+" spent on "+spentOn, action.Reason)
	if _, err := s.options.Tracker.Update(ctx, workItemID, beads.WorkItemChange{AppendNotes: note}); err != nil {
		outcome.fail(err)
		s.settleTrackerNote(ctx, outcome, workItemID, note, "the decision recorded on the item")
		return
	}
	closed := ""
	if word == runstate.RerunClaimGiveBack && strings.TrimSpace(decided.RunID) == run {
		closed = s.closeDocketEntry(ctx, decision, run, action.Reason)
	}
	outcome.applied("triaged %s as %q, about the re-run claim spent on %s%s", workItemID, decision, spentOn, closed)
}
