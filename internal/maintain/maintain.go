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
//     are over before it starts. A store past the tracker_limits size bound is
//     also cleaned up every overBoundEvery. A due cleanup is put off while any
//     other process has the store open, and each postponement is recorded; once it
//     has waited busyPatience it starts beside them, since with every
//     developer slot busy the store may never be free, and bd's lock keeps
//     their calls whole (internal/beads, cleanup.go). It is stopped once it
//     has held the store for the section's max_hold, so no call waiting
//     behind it runs past its own limit. Being put off or stopped is a skip,
//     never a failure: the next pass tries again. Nothing before the cleanup or while
//     it holds the store lists or counts work items, which an oversized store
//     cannot answer in time; whether one is due is read from the store's size
//     on disk. Each cleanup that ran is recorded with when, the store's size
//     either side, its count of work items after it, or why that count could
//     not be taken, and how long it held the store; the latest is said on the
//     pass's line in `yoyo status`, with the store's size at the last pass. A cleanup that fails is a failure of the pass,
//     recorded, and the next cleanup waits until one is due; a count that
//     could not be taken is not. A cleanup recorded by an earlier build, which
//     counted either side, after which an item it held before was missing
//     still holds the cleanups back: the pass looks for those items again,
//     one by one, each time a cleanup is due, and cleanups resume on their
//     own once all of them are back.
//   - measure: the work tracker's store copied and measured on the
//     copy (internal/beads, storemeasure.go): its size divided between current
//     data, history, and leftover data a cleanup takes back, how long a listing
//     of open items, a listing of ready items, and a read of one item take as
//     it is, after the cleanup, and after squashing old history and then all of
//     it, and whether a full export is the same after each. On a second copy it
//     starts the tracker's server mode and records where the server listens,
//     whether a connection made under the developer sandbox's network rules
//     reaches it, and which writes grew the store over the last week; and it
//     counts the harness's own tracker calls over the hour before by caller,
//     from the log every process keeps. The live store is
//     only read, and only to copy it, while no other process has it open. It
//     runs after the cleanup, on the same goroutine, where none has been
//     recorded, a week after the last that finished, or a day after the last
//     that failed; it is recorded under the product's state, and its figures
//     are said in full on the step. It holds the pass up while it runs, for as
//     much as measureBound, which is why it is not taken more often. Each
//     records whether the listings as the store was are within the design's
//     ten second limit and for how many measurements in a row, the second in
//     a row being when the tracker read wait may be revisited.
//   - squash: the live tracker store's history squashed, under the three
//     conditions of the design on keeping the store small
//     (docs/designs/tracker-store-size.md), decided once on each measurement
//     (trackersquash.go). It runs after the measurement, on the same
//     goroutine, so the pass's own tracker calls are over before it starts.
//     It holds the store for as long as bd takes, and every other tracker call
//     waits behind it meanwhile, which is why it runs only where the
//     measurement shows the listings past their limit or the store doubled
//     since its last squash, why one that leaves the listings past the limit
//     warns the development manager once with the figures, why the step says
//     how long the store was held, and why, once one
//     is due, it holds new runs and starts only once no developer run is in
//     flight, giving up if they have not finished within the watch session's
//     redeploy drain bound. That wait holds the pass up for as long as the
//     bound.
//   - limits: the live store's size on disk, and how long the harness's own
//     listings of open and ready work took over the hour before, against
//     services.maintenance.tracker_limits (trackerlimits.go). It asks the store
//     nothing, says the figures either way, and warns the development manager
//     once when either is past its bound.
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
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// The step names, as the sweep record carries them.
const (
	StepProvider        = "provider"
	StepReconcile       = "reconcile"
	StepTrackerCleanup  = "cleanup"
	StepTrackerMeasure  = "measure"
	StepTrackerSquash   = "squash"
	StepTrackerLimits   = "limits"
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
	// measureEvery is how long after a measurement of the tracker's store that
	// finished the next is due, and measureRetry how long after one that failed.
	// A measurement copies the whole store and runs a cleanup and two squashes
	// over the copy, so it is taken rarely; one that failed is tried again
	// sooner because its figures are what somebody is waiting on.
	measureEvery = 7 * 24 * time.Hour
	measureRetry = 24 * time.Hour
	// measureBound bounds one measurement, every call on the copy included.
	measureBound = 2 * time.Hour
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
	AwaitTakeUp(why string)
	RestartOnRequest(ctx context.Context, name config.ServiceName, now time.Time) string
}

