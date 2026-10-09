package orchestrator

// The merge queue's promotion: the "Harness-run queue" section of
// docs/designs/integration-through-a-merge-queue.md from "Before promotion"
// on, and its "Crash recovery" section. The worker (mergequeueworker.go)
// leaves a generation whose checks and independent review are bound to one
// candidate; this lands exactly that candidate, or lands nothing.
//
// A promotion holds the queue's worker lease for the whole call, so it is the
// one process landing, verifying, or recording anything for the queue. It
// takes the target's promotion lease only around a mutation of the target —
// moving the local target, asking the forge to merge, and fast-forwarding the
// local target onto what the forge landed — and under it reads everything
// again: the operator's pause, the directives and dependencies of the item,
// the integration approval policy, how the target lands, the generation's
// gate, and where the target stands. Publishing the candidate and opening its
// pull request touch no target and happen outside it; so does every wait on
// the forge, and nothing here runs a check or asks a provider anything.
//
// Every mutation is written down, with the commit it is pinned to and a key
// that names it, before it is requested, and settled once its result is known
// (runstate's mergequeuelanding.go). A call that finds a mutation an earlier
// process requested and never settled observes the target, the candidate
// branch, and the forge before anything else, and settles it from what it
// finds. What observation cannot establish is left standing and reported: it
// is never requested again on a guess. A landing that is confirmed completes
// the entry, the run, and the work item, each recorded as it is made, so a
// call that finds the completion partial makes only what is missing and
// nothing that came before it — no development, check, review, or merge — is
// done twice.
//
// The target moving off the generation's base is drift: the attempt is set
// aside, the generation invalidated, and the entry is verified again from
// nothing on the new target by the worker. Nothing about the run is charged
// for it. A refusal, a merge the forge stopped holding, and a remote target
// that moved after the local one was moved are retained as they are and
// reported. Withdrawing a queued merge and deciding what a failed candidate
// costs are mergequeuerecovery.go's; moving an entry between modes belongs to
// later work.
//
// Nothing in the harness calls this yet.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/queuemode"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MergeQueueLander is the part of the worktree manager a promotion moves and
// reads the target and the candidate branch with. It is satisfied by
// *gitworktree.Manager.
type MergeQueueLander interface {
	TargetCommit(ctx context.Context, branch string) (string, error)
	TargetHolds(ctx context.Context, branch, commit string) (bool, error)
	ValidateReady(ctx context.Context) error
	PromoteQueueCandidate(ctx context.Context, branch, base, candidate string) error
	QueueCandidateBranchCommit(ctx context.Context, branch string) (string, bool, error)
	PublishQueueCandidate(ctx context.Context, branch, candidate, expected string) error
	VerifyRemoteTarget(ctx context.Context, integration gitworktree.Integration) error
	ConfirmRemoteTarget(ctx context.Context, integration gitworktree.Integration, mergeCommit string) (string, error)
	CatchUpTarget(ctx context.Context, targetBranch string) (gitworktree.Catchup, error)
	CandidatePaths(ctx context.Context, base, candidate string) ([]string, error)
}

// MergeQueueLandingRecords is the queue's durable half a promotion works
// from. It is satisfied by *runstate.MergeQueueStore.
type MergeQueueLandingRecords interface {
	Entries(key runstate.MergeQueueKey) ([]runstate.MergeQueueEntry, error)
	LeaseWorker(ctx context.Context, key runstate.MergeQueueKey) (*runstate.Lease, bool, error)
	Generations(key runstate.MergeQueueKey, entryID string) ([]runstate.MergeQueueGeneration, error)
	RecordGeneration(worker *runstate.Lease, key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration) error
	Landing(key runstate.MergeQueueKey, entryID string) (runstate.MergeQueueLanding, bool, error)
	RecordLanding(worker *runstate.Lease, key runstate.MergeQueueKey, landing runstate.MergeQueueLanding) error
}

// MergeQueueCompletion records a confirmed landing outside the queue: on the
// run the change was made by, and on its work item. Each is asked only until
// the landing record says it was made, and each must make its record once
// however often it is asked, because a process can die after making it and
// before that is written down.
type MergeQueueCompletion interface {
	RecordRun(ctx context.Context, entry runstate.MergeQueueEntry, completion runstate.MergeQueueCompletion) error
	RecordWorkItem(ctx context.Context, entry runstate.MergeQueueEntry, completion runstate.MergeQueueCompletion) error
}

// MergeQueuePromoter lands the first unfinished entry of one queue.
type MergeQueuePromoter struct {
	// Pipeline supplies what a run's promotion reads and holds: the run store
	// and its promotion lease, the operator's pause, the directives, the
	// tracker, the configuration's approval policies, the forge, and the
	// clock.
	Pipeline *Pipeline
	Queue    MergeQueueLandingRecords
	Lander   MergeQueueLander
	// Forge is asked again, before an entry admitted to the forge's own queue
	// is handed to it, whether that queue still enforces the whole gate; nil
	// hands nothing to it.
	Forge   publish.QueueCapabilityReader
	Harness queuemode.Harness
	// Completion records a landing on the run and the work item.
	Completion MergeQueueCompletion
	// Withdrawer takes back a merge the forge holds queued, before the change
	// is handed to repair (mergequeuerecovery.go); nil withdraws nothing, and
	// a queued merge then stays armed and refuses its hand-back.
	Withdrawer MergeQueueWithdrawer
	// Repair hands a defective candidate's run back to its own repair loop;
	// nil records the decision and leaves the hand-back for a later call.
	Repair MergeQueueRepair
}

// MergeQueuePromotion is what one call to Promote found and did. At most one
// of Refusal, Waiting, and Unresolved is set, and none of them is a failure:
// each is a reason nothing more was done this call.
type MergeQueuePromotion struct {
	// Entry is the entry worked, and zero when the queue has none unfinished.
	Entry runstate.MergeQueueEntry
	// Attempt is the entry's newest promotion attempt as the call left it.
	Attempt runstate.MergeQueuePromotionAttempt
	// Landed is the attempt's landing confirmed, by this call or an earlier one.
	Landed bool
	// Completed is every part of the entry's completion recorded.
	Completed bool
	// Drift is the target having moved off the generation's base. The worker
	// verifies the entry again on the new target; nothing is charged for it.
	Drift bool
	// Refusal is why nothing was mutated: evidence that is missing or not the
	// candidate's, a pause, a directive, a dependency, or a policy.
	Refusal string
	// Waiting is a landing the forge holds and has not made yet.
	Waiting string
	// Unresolved is a mutation whose outcome is retained as it stands, because
	// observation did not establish it or it is for later work to settle.
	Unresolved string
	// Withdrawn is nothing the queue asked for able to land the change any
	// more: no merge was asked for, or the forge confirmed it withdrawn.
	Withdrawn bool
}

