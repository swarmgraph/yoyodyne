package orchestrator

// A queued merge is read with its checks.
//
// Until yoyodyne-ifd.429.16 a merge the forge had queued was read for one thing
// only — whether it was still queued — and the record never carried whether it
// could land. Pull request 609 sat queued 33 hours against a red build; 713 sat
// 31 commits behind main failing two tests its change never touched, and the
// development manager waited on it because the record said the merge was
// queued; on 2026-09-25 four requests sat that way past every later merge, and
// each cost a person a look at the forge. A merge the forge is holding for
// checks that will never pass is not a merge that is going to happen.
//
// So every sweep that finds a merge still queued reads the head's checks too,
// writes them onto the publication, and decides on them:
//
//   - Checks passing or still running: the merge stays queued, with the
//     reading beside it.
//   - A head behind its target whose failing checks name no file the change
//     touches: the failure is one the change met rather than brought, and the
//     remedy is the one a promotion that lost its race takes. The queued merge
//     is withdrawn and the run is put back at its promotion, where the target
//     is found moved and the change is replayed onto it, checked and reviewed
//     again, and queued again — under the same integration-retry budget a
//     replay spends, and hosted by the sweep that hosts runs.
//   - Jobs the forge ended itself — cancelled, timed out, or never started,
//     naming no file — are run again on the same head, up to
//     runstate.MaxCheckReruns times, and the merge stays queued: nothing in the
//     tree decided them. A step that failed ("failure") is not re-run, even
//     where its only annotation is the forge's own on .github, because that is
//     also what a genuine red test looks like. On 2026-09-28 two merges on heads
//     level with main were handed to a person with a failing check annotated
//     only on .github — adoption at 04:15Z, build at 06:08Z — and the record
//     kept neither check's conclusion, so which of the two those were is only
//     in the forge's logs of those runs; the conclusion is kept now.
//   - A head level with its target whose failing checks name no file the change
//     touches: nothing but the change differs from the target, so the failure
//     is the target's own. It is filed as the target's, one p0 item per target
//     branch and check as a red landing files its own, and the queued merge is
//     withdrawn and waits on those items with the harness as its next mover
//     rather than being handed to a person (redtarget.go, yoyodyne-m5p). It is
//     filed only once it is confirmed: the same check red on the target's own
//     head, or, where the forge cannot say, nothing the change adds or modifies
//     named in the check's log. A check that passes on the target's head, or
//     whose log names a file or directory of the change, is the change's own
//     failure and is handed back as the next case is (yoyodyne-c02).
//   - A failing check naming a file the change touches is the change's own
//     failure. The queued merge is withdrawn and the item is handed back with
//     a failing check for repair in its preserved developer session. The red
//     revision cannot be re-armed.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ReconcileChecks is the forge access reading a queued merge's checks needs:
// the checks themselves, and withdrawing the queued merge before the request is
// handed back or its head rewritten. A merge left armed across either would be
// landed by the forge the moment its checks turned green — for a rewritten head,
// a change no reviewer has seen.
//
// RerunCheck runs a job the forge ended itself again on the same head.
//
// It is satisfied by publish.GitHub.
type ReconcileChecks interface {
	Checks(ctx context.Context, number int, base string) (publish.CheckReading, error)
	DisableAutoMerge(ctx context.Context, number int) error
	RerunCheck(ctx context.Context, checkRun int64) error
}

// ActionUpdating reports a queued merge whose head fell behind its target and
// failed checks the change does not touch, which the sweep withdrew and put back
// at its promotion to be replayed onto the target and queued again. The run is
// live again and is hosted by ContinueUpdates.
const ActionUpdating ReconcileAction = "updating"

