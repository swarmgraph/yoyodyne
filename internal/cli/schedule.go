package cli

// `yoyo work`: the harness choosing what to run, rather than being told.
//
// `yoyo run <id>` is the operator naming an item, and it stays exactly what it
// was. This is the other entry point the design names — ready work scheduled by
// the harness itself, several items at once where the configuration allows it —
// and everything that separates the two lives in one place: the intake hold
// applies here and not there, and every run this starts records why it was
// chosen.
//
// It drains by default and watches when asked. That way round is deliberate and
// it is temporary: watching is the shape the loop is meant to have, and it waits
// on stopped work having an owner before it becomes what an operator gets
// without asking for it. One stoppage has one now — a run that failed
// independent review after every permitted attempt is put in front of the
// development manager by the pass itself, one per pull, and the repair or the
// re-run she decides about it is fired by the pass too, as many per pull as
// there are slots for them — and the rest of them,
// a failing check and a refused path and a replay conflict among them, still
// wait on somebody reading the docket. `--until-drained` states today's default
// out loud so that flipping it is one line here rather than a behaviour change
// nobody wrote down.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readiness"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/redeploy"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/staleness"
	"github.com/mason-bryant/yoyodyne/internal/watchdog"
)

type scheduleOutput struct {
	Schedule *orchestrator.Schedule `json:"schedule,omitempty"`
	Error    string                 `json:"error,omitempty"`
}

// scheduleWork pulls ready work from the backlog and runs it, up to the
// configured developer capacity, returning once every run it started has ended.
func scheduleWork(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("work", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	limit := flags.Int("limit", 0, "stop after starting this many runs (default: no bound on how many)")
	watch := flags.Bool("watch", false, "keep pulling work as it becomes ready, until stopped")
	untilDrained := flags.Bool("until-drained", false, "return once nothing more is ready to pull (the default)")
	budget := flags.Float64("budget", 0, "stop once this session has spent this many dollars (default: unbounded)")
	// How long nothing may start, over ready work and with nothing accounting for
	// it, before this session records that the harness has stopped. It is the
	// same number `yoyo reconcile --stall-after` takes, and the two are the two
	// places the reading happens: this loop catches a session that is alive and
	// has stopped choosing, and the sweep catches the session that died. An
	// operator who moves one should move the other, or the wider setting is the
	// one that does not decide.
	stallAfter := flags.Duration("stall-after", readmodel.DefaultStallThreshold, "how long nothing may start over ready work before this session records that the harness has stopped")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "work does not accept positional arguments")
		printWorkUsage(stderr)
		return 2
	}
	if *limit < 0 {
		fmt.Fprintln(stderr, "--limit cannot be negative")
		return 2
	}
	if *budget < 0 {
		fmt.Fprintln(stderr, "--budget cannot be negative; leave it out for an unbounded session")
		return 2
	}
	// Asking for both is asking for opposite things, and guessing which one was
	// meant is how a session ends up running all night that was meant to return.
	if *watch && *untilDrained {
		fmt.Fprintln(stderr, "--watch and --until-drained ask for opposite things: one stays open, the other returns when the queue is empty")
		return 2
	}
	// There is no way to ask for no watchdog, for the reason the sweep gives: a
	// threshold of zero would take the default rather than turn anything off, so
	// reading it as a switch would silently do the opposite of what somebody meant.
	if *stallAfter <= 0 {
		fmt.Fprintln(stderr, "stall-after must be positive; it is how long nothing may start rather than a switch, and there is no way to ask not to be told")
		return 2
	}

	scheduler := orchestrator.Scheduler{
		Limit:    *limit,
		Watching: *watch,
		Budget:   *budget,
		// The configuration is read here rather than above, once per pull. That
		// is the decision the design question asked for: capacity and priority
		// changes take effect at the next selection, which keeps the answer the
		// one every other command already gives and matches how the backlog is
		// steered. A run already in flight keeps the configuration its own pull
		// read, because a run's parameters are fixed when it is reserved.
		Open: func(context.Context) (orchestrator.Pull, error) { return openPull(*configPath, stderr) },
	}
	// A session that stays open says so where somebody who is not at this
	// terminal can read it. Failing to open that log fails the command rather
	// than starting a session nothing can see: an unattended session nobody can
	// tell is alive is the state the whole guard exists to prevent, and it is
	// better refused at the start than discovered in the morning.
	//
	// binary is the file this session is executing, resolved for a watch and for
	// nothing else, and left nil where the platform cannot replace a running
	// process.
	//
	// sessions is held past the block that opens it because the last thing this
	// command may have to say about the session is said after the scheduler has
	// returned: a stop recorded as a restart that then does not happen has to be
	// corrected in the same log it was claimed in.
	var binary *redeploy.Binary
	var sessions orchestrator.WatchSessions
	var watching *runstate.Lease
	if *watch {
		sessionID, err := runstate.NewWatchSessionID()
		if err != nil {
			fmt.Fprintf(stderr, "work failed: %v\n", err)
			return 1
		}
		// One session per product, taken before this one reads anything and held
		// for as long as it watches. A second session is refused here rather than
		// discovered later, because what two of them share is the queue: neither
		// can see what the other has chosen until the run reserves.
		watching, err = holdTheWatch(*configPath, sessionID)
		if err != nil {
			fmt.Fprintf(stderr, "work failed: %v\n", err)
			return 1
		}
		// Every way out of this command goes through here, which is what makes the
		// release one thing rather than one per exit: the pass returning, a bound
		// stopping it, and the restart that did not happen after the session
		// stopped to take up a deploy. A restart that does happen has already let
		// the watch go, below, so the build it becomes can take it.
		defer func() {
			if err := watching.Release(); err != nil {
				fmt.Fprintf(stderr, "%v\n", err)
			}
		}()
		opened, err := openWatchSession(*configPath, sessionID)
		if err != nil {
			fmt.Fprintf(stderr, "work failed: %v\n", err)
			return 1
		}
		sessions = opened
		scheduler.Sessions = sessions
		// The watch is the product's scheduler, and says which configuration keys
		// its build reads for the reason every long-running part does. A watch
		// that re-executes into a deployed build comes back through here and
		// replaces the record.
		if resolved, err := loadConfiguration(*configPath); err == nil {
			recordConfigReader(resolved, string(config.ServiceScheduler), stderr)
		}
		scheduler.SessionID = sessionID
		// This loop is the harness's own, so it is one of the two places the stall
		// reading is taken. Failing to assemble it does not stop the session: a
		// watchdog that could not be built is a session with no watchdog, which is
		// every session before this existed, and refusing to choose work over it
		// would be the reporting process's failure stopping the work again.
		watchdogFor, err := openStallWatch(*configPath, *stallAfter, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "this session will not notice that the harness has stopped, and `yoyo reconcile` is what notices instead: %v\n", err)
		} else {
			scheduler.Watchdog = watchdogFor
		}
		// Which file this process was started from, read before anything runs. A
		// watching session outlives the deploys that land behind it, and this is
		// what lets it take one up between runs rather than dispatching from a
		// build the harness moved past until somebody restarts it.
		//
		// A platform that cannot replace a running process says so here, and the
		// session watches without it. That is a session as capable as every session
		// before this existed, so refusing to watch at all over it would take away
		// more than this adds — but it is said out loud, because a session that
		// will not take up a deploy is exactly what somebody would otherwise assume
		// had happened.
		binary, err = redeploy.Running()
		switch {
		case errors.Is(err, redeploy.ErrUnsupported):
			fmt.Fprintf(stderr, "this session will not take up a build deployed over it, and has to be restarted by hand for one: %v\n", err)
		case err != nil:
			fmt.Fprintf(stderr, "work failed: %v\n", err)
			return 1
		default:
			scheduler.Deployment = binary
		}
	}
	schedule, err := scheduler.Schedule(ctx)
	code := reportSchedule(stdout, stderr, *jsonOutput, schedule, err)
	if !schedule.Redeploying() || binary == nil {
		return code
	}
	return takeUpTheDeploy(ctx, binary, sessions, watching, stderr, code, *budget, schedule.SpentUSD, *limit, schedule.Chosen())
}

