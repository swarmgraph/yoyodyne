package orchestrator

// The merge queue's worker, as far as verification: the "Harness-run queue"
// section of docs/designs/integration-through-a-merge-queue.md up to, and not
// including, promotion. The worker holds its queue's worker lease, takes the
// first entry still waiting in admission order, builds a candidate from the
// target branch as it stands and the entry's approved head, records that as a
// generation before anything judges it, and then runs the project's configured
// checks and asks an independent reviewer about exactly that candidate.
//
// What it leaves is evidence and nothing else. It never takes the promotion
// lease — the checks and the review are the long part of the queue, and the
// promotion lease is held only for the short step a later promotion takes —
// and it never moves a branch. A promotion reads the generation it leaves
// (runstate.MergeQueueStore.VerifiedGeneration) and checks its gate again
// under its own lease.
//
// The target moving while the checks or the review run makes the generation
// one nothing may promote: it is invalidated as drift, which is nobody's
// failure and is charged to nobody, and the entry is built again on the new
// target and verified from nothing. A restarted worker reads what the last one
// recorded before it starts anything: a stage that finished is not run again,
// and a stage that started and never finished is first looked for. Every check
// and every review is started behind its stage's hold (runstate's
// mergequeuehold.go), written down before it may do any work, so a process an
// earlier worker left running keeps the hold taken; while it does, nothing is
// started beside it and its checkout is left alone, and the call reports what
// it is waiting on. Only once it has stopped is the stage written down as
// interrupted, earning nothing, and run again on the candidate restored from
// its commit rather than rebuilt, so what was recorded against it still
// describes it.
//
// A stage is a spend like a run's, and passes the same doors a run's does
// before it starts: the operator's pause on harness activity holds both
// stages, and the review also waits out a provider nobody can reach, a usage
// limit already known for its account and model, and a provider that is not
// installed or not logged in. A provider that refuses the review for capacity,
// overload, login or reachability made no review, so the attempt earns nothing
// and the call reports a wait rather than a failure.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/spend"
)

// maxMergeQueueDriftRebuilds bounds how many times one call to Work builds an
// entry again because the target moved under it. Each rebuild is fresh
// verification of a fresh candidate, so a target that moves faster than the
// checks run is answered by stopping and saying so, and the next call starts
// from wherever the target then is.
const maxMergeQueueDriftRebuilds = 3

// MergeQueueCandidates is the part of the worktree manager the worker builds
// and reads candidates with. It is satisfied by *gitworktree.Manager.
type MergeQueueCandidates interface {
	TargetCommit(ctx context.Context, branch string) (string, error)
	BuildQueueCandidate(ctx context.Context, request gitworktree.QueueCandidateRequest) (gitworktree.QueueCandidate, error)
	RestoreQueueCandidate(ctx context.Context, entry, commit string) (string, error)
	RemoveQueueCandidate(ctx context.Context, path string) error
	HoldsCommit(ctx context.Context, commit string) (bool, error)
	CandidateChanges(ctx context.Context, baseCommit, candidate string, limits gitworktree.DiffLimits) (gitworktree.BranchChange, error)
	repositoryReader
}

// MergeQueueRecords is the queue's durable half the worker works from. It is
// satisfied by *runstate.MergeQueueStore.
type MergeQueueRecords interface {
	Entries(key runstate.MergeQueueKey) ([]runstate.MergeQueueEntry, error)
	LeaseWorker(ctx context.Context, key runstate.MergeQueueKey) (*runstate.Lease, bool, error)
	Generations(key runstate.MergeQueueKey, entryID string) ([]runstate.MergeQueueGeneration, error)
	RecordGeneration(worker *runstate.Lease, key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration) error
	HoldStage(worker *runstate.Lease, key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration, stage runstate.MergeQueueStage) (*runstate.MergeQueueStageHold, error)
	StageRunning(key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration, stage runstate.MergeQueueStage) (bool, string, error)
}

// ErrMergeQueueWorkerBusy is a queue another process is already working.
var ErrMergeQueueWorkerBusy = errors.New("another process holds this merge queue's worker lease")