// settleStillQueued is the reading of a merge the forge still holds: its checks
// are read, written onto the publication, and decided on.
//
// A reconciler wired without check access reads the merge as it always did. A
// reading that fails leaves the merge queued and records the failed read:
// a check state nobody could read is not a red one.
func (r Reconciler) settleStillQueued(ctx context.Context, state runstate.State) (Reconciliation, error) {
	published := *state.PullRequest
	target := state.Integration.TargetBranch
	result := reconciliationOf(state, ActionQueued)
	result.Detail = fmt.Sprintf("the forge still has the merge of pull request %d into %s queued", published.Number, target)
	if r.Checks == nil {
		return result, nil
	}
	reading, err := r.Checks.Checks(ctx, published.Number, target)
	if err != nil {
		result.Detail += fmt.Sprintf("; its checks could not be read (%v), so it is left queued and the next sweep reads them again", err)
		return result, r.recordUnreadChecks(state, err)
	}
	checks := recordedChecks(reading, r.clock().Now())
	if prior := published.Checks; prior != nil && prior.HeadCommit == checks.HeadCommit {
		checks.Reruns = prior.Reruns
		checks.RerunChecks = append([]int64(nil), prior.RerunChecks...)
	}
	// A job the forge ended is run again before anything is decided on it, and
	// the merge stays armed meanwhile: the re-run's result is the head's reading
	// from then on, so a pass lets the forge land it and a second ending is read
	// by the next sweep. A reading still naming only the check runs already sent
	// back was taken before the re-run began, and spends nothing.
	rerun, awaiting, rerunRefused := false, checks.AwaitingRerun(), ""
	if !awaiting && checks.FailedInTheJob() && checks.Reruns < runstate.MaxCheckReruns {
		if rerunRefused = r.rerunFailedJobs(ctx, checks); rerunRefused == "" {
			checks.RecordRerun()
			rerun = true
		}
	}
	published.Checks = &checks
	state.PullRequest = &published
	state.UpdatedAt = r.clock().Now()
	if err := r.Store.Save(state); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the checks of pull request %d on run %s: %w", published.Number, state.RunID, err)
	}
	result = reconciliationOf(state, ActionQueued)
	result.Detail = fmt.Sprintf("the forge still has the merge of pull request %d into %s queued; %s", published.Number, target, checks.Describe(target))
	switch {
	case !checks.Red():
		return result, nil
	case rerun:
		result.Detail += fmt.Sprintf("; the forge ended the failed jobs before any step failed, so the harness asked it to run them again (%d of %d on this head) and left the merge queued",
			checks.Reruns, runstate.MaxCheckReruns)
		return result, nil
	case awaiting:
		result.Detail += "; the forge has not yet started the re-run the harness asked for, so the merge is left queued for the next sweep"
		return result, nil
	case checks.BehindBy > 0 && !checks.ChangeFails():
		return r.updateQueuedHead(ctx, state, result, false)
	case checks.ChangeFails():
		return r.handBackFailedChange(ctx, state, fmt.Sprintf(
			"the forge's checks on pull request %d fail on this change: %s. The harness withdrew the queued merge rather than leave a red change queued, and the pull request needs its change repaired",
			published.Number, checks.Describe(target)))
	case checks.FailedInTheJob():
		why := fmt.Sprintf("were ended by the forge before any step failed, and ended that way again on each of %d re-run(s) of this head", checks.Reruns)
		if rerunRefused != "" {
			why = fmt.Sprintf("were ended by the forge before any step failed, and the forge would not run them again (%s)", rerunRefused)
		}
		return r.handBackRedMerge(ctx, state, fmt.Sprintf(
			"the forge's checks on pull request %d %s: %s. The forge's account of each, read under the harness's forge access, is in this item's notes. The harness withdrew the queued merge rather than leave a red change queued, and the pull request needs a person",
			published.Number, why, checks.Describe(target)))
	default:
		// A head level with its target, failing on no file its change touches:
		// the target's own failure, filed as the target's (redtarget.go).
		return r.waitOnRedTarget(ctx, state, checks, reading.Files, false)
	}
}

