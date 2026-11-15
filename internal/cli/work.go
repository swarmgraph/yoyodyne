package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// surveyedStatuses are the tracker slices a conversation reports, each asked
// for by name. A survey states what it found in every one of them, so an empty
// group is an answer rather than a gap.
var surveyedStatuses = []struct {
	status string
	// group is what the survey calls this slice, so a status that cannot be
	// read is reported as unknown exactly where it would have been listed.
	group string
	into  func(*chat.Survey, []chat.WorkItemSummary)
}{
	{"in_progress", "claimed", func(s *chat.Survey, items []chat.WorkItemSummary) { s.Claimed = items }},
	{"blocked", "blocked", func(s *chat.Survey, items []chat.WorkItemSummary) { s.Blocked = items }},
	{"open", "available", func(s *chat.Survey, items []chat.WorkItemSummary) { s.Available = items }},
	{"closed", "completed", func(s *chat.Survey, items []chat.WorkItemSummary) { s.Completed = items }},
}

// backlogStatuses are the tracker slices the backlog is assembled from: work
// that has been admitted and is not finished. Claimed work has been pulled
// already and closed work has left, so neither is still queued.
var backlogStatuses = []string{"open", "blocked"}

// conversationWork is the harness a product-manager conversation steers work
// with: the same pipeline `yoyodyne run` executes, the same reconciler
// `yoyodyne reconcile` settles with, and the same tracker and run state both
// read. Nothing here is a second path to development — it is the existing one,
// reachable from the conversation the operator is already in.
type conversationWork struct {
	tracker beads.Client
	store   *runstate.Store
	// remains is the repository the hold derivation asks whether a stopped run's
	// change is still there, which is what a hold on a preserved change is
	// decided from rather than the run's record. It is nil where the parts had
	// no worktree manager, and the derivation then answers from the record.
	remains readmodel.Remains
	// productID is what a record this conversation writes beside a run has to
	// name. The store knows its own product, and a record it will not accept from
	// the wrong one is exactly why this is carried rather than assumed.
	productID  domain.ProductID
	pipeline   orchestrator.Pipeline
	reconciler orchestrator.Reconciler
	// timeout bounds one tracker command taken on the conversation's behalf, so
	// an unresponsive tracker delays an answer rather than hanging the prompt.
	timeout time.Duration
}

func newConversationWork(parts components) conversationWork {
	return conversationWork{
		tracker:    parts.tracker(),
		store:      parts.store,
		remains:    remainsOf(parts),
		productID:  parts.config.Product.ID,
		pipeline:   pipelineFrom(parts),
		reconciler: reconcilerFrom(parts),
		timeout:    chatTrackerTimeout,
	}
}

// conversationDirectives is the durable directive record a conversation reads
// and writes: the same product-scoped store every run consults before it commits
// to work. It is the one place the conversation supplies what the operator does
// not get to assert — the directive's identity, when it was received, and which
// product it belongs to.
type conversationDirectives struct {
	store     *runstate.DirectiveStore
	productID domain.ProductID
}

// Record stamps a request with what the harness knows and makes it durable.
// Every directive recorded here names the product manager as the role that
// received it, because that is the agent the operator is talking to; a directive
// given to any other agent is recorded by `yoyo directive record --received-by`
// and lands in exactly the same place.
func (d conversationDirectives) Record(_ context.Context, request chat.DirectiveRequest) (directive.Directive, error) {
	id, err := directive.NewID()
	if err != nil {
		return directive.Directive{}, err
	}
	recorded := directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            id,
		ProductID:     d.productID,
		Kind:          request.Kind,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    time.Now().UTC(),
		Text:          strings.TrimSpace(request.Text),
		Artifact:      strings.TrimSpace(request.Artifact),
		Unresolved:    strings.TrimSpace(request.Unresolved),
		Scope:         request.Scope,
	}
	if err := d.store.Record(recorded); err != nil {
		return directive.Directive{}, err
	}
	return recorded, nil
}

func (d conversationDirectives) List(_ context.Context) ([]directive.Directive, error) {
	return d.store.List()
}