// TrackerStore is the work tracker's store as the cleanup step sees it. It is
// satisfied by beads.Client.
type TrackerStore interface {
	StoreSize() (string, int64, error)
	CleanUpStore(ctx context.Context, hold time.Duration) (beads.StoreCleanup, error)
	CleanUpStoreBeside(ctx context.Context, hold time.Duration) (beads.StoreCleanup, error)
	MissingItems(ctx context.Context, ids []string) ([]string, error)
}

// Cleanups is the record of each cleanup of the tracker's store that ran, and
// of each one that was due and put off. It is satisfied by
// *runstate.TrackerStoreCleanupStore.
type Cleanups interface {
	Read() (runstate.TrackerStoreCleanups, error)
	Record(runstate.TrackerStoreCleanup) error
	RecordPutOff(runstate.TrackerStoreCleanupPutOff) error
	PutOffsSince(since time.Time) (runstate.TrackerStoreCleanupPutOffs, error)
}

// StoreMeasurer measures the work tracker's store on a copy of it. It is
// satisfied by beads.Client.
type StoreMeasurer interface {
	MeasureStore(ctx context.Context, work *repowrite.PinnedRoot) (beads.StoreMeasurement, error)
}

// Measurements is the record of each measurement of the tracker's store, and
// where the copy is made. It is satisfied by
// *runstate.TrackerStoreMeasurementStore.
type Measurements interface {
	Read() (runstate.TrackerStoreMeasurements, error)
	Record(runstate.TrackerStoreMeasurement) error
	WorkDirectory() (*repowrite.PinnedRoot, error)
	Path() string
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
	// Measurer is the primary checkout's tracker store as the measuring step
	// copies it, and Measurements the record of its measurements; where either
	// is nil the step says so.
	Measurer     StoreMeasurer
	Measurements Measurements
	// Calls is the log of every tracker call the harness's processes made,
	// which the measurement counts by caller and the limits step reads the
	// listings' times from, and TrackerLimits the bounds that step holds the
	// store to; its zero value is the configuration's default.
	Calls         TrackerCalls
	TrackerLimits config.TrackerLimits
	// Squasher is the primary checkout's tracker store as the squashing step
	// squashes it, Squashes the record of each decision and where the copies are
	// kept, and Reports where the warning about changed records is filed; where
	// the first two or Measurements are nil the step says so. RepositoryID and
	// Build are what that warning is attributed to.
	Squasher     StoreSquasher
	Squashes     Squashes
	Reports      Reports
	RepositoryID string
	Build        string
	// SquashHolds is where the squash holds new runs while it is due or
	// running, Runs the product's runs it waits on, and DrainBound the watch
	// session's redeploy drain bound, the longest it waits for them and the
	// longest squash it runs; where either of the first two is nil no squash
	// runs and the step says so. Zero DrainBound is the configuration's default.
	SquashHolds SquashHolds
	Runs        RunsInFlight
	DrainBound  time.Duration
	// Sleep waits while the squash waits for the runs in flight, reporting false
	// where ctx ended first; nil waits on the clock.
	Sleep func(ctx context.Context, d time.Duration) bool

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
	// storeSize is the tracker store's size as the last pass read it, for the
	// pass's line, and putOffSince since when a due cleanup has waited for the
	// store to be free, zero where none is waiting.
	storeSize     *storeReading
	storeSizeRead bool
	putOffSince   time.Time
	// lostLookedAt is when the pass last looked for the items a cleanup found
	// missing, so it looks once a cleanup interval rather than at every pass.
	lostLookedAt time.Time
	// limitsWarned is which of the tracker's limits the development manager was
	// last warned of being past, and empty once both are within them
	// (trackerlimits.go).
	limitsWarned string
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
// then the tracker's cleanup, with the record of the cleanup where one ran,
// then the measurement of the tracker's store, then its squash, and then the
// check of its limits.
type offGoroutine struct {
	reconciled runstate.SweepStep
	cleaned    cleanedTracker
	measured   runstate.SweepStep
	squashed   runstate.SweepStep
	limits     runstate.SweepStep
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
	if p.storeSize == nil && p.Tracker != nil && !p.storeSizeRead {
		// Read once before the first pass has, so the line says it from the
		// start; each pass reads it afresh after that.
		p.storeSizeRead = true
		if _, size, err := p.Tracker.StoreSize(); err == nil {
			p.storeSize = &storeReading{size: size, at: p.now()}
		}
	}
	if p.storeSize != nil {
		said += fmt.Sprintf("; the tracker's store measured %s at %s", runstate.SayBytes(p.storeSize.size), local(p.storeSize.at))
	}
	if !p.putOffSince.IsZero() {
		said += fmt.Sprintf("; a cleanup due since %s has been put off because other processes had the store open, and starts beside them from %s", local(p.putOffSince), local(p.putOffSince.Add(busyPatience)))
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
		p.takeUpPromptly()
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
		work.cleaned = p.cleanUpTracker(ctx)
		work.measured = p.measureTracker(ctx)
		work.squashed = p.squashTracker(ctx)
		work.limits = p.checkTrackerLimits()
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
	recorded.Steps = append(recorded.Steps, done.cleaned.step)
	recorded.Steps = append(recorded.Steps, done.measured)
	recorded.Steps = append(recorded.Steps, done.squashed)
	recorded.Steps = append(recorded.Steps, done.limits)
	if done.cleaned.cleanup != nil {
		p.cleanedUp, p.cleanupsRead = done.cleaned.cleanup, true
	}
	if done.cleaned.size != nil {
		p.storeSize = done.cleaned.size
	}
	p.putOffSince = done.cleaned.putOffSince
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
// records it where one ran and where one was due and put off. It runs off the
// supervisor's goroutine, after reconcile, and touches nothing of the
// supervisor's.
func (p *Pass) cleanUpTracker(ctx context.Context) cleanedTracker {
	step := runstate.SweepStep{Name: StepTrackerCleanup}
	if p.Tracker == nil || p.Cleanups == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no tracker store is wired, so nothing is cleaned up"
		return cleanedTracker{step: step}
	}
	bounds := p.cleanupBounds()
	earlier, err := p.Cleanups.Read()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the record of earlier cleanups of the tracker's store could not be read, so whether one is due cannot be told: %v", err)
		return cleanedTracker{step: step}
	}
	store, size, err := p.Tracker.StoreSize()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the tracker's store could not be measured, so whether a cleanup is due cannot be told: %v", err)
		return cleanedTracker{step: step}
	}
	now := p.now()
	result := cleanedTracker{size: &storeReading{size: size, at: now}}
	due := cleanupDue(earlier, size, bounds, p.trackerLimits().StoreMegabytes, now)
	reason := due.reason
	if latest, found := earlier.Latest(); found && latest.Lost() {
		back, held := p.lookForMissing(ctx, latest, reason != "", bounds)
		if held != "" {
			step.Outcome, step.Detail = runstate.StepFailed, held
			if reason != "" {
				step.Detail += p.recordPutOff(runstate.TrackerStoreCleanupPutOff{Store: store, At: now, SizeBefore: size, SizeAfter: size, Reason: reason, Why: held})
			}
			result.step = step
			return result
		}
		reason += "; " + back
	}
	if reason == "" {
		step.Outcome, step.Detail = runstate.StepSkipped, due.notDue
		result.step = step
		return result
	}
	waited, beside, waitUnread := p.busyWait(earlier, due.since, now)
	clean := p.Tracker.CleanUpStore
	if beside {
		clean = p.Tracker.CleanUpStoreBeside
		reason += fmt.Sprintf("; it has waited since %s for no other process to have the store open, which is as long as the pass waits (%s), so it was started beside them", local(waited), busyPatience)
	}
	cleaned, err := clean(ctx, bounds.MaxHold.Duration())
	if err != nil && (!cleaned.Ran() || ctx.Err() != nil) {
		// Nothing was started, or the supervisor is stopping: there is no
		// cleanup to record, and the next pass is due one again.
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the tracker's store (%s) was due a cleanup because %s, and it could not be cleaned up: %v", runstate.SayBytes(size), reason, err)
		result.step = step
		return result
	}
	if !cleaned.Ran() {
		if waited.IsZero() {
			waited = now
		}
		why := fmt.Sprintf("another process had the store open, which a cleanup would have kept waiting: %s; it has waited for the store to be free since %s, and the first pass from %s starts it beside them",
			beads.SayHolders(cleaned.Busy), local(waited), local(waited.Add(busyPatience)))
		if waitUnread != "" {
			why += "; how long it has waited could not be read from its record, so it is measured from now: " + waitUnread
		}
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("the tracker's store (%s) is due a cleanup because %s, and it was put off: %s", runstate.SayBytes(size), reason, why)
		step.Detail += p.recordPutOff(runstate.TrackerStoreCleanupPutOff{Store: cleaned.Store, At: now, SizeBefore: size, SizeAfter: size, Reason: reason, Why: why, Busy: true})
		result.step, result.putOffSince = step, waited
		return result
	}
	record := runstate.TrackerStoreCleanup{
		Store:             cleaned.Store,
		StartedAt:         cleaned.StartedAt,
		Took:              cleaned.Took,
		SizeBefore:        cleaned.SizeBefore,
		SizeAfter:         cleaned.SizeAfter,
		Stopped:           cleaned.Stopped,
		Said:              cleaned.Said,
		Reason:            reason,
		ItemsAfter:        cleaned.ItemsAfter,
		ItemsCountedAfter: cleaned.Counted,
		ItemsNotCounted:   cleaned.NotCounted,
	}
	if len(cleaned.Beside) > 0 {
		record.Beside = beads.SayHolders(cleaned.Beside)
	}
	if err != nil {
		record.Failed = err.Error()
	}
	result.size = &storeReading{size: cleaned.SizeAfter, at: cleaned.StartedAt.Add(cleaned.Took)}
	recordFailure := ""
	if err := p.Cleanups.Record(record); err != nil {
		recordFailure = fmt.Sprintf("; it could not be recorded, so the next pass measures from the cleanup before it: %v", err)
	}
	besideSaid := ""
	if record.Beside != "" {
		besideSaid = ", started beside the processes that had it open: " + record.Beside
	}
	switch {
	case record.Failed != "":
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the tracker's store was due a cleanup because %s, and the cleanup failed after holding the store for %s%s: %s; it is not tried again until the next cleanup is due, at %s or once the store has grown %d MB%s",
			reason, roundTook(cleaned.Took), besideSaid, record.Failed, local(cleaned.StartedAt.Add(bounds.Every.Duration())), bounds.GrownMegabytes, recordFailure)
	case cleaned.Stopped:
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("the tracker's store was due a cleanup because %s, and the cleanup was stopped once it had held the store for %s, so that no other tracker call waits on it past its own limit: from %s to %s, holding the store for %s%s, %s; the next pass finishes it%s",
			reason, bounds.MaxHold, runstate.SayBytes(cleaned.SizeBefore), runstate.SayBytes(cleaned.SizeAfter), roundTook(cleaned.Took), besideSaid, record.SaysItems(), recordFailure)
	default:
		step.Outcome, step.Detail = runstate.StepRan, fmt.Sprintf("cleaned up the tracker's store, due because %s: from %s to %s, holding the store for %s%s, %s%s",
			reason, runstate.SayBytes(cleaned.SizeBefore), runstate.SayBytes(cleaned.SizeAfter), roundTook(cleaned.Took), besideSaid, record.SaysItems(), recordFailure)
		if recordFailure != "" {
			step.Outcome = runstate.StepFailed
		}
	}
	result.step, result.cleanup = step, &record
	return result
}