// recordUnreadChecks keeps a failed read on the publication, including a
// failure before the forge could name the head. Re-run accounting survives a
// failed read and is carried forward only if the next read names the same head.
func (r Reconciler) recordUnreadChecks(state runstate.State, problem error) error {
	published := *state.PullRequest
	var checks runstate.PullRequestChecks
	if published.Checks != nil {
		checks = *published.Checks
	}
	checks.ReadAt = r.clock().Now().UTC()
	checks.ReadError = problem.Error()
	published.Checks = &checks
	state.PullRequest = &published
	state.UpdatedAt = checks.ReadAt
	if err := r.Store.Save(state); err != nil {
		return fmt.Errorf("record the unread checks of pull request %d on run %s: %w", published.Number, state.RunID, err)
	}
	return nil
}

// rerunFailedJobs asks the forge to run every failed check of a reading again,
// and says what it refused if it refused any. A check with no check run to name
// cannot be run again, which is a refusal as well.
func (r Reconciler) rerunFailedJobs(ctx context.Context, checks runstate.PullRequestChecks) string {
	var refused []string
	for _, failing := range checks.Failing {
		if failing.CheckRun <= 0 {
			refused = append(refused, fmt.Sprintf("%s names no check run", failing.Name))
			continue
		}
		if err := r.Checks.RerunCheck(ctx, failing.CheckRun); err != nil {
			if errors.Is(err, publish.ErrForgeAccessRefused) {
				refused = append(refused, fmt.Sprintf("%s: the forge would not let the harness's token run the job again (%v), and granting it that is the operator's", failing.Name, err))
				continue
			}
			refused = append(refused, fmt.Sprintf("%s: %v", failing.Name, err))
		}
	}
	return oneline.Bound(strings.Join(refused, "; "), 400)
}

// recordedChecks is the forge's reading as the record keeps it: each failing
// check with the files its annotations named, and which of those the change
// itself touches.
func recordedChecks(reading publish.CheckReading, now time.Time) runstate.PullRequestChecks {
	touched := make(map[string]bool, len(reading.Files))
	for _, file := range reading.Files {
		touched[file] = true
	}
	checks := runstate.PullRequestChecks{
		HeadCommit: reading.HeadCommit,
		ReadAt:     now.UTC(),
		Pending:    len(reading.Pending),
		Passing:    reading.Passing,
		BehindBy:   reading.BehindBy,
	}
	for _, failed := range reading.Failing {
		if len(checks.Failing) == runstate.MaxRecordedFailingChecks {
			break
		}
		failing := runstate.FailingCheck{Name: boundedCheckName(failed.Name), CheckRun: failed.ID, Conclusion: oneline.Bound(failed.Conclusion, 200)}
		if len(failed.URL) <= 400 {
			failing.URL = failed.URL
		}
		for _, annotation := range failed.Annotations {
			if len(failing.Annotations) == runstate.MaxRecordedCheckAnnotations {
				break
			}
			failing.Annotations = append(failing.Annotations, runstate.CheckAnnotation{
				Path:    oneline.Bound(annotation.Path, runstate.MaxCheckAnnotationBytes),
				Line:    max(annotation.Line, 0),
				Level:   oneline.Bound(annotation.Level, 200),
				Message: oneline.Bound(annotation.Message, runstate.MaxCheckAnnotationBytes),
			})
		}
		for _, path := range failed.Paths {
			if touched[path] && len(failing.OnChange) < runstate.MaxRecordedCheckPaths {
				failing.OnChange = append(failing.OnChange, path)
			}
			if len(failing.Paths) < runstate.MaxRecordedCheckPaths {
				failing.Paths = append(failing.Paths, path)
			}
		}
		sort.Strings(failing.OnChange)
		checks.Failing = append(checks.Failing, failing)
	}
	return checks
}

func boundedCheckName(name string) string {
	if name = oneline.Bound(name, 200); name == "" {
		return "an unnamed check"
	}
	return name
}

