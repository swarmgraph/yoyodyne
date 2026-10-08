// Package supervise is the product's supervisor: the one process that starts
// every enabled part of the product, keeps each running, and stops restarting
// one that keeps failing.
//
// It is the management-and-supervision design's supervision tree, and the
// rules it holds are that design's. One supervisor per product. Its children
// are the Slack sink, the scheduler, and — once adopted — the dashboard, each a
// process of its own with recorded presence and a lease. The supervisor owns
// start, stop, restart, and whether each child is up; each child owns its own
// domain and none owns workflow truth. Children survive the supervisor's
// death: a supervisor that comes back finds them through their leases and
// reattaches rather than killing and respawning. A child that dies is
// restarted with backoff, and one that dies repeatedly is left down and
// reported as degraded, through the standing surfaces rather than a restart
// loop. The supervisor invents no verb: `yoyo start` and `yoyo stop` are the
// operator's, and stopping work is still `yoyo pause` and the intake hold.
//
// What it is not. It is not a second invoker of roles: nothing here asks a
// provider anything, and the children it starts are the harness's own
// processes, each of which passes every gate it passed when started by hand.
// It is not a place configuration grants anything: the services section says
// which registered parts run, and a part started here holds exactly what it
// holds started alone.
package supervise

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The restart bounds. The design says a crashed child is restarted with
// backoff and a repeatedly failing one is left down; these are the numbers.
const (
	// DefaultPoll is how often the supervisor looks at each child.
	DefaultPoll = 5 * time.Second
	// StableAfter is how long a child has to stay up after a start for that
	// start to count as having worked. A death inside it is a rapid failure,
	// which the bound below counts; a death after it is the first failure of a
	// fresh series, because a child that ran for an hour and then died is not
	// a child that cannot start.
	StableAfter = 2 * time.Minute
	// MaxRapidFailures is how many rapid failures in a row are restarted. The
	// next one leaves the child down and degraded.
	MaxRapidFailures = 5
	// MaxRestartBackoff caps the wait between restarts, which doubles from one
	// second with each rapid failure.
	MaxRestartBackoff = 30 * time.Second
)

// Child is one part of the product as the supervisor drives it: whether it is
// running, started if it is not, and stopped. Every real child answers the
// first from its own lease, which is what makes the second idempotent — a
// child asked to start while it is running reports that it already is — and
// the fake children the tests drive answer the same contract.
type Child interface {
	Name() config.ServiceName
	// Running reports whether the child holds its lease.
	Running(ctx context.Context) (bool, error)
	// Ensure makes the child be running: starts one if nothing holds its lease,
	// and reports one already running as not started. It is safe to repeat.
	Ensure(ctx context.Context) (Ensured, error)
	// Stop asks a running child to stop and waits for it to let go of its lease,
	// within the bound the child sets. A child that is not running is nothing
	// to stop, and is reported so rather than as a failure.
	Stop(ctx context.Context) (Stopped, error)
}

// Ensured is what one Ensure did.
type Ensured struct {
	// Started reports that a process was launched. False is a child that was
	// already running, which the supervisor records as reattached.
	Started bool
	PID     int
	Log     string
	// Unstartable is why the child cannot be started at all, which is nothing a
	// retry would change — this product's Slack tokens not stored, say. It is
	// the operator's to settle, so the supervisor degrades the child with it at
	// once rather than spending the restart bound finding out five times.
	Unstartable string
}

// Stopped is what one Stop did.
type Stopped struct {
	WasRunning bool
	PID        int
	// Detail is anything worth saying about how it stopped: that it was still
	// holding its lease when the wait ran out, for one.
	Detail string
}

// NotYet is a part the configuration enables whose adoption as a child has not
// landed. The supervisor declares it and starts nothing, and the reason names
// the work that adopts it, so `yoyo start` says what is missing rather than
// starting a part it does not know how to.
type NotYet struct {
	Name   config.ServiceName
	Reason string
}

// Records is where the supervisor keeps its lease and its account of the
// children. It is satisfied by *runstate.SupervisionStore.
type Records interface {
	Lease() (*runstate.Lease, bool, error)
	Save(runstate.Supervision) error
}