// deployedBinary is what taking up a deploy needs of the file this session is
// executing: the invocation to carry across, and the re-execution itself. It is
// an interface so that the half of this which only happens when the restart does
// not is reachable from a test — a re-execution that works never returns, so the
// failures are the only part of it a test can ever observe.
type deployedBinary interface {
	Args() []string
	Take([]string) error
}

// takeoverDeadline bounds the re-execution itself: how long the session waits
// for the operating system to replace it with the build deployed over it before
// it stops waiting and exits.
//
// It exists because the alternative turned out not to be "the restart fails".
// On 2026-09-05 a session logged the stop that begins the takeover and then went
// silent for an hour — alive, holding the queue, choosing nothing, and answering
// no signal below SIGKILL. A re-execution that is going to happen happens in
// milliseconds, so anything past a minute is not a slow restart, it is a restart
// that is not coming; and a session that exits instead is one the supervisor
// starts again from the build that was deployed, which is where the takeover was
// trying to get to anyway.
const takeoverDeadline = time.Minute

// takeUpTheDeploy is everything the command does after the scheduler has stopped
// the session in order to become the build deployed over it. It returns only
// where the restart did not happen, with the code the command exits on.
//
// Every way it declines corrects the durable record before returning. The
// stop is already in the watch log marked as a restart — the scheduler writes it
// as it stops, which is the only moment there is, because a re-execution that
// works never comes back to write anything — so a refusal that said nothing
// would leave every surface telling a reader that a stopped line is on its way
// back.
func takeUpTheDeploy(ctx context.Context, binary deployedBinary, sessions orchestrator.WatchSessions, watching *runstate.Lease, stderr io.Writer, code int, budget, spent float64, limit, started int) int {
	// The watch goes first, before either branch below. The scheduler has stopped
	// the session and either waited out every run it started or stopped and
	// preserved the ones its drain bound cut off, so nothing after this chooses
	// work again — and the build this restarts into takes the same lease
	// as it starts, which a process still holding it would refuse. It is dropped
	// here rather than left to the operating system closing the descriptor as the
	// image is replaced, because that would make the restart depend on a flag on
	// the file the lock happens to be taken through. Releasing twice is a no-op,
	// so the command's own release still covers the exits below.
	if err := watching.Release(); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
	}
	// A bounded session arrives here with something left of both its bounds: what
	// it has spent and how many runs it has started are checked at the top of
	// every pull, ahead of the redeploy, so a bound that is gone stops the session
	// on that bound rather than restarting it.
	//
	// The guard is here because the cost of that ordering ever changing is silent
	// in one direction and loud in the other, and neither is acceptable: a build
	// invoked with a negative budget refuses to start and takes the line down,
	// and a build invoked with "--limit 0" starts an unbounded session, which is
	// the operator's cap disappearing with nobody told.
	if gone := exhausted(budget, spent, limit, started); gone != "" {
		fmt.Fprintf(stderr, "work stopped to restart into the build deployed over it, and did not: %s\n", gone)
		endedInstead(sessions, stderr, "the session stopped to restart into the build deployed over it and did not: "+gone)
		return code
	}
	// Every run this session started has already ended, and the queue has been
	// left alone since the deploy landed. What is left is to become the build that
	// was deployed: the account of the session that just ended has been printed,
	// and nothing returns from this when it works — the process goes on as the new
	// build, watching the same queue under what is left of the same bounds.
	if err := takeWithin(ctx, binary, continued(binary.Args(), budget, spent, limit, started), takeoverDeadline); err != nil {
		fmt.Fprintf(stderr, "work stopped to restart into the build deployed over it, and could not: %v\n", err)
		endedInstead(sessions, stderr, fmt.Sprintf("the session stopped to restart into the build deployed over it and could not: %v", err))
		return 1
	}
	return code
}