// handBackRedMerge withdraws a red queued merge and settles the run as the
// dropped merge it now is, with the checks named: the item is handed to a
// person with a durable blocker, and the publication goes on the docket where
// triage's bounded re-arm is decided.
//
// The merge is withdrawn before anything is written. A withdrawal that fails
// leaves the record queued and the next sweep asks again; a record written
// first would hand back a merge the forge could still land.
func (r Reconciler) handBackRedMerge(ctx context.Context, state runstate.State, reason string) (Reconciliation, error) {
	return r.handBackRedMergeWithCheck(ctx, state, reason, false)
}

// handBackFailedChange returns the forge's failure to the same repair path as
// a local failing check, after withdrawing the merge that could land it.
func (r Reconciler) handBackFailedChange(ctx context.Context, state runstate.State, reason string) (Reconciliation, error) {
	return r.handBackRedMergeWithCheck(ctx, state, reason, true)
}

func (r Reconciler) handBackRedMergeWithCheck(ctx context.Context, state runstate.State, reason string, changeFails bool) (Reconciliation, error) {
	published := *state.PullRequest
	if err := r.Checks.DisableAutoMerge(ctx, published.Number); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("withdraw the red queued merge of pull request %d for run %s: %w", published.Number, state.RunID, err)
	}
	// The forge's account of the failing checks is read once the merge is
	// withdrawn, and goes onto the item with the settlement, so whoever the item
	// is handed to works from it rather than from the forge.
	var account string
	if published.Checks != nil {
		account = renderForgeAccount(r.checkAccounts(ctx, *published.Checks))
		if changeFails {
			state.CheckFailure = forgeCheckFailure(*published.Checks, account)
			state.ChecksPassed = nil
		}
	}
	published.MergeQueued = false
	// A merge handed back is no longer waiting on its target's red check: it is a
	// dropped merge somebody decides about, and every surface has to say so.
	published.TargetRed = nil
	state.PullRequest = &published
	state.PublishFailure = reason
	state.MergeDrop = &runstate.MergeDrop{At: r.clock().Now(), Reason: reason}
	return r.settleDroppedMergeWith(ctx, state, account)
}

// forgeCheckFailure keeps the forge's conclusion, annotations and captured log
// as repair input. There is no process exit code in a forge reading.
func forgeCheckFailure(checks runstate.PullRequestChecks, account string) *runstate.CheckFailure {
	names := make([]string, 0, len(checks.Failing))
	for _, failing := range checks.Failing {
		names = append(names, failing.Name)
	}
	return &runstate.CheckFailure{
		Command: strings.Join(names, ", "), ForgeHeadCommit: checks.HeadCommit,
		Output: boundedTail(account, runstate.MaxCheckOutputBytes),
	}
}

// invalidateForgeApproval retains the publication as history, but takes back
// the promotion and review credit the next developer attempt must earn again.
func invalidateForgeApproval(state *runstate.State) {
	state.Integration = nil
	state.ChecksPassed = nil
	state.ReviewSessionID = ""
	state.ReviewModel = ""
	state.ReviewResolvedModel = ""
	state.ReviewEffort = ""
	state.ReviewBaseCommit = ""
	state.ReviewHeadCommit = ""
	state.ReviewDecision = ""
	state.ReviewApproves = ""
	state.ReviewSummary = ""
	state.ReviewFindings = 0
	state.ReviewFindingDetails = nil
}

