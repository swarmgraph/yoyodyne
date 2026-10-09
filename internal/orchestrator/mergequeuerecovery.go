package orchestrator

// The merge queue's withdrawal and failure recovery: the "Failure, withdrawal
// and continuation" and "Crash recovery" sections of
// docs/designs/integration-through-a-merge-queue.md, for the harness's own
// queue.
//
// Withdrawal comes first because everything else depends on it. A merge the
// forge holds queued lands the candidate the moment the forge's own
// requirements are met, so before the change is handed to repair — whose next
// head is a change no reviewer has seen — the merge is taken back with the
// operation every other hand-back uses (DisableAutoMerge: auto-merge off, then
// out of the forge's queue). The withdrawal is written down before the forge
// is asked, and settled only from the pull request's own state afterwards:
// confirmed where the forge answered and the request is still unmerged, landed
// where it merged first. Anything else — a refusal, an answer that does not
// say, a request that cannot be read — leaves the withdrawal standing, and
// while it stands nothing rewrites the change's head, hands it back, or admits
// it again (runstate's MergeQueueLanding.HeadRewriteRefusal). A later call
// reads the pull request before asking again, so a landing that raced the
// withdrawal is completed through the promotion's own confirmed-landing path,
// once, and never replayed as fresh work.
//
// Recovery then reads what the failed candidate says. Its generation keeps its
// base, its heads in order, its binding, its check results and its review, and
// nothing here removes any of them. A failure is the change's own only where
// the evidence is whole: every configured check ran to its own end, or the
// reviewer answered. A check or review that judged nothing is infrastructure;
// a target that moved is drift; a check the target is already known red on —
// an unfinished item a red landing filed for the same check on the same branch
// — is the target's, and the entry waits on that item rather than filing
// another; evidence that cannot be read says nothing either way. None of those
// is charged to the run, and none ends the entry's turn: the worker builds
// again on drift, runs the stage again after an infrastructure failure, and
// promotes nothing on unreadable evidence.
//
// A candidate defect ends the entry's turn with a handback naming the failed
// generation and the run's saved change. The run's own repair counters decide
// what follows, read and never granted: a run with attempts left is handed the
// failure for its repair, the same run with its same branch; one with none
// left is recorded exhausted, with its work kept, for the development manager.
// Either is handed to the run the way a red queued merge's change is
// (MergeQueueRunHandback): the failure and a blocker go on the run and the
// item, and the run goes on the docket, where the existing repair
// continuation carries the repair out on the same run and session.
//
// A merge withdrawn for anything other than a defect — a head about to be
// rewritten, an entry moving to the other queue mode — releases the entry
// (release): its turn ends naming the harness as who moves next, so it never
// stands at the head of the queue refusing every entry behind it.
// A candidate combining more than one head is not handed to any one of them:
// a forge annotation or log naming one head is evidence, not proof that head
// caused the failure. Restarting changes none of it — the handback is written
// once and read back, and the counters are the run's durable record.
//
// The harness's queue does not run the target's own checks, so a target that
// is red with no red-landing item filed for that check yet cannot be told from
// a defect of the change, and is read as one. The red landing of the target's
// own last change files that item, so this is a window rather than a standing
// gap; closing it would mean checking the base as well as the candidate.
//
// The forge's own queue builds and checks its own combined commit, and its
// failures are read from the forge's account of that commit; recovering an
// entry admitted there is not done here.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MergeQueueWithdrawer takes back a merge the forge holds for a pull request:
// auto-merge off, and the request out of the forge's merge queue. It refuses
// rather than reporting done where it cannot tell whether the queue still holds
// the request. It is satisfied by publish.GitHub.
type MergeQueueWithdrawer interface {
	DisableAutoMerge(ctx context.Context, number int) error
}

// MergeQueueRepair hands a defective candidate's run back: the same run, its
// saved change, and the failure as the handback records it, for a repair or,
// where the run's budget is spent, for the development manager to decide on.
// It charges nothing itself — the run's repair loop records its attempt as
// every repair does — and must hand the run back once however often it is
// asked, because a process can stop after handing it back and before that is
// written down. MergeQueueRunHandback is the harness's.
type MergeQueueRepair interface {
	HandBack(ctx context.Context, entry runstate.MergeQueueEntry, handback runstate.MergeQueueHandback) error
}

// MergeQueueRecovery is what one call to Recover found and did.
type MergeQueueRecovery struct {
	// Entry is the entry recovered, and zero when the queue has none unfinished.
	Entry runstate.MergeQueueEntry
	// Failure is what the entry's newest candidate says, and zero where it has
	// not failed.
	Failure MergeQueueFailure
	// Withdrawal is the withdrawal a candidate defect waited on, as far as it
	// went.
	Withdrawal MergeQueuePromotion
	// Handback is the entry handed back, where it was.
	Handback *runstate.MergeQueueHandback
}

