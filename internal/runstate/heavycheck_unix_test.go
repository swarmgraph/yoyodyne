//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// These tests give places out and start real processes in process groups of
// their own, holding their permit's file the way a check started behind its
// gate does. A harness that dies is a process whose locks the kernel drops, so
// one is stood in for here by closing the files it held and never recording
// what it would have: that is all a dying harness leaves behind. Every process
// a test starts ends on its own within a few seconds, whatever the test does.

func newHeavyCheckStoreForTest(t *testing.T) *HeavyCheckStore {
	t.Helper()
	store, err := NewHeavyCheckStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.poll = 10 * time.Millisecond
	store.cleanupWait = 300 * time.Millisecond
	return store
}

func heavyAsk(run, command string, limit int, priority domain.HeavyCheckPriority) HeavyCheckAsk {
	return HeavyCheckAsk{
		Product: "yoyodyne", RunID: run, WorkItemID: "item-" + run, Commit: "0123456789abcdef0123456789abcdef01234567",
		Check: "make race", Command: command, Limit: limit, Priority: priority,
	}
}

// launchHeld starts script in a process group of its own holding the grant's
// file, writes it down through the grant, and lets go of this process's copy
// of the file, as a gated launch does. Like a gated launch, the script does
// nothing until it has been written down: it waits for a file the test makes
// only then, for at most five seconds.
func launchHeld(t *testing.T, grant *HeavyCheckGrant, script string) *exec.Cmd {
	t.Helper()
	open := filepath.Join(t.TempDir(), "open")
	gated := "i=0; while [ ! -e '" + open + "' ] && [ $i -lt 500 ]; do sleep 0.01; i=$((i+1)); done; " + script
	command := exec.Command("/bin/sh", "-c", gated)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.ExtraFiles = []*os.File{grant.Hold()}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	_ = grant.Hold().Close()
	if err := grant.Register(execution.StartedProcess{PID: command.Process.Pid, ProcessGroup: command.Process.Pid, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(open, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return command
}

// leftover is a script that leaves a process behind in its group and exits,
// and the way to end that process. The process runs until stop is called or
// for at most sixty seconds, so it outlives its check however slowly a loaded
// machine runs the test, and ends by itself however the test ends.
func leftover(t *testing.T) (script string, stop func()) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "stop")
	stop = func() { _ = os.WriteFile(marker, nil, 0o600) }
	t.Cleanup(stop)
	return "end=$(( $(date +%s) + 60 )); ( while [ ! -e '" + marker + "' ] && [ \"$(date +%s)\" -lt \"$end\" ]; do sleep 0.1; done ) & exit 0", stop
}

func recordOf(t *testing.T, store *HeavyCheckStore) HeavyCheckRecord {
	t.Helper()
	record, err := store.Record()
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func acquireSoon(t *testing.T, store *HeavyCheckStore, ask HeavyCheckAsk) *HeavyCheckGrant {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	grant, err := store.Acquire(ctx, ask, nil)
	if err != nil {
		t.Fatalf("Acquire(%s) error = %v", ask.RunID, err)
	}
	return grant
}

// acquireLater asks in the background, and says once it is waiting.
func acquireLater(store *HeavyCheckStore, ask HeavyCheckAsk) (<-chan *HeavyCheckGrant, <-chan HeavyCheckPermit, <-chan error) {
	granted := make(chan *HeavyCheckGrant, 1)
	waiting := make(chan HeavyCheckPermit, 1)
	failed := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		grant, err := store.Acquire(ctx, ask, func(permit HeavyCheckPermit) { waiting <- permit })
		if err != nil {
			failed <- err
			return
		}
		granted <- grant
	}()
	return granted, waiting, failed
}

func expectWaiting(t *testing.T, waiting <-chan HeavyCheckPermit, failed <-chan error) HeavyCheckPermit {
	t.Helper()
	select {
	case permit := <-waiting:
		return permit
	case err := <-failed:
		t.Fatalf("Acquire failed instead of waiting: %v", err)
	case <-time.After(time.Minute):
		t.Fatal("Acquire neither waited nor returned")
	}
	return HeavyCheckPermit{}
}

func expectGranted(t *testing.T, granted <-chan *HeavyCheckGrant, failed <-chan error) *HeavyCheckGrant {
	t.Helper()
	select {
	case grant := <-granted:
		return grant
	case err := <-failed:
		t.Fatalf("Acquire failed: %v", err)
	case <-time.After(2 * time.Minute):
		t.Fatal("Acquire was never granted")
	}
	return nil
}

func expectStillWaiting(t *testing.T, granted <-chan *HeavyCheckGrant, failed <-chan error) {
	t.Helper()
	select {
	case grant := <-granted:
		t.Fatalf("permit %s was granted while the place was taken", grant.Permit.ID)
	case err := <-failed:
		t.Fatalf("Acquire failed: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
}

// heldUntil is a script that runs until release is called, or for at most
// sixty seconds, so the place it holds is taken for as long as the test needs
// it however slowly a loaded machine reaches the next step, and the script ends
// by itself however the test ends.
func heldUntil(t *testing.T) (script string, release func()) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "release")
	release = func() { _ = os.WriteFile(marker, nil, 0o600) }
	t.Cleanup(release)
	return "end=$(( $(date +%s) + 60 )); while [ ! -e '" + marker + "' ] && [ \"$(date +%s)\" -lt \"$end\" ]; do sleep 0.05; done", release
}

// Two runs at the race suite with one place run one after the other: the
// second is recorded as waiting before it waits, and is let start only once
// the first has exited and its processes are gone.
func TestTwoRunsAtOneHeavyCheckWithOnePlaceRunOneAfterTheOther(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)

	first := acquireSoon(t, store, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	held, release := heldUntil(t)
	command := launchHeld(t, first, held)
	granted, waiting, failed := acquireLater(store, heavyAsk("run-b", "make race", 1, domain.HeavyCheckOrdinary))
	waited := expectWaiting(t, waiting, failed)
	if stored, ok := recordOf(t, store).Permit(waited.ID); !ok || stored.State != HeavyCheckWaiting {
		t.Fatalf("waiting permit = %#v, want it on the record as waiting", stored)
	}
	expectStillWaiting(t, granted, failed)
	release()
	_ = command.Wait()
	ended, err := first.Finish("succeeded, exit code 0")
	if err != nil {
		t.Fatal(err)
	}
	if ended.State != HeavyCheckReleased {
		t.Fatalf("first permit = %#v, want it released once its processes have gone", ended)
	}
	second := expectGranted(t, granted, failed)
	if second.Permit.ID != waited.ID || second.Permit.GrantedAt.Before(*ended.ReleasedAt) {
		t.Fatalf("second permit = %#v, want the waiting permit granted after the first was released", second.Permit)
	}
	if second.Waited <= 0 {
		t.Fatalf("waited = %s, want the time the second run waited", second.Waited)
	}
	command = launchHeld(t, second, "exit 0")
	_ = command.Wait()
	if _, err := second.Finish("succeeded, exit code 0"); err != nil {
		t.Fatal(err)
	}
	if holding := recordOf(t, store).Holding(""); len(holding) != 0 {
		t.Fatalf("holding = %#v, want every place given back", holding)
	}
}

// A process the check started that outlives it keeps the place: the permit
// is recorded exited, says why it is kept, and is released by the next look
// that finds the process gone.
func TestAChildThatOutlivesItsCheckKeepsThePlace(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)

	grant := acquireSoon(t, store, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	script, stop := leftover(t)
	command := launchHeld(t, grant, script)
	_ = command.Wait()
	kept, err := grant.Finish("succeeded, exit code 0")
	if err != nil {
		t.Fatal(err)
	}
	if kept.State != HeavyCheckExited || !kept.Stuck() || !strings.Contains(kept.Problem, "still running") {
		t.Fatalf("permit = %#v, want it exited and holding its place for its surviving process", kept)
	}
	if stuck := recordOf(t, store).Stuck(); len(stuck) != 1 || stuck[0].ID != kept.ID {
		t.Fatalf("stuck = %#v, want the permit named as one somebody has to look at", stuck)
	}
	granted, waiting, failed := acquireLater(store, heavyAsk("run-b", "make race", 1, domain.HeavyCheckOrdinary))
	expectWaiting(t, waiting, failed)
	expectStillWaiting(t, granted, failed)
	// The leftover process goes, and the place with it.
	stop()
	second := expectGranted(t, granted, failed)
	released, _ := recordOf(t, store).Permit(kept.ID)
	if released.State != HeavyCheckReleased || released.ReleasedAt.After(*second.Permit.GrantedAt) {
		t.Fatalf("first permit = %#v, want it released before the second was granted", released)
	}
	_, _ = second.Finish("not started")
}

// A landing's check that asks after a run's gate check is let start first.
func TestAWaitingLandingIsLetStartBeforeAnEarlierRun(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)

	holder := acquireSoon(t, store, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	ordinary, ordinaryWaiting, ordinaryFailed := acquireLater(store, heavyAsk("run-b", "make race", 1, domain.HeavyCheckOrdinary))
	expectWaiting(t, ordinaryWaiting, ordinaryFailed)
	preserved, preservedWaiting, preservedFailed := acquireLater(store, heavyAsk("run-c", "make race", 1, domain.HeavyCheckForPreservedWork))
	expectWaiting(t, preservedWaiting, preservedFailed)
	landing, landingWaiting, landingFailed := acquireLater(store, heavyAsk("run-d", "make race RACE_PACKAGES=./...", 1, domain.HeavyCheckForLanding))
	expectWaiting(t, landingWaiting, landingFailed)
	if _, err := holder.Finish("not started"); err != nil {
		t.Fatal(err)
	}
	first := expectGranted(t, landing, landingFailed)
	if first.Permit.RunID != "run-d" {
		t.Fatalf("granted %s first, want the landing", first.Permit.RunID)
	}
	expectStillWaiting(t, ordinary, ordinaryFailed)
	expectStillWaiting(t, preserved, preservedFailed)
	_, _ = first.Finish("not started")
	second := expectGranted(t, preserved, preservedFailed)
	if second.Permit.RunID != "run-c" {
		t.Fatalf("granted %s second, want the preserved work's run", second.Permit.RunID)
	}
	expectStillWaiting(t, ordinary, ordinaryFailed)
	_, _ = second.Finish("not started")
	third := expectGranted(t, ordinary, ordinaryFailed)
	_, _ = third.Finish("not started")
}

// After a restart a running check keeps its place and is not started twice:
// its run, asking again for the same check, waits for the launch that
// outlived its harness to end rather than starting a second beside it, and
// another run waits behind it too. Once its processes are gone it is released
// as having ended with nobody recording how, and the places go on in order.
func TestARunningCheckKeepsItsPlaceAcrossARestartAndIsNotStartedTwice(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)

	grant := acquireSoon(t, store, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	marker := filepath.Join(t.TempDir(), "starts")
	command := launchHeld(t, grant, "echo started >> '"+marker+"'; sleep 0.8")
	// The harness dies here: nothing it would have recorded is, and the check
	// goes on without it. The restarted harness is a new store over the same
	// record.
	restarted := &HeavyCheckStore{root: store.root, poll: store.poll, cleanupWait: store.cleanupWait}
	settled, err := restarted.Settle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if kept, _ := settled.Permit(grant.Permit.ID); kept.State != HeavyCheckRunning {
		t.Fatalf("after restart permit = %#v, want it still running", kept)
	}
	again, againWaiting, againFailed := acquireLater(restarted, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	if waitingOn := expectWaiting(t, againWaiting, againFailed); waitingOn.ID == grant.Permit.ID || waitingOn.RunID != "run-a" {
		t.Fatalf("the run waits as %#v, want a waiting permit of its own beside its earlier launch", waitingOn)
	}
	other, otherWaiting, otherFailed := acquireLater(restarted, heavyAsk("run-b", "make race", 1, domain.HeavyCheckOrdinary))
	expectWaiting(t, otherWaiting, otherFailed)
	expectStillWaiting(t, again, againFailed)
	_ = command.Wait()
	// The run's own new launch and the other run's are let start one at a
	// time, in the order they asked, once the first launch is gone.
	firstNext := expectGranted(t, again, againFailed)
	released, _ := recordOf(t, restarted).Permit(grant.Permit.ID)
	if released.State != HeavyCheckReleased || !strings.Contains(released.Released, "nobody recorded how it ended") {
		t.Fatalf("outlived permit = %#v, want it released as ended unrecorded", released)
	}
	expectStillWaiting(t, other, otherFailed)
	_, _ = firstNext.Finish("not started")
	secondNext := expectGranted(t, other, otherFailed)
	_, _ = secondNext.Finish("not started")
	starts, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(starts), "started"); count != 1 {
		t.Fatalf("the check started %d times, want once", count)
	}
}

// A harness that dies between being given a place and starting the check
// started nothing, so the next look releases the place.
func TestAPlaceGivenToAHarnessThatDiedBeforeLaunchingIsReleased(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)

	grant := acquireSoon(t, store, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	_ = grant.Hold().Close()
	second := acquireSoon(t, store, heavyAsk("run-b", "make race", 1, domain.HeavyCheckOrdinary))
	released, _ := recordOf(t, store).Permit(grant.Permit.ID)
	if released.State != HeavyCheckReleased || !strings.Contains(released.Released, "nothing ran") {
		t.Fatalf("permit = %#v, want it released as never started", released)
	}
	_, _ = second.Finish("not started")
}

// A run that died while waiting keeps its place in the order without holding
// anybody up, and takes the same place up again when it comes back.
func TestAWaitingRunThatDiedKeepsItsOrderWithoutHoldingAnybodyUp(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)

	holder := acquireSoon(t, store, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The waiting run is stood in for by asking and then letting go of its
	// file without a word, which is what dying does.
	var dead HeavyCheckPermit
	session, err := store.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session.settle()
	hold, permit, err := store.newPermit(session, heavyAsk("run-b", "make race", 1, domain.HeavyCheckOrdinary))
	if err != nil {
		t.Fatal(err)
	}
	session.record.Permits = append(session.record.Permits, permit)
	if err := session.save(); err != nil {
		t.Fatal(err)
	}
	session.close()
	dead = permit
	_ = hold.Close()
	later, laterWaiting, laterFailed := acquireLater(store, heavyAsk("run-c", "make race", 1, domain.HeavyCheckOrdinary))
	expectWaiting(t, laterWaiting, laterFailed)
	_, _ = holder.Finish("not started")
	granted := expectGranted(t, later, laterFailed)
	if granted.Permit.RunID != "run-c" {
		t.Fatalf("granted %s, want the run that is still there", granted.Permit.RunID)
	}
	abandoned, _ := recordOf(t, store).Permit(dead.ID)
	if abandoned.State != HeavyCheckWaiting || abandoned.AbandonedAt == nil {
		t.Fatalf("dead waiter = %#v, want it kept, waiting, and marked as having nobody waiting on it", abandoned)
	}
	// It comes back and takes its own place up, keeping when it first asked.
	back, backWaiting, backFailed := acquireLater(store, heavyAsk("run-b", "make race", 1, domain.HeavyCheckOrdinary))
	if waiting := expectWaiting(t, backWaiting, backFailed); waiting.ID != dead.ID || !waiting.AskedAt.Equal(dead.AskedAt) || waiting.AbandonedAt != nil {
		t.Fatalf("came back as %#v, want permit %s as it first asked", waiting, dead.ID)
	}
	_, _ = granted.Finish("not started")
	resumed := expectGranted(t, back, backFailed)
	if resumed.Permit.ID != dead.ID {
		t.Fatalf("granted %s, want the permit it first asked for", resumed.Permit.ID)
	}
	_, _ = resumed.Finish("not started")
}

// A run that stops waiting gives its request up, recorded as such.
func TestARunThatStopsWaitingGivesItsRequestUp(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)

	holder := acquireSoon(t, store, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	ctx, cancel := context.WithCancel(context.Background())
	var asked HeavyCheckPermit
	_, err := store.Acquire(ctx, heavyAsk("run-b", "make race", 1, domain.HeavyCheckOrdinary), func(permit HeavyCheckPermit) {
		asked = permit
		cancel()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire error = %v, want the cancellation", err)
	}
	given, _ := recordOf(t, store).Permit(asked.ID)
	if given.State != HeavyCheckReleased || !strings.Contains(given.Released, "stopped waiting") {
		t.Fatalf("permit = %#v, want it given up", given)
	}
	_, _ = holder.Finish("not started")
}

// Many runs at once over two places never run more than two at a time, every
// one of them runs, and every place is given back.
func TestManyRunsAtOnceNeverRunMoreThanTheirPlaces(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)
	// Eight runs reading the record every 10ms on a machine already running
	// the whole suite spend most of their time queued for its lock, so this
	// test reads it less often and allows minutes: what it proves is the
	// bound and that everything finishes, not how fast.
	store.poll = 50 * time.Millisecond

	var running, most atomic.Int32
	var wait sync.WaitGroup
	errs := make(chan error, 8)
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			grant, err := store.Acquire(ctx, heavyAsk("run-"+string(rune('a'+index)), "make race", 2, domain.HeavyCheckOrdinary), nil)
			if err != nil {
				errs <- fmt.Errorf("run %d asking for a place: %w", index, err)
				return
			}
			now := running.Add(1)
			for {
				seen := most.Load()
				if now <= seen || most.CompareAndSwap(seen, now) {
					break
				}
			}
			// Started the way the check runner starts one: behind a launch gate
			// that holds the permit's file and writes the process down first.
			_, err = execution.OSProcessRunner{}.Run(ctx, execution.Command{
				Name: "/bin/sh", Args: []string{"-c", "sleep 0.1"}, Dir: t.TempDir(), Timeout: 10 * time.Second,
				Gate: &execution.LaunchGate{Hold: grant.Hold(), Register: grant.Register},
			}, nil)
			running.Add(-1)
			if err != nil {
				errs <- fmt.Errorf("run %d running its check: %w", index, err)
			}
			if _, err := grant.Finish("succeeded, exit code 0"); err != nil {
				errs <- fmt.Errorf("run %d giving its place back: %w", index, err)
			}
		}(index)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if most.Load() > 2 || most.Load() < 1 {
		t.Fatalf("at most %d ran at once, want no more than the two places", most.Load())
	}
	record := recordOf(t, store)
	if len(record.Holding("")) != 0 || len(record.Waiting()) != 0 {
		t.Fatalf("record = %#v, want nothing held or waiting", record)
	}
}

