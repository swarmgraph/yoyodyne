package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// RetirementRuns reads publication evidence and saves the existing run record.
// It introduces no store or authority outside the run lifecycle.
type RetirementRuns interface {
	Recorded() ([]runstate.State, error)
	AdoptRun(context.Context, string) (runstate.State, *runstate.Lease, error)
	Save(runstate.State) error
}

// RunRetirer ends a run made obsolete by another run's confirmed merge. The
// caller must hold state.RunID's lease throughout Retire. The publication's
// lease is taken too, and both records and the item are read again before the
// retirement is saved. A live worker is never retired from a listing snapshot.
type RunRetirer struct {
	Runs    RetirementRuns
	Tracker WorkTracker
	Now     time.Time
	// ReadItem lets execution retain its existing tracker retry boundary.
	// Maintenance uses the tracker directly and records refusals per item.
	ReadItem func(context.Context, string) (beads.WorkItem, error)
}

// Retire returns handled for a saved retirement or an item whose closed status
// refuses continuation. Open or explicitly reopened work follows its existing
// lifecycle. Closed status alone never authorizes retirement.
func (r RunRetirer) Retire(ctx context.Context, state runstate.State) (runstate.State, bool, error) {
	if state.Retirement != nil {
		return r.note(ctx, state)
	}
	item, err := r.readItem(ctx, state.WorkItemID)
	if err != nil {
		return state, true, fmt.Errorf("read the item before continuing run %s: %w", state.RunID, err)
	}
	if item.ID != state.WorkItemID {
		return state, true, fmt.Errorf("the item read for run %s names %s, want %s", state.RunID, item.ID, state.WorkItemID)
	}
	if item.Status != "closed" {
		return state, false, nil
	}
	recorded, err := r.Runs.Recorded()
	if err != nil {
		return state, true, fmt.Errorf("read later publications of %s before retiring run %s: %w", item.Title, state.RunID, err)
	}
	var latest runstate.State
	for _, candidate := range recorded {
		if candidate.RunID == state.RunID || candidate.WorkItemID != state.WorkItemID || candidate.Retirement != nil || candidate.PullRequest == nil || handedBack(candidate) || !landedAfter(candidate, state.StartedAt) {
			continue
		}
		if latest.RunID == "" || laterLanding(candidate, latest) {
			latest = candidate
		}
	}
	if latest.RunID == "" {
		return state, true, fmt.Errorf("%s (%s) is closed but has no later confirmed merged publication; run %s is kept, and the development manager decides what becomes of its continuation", item.Title, item.ID, state.RunID)
	}
	publication, lease, err := r.Runs.AdoptRun(ctx, latest.RunID)
	if err != nil {
		return state, true, fmt.Errorf("read the publication of run %s under its lease before retiring run %s: %w", latest.RunID, state.RunID, err)
	}
	defer lease.Release()
	if publication.WorkItemID != state.WorkItemID || publication.RunID != latest.RunID || !confirmedCompletedPublication(publication) || !landedAfter(publication, state.StartedAt) || publication.Integration.TargetBranch != state.TargetBranch {
		return state, true, fmt.Errorf("%s (%s) is closed but its later publication in run %s is not a settled confirmed merge into %s; run %s is kept, and the harness retries after the publication settles", item.Title, item.ID, latest.RunID, state.TargetBranch, state.RunID)
	}
	// A newer publication may have appeared while the first listing was read.
	// Do not substitute an older confirmed merge for a newer unsettled one.
	recorded, err = r.Runs.Recorded()
	if err != nil {
		return state, true, fmt.Errorf("recheck later publications before retiring run %s: %w", state.RunID, err)
	}
	for _, candidate := range recorded {
		if candidate.RunID != state.RunID && candidate.WorkItemID == state.WorkItemID && candidate.Retirement == nil && candidate.PullRequest != nil && !handedBack(candidate) && laterLanding(candidate, publication) {
			return state, true, fmt.Errorf("a newer publication of %s (%s) appeared in run %s while retirement was checked; run %s is kept, and the harness rechecks its publication on the next pass", item.Title, item.ID, candidate.RunID, state.RunID)
		}
	}
	// A reopening while publication evidence was being read revokes retirement.
	item, err = r.readItem(ctx, state.WorkItemID)
	if err != nil {
		return state, true, fmt.Errorf("recheck the closed item before retiring run %s: %w", state.RunID, err)
	}
	if item.ID != state.WorkItemID {
		return state, true, fmt.Errorf("the rechecked item for run %s names %s, want %s", state.RunID, item.ID, state.WorkItemID)
	}
	if item.Status != "closed" {
		return state, false, nil
	}
	prior := state
	state.Retirement = &runstate.RunRetirement{
		RunID: publication.RunID, Commit: publication.PullRequest.MergeCommit,
		TargetBranch: publication.Integration.TargetBranch, Number: publication.PullRequest.Number,
		At: r.Now, PriorStatus: state.Status, PriorCompletedAt: state.CompletedAt, PriorFailure: state.Failure, PriorBlocker: state.Blocker,
		PriorWait: retirementWait(state),
	}
	// A terminal run cannot carry an instruction to resume. Keep what it was
	// waiting on in the retirement history before ending that wait.
	clearRecordedParks(&state)
	state.RedeployStop = nil
	state.Status = runstate.StatusCancelled
	state.CompletedAt = &r.Now
	state.UpdatedAt = r.Now
	state.Failure = retirementReason(state)
	state.Blocker = ""
	if err := r.Runs.Save(state); err != nil {
		return prior, true, fmt.Errorf("record the retirement of run %s: %w", state.RunID, err)
	}
	return r.note(ctx, state)
}