// cleanedTracker is what the cleanup step came to: its step, the cleanup it
// recorded where one ran, the store's size as last read, and since when a
// cleanup put off because the store was busy has waited.
type cleanedTracker struct {
	step        runstate.SweepStep
	cleanup     *runstate.TrackerStoreCleanup
	size        *storeReading
	putOffSince time.Time
}

// storeReading is the tracker store's size on disk and when it was read.
type storeReading struct {
	size int64
	at   time.Time
}

const (
	// overBoundEvery is how often a store past services.maintenance.tracker_limits'
	// size bound is cleaned up, however little it has grown; and busyPatience
	// is how long a due cleanup waits for no other process to have the store
	// open before it starts beside them. Together, with the pass's default
	// ten-minute cadence, a store past its bound has a cleanup start within
	// five and a half hours of the last, however busy every developer slot
	// keeps it. A cleanup reclaims what its history no longer reaches, which a
	// day of runs leaves plenty of, and holds every tracker call for at most
	// its max_hold, so four hours apart costs little.
	overBoundEvery = 4 * time.Hour
	busyPatience   = time.Hour
)

// busyWait is since when a due cleanup has waited for the store to be free,
// and whether it has waited busyPatience and so starts beside the processes
// holding it. The wait runs from the moment the cleanup fell due, where that is
// known, or from the first pass that put it off as busy since the last cleanup
// started, whichever is earlier: so a pass slower than the default cadence
// does not lengthen it. A record that cannot be read is said, and the wait is
// then measured from the moment it fell due alone.
func (p *Pass) busyWait(earlier runstate.TrackerStoreCleanups, dueSince, now time.Time) (since time.Time, beside bool, unread string) {
	since = dueSince
	var after time.Time
	if latest, found := earlier.Latest(); found {
		after = latest.StartedAt
	}
	putOffs, err := p.Cleanups.PutOffsSince(after)
	if err != nil {
		unread = err.Error()
	} else if first, found := putOffs.FirstBusy(); found && (since.IsZero() || first.At.Before(since)) {
		since = first.At
	}
	if since.IsZero() {
		return since, false, unread
	}
	return since, now.Sub(since) >= busyPatience, unread
}