func (o MergeQueuePromotion) stopped() bool {
	return o.Drift || o.Refusal != "" || o.Waiting != "" || o.Unresolved != ""
}

// Promote lands the first entry of the queue the queue has not finished with,
// or takes up where an earlier call stopped with it. It refuses with
// ErrMergeQueueWorkerBusy while another process works the queue. An error is a
// failure to promote — a record that would not save, a target or forge that
// would not answer — and leaves what was recorded for the next call to
// observe; a mutation it had begun is never asked for again until that
// observation settles it.
func (p MergeQueuePromoter) Promote(ctx context.Context, key runstate.MergeQueueKey) (MergeQueuePromotion, error) {
	if err := p.validate(); err != nil {
		return MergeQueuePromotion{}, err
	}
	worker, held, err := p.Queue.LeaseWorker(ctx, key)
	if err != nil {
		return MergeQueuePromotion{}, err
	}
	if !held {
		return MergeQueuePromotion{}, ErrMergeQueueWorkerBusy
	}
	defer func() { _ = worker.Release() }()

	entry, landing, found, err := p.next(key)
	if err != nil || !found {
		return MergeQueuePromotion{}, err
	}
	q := &queuePromotion{p: p, worker: worker, key: key, entry: entry, landing: landing}
	q.outcome.Entry = entry
	err = q.promote(ctx)
	if attempt, ok := q.landing.Current(); ok {
		q.outcome.Attempt = attempt
		q.outcome.Landed = attempt.Landed != nil
	}
	q.outcome.Completed = q.landing.Completion != nil && q.landing.Completion.Whole()
	return q.outcome, err
}

// MergeQueueWaiting is the worker's Waiting for a queue a promoter lands: an
// entry waits to be verified until a promotion of it is under way or its
// landing or its handback is recorded. An attempt the promoter set aside is no
// longer under way, so the entry waits again, except one whose merge was
// withdrawn: that entry waits for its recovery to be decided instead. A record
// that cannot be read is taken as waiting, because verifying an entry moves
// nothing.
func MergeQueueWaiting(records MergeQueueLandingRecords) func(runstate.MergeQueueEntry) bool {
	return func(entry runstate.MergeQueueEntry) bool {
		landing, found, err := records.Landing(entry.Key(), entry.EntryID)
		if err != nil || !found {
			return true
		}
		attempt, attempted := landing.Current()
		return landing.Waiting() && (!attempted || (attempt.SetAside != nil && !attempt.Withdrawn()))
	}
}

func (p MergeQueuePromoter) validate() error {
	var problems []error
	switch {
	case p.Pipeline == nil:
		problems = append(problems, errors.New("the merge queue promoter needs the pipeline whose leases, holds, and policies it promotes under"))
	case p.Pipeline.Store == nil || p.Pipeline.Tracker == nil || p.Pipeline.Holds == nil || p.Pipeline.Directives == nil:
		problems = append(problems, errors.New("the merge queue promoter needs a run store, a tracker, the operator's pause on harness activity, and the directives"))
	}
	if p.Queue == nil || p.Lander == nil || p.Completion == nil {
		problems = append(problems, errors.New("the merge queue promoter needs the queue's records, a way to move the target, and somewhere to record completion"))
	}
	return errors.Join(problems...)
}

// next is the first entry in admission order whose completion is not whole
// and that was not handed back, with its landing record.
func (p MergeQueuePromoter) next(key runstate.MergeQueueKey) (runstate.MergeQueueEntry, runstate.MergeQueueLanding, bool, error) {
	entries, err := p.Queue.Entries(key)
	if err != nil {
		return runstate.MergeQueueEntry{}, runstate.MergeQueueLanding{}, false, err
	}
	for _, entry := range entries {
		landing, found, err := p.Queue.Landing(key, entry.EntryID)
		if err != nil {
			return entry, runstate.MergeQueueLanding{}, false, err
		}
		if !found {
			return entry, runstate.NewMergeQueueLanding(entry), true, nil
		}
		if landing.Handback != nil {
			continue
		}
		if landing.Completion == nil || !landing.Completion.Whole() {
			return entry, landing, true, nil
		}
	}
	return runstate.MergeQueueEntry{}, runstate.MergeQueueLanding{}, false, nil
}

// queuePromotion is one call's work on one entry.
type queuePromotion struct {
	p       MergeQueuePromoter
	worker  *runstate.Lease
	key     runstate.MergeQueueKey
	entry   runstate.MergeQueueEntry
	landing runstate.MergeQueueLanding
	outcome MergeQueuePromotion
}

func (q *queuePromotion) now() time.Time {
	return q.p.Pipeline.clock().Now().UTC()
}

// save writes the landing record whole. Nothing is requested on the strength
// of a write that did not save: an error here returns before the request.
func (q *queuePromotion) save() error {
	if err := q.p.Queue.RecordLanding(q.worker, q.key, q.landing); err != nil {
		return fmt.Errorf("record the landing of merge queue entry %d: %w", q.entry.Order, err)
	}
	return nil
}

func (q *queuePromotion) attempt() *runstate.MergeQueuePromotionAttempt {
	if len(q.landing.Attempts) == 0 {
		return nil
	}
	return &q.landing.Attempts[len(q.landing.Attempts)-1]
}

// maxMergeQueueBegins bounds the attempts one call intends: the first, and one
// more where the newest was set aside because an earlier process stopped
// before it asked for anything.
const maxMergeQueueBegins = 2

func (q *queuePromotion) promote(ctx context.Context) error {
	begun := 0
	for {
		attempt := q.attempt()
		if attempt != nil && attempt.Withdrawal != nil && attempt.Withdrawal.Settled == nil {
			// A withdrawal that was asked for is finished before anything else:
			// the merge it takes back is what decides whether the attempt lands.
			if err := q.withdrawQueued(ctx); err != nil || q.outcome.stopped() || q.outcome.Withdrawn {
				return err
			}
			continue
		}
		if attempt != nil && attempt.Withdrawn() {
			q.outcome.Refusal = fmt.Sprintf("the merge of promotion attempt %d was withdrawn (%s), and nothing is asked for again until the entry's recovery is decided",
				attempt.Number, attempt.Withdrawal.Reason)
			return nil
		}
		if attempt == nil || attempt.SetAside != nil {
			if begun == maxMergeQueueBegins {
				return nil
			}
			begun++
			if err := q.begin(ctx); err != nil || q.outcome.stopped() {
				return err
			}
			continue
		}
		var err error
		if record, unsettled := attempt.Unsettled(); unsettled {
			err = q.observe(ctx, record)
		} else if attempt.Landed != nil {
			return q.recordCompletion(ctx)
		} else {
			err = q.proceed(ctx)
		}
		if err != nil || q.outcome.stopped() {
			return err
		}
	}
}