// MergeQueueWorker verifies the first waiting entry of one queue.
type MergeQueueWorker struct {
	// Pipeline supplies everything a check or a review is made with — the
	// configured checks and their runner, the reviewer, the run store the
	// entry's run is read from, the tracker its item is read from, the
	// configuration and its accounts, the repository product intent is read
	// from, and the clock — so the queue's
	// invocations obey the same capacity, spending, permission and evidence
	// rules a run's do.
	Pipeline   *Pipeline
	Queue      MergeQueueRecords
	Candidates MergeQueueCandidates
	// Events records the event stream of each generation's checks and review.
	Events func(event execution.Event) error
	// Waiting reports whether an admitted entry still waits to be integrated.
	// Landing and withdrawal belong to later work, which supplies it; nil is
	// every admitted entry waiting.
	Waiting func(entry runstate.MergeQueueEntry) bool
	// UsageLimits is where a provider refusing the review for want of capacity
	// is written down, as a branch review writes it; nil records nothing.
	UsageLimits UsageLimitRecorder
}

// mergeQueueWait is a stage the worker did not start, and why: a process an
// earlier worker started still running, the operator's pause, or a provider
// that cannot serve the review now. It is not a failure, and nothing is
// charged for it.
type mergeQueueWait struct{ reason string }

func (w mergeQueueWait) Error() string { return w.reason }

// MergeQueueVerification is what one call to Work found and did.
type MergeQueueVerification struct {
	// Entry is the entry worked, and zero when none was waiting.
	Entry runstate.MergeQueueEntry
	// Generation is the entry's newest generation as the call left it.
	Generation runstate.MergeQueueGeneration
	// Verified is the generation passing its gate; Refusal is why it does not.
	Verified bool
	Refusal  string
	// Drift counts the generations this call invalidated because the target
	// moved. It is reported apart because it is charged to nothing.
	Drift int
	// Waiting is what the call stopped short of starting a stage for, and
	// empty where it started everything it had to. A later call takes it up.
	Waiting string
}

// Work verifies the first waiting entry of a queue. It refuses with
// ErrMergeQueueWorkerBusy when another process is working the queue. An error
// is a failure to verify — a candidate that would not build, a record that
// would not save, a check runner or reviewer that failed — and leaves what was
// recorded for the next call to reconcile; a candidate that failed its checks
// or was refused by its reviewer is a completed verification, reported with
// Verified false.
func (w MergeQueueWorker) Work(ctx context.Context, key runstate.MergeQueueKey) (MergeQueueVerification, error) {
	if err := w.validate(); err != nil {
		return MergeQueueVerification{}, err
	}
	lease, held, err := w.Queue.LeaseWorker(ctx, key)
	if err != nil {
		return MergeQueueVerification{}, err
	}
	if !held {
		return MergeQueueVerification{}, ErrMergeQueueWorkerBusy
	}
	defer func() { _ = lease.Release() }()

	entry, found, err := w.next(key)
	if err != nil || !found {
		return MergeQueueVerification{}, err
	}
	outcome := MergeQueueVerification{Entry: entry}
	run, err := w.Pipeline.Store.Load(entry.RunID)
	if err != nil {
		return outcome, fmt.Errorf("read run %s, which merge queue entry %d was admitted for: %w", entry.RunID, entry.Order, err)
	}
	author := strings.TrimSpace(run.ProviderSessionID)
	if author == "" {
		return outcome, fmt.Errorf("run %s records no developer session, so no reviewer can be shown to be independent of it", entry.RunID)
	}
	configured := runstate.NewMergeQueueCheckConfiguration(w.Pipeline.Config.Checks)
	for {
		outcome, err = w.verify(ctx, lease, key, entry, run, author, configured, outcome)
		var wait mergeQueueWait
		if errors.As(err, &wait) {
			outcome.Waiting = wait.reason
			return outcome, nil
		}
		if err != nil || outcome.Verified || outcome.Refusal != "" {
			return outcome, err
		}
	}
}

// verify takes one generation as far as it goes: to a gate it passes or
// refuses, to a wait, or to the target moving under it, which is reported by
// returning with neither a verdict nor a refusal so the caller builds again.
func (w MergeQueueWorker) verify(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, entry runstate.MergeQueueEntry, run runstate.State, author string, configured runstate.MergeQueueCheckConfiguration, outcome MergeQueueVerification) (MergeQueueVerification, error) {
	generation, err := w.current(ctx, lease, key, entry, author, configured)
	outcome.Generation = generation
	if err != nil {
		return outcome, err
	}
	generation, moved, err := w.check(ctx, lease, key, generation)
	outcome.Generation = generation
	if err == nil && !moved && generation.CheckRun != nil && checksPassed(generation) {
		generation, moved, err = w.review(ctx, lease, key, entry, run, generation)
		outcome.Generation = generation
	}
	if err != nil {
		return outcome, err
	}
	if moved {
		outcome.Drift++
		if outcome.Drift > maxMergeQueueDriftRebuilds {
			outcome.Refusal = fmt.Sprintf("the target branch moved under %d candidates in a row; the next attempt builds on wherever it then stands", outcome.Drift)
		}
		return outcome, nil
	}
	if gate := generation.Gate(configured); gate != nil {
		outcome.Refusal = gate.Error()
		return outcome, nil
	}
	outcome.Verified = true
	return outcome, nil
}

