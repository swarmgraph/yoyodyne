// Package maintain is the product supervisor's periodic pass: what the interim
// maintenance job did by hand every ten minutes, taken by the resident on the
// cadence services.maintenance.every sets, and written down each time.
//
// The job it absorbs ran as a launchd script outside the repository, and its
// history is the reason each rule here is what it is. An operator's script
// beside it rewrote four tracker statuses with a bare `bd update --status`
// nobody could see, so this pass writes no tracker status of its own: `yoyo
// reconcile` writes settlements, each recorded with a note, and the release
// step only appends notes to the release item it cut. The job force-restarted the
// watch 158 times while the provider's login was expired, so nothing here
// restarts anything while the provider cannot be reached or is not logged in.
// It bounced a live watch session for a redeploy and cancelled a run, so the
// scheduler is never stopped by this pass: a session takes a build up itself,
// between the runs it hosts, on its own drain rules. And it ran with no record
// of its own, so each pass here is a sweep record beside the recurring tasks',
// with every step it took and every step it skipped saying why.
//
// The steps, in the order they are recorded:
//
//   - provider: whether the provider is answering, read from the product's
//     outage record. While it is not, the supervisor holds every restart it
//     would choose to make, and the steps below say so where it held one.
//   - reconcile: `yoyo reconcile` from the supervisor's own binary, which
//     settles what interrupted runs left behind, converges the checkout and
//     the worktrees, and takes the stall reading. It is the one slow step, so
//     it runs beside the supervisor's looks at the children rather than
//     holding them up.
//   - cleanup: the work tracker's store cleaned up, once it has grown
//     services.maintenance.tracker_cleanup.grown_megabytes since the last
//     cleanup left it or that section's "every" has passed since then. bd
//     never reclaims the room its changes leave behind, and every tracker call
//     opens the whole store, so one nobody cleans up slows every call. It runs
//     after reconcile, on the same goroutine, so the pass's own tracker calls
//     are over before it starts; it does not start while any other process has
//     the store open; and it is stopped once it has held the store for the
//     section's max_hold, so no call waiting behind it runs past its own limit
//     (internal/beads, cleanup.go). Being busy or stopped is a skip, never a
//     failure: the next pass tries again. Each cleanup that ran is recorded
//     with when, the store's size and its count of work items either side, and
//     how long it took, and the latest is said on the pass's line in `yoyo
//     status`. A cleanup that fails, or after which the count changed, is a
//     failure of the pass, recorded, and the next cleanup waits until one is
//     due. One after which an item it held before is missing is a failure of
//     every pass after it too: no cleanup runs while any of those items is
//     missing, the pass looks for them again each time a cleanup is due, and
//     cleanups resume on their own once all of them are back.
//   - rebuild: what the supervisor's rebuilder last came to — it builds the
//     binary on its own thirty-second look when the branch lands something the
//     binary is made of, so a deploy has a build to take up.
//   - restart-requests: the restarts program managers asked for, each answered
//     on the request with what the supervisor did.
//   - redeploy: the deployed build taken up. The parts are moved onto it by
//     the supervisor's own looks; what this step adds is the supervisor
//     itself, which re-executes into the build once this record is written.
//   - slack: `yoyo slack ensure`, as the supervisor's own look at the sink
//     already takes it every few seconds; the step records where it stands.
//   - release: a release the Lead Product Manager decided, cut and its tag
//     pushed under approvals.releases: automatic (internal/releasecut). Its
//     gates take the better part of an hour, so the cut runs beside the pass
//     rather than inside it: a pass starts one where none is under way, and the
//     pass that finds it finished records what it came to. While one is under
//     way the supervisor takes up no deployed build, because re-executing would
//     leave the cut's gates running with nothing to record them.
package maintain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/releasecut"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// The step names, as the sweep record carries them.
const (
	StepProvider        = "provider"
	StepReconcile       = "reconcile"
	StepTrackerCleanup  = "cleanup"
	StepRebuild         = "rebuild"
	StepRestartRequests = "restart-requests"
	StepRedeploy        = "redeploy"
	StepSlack           = "slack"
	StepRelease         = releasecut.StepName
)

const (
	// ReconcileTimeout bounds `yoyo reconcile`. It asks the forge about every
	// unsettled publication and waits a dropped connection out on its own
	// backoff, so it is given long enough to do that and no longer.
	ReconcileTimeout = 15 * time.Minute
	// maxOutputBytes bounds how much of a command's output one step's detail
	// carries. The whole of it is in the supervisor's log.
	maxOutputBytes = 1 << 10
	// StartedByMaintenance is what a `yoyo` this pass starts is marked as
	// started by.
	StartedByMaintenance = "the supervisor's maintenance pass"
)

