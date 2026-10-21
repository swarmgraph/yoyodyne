package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The hold the scheduler is actually subject to. `yoyo work` is the harness
// choosing, so a held intake stops it before it reads the tracker at all --
// nothing is claimed, nothing is developed, and the operator is told the two
// ways out.
func TestWorkStartsNothingWhileIntakeIsHeld(t *testing.T) {
	// Not parallel: the state root the command addresses is set here, and the
	// hold it reads is written under it.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	intake, err := runstate.NewIntakeHoldStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	if _, err := intake.Hold(runstate.IntakeHolderOperator, "the queue needs reordering first", time.Now()); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	stdout, stderr, code := runCLI(t, "work", "--config", configPath)
	if code != 0 {
		t.Fatalf("work code = %d, stderr = %q", code, stderr)
	}
	// The remedy is named as something runnable from this terminal: `/release`
	// is said beside it rather than instead of it, because whoever is reading
	// this may have no conversation open.
	for _, want := range []string{"nothing was started", "intake is held", "the operator placed it — the queue needs reordering first", "yoyo release", "/release", "yoyo run"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("work stdout = %q, want it to mention %q", stdout, want)
		}
	}

	stdout, stderr, code = runCLI(t, "work", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("work --json code = %d, stderr = %q", code, stderr)
	}
	var output struct {
		Schedule orchestrator.Schedule `json:"schedule"`
		Error    string                `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", stdout, err)
	}
	if output.Error != "" {
		t.Fatalf("error = %q, want a held intake reported as a schedule rather than a failure", output.Error)
	}
	if len(output.Schedule.Started) != 0 || output.Schedule.IntakeHeld == nil {
		t.Fatalf("schedule = %#v, want nothing started and the hold named", output.Schedule)
	}
	if output.Schedule.Stopped != orchestrator.ScheduleIntakeHeld {
		t.Fatalf("stopped = %q, want the hold to be why the choosing stopped", output.Schedule.Stopped)
	}
}

// The verb takes no item: naming one is `yoyo run`, and the difference between
// the two is exactly the hold above.
func TestWorkRefusesArgumentsItCannotActOn(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	if _, stderr, code := runCLI(t, "work", "--config", configPath, "yoyodyne-1"); code != 2 {
		t.Fatalf("work with an item code = %d, stderr = %q, want a usage refusal", code, stderr)
	}
	if _, stderr, code := runCLI(t, "work", "--config", configPath, "--limit", "-1"); code != 2 {
		t.Fatalf("work with a negative limit code = %d, stderr = %q, want a usage refusal", code, stderr)
	}
	if _, stderr, code := runCLI(t, "work", "--config", configPath, "--budget", "-1"); code != 2 {
		t.Fatalf("work with a negative budget code = %d, stderr = %q, want a usage refusal", code, stderr)
	}
	// Staying open and returning when the queue empties are opposite requests,
	// and guessing which was meant is how a session runs all night that was
	// meant to return.
	_, stderr, code := runCLI(t, "work", "--config", configPath, "--watch", "--until-drained")
	if code != 2 {
		t.Fatalf("work with both --watch and --until-drained code = %d, stderr = %q, want a usage refusal", code, stderr)
	}
	if !strings.Contains(stderr, "opposite things") {
		t.Fatalf("stderr = %q, want the refusal to say why the two cannot both be asked for", stderr)
	}
}

// Watching is what the loop is for, and the default is deliberately still the
// drain: the flip is a decision somebody makes rather than a side effect of this
// landing. Both are stated in the usage so neither is inferred from behaviour.
func TestWorkUsageSaysWhatWatchingIsAndThatDrainingIsTheDefault(t *testing.T) {
	t.Parallel()

	var usage strings.Builder
	printWorkUsage(&usage)
	for _, want := range []string{
		"--watch",
		"--until-drained",
		"(the default)",
		"execution.work_poll",
		"execution.blocked_runs_before_intake_hold",
		"Holding intake brakes a watching session in place",
		// The brake no longer waits on a person, and the usage has to say what it
		// does instead: an operator reading "stays held until yoyo release lifts
		// it" would go and lift a hold the harness was already deciding.
		"execution.brake_cooldown",
		// And the loop the cooldown makes has a bound, which the usage has to
		// name: an operator reading "summons her again" would otherwise expect
		// the loop to go round until she stops it, which is what it used to do.
		"execution.brake_escalation_cycles",
		// Escalated, the hold is the Lead Product Manager's and never the
		// operator's: the brake never summons him (operating rule 789c052c).
		"escalates it to\nthe Lead Product Manager itself, you are told",
		"a brake hold never waits on you: an escalated one is the Lead Product\nManager's to decide",
		"--budget",
		// A bound that can stop the session has to say so where the flag is
		// documented: an operator who reads "caps what one session spends" and
		// meets a stop instead is reading the wrong promise.
		"--budget fails closed",
		// A session that restarts itself is a process ending and beginning under
		// somebody's terminal, which is exactly the behaviour an operator should
		// not have to discover by watching it happen.
		"also takes up a build deployed over it",
		"restarts into what was deployed",
		// And that the wait on its runs is bounded, by what, and what becomes of
		// the runs the bound stops: a two-hour wait on one run's checks is what
		// the bound exists to end, and an operator reading "waits out every run"
		// would be reading the old promise.
		"execution.redeploy_drain_limit",
		"re-adopt at its first pull",
		// And what a deploy does to a bound the operator set, which is the half
		// somebody reading "caps what one session spends" would otherwise have to
		// find out from a bill.
		"never hands a bounded session its cap back",
	} {
		if !strings.Contains(usage.String(), want) {
			t.Fatalf("work usage = %q, want it to say %q", usage.String(), want)
		}
	}
}

// What a bounded session hands to the build it restarts into. The bound is the
// operator's and a deploy is not them raising it, so what crosses is the
// remainder: a session $45 into $50 comes back with $4.99 left rather than with
// $50 again, and the same for a count of runs.
func TestARestartCarriesWhatIsLeftOfTheBoundsRatherThanTheWholeOfThem(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		args    []string
		budget  float64
		spent   float64
		limit   int
		started int
		want    []string
	}{
		"a budget and a limit given as separate arguments": {
			args:    []string{"/usr/local/bin/yoyo", "work", "--watch", "--budget", "50", "--limit", "10"},
			budget:  50,
			spent:   45.01,
			limit:   10,
			started: 6,
			want:    []string{"/usr/local/bin/yoyo", "work", "--watch", "--budget", "4.99", "--limit", "4"},
		},
		"the same bounds written with equals signs and one dash": {
			args:    []string{"./bin/yoyo", "work", "--watch", "--budget=50", "-limit=10"},
			budget:  50,
			spent:   12.5,
			limit:   10,
			started: 1,
			want:    []string{"./bin/yoyo", "work", "--watch", "--budget=37.50", "-limit=9"},
		},
		"an unbounded session, which has nothing to reduce": {
			args: []string{"./bin/yoyo", "work", "--watch", "--json"},
			want: []string{"./bin/yoyo", "work", "--watch", "--json"},
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := continued(test.args, test.budget, test.spent, test.limit, test.started)
			if strings.Join(got, " ") != strings.Join(test.want, " ") {
				t.Fatalf("continued() = %v, want %v", got, test.want)
			}
		})
	}
}

// Neither bound is ever written into a restart as zero, because zero is how both
// of these flags ask for no bound at all -- a cap that came back as its own
// absence. The scheduler stops a session on the bound it reached before it can
// redeploy, and this is the guard that says so out loud rather than assuming it.
func TestARestartIsRefusedWhereABoundHasNothingLeftOfIt(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		budget  float64
		spent   float64
		limit   int
		started int
		want    string
	}{
		"something left of both bounds":     {budget: 50, spent: 45, limit: 10, started: 6, want: ""},
		"no bounds at all":                  {want: ""},
		"the budget exactly spent":          {budget: 50, spent: 50, want: "budget"},
		"more spent than the budget allows": {budget: 50, spent: 51.20, want: "budget"},
		"the last permitted run started":    {limit: 4, started: 4, want: "runs"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := exhausted(test.budget, test.spent, test.limit, test.started)
			if test.want == "" && got != "" {
				t.Fatalf("exhausted() = %q, want a session that may still be restarted", got)
			}
			if test.want != "" && !strings.Contains(got, test.want) {
				t.Fatalf("exhausted() = %q, want the %s bound named as the one with nothing left of it", got, test.want)
			}
		})
	}
}

// The remainder is written exactly rather than rounded to cents, because a
// remainder under a cent rounded to "0.00" is how an unbounded session is asked
// for -- a bound that quietly became no bound at all.
func TestARemainderUnderACentIsStillABound(t *testing.T) {
	t.Parallel()

	got := continued([]string{"yoyo", "work", "--watch", "--budget", "50"}, 50, 49.999, 0, 0)
	if got[4] == "0.00" || got[4] == "0" {
		t.Fatalf("continued() left the budget as %q, which asks for an unbounded session", got[4])
	}
	if _, err := strconv.ParseFloat(got[4], 64); err != nil {
		t.Fatalf("budget %q does not read back as a number: %v", got[4], err)
	}
}

// A restart that does not happen leaves nothing in the log saying the session is
// coming back.
//
// The stop is marked as a restart before the restart is known to happen, and it
// has to be: a restart that works never returns to write anything, so the moment
// the session stops is the only moment there is. Both branches that then decline
// to re-execute -- a bound with nothing left of it, and a re-execution the
// operating system refuses -- correct that claim in the same log, rather than
// exiting on a line of stderr that only whoever is at the terminal ever reads.
func TestARestartThatDoesNotHappenLeavesNoRecordSayingTheSessionIsComingBack(t *testing.T) {
	// Not parallel: the state root the log is written under is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	sessionID := newWatchSessionID(t)
	sessions, err := openWatchSession(configPath, sessionID)
	if err != nil {
		t.Fatalf("openWatchSession() error = %v", err)
	}
	// The scheduler's own last line, exactly as it writes it: a stop that says the
	// session is on its way back.
	if err := sessions.Record(orchestrator.SessionState{
		State:      runstate.WatchStopped,
		At:         time.Now().UTC(),
		Reason:     orchestrator.ScheduleRedeployed,
		Restarting: true,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	// The operating system refusing the re-execution, which is the case the
	// redeploy package's own tests reach for real: a replaced image that is not
	// executable, a path that went away between the stat and the exec.
	refused := &refusedRestart{args: []string{"/usr/local/bin/yoyo", "work", "--watch"}}
	var said strings.Builder
	watching := heldWatch(t, configPath, sessionID)
	if code := takeUpTheDeploy(context.Background(), refused, sessions, watching, &said, 0, 0, 0, 0, 0); code != 1 {
		t.Fatalf("takeUpTheDeploy() = %d, want the failed restart to fail the command", code)
	}
	// The watch goes with the session whichever way this ends. A restart that
	// worked would have the build it became take the same lease, and this one --
	// the fallback exit, where the re-execution was refused -- leaves it free for
	// whoever starts the next session.
	watchIsFree(t, configPath)
	if !strings.Contains(said.String(), "could not") {
		t.Fatalf("stderr = %q, want whoever is at the terminal told the restart did not happen", said.String())
	}

	watch, err := runstate.NewWatchStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	latest, found, err := watch.Latest()
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if !found {
		t.Fatalf("Latest() found nothing, want the correction the command recorded")
	}
	if latest.State != runstate.WatchStopped || latest.Restarting {
		t.Fatalf("latest = %#v, want a stop that is an ending rather than one claiming a restart", latest)
	}
	if !strings.Contains(latest.Reason, "could not") {
		t.Fatalf("reason = %q, want it to say why the restart did not happen", latest.Reason)
	}
	// The claim it corrects stays where it was. The log is append-only and read
	// forward, so what a surface gets is the line saying the session is coming
	// back and then the line saying it is not -- rather than a rewritten history
	// in which the claim was never made.
	recorded, err := watch.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 || !recorded[0].Restarting {
		t.Fatalf("recorded = %#v, want the restart claim kept and the ending appended after it", recorded)
	}

	// The other refusal: a bound with nothing left of it, which declines the
	// restart before the re-execution is attempted at all. It leaves the same
	// correction behind, and the command exits on the schedule's own code rather
	// than on a failure, because nothing failed.
	unspent := &refusedRestart{args: []string{"/usr/local/bin/yoyo", "work", "--watch", "--budget", "50"}}
	said.Reset()
	if code := takeUpTheDeploy(context.Background(), unspent, sessions, heldWatch(t, configPath, sessionID), &said, 0, 50, 50, 0, 0); code != 0 {
		t.Fatalf("takeUpTheDeploy() = %d, want a session stopped on its bound rather than a failure", code)
	}
	watchIsFree(t, configPath)
	if unspent.attempts != 0 {
		t.Fatalf("Take() was called %d time(s), want a bound that is gone to refuse the restart before it", unspent.attempts)
	}
	latest, _, err = watch.Latest()
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if latest.Restarting || !strings.Contains(latest.Reason, "budget") {
		t.Fatalf("latest = %#v, want an ending naming the bound that refused the restart", latest)
	}
}

// The wedge-recovery shape: a second watching session started while the first is
// still alive. Two of them read one queue and choose from it independently, and
// a run is not in durable state until it reserves, so both can pick the same
// item in that window -- which is how two watches came to be running against one
// product on 2026-09-05. The second is refused as it starts, and told which
// session has the watch rather than only that somebody does.
func TestASecondWatchingSessionIsRefusedWhileTheFirstIsAlive(t *testing.T) {
	// Not parallel: the state root the watch is taken under is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	// The session that is already watching, holding what the command holds for as
	// long as it runs.
	sessionID := newWatchSessionID(t)
	watching := heldWatch(t, configPath, sessionID)

	_, stderr, code := runCLI(t, "work", "--config", configPath, "--watch")
	if code != 1 {
		t.Fatalf("second work --watch code = %d, stderr = %q, want it refused", code, stderr)
	}
	for _, want := range []string{
		"already watching this product",
		// Named, because a refusal an operator cannot act on sends them looking
		// for a process by hand -- which on a machine running several products is
		// how the wrong session gets stopped.
		sessionID,
		fmt.Sprintf("process %d", os.Getpid()),
		"one session per product",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want it to say %q", stderr, want)
		}
	}
	// A session that was refused says nothing about itself: it never watched, and
	// a log entry from it would be a session `yoyo status` reports as running.
	watch, err := runstate.NewWatchStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	recorded, err := watch.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 0 {
		t.Fatalf("watch log = %#v, want nothing from a session that was refused", recorded)
	}

	// The first session ends, and the next one is admitted -- the recovery this
	// must not stand in the way of.
	if err := watching.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	watchIsFree(t, configPath)
}

// A drain takes no watch, so an operator's own pass is not refused by a session
// that is watching, and a drain leaves nothing behind that would refuse one.
func TestADrainNeitherTakesTheWatchNorIsRefusedByIt(t *testing.T) {
	// Not parallel: the state root the watch is taken under is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	intake, err := runstate.NewIntakeHoldStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	// Held intake keeps this pass off the tracker, which is not what is being
	// tested and is not available here.
	if _, err := intake.Hold(runstate.IntakeHolderOperator, "the queue needs reordering first", time.Now()); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	watching := heldWatch(t, configPath, newWatchSessionID(t))
	if _, stderr, code := runCLI(t, "work", "--config", configPath); code != 0 {
		t.Fatalf("work code = %d, stderr = %q, want a drain unaffected by a session watching", code, stderr)
	}
	if err := watching.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, stderr, code := runCLI(t, "work", "--config", configPath); code != 0 {
		t.Fatalf("work code = %d, stderr = %q", code, stderr)
	}
	watchIsFree(t, configPath)
}

// newWatchSessionID names a session the way the command does.
func newWatchSessionID(t *testing.T) string {
	t.Helper()
	sessionID, err := runstate.NewWatchSessionID()
	if err != nil {
		t.Fatalf("NewWatchSessionID() error = %v", err)
	}
	return sessionID
}

// heldWatch is a session holding the product's watch, taken exactly as the
// command takes it.
func heldWatch(t *testing.T, configPath, sessionID string) *runstate.Lease {
	t.Helper()
	lease, err := holdTheWatch(configPath, sessionID)
	if err != nil {
		t.Fatalf("holdTheWatch() error = %v", err)
	}
	t.Cleanup(func() { lease.Release() })
	return lease
}

// watchIsFree asserts that nobody holds the product's watch, by taking it. The
// lock is advisory and per open file description, so this is the same question
// the next session asks and gets the same answer.
func watchIsFree(t *testing.T, configPath string) {
	t.Helper()
	lease, err := holdTheWatch(configPath, newWatchSessionID(t))
	if err != nil {
		t.Fatalf("the watch was not let go of: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// refusedRestart is a deployed binary whose re-execution the operating system
// will not make. It is the only shape of this a test can observe: a Take that
// works never returns.
type refusedRestart struct {
	args     []string
	attempts int
}

func (r *refusedRestart) Args() []string { return append([]string(nil), r.args...) }

func (r *refusedRestart) Take(args []string) error {
	r.attempts++
	return fmt.Errorf("re-execute %s: permission denied", args[0])
}

// wedgedRestart is the re-execution that neither happens nor fails: the shape a
// session met on 2026-09-05, where the takeover was asked for, the log said the
// session was coming back, and nothing else ever happened.
//
// held is closed when the re-execution is asked for, so a test can tell a
// takeover that was never attempted from one waiting on a call that is not
// coming back. released is the test's own end, so the imitation does not outlive
// it.
type wedgedRestart struct {
	args     []string
	held     chan struct{}
	released chan struct{}
}

func newWedgedRestart(t *testing.T) *wedgedRestart {
	t.Helper()
	wedged := &wedgedRestart{
		args:     []string{"/usr/local/bin/yoyo", "work", "--watch"},
		held:     make(chan struct{}),
		released: make(chan struct{}),
	}
	t.Cleanup(func() { close(wedged.released) })
	return wedged
}

func (w *wedgedRestart) Args() []string { return append([]string(nil), w.args...) }

func (w *wedgedRestart) Take([]string) error {
	close(w.held)
	<-w.released
	return errors.New("the re-execution was released by the test rather than by the operating system")
}

// A takeover that is not going to happen is given up on rather than waited out.
//
// The session this is modelled on stayed alive for an hour after it had stopped
// choosing: the queue was held by a process doing nothing, no run could start,
// and nothing short of SIGKILL ended it. What the deadline buys is the ordinary
// ending instead -- the session exits, and whatever started it starts the next
// one from the build that was deployed, which is where the takeover was going.
func TestATakeoverThatDoesNotHappenIsGivenUpOnWithinItsDeadline(t *testing.T) {
	t.Parallel()

	wedged := newWedgedRestart(t)
	failed := make(chan error, 1)
	// A deadline short enough to sit through. What the command uses is a minute,
	// which bounds a wedge rather than something a test waits out.
	go func() { failed <- takeWithin(context.Background(), wedged, wedged.Args(), 50*time.Millisecond) }()

	<-wedged.held
	// Waited for and not bounded: a takeover still waiting on a re-execution
	// that is not coming is the wedge itself, and it is reported by the binary's
	// own timeout naming this goroutine rather than by a clock here.
	err := <-failed
	if err == nil {
		t.Fatal("takeWithin() reported nothing about a re-execution that never happened")
	}
	if !strings.Contains(err.Error(), "had not happened") {
		t.Fatalf("takeWithin() = %v, want it to say the re-execution did not happen in the time it was given", err)
	}
}

// A session asked to stop while it is waiting to become the new build stops.
//
// This is the other half of the same wedge: the takeover runs after the loop
// that was watching the cancellation has returned, so a signal arriving during
// it has nothing watching for it unless the takeover watches for it itself.
func TestASignalDuringTheTakeoverEndsTheSession(t *testing.T) {
	t.Parallel()

	wedged := newWedgedRestart(t)
	ctx, cancel := context.WithCancel(context.Background())
	var said strings.Builder
	done := make(chan int, 1)
	// No watch log and no watch lease: neither is what this exercises, and both
	// tolerate their absence the way the command's own drain path does.
	go func() { done <- takeUpTheDeploy(ctx, wedged, nil, nil, &said, 0, 0, 0, 0, 0) }()

	// Asked for the re-execution and waiting on it, which is where the signal has
	// to land.
	<-wedged.held
	cancel()
	if code := <-done; code != 1 {
		t.Fatalf("takeUpTheDeploy() = %d, want a takeover the operator stopped to fail the command", code)
	}
	if !strings.Contains(said.String(), "asked to stop") {
		t.Fatalf("stderr = %q, want it to say the session was stopped rather than that the re-execution failed on its own", said.String())
	}
}

// The session's account of itself lands in the product's own watch log, under
// an identifier nothing else is using. It is the seam between a scheduler that
// knows nothing about products and a record that is keyed by one.
func TestAWatchSessionWritesIntoTheProductsOwnWatchLog(t *testing.T) {
	// Not parallel: the state root the log is written under is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	sessions, err := openWatchSession(configPath, newWatchSessionID(t))
	if err != nil {
		t.Fatalf("openWatchSession() error = %v", err)
	}
	at := time.Now().UTC()
	if err := sessions.Record(orchestrator.SessionState{State: runstate.WatchWatching, At: at, Reason: "watching the backlog until stopped"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	watch, err := runstate.NewWatchStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	recorded, err := watch.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the one transition the session recorded", recorded)
	}
	if recorded[0].State != runstate.WatchWatching || recorded[0].ProductID != "yoyodyne" {
		t.Fatalf("transition = %#v, want this product's session recorded as watching", recorded[0])
	}
	if recorded[0].Reason != "watching the backlog until stopped" {
		t.Fatalf("reason = %q, want what the session said about itself", recorded[0].Reason)
	}
	// A second session is a second identity in the same log, so two of them
	// interleaved -- one after the other, since only one watches at a time -- can
	// still be read apart.
	other, err := openWatchSession(configPath, newWatchSessionID(t))
	if err != nil {
		t.Fatalf("openWatchSession() error = %v", err)
	}
	if err := other.Record(orchestrator.SessionState{State: runstate.WatchIdle, At: at, Reason: "the backlog is empty"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	recorded, err = watch.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 || recorded[0].SessionID == recorded[1].SessionID {
		t.Fatalf("recorded = %#v, want two sessions told apart in one log", recorded)
	}
}

// A watching session says what it is doing where somebody who is not at the
// terminal can read it, and `yoyo status` is one of the two places that read it.
// This drives the record and the surface rather than the loop: what the loop
// does with an empty queue is the scheduler's own test.
func TestStatusReportsWhereTheSessionChoosingWorkGotTo(t *testing.T) {
	// Not parallel: the state root the commands address is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	// A product nobody has watched says nothing about a session, rather than
	// asserting that nothing is running.
	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "session choosing work") {
		t.Fatalf("status stdout = %q, want nothing claimed about a session nobody has run", stdout)
	}

	watch, err := runstate.NewWatchStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	sessionID, err := runstate.NewWatchSessionID()
	if err != nil {
		t.Fatalf("NewWatchSessionID() error = %v", err)
	}
	if err := watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     sessionID,
		State:         runstate.WatchIdle,
		At:            time.Now().UTC(),
		Reason:        "the backlog is empty",
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	stdout, stderr, code = runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"the session choosing work is idle", "the backlog is empty"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status stdout = %q, want it to say %q", stdout, want)
		}
	}

	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("status --json code = %d, stderr = %q", code, stderr)
	}
	var output struct {
		Watch *runstate.WatchTransition `json:"watch"`
	}
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", stdout, err)
	}
	if output.Watch == nil || output.Watch.State != runstate.WatchIdle {
		t.Fatalf("watch = %#v, want the session's state carried machine-readably too", output.Watch)
	}
}

// `yoyo status` says the one stop that is not an ending as itself. Both of the
// two places that read the watch log have to, and this is the other one: a
// reader told only that the session is "stopped" goes looking for somebody to
// start it, which is precisely the move the session took off them by restarting
// itself.
func TestStatusSaysAStopThatIsARestartIsNotAnEnding(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	watch, err := runstate.NewWatchStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	sessionID, err := runstate.NewWatchSessionID()
	if err != nil {
		t.Fatalf("NewWatchSessionID() error = %v", err)
	}
	at := time.Now().UTC()
	record := func(restarting bool, reason string, when time.Time) {
		t.Helper()
		if err := watch.Record(runstate.WatchTransition{
			SchemaVersion: runstate.WatchSchemaVersion,
			ProductID:     "yoyodyne",
			SessionID:     sessionID,
			State:         runstate.WatchStopped,
			At:            when,
			Reason:        reason,
			Restarting:    restarting,
		}); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}

	record(true, orchestrator.ScheduleRedeployed, at)
	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "stopped to restart into the build deployed over it") {
		t.Fatalf("status stdout = %q, want the stop said as a session coming back", stdout)
	}

	// The same log with an ordinary ending on the end of it reads as one. Without
	// this the line above would pass on a command that called every stop a
	// restart, which is the same false reassurance in the other direction.
	record(false, "the operator stopped the session", at.Add(time.Minute))
	stdout, stderr, code = runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "stopped to restart") {
		t.Fatalf("status stdout = %q, want an ending said as an ending", stdout)
	}
	if !strings.Contains(stdout, "the session choosing work is stopped") {
		t.Fatalf("status stdout = %q, want the session reported as stopped", stdout)
	}
}

// An idle poll that could not read the store is not a session idling over an
// empty queue, and status says which: a reader told the session is idle through
// a store outage goes looking for work to admit.
func TestStatusSaysAFailedReadBeingRetriedRatherThanIdle(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	watch, err := runstate.NewWatchStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	sessionID, err := runstate.NewWatchSessionID()
	if err != nil {
		t.Fatalf("NewWatchSessionID() error = %v", err)
	}
	at := time.Now().UTC()
	record := func(unreadable bool, reason string, when time.Time) {
		t.Helper()
		if err := watch.Record(runstate.WatchTransition{
			SchemaVersion: runstate.WatchSchemaVersion,
			ProductID:     "yoyodyne",
			SessionID:     sessionID,
			State:         runstate.WatchIdle,
			At:            when,
			Reason:        reason,
			Unreadable:    unreadable,
		}); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}

	record(true, "the harness could not be read and is being read again", at)
	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "the session choosing work is retrying a failed read of the harness's store") {
		t.Fatalf("status stdout = %q, want the failing read said as one being retried", stdout)
	}

	record(false, "the backlog is empty", at.Add(time.Minute))
	stdout, stderr, code = runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "the session choosing work is idle") {
		t.Fatalf("status stdout = %q, want a read that succeeded and found nothing said as idle", stdout)
	}
}

// A session draining to restart says so on `yoyo status` with its bound, on
// whatever line it wrote last: a reader is told what stops the wait rather than
// left to time it, which on 2026-09-19 was two hours nobody could account for.
func TestStatusNamesTheDrainAndItsBoundWhileASessionDrains(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	watch, err := runstate.NewWatchStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	sessionID, err := runstate.NewWatchSessionID()
	if err != nil {
		t.Fatalf("NewWatchSessionID() error = %v", err)
	}
	since := time.Date(2026, 9, 19, 7, 35, 0, 0, time.UTC)
	if err := watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     sessionID,
		State:         runstate.WatchIdle,
		At:            since.Add(time.Minute),
		Reason:        "1 run in flight; nothing more is ready to pull",
		Running:       1,
		Draining:      &runstate.WatchDrain{Since: since, BoundSeconds: 900, Until: since.Add(15 * time.Minute), Hosting: 1},
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"the session choosing work is idle",
		"draining to restart into the build deployed over it since 2026-09-19T07:35:00Z",
		"bounded at 15m0s",
		"until 2026-09-19T07:50:00Z",
		"waiting out 1 run(s) it hosts while still pulling into free seats and firing its recurring tasks",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status stdout = %q, want %q in it", stdout, want)
		}
	}
}

// The answer to "when is a capacity change picked up?" is documented where an
// operator looks for it. It is a decision rather than an accident, so the usage
// states it rather than leaving it to be inferred from behavior.
func TestWorkUsageSaysWhenAConfigurationChangeTakesEffect(t *testing.T) {
	t.Parallel()

	var usage strings.Builder
	printWorkUsage(&usage)
	for _, want := range []string{
		"re-read before every pull",
		"takes effect at the next selection",
		"keep the configuration they started under",
		"max_concurrent_developers",
	} {
		if !strings.Contains(usage.String(), want) {
			t.Fatalf("work usage = %q, want it to say %q", usage.String(), want)
		}
	}
}