// updateQueuedHead brings a queued head that fell behind its target and failed
// checks it did not bring back to its promotion, where the pipeline replays it
// onto the target, re-earns the checks and the review, and queues the merge
// again.
//
// A lost race is never a reason to hand it back — the replay's own gate is
// where the integration budget is enforced. What it will not do is replay a
// run it cannot replay: a local promotion is already on the target and has no head to
// rewrite, and a run whose worktree, branch, or sessions are gone has nothing to
// replay from. Those are handed back as a red merge is. Where the replay is
// possible and the moment is not — no sweep hosting runs, intake held, every
// slot taken — the merge is left queued, saying why, for the next sweep.
//
// dropped is a merge the forge already stopped holding (replayDroppedLanding),
// which has nothing to withdraw; its caller has already refused the runs that
// cannot be replayed.
func (r Reconciler) updateQueuedHead(ctx context.Context, state runstate.State, result Reconciliation, dropped bool) (Reconciliation, error) {
	published := *state.PullRequest
	target := state.Integration.TargetBranch
	describe := published.Checks.Describe(target)
	if refusal := unreplayable(state); refusal != "" && !dropped {
		return r.handBackRedMerge(ctx, state, fmt.Sprintf(
			"the forge's checks on pull request %d fail on files this change does not touch while its head is behind %s, and the harness cannot bring it up to date: %s: %s. The harness withdrew the queued merge rather than leave a red change queued, and the pull request needs a person",
			published.Number, target, refusal, describe))
	}
	// A head that fell behind is a race the change lost after its merge was
	// queued, and a lost race is never a reason to hand anything back: the
	// change is replayed as any lost race is, and the replay's own gate is where
	// the integration budget is enforced, against a replay that stops on the
	// change.
	waiting := func(why string) (Reconciliation, error) {
		left := "it is left queued for the next sweep"
		if dropped {
			left = "it is left for the next sweep, which asks the forge again"
		}
		result.Detail += fmt.Sprintf("; its head is to be brought up to date onto %s, and %s, so %s", target, why, left)
		return result, nil
	}
	if !r.HostsRuns {
		return waiting("this pass hosts no runs — `yoyo reconcile` does")
	}
	if r.Intake != nil {
		hold, held, err := r.Intake.Held()
		if err != nil {
			return waiting(fmt.Sprintf("whether intake is held could not be read (%v)", err))
		}
		if held {
			return waiting(fmt.Sprintf("intake is held (%s)", nonEmpty(hold.Reason, "no reason recorded")))
		}
	}
	if free, err := r.slotFree(); err != nil || !free {
		if err != nil {
			return waiting(fmt.Sprintf("what is in flight could not be read (%v)", err))
		}
		return waiting("every developer slot is taken")
	}

	// The merge is withdrawn first, because the replay rewrites the head: a merge
	// left armed would land the rewritten head the moment its checks passed,
	// before any reviewer had seen it. A withdrawal that fails writes nothing.
	// A dropped merge is one the forge holds no longer, so there is nothing to
	// withdraw.
	reason := fmt.Sprintf("the forge dropped the queued merge of pull request %d while its head was %d commit(s) behind %s (%s), so the reconcile sweep put the run back at its promotion to be brought up to date onto %s from its kept branch, checked and reviewed again, and queued again, as a replay is; this is lost race %d, and a lost race costs nothing",
		published.Number, published.Checks.BehindBy, target, describe, target, state.IntegrationRetries+1)
	if published.TargetRed != nil {
		reason = fmt.Sprintf("every item the merge of pull request %d waited on for %s's red check is closed, and the fix left its head %d commit(s) behind %s (%s), so the reconcile sweep put the run back at its promotion to be brought up to date onto %s from its kept branch, checked and reviewed again, and queued again, as a replay is; this is lost race %d, and a lost race costs nothing",
			published.Number, target, published.Checks.BehindBy, target, describe, target, state.IntegrationRetries+1)
	}
	if !dropped {
		if err := r.Checks.DisableAutoMerge(ctx, published.Number); err != nil {
			return reconciliationOf(state, ActionUnsettled), fmt.Errorf("withdraw the queued merge of pull request %d before updating run %s: %w", published.Number, state.RunID, err)
		}
		reason = fmt.Sprintf("the forge's checks on pull request %d fail on files this change does not touch while its head is %d commit(s) behind %s (%s), so the reconcile sweep withdrew the queued merge and put the run back at its promotion to be brought up to date onto %s, checked and reviewed again, and queued again, as a replay is; this is lost race %d, and a lost race costs nothing",
			published.Number, published.Checks.BehindBy, target, describe, target, state.IntegrationRetries+1)
	}
	// The item is told first, as a resumption tells it first: a run made live
	// behind an item that says nothing about it is one nobody reading the item
	// can account for.
	account := renderForgeAccount(r.checkAccounts(ctx, *published.Checks))
	if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, renderQueuedUpdateNotes(state, reason, dropped, account)); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the update of run %s: %w", state.RunID, err)
	}
	now := r.clock().Now()
	resumed := state
	resumed.IntegrationResumptions = append(append([]runstate.IntegrationResumption{}, state.IntegrationResumptions...),
		runstate.IntegrationResumption{
			Cause:             runstate.CauseQueuedHeadBehind,
			Reason:            boundedReason(reason),
			ResumedAt:         now,
			SupersededFailure: state.Failure,
			SupersededBlocker: state.Blocker,
		})
	// The promotion the queued merge carried is taken back off the record: the
	// change is about to be replayed onto a new base, and what is promoted and
	// queued afterwards is the replayed change, recorded by the run that makes it.
	resumed.Integration = nil
	published.MergeQueued = false
	// The reading was about the head the replay is about to replace, and its
	// account travels on the resumption above; left on the publication it would
	// be said beside the replayed head's queued merge as though it were that
	// head's, until a sweep read the new one.
	published.Checks = nil
	// A wait on the target's red check ends here: the replay queues a merge of
	// its own, on a head that carries the fix.
	published.TargetRed = nil
	resumed.PullRequest = &published
	resumed.PublishFailure = ""
	resumed.Failure = ""
	resumed.Blocker = ""
	resumed.Status = runstate.StatusRunning
	resumed.Phase = runstate.PhaseIntegrating
	resumed.CompletedAt = nil
	resumed.SettledQuietSince = nil
	resumed.UpdatedAt = now
	if err := r.Store.Save(resumed); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("put run %s back at its promotion, whose queued merge the forge no longer holds: %w", state.RunID, err)
	}
	updating := reconciliationOf(resumed, ActionUpdating)
	updating.Detail = reason
	return updating, nil
}