// recordPutOff records a cleanup that was due and did not start, and says
// where that could not be done.
func (p *Pass) recordPutOff(putOff runstate.TrackerStoreCleanupPutOff) string {
	if err := p.Cleanups.RecordPutOff(putOff); err != nil {
		return fmt.Sprintf("; the postponement could not be recorded: %v", err)
	}
	return ""
}

// measureTracker measures the tracker's store on a copy of it where a
// measurement is due, and records it where one started. It runs off the
// supervisor's goroutine, after the cleanup, and touches nothing of the
// supervisor's.
func (p *Pass) measureTracker(ctx context.Context) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepTrackerMeasure}
	if p.Measurer == nil || p.Measurements == nil {
		step.Outcome, step.Detail = runstate.StepSkipped, "no measurement of the tracker's store is wired, so it is not measured"
		return step
	}
	earlier, err := p.Measurements.Read()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the record of earlier measurements of the tracker's store could not be read, so whether one is due cannot be told: %v", err)
		return step
	}
	if notDue := measurementDue(earlier, p.now()); notDue != "" {
		step.Outcome, step.Detail = runstate.StepSkipped, notDue
		return step
	}
	work, err := p.Measurements.WorkDirectory()
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the directory the tracker's store is copied into to be measured could not be made: %v", err)
		return step
	}
	defer work.Close()
	bounded, cancel := context.WithTimeout(ctx, measureBound)
	measured, err := p.Measurer.MeasureStore(bounded, work)
	cancel()
	switch {
	case len(measured.Busy) > 0:
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("the tracker's store is due to be measured, and it was not copied because another process had it open: %s; the next pass tries again", beads.SayHolders(measured.Busy))
		return step
	case measured.Moved != "":
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("the tracker's store is due to be measured, and its copy was discarded because %s; the next pass tries again", measured.Moved)
		return step
	case measured.StartedAt.IsZero():
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the tracker's store is due to be measured, and it could not be copied: %v", err)
		return step
	}
	record := runstate.TrackerStoreMeasurement{Measurement: measured, Callers: p.countCalls(measured.StartedAt)}
	if err != nil {
		record.Failed = err.Error()
	}
	if last, found := earlier.Latest(); found {
		before, beforeFound := last.Measurement.Stage(beads.StageAsItIs)
		now, nowFound := measured.Stage(beads.StageAsItIs)
		if beforeFound && nowFound && before.Size > 0 && now.Size > 0 {
			record.Grew = &runstate.StoreGrew{Since: last.StartedAt(), From: before.Size, To: now.Size}
		}
	}
	record.Listings = listingLimit(record, earlier)
	recordFailure := ""
	if err := p.Measurements.Record(record); err != nil {
		recordFailure = fmt.Sprintf("; it could not be recorded, so the next pass measures it again: %v", err)
	} else {
		recordFailure = "; recorded in " + p.Measurements.Path()
	}
	step.Outcome, step.Detail = runstate.StepRan, "measured the tracker's store on a copy of it "+record.Says()+recordFailure
	if record.Failed != "" || record.StageFailed() || !record.ExportsUnchanged() || strings.HasPrefix(recordFailure, "; it could not") {
		step.Outcome = runstate.StepFailed
	}
	return step
}