// Claims is where the pass records its cadence and its report: the sweep
// store every recurring task records in. It is satisfied by
// *runstate.SweepStore.
type Claims interface {
	Claim(ctx context.Context, task string, every time.Duration, now time.Time) (runstate.SweepClaim, error)
	Settle(ctx context.Context, task, problem string) (runstate.SweepClaim, error)
	Find(task string) (runstate.SweepClaim, bool, error)
	Append(recorded runstate.Sweep) error
}

// Outages is the product's record of the provider answering nobody. It is
// satisfied by *runstate.ProviderOutageStore.
type Outages interface {
	Standing() (runstate.ProviderOutage, bool, error)
}

// Requests is the log of program managers' restart requests. It is satisfied
// by *runstate.RestartRequestStore.
type Requests interface {
	Open() ([]runstate.RestartRequest, error)
	Answer(id, answer string, at time.Time) (runstate.RestartRequest, error)
}

// Rebuild is the supervisor's rebuilder as the pass reads it. It is satisfied
// by *supervise.Rebuilder.
type Rebuild interface {
	Standing() (string, bool)
}

// Host is the supervisor as the pass sees it. It is satisfied by
// *supervise.Supervisor, and every call on it is made on the supervisor's own
// goroutine.
type Host interface {
	ChildState(name config.ServiceName) (runstate.SupervisedChild, bool)
	Hosted(name config.ServiceName) string
	RunningBuild() string
	Deployed() string
	Moving() string
	TakeUp(into string) error
	RestartOnRequest(ctx context.Context, name config.ServiceName, now time.Time) string
}

// TrackerStore is the work tracker's store as the cleanup step sees it. It is
// satisfied by beads.Client.
type TrackerStore interface {
	StoreSize() (string, int64, error)
	CleanUpStore(ctx context.Context, hold time.Duration) (beads.StoreCleanup, error)
	MissingItems(ctx context.Context, ids []string) ([]string, error)
}

// Cleanups is the record of each cleanup of the tracker's store that ran. It
// is satisfied by *runstate.TrackerStoreCleanupStore.
type Cleanups interface {
	Read() (runstate.TrackerStoreCleanups, error)
	Record(runstate.TrackerStoreCleanup) error
}

// Releaser is the release step: a decided release cut and its tag pushed. It
// is satisfied by *releasecut.Cutter.
type Releaser interface {
	Pass(ctx context.Context) runstate.SweepStep
}

// Pass is the supervisor's periodic pass over one product.
type Pass struct {
	Product domain.ProductID
	// Every is the cadence, from the services section.
	Every time.Duration
	// Program is the binary the supervisor is running, which is what
	// `reconcile` is run from, Config the configuration file it reads, and
	// Repository where it runs.
	Program    string
	Config     string
	Repository string
	// Environ is what reconcile is given, with the Slack variables already
	// taken out by whoever started the supervisor.
	Environ []string

	Claims   Claims
	Outages  Outages
	Requests Requests
	// Rebuild is the supervisor's rebuilder, and nil where the binary is not
	// built from this checkout; NoRebuild then says why.
	Rebuild   Rebuild
	NoRebuild string
	Host      Host
	Runner    execution.ProcessRunner
	// Release cuts a decided release, and is nil where nothing is wired to;
	// the step then says so.
	Release Releaser
	// Tracker is the primary checkout's tracker store and Cleanups the record
	// of its cleanups; where either is nil the cleanup step says so. Cleanup is
	// when one is due and how long it may hold the store, and its zero value is
	// the configuration's default.
	Tracker        TrackerStore
	Cleanups       Cleanups
	TrackerCleanup config.TrackerCleanup

	Now func() time.Time
	Log func(format string, args ...any)
	// RecordFailures files or clears the shared finding after recording a pass.
	RecordFailures func(context.Context) error

	// nextDue is when the cadence next fires, kept so a supervisor looking every
	// few seconds does not open the claim each time.
	nextDue time.Time
	// running is the pass under way: when it started, and where its reconcile
	// reports once it is done.
	running *underway
	last    *runstate.Sweep
	// releasing is the release cut under way beside the passes, and released
	// what one came to once a Wait collected it before a pass did.
	releasing *cutting
	released  *runstate.SweepStep
	// cleanedUp is the latest cleanup of the tracker's store, for the pass's
	// line; cleanupsRead says the record has been read for it once.
	cleanedUp    *runstate.TrackerStoreCleanup
	cleanupsRead bool
	// lostLookedAt is when the pass last looked for the items a cleanup found
	// missing, so it looks once a cleanup interval rather than at every pass.
	lostLookedAt time.Time
}

type cutting struct {
	started time.Time
	done    chan runstate.SweepStep
}

type underway struct {
	started time.Time
	firing  int
	done    chan offGoroutine
}

