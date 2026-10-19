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

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
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
	// WaitedFor is the check the session let finish before the run stopped,
	// where it asked the run to stop before its next check (checkYield) rather
	// than cancelling it outright.
	WaitedFor string
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
// checks, by their index on the schedule, and the yield each hosted run's
// check stage answers. It is written from each run's own goroutine as its
// landing begins and read by the session as it applies the drain bound, so it
// is guarded.
type hostedLandings struct {
	mu      sync.Mutex
	landing map[int]bool
	yields  map[int]*checkYield
}

// hostedContext is the context a session hosts one run under: one of its own,
// so the drain bound can stop it without stopping the session, carrying the
// landing notice and the check yield the bound reaches the run through.
func hostedContext(ctx context.Context, index int, landings *hostedLandings) (context.Context, context.CancelCauseFunc) {
	runCtx, cancel := context.WithCancelCause(ctx)
	runCtx = withLandingNotice(runCtx, func() { landings.begun(index) })
	runCtx, yield := withCheckYield(runCtx, cancel)
	landings.mu.Lock()
	defer landings.mu.Unlock()
	if landings.yields == nil {
		landings.yields = make(map[int]*checkYield)
	}
	landings.yields[index] = yield
	return runCtx, cancel
}

// yieldAt is the check yield of the run hosted at index, where it was hosted
// with one.
func (h *hostedLandings) yieldAt(index int) *checkYield {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.yields[index]
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

// checkYieldKey is the context key a hosted run's checkYield travels under.
type checkYieldKey struct{}

// checkYield is how a session past its drain bound stops a run that has a
// check running without starting that check over: rather than cancelling the
// run, it asks it to stop at its next boundary between checks, and the run's
// check stage, which asks at each such boundary, cancels itself there with the
// drain as the cause. From there it is stopped exactly as a run the session
// cancelled is, at its checks, and the checks it has passed keep their results
// (keptchecks.go), so nothing it ran is run again.
//
// It is asked from the session's goroutine and answered from the run's, so it
// is guarded.
type checkYield struct {
	mu     sync.Mutex
	cancel context.CancelCauseFunc
	asked  *RedeployDrain
}

// withCheckYield is a hosted run's context carrying the yield the session asks
// through, and the yield itself.
func withCheckYield(ctx context.Context, cancel context.CancelCauseFunc) (context.Context, *checkYield) {
	yield := &checkYield{cancel: cancel}
	return context.WithValue(ctx, checkYieldKey{}, yield), yield
}

// ask is the session asking the run to stop before its next check. Asking
// again changes nothing: the first ask's moment is the stop's.
func (y *checkYield) ask(drained RedeployDrain) {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.asked == nil {
		y.asked = &drained
	}
}

// yieldBetweenChecks is the run's check stage at a boundary between checks —
// before a check launches, and once the last has ended — stopping there if its
// hosting session asked it to. It reports whether it stopped.
func yieldBetweenChecks(ctx context.Context) bool {
	yield, wired := ctx.Value(checkYieldKey{}).(*checkYield)
	if !wired || yield == nil {
		return false
	}
	yield.mu.Lock()
	defer yield.mu.Unlock()
	if yield.asked == nil {
		return false
	}
	yield.cancel(*yield.asked)
	return true
}

// passPublicationsKey is the context key a session's passPublications travels
// under, on the context its recurring passes take their turns in.
type passPublicationsKey struct{}

// passPublications is the document landing runs a session's recurring passes
// have going. A pass can publish a document its role wrote (internal/chat,
// PublishDocuments), and that run is short and is never stopped for a
// restart: the session waits for each to end before it stops its passes, and
// once its drain bound has run out it lets no pass start another, which is
// left waiting for the session that comes back to start instead
// (holdDocumentForRestart).
//
// It is counted from the passes' goroutines and read from the session's, so it
// is guarded.
type passPublications struct {
	mu      sync.Mutex
	running int
	closed  bool
}

func withPassPublications(ctx context.Context, publications *passPublications) context.Context {
	return context.WithValue(ctx, passPublicationsKey{}, publications)
}

func passPublicationsOf(ctx context.Context) *passPublications {
	publications, _ := ctx.Value(passPublicationsKey{}).(*passPublications)
	return publications
}

// begin is a document landing run starting, or continuing a run already
// recorded, inside a pass. A new run is refused once the drain bound has run
// out; one already recorded is continued whatever, because stopping it is
// what this exists to prevent. Where begin agrees, end is owed.
func (p *passPublications) begin(recorded bool) bool {
	if p == nil {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed && !recorded {
		return false
	}
	p.running++
	return true
}

func (p *passPublications) end() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running--
}

// close is the drain bound having run out: no pass starts a new document
// landing run from here.
func (p *passPublications) close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
}

// active is how many document landing runs the passes have going.
func (p *passPublications) active() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
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
	if a.state.Phase == runstate.PhaseChecking {
		stopped.Checks = stoppedChecks(drained, a.outcome.Checks)
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
		return a.outcome, PauseNoteError{What: "the redeploy stop", Err: err}
	}
	return a.outcome, nil
}