// listingLimit is how the measurement's listings of open and ready items, as
// the store was, stood against squashListingLimit, counting the measurements
// before it in a row that were within it too; nil where it did not time both.
// The earlier ones are judged from their own stages, so a measurement recorded
// before this was kept counts as well.
func listingLimit(measured runstate.TrackerStoreMeasurement, earlier runstate.TrackerStoreMeasurements) *runstate.ListingLimit {
	within, timed := listedAsItIsWithin(measured)
	if !timed {
		return nil
	}
	limit := &runstate.ListingLimit{Limit: squashListingLimit, Within: within}
	if !within {
		return limit
	}
	limit.InARow = 1
	for index := len(earlier) - 1; index >= 0; index-- {
		within, timed := listedAsItIsWithin(earlier[index])
		if !timed {
			continue
		}
		if !within {
			break
		}
		limit.InARow++
	}
	return limit
}

// listedAsItIsWithin reports whether a measurement timed both listings as the
// store was, and whether both finished within squashListingLimit. A listing
// that failed, which is how one that ran out of time is recorded, is past it.
func listedAsItIsWithin(measured runstate.TrackerStoreMeasurement) (within, timed bool) {
	stage, found := measured.Measurement.Stage(beads.StageAsItIs)
	if !found || stage.Failed != "" {
		return false, false
	}
	within = true
	for _, name := range []string{beads.ReadOpenListing, beads.ReadReadyListing} {
		listing, found := stage.Read(name)
		if !found {
			return false, false
		}
		if listing.Failed != "" || listing.Took > squashListingLimit {
			within = false
		}
	}
	return within, true
}