// offGoroutine is what the pass does off the supervisor's goroutine: reconcile,
// and then the tracker's cleanup, with the record of the cleanup where one ran.
type offGoroutine struct {
	reconciled runstate.SweepStep
	cleaned    runstate.SweepStep
	cleanup    *runstate.TrackerStoreCleanup
}

// Name is how the pass is named in what the supervisor says, and Service the
// part of the product it is recorded as.
func (p *Pass) Name() string { return config.MaintenanceTaskName }

func (p *Pass) Service() config.ServiceName { return config.ServiceMaintenance }

// Describe is the pass's line in the supervisor's record: its cadence, and
// what the last pass came to.
func (p *Pass) Describe() string {
	said := fmt.Sprintf("every %s", p.every())
	if p.last != nil {
		said += fmt.Sprintf("; last pass at %s (%s)", p.last.StartedAt.UTC().Format(time.RFC3339), summarize(p.last.Steps))
	}
	if p.running != nil {
		said += fmt.Sprintf("; a pass has been under way since %s", p.running.started.UTC().Format(time.RFC3339))
	} else if !p.nextDue.IsZero() {
		said += fmt.Sprintf("; next at %s", p.nextDue.UTC().Format(time.RFC3339))
	}
	if cleanup := p.latestCleanup(); cleanup != nil {
		said += "; the tracker's store was last cleaned up " + cleanup.Says()
	}
	return said
}

// latestCleanup is the latest cleanup of the tracker's store, read from its
// record the first time it is asked for and kept from each pass after that.
func (p *Pass) latestCleanup() *runstate.TrackerStoreCleanup {
	if !p.cleanupsRead && p.Cleanups != nil {
		p.cleanupsRead = true
		if cleanups, err := p.Cleanups.Read(); err == nil {
			if latest, found := cleanups.Latest(); found {
				p.cleanedUp = &latest
			}
		}
	}
	return p.cleanedUp
}

// Look is the pass as a resident of the supervisor. On every tick it finishes
// a pass whose reconcile has come back, and begins one when the cadence has
// come round. It never returns an error: a pass that failed is a fact in the
// record, and the next pass looks at everything this one would have.
func (p *Pass) Look(ctx context.Context, now time.Time) {
	if p.running != nil {
		select {
		case done := <-p.running.done:
			p.finish(ctx, done)
		default:
		}
		return
	}
	if !p.due(now) {
		return
	}
	claimed, err := p.Claims.Claim(ctx, config.MaintenanceTaskName, p.every(), now)
	if err != nil {
		var notDue runstate.SweepNotDueError
		if errors.As(err, &notDue) {
			p.nextDue = notDue.NextDue
			return
		}
		// Tried again at the next cadence rather than every tick, for the reason
		// a failed firing moves a recurring task's clock.
		p.nextDue = now.Add(p.every())
		p.log("the maintenance pass could not be claimed, so it was not taken: %v", err)
		return
	}
	p.nextDue = claimed.NextDue(p.every())
	p.running = &underway{started: now, firing: claimed.Firings, done: make(chan offGoroutine, 1)}
	p.log("maintenance pass %d starting", claimed.Firings)
	done := p.running.done
	go func() {
		work := offGoroutine{reconciled: p.reconcile(ctx)}
		work.cleaned, work.cleanup = p.cleanUpTracker(ctx)
		done <- work
	}()
	if p.Release != nil && p.releasing == nil && p.released == nil {
		// Beside the pass rather than inside it: see the package comment. It
		// touches nothing of the supervisor's.
		p.releasing = &cutting{started: now, done: make(chan runstate.SweepStep, 1)}
		done := p.releasing.done
		go func() { done <- p.Release.Pass(ctx) }()
	}
}

// Wait finishes the pass under way, blocking until its reconcile is back. It
// is for a test, and for nothing that holds the supervisor's looks.
func (p *Pass) Wait(ctx context.Context) {
	if p.releasing != nil {
		select {
		case released := <-p.releasing.done:
			p.releasing, p.released = nil, &released
		case <-ctx.Done():
			return
		}
	}
	if p.running == nil {
		return
	}
	select {
	case done := <-p.running.done:
		p.finish(ctx, done)
	case <-ctx.Done():
	}
}

// Last is the most recent pass this process recorded.
func (p *Pass) Last() (runstate.Sweep, bool) {
	if p.last == nil {
		return runstate.Sweep{}, false
	}
	return *p.last, true
}

// due reports whether the cadence has come round. It reads the durable claim
// once and then keeps the answer.
func (p *Pass) due(now time.Time) bool {
	if p.nextDue.IsZero() {
		claimed, found, err := p.Claims.Find(config.MaintenanceTaskName)
		if err != nil || !found {
			// A claim that cannot be read is answered by claiming, which refuses
			// on its own reading; a claim nobody made is a pass that is due.
			return true
		}
		p.nextDue = claimed.NextDue(p.every())
	}
	return !now.Before(p.nextDue)
}