// takeWithin re-executes this process from the deployed build, and gives up on
// it rather than waiting on it forever.
//
// The re-execution runs beside this rather than in it for one reason: a call
// that replaces the process image does not return, so there is nothing to bound
// from inside it. What is bounded is the waiting — the deadline, and the session
// being asked to stop, which is the other thing that must not be answered by
// standing here. A signal that arrived while the session was waiting to become
// the new build is an operator asking for this process to end, and ending is
// something it can still do.
//
// The call it walked away from is still armed, and that is worth being plain
// about: if the operating system replaces this image a moment after the deadline
// passed, the process becomes the new build with the log saying it ended
// instead. That is a correction that reads wrong for one line, against an hour
// of a session that was alive and choosing nothing, and the exchange is not
// close.
func takeWithin(ctx context.Context, binary deployedBinary, args []string, deadline time.Duration) error {
	refused := make(chan error, 1)
	go func() { refused <- binary.Take(args) }()
	waited := time.NewTimer(deadline)
	defer waited.Stop()
	select {
	case err := <-refused:
		return err
	case <-waited.C:
		return fmt.Errorf("the re-execution had not happened %s after it was asked for, so the session exits and whatever started it starts the next one", deadline)
	case <-ctx.Done():
		return fmt.Errorf("the session was asked to stop before the re-execution happened, so it exits rather than restarting: %w", ctx.Err())
	}
}

// endedInstead corrects the durable account of a session that stopped in order
// to restart and then did not.
//
// The claim it corrects is not a mistake, and it cannot be avoided by writing it
// later: a restart that works never returns, so the moment the session stops is
// the only moment there is to record why. What that costs is this window — the
// stop is already in the log marked as a restart, and both refusals below it
// leave the process exiting with the log saying it is coming back. Every reader
// who is not at this terminal would be told a stopped line needs nothing from
// them, which is precisely the standing chore the self-redeploy exists to end
// rather than to reproduce in a rarer place.
//
// So the correction is a second stop, marked as the ending it turned out to be
// and carrying why. The log is append-only and read forward, so what the
// surfaces get is the same shape as every other correction here: the line that
// said the session was coming back, and then the line saying it is not.
//
// A correction that cannot be written costs the session its visibility and not
// its work, exactly as the transitions the scheduler writes do, so it is said
// here rather than turned into a second failure of a process that has already
// stopped.
func endedInstead(sessions orchestrator.WatchSessions, stderr io.Writer, why string) {
	if sessions == nil {
		return
	}
	if err := sessions.Record(orchestrator.SessionState{
		State:  runstate.WatchStopped,
		At:     time.Now().UTC(),
		Reason: why,
	}); err != nil {
		fmt.Fprintf(stderr, "the session could not record that it ended rather than restarting, so the log still says it is coming back: %v\n", err)
	}
}

// exhausted names a bound a session has nothing left of, and says nothing for a
// session that may still be restarted.
//
// Zero is how both of these flags ask for no bound at all, so a remainder of
// zero must never be written into a restart: the bound would come back as its
// own absence. Nothing is expected to reach it — a session that has reached
// either bound stops on that bound before it can redeploy — which is exactly why
// it is here rather than assumed, because what a mistake in that ordering costs
// is the operator's cap, quietly.
func exhausted(budget, spent float64, limit, started int) string {
	switch {
	case budget > 0 && budget-spent <= 0:
		return "nothing was left of the budget the session was given"
	case limit > 0 && limit-started <= 0:
		return "no runs were left of the number the session was given"
	}
	return ""
}

// continued is how a session's own command line crosses a restart: the same
// invocation, with the bounds the operator gave it reduced to what is left of
// them.
//
// A bound carried whole would be a bound that starts again at every deploy, and
// on a machine that deploys several times a day that is not a bound at all — a
// session forty-five dollars into a fifty dollar budget would come back with
// fifty. Nothing about a deploy is the operator raising a cap they set, so what
// crosses the restart is the remainder: the budget less what the session spent,
// and the run count less what it started.
//
// Both remainders are strictly positive here: a session that has reached either
// bound stops on that bound instead of redeploying, and exhausted refuses the
// restart ahead of this if it ever does not.
func continued(args []string, budget, spent float64, limit, started int) []string {
	invocation := append([]string(nil), args...)
	if budget > 0 {
		replaceFlagValue(invocation, "budget", remainingBudget(budget-spent))
	}
	if limit > 0 {
		replaceFlagValue(invocation, "limit", strconv.Itoa(limit-started))
	}
	return invocation
}

// remainingBudget writes what is left of a budget as the argument the build this
// session restarts into is given.
//
// It is rounded down to the cent, which is how money is said everywhere else
// here and is the safe direction: rounded up, the restart would hand back a
// fraction of a cent the session had already spent. Under a cent it is written
// exactly instead, because a remainder rounded to "0.00" is how an unbounded
// session is asked for, and a bound that quietly became no bound at all is the
// one mistake this must not make.
func remainingBudget(remaining float64) string {
	if cents := math.Floor(remaining*100) / 100; cents > 0 {
		return strconv.FormatFloat(cents, 'f', 2, 64)
	}
	return strconv.FormatFloat(remaining, 'f', -1, 64)
}

// replaceFlagValue rewrites one flag's value wherever a command line gives it,
// in both spellings the flag package accepts and with either one dash or two. A
// flag the command line does not carry is left absent rather than added: what is
// being rewritten is a value the operator wrote, and a bound nobody asked for is
// not this command's to invent.
func replaceFlagValue(args []string, name, value string) {
	for index := 0; index < len(args); index++ {
		if args[index] == "-"+name || args[index] == "--"+name {
			if index+1 < len(args) {
				args[index+1] = value
				index++
			}
			continue
		}
		for _, prefix := range []string{"-" + name + "=", "--" + name + "="} {
			if strings.HasPrefix(args[index], prefix) {
				args[index] = prefix + value
			}
		}
	}
}