// begin intends a new attempt: the candidate it lands, the base it lands on,
// and how. Nothing is requested here; the attempt is written down first.
func (q *queuePromotion) begin(ctx context.Context) error {
	target, err := q.p.Lander.TargetCommit(ctx, q.key.TargetBranch)
	if err != nil {
		return fmt.Errorf("read where %s stands: %w", q.key.TargetBranch, err)
	}
	attempt := runstate.MergeQueuePromotionAttempt{Number: uint64(len(q.landing.Attempts) + 1), TargetBase: target, IntendedAt: q.now()}
	if q.entry.Mode == runstate.MergeQueueForge {
		if refusal, err := q.forgeQueueQualifies(ctx); err != nil || refusal != "" {
			q.outcome.Refusal = refusal
			return err
		}
		run, err := q.p.Pipeline.Store.Load(q.entry.RunID)
		if err != nil {
			return fmt.Errorf("read run %s, whose branch the forge's queue is handed: %w", q.entry.RunID, err)
		}
		if strings.TrimSpace(run.Branch) == "" {
			q.outcome.Refusal = fmt.Sprintf("run %s records no branch for the forge's queue to take the change from", q.entry.RunID)
			return nil
		}
		attempt.Path, attempt.Candidate, attempt.CandidateBranch = runstate.MergeQueueLandThroughForgeQueue, q.entry.ApprovedHead, run.Branch
	} else {
		generation, refusal, err := q.verified(target)
		if err != nil || refusal != "" {
			q.outcome.Refusal = refusal
			return err
		}
		if q.outcome.Drift {
			return nil
		}
		path, err := q.path(ctx)
		if err != nil {
			return err
		}
		attempt.Path, attempt.Generation, attempt.Binding, attempt.Candidate = path, generation.Number, generation.Binding(), generation.Candidate
		if path.ThroughForge() {
			attempt.CandidateBranch = runstate.MergeQueueCandidateBranch(q.entry.EntryID)
		}
	}
	attempt.Key = runstate.MergeQueuePromotionKey(q.entry.EntryID, attempt.Number, attempt.Path, attempt.Binding, attempt.Candidate)
	q.landing.Attempts = append(q.landing.Attempts, attempt)
	return q.save()
}

// verified is the entry's newest generation where it earned its gate under the
// checks configured now and stands on target. A generation that stands on
// some other base is invalidated as drift, and anything else short of a
// verified generation is a refusal: missing, unfinished, invalidated, or
// unreadable evidence lands nothing.
func (q *queuePromotion) verified(target string) (runstate.MergeQueueGeneration, string, error) {
	generations, err := q.p.Queue.Generations(q.key, q.entry.EntryID)
	if err != nil {
		return runstate.MergeQueueGeneration{}, fmt.Sprintf("the candidate's evidence could not be read, so nothing is promoted on it: %v", err), nil
	}
	if len(generations) == 0 {
		return runstate.MergeQueueGeneration{}, "no candidate has been built and verified for this entry yet", nil
	}
	generation := generations[len(generations)-1]
	if err := generation.Gate(runstate.NewMergeQueueCheckConfiguration(q.p.Pipeline.Config.Checks)); err != nil {
		return generation, err.Error(), nil
	}
	if generation.TargetBase != target {
		return generation, "", q.drift(generation, target, nil)
	}
	return generation, "", nil
}

// path is how the target lands as the project and the forge stand now.
func (q *queuePromotion) path(ctx context.Context) (runstate.MergeQueueLandingPath, error) {
	publishing, _, err := q.p.Pipeline.resolvePublishing(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve whether the project publishes: %w", err)
	}
	if !publishing {
		return runstate.MergeQueueLandLocally, nil
	}
	// A forge that cannot say is read as protecting the target, for the reason
	// a run's promotion reads it so (landsThroughPullRequest): the other
	// reading would move a branch the forge may refuse.
	protection, err := q.p.Pipeline.Publisher.Protection(ctx, q.key.TargetBranch)
	if err != nil || protection.Protected {
		return runstate.MergeQueueLandThroughPullRequest, nil
	}
	return runstate.MergeQueueLandLocallyThenPullRequest, nil
}

// forgeQueueQualifies asks the forge again whether its queue enforces the
// whole gate on the exact combined commit it lands, and is a refusal where it
// no longer does. Pull-request-head approval is never enough
// (queuemode.Select).
func (q *queuePromotion) forgeQueueQualifies(ctx context.Context) (string, error) {
	if q.p.Forge == nil {
		return "the entry was admitted to the forge's own merge queue, and nothing here can ask the forge whether that queue still enforces the checks and the independent review", nil
	}
	observed, err := q.p.Forge.QueueCapabilities(ctx, q.key.TargetBranch)
	if err != nil {
		return "", fmt.Errorf("ask the forge what its merge queue for %s establishes: %w", q.key.TargetBranch, err)
	}
	selection, err := queuemode.Select(q.key.TargetBranch, observed, q.p.Harness, q.now())
	if err != nil {
		return "", err
	}
	if selection.Mode != runstate.MergeQueueForge {
		return "the forge's merge queue no longer enforces everything the entry was admitted to it for: " + selection.Evidence.Explanation, nil
	}
	return "", nil
}

// drift invalidates a generation the target moved off and, where an attempt
// was intended for it and has moved nothing, sets that attempt aside. It is
// charged to nothing.
func (q *queuePromotion) drift(generation runstate.MergeQueueGeneration, target string, attempt *runstate.MergeQueuePromotionAttempt) error {
	q.outcome.Drift = true
	if generation.Invalidated == nil {
		generation.Invalidated = &runstate.MergeQueueInvalidation{Reason: runstate.MergeQueueTargetMoved, ObservedTarget: target, At: q.now()}
		if err := q.p.Queue.RecordGeneration(q.worker, q.key, generation); err != nil {
			return fmt.Errorf("invalidate generation %d, which %s moved off: %w", generation.Number, q.key.TargetBranch, err)
		}
	}
	if attempt != nil {
		return q.setAside(attempt, fmt.Sprintf("%s moved to %s, off the base %s the candidate was built on", q.key.TargetBranch, target, attempt.TargetBase), true)
	}
	return nil
}

func (q *queuePromotion) setAside(attempt *runstate.MergeQueuePromotionAttempt, reason string, drift bool) error {
	attempt.SetAside = &runstate.MergeQueueSetAside{At: q.now(), Reason: oneline.Fold(reason, 1500), Drift: drift}
	return q.save()
}