// finish takes the steps that read and change the supervisor's account of its
// children, on its own goroutine, and records the pass.
func (p *Pass) finish(ctx context.Context, done offGoroutine) {
	started := p.running.started
	p.running = nil
	recorded := runstate.Sweep{
		SchemaVersion: runstate.SweepSchemaVersion,
		ProductID:     p.Product,
		Task:          config.MaintenanceTaskName,
		StartedAt:     started.UTC(),
	}
	recorded.Steps = append(recorded.Steps, p.provider())
	recorded.Steps = append(recorded.Steps, done.reconciled)
	recorded.Steps = append(recorded.Steps, done.cleaned)
	if done.cleanup != nil {
		p.cleanedUp, p.cleanupsRead = done.cleanup, true
	}
	recorded.Steps = append(recorded.Steps, p.rebuild())
	recorded.Steps = append(recorded.Steps, p.restartRequests(ctx))
	// Collected before the redeploy is decided, so a cut that has just finished
	// does not hold a take-up back for a pass.
	release := p.release()
	redeploy, takeUp := p.redeploy()
	recorded.Steps = append(recorded.Steps, redeploy)
	recorded.Steps = append(recorded.Steps, p.slack())
	recorded.Steps = append(recorded.Steps, release)
	for index := range recorded.Steps {
		recorded.Steps[index].Detail = bounded(recorded.Steps[index].Detail)
		step := recorded.Steps[index]
		p.log("maintenance: %s: %s, %s", step.Name, step.Outcome, step.Detail)
	}

	recorded.EndedAt = p.now().UTC()
	recorded.Result = &sweep.Result{Status: sweep.StatusComplete, Summary: summarize(recorded.Steps)}
	recorded.Problem = bounded(failures(recorded.Steps))
	if err := p.Claims.Append(recorded); err != nil {
		p.log("the maintenance pass could not be recorded, so `yoyo sweeps` will not show it: %v", err)
	}
	if _, err := p.Claims.Settle(ctx, config.MaintenanceTaskName, recorded.Problem); err != nil {
		p.log("the maintenance pass's claim could not be settled: %v", err)
	}
	if p.RecordFailures != nil {
		if err := p.RecordFailures(ctx); err != nil {
			p.log("the product pass failure finding could not be recorded: %v", err)
		}
	}
	p.last = &recorded
	// Last, once the record that says so is written: the supervisor lets its
	// lease go at the end of this tick and re-executes into the build.
	if takeUp != "" {
		if err := p.Host.TakeUp(takeUp); err != nil {
			p.log("the deployed build %s was not taken up: %v", takeUp, err)
		}
	}
}

// release is what the release cut beside the passes came to, where it has
// finished, and that it is under way where it has not.
func (p *Pass) release() runstate.SweepStep {
	step := runstate.SweepStep{Name: StepRelease}
	if p.Release == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no release cut is wired, so a decided release is cut by a person"
		return step
	}
	if p.released != nil {
		step, p.released = *p.released, nil
		return step
	}
	if p.releasing == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no release cut is under way; the next pass starts one"
		return step
	}
	select {
	case released := <-p.releasing.done:
		p.releasing = nil
		return released
	default:
	}
	step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("a release cut has been under way since %s; the pass that finds it finished records what it came to", p.releasing.started.UTC().Format(time.RFC3339))
	return step
}

// provider reads whether the provider is answering. The hold itself is the
// supervisor's, read from the same record wherever it would restart anything;
// this step is what puts the reading on the pass.
func (p *Pass) provider() runstate.SweepStep {
	step := runstate.SweepStep{Name: StepProvider}
	if p.Outages == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no outage record is wired, so whether the provider is answering is not read"
		return step
	}
	outage, standing, err := p.Outages.Standing()
	switch {
	case err != nil:
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("whether the provider is answering could not be read: %v", err)
	case standing:
		step.Outcome, step.Detail = runstate.StepRan, outage.Says()+"; nothing is restarted while it stands"
	default:
		step.Outcome, step.Detail = runstate.StepRan, "answering"
	}
	return step
}

// reconcile runs `yoyo reconcile` from the running binary. It is a subprocess
// rather than a call because the verb is the whole of the settlement — runs,
// publications, convergence, the stall reading — and a second copy of its
// wiring here would be a second reconcile to keep in step with the first. It
// runs off the supervisor's goroutine and touches nothing of the supervisor's.
//
// The process is marked as this pass's, so the verb does not record it as the
// operator settling runs by hand (execution.StartedByVariable).
func (p *Pass) reconcile(ctx context.Context) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepReconcile}
	if p.Runner == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no process runner is wired, so nothing can be run"
		return step
	}
	result, err := p.Runner.Run(ctx, execution.Command{
		Name:    p.Program,
		Args:    []string{"reconcile", "--config", p.Config},
		Dir:     p.Repository,
		Env:     execution.WithStartedBy(p.Environ, StartedByMaintenance),
		Timeout: ReconcileTimeout,
	}, nil)
	switch {
	case err != nil:
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("yoyo reconcile could not be run: %v", err)
	case result.Status != execution.ProcessSucceeded:
		step.Outcome, step.Detail = runstate.StepFailed, "yoyo reconcile "+describeResult(result)
	default:
		step.Outcome, step.Detail = runstate.StepRan, "yoyo reconcile "+describeResult(result)
	}
	return step
}

