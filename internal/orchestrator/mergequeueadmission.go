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
// through the forge's own merge queue — or the protection cannot be read to
// say, nothing is admitted and nothing is merged the old way either: the
// operator chose the queue by the switch, and replaying the change would land
// it through the path they turned off. The change waits at its promotion with
// its approval standing, held with the setting named (CauseMergeQueueHeld),
// and the queue files one report for the condition however many changes it
// holds. The design is "GitHub's own merge queue" in the document above;
// mergequeueheld.go is what takes the held changes up again once the
// condition clears or the switch is turned off.
//
// A run whose change is admitted ends succeeded with its item still claimed and
// its branch and worktree kept, because the queue may hand the change back to
// this run for repair; it holds no developer slot while it waits. The queue
// records the landing on the run and settles the item when it lands.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/queuemode"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MergeQueueAdmissions is the queue a pipeline admits approved changes to, and
// where it records why a queue refused one. It is satisfied by
// *runstate.MergeQueueStore.
type MergeQueueAdmissions interface {
	runstate.MergeQueueAdmitter
	Refusal(key runstate.MergeQueueKey) (runstate.MergeQueueRefusal, bool, error)
	RecordRefusal(ctx context.Context, key runstate.MergeQueueKey, refusal runstate.MergeQueueRefusal) error
	ClearRefusal(ctx context.Context, key runstate.MergeQueueKey) error
}

// admitsToMergeQueue reports whether an approved change of this run goes to
// the merge queue rather than being promoted by the run.
func (a *activeRun) admitsToMergeQueue() bool {
	p := a.pipeline
	// A confirmed document lands by its own route. A run that brings a file in
	// goes through the queue like any approved change: with the queue on,
	// nothing replays a change onto a target that moved, so one kept out could
	// land on a target its checks never saw (outsidefilequeue.go).
	return p.Config.Execution.MergeQueue && p.MergeQueue != nil && a.state.Document == nil
}

// admitToMergeQueue admits the run's approved change to its target's queue and
// ends the run, or ends it holding the change where the queue cannot take it.
// The boolean is whether the run has ended here, which with the switch on it
// always has.
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
		return a.holdFromMergeQueue(ctx, key, *admitted.Hold)
	}
	_ = p.MergeQueue.ClearRefusal(ctx, key)
	outcome, err := a.completeQueued(ctx, admitted.Entry)
	return outcome, true, err
}

// ErrMergeQueueHeld is what a run stopped because its approved change could
// not be admitted to the merge queue unwraps to. integrationStopCauseOf reads
// it as CauseMergeQueueHeld, which is what makes the stop one the harness
// resumes on its own with the approval standing.
var ErrMergeQueueHeld = errors.New("the approved change is held from the merge queue")

// holdFromMergeQueue ends the run holding its approved change at the
// promotion: the item is blocked naming the setting that holds it, and the
// stop is recorded as the environment's, so it charges nothing and counts
// toward no brake. The queue's refusal record is what files the one report:
// a refusal already standing for this queue was reported when it was first
// found, and this change is only added to what it holds.
func (a *activeRun) holdFromMergeQueue(ctx context.Context, key runstate.MergeQueueKey, hold queuemode.Hold) (Outcome, bool, error) {
	p := a.pipeline
	requirement := strings.TrimSpace(hold.Requirement)
	says := fmt.Sprintf("The merge queue is switched on (execution.merge_queue), and this approved change is held rather than admitted to the queue for %s: %s What would let it be admitted: %s. It is not merged the way changes merge with the queue off, because the switch chose the queue; the harness admits it by itself, in the order the held changes were approved, at the first reading that finds the forge's merge queue no longer required, or integrates it the old way once execution.merge_queue is turned off.",
		key.TargetBranch, strings.TrimSuffix(requirement, ".")+".", strings.TrimSuffix(strings.TrimSpace(hold.Step), "."))
	if hold.PersonOnly != nil {
		says += " That is a repository setting or a login only a person can change; the harness changes no repository setting."
	}
	a.outcome.MergeQueueRefused = says
	if _, standing, err := p.MergeQueue.Refusal(key); err == nil && !standing {
		if err := p.MergeQueue.RecordRefusal(ctx, key, runstate.MergeQueueRefusal{
			At: p.clock().Now(), WorkItemID: a.state.WorkItemID, RunID: a.state.RunID,
			Requirement: oneline.Fold(requirement, runstate.MaxMergeQueueTextBytes),
			Step:        oneline.Fold(strings.TrimSpace(hold.Step), runstate.MaxMergeQueueTextBytes),
			PersonOnly:  hold.PersonOnly != nil,
		}); err == nil {
			a.reportMergeQueueHeld(key, says, hold)
		}
	}
	held := fmt.Errorf("%w: %s", ErrMergeQueueHeld, oneline.Fold(requirement, runstate.MaxEnvironmentalDetailBytes/2))
	if err := a.block(says); err != nil {
		outcome, err := a.endPromotion(ctx, stoppedBy(runstate.StopIntegration, withFailedRecord(held, fmt.Errorf("record the merge queue hold as a blocker: %w", err))))
		return outcome, true, err
	}
	outcome, err := a.endPromotion(ctx, stoppedBy(runstate.StopIntegration, held))
	return outcome, true, err
}