func (w MergeQueueWorker) validate() error {
	var problems []error
	switch {
	case w.Pipeline == nil:
		problems = append(problems, errors.New("the merge queue worker needs the pipeline its checks and reviews are made with"))
	default:
		if w.Pipeline.Checks == nil || w.Pipeline.Reviewer == nil || w.Pipeline.Store == nil || w.Pipeline.Tracker == nil || w.Pipeline.Holds == nil {
			problems = append(problems, errors.New("the merge queue worker needs a check runner, a reviewer, a run store, a tracker, and the operator's pause on harness activity"))
		}
		if len(w.Pipeline.Config.Checks) == 0 {
			problems = append(problems, errors.New("the project configures no checks, so no merge queue candidate can earn its gate"))
		}
	}
	if w.Queue == nil || w.Candidates == nil {
		problems = append(problems, errors.New("the merge queue worker needs the queue's records and a way to build candidates"))
	}
	if w.Events == nil {
		problems = append(problems, errors.New("the merge queue worker needs somewhere to record its checks' and reviews' events"))
	}
	return errors.Join(problems...)
}

// next is the first entry in admission order that still waits.
func (w MergeQueueWorker) next(key runstate.MergeQueueKey) (runstate.MergeQueueEntry, bool, error) {
	entries, err := w.Queue.Entries(key)
	if err != nil {
		return runstate.MergeQueueEntry{}, false, err
	}
	for _, entry := range entries {
		if w.Waiting == nil || w.Waiting(entry) {
			return entry, true, nil
		}
	}
	return runstate.MergeQueueEntry{}, false, nil
}

func (w MergeQueueWorker) now() time.Time {
	return w.Pipeline.clock().Now().UTC()
}

// current is the generation to verify: the standing one, reconciled with what
// a worker before this one left, or a new one built on the target as it stands
// when there is none or the standing one can no longer be promoted.
func (w MergeQueueWorker) current(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, entry runstate.MergeQueueEntry, author string, configured runstate.MergeQueueCheckConfiguration) (runstate.MergeQueueGeneration, error) {
	generations, err := w.Queue.Generations(key, entry.EntryID)
	if err != nil {
		return runstate.MergeQueueGeneration{}, err
	}
	// A stage an earlier worker started and never finished may still be
	// running, and its checkout with it, so it is looked for before anything is
	// started, set aside, rebuilt, or restored.
	if count := len(generations); count > 0 {
		if err := w.awaitUnfinished(key, generations[count-1]); err != nil {
			return generations[count-1], err
		}
	}
	target, err := w.Candidates.TargetCommit(ctx, key.TargetBranch)
	if err != nil {
		return runstate.MergeQueueGeneration{}, fmt.Errorf("read where %s stands: %w", key.TargetBranch, err)
	}
	if count := len(generations); count > 0 && generations[count-1].Invalidated == nil {
		standing := generations[count-1]
		var reason runstate.MergeQueueInvalidationReason
		switch {
		case !standing.Checks.Same(configured):
			reason = runstate.MergeQueueChecksChanged
		case standing.TargetBase != target:
			reason = runstate.MergeQueueTargetMoved
		default:
			held, err := w.Candidates.HoldsCommit(ctx, standing.Candidate)
			if err != nil {
				return standing, err
			}
			if !held {
				reason = runstate.MergeQueueCandidateLost
			}
		}
		if reason == "" {
			return w.reconcile(ctx, lease, key, standing)
		}
		if _, err := w.invalidate(lease, key, standing, reason, target); err != nil {
			return standing, err
		}
	}
	candidate, err := w.Candidates.BuildQueueCandidate(ctx, gitworktree.QueueCandidateRequest{
		Entry:        entry.EntryID,
		TargetBranch: key.TargetBranch,
		Heads:        []string{entry.ApprovedHead},
		Message: fmt.Sprintf("yoyodyne: merge queue candidate for %s %s\n\nEntry: %s (order %d)\nRun: %s\nApproved head: %s\n",
			entry.WorkItemID, entry.WorkItemTitle, entry.EntryID, entry.Order, entry.RunID, entry.ApprovedHead),
	})
	if err != nil {
		return runstate.MergeQueueGeneration{}, fmt.Errorf("build the candidate for merge queue entry %d: %w", entry.Order, err)
	}
	generation := runstate.MergeQueueGeneration{
		Number:        uint64(len(generations) + 1),
		EntryID:       entry.EntryID,
		EntryOrder:    entry.Order,
		TargetBranch:  key.TargetBranch,
		TargetBase:    candidate.BaseCommit,
		Heads:         candidate.Heads,
		Candidate:     candidate.Commit,
		Content:       candidate.Tree,
		Checks:        configured,
		AuthorSession: author,
		CreatedAt:     w.now(),
		Checkout:      candidate.Path,
	}
	// The generation is on the record before anything judges it, so whatever
	// is recorded against it later names a candidate a reader can find. A save
	// that may not have landed is reported rather than worked past: the next
	// call reads which it was.
	if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
		return generation, fmt.Errorf("record generation %d of merge queue entry %d: %w", generation.Number, entry.Order, err)
	}
	return generation, nil
}