// cleanUpTracker cleans up the tracker's store where a cleanup is due, and
// records it where one ran. It runs off the supervisor's goroutine, after
// reconcile, and touches nothing of the supervisor's.
func (p *Pass) cleanUpTracker(ctx context.Context) (runstate.SweepStep, *runstate.TrackerStoreCleanup) {
	step := runstate.SweepStep{Name: StepTrackerCleanup}
	if p.Tracker == nil || p.Cleanups == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no tracker store is wired, so nothing is cleaned up"
		return step, nil
	}
	bounds := p.cleanupBounds()
	earlier, err := p.Cleanups.Read()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the record of earlier cleanups of the tracker's store could not be read, so whether one is due cannot be told: %v", err)
		return step, nil
	}
	_, size, err := p.Tracker.StoreSize()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the tracker's store could not be measured, so whether a cleanup is due cannot be told: %v", err)
		return step, nil
	}
	reason, notDue := cleanupDue(earlier, size, bounds, p.now())
	if latest, found := earlier.Latest(); found && latest.Lost() {
		back, held := p.lookForMissing(ctx, latest, reason != "", bounds)
		if held != "" {
			step.Outcome, step.Detail = runstate.StepFailed, held
			return step, nil
		}
		reason += "; " + back
	}
	if reason == "" {
		step.Outcome, step.Detail = runstate.StepSkipped, notDue
		return step, nil
	}
	cleaned, err := p.Tracker.CleanUpStore(ctx, bounds.MaxHold.Duration())
	if err != nil && (!cleaned.Ran() || ctx.Err() != nil) {
		// Nothing was started, or the supervisor is stopping: there is no
		// cleanup to record, and the next pass is due one again.
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the tracker's store (%s) was due a cleanup because %s, and it could not be cleaned up: %v", runstate.SayBytes(size), reason, err)
		return step, nil
	}
	if !cleaned.Ran() {
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("the tracker's store (%s) is due a cleanup because %s, and it was not started because another process had the store open, which a cleanup would have kept waiting: %s; the next pass tries again",
			runstate.SayBytes(size), reason, beads.SayHolders(cleaned.Busy))
		return step, nil
	}
	record := runstate.TrackerStoreCleanup{
		Store:        cleaned.Store,
		StartedAt:    cleaned.StartedAt,
		Took:         cleaned.Took,
		SizeBefore:   cleaned.SizeBefore,
		SizeAfter:    cleaned.SizeAfter,
		Stopped:      cleaned.Stopped,
		Said:         cleaned.Said,
		Reason:       reason,
		ItemsBefore:  cleaned.ItemsBefore,
		ItemsAfter:   cleaned.ItemsAfter,
		ItemsCounted: cleaned.Counted,
		Missing:      cleaned.Missing,
		Added:        cleaned.Added,
	}
	if err != nil {
		record.Failed = err.Error()
	}
	recordFailure := ""
	if err := p.Cleanups.Record(record); err != nil {
		recordFailure = fmt.Sprintf("; it could not be recorded, so the next pass measures from the cleanup before it: %v", err)
	}
	switch {
	case record.Failed != "":
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the tracker's store was due a cleanup because %s, and the cleanup failed after %s: %s; it is not tried again until the next cleanup is due, at %s or once the store has grown %d MB%s",
			reason, roundTook(cleaned.Took), record.Failed, local(cleaned.StartedAt.Add(bounds.Every.Duration())), bounds.GrownMegabytes, recordFailure)
	case record.Lost():
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the tracker's store was cleaned up because %s, from %s to %s in %s, and it held %s, so the cleanup may have lost work items; no cleanup runs while any of them is missing: the pass looks for them again each time a cleanup is due, and cleanups resume on their own once all of them are back in the store%s",
			reason, runstate.SayBytes(cleaned.SizeBefore), runstate.SayBytes(cleaned.SizeAfter), roundTook(cleaned.Took), record.SaysItems(), recordFailure)
	case record.ItemsChanged():
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the tracker's store was cleaned up because %s, from %s to %s in %s, and its count of work items changed: %s; no item it held before is missing, so another process created the added items while it ran, and the next cleanup runs when it is due%s",
			reason, runstate.SayBytes(cleaned.SizeBefore), runstate.SayBytes(cleaned.SizeAfter), roundTook(cleaned.Took), record.SaysItems(), recordFailure)
	case cleaned.Stopped:
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("the tracker's store was due a cleanup because %s, and the cleanup was stopped once it had held the store for %s, so that no other tracker call waits on it past its own limit: from %s to %s in %s, %s; the next pass finishes it%s",
			reason, bounds.MaxHold, runstate.SayBytes(cleaned.SizeBefore), runstate.SayBytes(cleaned.SizeAfter), roundTook(cleaned.Took), record.SaysItems(), recordFailure)
	default:
		step.Outcome, step.Detail = runstate.StepRan, fmt.Sprintf("cleaned up the tracker's store, due because %s: from %s to %s in %s, %s%s",
			reason, runstate.SayBytes(cleaned.SizeBefore), runstate.SayBytes(cleaned.SizeAfter), roundTook(cleaned.Took), record.SaysItems(), recordFailure)
		if recordFailure != "" {
			step.Outcome = runstate.StepFailed
		}
	}
	return step, &record
}