// holdTheWatch makes this process the product's only watching session, and names
// the session that has it where it cannot.
//
// Two sessions against one product double-spend rather than share the work:
// each reads the whole ready queue and chooses from it, and an item one of them
// chose is invisible to the other until its run reserves, several steps later.
// The refusal names the holder because that is what somebody acts on — the same
// session `yoyo status` reports, and the process to stop where stopping it is
// what they want — and it refuses whether or not the holder can be named, since
// not knowing which session has the watch is no reason to start a second one.
func holdTheWatch(configPath, sessionID string) (*runstate.Lease, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return nil, err
	}
	lease, held, err := parts.watch.Lease(sessionID)
	if err != nil {
		return nil, err
	}
	if !held {
		return nil, fmt.Errorf("another session is already watching this product%s; one session per product is what keeps two of them from choosing the same item", heldWatchBy(parts.watch))
	}
	return lease, nil
}

// heldWatchBy names the session holding the watch, as the end of the sentence
// that refuses a second one. A stamp that is not there or will not read says so
// rather than being left out: the reader is owed the difference between a
// refusal that named nobody and one that could not.
func heldWatchBy(watch *runstate.WatchStore) string {
	holder, found, err := watch.Holder()
	switch {
	case err != nil:
		return fmt.Sprintf(", and which session it is could not be read: %v", err)
	case !found:
		return ", and it did not record which session it is"
	}
	return fmt.Sprintf(": session %s, process %d, watching since %s",
		holder.SessionID, holder.PID, holder.HeldAt.Format(time.RFC3339))
}

// openWatchSession gives one session of watching somewhere durable to say what
// it is doing. The identifier is the command's rather than the scheduler's,
// because it is the session's identity rather than the loop's: the same name is
// stamped on the watch this session holds, so a session refused by that lease
// and a session read out of this log are the same session to whoever reads
// either.
func openWatchSession(configPath, sessionID string) (orchestrator.WatchSessions, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return nil, err
	}
	return watchSessionLog{
		store:     parts.watch,
		productID: parts.config.Product.ID,
		sessionID: sessionID,
		// What this session is actually running, read once as it opens rather than
		// on every transition: a process does not change binary while it lives, and
		// that is exactly the problem — it goes on running what it was started with
		// while the harness moves on underneath it. A binary that recorded no
		// revision leaves this empty, which reads as a comparison nobody can make.
		build: buildinfo.Commit(),
	}, nil
}

// openStallWatch builds the stall reading this session takes of itself, gated so
// a loop that polls in seconds does not spawn a tracker process every poll.
//
// The reading is the same one `yoyo reconcile` takes, from the same package and
// over the same records, so a session and a sweep cannot come to two answers
// about one machine. What differs is only which failure each catches: a session
// that is alive and has stopped choosing writes nothing about that and is caught
// here, and a session that died is caught by the sweep.
func openStallWatch(configPath string, threshold time.Duration, stderr io.Writer) (func(context.Context), error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return nil, err
	}
	stalls, err := runstate.NewStallStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return nil, err
	}
	watch := &stallWatch{
		checker: watchdog.Checker{
			Runs:        parts.store,
			Sessions:    parts.watch,
			Holds:       parts.holds,
			Intake:      parts.intake,
			Outages:     parts.outages,
			Divergences: parts.divergences,
			Backlog:     readyBacklog{tracker: parts.tracker()},
			Stalls:      stalls,
			Threshold:   threshold,
		},
		threshold: threshold,
		stderr:    stderr,
	}
	return watch.check, nil
}

// stallWatch is one session's memory of when it last took the reading, which is
// the whole of the gate.
//
// The gate is the threshold rather than the poll interval, and that is a cost
// decision rather than a promptness one: a drained queue is deliberately not a
// state that accounts for the quiet, since nothing but the tracker can say the
// queue is drained, so an ungated reading would spawn `bd` on every poll of a
// perfectly healthy idle product. Gating at the threshold cannot delay a stall
// past it either — the reading is due exactly as often as a stall could newly
// become true.
//
// Nothing here is synchronized because nothing needs to be: the scheduler calls
// this from the one goroutine that runs its loop, never from a run's.
type stallWatch struct {
	checker   watchdog.Checker
	threshold time.Duration
	stderr    io.Writer
	// last is when the reading was last taken, zero before the first.
	last time.Time
	// now is injected so a test does not have to spend real minutes.
	now func() time.Time
}

// check takes the reading where one is due, and says what it found only where
// there is something to say: a stall this reading opened, or a reading that could
// not be made. A machine that is behaving says nothing at all, on every poll for
// the life of the session.
func (w *stallWatch) check(ctx context.Context) {
	at := w.stamp()
	if !w.last.IsZero() && at.Sub(w.last) < w.threshold {
		return
	}
	w.last = at
	reading, err := w.checker.Check(ctx)
	if err != nil {
		// Nothing was recorded and nothing is guessed. It is said where the session
		// says everything else about itself, and asked again at the next interval
		// rather than at the next poll, so a tracker that is down does not become a
		// tracker asked every fifteen seconds.
		fmt.Fprintf(w.stderr, "whether this product has gone quiet was not decided: %v\n", err)
		return
	}
	// Only a stall this reading opened is said. One that was already standing has
	// been said once already, by whichever reading opened it, and repeating it
	// every threshold for the life of the session is the nagging the record exists
	// to prevent.
	if reading.Opened == nil {
		return
	}
	fmt.Fprintf(w.stderr, "nothing has started on this product for %s, with %d item(s) ready\n",
		reading.Opened.For().Round(time.Second), reading.Opened.Ready)
	if reading.Opened.Chooser != "" {
		fmt.Fprintf(w.stderr, "  %s\n", reading.Opened.Chooser)
	}
}

func (w *stallWatch) stamp() time.Time {
	if w.now == nil {
		return time.Now().UTC()
	}
	return w.now().UTC()
}

// watchSessionLog is the scheduler's account of itself, written into the
// product's watch log. It carries the identity the scheduler has no business
// knowing about — which product, which session — and nothing else.
type watchSessionLog struct {
	store     *runstate.WatchStore
	productID domain.ProductID
	sessionID string
	// build is the revision this process was built from, carried on every
	// transition so a reader who arrives mid-session finds it on the entry the
	// session happened to write last.
	build string
}