// proceed takes the attempt one step on from mutations that have all settled.
func (q *queuePromotion) proceed(ctx context.Context) error {
	attempt := q.attempt()
	if count := len(attempt.Mutations); count > 0 {
		last := attempt.Mutations[count-1]
		switch {
		case last.Settled.Result == runstate.MergeQueueMutationNotMade:
			return q.notMade(ctx, last)
		case last.Mutation == runstate.MergeQueueRequestMerge && last.Settled.Result == runstate.MergeQueueMutationQueued:
			return q.awaitForge(ctx)
		case last.Mutation == runstate.MergeQueueRequestMerge || (last.Mutation == runstate.MergeQueueMoveTarget && attempt.Path == runstate.MergeQueueLandLocally):
			// The landing mutation settled done and the record lacks only the
			// landing itself, which a save that failed in between leaves.
			return q.confirm(ctx)
		}
	}
	switch mutation := attempt.Next(); mutation {
	case runstate.MergeQueuePushCandidate:
		return q.pushCandidate(ctx)
	case runstate.MergeQueueOpenPullRequest:
		return q.openPullRequest(ctx)
	case runstate.MergeQueueMoveTarget, runstate.MergeQueueRequestMerge:
		return q.mutateTarget(ctx, mutation)
	default:
		return fmt.Errorf("promotion attempt %d of merge queue entry %d has made every mutation and records no landing", attempt.Number, q.entry.Order)
	}
}

// request writes a mutation down, before it is asked for.
func (q *queuePromotion) request(mutation runstate.MergeQueueMutation, commit, expected string) error {
	attempt := q.attempt()
	attempt.Mutations = append(attempt.Mutations, runstate.MergeQueueMutationRecord{
		Mutation: mutation, Key: attempt.MutationKey(mutation), Commit: commit, Expected: expected, RequestedAt: q.now(),
	})
	return q.save()
}

// settle records how the attempt's last mutation turned out.
func (q *queuePromotion) settle(result runstate.MergeQueueMutationResult, detail string, observed bool) error {
	attempt := q.attempt()
	record := &attempt.Mutations[len(attempt.Mutations)-1]
	record.Settled = &runstate.MergeQueueSettlement{Result: result, At: q.now(), Observed: observed, Detail: runstate.BoundedMergeQueueText(detail)}
	return q.save()
}

// pushCandidate publishes the candidate on the entry's candidate branch, over
// whatever the queue last put there.
func (q *queuePromotion) pushCandidate(ctx context.Context) error {
	attempt := q.attempt()
	published, exists, err := q.p.Lander.QueueCandidateBranchCommit(ctx, attempt.CandidateBranch)
	if err != nil {
		return fmt.Errorf("read where %s stands: %w", attempt.CandidateBranch, err)
	}
	expected := ""
	if exists {
		expected = published
	}
	if err := q.request(runstate.MergeQueuePushCandidate, attempt.Candidate, expected); err != nil {
		return err
	}
	err = q.p.Lander.PublishQueueCandidate(ctx, attempt.CandidateBranch, attempt.Candidate, expected)
	if errors.Is(err, gitworktree.ErrRemotePushRejected) {
		return q.settle(runstate.MergeQueueMutationNotMade, err.Error(), false)
	}
	if err != nil {
		return fmt.Errorf("publish the candidate on %s: %w", attempt.CandidateBranch, err)
	}
	return q.settle(runstate.MergeQueueMutationDone, "", false)
}

// openPullRequest opens, or finds open, the pull request that carries the
// candidate.
func (q *queuePromotion) openPullRequest(ctx context.Context) error {
	attempt := q.attempt()
	if err := q.request(runstate.MergeQueueOpenPullRequest, attempt.Candidate, ""); err != nil {
		return err
	}
	opened, err := q.p.Pipeline.Publisher.Ensure(ctx, publish.Request{
		Head:  attempt.CandidateBranch,
		Base:  q.key.TargetBranch,
		Title: fmt.Sprintf("%s: %s", q.entry.WorkItemID, q.entry.WorkItemTitle),
		Body:  q.pullRequestBody(*attempt),
	})
	if err != nil {
		return fmt.Errorf("open the pull request that carries %s: %w", attempt.CandidateBranch, err)
	}
	attempt = q.attempt()
	attempt.PullRequest, attempt.PullRequestURL = opened.Number, opened.URL
	return q.settle(runstate.MergeQueueMutationDone, "", false)
}

func (q *queuePromotion) pullRequestBody(attempt runstate.MergeQueuePromotionAttempt) string {
	if attempt.Path == runstate.MergeQueueLandThroughForgeQueue {
		return fmt.Sprintf("The change for %s, approved at %s and admitted to the forge's merge queue for %s.\n\nRun: %s\n",
			q.entry.WorkItemID, q.entry.ApprovedHead, q.key.TargetBranch, q.entry.RunID)
	}
	body := fmt.Sprintf("Merge queue candidate %s for %s: %s at %s with the approved head %s merged onto it. Its configured checks passed and an independent reviewer approved it, both bound to generation %d of entry %s.\n\nRun: %s\n",
		attempt.Candidate, q.entry.WorkItemID, q.key.TargetBranch, attempt.TargetBase, q.entry.ApprovedHead, attempt.Generation, q.entry.EntryID, q.entry.RunID)
	if q.entry.Publication != "" {
		body += "Approved as: " + q.entry.Publication + "\n"
	}
	return body
}

// mutateTarget moves the local target or asks the forge to merge, under the
// target's promotion lease and after reading everything that may stop it
// again.
func (q *queuePromotion) mutateTarget(ctx context.Context, mutation runstate.MergeQueueMutation) error {
	p := q.p.Pipeline
	lease, err := p.Store.LeasePromotion(ctx, q.key.TargetBranch)
	if err != nil {
		return fmt.Errorf("wait for the turn to promote into %s: %w", q.key.TargetBranch, err)
	}
	// Releasing lets the next promotion in; the operating system does it anyway
	// when the process exits, so a close that failed says nothing about the
	// mutation below.
	defer func() { _ = lease.Release() }()
	if ready, err := q.fresh(ctx, mutation); err != nil || !ready {
		return err
	}
	attempt := q.attempt()
	if mutation == runstate.MergeQueueMoveTarget {
		if err := q.request(mutation, attempt.Candidate, attempt.TargetBase); err != nil {
			return err
		}
		err := q.p.Lander.PromoteQueueCandidate(ctx, q.key.TargetBranch, attempt.TargetBase, attempt.Candidate)
		if errors.Is(err, gitworktree.ErrTargetDrift) || errors.Is(err, gitworktree.ErrNotFastForward) {
			// The swap refused: the target moved between the read above and the
			// move, and nothing was moved.
			if settleErr := q.settle(runstate.MergeQueueMutationNotMade, err.Error(), false); settleErr != nil {
				return settleErr
			}
			return q.driftFrom(ctx)
		}
		if err != nil {
			return fmt.Errorf("move %s onto the candidate: %w", q.key.TargetBranch, err)
		}
		if err := q.settle(runstate.MergeQueueMutationDone, "", false); err != nil {
			return err
		}
		if attempt.Path == runstate.MergeQueueLandLocally {
			return q.confirm(ctx)
		}
		return nil
	}
	if err := q.request(mutation, attempt.Candidate, ""); err != nil {
		return err
	}
	result, err := p.Publisher.Merge(ctx, publish.MergeRequest{Number: attempt.PullRequest, HeadCommit: attempt.Candidate, Method: mergeMethod})
	var refused publish.MergeRefused
	var unavailable publish.AutoMergeUnavailable
	if errors.As(err, &refused) || errors.As(err, &unavailable) {
		return q.settle(runstate.MergeQueueMutationNotMade, err.Error(), false)
	}
	if err != nil {
		// Whether the forge took the request is not known, so it is left
		// standing for the next call to ask the forge about.
		return fmt.Errorf("ask the forge to merge pull request %d: %w", attempt.PullRequest, err)
	}
	if result.Queued {
		return q.settle(runstate.MergeQueueMutationQueued, "", false)
	}
	if err := q.settle(runstate.MergeQueueMutationDone, "", false); err != nil {
		return err
	}
	return q.confirm(ctx)
}