// awaitUnfinished reports a wait where a stage of the generation was started
// and never finished and a process it started may still be running.
func (w MergeQueueWorker) awaitUnfinished(key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration) error {
	for _, stage := range []struct {
		stage   runstate.MergeQueueStage
		started bool
	}{
		{runstate.MergeQueueStageChecks, generation.CheckRun != nil && generation.CheckRun.FinishedAt == nil},
		{runstate.MergeQueueStageReview, generation.Review != nil && generation.Review.FinishedAt == nil},
	} {
		if !stage.started {
			continue
		}
		running, reason, err := w.Queue.StageRunning(key, generation, stage.stage)
		if err != nil {
			return err
		}
		if running {
			return mergeQueueWait{reason: runstate.MergeQueueStageRunningError{Generation: generation.Number, Stage: stage.stage, Reason: reason}.Error()}
		}
	}
	return nil
}

// paused reports the operator's pause on harness activity as a wait, and a
// pause that cannot be read as an error, as everywhere else it is read.
func (w MergeQueueWorker) paused(stage runstate.MergeQueueStage) error {
	hold, held, err := w.Pipeline.operatorHold()
	if err != nil {
		return err
	}
	if held {
		return mergeQueueWait{reason: fmt.Sprintf("the operator has paused harness activity since %s, so the %s is not started until the pause is lifted",
			hold.HeldAt.Local().Format("2006-01-02 15:04 MST"), stage)}
	}
	return nil
}

// reviewerReady is the doors a run's review passes before its provider is
// asked, applied to the candidate's review: a provider recorded as answering
// nobody, a usage limit already known for the account and the reviewer's
// model, and a provider that is not installed or not logged in.
func (w MergeQueueWorker) reviewerReady(ctx context.Context, entry runstate.MergeQueueEntry, run runstate.State) error {
	p := w.Pipeline
	if p.ProviderOutages != nil {
		outage, standing, err := p.ProviderOutages.Standing()
		if err != nil {
			return fmt.Errorf("read whether the provider is answering: %w", err)
		}
		if standing {
			return mergeQueueWait{reason: "the review is not asked for while " + outage.Says()}
		}
	}
	if p.EndpointLimits != nil {
		limit, limited, err := p.EndpointLimits.KnownLimited(run.AccountAlias, p.reviewer().Model, p.clock().Now())
		if err != nil {
			return fmt.Errorf("read whether the reviewer's account is out of capacity: %w", err)
		}
		if limited {
			reason := "the reviewer's account and model are out of capacity: " + limit.Says
			if limit.ResetsAt != nil {
				reason += fmt.Sprintf("; the limit resets at %s", limit.ResetsAt.Local().Format("2006-01-02 15:04 MST"))
			}
			return mergeQueueWait{reason: reason}
		}
	}
	named := p.reviewer().Backend
	provider, ok := p.adapterFor(named)
	if !ok {
		return fmt.Errorf("the reviewer runs on %s, which this harness cannot check is ready", named)
	}
	if err := p.requireBackendReady(ctx, entry.WorkItemID, provider, named); err != nil {
		var outage ProviderOutageError
		if errors.As(err, &outage) {
			return mergeQueueWait{reason: outage.Error()}
		}
		return err
	}
	return nil
}

// launches writes each process a stage starts onto the generation before the
// process may do any work, and hands it the stage's hold.
type launches struct {
	worker     MergeQueueWorker
	lease      *runstate.Lease
	key        runstate.MergeQueueKey
	generation *runstate.MergeQueueGeneration
	hold       *runstate.MergeQueueStageHold
	stage      runstate.MergeQueueStage
}

