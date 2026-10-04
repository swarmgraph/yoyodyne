package readmodel

// What is awaiting the forge, answered once.
//
// A promotion is on the local target branch the moment a run integrates it, and
// on the remote only once the forge merges the pull request that carries it. In
// between it is awaiting the forge, and so is a promotion whose merge the forge
// dropped: the change reads as landed everywhere else while the request sits on
// the forge unmerged, waiting on whoever finds out.
//
// Until yoyodyne-ifd.357 two surfaces answered "what is awaiting the forge" from
// two different readings. The channel's heartbeat counted it from the record's
// own predicate, runstate.State.AwaitingForge, and the four lines derived it from
// the runs that still owe a step — which a run settled after a dropped merge is
// not, so its unpublished promotion was in the hourly count and on no line at a
// terminal. This is the one reading both take.

import "github.com/mason-bryant/yoyodyne/internal/runstate"

// AwaitingForge is every recorded promotion whose publication the forge has not
// finished, by the record's own predicate and nothing else. It is a function
// over the states a caller has already read rather than a reading of its own,
// because the heartbeat counts it from the same reading it selects its
// crossings from, and two readings of the same files a moment apart could
// disagree about one run.
func AwaitingForge(states []runstate.State) []runstate.State {
	awaiting := make([]runstate.State, 0, len(states))
	for _, state := range states {
		if state.AwaitingForge() {
			awaiting = append(awaiting, state)
		}
	}
	return awaiting
}

// awaitingForgeAttention is one unpublished promotion as the attention line
// carries it, with whose move it is: a merge the forge is holding is the
// forge's while checks pass or run, and the harness's when checks fail. A merge
// it dropped is the development manager's to decide, and a request nothing ever asked the forge to merge is
// the development manager's to decide — a re-arm the harness carries out, or a
// re-run — and a promotion whose record holds no request at all is the
// harness's: the next reconcile looks the request up by the run's branch and
// arms its merge. Until yoyodyne-ifd.429.31 the request nothing asked the forge
// to merge was the operator's, whose only move was a hand merge on the forge;
// what is left as the operator's is an unmerged request whose record carries
// some other account and no drop. All are settled by the same sweep once the
// forge records the merge, and the sentence Attention.Whose derives from the
// record says so. A queued merge whose checks could not be read is the
// harness's until the next sweep reads them. A merge the sweep withdrew because
// its checks failed on the target itself is the harness's too: it waits on the items filed for that
// check, and the harness takes it up once they close (yoyodyne-m5p).
func awaitingForgeAttention(state runstate.State) Attention {
	// The predicate that selects a state here requires the promotion to be
	// recorded, so it cannot be missing; a reading of every recorded run must
	// still not be able to panic on a record if that predicate is ever widened,
	// so a missing one leaves the field empty rather than being dereferenced,
	// and the sentence says so — a placeholder belongs in the sentence, not in
	// a field a surface would act on.
	publication := Publication{ReconcileFindings: state.ReconcileFindings, Branch: state.Branch, EndedAt: runEnded(state)}
	if state.Integration != nil {
		publication.TargetBranch = state.Integration.TargetBranch
	}
	if state.MergeDrop != nil {
		dropped := *state.MergeDrop
		publication.MergeDrop = &dropped
	}
	// The mover is read off the same fields, in the same order, that Whose
	// reads them in: a queued merge is the forge's whatever was dropped before
	// it was re-armed.
	mover := MoverHarness
	if state.PullRequest != nil {
		published := *state.PullRequest
		publication.PullRequest = &published
		switch {
		case published.MergeQueued && published.Checks != nil && published.Checks.ReadError != "":
			mover = MoverHarness
		case published.MergeQueued:
			mover = MoverForge
			if published.Checks != nil && published.Checks.Red() {
				mover = MoverHarness
			}
		case state.WaitingOnRedTarget():
			// Withdrawn for the target's red check, and waiting on the items filed
			// for it: the harness takes it up once they close, and nobody decides.
			mover = MoverHarness
		case publication.MergeDrop != nil:
			mover = MoverDevelopmentManager
		case state.PublicationUnasked():
			publication.Unarmed = true
			mover = MoverDevelopmentManager
		default:
			mover = MoverOperator
		}
	}
	if len(state.ReconcileFindings) > 0 {
		mover = reconcileFindingMover(state.ReconcileFindings)
	}
	return Attention{
		Kind:        AttentionPublication,
		ID:          state.RunID,
		Mover:       mover,
		WorkItemID:  state.WorkItemID,
		Publication: &publication,
	}
}
