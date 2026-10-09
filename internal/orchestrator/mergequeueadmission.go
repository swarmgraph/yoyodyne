package orchestrator

// Where a run hands its approved change to the merge queue instead of
// promoting it itself: execution.merge_queue on, in
// docs/designs/integration-through-a-merge-queue.md, "Decision".
//
// The run's own gate is earned exactly as it always is — the protected-path
// gate, the checks, and an independent review of the change — and is read off
// the record before anything is admitted. What the switch removes is the run's
// own promotion and with it the replay onto a target that moved: the queue
// builds its candidate from the target as it then stands, and that candidate
// earns its own gate before it lands. The run's approval admits the change; it
// authorizes no landing.
//
// The mode is chosen at admission (internal/queuemode). Where neither mode can
// land into the target as it is protected — the target lands changes only
// through the forge's own merge queue, say — nothing is admitted: the queue's
// refusal is recorded beside it and said on the item, naming what would let
// the queue be used, and the change is integrated the way runs integrate with
// the switch off. A switch turned on in the wrong order stops no merges.
//
// A run whose change is admitted ends succeeded with its item still claimed and
// its branch and worktree kept, because the queue may hand the change back to
// this run for repair; it holds no developer slot while it waits. The queue
// records the landing on the run and settles the item when it lands.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/queuemode"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MergeQueueAdmissions is the queue a pipeline admits approved changes to, and
// where it records why a queue refused one. It is satisfied by
// *runstate.MergeQueueStore.
type MergeQueueAdmissions interface {
	runstate.MergeQueueAdmitter
	RecordRefusal(ctx context.Context, key runstate.MergeQueueKey, refusal runstate.MergeQueueRefusal) error
	ClearRefusal(ctx context.Context, key runstate.MergeQueueKey) error
}

// admitsToMergeQueue reports whether an approved change of this run goes to
// the merge queue rather than being promoted by the run.
func (a *activeRun) admitsToMergeQueue() bool {
	p := a.pipeline
	return p.Config.Execution.MergeQueue && p.MergeQueue != nil && a.state.Document == nil
}

// admitToMergeQueue admits the run's approved change to its target's queue and
// ends the run, or reports that the queue refused it, in which case the run
// promotes the change itself. The boolean is whether the run has ended here.
func (a *activeRun) admitToMergeQueue(ctx context.Context) (Outcome, bool, error) {
	if !a.admitsToMergeQueue() {
		return Outcome{}, false, nil
	}
	p := a.pipeline
	end := func(err error) (Outcome, bool, error) {
		outcome, err := a.endPromotion(ctx, stoppedBy(runstate.StopIntegration, err))
		return outcome, true, err
	}
	if err := a.integrationEarned(ctx); err != nil {
		return end(err)
	}
	// What was approved is committed, so the queue merges a commit rather than
	// a worktree: a published run's change is committed already, and a local
	// one's is committed here with nothing about its content changed.
	head, err := p.Worktrees.CommitAttempt(ctx, a.worktree, integrationMessage(a.item, a.outcome))
	if err != nil {
		return end(fmt.Errorf("commit the approved change for the merge queue: %w", err))
	}
	if head != a.state.HarnessCommit {
		a.recordHarnessCommit(head)
		a.state.UpdatedAt = p.clock().Now()
		if err := p.Store.Save(a.state); err != nil {
			return end(fmt.Errorf("record the commit admitted to the merge queue: %w", err))
		}
	}
	publishing, _, err := p.resolvePublishing(ctx)
	if err != nil {
		return end(fmt.Errorf("resolve whether the project publishes: %w", err))
	}
	var forge publish.QueueCapabilityReader
	if publishing {
		forge, _ = p.Publisher.(publish.QueueCapabilityReader)
	}
	key := runstate.MergeQueueKey{Repository: nonEmpty(a.state.RepositoryID, string(p.Config.Product.ID)), TargetBranch: a.worktree.TargetBranch}
	publication := ""
	if a.state.PullRequest != nil {
		publication = a.state.PullRequest.URL
	}
	admitted, err := queuemode.Admitter{
		Queue: p.MergeQueue, Forge: forge, Now: p.clock().Now,
		Harness: queuemode.Harness{PullRequests: publishing, CheckConfiguration: runstate.NewMergeQueueCheckConfiguration(p.Config.Checks).Digest},
	}.Admit(ctx, runstate.MergeQueueAdmission{
		Key: key, WorkItemID: a.state.WorkItemID, RunID: a.state.RunID,
		WorkItemTitle:     oneline.Fold(strings.TrimSpace(nonEmpty(a.item.Title, a.state.WorkItemID)), runstate.MaxMergeQueueTextBytes),
		Publication:       publication,
		ApprovedHead:      head,
		IntegrationPolicy: string(p.Config.Approvals.Integration),
	})
	if err != nil {
		return end(fmt.Errorf("admit the approved change to the merge queue for %s: %w", key.TargetBranch, err))
	}
	if admitted.Hold != nil {
		a.refuseMergeQueue(ctx, key, *admitted.Hold)
		return Outcome{}, false, nil
	}
	_ = p.MergeQueue.ClearRefusal(ctx, key)
	outcome, err := a.completeQueued(ctx, admitted.Entry)
	return outcome, true, err
}