func (r RunRetirer) readItem(ctx context.Context, id string) (beads.WorkItem, error) {
	if r.ReadItem != nil {
		return r.ReadItem(ctx, id)
	}
	return r.Tracker.Show(ctx, id)
}

func confirmedCompletedPublication(state runstate.State) bool {
	p := state.PullRequest
	return state.Retirement == nil && state.Status == runstate.StatusSucceeded && state.Phase == runstate.PhaseComplete &&
		state.Integration != nil && p != nil && p.Merged && p.MergeCommit != "" && p.HeadCommit == state.Integration.SourceCommit &&
		p.Superseded == "" && p.HandedBack == nil && !state.Outstanding() && state.PublishFailure == "" && state.CleanupFailure == "" &&
		(state.LandingOutcome == "" || state.LandingOutcome == runstate.LandingDischarged) && state.LandingProblem == ""
}

func retirementReason(state runstate.State) string {
	r := state.Retirement
	return fmt.Sprintf("run %s was retired because %s (%s) is closed and run %s confirmed its merge through pull request #%d at %s into %s; its branch, worktree, developer session, and history are preserved, and its developer slot and reservations are released", state.RunID, state.WorkItemTitle, state.WorkItemID, r.RunID, r.Number, r.Commit, r.TargetBranch)
}

func retirementWait(state runstate.State) string {
	var waits []string
	if state.UsageLimitResetsAt != nil {
		waits = append(waits, fmt.Sprintf("paused for %s until %s", runstate.DescribePause(state.PauseCause, state.UsageLimitKind), state.UsageLimitResetsAt.Format(time.RFC3339Nano)))
	}
	if state.UsageLimitPausedSince != nil {
		waits = append(waits, "pause began at "+state.UsageLimitPausedSince.Format(time.RFC3339Nano))
	}
	if state.UsageLimitResetUnknown {
		waits = append(waits, "the provider gave no reset time")
	}
	if state.ProviderStop != "" {
		waits = append(waits, "the provider was stopped because "+describeProviderStop(state.ProviderStop))
	}
	if pause := state.DirectivePause; pause != nil {
		waits = append(waits, fmt.Sprintf("directive %s (%s): %s", pause.DirectiveID, pause.Kind, pause.Unresolved))
	}
	if state.DependencyPause != nil {
		waits = append(waits, "waiting on "+state.DependencyPause.Summary())
	}
	if state.TrackerPause != nil {
		waits = append(waits, state.TrackerPause.Summary())
	}
	if state.OperatorHeldSince != nil {
		waits = append(waits, "the operator's pause since "+state.OperatorHeldSince.Format(time.RFC3339Nano))
	}
	if stop := state.RedeployStop; stop != nil {
		waits = append(waits, fmt.Sprintf("watch session %s stopped it for redeploy at %s in %s after %s", stop.SessionID, stop.At.Format(time.RFC3339Nano), stop.Phase, stop.Bound()))
	}
	return runstate.RecordBlocker(strings.Join(waits, "; "))
}

func (r RunRetirer) note(ctx context.Context, state runstate.State) (runstate.State, bool, error) {
	if state.Retirement.NotedAt != nil {
		return state, true, nil
	}
	note := "Yoyodyne retired a run whose work item already merged.\n" + retirementReason(state)
	item, err := r.readItem(ctx, state.WorkItemID)
	if err != nil {
		return state, true, fmt.Errorf("read the prior retirement note of run %s: %w", state.RunID, err)
	}
	if !strings.Contains(item.Notes, note) {
		if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, note); err != nil {
			return state, true, fmt.Errorf("note the retirement of run %s: %w", state.RunID, err)
		}
	}
	retirement := *state.Retirement
	retirement.NotedAt = &r.Now
	state.Retirement = &retirement
	state.UpdatedAt = r.Now
	if err := r.Runs.Save(state); err != nil {
		return state, true, fmt.Errorf("record the delivered retirement note of run %s: %w", state.RunID, err)
	}
	return state, true, nil
}