// replayDroppedLanding decides a merge the forge stopped holding for a change
// that landed through its pull request, before it is handed to anybody as
// dropped. decided is false where the drop is a person's after all, and the
// caller hands it back exactly as it always has.
//
// A landing through the pull request moved no local branch, and its run kept
// its branch and worktree because nothing proved the change was on the target.
// So a drop there is not the end of a promotion: the change is whole on its kept
// branch, and where the forge dropped it with its head behind the target and
// failing nothing the change touches, it is the race a replay answers — brought
// up to date from the kept branch, checked and reviewed again, and queued again
// by its own run (updateQueuedHead). Only a drop that cannot be replayed is a
// person's: a local promotion, a run whose artifacts or sessions are gone, a
// request the forge closed, a head level with its target, or checks that fail
// on the change itself.
//
// A reading of the checks the forge could not give decides the drop neither
// way. The record is left as it stands, still queued, and the next sweep asks
// the forge again — the same rule a merge still held follows.
func (r Reconciler) replayDroppedLanding(ctx context.Context, state *runstate.State, observed publish.PullRequest) (Reconciliation, bool, error) {
	published := *state.PullRequest
	target := state.Integration.TargetBranch
	if r.Checks == nil || unreplayable(*state) != "" || !strings.EqualFold(observed.State, "OPEN") {
		return Reconciliation{}, false, nil
	}
	result := reconciliationOf(*state, ActionQueued)
	reading, err := r.Checks.Checks(ctx, published.Number, target)
	if err != nil {
		result.Detail = fmt.Sprintf("the forge holds no merge for pull request %d any more, and its checks could not be read (%v), so whether its head can be brought up to date is not known; the record is left as it stands and the next sweep asks again",
			published.Number, err)
		return result, true, r.recordUnreadChecks(*state, err)
	}
	checks := recordedChecks(reading, r.clock().Now())
	if prior := published.Checks; prior != nil && prior.HeadCommit == checks.HeadCommit {
		checks.Reruns = prior.Reruns
		checks.RerunChecks = append([]int64(nil), prior.RerunChecks...)
	}
	// Carry the successful reading back to the caller even when it settles the
	// drop instead of replaying it, so a previous read error does not survive.
	published.Checks = &checks
	state.PullRequest = &published
	// This also recovers a withdrawal whose settlement was interrupted: the
	// forge no longer holds the merge, but its own-change failure still goes to
	// repair rather than becoming a generic drop of the red revision.
	if checks.ChangeFails() {
		stopped, err := r.handBackFailedChange(ctx, *state, fmt.Sprintf(
			"the forge dropped the queued merge of pull request %d with checks failing on this change: %s. The pull request needs its change repaired",
			published.Number, checks.Describe(target)))
		return stopped, true, err
	}
	// A head level with its target that failed on no file its change touches
	// failed on the target, and the queue dropping it is the same fact the sweep
	// withdrawing it would have been: filed as the target's, and waited on.
	if checks.BehindBy == 0 && checks.Red() && !checks.ChangeFails() && !checks.FailedInTheJob() && r.Filer != nil {
		waiting, err := r.waitOnRedTarget(ctx, *state, checks, reading.Files, true)
		return waiting, true, err
	}
	if checks.BehindBy == 0 || checks.ChangeFails() {
		return Reconciliation{}, false, nil
	}
	result = reconciliationOf(*state, ActionQueued)
	result.Detail = fmt.Sprintf("the forge holds no merge for pull request %d any more; %s", published.Number, checks.Describe(target))
	updated, err := r.updateQueuedHead(ctx, *state, result, true)
	return updated, true, err
}