func (l launches) gate(command string) (*execution.LaunchGate, error) {
	inherited, err := l.hold.Inherited()
	if err != nil || inherited == nil {
		return nil, err
	}
	host, err := os.Hostname()
	if err != nil {
		inherited.Close()
		return nil, fmt.Errorf("name this host for the launch record: %w", err)
	}
	return &execution.LaunchGate{Hold: inherited, Register: func(process execution.StartedProcess) error {
		recorded := *l.generation
		recorded.Launches = append(append([]runstate.MergeQueueLaunch(nil), recorded.Launches...), runstate.MergeQueueLaunch{
			Stage: l.stage, Command: command, Host: host,
			PID: process.PID, ProcessGroup: process.ProcessGroup, StartedAt: process.StartedAt.UTC(),
		})
		if err := l.worker.Queue.RecordGeneration(l.lease, l.key, recorded); err != nil {
			return fmt.Errorf("record the process the %s of generation %d was started as: %w", l.stage, recorded.Number, err)
		}
		*l.generation = recorded
		return nil
	}}, nil
}

// holdStage takes a stage's hold, reporting a hold still taken as a wait.
func (w MergeQueueWorker) holdStage(lease *runstate.Lease, key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration, stage runstate.MergeQueueStage) (*runstate.MergeQueueStageHold, error) {
	hold, err := w.Queue.HoldStage(lease, key, generation, stage)
	var running runstate.MergeQueueStageRunningError
	if errors.As(err, &running) {
		return nil, mergeQueueWait{reason: running.Error()}
	}
	return hold, err
}

// reconcile takes up a standing generation a worker before this one left: its
// checkout is put back at the recorded candidate, and a stage it started and
// never finished is written down as interrupted and cleared, so it is run
// again and earns only what the new run earns.
func (w MergeQueueWorker) reconcile(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration) (runstate.MergeQueueGeneration, error) {
	path, err := w.Candidates.RestoreQueueCandidate(ctx, generation.EntryID, generation.Candidate)
	if err != nil {
		return generation, fmt.Errorf("restore the checkout of generation %d: %w", generation.Number, err)
	}
	revised := generation
	revised.Checkout = path
	if revised.CheckRun != nil && revised.CheckRun.FinishedAt == nil {
		revised = setAside(revised, runstate.MergeQueueStageChecks, revised.CheckRun.StartedAt, "", w.now())
	}
	if revised.Review != nil && revised.Review.FinishedAt == nil {
		revised = setAside(revised, runstate.MergeQueueStageReview, revised.Review.StartedAt, "", w.now())
	}
	if revised.Checkout == generation.Checkout && len(revised.Interruptions) == len(generation.Interruptions) {
		return generation, nil
	}
	if err := w.Queue.RecordGeneration(lease, key, revised); err != nil {
		return generation, fmt.Errorf("record what was found of generation %d: %w", generation.Number, err)
	}
	return revised, nil
}

func (w MergeQueueWorker) invalidate(lease *runstate.Lease, key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration, reason runstate.MergeQueueInvalidationReason, target string) (runstate.MergeQueueGeneration, error) {
	generation.Invalidated = &runstate.MergeQueueInvalidation{Reason: reason, At: w.now()}
	if reason == runstate.MergeQueueTargetMoved {
		generation.Invalidated.ObservedTarget = target
	}
	if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
		return generation, fmt.Errorf("invalidate generation %d (%s): %w", generation.Number, reason, err)
	}
	return generation, nil
}

// moved reads the target again after a stage and invalidates the generation
// where it no longer stands on the generation's base. The stage's own record
// is already saved by then, so the evidence stays on the generation it was
// earned on.
func (w MergeQueueWorker) moved(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration) (runstate.MergeQueueGeneration, bool, error) {
	target, err := w.Candidates.TargetCommit(ctx, key.TargetBranch)
	if err != nil {
		return generation, false, fmt.Errorf("read where %s stands: %w", key.TargetBranch, err)
	}
	if target == generation.TargetBase {
		return generation, false, nil
	}
	generation, err = w.invalidate(lease, key, generation, runstate.MergeQueueTargetMoved, target)
	return generation, err == nil, err
}