func (d conversationDirectives) Find(_ context.Context, reference string) (directive.Directive, error) {
	return d.store.Find(reference)
}

func (d conversationDirectives) Resolve(_ context.Context, reference, resolution string) (directive.Directive, error) {
	return d.store.Resolve(reference, resolution, time.Now())
}

// CarryOut records what came of a directive that paused nothing, in the same
// product-scoped store every run and every other surface reads. What became of a
// directive is a fact about the product rather than about the conversation that
// happened to record it, which is why it lands here and not in the conversation's
// own log.
func (d conversationDirectives) CarryOut(_ context.Context, reference, outcome string) (directive.Directive, error) {
	return d.store.CarryOut(reference, outcome, time.Now())
}

// Withdraw takes a directive out of force in the same product-scoped store, for
// the same reason: whether the operator still means a directive is a fact about
// the product, and one withdrawn in a conversation has to stop reaching the runs
// every other process makes.
func (d conversationDirectives) Withdraw(_ context.Context, reference, by string, role domain.AgentRole, reason string) (directive.Directive, error) {
	return d.store.Withdraw(reference, by, role, reason, time.Now())
}

// ResolveInto ends a directive in the same product-scoped store by naming what
// now carries it, so a directive the Lead Product Manager wrote into a document
// stops reaching the runs every other process makes as live direction.
func (d conversationDirectives) ResolveInto(_ context.Context, reference, became, by string, role domain.AgentRole, reason string) (directive.Directive, error) {
	return d.store.ResolveInto(reference, became, by, role, reason, time.Now())
}

// Survey reads what the harness has in flight from durable run state and what
// the work looks like from the tracker. The two are separate questions: a run
// another process is executing is in the state whether or not the tracker has
// caught up with it.
// A part that cannot be read is named in the survey rather than failing it: an
// operator asking what is in flight is worse off with nothing than with the
// half the harness could answer, as long as the missing half is stated.
func (w conversationWork) Survey(ctx context.Context) (chat.Survey, error) {
	survey := chat.Survey{}
	incomplete, err := w.store.Incomplete()
	if err != nil {
		survey.Unavailable = append(survey.Unavailable, chat.Unread{Group: chat.InFlightGroup(), Reason: err.Error()})
	}
	for _, state := range incomplete {
		snapshot := snapshotOf(state)
		// Whether the operator has already asked this run to stop is read beside the
		// run, because that is where they wrote it. A request nobody could read is
		// left out of the snapshot rather than failing the survey: what is in flight
		// is still worth reading, and the run says the same thing about itself once
		// it honors the request.
		if _, requested, err := w.store.StopRequested(state.RunID); err == nil {
			snapshot.StopRequested = requested
		}
		survey.InFlight = append(survey.InFlight, snapshot)
	}
	for _, group := range surveyedStatuses {
		items, err := w.list(ctx, group.status)
		if err != nil {
			survey.Unavailable = append(survey.Unavailable, chat.Unread{Group: group.group, Reason: err.Error()})
			continue
		}
		group.into(&survey, items)
	}
	return survey, nil
}