func (w watchSessionLog) Record(transition orchestrator.SessionState) error {
	return w.store.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     w.productID,
		SessionID:     w.sessionID,
		State:         transition.State,
		At:            transition.At,
		Reason:        transition.Reason,
		// What the session could see going, and the conversation it is waiting on
		// where it is waiting on one. Both are what keep an idle line from reading as
		// a stopped machine or as a queue nobody has admitted work to.
		Running:  transition.Running,
		Executor: transition.Executor,
		// Whose move a braked poll is, in the hold's own words, so the channel's
		// closing clause names the development manager or the harness rather than
		// the operator over a hold the brake is working itself.
		Mover: transition.Mover,
		// The same account the reason states, in classes rather than in prose, so
		// the alarm that wakes somebody names the cause the poll already found
		// instead of deriving its own.
		PassedOver: transition.PassedOver,
		Unreadable: transition.Unreadable,
		// The provider's usage window, so a wait nobody can shorten is readable as
		// one rather than as a queue nothing is pulling.
		ProviderWindow:         transition.ProviderWindow,
		ProviderWindowResetsAt: transition.ProviderWindowResetsAt,
		Build:                  w.build,
		// A stop that is a restart says so, so the reader who is not at this
		// terminal is told a session is coming back rather than told to start one.
		Restarting: transition.Restarting,
		// A dispatch holding a slot while it waits out the tracker before it has
		// claimed anything, which no run record exists yet to say.
		DispatchWait: transition.DispatchWait,
		// A Git command a dispatch ran again over another worktree's creation or
		// removal, which the re-run otherwise absorbs without a trace.
		WorktreeCrossing: transition.WorktreeCrossing,
		// The recurring pass the session has begun inside its poll, which is what
		// the session is doing — and not pulling — until its next line.
		RecurringPass: transition.RecurringPass,
		// The drain and its bound, on every line the session writes while it
		// waits out its runs to restart, so a reader is told what stops the wait
		// rather than left to time it.
		Draining: transition.Draining,
	})
}

// openPull builds everything one pull acts through from one reading of the
// configuration. The parts are captured by the starter it returns, so each run
// this pull begins uses the configuration this pull read.
//
// stderr is where the conversation this pull may open says what it could not
// read, and is used for nothing else: everything the pull itself did is on the
// schedule the command reports.
func openPull(configPath string, stderr io.Writer) (orchestrator.Pull, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return orchestrator.Pull{}, err
	}
	tracker := parts.tracker()
	// The wakeup a refused tracker block earns. It is built before the pull rather
	// than inside it because the conversation store it reads the refusals from is
	// the one thing here that can refuse to be built at all, and a pull assembled
	// around a nil it could not report would be a self-correction that quietly
	// stopped existing.
	corrections, err := correctorFrom(parts, configPath, stderr)
	if err != nil {
		return orchestrator.Pull{}, err
	}
	// What the configuration schedules on a cadence, and — the same trigger —
	// what the brake summons out of it. Both are wired into the pull for the
	// same reason the delivery below is, and for one more: the harness is the
	// only thing that invokes a role, so a schedule that lived in a cron entry
	// or a launchd job would be a second invoker of one. A project that
	// schedules nothing gets neither, and its brake is decided by the cooldown's
	// probe rather than by a summons.
	recurring := recurringTrigger(parts, configPath, stderr)
	var summons orchestrator.ScheduleSummons
	if trigger, scheduled := recurring.(*orchestrator.Trigger); scheduled {
		summons = trigger
	}
	return orchestrator.Pull{
		Tracker:   tracker,
		Runs:      parts.store,
		Stoppages: parts.store,
		Decisions: parts.store.Triage(),
		Remains:   remainsOf(parts),
		Intake:    parts.intake,
		// The same pause every run and every turn reads. The pass enforces nothing
		// with it; it is what tells the pass not to attempt, on every poll of a
		// pause, a decision the pause has already stopped once.
		Holds:      parts.holds,
		Directives: parts.directives,
		// The gates a person has passed live in the harness's own store, because
		// the tracker has no way to record one: an item's closure is the only
		// completion it knows, and that is exactly what must not pass a step
		// somebody reserved for themselves.
		Gates: parts.store,
		Staleness: repositoryStaleness{
			repository: parts.repository,
			product:    parts.config.Product,
			tracker:    tracker,
		},
		Environment:                 parts.worktrees,
		Capacity:                    parts.config.Execution.MaxConcurrentDevelopers,
		Slots:                       parts.config.Execution.DeveloperSlots,
		Poll:                        parts.config.Execution.WorkPoll.Duration(),
		BlockedRunsBeforeIntakeHold: parts.config.Execution.BlockedRunsBeforeIntakeHold,
		BrakeCooldown:               parts.config.Execution.BrakeCooldown.Duration(),
		BrakeEscalationCycles:       parts.config.Execution.BrakeEscalationCycles,
		// The brake places the operator's own switch, so it is the same store
		// the hold is read from. What it releases is its own hold and never the
		// operator's: on the development manager's decision, or on a probe run
		// that lands.
		Brake:   parts.intake,
		Summons: summons,
		// What a session has spent is read from the same recorded run evidence
		// `yoyo cost` prices items from, so a bounded session and a ledger can
		// never disagree about what a run cost.
		Spend: parts.store,
		// Where a run that stopped on its reviewer reaches the development
		// manager. It is wired here rather than into the run that stopped for the
		// reason escalate.go gives: a delivery is a conversation turn, and a run
		// waiting out one would hold a developer slot open on its way out.
		Escalations: escalatorFrom(parts, configPath, stderr),
		// And where the decision she records about it is fired. It is wired beside
		// the delivery because the two are the same loop seen at its two ends: the
		// pull puts a stoppage to her, and the pull carries out what she decided
		// about it, with nobody typing a verb between them.
		CarryOut: carryOutFrom(parts),
		// The tree an item's stated prerequisites are read against, and where an
		// item that does not meet them is routed. The tree is the primary checkout
		// rather than a worktree, because what the check is about is what a run cut
		// from here would find; and it is built per pull, which is what bounds the
		// source it caches to one reading of a queue that is re-read every interval.
		Tree:      &readiness.Repository{Root: parts.repository},
		Triage:    docketerFrom(parts),
		Recurring: recurring,
		// And where a role whose tracker block the harness refused is woken to
		// re-issue it. It is wired here for the same reason the two above are: the
		// harness is the only thing that invokes a role, and the pull is where it is
		// already deciding what to do next.
		Corrections: corrections,
		// The provider answering nobody, read before anything is chosen so the
		// brake never counts a dispatch the provider turned away, and the
		// developer's provider, asked at every pull whether the login has been
		// renewed. The probe interval is the one the configuration already states
		// for asking again rather than being told when.
		Outages:     parts.outages,
		Provider:    pipelineFrom(parts).Backend,
		OutageProbe: parts.config.Execution.UsageLimitUnknownResetPause.Duration(),
		// The usage windows the harness has recorded, read at every pull against
		// every endpoint a developer's turn can end on, so a session started inside
		// a window the record already holds chooses nothing from its first poll.
		UsageLimits: parts.usageLimits,
		// A target branch the harness will not catch up to the remote's, recorded
		// by the run that met it, holds the choosing until a sweep finds the
		// branches converged.
		Divergences: parts.divergences,
		Developers:  developerEndpoints(parts.config),
		// Read against what the provider has served since and which conversations
		// are still their roles', so intake is never held on a window a served
		// turn has disproved or on a conversation nothing will speak in again.
		CapacityServed: parts.capacityServed,
		Conversations:  pullConversations(parts),
		// The audit that gives back a claim with nothing alive behind it. It is
		// wired into the pull rather than into a run for the reason the escalation
		// is, and a sharper one: the state it catches is a run that is not there, so
		// there is no run to put it in. The watch loop is the only thing that outlives
		// the process whose death made the claim dead.
		Claims: orchestrator.ClaimAuditor{
			Tracker:   tracker,
			Runs:      parts.store,
			Releases:  parts.releasedClaims,
			ProductID: parts.config.Product.ID,
			// The repository, asked what the run behind a claim left: it is what
			// keeps a claim over a surviving change standing, and what the release
			// says on the item, in the words the hold uses about the same run.
			Remains: remainsOf(parts),
		},
		// The close of a conversation-carried item whose design has landed. It is
		// wired into the pull for the reason the audit is: the landing is a
		// revision in the primary checkout's artifact homes, which no run made and
		// no run reads back, and the pull is the one process that reads both the
		// queue and the tree on every interval.
		Landings: orchestrator.ConversationLander{
			Tracker:    tracker,
			Repository: parts.repository,
			Product:    parts.config.Product,
		},
		// A run paused on work its item waits on, continued once that work has
		// closed. The tracker says what the item waits on now and takes the note
		// recording the continuation; the run store says whether a stop was
		// recorded on the run, which is honoured before any continuation.
		Continuations: pausedRunContinuations{Client: tracker, stops: parts.store},
		// How long a session that has found a build deployed over it waits out the
		// runs it hosts before it restarts anyway, with those runs stopped and
		// preserved for the session that comes back.
		RedeployDrainLimit: parts.config.Execution.RedeployDrainLimit.Duration(),
		Start: func(ctx context.Context, workItemID string, selection runstate.Selection) (orchestrator.Outcome, error) {
			// The pipeline is a value, so each run gets its own with its own
			// selection on it. Two runs started from one pull therefore record
			// separately why each of them was started.
			pipeline := pipelineFrom(parts)
			pipeline.Selection = selection
			return pipeline.Run(ctx, workItemID)
		},
	}, nil
}