// check runs the configured checks over the generation's candidate, unless
// they already finished on it.
func (w MergeQueueWorker) check(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, generation runstate.MergeQueueGeneration) (runstate.MergeQueueGeneration, bool, error) {
	if generation.CheckRun != nil && generation.CheckRun.FinishedAt != nil {
		if generation.CheckRun.Problem == "" {
			return w.moved(ctx, lease, key, generation)
		}
		generation = setAside(generation, runstate.MergeQueueStageChecks, generation.CheckRun.StartedAt, generation.CheckRun.Problem, w.now())
	}
	p := w.Pipeline
	if err := w.paused(runstate.MergeQueueStageChecks); err != nil {
		return generation, false, err
	}
	hold, err := w.holdStage(lease, key, generation, runstate.MergeQueueStageChecks)
	if err != nil {
		return generation, false, err
	}
	defer hold.Close()
	generation.CheckRun = &runstate.MergeQueueCheckEvidence{Binding: generation.Binding(), StartedAt: w.now()}
	if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
		return generation, false, fmt.Errorf("record that generation %d's checks began: %w", generation.Number, err)
	}
	launched := launches{worker: w, lease: lease, key: key, generation: &generation, hold: hold, stage: runstate.MergeQueueStageChecks}
	results, lastSequence, runErr := p.Checks.Run(ctx, checks.Request{
		RunID:        generation.EventStream(),
		Directory:    generation.Checkout,
		Commands:     generation.Checks.Commands,
		LastSequence: generation.LastSequence,
		// The candidate is checked whole: it is what would land, and nothing
		// narrows what a landing has to answer for.
		Env:  []string{checks.Narrowing{Whole: true, Reason: "a merge queue candidate is checked whole"}.Env()},
		Gate: launched.gate,
	}, w.Events)
	finished := w.now()
	if lastSequence > generation.LastSequence {
		generation.LastSequence = lastSequence
	}
	evidence := *generation.CheckRun
	evidence.FinishedAt = &finished
	for _, result := range results {
		evidence.Results = append(evidence.Results, runstate.MergeQueueCheckResult{
			Command:     result.Command,
			Passed:      result.Passed,
			ExitCode:    result.Process.ExitCode,
			Status:      string(result.Process.Status),
			CouldNotRun: boundedEvidence(result.CouldNotRun),
		})
		// A check stopped on time or cancelled judged nothing about the
		// candidate, so the run of them is incomplete rather than failed.
		switch result.Process.Status {
		case execution.ProcessTimedOut, execution.ProcessCancelled, execution.ProcessStalled:
			evidence.Problem = boundedEvidence(fmt.Sprintf("%s was %s and judged nothing", result.Command, result.Process.Status))
		}
	}
	if runErr != nil {
		evidence.Problem = boundedEvidence("the checks could not be run: " + runErr.Error())
	}
	generation.CheckRun = &evidence
	if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
		return generation, false, fmt.Errorf("record generation %d's checks: %w", generation.Number, err)
	}
	if runErr != nil {
		return generation, false, fmt.Errorf("run the checks of generation %d: %w", generation.Number, runErr)
	}
	return w.moved(ctx, lease, key, generation)
}

// setAside clears an attempt at a stage that earned nothing, keeping it among
// the generation's interruptions, so the stage runs again from nothing.
func setAside(generation runstate.MergeQueueGeneration, stage runstate.MergeQueueStage, started time.Time, problem string, found time.Time) runstate.MergeQueueGeneration {
	generation.Interruptions = append(append([]runstate.MergeQueueInterruption(nil), generation.Interruptions...),
		runstate.MergeQueueInterruption{Stage: stage, StartedAt: started, FoundAt: found, Problem: problem})
	if stage == runstate.MergeQueueStageChecks {
		generation.CheckRun = nil
	} else {
		generation.Review = nil
	}
	return generation
}

func checksPassed(generation runstate.MergeQueueGeneration) bool {
	run := generation.CheckRun
	if run == nil || run.FinishedAt == nil || run.Problem != "" || len(run.Results) != len(generation.Checks.Commands) {
		return false
	}
	for _, result := range run.Results {
		if !result.Passed || result.CouldNotRun != "" {
			return false
		}
	}
	return true
}