// Supervisor drives one product's children.
type Supervisor struct {
	Records Records
	Product domain.ProductID
	// Children are the parts the supervisor starts, in start order. Stopping
	// takes them in reverse.
	Children []Child
	// NotYet are the enabled parts nothing here can start yet, and Off the
	// parts the configuration leaves off. Both are recorded so the record
	// shows the product's whole shape.
	NotYet []NotYet
	Off    []config.ServiceName
	// Residents are the work the supervisor hosts beside its children, looked
	// at after them on every tick: rebuilding the product's binary when its
	// branch lands is the one there is.
	Residents []Resident
	// Scheduled are the parts that are passes the supervisor takes itself
	// rather than processes it starts — the maintenance pass — each recorded
	// with its cadence and what its last pass came to. Each is also a resident,
	// which is how it is taken.
	Scheduled []Scheduled
	// RestartHold says why nothing running may be restarted right now, and is
	// empty when a restart may go ahead. It is the provider guard: while the
	// provider cannot be reached or is not logged in, a restart cannot renew the
	// login or bring the network back, and it can kill a process that is waiting
	// one of them out. It holds the restarts the supervisor chooses to make — a
	// part moved onto a deployed build, a part restarted on a program manager's
	// request, the supervisor taking a deployed build up itself — and never the
	// start of a part that died, since a part left dead through an outage is a
	// part nothing is left to notice the outage ending. Nil holds nothing.
	RestartHold func() string
	// Poll is how often each child is looked at; zero takes DefaultPoll.
	Poll time.Duration
	// Now and Sleep are the clock, injectable so a test drives the bounds
	// without waiting them out. Sleep reports false where the context ended.
	Now   func() time.Time
	Sleep func(context.Context, time.Duration) bool
	// Log is one window onto a process that otherwise runs silently.
	Log func(format string, args ...any)
	// PID and Build are what the record says the supervisor is.
	PID   int
	Build string
	// Binary is the binary on disk the children are started from. Where it is
	// set, each child that says which build it runs is compared with it and
	// moved onto it when it is deployed over; nil moves nothing.
	Binary *DeployedBinary
	// Copies is every process that recorded itself as a part of the product,
	// which the supervisor settles on its look at builds so exactly one copy of
	// each part it hosts runs (copies.go); nil settles nothing. Collect collects
	// an exited child and reports whether it did; nil asks the operating system.
	Copies  CopyRecords
	Collect func(pid int) (bool, error)
	// PowerHistory is the OS account of sleep and wake, read once a minute.
	PowerHistory    func(context.Context) ([]runstate.PowerEvent, error)
	lastMachineLook time.Time
	seenPower       map[string]bool

	// states is the supervisor's own account of each child, kept between
	// ticks and written to the record when it changes.
	states    map[config.ServiceName]*runstate.SupervisedChild
	startedAt time.Time
	recorded  string
	// deployed is the revision of the binary on disk as this tick read it, and
	// deployLook whether this tick asks each child which build it is on, which
	// it does every DeployEvery.
	deployed       string
	deployLook     bool
	lastDeployLook time.Time
	lastSaid       string
	// takeUp is the deployed build a pass has asked the supervisor to take up
	// by re-executing into it, which Run does once the tick asking it is over.
	takeUp string
	// unaccounted is each copy the supervisor has already said it cannot
	// account for, so the log says each once.
	unaccounted map[string]bool
}

// ErrAlreadyRunning is a supervisor refused because another holds the lease.
var ErrAlreadyRunning = errors.New("a supervisor is already running for this product")

// Run takes the product's supervisor lease, brings every child up, and keeps
// them up until the context ends. It returns when asked to stop, having let
// the lease go and left every child running: the children survive it by
// design, and stopping them is `yoyo stop`'s to do in order.
func (s *Supervisor) Run(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}
	lease, held, err := s.Records.Lease()
	if err != nil {
		return err
	}
	if !held {
		return ErrAlreadyRunning
	}
	defer func() {
		if err := lease.Release(); err != nil {
			s.log("%v", err)
		}
	}()
	s.startedAt = s.now()
	s.log("supervising %s as pid %d", s.Product, s.PID)
	for {
		s.Tick(ctx)
		if s.takeUp != "" {
			s.log("the supervisor for %s lets its lease go to take up the deployed build %s; its children are left running, to be reattached by the build that follows", s.Product, short(s.takeUp))
			return &TakeUpError{Into: s.takeUp}
		}
		if !s.sleep(ctx, s.poll()) {
			s.log("the supervisor for %s is stopping; its children are left running, and `yoyo stop` is what stops them", s.Product)
			return nil
		}
	}
}