// measurementDue is why no measurement of the tracker's store is due and when
// the next is, and empty where one is due.
func measurementDue(earlier runstate.TrackerStoreMeasurements, now time.Time) string {
	last, found := earlier.Latest()
	if !found {
		return ""
	}
	wait, said := measureEvery, "finished"
	if last.Failed != "" || !last.Measurement.Measured() {
		wait, said = measureRetry, "failed"
	}
	next := last.StartedAt().Add(wait)
	if !now.Before(next) {
		return ""
	}
	return fmt.Sprintf("the tracker's store was last measured at %s, and that measurement %s; the next is due at %s", local(last.StartedAt()), said, local(next))
}

// lookForMissing looks for the items a cleanup found missing, where a cleanup
// is due and the pass has not looked within the cleanup interval, reading each
// item on its own rather than listing the store (beads.Client.MissingItems),
// and says
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
		return "", fmt.Sprintf("%s; the pass looked for them at %s and %s is still missing; it looks again at %s, and %s", said, local(now), runstate.SayIdentifiers(still), local(now.Add(bounds.Every.Duration())), ending)
	}
	// Looked for again at the next pass where this one does not get to run the
	// cleanup, since nothing records that they were found.
	p.lostLookedAt = time.Time{}
	return fmt.Sprintf("the items its last cleanup, at %s, found missing (%s) are all in the store again", local(lost.StartedAt), runstate.SayIdentifiers(lost.Missing)), ""
}

// dueCleanup is whether a cleanup is due: why, where it is, and since when where that
// is known; or why not and when it will be.
type dueCleanup struct {
	reason, notDue string
	since          time.Time
}