// reportMergeQueueHeld files the harness's one report of a queue it holds
// changes out of. It is a warning: nothing is lost and nothing lands wrongly,
// but approved work stops reaching the target until the condition clears. It
// is addressed to nobody where only a person can clear it, and to the
// development manager where what failed was a reading.
func (a *activeRun) reportMergeQueueHeld(key runstate.MergeQueueKey, says string, hold queuemode.Hold) {
	p := a.pipeline
	if p.Reports == nil {
		return
	}
	entry := report.Entry{Severity: report.SeverityWarning, Message: oneline.Fold(fmt.Sprintf(
		"Approved changes into %s are held out of the merge queue, starting with %s. %s", key.TargetBranch, a.state.WorkItemID, says), report.MaxMessageBytes)}
	if hold.PersonOnly == nil {
		entry.For = domain.RoleDevelopmentManager
	}
	collected, err := report.Collect([]report.Entry{entry}, report.Attribution{
		Role: report.HarnessReporter, RunID: a.state.RunID, WorkItemID: a.state.WorkItemID, Build: a.state.Build,
		ProductID: p.Config.Product.ID, RepositoryID: string(p.Config.Product.RepositoryID),
	}, p.clock().Now())
	if err != nil {
		a.noteReportProblem(report.HarnessReporter, err)
		return
	}
	for _, reported := range collected {
		if err := p.Reports.Append(reported); err != nil {
			a.noteReportProblem(report.HarnessReporter, err)
			continue
		}
		a.outcome.Reports = append(a.outcome.Reports, reported)
	}
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
	// The queue owns the entry from here, and may record on the run while this
	// process waits on the tracker, so every retry from now on is written as a
	// narrow update on the record as stored.
	a.narrowRetries = true
	notes := fmt.Sprintf("Approved and admitted to the merge queue for %s as entry %d (%s queue), at %s. %s The queue builds a candidate from %s as it then stands with this change merged onto it, runs the configured checks on it, has an independent reviewer approve it, and lands exactly that candidate; this item is settled when it lands, or the change is handed back to this run if the candidate fails on the change.",
		entry.TargetBranch, entry.Order, modeName(entry.Mode), shortCommit(entry.ApprovedHead), entry.ModeEvidence.Explanation, entry.TargetBranch)
	if err := a.recovering(ctx, runstate.RetryTrackerWrite, func(ctx context.Context) error {
		_, err := p.Tracker.RecordOutcome(ctx, a.state.WorkItemID, notes)
		return err
	}); err != nil {
		return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("record the merge queue admission on the item: %w", err)), runstate.StatusFailed)
	}
	a.recordPrice()
	// The tracker may have been waited on for a long time, and the queue may
	// have recorded on the run meanwhile — its landing, or a transfer — so the
	// ending is a narrow update of the run as stored rather than a save of the
	// copy this process held before the wait. Everything this process recorded
	// is already stored: the admission above, and each retry, written as a
	// narrow update on the stored record as it was taken (narrowRetries).
	ended, err := p.Store.Update(ctx, a.state.RunID, runstate.QueuedRunCompleted{At: p.clock().Now()})
	if err != nil {
		return a.fail(stoppedBy(runstate.StopRecording, fmt.Errorf("save the run its change was queued by: %w", err)), runstate.StatusFailed)
	}
	a.state = ended
	a.outcome.Status = runstate.StatusSucceeded
	a.outcome.Phase = a.state.Phase
	return a.outcome, nil
}
