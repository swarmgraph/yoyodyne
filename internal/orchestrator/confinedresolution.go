package orchestrator

// A resolution confined to the conflicting files rejoins the merge queue with
// one review: decision 4 of docs/designs/approved-changes-that-no-longer-merge.md,
// and "A resolved change whose approval was of an earlier head" in
// docs/designs/integration-through-a-merge-queue.md.
//
// An approved change the queue handed back because it no longer merged is
// resolved in a new session (conflictresolution.go). When that resolution
// passes its checks, the run keeps it apart (noteResolutionChecked), and the
// harness compares it with Git's own merge of the approved head onto the same
// target (rejoinsUnreviewed). The comparison is the harness's, never the
// developer's word. Where every file outside the conflicting ones is exactly as
// Git's merge leaves them, the resolution is admitted to the harness's own
// queue on the approval of the head it resolves, with no review in between
// (admitConfined); the queue's candidate is then reviewed whole before it
// lands, by a reviewer who wrote none of it and who is shown the conflicts and
// how each was resolved (candidateApprovingReview). Every other resolution is
// reviewed before it rejoins, as any repaired change is.
//
// The path is closed for the run, and the resolution reviewed here, where the
// queue's review of such a candidate refused it (the hand-back records
// State.ConfinedRefused), where the queue would not take it straight back, and
// wherever something else must approve the new head: a person's approval the
// integration policy requires is handed over only after a review here, and the
// protected-path gate runs on every attempt before its checks.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// resolutionComparer is the worktree manager comparing a resolution with
// Git's merge (gitworktree.Manager.CompareResolution). A manager that cannot
// is a comparison that could not be made, which closes the path.
type resolutionComparer interface {
	CompareResolution(ctx context.Context, target, approvedHead, resolvedHead string) (gitworktree.ResolutionComparison, error)
}

// noteResolutionChecked keeps the resolution whose checks have just passed
// apart from the conflict it answers, which the checks clear. An attempt that
// is not that resolution replaces the record of an earlier one.
func (a *activeRun) noteResolutionChecked() {
	conflict := a.state.ReplayConflict
	if !a.state.ResolvingInNewSession() || conflict.Approved == nil || conflict.Approved.HeadCommit == "" ||
		a.state.HarnessCommit == "" || a.state.BaseCommit == "" {
		if r := a.state.ConflictResolution; r != nil && r.ResolvedHead != a.state.HarnessCommit {
			a.state.ConflictResolution = nil
		}
		return
	}
	a.state.ConflictResolution = &runstate.ConflictResolution{
		Handback: conflict.Handback, Approved: *conflict.Approved,
		TargetCommit: a.state.BaseCommit, ResolvedHead: a.state.HarnessCommit,
		Attempt: a.state.RepairAttempts, RecordedAt: a.pipeline.clock().Now().UTC(),
	}
}

// confinedPathClosed is why the run's change, whose checks have passed, is not
// one that may rejoin the queue on the approval of the head it resolves, and
// empty where it may, once the comparison finds it confined.
func (a *activeRun) confinedPathClosed() string {
	state, r := a.state, a.state.ConflictResolution
	passed := state.ChecksPassed
	switch {
	case r == nil:
		return "the change is not a resolution of a conflict handed back after approval"
	case passed == nil || r.ResolvedHead != state.HarnessCommit || passed.Commit != r.ResolvedHead || passed.Attempt != r.Attempt || state.RepairAttempts != r.Attempt:
		return "the change is no longer the resolution whose checks passed"
	case state.ConfinedRefused != "":
		return state.ConfinedRefused
	case state.ReviewDecision != "":
		return "the change has a review of its own"
	case !a.pipeline.automatic():
		return "a person approves this project's integration, so the change is reviewed before it is handed to them"
	case !a.admitsToMergeQueue():
		return "the change is not admitted to the merge queue, whose review of the candidate is the only one it would have"
	case state.Document != nil || state.HarnessAuthored() || state.OutsideFile != nil:
		return "the change is not a developer's code change"
	case passed.Narrowed != "" && !passed.WholeLeftToQueue:
		return "the checks ran over part of the module and the queue was not left the rest"
	}
	return ""
}

// rejoinsUnreviewed reports a change whose checks have passed and which is a
// resolution the harness found confined to the conflicting files, making and
// recording the comparison where it has not been made for this resolution.
func (a *activeRun) rejoinsUnreviewed(ctx context.Context) bool {
	if a.confinedPathClosed() != "" {
		return false
	}
	// The comparison is saved with whatever the run saves next: the admission
	// of a confined resolution saves it before the entry is admitted on it
	// (admitToMergeQueue), and the review of any other with the review.
	r := a.state.ConflictResolution
	if r.Comparison == nil {
		comparison := a.compareResolution(ctx, *r)
		recorded := *r
		recorded.Comparison = &comparison
		a.state.ConflictResolution = &recorded
	}
	return a.state.ConflictResolution.Confined()
}