// Tick looks at every child once and does what the bounds say: starts what is
// not running and may be, restarts what died once its backoff has passed,
// and leaves down what has failed past the bound. It writes the record when
// anything about a child changed. It is exported so a test drives the
// supervisor one look at a time against a clock it controls.
func (s *Supervisor) Tick(ctx context.Context) {
	if s.states == nil {
		s.states = make(map[config.ServiceName]*runstate.SupervisedChild, len(s.Children))
	}
	if s.startedAt.IsZero() {
		s.startedAt = s.now()
	}
	now := s.now()
	s.observeMachine(ctx, now)
	s.readDeployed()
	s.deployLook = s.lastDeployLook.IsZero() || !now.Before(s.lastDeployLook.Add(DeployEvery))
	if s.deployLook {
		s.lastDeployLook = now
	}
	for _, child := range s.Children {
		if ctx.Err() != nil {
			return
		}
		state, known := s.states[child.Name()]
		if !known {
			state = &runstate.SupervisedChild{Service: child.Name()}
			s.states[child.Name()] = state
		}
		s.look(ctx, child, state, now)
	}
	if s.deployLook {
		s.settleCopies()
	}
	s.record(now)
	for _, resident := range s.Residents {
		if ctx.Err() != nil {
			return
		}
		resident.Look(ctx, now)
	}
}

// look is one child, once.
func (s *Supervisor) look(ctx context.Context, child Child, state *runstate.SupervisedChild, now time.Time) {
	running, err := child.Running(ctx)
	if err != nil {
		// A lease that cannot be asked about is not a child that died, and
		// starting one over it could start a second. It is said and left for the
		// next look.
		s.log("could not tell whether the %s service is running: %v", child.Name(), err)
		return
	}
	if running {
		// Back from re-executing itself into a deployed build: which build it
		// came back as is asked now rather than at the next look at builds.
		returned := state.State != runstate.ChildRunning && state.RestartingInto != ""
		s.up(child, state, now)
		// A child taken back rather than started has no process number from a
		// start, and without one the supervisor cannot tell its own copy from one
		// it did not start; the child's own record says which process holds it.
		if identified, ok := child.(Identified); ok && state.PID <= 0 {
			state.PID = identified.HolderPID(ctx)
		}
		s.deploy(ctx, child, state, now, returned)
		return
	}
	switch state.State {
	case runstate.ChildDegraded:
		// Left down, on purpose, until somebody acts. A degraded child somebody
		// started by hand is found running above and taken back.
		return
	case runstate.ChildRunning:
		switch {
		case s.selfRestarting(child, state):
			// Re-executing itself into the deployed build, which lets its lease go
			// for a moment. That is a restart and not a death: it is given the
			// grace to come back, and started from the binary if it has not.
			state.State = runstate.ChildDown
			state.PID = 0
			state.NextStartAt = now.Add(SelfRestartGrace)
			state.Reason = fmt.Sprintf("restarting itself into the deployed build %s; started from it at %s if it has not taken its lease back by then",
				short(state.RestartingInto), state.NextStartAt.UTC().Format(time.RFC3339))
			s.log("the %s service let its lease go to restart itself into build %s, which is a restart rather than a death", child.Name(), short(state.RestartingInto))
			return
		case state.RestartingInto != "":
			// Asked to stop to move it onto the deployed build, and now gone:
			// started from that build, with nothing counted against it.
			state.PID = 0
			s.start(ctx, child, state, now)
			return
		}
		s.died(child, state, now)
		return
	}
	if !state.NextStartAt.IsZero() && now.Before(state.NextStartAt) {
		return
	}
	s.start(ctx, child, state, now)
}