// A machine restarted since a check started has none of its processes left.
func TestACheckFromBeforeTheMachineRestartedIsReleased(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)
	store.boot = func() string { return "boot-1" }

	grant := acquireSoon(t, store, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	command := launchHeld(t, grant, "sleep 0.5")
	defer func() { _ = command.Wait() }()
	store.boot = func() string { return "boot-2" }
	settled, err := store.Settle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if released, _ := settled.Permit(grant.Permit.ID); released.State != HeavyCheckReleased || !strings.Contains(released.Released, "gone") {
		t.Fatalf("permit = %#v, want it released", released)
	}
}

// A place a check's surviving processes keep is released by hand only with
// who released it and why, and only while it holds one.
func TestAPlaceIsReleasedByHandWithWhoAndWhy(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)

	grant := acquireSoon(t, store, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	script, _ := leftover(t)
	command := launchHeld(t, grant, script)
	_ = command.Wait()
	kept, _ := grant.Finish("succeeded, exit code 0")
	if !kept.Stuck() {
		t.Fatalf("permit = %#v, want it holding its place for its surviving process", kept)
	}
	if _, err := store.ReleaseByHand(context.Background(), kept.ID, "", ""); err == nil {
		t.Fatal("a release naming nobody was accepted")
	}
	released, err := store.ReleaseByHand(context.Background(), kept.ID, "development-manager", "the leftover process is a known hang")
	if err != nil {
		t.Fatal(err)
	}
	if released.State != HeavyCheckReleased || released.ReleasedBy != "development-manager" || !strings.Contains(released.Released, "known hang") {
		t.Fatalf("permit = %#v", released)
	}
	if _, err := store.ReleaseByHand(context.Background(), kept.ID, "development-manager", "again"); err == nil {
		t.Fatal("a released permit was released again")
	}
}

// Different heavy checks count against their own places.
func TestEachHeavyCheckCountsItsOwnPlaces(t *testing.T) {
	t.Parallel()
	store := newHeavyCheckStoreForTest(t)

	race := acquireSoon(t, store, heavyAsk("run-a", "make race", 1, domain.HeavyCheckOrdinary))
	ask := heavyAsk("run-b", "make test", 1, domain.HeavyCheckOrdinary)
	ask.Check = "make test"
	test := acquireSoon(t, store, ask)
	if limits := recordOf(t, store).Limits; limits["make race"] != 1 || limits["make test"] != 1 {
		t.Fatalf("limits = %v", limits)
	}
	_, _ = race.Finish("not started")
	_, _ = test.Finish("not started")
}