// compareResolution compares the resolution with Git's merge of the approved
// head onto the target it was built on.
func (a *activeRun) compareResolution(ctx context.Context, r runstate.ConflictResolution) runstate.ResolutionComparison {
	comparison := runstate.ResolutionComparison{ComparedAt: a.pipeline.clock().Now().UTC()}
	comparer, ok := a.pipeline.Worktrees.(resolutionComparer)
	if !ok {
		comparison.Problem = "this harness's worktree manager cannot compare a resolution with Git's merge"
		return comparison
	}
	compared, err := comparer.CompareResolution(ctx, r.TargetCommit, r.Approved.HeadCommit, r.ResolvedHead)
	if err != nil {
		comparison.Problem = oneline.Fold(err.Error(), runstate.MaxMergeQueueTextBytes)
		return comparison
	}
	comparison.MergedTree = compared.MergedTree
	comparison.Conflicting, comparison.ConflictingOmitted = runstate.BoundConflictPaths(compared.Conflicting)
	comparison.Outside, comparison.OutsideOmitted = runstate.BoundConflictPaths(compared.Outside)
	comparison.Resolutions, comparison.ResolutionsTruncated = compared.Resolutions, compared.Truncated
	if len(compared.Conflicting) == 0 && compared.Confined() {
		// Git merged the approved head by itself, so there was nothing to
		// resolve, and a resolution that left everything as Git did resolved
		// nothing either: it is reviewed as any change no conflict explains.
		comparison.Problem = "Git's own merge of the approved head onto the target found no conflict to resolve"
	}
	return comparison
}

// rejoiningConfined reports a run at its promotion whose change is a confined
// resolution with no review of its own, which is admitted by admitConfined
// rather than promoted on an approval.
func (a *activeRun) rejoiningConfined() bool {
	return a.confinedPathClosed() == "" && a.state.ConflictResolution.Confined()
}

// admitConfined admits a confined resolution to the harness's merge queue, or
// closes the path and has the caller go round the gate again, where the
// resolution is reviewed. The boolean is promoteApproved's: whether the gate is
// to be earned again.
func (a *activeRun) admitConfined(ctx context.Context) (Outcome, bool, error) {
	if err := a.holdForDirective(); err != nil {
		outcome, err := a.stop(ctx, err)
		return outcome, false, err
	}
	if err := a.holdForDependency(ctx); err != nil {
		outcome, err := a.stop(ctx, err)
		return outcome, false, err
	}
	resolution := *a.state.ConflictResolution
	outcome, ended, err := a.admitToMergeQueue(ctx, &resolution)
	if ended {
		return outcome, false, err
	}
	return Outcome{}, true, nil
}

// declineConfined closes the path for this run and says why, so the
// resolution goes round the gate again and is reviewed before it rejoins; the
// reason is saved with that review. The boolean is admitToMergeQueue's: the
// run has not ended.
func (a *activeRun) declineConfined(why string) (Outcome, bool, error) {
	a.state.ConfinedRefused = oneline.Fold("the resolution was not admitted on the approval of the head it resolves: "+why, runstate.MaxMergeQueueTextBytes)
	return Outcome{}, false, nil
}

// comparedSays is the run's comparison in a sentence, or that its record holds
// none.
func comparedSays(r *runstate.ConflictResolution) string {
	if r == nil || r.Comparison == nil {
		return "no comparison on the run's record"
	}
	return r.Comparison.Says()
}

// resolutionReview is what the reviewer of a confined resolution's candidate
// is told beside the earlier approval: that no reviewer has seen the
// resolution, which files conflicted, and how each was resolved.
func resolutionReview(entry runstate.MergeQueueEntry, run runstate.State) string {
	var said strings.Builder
	fmt.Fprintf(&said, "This candidate carries resolved head %s, a resolution of a conflict that approved head %s met on %s. No reviewer has seen the resolution: it rejoined the queue on the approval of the head it resolves because the harness found every file outside the conflicting ones exactly as Git's own merge of the approved head leaves it. Your review is the only one the resolution has, so judge how each conflict was resolved as closely as the rest of the change.\n\n",
		entry.ResolvedHead, entry.ApprovedHead, entry.TargetBranch)
	r := run.ConflictResolution
	if r == nil || r.ResolvedHead != entry.ResolvedHead || r.Comparison == nil {
		said.WriteString("The run's record no longer holds the comparison, so the conflicts are not listed here.")
		if resolution := entry.Resolution; resolution != nil {
			fmt.Fprintf(&said, " Compare the resolved head with Git's merge yourself: `git diff %s %s`.", resolution.MergedTree, entry.ResolvedHead)
		}
		return said.String()
	}
	c := r.Comparison
	said.WriteString("The files that conflicted:\n\n")
	for _, path := range c.Conflicting {
		said.WriteString("- " + path + "\n")
	}
	if c.ConflictingOmitted > 0 {
		fmt.Fprintf(&said, "- and %d more not listed here\n", c.ConflictingOmitted)
	}
	fmt.Fprintf(&said, "\nHow each was resolved, as a diff from Git's merge (conflict markers and all, tree %s) to the resolved head:\n\n", c.MergedTree)
	var fenced strings.Builder
	writeFenced(&fenced, "diff", c.Resolutions)
	said.WriteString(fenced.String())
	if c.ResolutionsTruncated {
		fmt.Fprintf(&said, "\nThe diff is cut at %d bytes; read the rest with `git diff %s %s`.\n", runstate.MaxResolutionDiffBytes, c.MergedTree, entry.ResolvedHead)
	}
	return said.String()
}