// fresh reads, under the promotion lease, everything that decides whether the
// mutation may happen now, and reports false with the outcome saying why where
// it may not. Nothing is written down as requested before this has passed.
func (q *queuePromotion) fresh(ctx context.Context, mutation runstate.MergeQueueMutation) (bool, error) {
	p := q.p.Pipeline
	attempt := q.attempt()
	refuse := func(reason string) (bool, error) {
		q.outcome.Refusal = reason
		return false, nil
	}
	hold, held, err := p.operatorHold()
	if err != nil {
		return false, err
	}
	if held {
		return refuse(fmt.Sprintf("the operator has paused harness activity since %s", hold.HeldAt.Local().Format("2006-01-02 15:04 MST")))
	}
	pausing, err := p.pausingDirectives(q.entry.WorkItemID)
	if err != nil {
		return false, err
	}
	if len(pausing) > 0 {
		return refuse("an unresolved directive stops this work: " + pausing[0].Summary())
	}
	item, err := p.Tracker.Show(ctx, q.entry.WorkItemID)
	if err != nil {
		return false, fmt.Errorf("read what %s waits on: %w", q.entry.WorkItemID, err)
	}
	if blockers := blockingDependencies(item); len(blockers) > 0 {
		return refuse(fmt.Sprintf("%s now waits on %s", q.entry.WorkItemID, strings.Join(blockers, ", ")))
	}
	if !p.automatic() || q.entry.IntegrationPolicy != string(p.Config.Approvals.Integration) {
		return refuse(fmt.Sprintf("the change was admitted under %s integration, and integration is now %s, so the queue lands nothing by itself",
			q.entry.IntegrationPolicy, p.Config.Approvals.Integration))
	}
	// The protected-path gate is the third thing a landing needs evidence of,
	// beside the checks and the review, and it is asked of exactly what lands:
	// the candidate, whose merge onto the target is a revision no run's own gate
	// ever saw. An attempt that already moved the local target asked it then.
	if !(attempt.Path.MovesLocalTargetFirst() && mutation == runstate.MergeQueueRequestMerge) {
		refused, err := q.refusedPaths(ctx, item, *attempt)
		if err != nil {
			return false, err
		}
		if len(refused) > 0 {
			return refuse(fmt.Sprintf("the candidate changes protected paths %s does not grant (%s), so it lands nothing",
				q.entry.WorkItemID, strings.Join(refused, ", ")))
		}
	}
	if attempt.Path == runstate.MergeQueueLandThroughForgeQueue {
		refusal, err := q.forgeQueueQualifies(ctx)
		if err != nil || refusal == "" {
			return err == nil, err
		}
		if err := q.setAside(attempt, refusal, false); err != nil {
			return false, err
		}
		return refuse(refusal)
	}
	// Everything below is about the generation and the target, which an
	// attempt that already moved the local target has settled for itself.
	moved := attempt.Path.MovesLocalTargetFirst() && mutation == runstate.MergeQueueRequestMerge
	if !moved {
		path, err := q.path(ctx)
		if err != nil {
			return false, err
		}
		if path != attempt.Path {
			reason := fmt.Sprintf("%s lands by %s now, not %s as the attempt was intended", q.key.TargetBranch, path, attempt.Path)
			if err := q.setAside(attempt, reason, false); err != nil {
				return false, err
			}
			return refuse(reason)
		}
		target, err := q.p.Lander.TargetCommit(ctx, q.key.TargetBranch)
		if err != nil {
			return false, fmt.Errorf("read where %s stands: %w", q.key.TargetBranch, err)
		}
		generation, refusal, err := q.standing(attempt)
		if err != nil || refusal != "" {
			if err == nil {
				err = q.setAside(attempt, refusal, false)
			}
			q.outcome.Refusal = refusal
			return false, err
		}
		if target != attempt.TargetBase {
			return false, q.drift(generation, target, attempt)
		}
		// A primary checkout that cannot take the move refuses it before it is
		// written down, so a checkout holding somebody's work stops the queue
		// rather than spending an attempt on every call.
		if mutation == runstate.MergeQueueMoveTarget {
			if err := q.p.Lander.ValidateReady(ctx); err != nil {
				return refuse(fmt.Sprintf("the primary checkout is not ready for %s to move: %v", q.key.TargetBranch, err))
			}
		}
	}
	if mutation == runstate.MergeQueueRequestMerge {
		// The forge would reconcile a moved remote target itself, merging the
		// candidate onto something it was never checked or reviewed on.
		err := q.p.Lander.VerifyRemoteTarget(ctx, gitworktree.Integration{
			TargetBranch: q.key.TargetBranch, TargetCommit: attempt.TargetBase, PreviousTargetCommit: attempt.TargetBase,
		})
		if errors.Is(err, gitworktree.ErrRemoteTargetDrift) {
			if moved {
				q.outcome.Unresolved = fmt.Sprintf("%s was moved onto the candidate here, and the remote %s has since moved off its base: %v", q.key.TargetBranch, q.key.TargetBranch, err)
				return false, nil
			}
			return false, q.driftFrom(ctx)
		}
		if err != nil {
			return false, fmt.Errorf("check the remote %s before asking for the merge: %w", q.key.TargetBranch, err)
		}
	}
	return true, nil
}

// refusedPaths is every path the attempt's candidate changes that the
// protected-path gate refuses for its work item, as a run's own gate decides
// it (gateProtectedPaths): the configured homes and held exports, less what
// the item grants. A list that cannot be read is an error, so nothing is
// mutated on a gate nobody could ask.
func (q *queuePromotion) refusedPaths(ctx context.Context, item beads.WorkItem, attempt runstate.MergeQueuePromotionAttempt) ([]string, error) {
	p := q.p.Pipeline
	changed, err := q.p.Lander.CandidatePaths(ctx, attempt.TargetBase, attempt.Candidate)
	if err != nil {
		return nil, fmt.Errorf("list the paths the candidate changes: %w", err)
	}
	var exports []string
	if p.Worktrees != nil {
		exports = p.Worktrees.CurrentExports()
	}
	return protectedpath.Protect(p.Config, exports...).Refused(changed, protectedpath.Grants(grantEvidence(item)...)), nil
}