// unreplayable says why a queued run cannot be put back at its promotion, and
// nothing for one that can.
func unreplayable(state runstate.State) string {
	switch {
	case !state.Integration.ThroughPullRequest:
		return "its change was promoted onto the local target already, so there is no unlanded head to bring up to date"
	case state.WorktreePath == "" || state.WorktreeRemoved:
		return "its worktree is gone"
	case state.Branch == "" || state.BranchRemoved:
		return "its branch is gone"
	case state.ReviewDecision != runstate.ReviewApprove || strings.TrimSpace(state.ReviewSessionID) == "":
		return "its record carries no standing approval"
	case strings.TrimSpace(state.ProviderSessionID) == "":
		return "its record names no developer session"
	case state.ResumptionsLeft() == 0:
		return fmt.Sprintf("its promotion has been resumed %d times, which is the record's bound", runstate.MaxIntegrationResumptions)
	}
	return ""
}

// boundedReason keeps a resumption's reason inside the record's bound.
func boundedReason(reason string) string {
	return oneline.Bound(reason, runstate.MaxSelectionReasonBytes)
}

// slotFree counts the runs in flight against the configured limit, from the same
// records the reservation counts. A reconciler with no limit set has no room.
func (r Reconciler) slotFree() (bool, error) {
	if r.Capacity <= 0 {
		return false, nil
	}
	outstanding, err := r.Store.Outstanding()
	if err != nil {
		return false, err
	}
	inFlight := 0
	for _, state := range outstanding {
		if state.Status.InFlight() {
			inFlight++
		}
	}
	return inFlight < r.Capacity, nil
}