// up is a child found holding its lease.
func (s *Supervisor) up(child Child, state *runstate.SupervisedChild, now time.Time) {
	if state.State != runstate.ChildRunning {
		if (state.StartedAt.IsZero() && state.RestartingInto == "") || state.State == runstate.ChildDegraded {
			// Running and not started by this supervisor since it last looked:
			// a child that survived the supervisor, or one somebody started by
			// hand. Either way it is taken back rather than started again.
			state.Reattached = true
			s.log("the %s service is running and was reattached", child.Name())
		}
		state.State = runstate.ChildRunning
		state.Reason = ""
		state.NextStartAt = time.Time{}
	}
	// A child that has stayed up long enough has had its start work, so its
	// failures are no longer a series.
	if state.Failures > 0 && (state.StartedAt.IsZero() || !now.Before(state.StartedAt.Add(StableAfter))) {
		state.Failures = 0
	}
}

// died is a child that was running at the last look and is not now.
func (s *Supervisor) died(child Child, state *runstate.SupervisedChild, now time.Time) {
	stable := state.StartedAt.IsZero() || !now.Before(state.StartedAt.Add(StableAfter))
	if stable {
		state.Failures = 0
	}
	state.Failures++
	state.DiedAt = now
	s.collectDied(state.PID)
	state.PID = 0
	if state.Failures > MaxRapidFailures {
		s.degrade(child, state, fmt.Sprintf("died %d times within %s of being started, most recently at %s, so it is left down",
			state.Failures, StableAfter, now.UTC().Format(time.RFC3339)))
		return
	}
	wait := backoff(state.Failures)
	state.State = runstate.ChildDown
	state.NextStartAt = now.Add(wait)
	state.Reason = fmt.Sprintf("died at %s; restarting in %s (restart %d of %d before it is left down)",
		now.UTC().Format(time.RFC3339), wait, state.Failures, MaxRapidFailures)
	s.log("the %s service %s", child.Name(), state.Reason)
}

// start is a child that is not running and may be started.
func (s *Supervisor) start(ctx context.Context, child Child, state *runstate.SupervisedChild, now time.Time) {
	ensured, err := child.Ensure(ctx)
	if err != nil {
		// A start that failed is a failure like a death: it is counted against
		// the same bound and waited out on the same backoff, because a launcher
		// that fails five times in a row is not going to work on the sixth.
		state.Failures++
		if state.Failures > MaxRapidFailures {
			s.degrade(child, state, fmt.Sprintf("could not be started %d times in a row, most recently because %v, so it is left down", state.Failures, err))
			return
		}
		wait := backoff(state.Failures)
		state.State = runstate.ChildDown
		state.NextStartAt = now.Add(wait)
		state.Reason = fmt.Sprintf("could not be started: %v; trying again in %s (attempt %d of %d before it is left down)",
			err, wait, state.Failures, MaxRapidFailures)
		s.log("the %s service %s", child.Name(), state.Reason)
		return
	}
	if ensured.Unstartable != "" {
		s.degrade(child, state, ensured.Unstartable)
		return
	}
	state.State = runstate.ChildRunning
	state.Reason = ""
	state.NextStartAt = time.Time{}
	state.PID = ensured.PID
	if ensured.Log != "" {
		state.Log = ensured.Log
	}
	if ensured.Started {
		state.Reattached = false
		state.StartedAt = now
		state.Starts++
		s.log("started the %s service as pid %d", child.Name(), ensured.PID)
		// Started from the binary on disk, so on the build that binary is.
		if state.RestartingInto != "" {
			s.restarted(child, state, now, "was started from the deployed binary")
		} else if s.deployed != "" {
			state.Build = s.deployed
			state.BuildSince = now
		}
		return
	}
	// Running by the time it was asked: somebody else's start, or the
	// child's own restart. Reattached rather than counted as this
	// supervisor's start.
	state.Reattached = true
	s.log("the %s service was already running and was reattached", child.Name())
}

// degrade leaves a child down with the reason, which is what every surface
// then says about it.
func (s *Supervisor) degrade(child Child, state *runstate.SupervisedChild, reason string) {
	state.State = runstate.ChildDegraded
	state.Reason = reason
	state.NextStartAt = time.Time{}
	s.log("the %s service is degraded: %s", child.Name(), reason)
}