// cleanupDue says why a cleanup of a store of size bytes is due, or, where
// none is, why not and when one will be. boundMegabytes is the size bound the
// store is held to (services.maintenance.tracker_limits.store_megabytes): a
// store past it is cleaned up every overBoundEvery.
func cleanupDue(earlier runstate.TrackerStoreCleanups, size int64, bounds config.TrackerCleanup, boundMegabytes int, now time.Time) dueCleanup {
	last, found := earlier.LastFinished()
	if !found {
		return dueCleanup{reason: "the harness has never finished cleaning it up"}
	}
	every := bounds.Every.Duration()
	grownBound := int64(bounds.GrownMegabytes) << 20
	sizeBound := int64(boundMegabytes) << 20
	grown := size - last.SizeAfter
	since := now.Sub(last.StartedAt)
	if since >= every {
		return dueCleanup{reason: fmt.Sprintf("its last cleanup, at %s, was more than %s ago", local(last.StartedAt), bounds.Every), since: last.StartedAt.Add(every)}
	}
	if grown >= grownBound {
		return dueCleanup{reason: fmt.Sprintf("it has grown %s since its last cleanup left it at %s, past the %d MB bound", runstate.SayBytes(grown), runstate.SayBytes(last.SizeAfter), bounds.GrownMegabytes)}
	}
	over := sizeBound > 0 && size > sizeBound
	if over && since >= overBoundEvery {
		fell := dueCleanup{reason: fmt.Sprintf("it is %s, past its %d MB size bound, and its last cleanup, at %s, was more than %s ago", runstate.SayBytes(size), boundMegabytes, local(last.StartedAt), config.Duration(overBoundEvery))}
		if last.SizeAfter > sizeBound {
			// It was past the bound once that cleanup ended, so it fell due as the
			// interval ran out; otherwise when it passed the bound is not known.
			fell.since = last.StartedAt.Add(overBoundEvery)
		}
		return fell
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
	next := last.StartedAt.Add(every)
	if over {
		next = last.StartedAt.Add(overBoundEvery)
		said += fmt.Sprintf(", past its %d MB size bound", boundMegabytes)
	}
	return dueCleanup{notDue: fmt.Sprintf("%s; the next cleanup is due at %s, or sooner once it has grown %d MB", said, local(next), bounds.GrownMegabytes)}
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
	if wait := p.takeUpWait(); wait != "" {
		p.Host.AwaitTakeUp(wait)
		parts = append([]string{fmt.Sprintf("%s, and it moves onto it %s", behind, wait)}, parts...)
		step.Outcome, step.Detail = runstate.StepSkipped, strings.Join(parts, "; ")
		return step, ""
	}
	parts = append([]string{behind + "; it re-executes into the deployed build once this pass is recorded, leaving every part running to be reattached"}, parts...)
	step.Outcome, step.Detail = runstate.StepRan, strings.Join(parts, "; ")
	return step, deployed
}

// takeUpWait is what holds the supervisor back from re-executing into the
// deployed build, in words that finish "it moves onto it", and nothing where
// nothing does: a release cut under way beside the passes, the provider guard,
// or a part the supervisor is itself moving or starting again.
func (p *Pass) takeUpWait() string {
	if p.releasing != nil {
		// A cut that has finished holds nothing back; what it came to is kept for
		// the next pass to record.
		select {
		case released := <-p.releasing.done:
			p.releasing, p.released = nil, &released
		default:
		}
	}
	if p.releasing != nil {
		return fmt.Sprintf("once the release cut under way since %s has finished", runstate.LocalStamp(p.releasing.started))
	}
	if p.released != nil {
		// A finished cut not yet recorded would be lost from the pass log by a
		// re-execution now; the next pass records it and takes the build up.
		return "once the next maintenance pass has recorded the release cut that has finished"
	}
	if held := p.hold(); held != "" {
		return "once nothing holds restarts back; nothing is restarted while " + held
	}
	if moving := p.Host.Moving(); moving != "" {
		return fmt.Sprintf("once the %s has settled", moving)
	}
	return ""
}

// takeUpPromptly is the supervisor's own move onto a deployed build, made at the
// first look nothing holds it back rather than at the end of the next pass,
// which is up to the pass's cadence away and then as long as the pass takes.
// It is taken only between passes, so no pass is left half-done by the
// re-execution; a pass that is due takes the build up itself once it is
// recorded. Where something holds it back, what that is goes on the
// supervisor's record, which is how `yoyo status` says it.
func (p *Pass) takeUpPromptly() {
	deployed, running := p.Host.Deployed(), p.Host.RunningBuild()
	if deployed == "" || running == "" || deployed == running {
		p.Host.AwaitTakeUp("")
		return
	}
	wait := p.takeUpWait()
	p.Host.AwaitTakeUp(wait)
	if wait != "" {
		return
	}
	if err := p.Host.TakeUp(deployed); err != nil {
		p.Host.AwaitTakeUp(fmt.Sprintf("once it can; it was refused at its last look: %v", err))
		return
	}
	p.log("the supervisor is on build %s, behind the deployed %s, and nothing holds it back, so it re-executes into the deployed build now rather than at the end of the next maintenance pass", short(running), short(deployed))
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