// pausedRunContinuations is what a pull continues a run paused on work its item
// waits on through: the tracker for the item and its notes, and the run store
// for a stop recorded on the run.
type pausedRunContinuations struct {
	beads.Client
	stops *runstate.Store
}

func (c pausedRunContinuations) StopRequested(runID string) (runstate.StopRequest, bool, error) {
	return c.stops.StopRequested(runID)
}

// repositoryStaleness reads what changed upstream of the admitted work, from the
// same documents and the same tracker `yoyo stale` reads. It decides nothing
// here: what it produces goes into the recorded reason a run was chosen, so
// somebody reading what the harness picked can see that an item's goal had moved
// under it.
type repositoryStaleness struct {
	repository string
	product    config.Product
	tracker    beads.Client
}

func (s repositoryStaleness) Stale(ctx context.Context) ([]staleness.WorkItem, error) {
	artifacts, err := artifactStore(s.repository, s.product).Load()
	if err != nil {
		return nil, fmt.Errorf("read the recorded artifacts: %w", err)
	}
	admitted, err := admittedWorkItems(ctx, s.tracker)
	if err != nil {
		return nil, err
	}
	return staleness.Survey(artifacts, goal.Collect(s.repository, artifacts), admitted).WorkItems, nil
}

func reportSchedule(stdout, stderr io.Writer, jsonOutput bool, schedule orchestrator.Schedule, err error) int {
	if jsonOutput {
		output := scheduleOutput{Schedule: &schedule}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
	} else {
		fmt.Fprint(stdout, schedule.Render())
		if schedule.IntakeHeld != nil {
			// The remedy is named as something runnable from here. `/release` in a
			// conversation lifts the same hold, but somebody reading this at a
			// terminal with no conversation open was being told a remedy they had
			// no way to reach.
			fmt.Fprintln(stdout, "`yoyo release` lets the harness choose work again, as does /release in a conversation, and `yoyo run <id>` runs one item now regardless")
		}
		if err != nil {
			fmt.Fprintf(stderr, "scheduling stopped: %v\n", err)
		}
	}
	// A run that failed is reported as a failure, and so is a pass that could not
	// keep pulling. A declined start is neither: the work went to another
	// process, which is what two schedulers sharing one capacity look like when
	// they are working.
	if err != nil || schedule.Failed() {
		return 1
	}
	return 0
}

func printWorkUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo work [options]

Pulls ready work from the backlog and runs it, up to
execution.max_concurrent_developers at once, in a worktree of its own per run.
It returns once nothing more is ready and every run it started has ended.