// standing is the attempt's generation as it is recorded now, and a refusal
// where it no longer earns its gate.
func (q *queuePromotion) standing(attempt *runstate.MergeQueuePromotionAttempt) (runstate.MergeQueueGeneration, string, error) {
	generations, err := q.p.Queue.Generations(q.key, q.entry.EntryID)
	if err != nil {
		return runstate.MergeQueueGeneration{}, fmt.Sprintf("the candidate's evidence could not be read again, so nothing is promoted on it: %v", err), nil
	}
	if attempt.Generation == 0 || int(attempt.Generation) != len(generations) {
		return runstate.MergeQueueGeneration{}, fmt.Sprintf("generation %d is not the entry's newest generation any more", attempt.Generation), nil
	}
	generation := generations[attempt.Generation-1]
	if generation.Binding() != attempt.Binding {
		return generation, fmt.Sprintf("generation %d is not the candidate the attempt was intended for", attempt.Generation), nil
	}
	if err := generation.Gate(runstate.NewMergeQueueCheckConfiguration(q.p.Pipeline.Config.Checks)); err != nil {
		return generation, err.Error(), nil
	}
	return generation, "", nil
}

// driftUnderLease is driftFrom for a caller that does not hold the target's
// promotion lease: it takes the lease first, because driftFrom may catch the
// local target up onto the remote, and nothing moves a target without it.
func (q *queuePromotion) driftUnderLease(ctx context.Context) error {
	lease, err := q.p.Pipeline.Store.LeasePromotion(ctx, q.key.TargetBranch)
	if err != nil {
		return fmt.Errorf("wait for the turn to read where %s went: %w", q.key.TargetBranch, err)
	}
	defer func() { _ = lease.Release() }()
	return q.driftFrom(ctx)
}

// driftFrom reads where the target went and records the drift. Its caller
// holds the target's promotion lease, because it may move the local target
// (driftUnderLease is for one that does not).
func (q *queuePromotion) driftFrom(ctx context.Context) error {
	attempt := q.attempt()
	target, err := q.p.Lander.TargetCommit(ctx, q.key.TargetBranch)
	if err != nil {
		return fmt.Errorf("read where %s stands: %w", q.key.TargetBranch, err)
	}
	if attempt.Path.ThroughForge() && target == attempt.TargetBase {
		// The remote moved and the local target has not followed. It is caught
		// up first, so the generation the worker builds next stands on what the
		// remote has; a catch-up held for somebody's work changes nothing here.
		if catchup, err := q.p.Lander.CatchUpTarget(ctx, q.key.TargetBranch); err == nil && catchup.Advanced {
			target = catchup.RemoteCommit
		}
	}
	generations, err := q.p.Queue.Generations(q.key, q.entry.EntryID)
	if err != nil {
		return fmt.Errorf("read the generation %s moved off: %w", q.key.TargetBranch, err)
	}
	if attempt.Generation == 0 || int(attempt.Generation) > len(generations) {
		q.outcome.Drift = true
		return q.setAside(attempt, fmt.Sprintf("%s moved off the base %s", q.key.TargetBranch, attempt.TargetBase), true)
	}
	return q.drift(generations[attempt.Generation-1], target, attempt)
}

// awaitForge asks the forge once about a merge it holds queued. The wait is
// the forge's and happens between calls, outside every lease but the worker's.
func (q *queuePromotion) awaitForge(ctx context.Context) error {
	attempt := q.attempt()
	state, err := q.p.Pipeline.Publisher.State(ctx, attempt.CandidateBranch)
	if err != nil {
		q.outcome.Waiting = fmt.Sprintf("the forge holds the merge of pull request %d queued, and could not be asked about it now: %v", attempt.PullRequest, err)
		return nil
	}
	switch {
	case state.Merged:
		return q.confirmWith(ctx, state.MergeCommit)
	case strings.EqualFold(state.State, "OPEN") && state.AutoMerge:
		q.outcome.Waiting = fmt.Sprintf("the forge holds the merge of pull request %d queued until its own requirements are met", attempt.PullRequest)
	default:
		q.outcome.Unresolved = fmt.Sprintf("the forge no longer holds the merge of pull request %d and has not made it (it reads %s); it is kept as it is and not asked for again",
			attempt.PullRequest, strings.ToLower(nonEmpty(state.State, "unreported")))
	}
	return nil
}

// confirm establishes the landing the attempt's last mutation made.
func (q *queuePromotion) confirm(ctx context.Context) error {
	attempt := q.attempt()
	if !attempt.Path.ThroughForge() {
		held, err := q.p.Lander.TargetHolds(ctx, q.key.TargetBranch, attempt.Candidate)
		if err != nil {
			return fmt.Errorf("read whether %s holds the candidate: %w", q.key.TargetBranch, err)
		}
		if !held {
			q.outcome.Unresolved = fmt.Sprintf("%s was moved onto the candidate %s and no longer holds it", q.key.TargetBranch, attempt.Candidate)
			return nil
		}
		return q.land("")
	}
	state, err := q.p.Pipeline.Publisher.State(ctx, attempt.CandidateBranch)
	if err != nil {
		return fmt.Errorf("read pull request %d after its merge: %w", attempt.PullRequest, err)
	}
	return q.confirmWith(ctx, state.MergeCommit)
}

// confirmWith establishes on the remote target that the candidate itself
// landed, and names the forge's merge commit where it made one.
func (q *queuePromotion) confirmWith(ctx context.Context, mergeCommit string) error {
	attempt := q.attempt()
	remoteMerge, err := q.p.Lander.ConfirmRemoteTarget(ctx, gitworktree.Integration{
		TargetBranch: q.key.TargetBranch, TargetCommit: attempt.Candidate, PreviousTargetCommit: attempt.TargetBase,
	}, mergeCommit)
	if errors.Is(err, gitworktree.ErrRemoteTargetMismatch) {
		q.outcome.Unresolved = fmt.Sprintf("the forge reports pull request %d merged, and the remote %s does not hold the candidate %s: %v",
			attempt.PullRequest, q.key.TargetBranch, attempt.Candidate, err)
		return nil
	}
	if err != nil {
		return fmt.Errorf("confirm the candidate reached the remote %s: %w", q.key.TargetBranch, err)
	}
	// The forge's own queue checks and has reviewed a combined commit of its
	// own making, and that commit — not the head handed to it — is what
	// landed. A landing the forge does not name is not recorded as one.
	if attempt.Path == runstate.MergeQueueLandThroughForgeQueue && remoteMerge == "" {
		q.outcome.Unresolved = fmt.Sprintf("the remote %s holds the change from pull request %d, and nothing names the combined commit the forge's merge queue landed it in; the landing is recorded once that commit is known",
			q.key.TargetBranch, attempt.PullRequest)
		return nil
	}
	return q.land(remoteMerge)
}