// MergeQueueFailure is a failed candidate read for what it says.
type MergeQueueFailure struct {
	Class runstate.MergeQueueFailureClass
	// Generation is the candidate's generation, and zero where none could be
	// read.
	Generation runstate.MergeQueueGeneration
	// Check is the configured check the failure is about, where it is one.
	Check string
	// Attributed is a defect the evidence ties to the change of a single head.
	Attributed bool
	// WaitsOn is the unfinished item a target failure waits on.
	WaitsOn string
	// Mover is who moves next.
	Mover  ownership.Mover
	Reason string
}

// MergeQueueFailureEvidence is what a failed candidate is classified from.
type MergeQueueFailureEvidence struct {
	Generations []runstate.MergeQueueGeneration
	// ReadErr is the generations record failing to read.
	ReadErr error
	// Configured is the checks the project configures now.
	Configured runstate.MergeQueueCheckConfiguration
	// SuspectedHeads are heads a forge annotation or log names as the cause.
	// They are recorded in the reason and never attribute the failure.
	SuspectedHeads []string
	// TargetRed answers whether an unfinished item already records the target
	// red on a check, and names it.
	TargetRed func(check string) (string, error)
}

// ClassifyMergeQueueFailure reads the newest generation for what its failure
// says, and reports false where it has not failed: no candidate yet, one still
// being verified, or one that earned its gate.
func ClassifyMergeQueueFailure(evidence MergeQueueFailureEvidence) (MergeQueueFailure, bool, error) {
	if evidence.ReadErr != nil {
		return MergeQueueFailure{
			Class: runstate.MergeQueueUnreadableEvidence, Mover: ownership.MoverHarness,
			Reason: fmt.Sprintf("the candidate's evidence could not be read, so it says nothing either way and nothing is promoted on it: %v", evidence.ReadErr),
		}, true, nil
	}
	if len(evidence.Generations) == 0 {
		return MergeQueueFailure{}, false, nil
	}
	generation := evidence.Generations[len(evidence.Generations)-1]
	failure := MergeQueueFailure{Generation: generation, Mover: ownership.MoverHarness}
	switch invalidated := generation.Invalidated; {
	case invalidated != nil && invalidated.Reason == runstate.MergeQueueTargetMoved:
		failure.Class = runstate.MergeQueueTargetDrift
		failure.Reason = fmt.Sprintf("%s moved off generation %d's base %s to %s; the entry is built again on it and verified from nothing, and nothing is charged for it",
			generation.TargetBranch, generation.Number, generation.TargetBase, nonEmpty(invalidated.ObservedTarget, "a commit not recorded"))
		return failure, true, nil
	case invalidated != nil:
		// A changed check configuration or a lost candidate commit says nothing
		// about the change: the entry is verified again on a fresh candidate.
		failure.Class = runstate.MergeQueueInfrastructureFailure
		failure.Reason = fmt.Sprintf("generation %d stopped being promotable (%s), which says nothing about the change; the entry is built again and verified from nothing, and nothing is charged for it", generation.Number, invalidated.Reason)
		return failure, true, nil
	case !generation.Checks.Same(evidence.Configured):
		failure.Class = runstate.MergeQueueInfrastructureFailure
		failure.Reason = fmt.Sprintf("generation %d was verified by checks the project no longer configures, which says nothing about the change; it is verified again under the checks configured now, and nothing is charged for it", generation.Number)
		return failure, true, nil
	}
	if generation.Gate(evidence.Configured) == nil {
		return MergeQueueFailure{}, false, nil
	}
	checksRun := generation.CheckRun
	switch {
	case checksRun == nil || checksRun.FinishedAt == nil:
		return MergeQueueFailure{}, false, nil
	case checksRun.Binding != generation.Binding() || len(checksRun.Results) > len(generation.Checks.Commands):
		failure.Class = runstate.MergeQueueUnreadableEvidence
		failure.Reason = fmt.Sprintf("generation %d's check record is not whole for its candidate, so it says nothing either way", generation.Number)
		return failure, true, nil
	case checksRun.Problem != "":
		failure.Class = runstate.MergeQueueInfrastructureFailure
		failure.Reason = fmt.Sprintf("generation %d's checks judged nothing (%s); they are run again on the same candidate, and nothing is charged for it", generation.Number, checksRun.Problem)
		return failure, true, nil
	}
	for index, result := range checksRun.Results {
		switch {
		case result.Command != generation.Checks.Commands[index]:
			failure.Class = runstate.MergeQueueUnreadableEvidence
			failure.Reason = fmt.Sprintf("generation %d records %q where %q is configured, so its checks say nothing either way", generation.Number, result.Command, generation.Checks.Commands[index])
			return failure, true, nil
		case result.CouldNotRun != "":
			failure.Class, failure.Check = runstate.MergeQueueInfrastructureFailure, result.Command
			failure.Reason = fmt.Sprintf("%s could not run on generation %d's candidate (%s), so it judged nothing; it is run again, and nothing is charged for it", result.Command, generation.Number, result.CouldNotRun)
			return failure, true, nil
		case result.Passed:
			continue
		}
		failure.Check = result.Command
		if evidence.TargetRed != nil {
			item, err := evidence.TargetRed(result.Command)
			if err != nil {
				return MergeQueueFailure{}, false, fmt.Errorf("read whether %s is already known red on %s: %w", result.Command, generation.TargetBranch, err)
			}
			if item != "" {
				failure.Class, failure.WaitsOn = runstate.MergeQueueTargetFailure, item
				failure.Reason = fmt.Sprintf("%s fails on generation %d's candidate, and %s already records %s red on that check; the entry waits on that item, is built again once the target moves, and nothing is charged for it",
					result.Command, generation.Number, item, generation.TargetBranch)
				return failure, true, nil
			}
		}
		// A check that ran to its own end and failed is evidence enough, even
		// where it stopped the checks after it from running.
		failure.Reason = fmt.Sprintf("%s failed on generation %d's candidate, %s at %s with %s merged onto it",
			result.Command, generation.Number, generation.TargetBranch, generation.TargetBase, strings.Join(generation.Heads, ", "))
		return defect(failure, generation, evidence.SuspectedHeads), true, nil
	}
	if len(checksRun.Results) != len(generation.Checks.Commands) {
		failure.Class = runstate.MergeQueueUnreadableEvidence
		failure.Reason = fmt.Sprintf("%d of generation %d's %d configured checks have a result, and none of them failed, so the checks say nothing either way",
			len(checksRun.Results), generation.Number, len(generation.Checks.Commands))
		return failure, true, nil
	}
	verdict := generation.Review
	switch {
	case verdict == nil || verdict.FinishedAt == nil:
		return MergeQueueFailure{}, false, nil
	case verdict.Binding != generation.Binding():
		failure.Class = runstate.MergeQueueUnreadableEvidence
		failure.Reason = fmt.Sprintf("generation %d's review was recorded against another candidate, so it says nothing either way", generation.Number)
		return failure, true, nil
	case verdict.Problem != "":
		failure.Class = runstate.MergeQueueInfrastructureFailure
		failure.Reason = fmt.Sprintf("generation %d's review reached no verdict (%s); it is asked again, and nothing is charged for it", generation.Number, verdict.Problem)
		return failure, true, nil
	case verdict.Decision == "" || strings.TrimSpace(verdict.SessionID) == "" || verdict.SessionID == generation.AuthorSession:
		failure.Class = runstate.MergeQueueUnreadableEvidence
		failure.Reason = fmt.Sprintf("generation %d's review is not an independent verdict a failure can be read from", generation.Number)
		return failure, true, nil
	}
	failure.Reason = fmt.Sprintf("the independent reviewer decided %q on generation %d's candidate, %s at %s with %s merged onto it: %s",
		verdict.Decision, generation.Number, generation.TargetBranch, generation.TargetBase, strings.Join(generation.Heads, ", "), nonEmpty(verdict.Summary, "no summary was given"))
	return defect(failure, generation, evidence.SuspectedHeads), true, nil
}