// renderQueuedUpdateNotes tells the work item its queued merge was withdrawn —
// or found dropped by the forge — to bring its head up to date.
func renderQueuedUpdateNotes(state runstate.State, reason string, dropped bool, account string) string {
	headline := "Yoyodyne withdrew the merge this run left queued with the forge, to bring its head up to date onto the target."
	if dropped {
		headline = "Yoyodyne found the merge this run left queued dropped by the forge while its head was behind the target, and is bringing the head up to date from the run's kept branch rather than handing the item back."
	}
	lines := []string{
		headline,
		"Outcome: " + reason,
		"Run: " + state.RunID,
		fmt.Sprintf("Pull request: #%d %s", state.PullRequest.Number, state.PullRequest.URL),
		"The change is replayed onto the target, checked and reviewed again, and its merge queued again by the same run; if the replay conflicts or the review does not approve it, the run stops and the item is handed back as any run's is.",
	}
	if account != "" {
		lines = append(lines, "", account)
	}
	return strings.Join(lines, "\n")
}

// updatingQueuedHead reports a run the sweep put back at its promotion to bring
// its queued head up to date, and that nothing has taken up yet.
func updatingQueuedHead(state runstate.State) bool {
	return resumableIntegration(state) && state.UpdatingQueuedHead()
}

// UpdateContinuation is what the sweep did about one run it put back at its
// promotion to bring a queued head up to date.
type UpdateContinuation struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	// Continued reports the run handed to its pipeline. A run left where it was
	// says why in Detail and is not a failure.
	Continued bool     `json:"continued"`
	Detail    string   `json:"detail,omitempty"`
	Outcome   *Outcome `json:"outcome,omitempty"`
	Failure   string   `json:"failure,omitempty"`
}

// ContinueUpdates hosts every run the sweep put back at its promotion to bring a
// queued head up to date and that no process holds. It is the same hosting
// ContinueWaits gives a wait: the run's own pipeline re-enters the run named,
// through Pipeline.Continue, in its own worktree — where the promotion finds the
// target moved, replays the change onto it, and re-earns the gate. A sweep wired
// with no Continue takes this step as a reading and continues nothing.
//
// The run is asked for under its lease and continued after the lease is
// released, because continuing it is the pipeline adopting it. A run a process
// holds is that process's; one that is no longer waiting at its promotion is
// left as it stands.
func (r Reconciler) ContinueUpdates(ctx context.Context) ([]UpdateContinuation, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	if r.Continue == nil {
		return nil, nil
	}
	outstanding, err := r.Store.Outstanding()
	if err != nil {
		return nil, fmt.Errorf("discover outstanding runs: %w", err)
	}
	var results []UpdateContinuation
	var launched []int
	for _, recorded := range outstanding {
		if !updatingQueuedHead(recorded) {
			continue
		}
		result := UpdateContinuation{RunID: recorded.RunID, WorkItemID: recorded.WorkItemID}
		state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
		switch {
		case errors.Is(err, runstate.ErrRunHeld):
			result.Detail = "a live process holds this run, so it is updating it itself"
		case err != nil:
			result.Failure = fmt.Errorf("adopt run %s: %w", recorded.RunID, err).Error()
		case !updatingQueuedHead(state):
			lease.Release()
			result.Detail = "the run is no longer waiting at its promotion, so it is left as it stands"
		default:
			lease.Release()
			result.Continued = true
			result.Detail = state.IntegrationResumptions[len(state.IntegrationResumptions)-1].Reason
		}
		results = append(results, result)
		if result.Continued {
			launched = append(launched, len(results)-1)
		}
	}
	var wait sync.WaitGroup
	for _, index := range launched {
		wait.Add(1)
		go func(result *UpdateContinuation) {
			defer wait.Done()
			outcome, err := r.Continue(ctx, result.WorkItemID, result.RunID)
			if err == nil || outcome.RunID != "" || outcome.Paused {
				result.Outcome = &outcome
			}
			if err != nil {
				result.Failure = err.Error()
			}
		}(&results[index])
	}
	wait.Wait()
	return results, nil
}

var _ ReconcileChecks = publish.GitHub{}