Items are taken in the Lead Product Manager's order -- highest priority first --
and only where the tracker itself reports them as ready to pull, so dependencies
are the tracker's answer rather than this command's guess. An item an unresolved
directive pauses is named and skipped, and every run records why the harness
chose the item it did.

The configuration is re-read before every pull, so a change to capacity or to
the backlog's priorities takes effect at the next selection. Runs already in
flight keep the configuration they started under.

Holding intake stops it choosing anything more; runs already going carry on to
their own end. An item you name with "yoyo run" is not subject to that hold.

With --watch it does not return when the queue empties: it waits out
execution.work_poll and reads the queue again, until you stop it with Ctrl-C.
Nothing is cached between readings, so work you admit or reorder is picked up at
the next poll, and an idle session costs one tracker read per interval and no
provider call at all, unless it has a stopped run to put in front of the
development manager, a recurring task come due, a refused tracker block to
wake a role for, or its own brake to summon her over.
Holding intake brakes a watching session in place -- it keeps polling and
chooses nothing -- and "yoyo release" resumes it.

Every pull also puts stopped work in front of the development manager: a run
that ended with its independent reviewer still requiring repair after every
permitted attempt is delivered into her conversation by the pass, once, with the
docket entry it is about. It is delivered per stopped run rather than per
entry, so a run docketed both as that stoppage and as an escalation raised from
it is put to her once, with both in front of her.
One stoppage per pull, oldest first, so a backlog of
them reaches her over several polls rather than holding the queue closed while
she reads, and a stoppage she has already been granted a repair or a re-run for
is passed over whether or not that decision has been carried out yet. Her
decisions that spend nothing -- escalating to you, re-scoping, waiting -- leave
no counter to read, and what says she looked is that the decision closed the
docket entry: a settled stoppage is neither delivered again nor listed on the
docket she reads, whichever way she decided it. The same run stopping again
after she decided about it -- a repair she granted, carried out on the run that
stopped, and that run dying again on its reviewer -- is a fresh stoppage, and
the pass delivers it as it delivered the first, with the blocker it stopped on
this time; a merge she decided to wait on is back on the docket she reads once
it has been sitting there for another triage.stuck_merge_age, though the pass
delivers no publication. She
decides there and the decision is recorded against the item's triage budget
exactly as it is when somebody brings her a stoppage by hand. A turn that may have reached her and then failed is made again a
quarter of an hour later, three times in all, and
then left for a person. One that provably reached her with nothing -- her
conversation could not be opened, the provider had no capacity -- keeps its
attempt and is tried again every quarter of an hour until it gets through, since
nothing was said to her and every reason for it clears. A pause covers a delivery
like any other provider call and --budget counts what it spent; holding intake
does not stop it, because the judgment a held queue is waiting on is what the
delivery produces. What it did, and anything still waiting on a person, is on the
pass.

Every pull also carries out the decisions she recorded. A repair or a re-run
she settled a stoppage with is fired by the pass itself, oldest stoppage first,
as many per pull as there are developer slots free for them and --limit leaves,
each taking a developer slot exactly as a pulled item does -- so
recording a decision is what causes it and nobody types a verb. "yoyo triage
repair" and "yoyo triage rerun" still work and are what fires one now rather than
at the next pass. Every gate those verbs ask refuses this the same way: your pause,
your intake hold, the item's own triage budgets, developer capacity, and the
preserved worktree being what a continued developer could be handed back. Nothing
is spent by a refusal, and every refusal is written onto the item's triage record
and onto the docket entry she reads, naming the gate and what would clear it -- so
a decision that cannot be carried out says so where she is looking rather than
sitting silently. A recorded re-arm is carried out by the pass too, for a merge
the forge dropped and for one nothing ever asked it to make: it is one merge
request rather than a run, so it takes no developer slot, and "yoyo triage
rearm" is what fires one now. A gate that stops one item is retried at a paced interval
rather than every poll; one that stops everything at once -- your pause, your
intake hold, a full harness -- is attempted once while it stands, so the docket
says so, and again on the first pull after it opens. A decision no pull has
attempted a poll interval after it was recorded is written onto the item and
her docket entry as unattempted, with why, and "yoyo status" counts those
beside the refused ones, so no decision is ever silently passed over.

Every pull also wakes a role whose block of tracker actions the harness refused.
A block it cannot read is refused whole, so nothing in it happens and the queue
is not what the role thinks it is; the refusal has always opened that role's next
turn in the harness's own words, and now the pass starts that turn instead of it
waiting on somebody opening the conversation. One wakeup per refusal and at most
one per pull, oldest refusal first, and what the role does with it is re-issue
the actions itself. A block refused again on the woken turn goes to you rather
than earning a second wakeup, because another copy of the same message is not
going to help; so does a second block refused with the first still unanswered
whether or not a wakeup was ever made, and so does a woken turn that answers
without asking for any tracker action at all. A turn the provider refused for
want of capacity put nothing in front of the role, so the turn is given back and
made again a quarter of an hour later, three times in all before the harness
stops -- a window cannot silently spend the one turn a refusal is owed, and the
attempt is kept each time so that bound is one it actually reaches. A
conversation nothing can open keeps its turn spent, since what it waits on is
somebody changing something, and the refusal falls back to what it had before:
the role's own next turn, whenever one happens.
A pause covers it like any other provider call and --budget
counts what it spent; holding intake does not stop it, because it chooses no work
and starts no run.

Every pull also audits the claims the tracker holds against the runs the harness
actually has. An item the tracker calls in progress with no run alive behind it
for half an hour is given back to the queue, with the reason on its notes, and
the record of the run that left it is ended so the developer slot it was filling
comes back with it -- a killed process leaves its item claimed and its slot taken
forever otherwise, and a claimed item has left the ready queue, so a machine
stuck behind one looks exactly like a drained one. Both halves happen before the
intake hold and the machine's own capacity are even consulted, because a held or
full session is exactly where a dead claim hides and neither pass gets as far as
reading the queue.