// defect finishes a failure the evidence shows is the candidate's. A
// candidate of one head is that head on that base, so the failure is the
// change's; one combining several is the combination's, and a head an
// annotation or a log names is recorded as suspected and attributes nothing.
func defect(failure MergeQueueFailure, generation runstate.MergeQueueGeneration, suspected []string) MergeQueueFailure {
	failure.Class = runstate.MergeQueueCandidateDefect
	failure.Attributed = len(generation.Heads) == 1
	failure.Mover = ownership.MoverOf(domain.RoleDeveloper)
	if !failure.Attributed {
		failure.Mover = ownership.MoverDevelopmentManager
		failure.Reason += "; the candidate combines more than one head, and nothing attributes the failure to one of them"
	}
	if len(suspected) > 0 {
		failure.Reason += fmt.Sprintf("; the forge's account names %s, which is evidence of where to look and not proof of the cause", strings.Join(suspected, ", "))
	}
	return failure
}

// Withdraw takes back whatever the queue asked the forge for on an entry's
// behalf, so the change can be handed to repair, have its head rewritten, or
// move to another mode without the old request landing it. It reports
// Withdrawn once nothing can land the change; Landed where the merge landed
// first, which is completed exactly as any confirmed landing is; and
// Unresolved, with the withdrawal left standing, where the forge's answer did
// not settle it. It refuses an entry whose local target was already moved,
// because no withdrawal takes that back.
func (p MergeQueuePromoter) Withdraw(ctx context.Context, key runstate.MergeQueueKey, entryID, reason string) (MergeQueuePromotion, error) {
	if err := p.validate(); err != nil {
		return MergeQueuePromotion{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return MergeQueuePromotion{}, errors.New("a withdrawal says why it is made")
	}
	worker, held, err := p.Queue.LeaseWorker(ctx, key)
	if err != nil {
		return MergeQueuePromotion{}, err
	}
	if !held {
		return MergeQueuePromotion{}, ErrMergeQueueWorkerBusy
	}
	defer func() { _ = worker.Release() }()
	entry, landing, err := p.entry(key, entryID)
	if err != nil {
		return MergeQueuePromotion{}, err
	}
	q := &queuePromotion{p: p, worker: worker, key: key, entry: entry, landing: landing}
	q.outcome.Entry = entry
	if err := q.withdraw(ctx, reason); err != nil || !q.outcome.Withdrawn {
		return q.result(), err
	}
	return q.result(), q.release("its queued merge was withdrawn: " + reason)
}

// release ends the turn of an entry whose merge was withdrawn for something
// other than a defect, so it neither stands at the head of the queue refusing
// every later entry nor is asked for again. The harness moves next: whatever
// asked for the withdrawal — a head to rewrite, a move to the other queue
// mode — admits the change again once it is done. An entry already handed
// back keeps what it was given.
func (q *queuePromotion) release(why string) error {
	if q.landing.Handback != nil || q.landing.Completion != nil {
		return nil
	}
	if refusal := q.landing.HeadRewriteRefusal(); refusal != "" {
		return fmt.Errorf("merge queue entry %d cannot be released: %s", q.entry.Order, refusal)
	}
	q.landing.Handback = &runstate.MergeQueueHandback{
		At: q.now(), Continuation: runstate.MergeQueueReleased, Mover: ownership.MoverHarness,
		Reason:       oneline.Fold(why+"; the entry leaves the queue, and the change is admitted again once what the withdrawal was for is done", 1500),
		ApprovedHead: q.entry.ApprovedHead,
	}
	if err := q.save(); err != nil {
		q.landing.Handback = nil
		return err
	}
	return nil
}

// entry is one admitted entry and its landing record.
func (p MergeQueuePromoter) entry(key runstate.MergeQueueKey, entryID string) (runstate.MergeQueueEntry, runstate.MergeQueueLanding, error) {
	entries, err := p.Queue.Entries(key)
	if err != nil {
		return runstate.MergeQueueEntry{}, runstate.MergeQueueLanding{}, err
	}
	for _, entry := range entries {
		if entry.EntryID != entryID {
			continue
		}
		landing, found, err := p.Queue.Landing(key, entryID)
		if err != nil {
			return entry, runstate.MergeQueueLanding{}, err
		}
		if !found {
			landing = runstate.NewMergeQueueLanding(entry)
		}
		return entry, landing, nil
	}
	return runstate.MergeQueueEntry{}, runstate.MergeQueueLanding{}, fmt.Errorf("the merge queue for %s admits no entry %s", key.TargetBranch, entryID)
}

func (q *queuePromotion) result() MergeQueuePromotion {
	if attempt, ok := q.landing.Current(); ok {
		q.outcome.Attempt = attempt
		q.outcome.Landed = attempt.Landed != nil
	}
	q.outcome.Completed = q.landing.Completion != nil && q.landing.Completion.Whole()
	return q.outcome
}

// maxMergeQueueWithdrawSteps bounds the steps one withdrawal takes: an
// unsettled mutation observed, the merge withdrawn or found landed, and the
// landing completed.
const maxMergeQueueWithdrawSteps = 6

func (q *queuePromotion) withdraw(ctx context.Context, reason string) error {
	for step := 0; step < maxMergeQueueWithdrawSteps; step++ {
		if q.landing.Completion != nil {
			return q.recordCompletion(ctx)
		}
		attempt := q.attempt()
		if attempt == nil || attempt.SetAside != nil {
			q.outcome.Withdrawn = true
			return nil
		}
		if attempt.Landed != nil {
			if err := q.recordCompletion(ctx); err != nil {
				return err
			}
			continue
		}
		// A request whose answer was never written down is settled from what
		// the target and the forge show before anything is asked of them.
		if record, unsettled := attempt.Unsettled(); unsettled {
			if err := q.observe(ctx, record); err != nil || q.outcome.stopped() {
				return err
			}
			continue
		}
		if w := attempt.Withdrawal; w != nil {
			var err error
			switch {
			case w.Settled == nil:
				err = q.withdrawQueued(ctx)
			case w.Settled.Result == runstate.MergeQueueWithdrawalConfirmed:
				// Confirmed and written down, and the process stopped before the
				// attempt was set aside: nothing is asked of the forge again.
				if err = q.setAside(attempt, "its merge was withdrawn: "+w.Reason, false); err == nil {
					q.outcome.Withdrawn = true
				}
			default:
				// Found landed and written down, and the landing itself not yet:
				// it is confirmed through the promotion's own path.
				err = q.confirm(ctx)
			}
			if err != nil || q.outcome.stopped() || q.outcome.Withdrawn {
				return err
			}
			continue
		}
		moved, _ := attempt.Mutation(runstate.MergeQueueMoveTarget)
		requested, asked := attempt.Mutation(runstate.MergeQueueRequestMerge)
		switch {
		case moved.Settled != nil && moved.Settled.Result == runstate.MergeQueueMutationDone:
			q.outcome.Refusal = fmt.Sprintf("%s was already moved onto the candidate by promotion attempt %d, and no withdrawal takes that back; the attempt is finished as a landing", q.key.TargetBranch, attempt.Number)
			return nil
		case asked && requested.Settled.Result == runstate.MergeQueueMutationDone:
			// The forge said it merged, and the record lacks only the landing.
			if err := q.confirm(ctx); err != nil || q.outcome.stopped() {
				return err
			}
		case asked && requested.Settled.Result == runstate.MergeQueueMutationQueued:
			if q.p.Withdrawer == nil {
				q.outcome.Unresolved = fmt.Sprintf("the forge holds the merge of pull request %d queued, and nothing here can withdraw it, so the change is not handed back", attempt.PullRequest)
				return nil
			}
			// The intent is on the record before the forge is asked, so a process
			// that stops after asking leaves a withdrawal the next call settles.
			attempt.Withdrawal = &runstate.MergeQueueWithdrawal{
				Key: attempt.MergeQueueWithdrawalKey(), PullRequest: attempt.PullRequest, Pinned: attempt.Candidate,
				Reason: oneline.Fold(reason, 1500), IntendedAt: q.now(),
			}
			if err := q.save(); err != nil {
				attempt.Withdrawal = nil
				return err
			}
		default:
			// Nothing that could land the change was asked for: a pushed branch
			// and an open pull request with no merge requested land nothing.
			if err := q.setAside(attempt, "withdrawn before any merge was asked for: "+reason, false); err != nil {
				return err
			}
			q.outcome.Withdrawn = true
			return nil
		}
	}
	return nil
}

// withdrawQueued settles the attempt's standing withdrawal: it reads the pull
// request first, asks the forge to withdraw the merge, and reads the request
// again, and only those readings settle it.
func (q *queuePromotion) withdrawQueued(ctx context.Context) error {
	attempt := q.attempt()
	w := attempt.Withdrawal
	if q.p.Withdrawer == nil {
		q.outcome.Unresolved = fmt.Sprintf("the withdrawal of pull request %d's merge is not confirmed, and nothing here can ask the forge for it", w.PullRequest)
		return nil
	}
	// The request is read before the forge is asked again, so a merge that
	// landed while the withdrawal stood is completed rather than asked about.
	if before, err := q.p.Pipeline.Publisher.State(ctx, attempt.CandidateBranch); err == nil && before.Merged {
		return q.withdrawalLanded(ctx, before)
	}
	asked := q.p.Withdrawer.DisableAutoMerge(ctx, w.PullRequest)
	state, readErr := q.p.Pipeline.Publisher.State(ctx, attempt.CandidateBranch)
	switch {
	case readErr == nil && state.Merged:
		return q.withdrawalLanded(ctx, state)
	case readErr == nil && state.Number != w.PullRequest:
		q.outcome.Unresolved = fmt.Sprintf("%s is carried by pull request %d, not %d whose merge was asked for, so the withdrawal is not confirmed", attempt.CandidateBranch, state.Number, w.PullRequest)
		return nil
	case asked == nil && readErr == nil:
		w.Settled = &runstate.MergeQueueWithdrawalSettlement{
			Result: runstate.MergeQueueWithdrawalConfirmed, At: q.now(),
			Detail: fmt.Sprintf("the forge withdrew the merge and pull request %d reads %s, unmerged", w.PullRequest, strings.ToLower(nonEmpty(state.State, "unreported"))),
		}
		if err := q.save(); err != nil {
			return err
		}
		if err := q.setAside(attempt, "its merge was withdrawn: "+w.Reason, false); err != nil {
			return err
		}
		q.outcome.Withdrawn = true
		return nil
	}
	answer := runstate.MergeQueueWithdrawalAnswer{At: q.now()}
	if asked != nil {
		answer.Rejected = errors.Is(asked, publish.ErrForgeAccessRefused)
		answer.Detail = runstate.BoundedMergeQueueText(asked.Error())
	} else {
		answer.Detail = runstate.BoundedMergeQueueText(fmt.Sprintf("the forge withdrew the merge, and pull request %d could not be read to confirm it had not landed first: %v", w.PullRequest, readErr))
	}
	if len(w.Answers) < runstate.MaxMergeQueueWithdrawalAnswers {
		w.Answers = append(w.Answers, answer)
		if err := q.save(); err != nil {
			return err
		}
	}
	verb := "did not say whether it withdrew"
	if answer.Rejected {
		verb = "refused to withdraw"
	}
	q.outcome.Unresolved = fmt.Sprintf("the forge %s the merge of pull request %d (%s); it may still land, so the change is not handed back or rewritten until a later call confirms the withdrawal",
		verb, w.PullRequest, answer.Detail)
	return nil
}

// withdrawalLanded settles a withdrawal the forge's merge got to first, and
// confirms the landing through the promotion's own path.
func (q *queuePromotion) withdrawalLanded(ctx context.Context, state publish.PullRequest) error {
	w := q.attempt().Withdrawal
	w.Settled = &runstate.MergeQueueWithdrawalSettlement{
		Result: runstate.MergeQueueWithdrawalLanded, At: q.now(),
		Detail: fmt.Sprintf("the forge merged pull request %d before the withdrawal reached it", w.PullRequest),
	}
	if err := q.save(); err != nil {
		return err
	}
	return q.confirmWith(ctx, state.MergeCommit)
}

// Recover reads the first unfinished entry of a queue for what its newest
// candidate's failure says, and acts on a candidate defect: the queued merge
// is withdrawn, and the entry is handed back for its run's repair or recorded
// exhausted, as the run's own repair counters say. Every other class is
// reported and charges nothing. An entry already handed back is the same
// handback read again, and is given to its run where it has not been yet.
func (p MergeQueuePromoter) Recover(ctx context.Context, key runstate.MergeQueueKey) (MergeQueueRecovery, error) {
	if err := p.validate(); err != nil {
		return MergeQueueRecovery{}, err
	}
	worker, held, err := p.Queue.LeaseWorker(ctx, key)
	if err != nil {
		return MergeQueueRecovery{}, err
	}
	if !held {
		return MergeQueueRecovery{}, ErrMergeQueueWorkerBusy
	}
	defer func() { _ = worker.Release() }()

	if pending, found, err := p.pendingHandback(key); err != nil || found {
		if err != nil {
			return MergeQueueRecovery{}, err
		}
		q := &queuePromotion{p: p, worker: worker, key: key, entry: pending.entry, landing: pending.landing}
		recovered := MergeQueueRecovery{Entry: pending.entry, Handback: q.landing.Handback}
		return recovered, q.handBack(ctx)
	}
	entry, landing, found, err := p.next(key)
	if err != nil || !found {
		return MergeQueueRecovery{}, err
	}
	recovered := MergeQueueRecovery{Entry: entry}
	// An entry whose merge was withdrawn and that nothing has handed back or
	// released yet — a Withdraw or a promotion that stopped short of recording
	// what followed — is decided here and never left refusing the entries
	// behind it: handed back where its candidate is a defect, released
	// otherwise. The promotion cannot tell the two apart, so it is decided here.
	releaseIfWithdrawn := func(why string) (MergeQueueRecovery, error) {
		attempt, ok := landing.Current()
		if !ok || !attempt.Withdrawn() || landing.Handback != nil {
			return recovered, nil
		}
		q := &queuePromotion{p: p, worker: worker, key: key, entry: entry, landing: landing}
		if err := q.release("its queued merge was withdrawn (" + attempt.Withdrawal.Reason + ") and " + why); err != nil {
			return recovered, err
		}
		recovered.Handback = q.landing.Handback
		return recovered, nil
	}
	if entry.Mode != runstate.MergeQueueHarness {
		return releaseIfWithdrawn("the entry was in the forge's own queue, whose failures are not read here")
	}
	generations, readErr := p.Queue.Generations(key, entry.EntryID)
	failure, failed, err := ClassifyMergeQueueFailure(MergeQueueFailureEvidence{
		Generations: generations, ReadErr: readErr,
		Configured: runstate.NewMergeQueueCheckConfiguration(p.Pipeline.Config.Checks),
		TargetRed:  p.targetRed(ctx, key.TargetBranch),
	})
	if err != nil {
		return recovered, err
	}
	if !failed {
		return releaseIfWithdrawn("its candidate has not failed")
	}
	recovered.Failure = failure
	q := &queuePromotion{p: p, worker: worker, key: key, entry: entry, landing: landing}
	q.outcome.Entry = entry
	if failure.Class != runstate.MergeQueueCandidateDefect {
		// A merge withdrawn by a recovery that stopped before it recorded the
		// handback, on evidence that no longer reads as a defect, is released
		// rather than left refusing every entry behind it.
		if attempt, ok := landing.Current(); ok && attempt.Withdrawn() {
			if err := q.release("its queued merge was withdrawn for a defect its evidence no longer shows (" + failure.Reason + ")"); err != nil {
				return recovered, err
			}
			recovered.Handback = q.landing.Handback
		}
		return recovered, nil
	}
	err = q.withdraw(ctx, "generation "+fmt.Sprint(failure.Generation.Number)+" is defective: "+failure.Reason)
	recovered.Withdrawal = q.result()
	if err != nil || !q.outcome.Withdrawn {
		return recovered, err
	}
	if refusal := q.landing.HeadRewriteRefusal(); refusal != "" {
		return recovered, fmt.Errorf("merge queue entry %d is withdrawn and still refuses its hand-back: %s", entry.Order, refusal)
	}
	q.landing.Handback = q.decide(failure)
	if err := q.save(); err != nil {
		q.landing.Handback = nil
		return recovered, err
	}
	recovered.Handback = q.landing.Handback
	return recovered, q.handBack(ctx)
}

type pendingHandback struct {
	entry   runstate.MergeQueueEntry
	landing runstate.MergeQueueLanding
}

// pendingHandback is the first entry handed back for repair whose run has not
// been given it yet: a process that stopped between the two writes.
func (p MergeQueuePromoter) pendingHandback(key runstate.MergeQueueKey) (pendingHandback, bool, error) {
	if p.Repair == nil {
		return pendingHandback{}, false, nil
	}
	entries, err := p.Queue.Entries(key)
	if err != nil {
		return pendingHandback{}, false, err
	}
	for _, entry := range entries {
		landing, found, err := p.Queue.Landing(key, entry.EntryID)
		if err != nil {
			return pendingHandback{}, false, err
		}
		if found && landing.Handback != nil && landing.Handback.Continuation.HandsBackToRun() && landing.Handback.HandedBackAt == nil {
			return pendingHandback{entry: entry, landing: landing}, true, nil
		}
	}
	return pendingHandback{}, false, nil
}

// targetRed looks for the unfinished item a red landing filed for a check on
// the target, which is the record existing triage keeps of a target known red.
func (p MergeQueuePromoter) targetRed(ctx context.Context, target string) func(string) (string, error) {
	if p.Pipeline.Filer == nil {
		return nil
	}
	return func(check string) (string, error) {
		return openItemMarked(ctx, p.Pipeline.Filer, redLandingMarker(target, check))
	}
}

// decide is the handback for a defective candidate, from the run's durable
// record as it stands. Nothing here grants an attempt: the counters are read.
func (q *queuePromotion) decide(failure MergeQueueFailure) *runstate.MergeQueueHandback {
	generation := failure.Generation
	handback := &runstate.MergeQueueHandback{
		At: q.now(), Class: failure.Class, Reason: oneline.Fold(failure.Reason, 1500),
		Generation: generation.Number, Binding: generation.Binding(), TargetBase: generation.TargetBase,
		Heads: append([]string(nil), generation.Heads...), Candidate: generation.Candidate,
		ApprovedHead: q.entry.ApprovedHead,
	}
	missing := func(why string) *runstate.MergeQueueHandback {
		handback.Continuation, handback.Mover = runstate.MergeQueueMissingPrerequisite, ownership.MoverDevelopmentManager
		handback.Reason = oneline.Fold(failure.Reason+"; "+why+", so it is kept where it is for the development manager", 1500)
		return handback
	}
	if !failure.Attributed {
		handback.Continuation, handback.Mover = runstate.MergeQueueUnattributed, ownership.MoverDevelopmentManager
		return handback
	}
	run, err := q.p.Pipeline.Store.Load(q.entry.RunID)
	if err != nil {
		return missing(fmt.Sprintf("run %s's record could not be read (%v)", q.entry.RunID, err))
	}
	handback.Branch = run.Branch
	handback.RepairAttempts = run.RepairAttempts
	handback.RepairBudget = run.RepairBudget(q.p.Pipeline.Config.Execution.RepairAttemptsBeforeReplan)
	switch {
	case strings.TrimSpace(run.Branch) == "":
		return missing(fmt.Sprintf("run %s records no branch holding its change", q.entry.RunID))
	case strings.TrimSpace(run.ProviderSessionID) == "":
		return missing(fmt.Sprintf("run %s records no developer session to repair its change in", q.entry.RunID))
	case run.RepairAttempts >= handback.RepairBudget:
		handback.Continuation, handback.Mover = runstate.MergeQueueBudgetExhausted, ownership.MoverDevelopmentManager
		handback.Reason = oneline.Fold(fmt.Sprintf("%s; run %s has spent %d of its %d repair attempts, so its change is kept on %s for the development manager",
			failure.Reason, q.entry.RunID, run.RepairAttempts, handback.RepairBudget, run.Branch), 1500)
		return handback
	}
	handback.Continuation, handback.Mover = runstate.MergeQueueContinueRepair, failure.Mover
	return handback
}

// handBack gives a repair handback to its run, once, and writes down that it
// was given.
func (q *queuePromotion) handBack(ctx context.Context) error {
	handback := q.landing.Handback
	if handback == nil || !handback.Continuation.HandsBackToRun() || handback.HandedBackAt != nil || q.p.Repair == nil {
		return nil
	}
	if err := q.p.Repair.HandBack(ctx, q.entry, *handback); err != nil {
		return fmt.Errorf("hand merge queue entry %d back to run %s for repair: %w", q.entry.Order, q.entry.RunID, err)
	}
	at := q.now()
	handback.HandedBackAt = &at
	return q.save()
}

// MergeQueueRunHandback hands a defective candidate's run back the way a red
// queued merge's change is handed back (handBackFailedChange): the failure is
// recorded on the run as the check that failed, the work item is blocked on
// it, and the stopped run goes on the docket. From there it is the existing
// repair continuation that carries the repair out on the same run, branch, and
// developer session, under the item's existing grants and the run's existing
// repair count; nothing here grants an attempt or invokes a developer.
type MergeQueueRunHandback struct {
	Store   StateStore
	Tracker WorkTracker
	// Docket is where the stopped run is put for the development manager;
	// nil dockets nothing.
	Docket *Docketer
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

var _ MergeQueueRepair = MergeQueueRunHandback{}

// mergeQueueHandbackMarker is the line a hand-back writes on the run's failure
// and blocker, which is how a second hand-back of the same candidate finds the
// first already made.
func mergeQueueHandbackMarker(entry runstate.MergeQueueEntry, handback runstate.MergeQueueHandback) string {
	return fmt.Sprintf("Merge queue entry %s, generation %d (%s)", entry.EntryID, handback.Generation, handback.Candidate)
}

func (h MergeQueueRunHandback) HandBack(ctx context.Context, entry runstate.MergeQueueEntry, handback runstate.MergeQueueHandback) error {
	if h.Store == nil || h.Tracker == nil {
		return errors.New("handing a merge queue entry back needs the run store and the tracker")
	}
	state, err := h.Store.Load(entry.RunID)
	if err != nil {
		return fmt.Errorf("read run %s: %w", entry.RunID, err)
	}
	marker := mergeQueueHandbackMarker(entry, handback)
	if failure := state.CheckFailure; failure != nil && strings.Contains(failure.Output, marker) && strings.TrimSpace(state.Blocker) != "" {
		return h.docket(state)
	}
	item, err := h.Tracker.Show(ctx, entry.WorkItemID)
	if err != nil {
		return fmt.Errorf("read %s: %w", entry.WorkItemID, err)
	}
	notes := fmt.Sprintf("The merge queue handed this change back. %s\n%s\nRun %s keeps its branch %s and its approved head %s.",
		handback.Reason, marker, entry.RunID, nonEmpty(handback.Branch, "(none recorded)"), handback.ApprovedHead)
	if handback.Continuation == runstate.MergeQueueBudgetExhausted {
		notes += fmt.Sprintf(" It has spent %d of its %d repair attempts, so what follows is the development manager's decision.", handback.RepairAttempts, handback.RepairBudget)
	}
	if item.Status == "blocked" {
		_, err = h.Tracker.RecordOutcome(ctx, entry.WorkItemID, notes)
	} else {
		_, err = h.Tracker.Block(ctx, entry.WorkItemID, notes)
	}
	if err != nil {
		return fmt.Errorf("block %s on the merge queue's hand-back: %w", entry.WorkItemID, err)
	}
	command := "the merge queue candidate's review"
	if check := strings.TrimSpace(handbackCheck(handback.Reason)); check != "" {
		command = check
	}
	state.CheckFailure = &runstate.CheckFailure{Command: command, ExitCode: 1, Output: boundedTail(marker+"\n"+handback.Reason, runstate.MaxCheckOutputBytes)}
	state.Blocker = runstate.RecordBlocker(notes)
	now := h.now()
	// A run admitted to the queue ended succeeded once its change was approved;
	// the change has not landed, so it is a stopped run now, failed as a run the
	// harness stops is, which is the status the docket and the repair
	// continuation take a stoppage from. A cancelled run keeps its own account.
	if !state.Status.Terminal() || state.Status == runstate.StatusSucceeded {
		state.Status = runstate.StatusFailed
		state.CompletedAt = &now
	}
	if strings.TrimSpace(state.Failure) == "" {
		state.Failure = runstate.RecordFailure("the merge queue handed the change back: " + handback.Reason)
	}
	state.UpdatedAt = now
	if err := h.Store.Save(state); err != nil {
		return fmt.Errorf("record the hand-back on run %s: %w", entry.RunID, err)
	}
	return h.docket(state)
}

func (h MergeQueueRunHandback) docket(state runstate.State) error {
	if h.Docket == nil {
		return nil
	}
	if _, err := h.Docket.RecordStoppedRun(state); err != nil {
		return fmt.Errorf("docket the stopped run %s: %w", state.RunID, err)
	}
	return nil
}

func (h MergeQueueRunHandback) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// handbackCheck is the configured check a handback's reason names as failing,
// which every check failure's reason opens with; a reviewer's verdict names
// none.
func handbackCheck(reason string) string {
	if command, _, found := strings.Cut(reason, " failed on generation "); found {
		return command
	}
	return ""
}
