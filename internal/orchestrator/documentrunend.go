package orchestrator

import (
	"context"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A document run whose process died before the run ended.
//
// A document run is driven by whichever process offered it: its owning
// conversation, or the scheduler starting a document that waited for a document
// landing. When that process dies — at every move onto a new build, among other
// times — the run's record is left saying it is still going, and nothing comes
// back to it unless its conversation happens to offer the same handoff again. A
// run left like that holds its document: the conversation reads it as still
// publishing, so every later revision of the document waits behind it, and
// nobody is told it stopped.
//
// So the sweep settles it. It finds such a run only under the run's lease,
// which a live process holds for as long as it drives the run, so a run being
// driven never reaches here. The run is ended as stopped by its process
// vanishing, which judged nothing about the document, so the next offer of the
// handoff runs the same text again as its next attempt (retryable) and tells the
// owning role why, and the work item the run is recorded against is told now.

// documentProcessGone reports a document run, found under its lease, that is
// still marked as going with nothing integrated, queued, or merging, and with
// no pause recorded: a run whose process died before the run ended. A run at
// its promotion is left to the attempt that continues it, which finds whether
// its commit reached the target (recoverDocumentIntegration), because ending it
// here could start a second copy of a document that has already landed. A run
// that recorded a pause and returned — the operator's hold, a usage limit, a
// directive, a tracker or forge wait, a provider the harness stopped on time —
// also releases its lease while still marked as going; it matches one of
// resumeShapes and is left for its owning conversation to continue, because
// ending it would override the operator's hold, or spend one of the document's
// attempts on a wait that judged nothing and was going to end by itself. The
// redeploy drain never stops a document run (holdDocumentForRestart), so a
// run whose process died at a move onto a new build records no pause.
func documentProcessGone(state runstate.State) bool {
	if state.Document == nil || state.Status.Terminal() || state.Integration != nil || state.MergeQueue != nil {
		return false
	}
	if state.PullRequest != nil && (state.PullRequest.MergeQueued || state.PullRequest.Merged) {
		return false
	}
	return state.Phase != runstate.PhaseIntegrating && !resumable(state)
}

// settleDocumentProcessGone ends a document run whose process died, says so on
// the work item the run is recorded against, and, for a retry the development
// manager decided — which no conversation holds to offer again — leaves the
// next attempt where the scheduler starts it.
func (r Reconciler) settleDocumentProcessGone(ctx context.Context, tracker WorkTracker, state runstate.State) (Reconciliation, error) {
	d := state.Document
	reason := fmt.Sprintf("the process publishing document %s (%s) stopped at the run's %s step before the run ended, and nothing judged the document", d.WriteID, d.Candidate.Artifact.Title, nonEmpty(string(state.Phase), "first"))
	settled, err := r.saveTerminalFailure(state, reason)
	if err != nil {
		return reconciliationOf(state, ActionUnsettled), err
	}
	next := fmt.Sprintf("The %s's conversation runs the same text again at its next message, as the next attempt, and is told why; nothing needs writing again, and nothing is counted against the document.", d.Owner.Title())
	switch attempt := settled.DocumentAttempt(); {
	case d.StandingApproval():
		next = triedAgain(*d)
	case attempt+1 >= runstate.MaxDocumentAttempts:
		next = fmt.Sprintf("It was the last of %d attempts, so the next offer of it hands it to the development manager with the confirmed text kept on this run's record.", runstate.MaxDocumentAttempts)
	case d.Retried != nil:
		next = fmt.Sprintf("It is a retry the development manager decided, so the harness starts attempt %d of %d itself as a document landing frees.", attempt+2, runstate.MaxDocumentAttempts)
		if waits, ok := r.Store.(DocumentWaits); ok {
			if _, _, err := waits.WaitForSlot(ctx, *d, attempt+1, r.clock().Now()); err != nil {
				result := reconciliationOf(settled, ActionFailed)
				result.Detail = reason
				return result, fmt.Errorf("record the next attempt at retried document %s for the scheduler: %w", d.WriteID, err)
			}
		}
	}
	recordedDocumentItem(tracker, settled).note(ctx, fmt.Sprintf("Yoyodyne's run %s stopped publishing document %s (%s) without judging it: %s. %s", settled.RunID, d.WriteID, d.Candidate.Artifact.Title, documentStopSays(settled), next))
	result := reconciliationOf(settled, ActionFailed)
	result.Detail = reason + ". " + next
	return result, nil
}