// land records the confirmed landing: the attempt's own candidate, or, in the
// forge's queue, the combined commit the forge landed.
func (q *queuePromotion) land(remoteMerge string) error {
	attempt := q.attempt()
	landed := attempt.Candidate
	if attempt.Path == runstate.MergeQueueLandThroughForgeQueue {
		landed = remoteMerge
	}
	attempt.Landed = &runstate.MergeQueueLanded{Commit: landed, RemoteMerge: remoteMerge, ConfirmedAt: q.now()}
	return q.save()
}

// observe settles a mutation an earlier process requested and never settled,
// from what the target, the candidate branch, and the forge show now. A
// result observation cannot establish leaves the mutation standing.
func (q *queuePromotion) observe(ctx context.Context, record runstate.MergeQueueMutationRecord) error {
	attempt := q.attempt()
	var result runstate.MergeQueueMutationResult
	var found string
	switch record.Mutation {
	case runstate.MergeQueueMoveTarget:
		target, err := q.p.Lander.TargetCommit(ctx, q.key.TargetBranch)
		if err != nil {
			return fmt.Errorf("read where %s stands: %w", q.key.TargetBranch, err)
		}
		held, err := q.p.Lander.TargetHolds(ctx, q.key.TargetBranch, attempt.Candidate)
		if err != nil {
			return fmt.Errorf("read whether %s holds the candidate: %w", q.key.TargetBranch, err)
		}
		if held {
			result, found = runstate.MergeQueueMutationDone, fmt.Sprintf("%s holds the candidate", q.key.TargetBranch)
		} else {
			result, found = runstate.MergeQueueMutationNotMade, fmt.Sprintf("%s is at %s and does not hold the candidate", q.key.TargetBranch, target)
		}
	case runstate.MergeQueuePushCandidate:
		published, exists, err := q.p.Lander.QueueCandidateBranchCommit(ctx, attempt.CandidateBranch)
		if err != nil {
			return fmt.Errorf("read where %s stands: %w", attempt.CandidateBranch, err)
		}
		switch {
		case exists && published == attempt.Candidate:
			result, found = runstate.MergeQueueMutationDone, attempt.CandidateBranch+" carries the candidate"
		case (!exists && record.Expected == "") || (exists && published == record.Expected):
			// The branch is where it was before the push, so the push never
			// arrived; it is made again under its own compare-and-swap.
			result, found = runstate.MergeQueueMutationNotMade, attempt.CandidateBranch+" is where it was before the push"
		default:
			q.outcome.Unresolved = fmt.Sprintf("%s is at %q, which is neither the candidate nor what it held before the push", attempt.CandidateBranch, published)
			return nil
		}
	case runstate.MergeQueueOpenPullRequest:
		state, err := q.p.Pipeline.Publisher.State(ctx, attempt.CandidateBranch)
		if err != nil || state.Number == 0 {
			// Opening is asked again: the forge opens one request per branch and
			// finds the one it has, so asking twice opens nothing twice.
			result, found = runstate.MergeQueueMutationNotMade, "no pull request could be found for "+attempt.CandidateBranch
			break
		}
		attempt.PullRequest, attempt.PullRequestURL = state.Number, state.URL
		result, found = runstate.MergeQueueMutationDone, fmt.Sprintf("pull request %d carries %s", state.Number, attempt.CandidateBranch)
	case runstate.MergeQueueRequestMerge:
		state, err := q.p.Pipeline.Publisher.State(ctx, attempt.CandidateBranch)
		if err != nil {
			q.outcome.Unresolved = fmt.Sprintf("the merge of pull request %d was asked for and its answer never recorded, and the forge could not be asked what it did: %v", attempt.PullRequest, err)
			return nil
		}
		switch {
		case state.Merged:
			result, found = runstate.MergeQueueMutationDone, fmt.Sprintf("the forge merged pull request %d", attempt.PullRequest)
		case strings.EqualFold(state.State, "OPEN") && state.AutoMerge:
			result, found = runstate.MergeQueueMutationQueued, fmt.Sprintf("the forge holds the merge of pull request %d queued", attempt.PullRequest)
		default:
			result, found = runstate.MergeQueueMutationNotMade, fmt.Sprintf("pull request %d reads %s, neither merged nor queued", attempt.PullRequest, strings.ToLower(nonEmpty(state.State, "unreported")))
		}
	case runstate.MergeQueueFollowTarget:
		held, err := q.p.Lander.TargetHolds(ctx, q.key.TargetBranch, attempt.Candidate)
		if err != nil {
			return fmt.Errorf("read whether %s holds the candidate: %w", q.key.TargetBranch, err)
		}
		if held {
			result, found = runstate.MergeQueueMutationDone, q.key.TargetBranch+" holds the landed candidate"
		} else {
			result, found = runstate.MergeQueueMutationNotMade, q.key.TargetBranch+" has not followed the landing yet"
		}
	default:
		return fmt.Errorf("promotion attempt %d records a %s this build does not make", attempt.Number, record.Mutation)
	}
	q.landing.Recoveries = append(q.landing.Recoveries, runstate.MergeQueueRecovery{
		At: q.now(), Attempt: attempt.Number, Mutation: record.Mutation,
		Found: oneline.Fold(fmt.Sprintf("requested at %s and never settled: %s", record.RequestedAt.Format(time.RFC3339), found), 1500),
	})
	return q.settle(result, found, true)
}

// notMade decides what follows a mutation that was not made. One a process
// stopped before it reached anything is asked for again where that is the
// same request under the same key — a push under its compare-and-swap, a pull
// request the forge opens once per branch — and an attempt that has changed nothing yet is set aside
// instead, so the next is intended afresh under everything read again. A
// local move observed not made is drift where the target moved and an
// interruption where it did not. Everything else — a refusal, a merge neither
// made nor held — is kept as it is and reported: deciding what it costs
// belongs to later work, and asking again here would be deciding it.
func (q *queuePromotion) notMade(ctx context.Context, last runstate.MergeQueueMutationRecord) error {
	attempt := q.attempt()
	interrupted := last.Settled.Observed
	switch {
	case interrupted && last.Mutation == runstate.MergeQueueMoveTarget:
		target, err := q.p.Lander.TargetCommit(ctx, q.key.TargetBranch)
		if err != nil {
			return fmt.Errorf("read where %s stands: %w", q.key.TargetBranch, err)
		}
		if target == attempt.TargetBase {
			return q.setAside(attempt, "the process that moved the target stopped before the target moved", false)
		}
		return q.driftUnderLease(ctx)
	case !interrupted && last.Mutation == runstate.MergeQueueMoveTarget:
		return q.driftUnderLease(ctx)
	case interrupted && (last.Mutation == runstate.MergeQueuePushCandidate || last.Mutation == runstate.MergeQueueOpenPullRequest):
		if !attempt.MovedTarget() {
			return q.setAside(attempt, fmt.Sprintf("the process that asked for the %s stopped before it was made", last.Mutation), false)
		}
		if last.Mutation == runstate.MergeQueuePushCandidate {
			return q.pushCandidate(ctx)
		}
		return q.openPullRequest(ctx)
	}
	q.outcome.Unresolved = fmt.Sprintf("the %s of promotion attempt %d was not made (%s); it is kept as it is and not asked for again",
		last.Mutation, attempt.Number, nonEmpty(last.Settled.Detail, "no reason was given"))
	return nil
}