// lookForMissing looks for the items a cleanup found missing, where a cleanup
// is due and the pass has not looked within the cleanup interval, and says
// either that they are all back, which lets the cleanup run, or why the store
// is still held from cleanups. A cleanup after which items are missing may have
// lost them, so no other cleanup runs over the store while any is; the hold
// ends on its own once every one of them is found again, so no person has to
// release it.
func (p *Pass) lookForMissing(ctx context.Context, lost runstate.TrackerStoreCleanup, due bool, bounds config.TrackerCleanup) (back, held string) {
	said := fmt.Sprintf("the tracker's store is not cleaned up: its last cleanup, at %s, held %s, so it may have lost them, and no cleanup runs while any of them is missing", local(lost.StartedAt), lost.SaysItems())
	ending := "cleanups resume on their own once every one of them is back in the store"
	now := p.now()
	if !due || (!p.lostLookedAt.IsZero() && now.Sub(p.lostLookedAt) < bounds.Every.Duration()) {
		next := lost.StartedAt.Add(bounds.Every.Duration())
		if !p.lostLookedAt.IsZero() {
			next = p.lostLookedAt.Add(bounds.Every.Duration())
		}
		return "", fmt.Sprintf("%s; the pass looks for them again when the next cleanup is due, at %s, and %s", said, local(next), ending)
	}
	p.lostLookedAt = now
	still, err := p.Tracker.MissingItems(ctx, lost.Missing)
	if err != nil {
		return "", fmt.Sprintf("%s; the pass looked for them and could not read the store: %v; it looks again at %s, and %s", said, err, local(now.Add(bounds.Every.Duration())), ending)
	}
	if len(still) > 0 {
		return "", fmt.Sprintf("%s; the pass looked for them at %s and %s still missing; it looks again at %s, and %s", said, local(now), runstate.SayIdentifiers(still), local(now.Add(bounds.Every.Duration())), ending)
	}
	// Looked for again at the next pass where this one does not get to run the
	// cleanup, since nothing records that they were found.
	p.lostLookedAt = time.Time{}
	return fmt.Sprintf("the items its last cleanup, at %s, found missing (%s) are all in the store again", local(lost.StartedAt), runstate.SayIdentifiers(lost.Missing)), ""
}

// cleanupDue says why a cleanup of a store of size bytes is due, or, where
// none is, why not and when one will be.
func cleanupDue(earlier runstate.TrackerStoreCleanups, size int64, bounds config.TrackerCleanup, now time.Time) (reason, notDue string) {
	last, found := earlier.LastFinished()
	if !found {
		return "the harness has never finished cleaning it up", ""
	}
	every := bounds.Every.Duration()
	grownBound := int64(bounds.GrownMegabytes) << 20
	grown := size - last.SizeAfter
	if since := now.Sub(last.StartedAt); since >= every {
		return fmt.Sprintf("its last cleanup, at %s, was more than %s ago", local(last.StartedAt), bounds.Every), ""
	}
	if grown >= grownBound {
		return fmt.Sprintf("it has grown %s since its last cleanup left it at %s, past the %d MB bound", runstate.SayBytes(grown), runstate.SayBytes(last.SizeAfter), bounds.GrownMegabytes), ""
	}
	said := fmt.Sprintf("the tracker's store is %s", runstate.SayBytes(size))
	if grown > 0 {
		said += fmt.Sprintf(", %s more than its last cleanup at %s left it", runstate.SayBytes(grown), local(last.StartedAt))
	} else {
		said += fmt.Sprintf(", no more than its last cleanup at %s left it", local(last.StartedAt))
	}
	if last.Failed != "" {
		said += fmt.Sprintf("; that cleanup failed: %s", last.Failed)
	}
	return "", fmt.Sprintf("%s; the next cleanup is due at %s, or sooner once it has grown %d MB", said, local(last.StartedAt.Add(every)), bounds.GrownMegabytes)
}

