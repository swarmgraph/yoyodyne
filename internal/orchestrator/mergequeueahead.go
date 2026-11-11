package orchestrator

// Checking the next merge queue candidate while the current one is still being
// checked: decision 3 of docs/designs/merge-queue-pace.md.
//
// While the worker runs the checks and the review of the first waiting entry's
// candidate, it builds the next waiting entry's candidate on top of that
// candidate's commit, as if it lands, and runs that candidate's protected-path
// check, checks and review beside it. The candidate checked ahead is a
// generation of its own entry like any other: a commit of its own, its own
// recorded base, and evidence bound to it alone (runstate's MergeQueueAhead
// says what it was built on). The queue checks at most one candidate ahead.
//
// Nothing about a candidate checked ahead is trusted beyond what it is. It
// lands only through the ordinary promotion, which takes it only once its
// entry is the first unfinished one and lands it only where the target, read
// again under the promotion lease, is exactly its base: that is, where the
// candidate ahead of it landed as that very commit. Wherever it did not — it
// failed, was withdrawn or handed back, or landed as another commit, or the
// target moved some other way — the generation is invalidated as
// base_not_landed and the entry is built again on the target as it stands,
// exactly as a moved target is handled, and nothing is charged to its run. A
// failure or a refusal found on a candidate checked ahead is never handed back
// on its own account: recovery reads an entry only once it is the first, and
// by then its candidate either stands on the target or is invalidated
// (Recover). A failing check is not run on its base here either, because its
// base is not yet the target; that waits until the entry is the first and its
// candidate's base is the target.
//
// A candidate lands as exactly its own commit only on a target the harness
// moves itself, in a project that does not publish. A project that publishes
// lands every candidate through a pull request, and the forge's merge (the
// merge-commit method, mergeMethod in publish.go) puts a merge commit of its
// own above the candidate, which the local target then follows; the target
// never becomes the candidate, so nothing built on the candidate could land.
// On that path the worker checks nothing ahead and says why.
//
// The two candidates' stages run side by side under the one worker lease the
// call holds; the call waits for both before it lets the lease go, so the
// entry ahead lands once both are done. A current candidate that does not end
// verified stops what is being checked ahead of it, since nothing built on it
// can land.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// aheadCheck is one Work call's candidate checked ahead, running in a
// goroutine of its own. The zero value has none running.
type aheadCheck struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	outcome *MergeQueueVerification
}

// start checks the next entry ahead of base, unless one is already running.
func (a *aheadCheck) start(ctx context.Context, w MergeQueueWorker, lease *runstate.Lease, key runstate.MergeQueueKey, current runstate.MergeQueueEntry, base runstate.MergeQueueGeneration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.done != nil {
		return
	}
	// The candidate checked ahead is not one a restart waits for, so its
	// checks are not counted among the session's check stages
	// (queueCheckStages); a session about to restart starts none.
	if !queueCheckStagesOf(ctx).accepting() {
		a.outcome = &MergeQueueVerification{Waiting: "the watch session working this queue is about to restart onto a new build, so nothing is checked ahead"}
		return
	}
	aheadCtx, cancel := context.WithCancel(context.WithValue(ctx, queueCheckStagesKey{}, (*queueCheckStages)(nil)))
	a.cancel, a.done = cancel, make(chan struct{})
	go func() {
		defer close(a.done)
		outcome := w.checkAhead(aheadCtx, lease, key, current, base)
		a.mu.Lock()
		a.outcome = &outcome
		a.mu.Unlock()
	}()
}

// halt ends what is being checked ahead and waits for it, so another may be
// started.
func (a *aheadCheck) halt() {
	a.mu.Lock()
	cancel, done := a.cancel, a.done
	a.mu.Unlock()
	if done == nil {
		return
	}
	cancel()
	<-done
	a.mu.Lock()
	a.cancel, a.done = nil, nil
	a.mu.Unlock()
}