// review asks an independent reviewer about the generation's candidate,
// unless one already answered about it. The review is made the way a run's is
// — the same reviewer, under the run's account, charged to the run and its
// item as review spend — and it judges the candidate as a whole against the
// item it was approved for: the original approval was of the head alone, and
// it does not answer for what the head became on this target.
func (w MergeQueueWorker) review(ctx context.Context, lease *runstate.Lease, key runstate.MergeQueueKey, entry runstate.MergeQueueEntry, run runstate.State, generation runstate.MergeQueueGeneration) (runstate.MergeQueueGeneration, bool, error) {
	if generation.Review != nil && generation.Review.FinishedAt != nil {
		if generation.Review.Problem == "" {
			return w.moved(ctx, lease, key, generation)
		}
		generation = setAside(generation, runstate.MergeQueueStageReview, generation.Review.StartedAt, generation.Review.Problem, w.now())
	}
	p := w.Pipeline
	if err := w.paused(runstate.MergeQueueStageReview); err != nil {
		return generation, false, err
	}
	if err := w.reviewerReady(ctx, entry, run); err != nil {
		return generation, false, err
	}
	item, err := p.Tracker.Show(ctx, entry.WorkItemID)
	if err != nil {
		return generation, false, fmt.Errorf("read work item %s for the candidate's review: %w", entry.WorkItemID, err)
	}
	change, err := w.Candidates.CandidateChanges(ctx, generation.TargetBase, generation.Candidate, gitworktree.DiffLimits{})
	if err != nil {
		return generation, false, fmt.Errorf("describe generation %d's candidate: %w", generation.Number, err)
	}
	invariants, err := invariant.Store{RepositoryRoot: p.Repository, Directory: p.Config.Product.Invariants}.Load()
	if err != nil {
		return generation, false, fmt.Errorf("load architectural invariants: %w", err)
	}
	selected := invariants.Select(item.Title, item.Description, item.Design, item.AcceptanceCriteria, change.Changes.Status, change.Changes.DiffStat)
	revision := reviewedRevision(ctx, w.Candidates, generation.TargetBase)
	revision.CandidateFiles = reviewedRevision(ctx, w.Candidates, generation.Candidate).ListFiles
	intent, err := contextbundle.AssembleIntent(p.Repository, p.Config.Product.IntentRoot(p.Repository), p.Config.Product.Specifications, revision)
	if err != nil {
		return generation, false, fmt.Errorf("assemble product intent for the candidate's review: %w", err)
	}

	hold, err := w.holdStage(lease, key, generation, runstate.MergeQueueStageReview)
	if err != nil {
		return generation, false, err
	}
	defer hold.Close()
	generation.Review = &runstate.MergeQueueReviewEvidence{Binding: generation.Binding(), StartedAt: w.now()}
	if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
		return generation, false, fmt.Errorf("record that generation %d's review began: %w", generation.Number, err)
	}
	launched := launches{worker: w, lease: lease, key: key, generation: &generation, hold: hold, stage: runstate.MergeQueueStageReview}
	gate, err := launched.gate("")
	if err != nil {
		return generation, false, err
	}
	account := p.accountFor(run.AccountAlias)
	result, reviewErr := p.Reviewer.Review(ctx, review.Request{
		RunID:        generation.EventStream(),
		WorkItemID:   entry.WorkItemID,
		Context:      candidateReviewContext(entry, generation, item.Title, item.Description, item.Design, item.AcceptanceCriteria) + intent,
		Invariants:   selected.Text(),
		WorktreePath: generation.Checkout,
		Changes:      change.Changes,
		Repository:   reviewedRepository(ctx, w.Candidates, generation.Candidate, item, change.Changes),
		Checks:       candidateCheckResults(generation),
		RedactValues: p.RedactValues,
		LastSequence: generation.LastSequence,
		EventSink:    w.Events,
		Spend: spend.Attribution{
			ProductID:      p.Config.Product.ID,
			Agent:          p.agentNameForRole(domain.RoleReviewer),
			Phase:          runstate.SpendPhaseReview,
			AccountAlias:   run.AccountAlias,
			ConfigRevision: run.ConfigRevision,
			Backend:        p.reviewer().Backend,
			RunID:          entry.RunID,
			WorkItemID:     entry.WorkItemID,
		},
		AccountAlias:     account.Alias,
		AccountConfigDir: account.Directory,
		LaunchGate:       gate,
	})
	if gate != nil && gate.Hold != nil {
		// The gate closes its copy once the provider has started; one the
		// provider never started is closed here, and closing twice is harmless.
		_ = gate.Hold.Close()
	}
	finished := w.now()
	if result.LastSequence > generation.LastSequence {
		generation.LastSequence = result.LastSequence
	}
	evidence := *generation.Review
	evidence.FinishedAt = &finished
	evidence.SessionID = result.SessionID
	evidence.Model = result.RequestedModel
	evidence.ResolvedModel = result.ResolvedModel
	evidence.Summary = boundedEvidence(result.Verdict.Summary)
	if result.Decision.Valid() {
		evidence.Decision = string(result.Decision)
	}
	refusal := w.refusedReview(entry, result, reviewErr)
	switch {
	case refusal != "":
		evidence.Decision = ""
		evidence.Problem = boundedEvidence("the provider made no review: " + refusal)
	case reviewErr != nil:
		evidence.Decision = ""
		evidence.Problem = boundedEvidence("the review failed: " + reviewErr.Error())
	case result.RequestedModel != p.reviewer().Model:
		// The reviewer reports the selector it ran with, and a verdict from a
		// reviewer other than the configured one is no verdict, as it is for a
		// run.
		evidence.Decision = ""
		evidence.Problem = boundedEvidence(fmt.Sprintf("the reviewer ran with model %q, and the configured reviewer model is %q", result.RequestedModel, p.reviewer().Model))
	}
	generation.Review = &evidence
	if err := w.Queue.RecordGeneration(lease, key, generation); err != nil {
		return generation, false, fmt.Errorf("record generation %d's review: %w", generation.Number, err)
	}
	if refusal != "" {
		return generation, false, mergeQueueWait{reason: fmt.Sprintf("the provider made no review of generation %d: %s; it is asked again on a later pass", generation.Number, refusal)}
	}
	if reviewErr != nil {
		return generation, false, fmt.Errorf("review generation %d: %w", generation.Number, reviewErr)
	}
	if err := p.noticeProviderServed(); err != nil {
		return generation, false, err
	}
	return w.moved(ctx, lease, key, generation)
}