// stoppedChecks is what a stop at a run's checks did to them: the check the
// session let finish, where it waited for one, and, from the stage's results,
// a check the stop cut short and each that said it could not run. Every other
// check of the stage either passed, and is kept, or had not started.
func stoppedChecks(drained RedeployDrain, results []checks.Result) *runstate.RedeployStopChecks {
	stopped := &runstate.RedeployStopChecks{WaitedFor: drained.WaitedFor}
	for _, result := range results {
		switch {
		case result.Passed:
		case result.CouldNotRun != "":
			stopped.CouldNotRun = append(stopped.CouldNotRun, oneline.Fold(result.Command, 512))
		case result.StoppedByCaller && !result.NotLaunched:
			stopped.CutShort = oneline.Fold(result.Command, 512)
		}
	}
	return stopped
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
	said := fmt.Sprintf("Yoyodyne paused this run for a redeploy: the watch session hosting it found a build deployed over it, drained for its bound of %s, and restarted with this run still at its %s phase. The run was stopped and preserved rather than waited out; nothing here says the change is wrong.",
		stopped.Bound(), stopped.Phase)
	if atChecks := stopped.Checks; atChecks != nil {
		said = fmt.Sprintf("Yoyodyne paused this run for a redeploy: the watch session hosting it found a build deployed over it, drained for its bound of %s, and stopped this run at its checks to restart. Nothing here says the change is wrong.",
			stopped.Bound())
		switch {
		case atChecks.CutShort != "":
			said += fmt.Sprintf(" %s was still running when the run was stopped, so it runs again from its start when the run is picked back up.", atChecks.CutShort)
		case atChecks.WaitedFor != "":
			said += fmt.Sprintf(" %s was running when the bound ran out, so the session let it finish rather than start it over, and the run stopped before its next check.", atChecks.WaitedFor)
		default:
			said += " No check was running when the run stopped, so none was cut short."
		}
	}
	lines := []string{
		said,
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

// queueCheckStagesKey is the context key a session's queueCheckStages travels under, on
// the context its merge queue passes work in.
type queueCheckStagesKey struct{}

// queueCheckStages is the merge queue check stages a session's queue passes have
// going. A candidate's checks are one stage whose results count only whole —
// a stage stopped part-way is set aside and every check in it runs again — so
// a restart onto a new build does not stop a queue pass in its checks: it
// waits for the stage to end, each check held to its configured limit, and
// stops the pass once it is past them. Once the drain bound has run out no
// queue starts a new check stage; the session that comes back runs it.
//
// It is written from the passes' goroutines and read from the session's, so it
// is guarded.
type queueCheckStages struct {
	mu     sync.Mutex
	closed bool
	stages map[runstate.MergeQueueKey]*queueCheckStage
}

// queueCheckStage is one queue's check stage in progress: which generation,
// the check it is on, when that check launched and under what limit, and when
// any of that last changed.
type queueCheckStage struct {
	generation uint64
	check      string
	launched   time.Time
	limit      time.Duration
	changed    time.Time
}

func withQueueCheckStages(ctx context.Context, checks *queueCheckStages) context.Context {
	return context.WithValue(ctx, queueCheckStagesKey{}, checks)
}

func queueCheckStagesOf(ctx context.Context) *queueCheckStages {
	checks, _ := ctx.Value(queueCheckStagesKey{}).(*queueCheckStages)
	return checks
}

// begin is a queue's check stage starting. It is refused once the drain bound
// has run out; where it agrees, end is owed.
func (q *queueCheckStages) begin(key runstate.MergeQueueKey, generation uint64, now time.Time) bool {
	if q == nil {
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	if q.stages == nil {
		q.stages = map[runstate.MergeQueueKey]*queueCheckStage{}
	}
	q.stages[key] = &queueCheckStage{generation: generation, changed: now}
	return true
}

// started is the stage beginning a check, and launched that check launching
// under its limit.
func (q *queueCheckStages) started(key runstate.MergeQueueKey, check string, now time.Time) {
	q.update(key, func(stage *queueCheckStage) {
		stage.check, stage.launched, stage.limit, stage.changed = check, time.Time{}, 0, now
	})
}

func (q *queueCheckStages) launched(key runstate.MergeQueueKey, limit time.Duration, now time.Time) {
	q.update(key, func(stage *queueCheckStage) {
		stage.launched, stage.limit, stage.changed = now, limit, now
	})
}

func (q *queueCheckStages) update(key runstate.MergeQueueKey, change func(*queueCheckStage)) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if stage, ok := q.stages[key]; ok {
		change(stage)
	}
}

func (q *queueCheckStages) end(key runstate.MergeQueueKey) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.stages, key)
}

// close is the drain bound having run out: no queue starts a check stage from
// here.
func (q *queueCheckStages) close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
}

// waiting is each queue whose check stage the restart is waiting for at a
// moment, with the latest time its current check can run to: its launch plus
// its limit, or, for a check not yet launched — between two checks, or waiting
// for a heavy check's place — checkYieldGrace from the stage's last change. A
// stage past that and the grace after it is not waited for.
func (q *queueCheckStages) waiting(now time.Time) map[runstate.MergeQueueKey]runstate.WatchCheckWait {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	waits := map[runstate.MergeQueueKey]runstate.WatchCheckWait{}
	for key, stage := range q.stages {
		until := stage.changed.Add(checkYieldGrace)
		if !stage.launched.IsZero() && stage.limit > 0 {
			until = stage.launched.Add(stage.limit)
		}
		if !now.Before(until.Add(checkYieldGrace)) {
			continue
		}
		check := nonEmpty(stage.check, "its first check")
		waits[key] = runstate.WatchCheckWait{Queue: key.TargetBranch, Generation: stage.generation, Check: oneline.Fold(check, 512), Until: until}
	}
	return waits
}