// recordCompletion follows a confirmed landing onto the local target and
// records the completion part by part.
func (q *queuePromotion) recordCompletion(ctx context.Context) error {
	attempt := q.attempt()
	if attempt.Path.ThroughForge() {
		// A follow observed not made is asked for again: it is a fast-forward
		// onto a landing already confirmed, so asking twice moves nothing twice.
		followed, recorded := attempt.Mutation(runstate.MergeQueueFollowTarget)
		if !recorded || followed.Settled.Result == runstate.MergeQueueMutationNotMade {
			if err := q.follow(ctx); err != nil {
				return err
			}
		}
	}
	attempt = q.attempt()
	if q.landing.Completion == nil {
		q.landing.Completion = &runstate.MergeQueueCompletion{
			Attempt: attempt.Number, Path: attempt.Path, Generation: attempt.Generation, Binding: attempt.Binding,
			TargetBase: attempt.TargetBase, Landed: attempt.Landed.Commit, RemoteMerge: attempt.Landed.RemoteMerge,
			PullRequest: attempt.PullRequestURL, QueueAt: q.now(),
		}
		if err := q.save(); err != nil {
			return err
		}
	}
	completion := q.landing.Completion
	if completion.RunAt == nil {
		if err := q.p.Completion.RecordRun(ctx, q.entry, *completion); err != nil {
			return fmt.Errorf("record the landing on run %s: %w", q.entry.RunID, err)
		}
		at := q.now()
		completion.RunAt = &at
		if err := q.save(); err != nil {
			return err
		}
	}
	if completion.WorkItemAt == nil {
		if err := q.p.Completion.RecordWorkItem(ctx, q.entry, *completion); err != nil {
			return fmt.Errorf("record the landing on %s: %w", q.entry.WorkItemID, err)
		}
		at := q.now()
		completion.WorkItemAt = &at
		if err := q.save(); err != nil {
			return err
		}
	}
	return nil
}

// follow fast-forwards the local target onto what the forge landed, under the
// promotion lease, because it moves the target. A checkout holding somebody's
// unsaved work holds the follow rather than losing the work; the landing
// stands, and reconciliation catches the target up later.
func (q *queuePromotion) follow(ctx context.Context) error {
	lease, err := q.p.Pipeline.Store.LeasePromotion(ctx, q.key.TargetBranch)
	if err != nil {
		return fmt.Errorf("wait for the turn to move %s onto the landing: %w", q.key.TargetBranch, err)
	}
	defer func() { _ = lease.Release() }()
	if err := q.request(runstate.MergeQueueFollowTarget, q.attempt().Candidate, ""); err != nil {
		return err
	}
	catchup, err := q.p.Lander.CatchUpTarget(ctx, q.key.TargetBranch)
	if err != nil {
		return fmt.Errorf("move %s onto what the forge landed: %w", q.key.TargetBranch, err)
	}
	if catchup.Held != "" {
		return q.settle(runstate.MergeQueueMutationHeld, catchup.Held, false)
	}
	return q.settle(runstate.MergeQueueMutationDone, "", false)
}

// MergeQueueRunCompletion records a landing on the run and the work item the
// way a run's own promotion leaves them: the run carries its integration, and
// the item is settled as the run's landing claim and its approval say. Each
// asks first whether its record is already made, so asking again makes
// nothing twice.
type MergeQueueRunCompletion struct {
	Store   StateStore
	Tracker WorkTracker
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

var _ MergeQueueCompletion = MergeQueueRunCompletion{}

func (c MergeQueueRunCompletion) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// RecordRun writes the landing onto the run as its integration. The change the
// run made is its source; what landed is the commit that carried it — the
// harness's candidate, or the forge queue's combined commit.
func (c MergeQueueRunCompletion) RecordRun(_ context.Context, entry runstate.MergeQueueEntry, completion runstate.MergeQueueCompletion) error {
	state, err := c.Store.Load(entry.RunID)
	if err != nil {
		return err
	}
	if state.Integration != nil && state.Integration.TargetCommit == completion.Landed {
		return nil
	}
	state.Integration = &runstate.Integration{
		TargetBranch:         entry.TargetBranch,
		SourceCommit:         entry.ApprovedHead,
		TargetCommit:         completion.Landed,
		PreviousTargetCommit: completion.TargetBase,
		ThroughPullRequest:   completion.Path.ThroughForge() && !completion.Path.MovesLocalTargetFirst(),
	}
	state.UpdatedAt = c.now()
	return c.Store.Save(state)
}

// RecordWorkItem settles the item on the landing as a run's own settlement
// does: it is closed only where the change discharges it — the developer's
// landing claim and the reviewer's approval of the change both saying so, read
// from the run's durable record — and is otherwise put back as the claim
// says. The candidate's own review authorized the landing; it says nothing
// about whether the change finishes the item. An item already where the run
// calls for is left alone.
func (c MergeQueueRunCompletion) RecordWorkItem(ctx context.Context, entry runstate.MergeQueueEntry, completion runstate.MergeQueueCompletion) error {
	state, err := c.Store.Load(entry.RunID)
	if err != nil {
		return err
	}
	item, err := c.Tracker.Show(ctx, entry.WorkItemID)
	if err != nil {
		return err
	}
	if itemSettled(state, item.Status) {
		return nil
	}
	if !state.Discharges() {
		settled, err := settleUndischarged(ctx, c.Tracker, state)
		if err != nil {
			return fmt.Errorf("put back the work item run %s did not discharge: %w", entry.RunID, err)
		}
		if reflect.DeepEqual(settled, state) {
			return nil
		}
		settled.UpdatedAt = c.now()
		return c.Store.Save(settled)
	}
	reason := fmt.Sprintf("Reviewed by Yoyodyne run %s and landed on %s through the merge queue as %s, the candidate whose checks and independent review authorized it.",
		entry.RunID, entry.TargetBranch, completion.Landed)
	if completion.Path == runstate.MergeQueueLandThroughForgeQueue {
		reason = fmt.Sprintf("Reviewed by Yoyodyne run %s at %s and landed on %s by the forge's merge queue as %s, the combined commit that queue required the project's checks and an independent approval of before landing it.",
			entry.RunID, entry.ApprovedHead, entry.TargetBranch, completion.Landed)
	}
	_, err = c.Tracker.Complete(ctx, entry.WorkItemID, reason)
	return err
}