// Backlog reads the admitted work and puts it in the product manager's order.
// It is the same tracker the survey reads and the same ordering a development
// manager pulls in, assembled here rather than stored, so the queue can never
// drift from the priorities the product manager actually set.
//
// A slice that cannot be read fails the whole answer, where a survey would name
// it as unknown and report the rest. The difference is what the two are for: a
// survey describes what is happening, and a partial one is still worth reading,
// while a backlog says what comes next, and half a queue would answer that
// question wrongly rather than incompletely.
func (w conversationWork) Backlog(ctx context.Context) (backlog.Queue, error) {
	var admitted []beads.WorkItem
	for _, status := range backlogStatuses {
		items, err := w.listItems(ctx, status)
		if err != nil {
			return backlog.Queue{}, err
		}
		admitted = append(admitted, items...)
	}
	// The order comes from the listings; which of those items can actually be
	// pulled comes from the tracker, because a blocker lives in its dependency
	// graph rather than reliably in a status listing.
	trackerCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	ready, err := w.tracker.Ready(trackerCtx)
	if err != nil {
		return backlog.Queue{}, fmt.Errorf("list the work items the tracker reports as ready: %w", err)
	}
	pullable := make([]string, 0, len(ready))
	for _, item := range ready {
		pullable = append(pullable, item.ID)
	}
	// And what somebody still has to release comes from the harness's own records,
	// through the derivation the scheduler and every operator surface read. A
	// conversation that worked this out for itself would be a second opinion about
	// which work is startable, in front of the role that decides what to start. A
	// conversation wired without the records holds every blocked item rather than
	// releasing work whose hold it could not read.
	var held backlog.Holds
	var discharged map[string][]string
	if w.store != nil {
		held, err = readmodel.HeldForAPerson(ctx, w.store, w.store.Triage(), w.remains, w.tracker)
		if err != nil {
			return backlog.Queue{}, fmt.Errorf("read what the harness is holding back after stopped runs: %w", err)
		}
		// The human gates a person has recorded passing come from the same store
		// rather than from the tracker, which has no way to know about them: the
		// only completion a tracker records is an item being closed, and closure
		// passing a gate that reserved somebody's step is what this exists to stop.
		// A conversation wired without the store holds every declared gate, for the
		// same reason it holds every blocked item.
		discharged, err = w.store.DischargedGates()
		if err != nil {
			return backlog.Queue{}, fmt.Errorf("read the steps a person has recorded taking: %w", err)
		}
	}
	return backlog.Order(admitted, pullable, held, discharged), nil
}

// Run executes one work item through the pipeline. The outcome is reported even
// when the run failed, because a failed run's branch, worktree, and blocker are
// what the operator decides about next.
func (w conversationWork) Run(ctx context.Context, workItemID string, selection chat.Selection) (chat.RunReport, error) {
	// The pipeline is a value, so the selection is set on this run's copy of it
	// and reaches the run state without becoming a property of the conversation:
	// two runs started from one conversation record why each of them was started.
	pipeline := w.pipeline
	pipeline.Selection = runstate.Selection{By: selection.By, Reason: selection.Reason}
	outcome, err := pipeline.Run(ctx, workItemID)
	return runReportOf(outcome), err
}

// RequestStop records the operator's decision that one run should stop, where
// the process working on that run reads it. It never adopts the run and never
// takes its lease: the process holding it is the only thing entitled to end it,
// and taking the lease to stop a run would be this process deciding something
// about work it is not doing.
func (w conversationWork) RequestStop(_ context.Context, request chat.StopRequest) error {
	return w.store.RecordStop(runstate.StopRequest{
		SchemaVersion: runstate.StopSchemaVersion,
		ProductID:     w.productID,
		RunID:         request.RunID,
		WorkItemID:    request.WorkItemID,
		RequestedAt:   time.Now().UTC(),
		Reason:        request.Reason,
	})
}

// Progress reports where the most recent recorded run of a work item has got
// to. It reads the durable run state and nothing else, exactly as Changes does
// and for the same reason: the record is written as the run goes, so a
// conversation watching one is reading what actually happened rather than
// asking the process executing it. Reading decides nothing, so a run another
// process is executing is as readable here as one this conversation started.
func (w conversationWork) Progress(_ context.Context, workItemID string) (chat.RunProgress, error) {
	state, err := w.store.Latest(workItemID)
	if err != nil {
		return chat.RunProgress{}, err
	}
	return progressOf(state), nil
}

// Price reports what one work item cost, broken down by the runs made for it.
// The price reads the durable run records, which is what makes it answerable
// for an item closed long ago: the run state
// and the event logs those runs wrote are still here, and the cost in them is
// what the provider reported rather than an estimate from a price table. What
// survives beside that price is asked of the repository as the hold asks it.
func (w conversationWork) Price(ctx context.Context, workItemID string) (chat.ItemPrice, error) {
	price, err := w.store.Price(workItemID)
	if err != nil {
		return chat.ItemPrice{}, err
	}
	prices := []runstate.ItemPrice{price}
	readmodel.LookForPrices(ctx, w.remains, w.store, prices)
	return priceOf(prices[0]), nil
}