// collect waits for what is being checked ahead, stopping it first where the
// current candidate did not end verified, and reports it on the outcome.
func (a *aheadCheck) collect(outcome MergeQueueVerification) MergeQueueVerification {
	a.mu.Lock()
	cancel, done := a.cancel, a.done
	a.mu.Unlock()
	if done != nil {
		if !outcome.Verified {
			cancel()
		}
		<-done
		cancel()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	outcome.Ahead = a.outcome
	return outcome
}

// stagesLeft reports a generation whose checks or review still have to run, so
// there is time to check another candidate beside them. One already verified,
// or already failed, has none.
func stagesLeft(generation runstate.MergeQueueGeneration) bool {
	run := generation.CheckRun
	if run == nil || run.FinishedAt == nil || run.Problem != "" {
		return true
	}
	if !checksPassed(generation) {
		return false
	}
	verdict := generation.Review
	return verdict == nil || verdict.FinishedAt == nil || verdict.Problem != ""
}

// mergeQueueAuthor is who wrote an entry's change, whom the candidate's
// reviewer must not be, and empty where the run records nobody.
func mergeQueueAuthor(run runstate.State) string {
	author := strings.TrimSpace(run.ProviderSessionID)
	if author == "" && run.Document != nil {
		// A document's author is the owning role's conversation session that
		// wrote it, which the candidate's reviewer must not be.
		author = strings.TrimSpace(run.Document.AuthorSession)
	}
	if author == "" && run.HarnessAuthored() {
		// No provider session wrote the change of a run that brings a file in:
		// the harness fetched and wrote it. Its author is named as the harness's
		// own run, which no reviewer session can be. A document recorded with no
		// author session is named the same way.
		author = "harness:" + run.RunID
	}
	return author
}

// checkAhead builds or takes up the next waiting entry's candidate on base's
// candidate and runs its stages. What it reports is the account of that; a
// reason it checked nothing is reported as Waiting, and an error as Refusal,
// because nothing about the entry checked ahead is decided here.
func (w MergeQueueWorker) checkAhead(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, current runstate.MergeQueueEntry, base runstate.MergeQueueGeneration) MergeQueueVerification {
	outcome := MergeQueueVerification{}
	skip := func(why string) MergeQueueVerification {
		outcome.Waiting = why
		return outcome
	}
	stopped := func(err error) MergeQueueVerification {
		outcome.Refusal = "the check ahead stopped: " + err.Error()
		return outcome
	}
	publishing, _, err := w.Pipeline.resolvePublishing(ctx)
	if err != nil {
		return stopped(fmt.Errorf("resolve whether the project publishes: %w", err))
	}
	if publishing {
		return skip(fmt.Sprintf("nothing is checked ahead: %s lands through a pull request, which the forge lands as a merge commit of its own rather than as the candidate, so a candidate built on the candidate could never land", key.TargetBranch))
	}
	if current.Mode != runstate.MergeQueueHarness {
		return skip("nothing is checked ahead of an entry the forge's own queue lands")
	}
	next, found, err := w.nextAhead(key, current)
	if err != nil {
		return stopped(err)
	}
	if !found {
		return skip("no entry waits behind this one")
	}
	outcome.Entry = next
	if next.Mode != runstate.MergeQueueHarness {
		return skip("the entry behind this one is landed by the forge's own queue")
	}
	run, err := w.Pipeline.Store.Load(next.RunID)
	if err != nil {
		return stopped(fmt.Errorf("read run %s, which merge queue entry %d was admitted for: %w", next.RunID, next.Order, err))
	}
	author := mergeQueueAuthor(run)
	if author == "" {
		return skip(fmt.Sprintf("run %s records no developer session, so no reviewer can be shown to be independent of it", next.RunID))
	}
	configured := w.Pipeline.mergeQueueChecks(next)
	generation, found, why, err := w.aheadGeneration(ctx, lease, key, next, author, configured, base)
	outcome.Generation = generation
	var conflict *gitworktree.QueueCandidateConflict
	switch {
	case errors.As(err, &conflict):
		return skip(fmt.Sprintf("the approved head of entry %d does not merge onto the candidate ahead of it; it is built on the target once that candidate settles", next.Order))
	case err != nil:
		var wait mergeQueueWait
		if errors.As(err, &wait) {
			return skip(wait.reason)
		}
		return stopped(err)
	case !found:
		return skip(why)
	}
	generation, err = w.checkPaths(ctx, lease, key, next, generation)
	outcome.Generation = generation
	if err != nil {
		return stopped(err)
	}
	if generation.Paths != nil && len(generation.Paths.Refused) > 0 {
		outcome.Refusal = generation.Gate(configured).Error()
		return outcome
	}
	generation, moved, err := w.check(ctx, lease, key, generation)
	outcome.Generation = generation
	if err == nil && !moved && generation.CheckRun != nil && checksPassed(generation) {
		generation, moved, err = w.review(ctx, lease, key, next, run, generation)
		outcome.Generation = generation
	}
	var wait mergeQueueWait
	switch {
	case errors.As(err, &wait):
		return skip(wait.reason)
	case err != nil:
		return stopped(err)
	case moved:
		outcome.Refusal = fmt.Sprintf("the candidate ahead of it did not land as %s, so generation %d can never land; it is built again on the target", base.Candidate, generation.Number)
		return outcome
	}
	if gate := generation.Gate(configured); gate != nil {
		outcome.Refusal = gate.Error()
		return outcome
	}
	outcome.Verified = true
	return outcome
}

// nextAhead is the first entry behind current, in the order the queue is
// worked, that still waits to be integrated.
func (w MergeQueueWorker) nextAhead(key runstate.MergeQueueKey, current runstate.MergeQueueEntry) (runstate.MergeQueueEntry, bool, error) {
	entries, err := w.Queue.Entries(key)
	if err != nil {
		return runstate.MergeQueueEntry{}, false, err
	}
	behind := false
	for _, entry := range runstate.MergeQueueWorkOrder(entries) {
		if !behind {
			behind = entry.EntryID == current.EntryID
			continue
		}
		if w.Waiting != nil && !w.Waiting(entry) {
			continue
		}
		return entry, true, nil
	}
	return runstate.MergeQueueEntry{}, false, nil
}

// aheadGeneration is the generation of next standing on base's candidate:
// the one already built there, reconciled with what an earlier worker left, or
// a new one. A generation next already has on the target itself is left alone
// and nothing is checked ahead, because it is the evidence the entry needs if
// the candidate ahead does not land. One built ahead on any other candidate is
// invalidated first, because that candidate is not the one going to land.
func (w MergeQueueWorker) aheadGeneration(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, next runstate.MergeQueueEntry, author string, configured runstate.MergeQueueCheckConfiguration, base runstate.MergeQueueGeneration) (runstate.MergeQueueGeneration, bool, string, error) {
	generations, err := w.Queue.Generations(key, next.EntryID)
	if err != nil {
		return runstate.MergeQueueGeneration{}, false, "", err
	}
	ahead := &runstate.MergeQueueAhead{EntryID: base.EntryID, Generation: base.Number, Candidate: base.Candidate, OnTarget: base.TargetBase}
	if count := len(generations); count > 0 {
		standing := generations[count-1]
		if err := w.awaitUnfinished(key, standing); err != nil {
			return standing, false, "", err
		}
		if standing.Invalidated == nil {
			if standing.BuiltAhead == nil {
				return standing, false, fmt.Sprintf("entry %d already has generation %d built on %s itself, which is kept for the case the candidate ahead does not land", next.Order, standing.Number, key.TargetBranch), nil
			}
			reason := runstate.MergeQueueBaseNotLanded
			if *standing.BuiltAhead == *ahead {
				reason = runstate.MergeQueueChecksChanged
				if standing.Checks.Same(configured) {
					held, err := w.Candidates.HoldsCommit(ctx, standing.Candidate)
					if err != nil {
						return standing, false, "", err
					}
					if held {
						generation, err := w.reconcile(ctx, lease, key, standing)
						return generation, err == nil, "", err
					}
					reason = runstate.MergeQueueCandidateLost
				}
			}
			target, err := w.Candidates.TargetCommit(ctx, key.TargetBranch)
			if err != nil {
				return standing, false, "", fmt.Errorf("read where %s stands: %w", key.TargetBranch, err)
			}
			if _, err := w.invalidate(lease, key, standing, reason, target); err != nil {
				return standing, false, "", err
			}
		}
	}
	generation, err := w.build(ctx, lease, key, next, author, configured, uint64(len(generations)+1), ahead)
	return generation, err == nil, "", err
}