Alive means the run's own record still moving rather than its status saying it is
in flight, and it is settled under that run's lease, which a live process holds
and the operating system drops when it dies. A run that is owed a continuation
keeps its claim however quiet it has gone -- one waiting out a provider, one
parked by "yoyo pause", one held up by a directive or by work it depends on --
and so does a claim with no recorded run behind it, which is somebody else's
rather than the harness's to take back. The ended run is recorded as cancelled
rather than failed: nothing about its change was judged, and the branch and
worktree it left are untouched. Each release is on the pass and sent to the
operators once.

Only one session watches a product at a time. A second one is refused as it
starts, naming the session that holds the watch and the process running it,
because two sessions read one queue and can both choose an item before either
has reserved a run for it. The watch is held for as long as the session runs and
is let go however it ends, including when it stops to restart into a build
deployed over it -- so the session that comes back takes it up again. A drain
takes nothing and is refused nothing: what this refuses is a second session that
stays open, which is the shape that ran on 2026-09-05.

A watching session guards itself three ways. It does not start the same item
twice unless the item has changed -- what it says, what it is for, its priority,
its status, what it depends on, its notes -- so a start the harness cannot get
past is not retried every interval, and a blocker you release is picked up
because releasing it changed the item. Runs blocking one after another with
nothing landing between them hold intake at
execution.blocked_runs_before_intake_hold -- verdicts and check failures on
changes that were present; a stop the environment made counts toward nothing --
and the same poll summons the development manager's sweep ahead of its schedule
with the blocked runs and the reason each blocked in front of her. She decides
what happens to the hold: release it, keep it and probe the line with one run,
or escalate it to you. Where she records nothing by execution.brake_cooldown,
the session probes by itself: one run started under the hold, whose landing
reopens intake and whose blocking keeps it held and summons her again. That
loop is bounded by execution.brake_escalation_cycles: after that many probes
have blocked with her not escalating the hold, the session escalates it to
you itself, and no further probe starts. So the only brake hold that waits
on a person is one she escalated or one the harness escalated at that bound;
"yoyo release" lifts any of them sooner. The hold's own record says who is
deciding it, where the loop stands, and what the harness does next, and
"yoyo status" reads it back.
And what the session is doing -- watching, idle, braked, resumed, stopped -- is
recorded where "yoyo status" and the Slack sink read it, because an idle session
and a dead one are otherwise the same silence.

A reading of the harness that fails does not end a watching session. The tracker
is a store a reconcile and every settling run write to, so a reading that fails
is usually contention: the session waits and reads it again, two seconds doubling
to thirty, and stops only once the readings have gone on failing for five
minutes, saying how long it tried. What it rode through is reported on the pass
and said in the watch log while it happens. A drain stops on the first one, and
so does either kind of pass on a pull that assembles and is unusable -- a
capacity of zero, a --budget with nothing to price it -- which is a decision
about the configuration rather than a reading that failed.

A watching session also takes up a build deployed over it. When the yoyo it is
running is written over -- installed, rebuilt -- it drains: it restarts into
what was deployed the moment it hosts no run, and until then it carries on
polling, pulling into free seats, and firing its recurring tasks, because the
drain is about the runs it hosts and not about its other duties. The drain is
bounded by execution.redeploy_drain_limit (fifteen minutes by default): past
it the session restarts anyway, and at once, and each run it still hosts is
stopped where it is and preserved -- worktree, branch, claim, developer session, every counter --
for the session that comes back to re-adopt at its first pull, ahead of anything
new. A run at its promotion is the one exception and is waited out. A pull the
session declines because the bound is less than a poll away says so, and the
drain and its bound are on every line the session writes while it lasts. Either
way the session restarts into what was deployed, and that stop is recorded as a
restart rather than as an ending, so
"yoyo status" and the Slack sink say a session is coming back rather than telling
you to start one. The queue is re-read from scratch on the way back in, exactly as it
is at every poll, and the bounds you gave the session cross the restart reduced
to what is left of them -- --budget less what it has spent, --limit less what it
has started -- so a deploy never hands a bounded session its cap back. A session
that has reached either bound stops on it rather than restarting, and a restart
that does not happen -- a bound with nothing left of it, a re-execution the
operating system refuses -- is recorded as the ending it turned out to be, so
neither surface is left saying a stopped session is on its way back. A platform
that cannot replace a running process says so when the session opens, and that
session watches without this and is restarted by hand for a deploy.

--budget fails closed, and it bounds everything the session spends: the runs it
starts, the turns it takes putting stopped work to the development manager, the
turns a recurring task spends, the turns a summoned sweep spends, and the turn
it spends waking a role to re-issue a refused tracker block. A
pass with no way to price itself is refused before anything starts, and a session
that meets a run whose recorded evidence will not price stops and says which run
it was rather than counting it as free and carrying on inside a bound it can no
longer hold.

A watching session also notices that the harness has stopped doing anything. On
every pull, at most once per --stall-after, it reads whether anything has started
at all: nothing for that long, work the tracker calls ready, and no hold, full
machine, still-moving run or provider usage window to account for it is recorded
against the product as a stall, which "yoyo status" reads back and the Slack sink,
where one is running, takes to the operators once. This is the loop that catches a
session which is alive and has stopped choosing; a session that died writes
nothing at all, and "yoyo reconcile" is what catches that. Nothing on the path
asks a provider anything, and noticing is all it does -- it restarts nothing. The
reading costs one tracker read per --stall-after, and none at all while something
else already accounts for the quiet.

Options:
  --config <path>    configuration file (default: the nearest .yoyodyne/config.yaml)
  --limit <n>        stop after starting this many runs (default: no bound)
  --watch            keep pulling work as it becomes ready, until stopped
  --until-drained    return once nothing more is ready to pull (the default)
  --budget <usd>     stop once this session has spent this much (default: unbounded)
  --stall-after <d>  how long nothing may start over ready work, with nothing
                     accounting for it, before a watching session records that
                     the harness has stopped (default 10m). It is the same number
                     "yoyo reconcile --stall-after" takes.
  --json             emit machine-readable JSON`)
}