// refuseMergeQueue records why the queue could not take the change and says
// so on the item. The change is promoted by the run instead, so the refusal is
// news rather than a stop: it names what would let the queue be used, and a
// repository setting only a person can change is named as theirs.
func (a *activeRun) refuseMergeQueue(ctx context.Context, key runstate.MergeQueueKey, hold queuemode.Hold) {
	p := a.pipeline
	says := fmt.Sprintf("The merge queue is switched on (execution.merge_queue) and cannot be used for %s: %s What would let it be used: %s. This change is integrated the way runs integrate with the queue switched off.",
		key.TargetBranch, strings.TrimSpace(hold.Requirement), hold.Step)
	if hold.PersonOnly != nil {
		says += " That is a repository setting, which only a person can change."
	}
	a.outcome.MergeQueueRefused = says
	_ = p.MergeQueue.RecordRefusal(ctx, key, runstate.MergeQueueRefusal{
		At: p.clock().Now(), WorkItemID: a.state.WorkItemID, RunID: a.state.RunID,
		Requirement: oneline.Fold(strings.TrimSpace(hold.Requirement), runstate.MaxMergeQueueTextBytes),
		Step:        oneline.Fold(strings.TrimSpace(hold.Step), runstate.MaxMergeQueueTextBytes),
		PersonOnly:  hold.PersonOnly != nil,
	})
	_ = a.recovering(ctx, runstate.RetryTrackerWrite, func(ctx context.Context) error {
		_, err := p.Tracker.RecordOutcome(ctx, a.state.WorkItemID, says)
		return err
	})
}

// completeQueued ends a run whose change the queue has taken. The admission is
// on the run's record before anything else is written, so a process that dies
// after it finds the run's change in the queue rather than admitting it again;
// a repeated admission of the same head is the same entry anyway.
func (a *activeRun) completeQueued(ctx context.Context, entry runstate.MergeQueueEntry) (Outcome, error) {
	p := a.pipeline
	admitted := entry.AdmissionRecord()
	a.state.MergeQueue = &admitted
	a.outcome.MergeQueue = &admitted
	a.state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(a.state); err != nil {
		return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("record the merge queue admission: %w", err)), runstate.StatusFailed)
	}
	notes := fmt.Sprintf("Approved and admitted to the merge queue for %s as entry %d (%s queue), at %s. %s The queue builds a candidate from %s as it then stands with this change merged onto it, runs the configured checks on it, has an independent reviewer approve it, and lands exactly that candidate; this item is settled when it lands, or the change is handed back to this run if the candidate fails on the change.",
		entry.TargetBranch, entry.Order, modeName(entry.Mode), shortCommit(entry.ApprovedHead), entry.ModeEvidence.Explanation, entry.TargetBranch)
	if err := a.recovering(ctx, runstate.RetryTrackerWrite, func(ctx context.Context) error {
		_, err := p.Tracker.RecordOutcome(ctx, a.state.WorkItemID, notes)
		return err
	}); err != nil {
		return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("record the merge queue admission on the item: %w", err)), runstate.StatusFailed)
	}
	a.recordPrice()
	completedAt := p.clock().Now()
	a.state.ProviderStop = ""
	a.state.RedeployStop = nil
	a.state.OperatorHeldSince = nil
	a.state.Status = runstate.StatusSucceeded
	// Cleaning up is what is owed once the queue lands the change and records
	// the landing as the run's integration; until then the run owes nothing,
	// and its branch and worktree are what a handback for repair continues.
	a.state.Phase = runstate.PhaseCleaningUp
	a.state.UpdatedAt = completedAt
	a.state.CompletedAt = &completedAt
	if err := p.Store.Save(a.state); err != nil {
		return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("save the run its change was queued by: %w", err)), runstate.StatusFailed)
	}
	a.outcome.Status = runstate.StatusSucceeded
	a.outcome.Phase = a.state.Phase
	return a.outcome, nil
}