// backoff is the wait before a child's next restart: doubling from a second,
// capped.
func backoff(failures int) time.Duration {
	wait := time.Second
	for i := 1; i < failures && wait < MaxRestartBackoff; i++ {
		wait *= 2
	}
	if wait > MaxRestartBackoff {
		wait = MaxRestartBackoff
	}
	return wait
}

// record writes what the supervisor knows, when anything about it changed.
func (s *Supervisor) record(now time.Time) {
	supervision := s.Supervision(now)
	// Compared without the observation time, which moves on every tick and
	// would make every tick a write.
	rendered := fmt.Sprintf("%+v", supervision.Children)
	if rendered == s.recorded {
		return
	}
	if err := s.Records.Save(supervision); err != nil {
		s.log("the supervisor could not record what it knows about the children, so `yoyo status` will not see it: %v", err)
		return
	}
	s.recorded = rendered
}

// Supervision is the record as it stands: every declared part in the section's
// order, with what the supervisor knows about each.
func (s *Supervisor) Supervision(now time.Time) runstate.Supervision {
	children := make([]runstate.SupervisedChild, 0, len(config.ServiceNames))
	for _, name := range config.ServiceNames {
		if state, known := s.states[name]; known {
			children = append(children, *state)
			continue
		}
		if pass, scheduled := s.scheduled(name); scheduled {
			children = append(children, runstate.SupervisedChild{Service: name, State: runstate.ChildScheduled, Reason: pass.Describe()})
			continue
		}
		if reason, notYet := s.notYet(name); notYet {
			children = append(children, runstate.SupervisedChild{Service: name, State: runstate.ChildNotYet, Reason: reason})
			continue
		}
		for _, off := range s.Off {
			if off == name {
				children = append(children, runstate.SupervisedChild{Service: name, State: runstate.ChildOff, Reason: "not enabled in the services section"})
				break
			}
		}
	}
	return runstate.Supervision{
		SchemaVersion: runstate.SupervisionSchemaVersion,
		ProductID:     s.Product,
		PID:           s.PID,
		Build:         s.Build,
		Deployed:      s.deployed,
		StartedAt:     s.startedAt.UTC(),
		ObservedAt:    now.UTC(),
		Children:      children,
	}
}

func (s *Supervisor) scheduled(name config.ServiceName) (Scheduled, bool) {
	for _, pass := range s.Scheduled {
		if pass != nil && pass.Service() == name {
			return pass, true
		}
	}
	return nil, false
}

func (s *Supervisor) notYet(name config.ServiceName) (string, bool) {
	for _, part := range s.NotYet {
		if part.Name == name {
			return part.Reason, true
		}
	}
	return "", false
}

func (s *Supervisor) validate() error {
	var problems []error
	if s.Records == nil {
		problems = append(problems, errors.New("a supervisor needs somewhere to keep its lease and its record"))
	}
	if s.PID <= 0 {
		problems = append(problems, errors.New("a supervisor needs to know which process it is"))
	}
	if err := domain.ValidateIdentifier("product id", string(s.Product)); err != nil {
		problems = append(problems, err)
	}
	for _, resident := range s.Residents {
		if resident == nil {
			problems = append(problems, errors.New("a supervisor was given a resident that is nothing"))
		}
	}
	for _, pass := range s.Scheduled {
		if pass == nil {
			problems = append(problems, errors.New("a supervisor was given a scheduled pass that is nothing"))
		}
	}
	seen := make(map[config.ServiceName]struct{}, len(s.Children))
	for _, child := range s.Children {
		if child == nil {
			problems = append(problems, errors.New("a supervisor was given a child that is nothing"))
			continue
		}
		if _, duplicate := seen[child.Name()]; duplicate {
			problems = append(problems, fmt.Errorf("the %s service was given to the supervisor twice", child.Name()))
		}
		seen[child.Name()] = struct{}{}
	}
	return errors.Join(problems...)
}

func (s *Supervisor) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Supervisor) poll() time.Duration {
	if s.Poll > 0 {
		return s.Poll
	}
	return DefaultPoll
}