// Changes reports what the most recent recorded run of a work item changed. It
// reads the durable run state and nothing else: no worktree is inspected and no
// git command is run, so the answer is the same whether the run finished a
// moment ago or was cleaned up weeks back and had its worktree and branch
// removed. Reading a record decides nothing about the run, so a run another
// process is executing is as readable here as a finished one.
func (w conversationWork) Changes(_ context.Context, workItemID string) (chat.RunChanges, error) {
	state, err := w.store.Latest(workItemID)
	if err != nil {
		return chat.RunChanges{}, err
	}
	return changesOf(state), nil
}

// Direct appends the operator's direction to the item's notes, which is where
// the next attempt at it reads it: the developer's context carries the item's
// notes, so direction recorded here reaches whoever picks the work up. It
// deliberately does not touch the item's status — saying what to do differently
// is not deciding the work is done or blocked.
func (w conversationWork) Direct(ctx context.Context, workItemID, note string) error {
	trackerCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	if _, err := w.tracker.RecordOutcome(trackerCtx, workItemID, note); err != nil {
		return err
	}
	return nil
}

// Settle runs the same reconciliation `yoyodyne reconcile` runs. It is safe to
// repeat and leaves alone every run a live process still holds, so settling
// after stopping one run never disturbs another.
func (w conversationWork) Settle(ctx context.Context) ([]chat.Settlement, error) {
	results, err := w.reconciler.Reconcile(ctx)
	settlements := make([]chat.Settlement, 0, len(results))
	for _, result := range results {
		settlements = append(settlements, chat.Settlement{
			RunID:      result.RunID,
			WorkItemID: result.WorkItemID,
			Action:     string(result.Action),
			Detail:     result.Detail,
			Failure:    result.Failure,
		})
	}
	return settlements, err
}