// cleanupBounds is the configured cleanup, with the default standing for each
// bound left at zero.
func (p *Pass) cleanupBounds() config.TrackerCleanup {
	bounds, defaults := p.TrackerCleanup, config.DefaultTrackerCleanup()
	if bounds.Every <= 0 {
		bounds.Every = defaults.Every
	}
	if bounds.GrownMegabytes <= 0 {
		bounds.GrownMegabytes = defaults.GrownMegabytes
	}
	if bounds.MaxHold <= 0 {
		bounds.MaxHold = defaults.MaxHold
	}
	return bounds
}

func local(moment time.Time) string {
	return moment.Local().Format("2006-01-02 15:04 MST")
}

func roundTook(took time.Duration) string {
	if took < time.Minute {
		return fmt.Sprintf("%.1f seconds", took.Seconds())
	}
	return took.Round(time.Second).String()
}

// rebuild is what the supervisor's rebuilder last came to.
func (p *Pass) rebuild() runstate.SweepStep {
	step := runstate.SweepStep{Name: StepRebuild}
	if p.Rebuild == nil {
		reason := p.NoRebuild
		if reason == "" {
			reason = "the supervisor has no rebuilder"
		}
		step.Outcome, step.Detail = runstate.StepSkipped, reason
		return step
	}
	standing, failed := p.Rebuild.Standing()
	switch {
	case standing == "":
		step.Outcome, step.Detail = runstate.StepSkipped, "the rebuilder has not looked at the checkout yet"
	case failed:
		step.Outcome, step.Detail = runstate.StepFailed, standing
	default:
		step.Outcome, step.Detail = runstate.StepRan, standing
	}
	return step
}

// restartRequests answers every open request a program manager made, each
// with what the supervisor did about it.
func (p *Pass) restartRequests(ctx context.Context) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepRestartRequests}
	if p.Requests == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no restart request log is wired"
		return step
	}
	open, err := p.Requests.Open()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the restart requests could not be read: %v", err)
		return step
	}
	if len(open) == 0 {
		step.Outcome, step.Detail = runstate.StepSkipped, "no program manager has an open restart request"
		return step
	}
	var said []string
	var failed bool
	for _, request := range open {
		answer := p.Host.RestartOnRequest(ctx, request.Part, p.now())
		if _, err := p.Requests.Answer(request.ID, answer, p.now()); err != nil {
			failed = true
			said = append(said, fmt.Sprintf("%s (%s asked for the %s): %s, and the answer could not be recorded: %v", request.ID, request.Agent, request.Part, answer, err))
			continue
		}
		said = append(said, fmt.Sprintf("%s (%s asked for the %s): %s", request.ID, request.Agent, request.Part, answer))
	}
	step.Outcome, step.Detail = runstate.StepRan, strings.Join(said, "; ")
	if failed {
		step.Outcome = runstate.StepFailed
	}
	return step
}

// redeploy is the deployed build taken up. The parts are moved onto it by the
// supervisor's own looks, one at a time and never in the middle of what one is
// doing; the scheduler takes it up itself between the runs it hosts; and the
// supervisor, which nothing else moves, re-executes into it once this pass is
// recorded. It returns the build to take up, and nothing where there is none.
func (p *Pass) redeploy() (runstate.SweepStep, string) {
	step := runstate.SweepStep{Name: StepRedeploy}
	deployed, running := p.Host.Deployed(), p.Host.RunningBuild()
	var parts []string
	for _, name := range config.ServiceNames {
		state, known := p.Host.ChildState(name)
		if known && state.Redeploy != "" {
			parts = append(parts, fmt.Sprintf("the %s is %s", name, state.Redeploy))
		}
	}
	switch {
	case deployed == "":
		step.Outcome, step.Detail = runstate.StepSkipped, "which build the binary on disk is could not be read, so nothing is taken up"
		return step, ""
	case running == "":
		step.Outcome, step.Detail = runstate.StepSkipped, "the supervisor's own build carries no revision, so whether it is behind the binary on disk cannot be told"
		return step, ""
	case running == deployed && len(parts) == 0:
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("every part is on the deployed build %s; nothing to take up", short(deployed))
		return step, ""
	case running == deployed:
		step.Outcome, step.Detail = runstate.StepRan, strings.Join(parts, "; ")
		return step, ""
	}
	behind := fmt.Sprintf("the supervisor is on build %s, behind the deployed %s", short(running), short(deployed))
	if p.releasing != nil {
		parts = append([]string{fmt.Sprintf("%s, and it is taken up at a later pass, once the release cut under way since %s has finished", behind, p.releasing.started.UTC().Format(time.RFC3339))}, parts...)
		step.Outcome, step.Detail = runstate.StepSkipped, strings.Join(parts, "; ")
		return step, ""
	}
	if held := p.hold(); held != "" {
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("%s, and nothing is restarted while %s", behind, held)
		return step, ""
	}
	if moving := p.Host.Moving(); moving != "" {
		parts = append([]string{fmt.Sprintf("%s, and it is taken up at a later pass, once the %s has settled", behind, moving)}, parts...)
		step.Outcome, step.Detail = runstate.StepSkipped, strings.Join(parts, "; ")
		return step, ""
	}
	parts = append([]string{behind + "; it re-executes into the deployed build once this pass is recorded, leaving every part running to be reattached"}, parts...)
	step.Outcome, step.Detail = runstate.StepRan, strings.Join(parts, "; ")
	return step, deployed
}

