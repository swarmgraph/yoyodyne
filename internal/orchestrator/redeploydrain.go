package orchestrator

// A run stopped for the redeploy of the session hosting it.
//
// A watching session that finds a build deployed over it waits out the runs it
// hosts before it restarts, and the wait is bounded: past
// execution.redeploy_drain_limit the session restarts anyway. What that costs
// the run it was hosting is this file. The session cancels the run's context
// with RedeployDrain as the cause, the run's pipeline reads that cause where
// it turns a stopped step into an ending, and instead of the cancelled run a
// killed process leaves — item still claimed, work to be developed again from
// scratch — it records a stop the session that comes back can continue from:
// the phase it was at, in the worktree and developer session it already has,
// with every counter as it was.
//
// It is the same shape as a provider the harness stopped on time, and
// deliberately so: both are the harness's own clock ending an invocation the
// provider had not finished, and both leave the run in flight and resumable.
// What differs is what the run is owed. A stopped provider is owed the rest of
// the attempt it was making; a run stopped at its checks is owed the gate from
// the checks, and one stopped at its review is owed the gate again, because a
// verdict half-made is no verdict.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// RedeployDrain is the cause a watch session cancels a hosted run's context
// with when its drain bound has run out. It is an error because that is what a
// context carries as its cause, and it is a type rather than a sentinel because
// what the run records about the stop — when, how long the session drained,
// which session — travels on it.
type RedeployDrain struct {
	At        time.Time
	Bound     time.Duration
	SessionID string
}

func (d RedeployDrain) Error() string {
	return fmt.Sprintf("the watch session hosting this run stopped it to restart into the build deployed over it, after draining for its bound of %s", d.Bound)
}

// drainedForRedeploy reports the run's context having been cancelled by its
// hosting session for a redeploy, and the cause it was cancelled with. It reads
// the context's cause rather than the error a step returned, because the step
// that met the cancellation may have wrapped it any number of ways on the way
// up, and a cause on the context is the one account nothing on that path can
// hide.
func drainedForRedeploy(ctx context.Context) (RedeployDrain, bool) {
	var drained RedeployDrain
	if errors.As(context.Cause(ctx), &drained) {
		return drained, true
	}
	return RedeployDrain{}, false
}

// redeployCommitTimeout bounds the commit a developer attempt stopped for a
// redeploy is given of what it left in the worktree. The commit is made under
// a context the stop did not cancel, so it needs a bound of its own; a commit
// that does not finish inside it is not lost, because the worktree still holds
// the change and the continuation's own attempt commits it.
const redeployCommitTimeout = time.Minute

// landingNoticeKey is the context key a hosting session's landing notice
// travels under.
type landingNoticeKey struct{}

// withLandingNotice is a hosted run's context carrying what the run calls as
// its landing checks begin. It travels on the context for the reason a
// dispatch's waits do: the session that hosts the run is the scheduler, the
// pipeline is built by whoever wired the start, and the context is the one
// thing both hand along.
func withLandingNotice(ctx context.Context, notice func()) context.Context {
	return context.WithValue(ctx, landingNoticeKey{}, notice)
}

// landingBegun tells the session hosting this run, where one asked, that the
// run is over and its landing checks are starting. From there the run has no
// in-flight record for the drain bound to read a phase off, and without the
// notice it would read as a run still before its claim.
func landingBegun(ctx context.Context) {
	if notice, wired := ctx.Value(landingNoticeKey{}).(func()); wired && notice != nil {
		notice()
	}
}

// hostedLandings is which of a session's hosted runs are in their landing
// checks, by their index on the schedule. It is written from each run's own
// goroutine as its landing begins and read by the session as it applies the
// drain bound, so it is guarded.
type hostedLandings struct {
	mu      sync.Mutex
	landing map[int]bool
}

func (h *hostedLandings) begun(index int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.landing == nil {
		h.landing = make(map[int]bool)
	}
	h.landing[index] = true
}

func (h *hostedLandings) landingAt(index int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.landing[index]
}