func (w conversationWork) list(ctx context.Context, status string) ([]chat.WorkItemSummary, error) {
	items, err := w.listItems(ctx, status)
	if err != nil {
		return nil, err
	}
	summaries := make([]chat.WorkItemSummary, 0, len(items))
	for _, item := range items {
		summary := chat.WorkItemSummary{
			ID:       item.ID,
			Title:    item.Title,
			Status:   item.Status,
			Priority: item.Priority,
			Parked:   item.Parking.Parked(),
		}
		// The price comes from the tracker along with everything else about the
		// item, because the tracker is where a completed run put it. An item
		// nothing has priced carries none, and none is shown rather than a zero.
		if item.Cost != nil {
			summary.Cost = &chat.ItemCost{
				TotalUSD:    item.Cost.TotalUSD,
				Runs:        item.Cost.Runs,
				UnknownRuns: item.Cost.UnknownRuns,
			}
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// listItems asks the tracker for one status, bounded so an unresponsive tracker
// delays an answer rather than hanging the prompt.
func (w conversationWork) listItems(ctx context.Context, status string) ([]beads.WorkItem, error) {
	trackerCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	items, err := w.tracker.List(trackerCtx, status)
	if err != nil {
		return nil, fmt.Errorf("list %s work items: %w", status, err)
	}
	return items, nil
}

// snapshotOf describes one recorded run for a conversation. A run waiting out a
// provider usage limit is named as waiting rather than reported as work in
// progress, because those call for opposite responses from an operator.
func snapshotOf(state runstate.State) chat.RunSnapshot {
	snapshot := chat.RunSnapshot{
		RunID:      state.RunID,
		WorkItemID: state.WorkItemID,
		Status:     string(state.Status),
		Phase:      string(state.Phase),
		Branch:     state.Branch,
		StartedAt:  state.StartedAt,
	}
	// Why the item was chosen comes from what the run recorded when it started. A
	// run that recorded nothing leaves both fields empty, and a survey says so in
	// those words rather than showing a blank line where a reason should be.
	if state.Selection != nil {
		snapshot.SelectedBy = state.Selection.By
		snapshot.SelectedBecause = state.Selection.Reason
	}
	switch {
	case state.DirectivePause != nil:
		snapshot.Detail = fmt.Sprintf("paused for unresolved directive %s: %s",
			state.DirectivePause.DirectiveID, state.DirectivePause.Unresolved)
	case state.DependencyPause != nil:
		snapshot.Detail = "paused waiting on unfinished work it depends on: " + state.DependencyPause.Summary()
	case state.TrackerPause != nil:
		snapshot.Detail = "parked waiting on the tracker to answer: " + state.TrackerPause.Summary()
	case state.UsageLimitResetsAt != nil:
		snapshot.Detail = fmt.Sprintf("paused for %s until %s",
			runstate.DescribePause(state.PauseCause, state.UsageLimitKind), state.UsageLimitResetsAt.UTC().Format(time.RFC3339))
	case state.ProviderStop == runstate.ProviderStopStalled:
		snapshot.Detail = "the AI session running it produced no output for longer than the harness allows, so the harness stopped it; the cause was outside the work, and the run can be continued"
	case state.ProviderStop == runstate.ProviderStopBudgetExhausted:
		snapshot.Detail = "its provider ran out of total budget while still working; the run can be continued"
	}
	return snapshot
}

// changesOf projects one recorded run into the account of what it changed. It
// claims nothing the record does not hold: the promotion comes from the
// recorded integration, the preserved worktree from the cleanup markers rather
// than from the path still being written down, and the change itself from the
// summary the run recorded while its worktree existed.
func changesOf(state runstate.State) chat.RunChanges {
	changes := chat.RunChanges{
		RunID:        state.RunID,
		WorkItemID:   state.WorkItemID,
		Status:       string(state.Status),
		Phase:        string(state.Phase),
		StartedAt:    state.StartedAt,
		CompletedAt:  state.CompletedAt,
		Branch:       state.Branch,
		WorktreePath: state.WorktreePath,
		Preserved:    state.WorktreePath != "" && !state.WorktreeRemoved,
		Failure:      state.Failure,
	}
	if state.Changes != nil {
		changes.Files = state.Changes.Files
		changes.DiffStat = state.Changes.DiffStat
	}
	if state.Integration != nil {
		changes.Integrated = true
		changes.TargetBranch = state.Integration.TargetBranch
		changes.Commit = state.Integration.TargetCommit
	}
	if state.PullRequest != nil {
		changes.PullRequest = &chat.PublishedChange{
			Number: state.PullRequest.Number,
			URL:    state.PullRequest.URL,
			State:  state.PullRequest.State,
			Merged: state.PullRequest.Merged,
		}
	}
	return changes
}

// priceOf projects the recorded price of one item's runs into what a
// conversation reports. It claims nothing the records do not hold: a run whose
// evidence is gone is carried through as unknown rather than as a run that cost
// nothing, and the total is the sum of the runs that could actually be priced.
func priceOf(price runstate.ItemPrice) chat.ItemPrice {
	projected := chat.ItemPrice{
		WorkItemID:  price.WorkItemID,
		TotalUSD:    price.TotalUSD,
		UnknownRuns: price.UnknownRuns,
	}
	for _, run := range price.Runs {
		projected.Runs = append(projected.Runs, chat.RunPrice{
			RunID:       run.RunID,
			Status:      string(run.Status),
			Outcome:     string(run.Outcome),
			Phase:       string(run.Phase),
			StartedAt:   run.StartedAt,
			CompletedAt: run.CompletedAt,
			Integrated:  run.Integrated,
			Remains:     run.Remains,
			Invocations: run.Invocations,
			CostUSD:     run.CostUSD,
			Unknown:     run.Unknown,
		})
	}
	return projected
}

// progressOf projects one recorded run into where it has got to. Like the
// account of what a run changed, it claims nothing the record does not hold:
// the promotion comes from the recorded integration and the merge from what the
// forge was last observed to say, rather than from the run having reached a
// phase where either was attempted.
func progressOf(state runstate.State) chat.RunProgress {
	progress := chat.RunProgress{
		RunID:          state.RunID,
		Status:         string(state.Status),
		Phase:          string(state.Phase),
		ChecksPassed:   checksBehind(state),
		ReviewDecision: state.ReviewDecision,
	}
	if state.Integration != nil {
		progress.Integrated = true
		progress.TargetBranch = state.Integration.TargetBranch
	}
	if state.PullRequest != nil {
		progress.MergeQueued = state.PullRequest.MergeQueued
		progress.Merged = state.PullRequest.Merged
	}
	return progress
}

// checksBehind reports a run with the deterministic checks behind it: the
// record carries no failing check and the run has moved past running them. Both
// halves are needed. A run in a later phase with a failing check recorded is one
// the checks handed back to the developer, and a run that has not reached
// reviewing has not been past the gate yet whatever else its record says.
func checksBehind(state runstate.State) bool {
	if state.CheckFailure != nil {
		return false
	}
	switch state.Phase {
	case runstate.PhaseReviewing, runstate.PhaseIntegrating, runstate.PhaseCompleting, runstate.PhaseCleaningUp, runstate.PhaseComplete:
		return true
	default:
		return false
	}
}

// runReportOf projects a pipeline outcome into what a conversation reports. It
// claims integration only from the recorded promotion itself, so a run that
// ended any other way is never described as integrated.
func runReportOf(outcome orchestrator.Outcome) chat.RunReport {
	// Read before the projection, which shadows the package this comes from.
	worstReported := report.Worst(outcome.Reports)
	report := chat.RunReport{
		RunID:               outcome.RunID,
		WorkItemID:          outcome.WorkItemID,
		Status:              string(outcome.Status),
		Branch:              outcome.Branch,
		WorktreePath:        outcome.WorktreePath,
		WorkItemClosed:      outcome.WorkItemClosed,
		RepairAttempts:      outcome.RepairAttempts,
		TransientRelaunches: outcome.TransientRelaunches,
		Blocked:             outcome.Blocked,
		Paused:              outcome.Paused,
		UsageLimitKind:      outcome.UsageLimitKind,
		UsageLimitResetsAt:  outcome.UsageLimitResetsAt,
		PauseCause:          outcome.PauseCause,
		ProviderStop:        outcome.ProviderStop,
		Failure:             outcome.Failure,
		Reported:            len(outcome.Reports),
		ReportedWorst:       worstReported,
		ReportProblem:       outcome.ReportProblem,
	}
	if outcome.Integration != nil {
		report.Integrated = true
		report.Undischarged = !outcome.Discharges()
		// The account and the disposition describe an item that was not closed, so
		// they are absent from a run that closed one rather than carried beside it
		// saying where an item nobody left open was left.
		if report.Undischarged {
			report.UndischargedReason = outcome.UndischargedAccount()
			report.UndischargedDisposition = outcome.UndischargedDisposition()
		}
		report.TargetBranch = outcome.Integration.TargetBranch
		report.Commit = outcome.Integration.TargetCommit
	}
	// A directive pause is summarized to one line here rather than carried whole:
	// what a conversation says about a paused run is a headline, and /directives
	// is where the directive itself is read.
	if outcome.PausedByDirective != nil {
		report.DirectivePause = outcome.PausedByDirective.Summary()
	}
	// A dependency pause is carried the same way and for the same reason: what a
	// conversation says about it is which work is being waited on, and the items
	// themselves are read with /show.
	if outcome.PausedByDependency != nil {
		report.DependencyPause = outcome.PausedByDependency.Summary()
	}
	// A tracker park is carried the same way: what a conversation says about it is
	// which read went unanswered and for how long, and the run's own record holds
	// each wait that was taken.
	if outcome.PausedByTracker != nil {
		report.TrackerPause = outcome.PausedByTracker.Summary()
	}
	// The operator's own pause is carried as the moment they placed it, which is
	// the whole of what a conversation has to say about it: what lifts it is one
	// command rather than anything about this work item.
	if outcome.PausedByOperator != nil {
		heldAt := outcome.PausedByOperator.HeldAt
		report.OperatorHeldSince = &heldAt
	}
	// A held intake is carried whole: unlike the hold over spending, this one is
	// usually placed because something looked wrong, and who placed it and what
	// that was are together the whole of what makes it actionable later.
	if outcome.PausedByIntake != nil {
		hold := *outcome.PausedByIntake
		report.IntakeHold = &hold
	}
	return report
}