func (s *Supervisor) sleep(ctx context.Context, wait time.Duration) bool {
	if s.Sleep != nil {
		return s.Sleep(ctx, wait)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Supervisor) log(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

// StopAll stops every child in reverse start order, and reports what each
// did. It is what `yoyo stop` does after the supervisor itself has been asked
// to stop — the supervisor first, so nothing restarts a child as it goes down
// — and it carries on past a child that would not stop, because the ones
// after it still have to be asked.
func StopAll(ctx context.Context, children []Child, log func(format string, args ...any)) ([]ChildStop, error) {
	stops := make([]ChildStop, 0, len(children))
	var problems []error
	for index := len(children) - 1; index >= 0; index-- {
		child := children[index]
		if log != nil {
			log("stopping the %s service", child.Name())
		}
		stopped, err := child.Stop(ctx)
		stop := ChildStop{Service: child.Name(), Stopped: stopped}
		if err != nil {
			stop.Problem = err.Error()
			problems = append(problems, fmt.Errorf("stop the %s service: %w", child.Name(), err))
		}
		stops = append(stops, stop)
	}
	return stops, errors.Join(problems...)
}

// ChildStop is what stopping one child came to.
type ChildStop struct {
	Service config.ServiceName `json:"service"`
	Stopped Stopped            `json:"stopped"`
	Problem string             `json:"problem,omitempty"`
}

// Describe is one child's stop in a sentence.
func (c ChildStop) Describe() string {
	switch {
	case c.Problem != "":
		return fmt.Sprintf("%s: %s", c.Service, c.Problem)
	case !c.Stopped.WasRunning:
		return fmt.Sprintf("%s: was not running", c.Service)
	case c.Stopped.Detail != "":
		return fmt.Sprintf("%s: asked pid %d to stop; %s", c.Service, c.Stopped.PID, c.Stopped.Detail)
	default:
		return fmt.Sprintf("%s: stopped pid %d", c.Service, c.Stopped.PID)
	}
}

// DescribeChild is one child's state in a sentence, the same words on every
// surface that says it.
func DescribeChild(child runstate.SupervisedChild) string {
	var said strings.Builder
	fmt.Fprintf(&said, "%s: ", child.Service)
	switch child.State {
	case runstate.ChildRunning:
		if child.Reattached {
			said.WriteString("running, reattached")
		} else {
			said.WriteString("running")
		}
		if child.PID > 0 {
			fmt.Fprintf(&said, " as pid %d", child.PID)
		}
		if child.Log != "" {
			fmt.Fprintf(&said, ", logging to %s", child.Log)
		}
		said.WriteString(describeBuild(child))
		if child.Redeploy != "" {
			fmt.Fprintf(&said, "; %s", child.Redeploy)
		}
	case runstate.ChildDegraded:
		fmt.Fprintf(&said, "degraded, %s", child.Reason)
	case runstate.ChildDown:
		fmt.Fprintf(&said, "down, %s", child.Reason)
	case runstate.ChildNotYet:
		fmt.Fprintf(&said, "enabled, and not yet a child of the supervisor: %s", child.Reason)
	case runstate.ChildScheduled:
		fmt.Fprintf(&said, "the supervisor's own pass, %s", child.Reason)
	case runstate.ChildOff:
		fmt.Fprintf(&said, "off; set services.%s.enabled to start it with the product", child.Service)
	default:
		fmt.Fprintf(&said, "%s", child.State)
	}
	return said.String()
}

// describeBuild is which build a running child is on and since when, in the
// reader's own zone, and how many times a deploy has moved it. It says nothing
// where the child has said nothing the supervisor could read.
func describeBuild(child runstate.SupervisedChild) string {
	if child.Build == "" {
		return ""
	}
	said := fmt.Sprintf(", on build %s", short(child.Build))
	if !child.BuildSince.IsZero() {
		said += " since " + child.BuildSince.Local().Format("2006-01-02 15:04 MST")
	}
	switch child.Restarts {
	case 0:
	case 1:
		said += " (restarted into a deployed build once)"
	default:
		said += fmt.Sprintf(" (restarted into a deployed build %d times)", child.Restarts)
	}
	return said
}