// slack is where the sink stands: `yoyo slack ensure`, which the supervisor's
// own look at the sink already takes every few seconds — lease-checked, from
// this product's own stored tokens, nothing done while one is running.
func (p *Pass) slack() runstate.SweepStep {
	step := runstate.SweepStep{Name: StepSlack}
	sink, known := p.Host.ChildState(config.ServiceSlack)
	if !known {
		step.Outcome, step.Detail = runstate.StepSkipped, "the sink is "+p.Host.Hosted(config.ServiceSlack)
		return step
	}
	switch sink.State {
	case runstate.ChildRunning:
		detail := "running"
		if sink.PID > 0 {
			detail = fmt.Sprintf("running as pid %d", sink.PID)
		}
		step.Outcome, step.Detail = runstate.StepRan, detail+"; the supervisor's own look keeps it up, which is `yoyo slack ensure` taken every few seconds"
	case runstate.ChildDown:
		step.Outcome, step.Detail = runstate.StepRan, "down, and the supervisor is starting it again: "+sink.Reason
	case runstate.ChildDegraded:
		step.Outcome, step.Detail = runstate.StepFailed, "degraded, and the supervisor has stopped restarting it: "+sink.Reason
	default:
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("%s: %s", sink.State, sink.Reason)
	}
	return step
}

// hold is the provider guard as the redeploy step says it; the supervisor's
// TakeUp refuses on the same reading.
func (p *Pass) hold() string {
	if p.Outages == nil {
		return ""
	}
	outage, standing, err := p.Outages.Standing()
	if err != nil {
		return fmt.Sprintf("whether the provider is answering could not be read (%v)", err)
	}
	if standing {
		return outage.Says()
	}
	return ""
}

func (p *Pass) every() time.Duration {
	if p.Every > 0 {
		return p.Every
	}
	return config.DefaultMaintenanceInterval
}

func (p *Pass) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Pass) log(format string, args ...any) {
	if p.Log != nil {
		p.Log(format, args...)
	}
}

// summarize is the pass in a sentence: how many steps ran, were skipped, and
// failed.
func summarize(steps []runstate.SweepStep) string {
	ran, skipped, failed := 0, 0, 0
	for _, step := range steps {
		switch step.Outcome {
		case runstate.StepRan:
			ran++
		case runstate.StepSkipped:
			skipped++
		case runstate.StepFailed:
			failed++
		}
	}
	return fmt.Sprintf("%d step(s) ran, %d skipped, %d failed", ran, skipped, failed)
}

// failures is what went wrong, for the record's problem and the claim's, and
// empty on a pass in which nothing did.
func failures(steps []runstate.SweepStep) string {
	var said []string
	for _, step := range steps {
		if step.Outcome == runstate.StepFailed {
			said = append(said, step.Name+": "+step.Detail)
		}
	}
	return strings.Join(said, "; ")
}

// describeResult is a command's ending in a line: its status and the tail of
// what it said, bounded.
func describeResult(result execution.ProcessResult) string {
	output := strings.TrimSpace(result.Stdout)
	said := fmt.Sprintf("exited %d", result.ExitCode)
	if result.Status != execution.ProcessSucceeded {
		if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
			output = stderr
		}
		said = fmt.Sprintf("ended %s, exit %d", result.Status, result.ExitCode)
	}
	if output == "" {
		return said
	}
	return said + ": " + tail(output, maxOutputBytes)
}

func short(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}

// tail folds output onto one line and keeps its end, which is where a command
// says how it came out, never cutting mid-character.
func tail(text string, limit int) string {
	folded := strings.Join(strings.Fields(text), " ")
	if len(folded) <= limit {
		return folded
	}
	cut := folded[len(folded)-limit:]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[1:]
	}
	return "…" + strings.TrimSpace(cut)
}

// bounded keeps a detail inside what the record accepts.
func bounded(text string) string {
	if len(text) <= runstate.MaxSweepTextBytes {
		return text
	}
	return oneline.Fold(text, runstate.MaxSweepTextBytes-len(oneline.Marker))
}