// refusedReview names a review the provider never made — refused for
// capacity, overloaded, not logged in or unreachable, or killed by something
// that judged nothing — and records what a run's review records of the same
// refusal. It is empty for a review the provider answered.
func (w MergeQueueWorker) refusedReview(entry runstate.MergeQueueEntry, result review.Result, reviewErr error) string {
	p := w.Pipeline
	what := fmt.Sprintf("the merge queue review of %s for run %s", entry.WorkItemID, entry.RunID)
	if limit, refused := refusedReviewForUsageLimit(result.UsageLimit, reviewErr); refused {
		_ = recordUsageLimit(w.UsageLimits, p.Config.Product.ID, p.clock().Now(), what, &limit)
		return "the provider's usage limit was reached"
	}
	if _, refused := refusedReviewForServerOverload(result.ServerOverload, reviewErr); refused {
		return "the provider's servers were overloaded"
	}
	if outage := result.ProviderOutage; outage != nil && reviewErr != nil {
		_ = p.noticeProviderOutage(outage.Cause, outage.Channel, outage.Detail, what, "")
		return fmt.Sprintf("the provider is not answering (%s)", outage.Cause)
	}
	if transient := result.TransientFailure; transient != nil && reviewErr != nil {
		return "the provider's invocation died before it judged anything"
	}
	return ""
}

// candidateReviewContext is what the harness knows about the candidate, and
// the item as it was approved.
func candidateReviewContext(entry runstate.MergeQueueEntry, generation runstate.MergeQueueGeneration, title, description, design, acceptance string) string {
	lines := []string{
		fmt.Sprintf("# Merge queue candidate for %s: %s", entry.WorkItemID, title),
		"",
		fmt.Sprintf("The change for this work item was approved at head `%s` and admitted to the merge queue for `%s`. The candidate under review is commit `%s`: the target branch at `%s` with that head merged onto it. It is the exact revision that would land, and nothing else is.",
			entry.ApprovedHead, generation.TargetBranch, generation.Candidate, generation.TargetBase),
		"",
		"The earlier approval was of the head on the base it was written against. It does not answer for this candidate: the target has other work in it now, and what you are asked is whether the change, combined with that work, still does what the item asks and breaks nothing. The configured checks have already run on this candidate, and their results are below.",
		"",
		"## Description",
		"",
		strings.TrimSpace(description),
	}
	if strings.TrimSpace(design) != "" {
		lines = append(lines, "", "## Design guidance", "", strings.TrimSpace(design))
	}
	if strings.TrimSpace(acceptance) != "" {
		lines = append(lines, "", "## Acceptance criteria", "", strings.TrimSpace(acceptance))
	}
	return strings.Join(lines, "\n") + "\n\n"
}

// candidateCheckResults is the generation's checks as the reviewer is shown
// them. Only the outcome is kept on the generation, so the output is not.
func candidateCheckResults(generation runstate.MergeQueueGeneration) []checks.Result {
	if generation.CheckRun == nil {
		return nil
	}
	results := make([]checks.Result, 0, len(generation.CheckRun.Results))
	for _, result := range generation.CheckRun.Results {
		results = append(results, checks.Result{
			Command:     result.Command,
			Passed:      result.Passed,
			CouldNotRun: result.CouldNotRun,
			Process:     execution.ProcessResult{ExitCode: result.ExitCode, Status: execution.ProcessStatus(result.Status)},
		})
	}
	return results
}

// boundedEvidence cuts free text to what a generation's evidence carries.
func boundedEvidence(text string) string {
	const bound = 4 << 10
	if len(text) <= bound {
		return text
	}
	cut := bound
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut]
}