// pauseForRedeploy ends this process's part in a run its hosting session
// stopped for a redeploy, leaving the run in flight for the session that comes
// back.
//
// The stop is made durable first, and only where the run can be continued from
// it: a run stopped before it recorded a developer session, or at a phase
// nothing knows how to re-enter, has nothing a continuation could pick up, and
// is ended as cancelled with its branch and worktree preserved and its reason
// naming the redeploy — never left in flight with a marker nothing can act on,
// and never silently held.
func (a *activeRun) pauseForRedeploy(drained RedeployDrain) (Outcome, error) {
	stopped := runstate.RedeployStop{
		At:           a.pipeline.clock().Now(),
		Phase:        a.state.Phase,
		BoundSeconds: int64(drained.Bound / time.Second),
		SessionID:    drained.SessionID,
	}
	a.state.RedeployStop = &stopped
	if !stoppedForRedeployIsResumable(a.state) {
		a.state.RedeployStop = nil
		return a.fail(fmt.Errorf("%w; the run was at its %s phase with nothing recorded that a continuation could pick up, so it is cancelled with its branch and worktree preserved rather than held for a session that could not re-adopt it",
			drained, nonEmpty(string(a.state.Phase), "unrecorded")), runstate.StatusCancelled)
	}
	a.state.UpdatedAt = stopped.At
	if err := a.pipeline.Store.Save(a.state); err != nil {
		// A stop that could not be made durable leaves nothing for the session
		// that comes back to re-adopt, so the run is ended as cancelled rather
		// than left in flight claiming a continuation that is not recorded.
		a.state.RedeployStop = nil
		return a.fail(fmt.Errorf("%w; recording the stop failed, so the run is cancelled with its branch and worktree preserved rather than held: %v", drained, err), runstate.StatusCancelled)
	}
	a.outcome.Status = runstate.StatusRunning
	a.outcome.Phase = a.state.Phase
	a.outcome.Paused = true
	a.outcome.RedeployStop = &stopped
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	if !a.claimed {
		return a.outcome, nil
	}
	recordCtx, cancel := runTrackerContext(a.state.RunID, a.state.WorkItemID)
	defer cancel()
	if _, err := a.pipeline.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderRedeployStopNotes(a.outcome)); err != nil {
		// The stop is already durable, so a note that could not be written costs
		// the run nothing. It is still reported, for the same reason every other
		// pause reports it: an operator watching the tracker would otherwise see
		// the item simply stop moving.
		return a.outcome, fmt.Errorf("record the redeploy stop on the work item: %w", err)
	}
	return a.outcome, nil
}

// stoppedForRedeployIsResumable reports a run its hosting session stopped for a
// redeploy and which can be continued from durable state. A run at its
// developer attempt continues in the session that attempt established, so it
// needs one recorded; a run at its checks or its review re-earns the gate from
// the checks, in the worktree the developer left, and needs the session for
// the independence integration later demands. A run at its promotion is never
// stopped for a redeploy, so it is never one of these.
func stoppedForRedeployIsResumable(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.RedeployStop == nil {
		return false
	}
	// A run the drain stopped while it was already parked — asleep on a usage
	// limit, held by the operator, waiting on a directive or on other work — is
	// owed what that park owed it, which the park's own marker already says how
	// to continue. The stop is recorded beside it so the session that comes back
	// picks the run up, and the park is honoured from there.
	if pausedForUsageLimit(state) || pausedForOperatorHold(state) || pausedForDirective(state) || pausedForDependency(state) {
		return true
	}
	switch state.Phase {
	case runstate.PhaseDeveloping, runstate.PhaseChecking, runstate.PhaseReviewing:
	default:
		return false
	}
	if state.ProviderSessionID == "" {
		return false
	}
	return state.WorktreePath != "" && state.Branch != "" && state.BaseCommit != ""
}

// describeRedeployStop is the stop as one clause, for the reconcile listing and
// the schedule that re-adopts the run.
func describeRedeployStop(stopped runstate.RedeployStop) string {
	return fmt.Sprintf("the watch session hosting it stopped it at its %s phase at %s to restart into the build deployed over it, after draining for its bound of %s",
		stopped.Phase, stopped.At.UTC().Format(time.RFC3339), stopped.Bound())
}

// renderRedeployStopNotes describes a run its hosting session stopped for a
// redeploy, on the work item. It says plainly that the work was not abandoned
// and who continues it, because an operator reading a claimed item that has
// gone quiet has to be able to tell waiting from stopped.
func renderRedeployStopNotes(outcome Outcome) string {
	stopped := outcome.RedeployStop
	lines := []string{
		fmt.Sprintf("Yoyodyne paused this run for a redeploy: the watch session hosting it found a build deployed over it, drained for its bound of %s, and restarted with this run still at its %s phase. The run was stopped and preserved rather than waited out; nothing here says the change is wrong.",
			stopped.Bound(), stopped.Phase),
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	lines = append(lines,
		"This item stays claimed and its branch, worktree, developer session, and every counter are preserved.",
		"The session that comes back re-adopts this run at its first pull and continues it from here; `yoyo run` on this item continues it too, if no session does.",
	)
	return strings.Join(lines, "\n")
}
