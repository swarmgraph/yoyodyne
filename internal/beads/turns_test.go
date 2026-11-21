//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package beads

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// orderRunner answers every bd call after a pause, noting the order calls
// started in and the most that ran at once.
type orderRunner struct {
	pause   time.Duration
	started chan string
	mu      sync.Mutex
	order   []string
	running atomic.Int32
	peak    atomic.Int32
}

func (r *orderRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	now := r.running.Add(1)
	defer r.running.Add(-1)
	for seen := r.peak.Load(); now > seen && !r.peak.CompareAndSwap(seen, now); seen = r.peak.Load() {
	}
	r.mu.Lock()
	r.order = append(r.order, command.Args[1])
	r.mu.Unlock()
	if r.started != nil {
		r.started <- command.Args[1]
	}
	time.Sleep(r.pause)
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "{}"}, nil
}

// bd does not queue for its store: a bd that finds it taken sleeps and tries
// again, so the one that has waited longest is not the one that gets in next.
// The harness's own calls therefore wait for their turn before starting bd,
// and are served in the order they asked, one at a time. Without the line all
// four of these reads ran at once and in no particular order.
func TestTheHarnesssCallsTakeTurnsInTheOrderTheyAsked(t *testing.T) {
	dir := listingStore(t)
	runner := &orderRunner{pause: 300 * time.Millisecond, started: make(chan string, 8)}
	client := Client{Runner: runner, Binary: "bd-test", Dir: dir}
	var wg sync.WaitGroup
	read := func(id string) {
		defer wg.Done()
		if _, err := client.run(context.Background(), "show", id, "--json"); err != nil {
			t.Errorf("read %s: %v", id, err)
		}
	}
	wg.Add(1)
	go read("calc-1")
	<-runner.started
	// Each read is asked once the one before it has its ticket, so the order
	// they asked in is known whatever the machine is doing.
	for asked, id := range []string{"calc-2", "calc-3", "calc-4"} {
		wg.Add(1)
		go read(id)
		for deadline := time.Now().Add(5 * time.Second); len(tickets(t, dir)) < asked+2; time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%s never took a ticket", id)
			}
		}
	}
	wg.Wait()
	if got := strings.Join(runner.order, " "); got != "calc-1 calc-2 calc-3 calc-4" {
		t.Errorf("calls ran in the order %s, want the order they asked", got)
	}
	if peak := runner.peak.Load(); peak != 1 {
		t.Errorf("%d calls ran at once, want one at a time", peak)
	}
	if left := tickets(t, dir); len(left) != 0 {
		t.Errorf("tickets left behind: %v", left)
	}
}

// A ticket nobody holds, left by a process that died, is passed and removed,
// rather than keeping every later call waiting for ever. A ticket a live call
// holds is waited for.
func TestATicketLeftByADeadCallIsPassedAndAHeldOneIsWaitedFor(t *testing.T) {
	dir := listingStore(t)
	turns := filepath.Join(dir, ".beads", turnDirectory)
	if err := os.MkdirAll(turns, 0o700); err != nil {
		t.Fatal(err)
	}
	dead := filepath.Join(turns, turnName(time.Now().Add(-time.Minute), 1, 1))
	if err := os.WriteFile(dead, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &orderRunner{}
	client := Client{Runner: runner, Binary: "bd-test", Dir: dir}
	if _, err := client.run(context.Background(), "show", "calc-1", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dead); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the dead call's ticket is still there (%v)", err)
	}

	held, err := os.OpenFile(filepath.Join(turns, turnName(time.Now().Add(-time.Minute), 2, 1)), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := client.run(context.Background(), "show", "calc-2", "--json"); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("the call ran past a held ticket ahead of it (%v)", err)
	case <-time.After(150 * time.Millisecond):
	}
	_ = syscall.Flock(int(held.Fd()), syscall.LOCK_UN)
	_ = os.Remove(held.Name())
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not run once the ticket ahead of it was given up")
	}
}

// A listing joins the line as soon as it is asked, like every other call. It
// used to take a lock shared by every listing first and hold it while it waited
// for its turn, so a second listing could not even join the line until the
// first had waited and run, and then waited its own turn from the back: on the
// live factory listings waited about three times as long as other calls.
// Here a held ticket stands for a call running ahead of both listings; the
// second listing, from another process, has to be in the line beside the first
// while the first is still waiting, and the two are answered in the order they
// asked.
func TestASecondListingJoinsTheLineWhileTheFirstWaitsForItsTurn(t *testing.T) {
	dir := listingStore(t)
	turns := filepath.Join(dir, ".beads", turnDirectory)
	if err := os.MkdirAll(turns, 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := os.OpenFile(filepath.Join(turns, turnName(time.Now().Add(-time.Minute), 2, 1)), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	var order []string
	var mu sync.Mutex
	listing := func(binary string, done chan<- error) {
		// Distinct binaries keep the two from sharing one read in memory, standing
		// for two processes. The runner notes when each one's bd starts, which is
		// what the line orders.
		runner := startRunner{started: func(name string) {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
		}}
		_, err := Client{Runner: runner, Binary: binary, Dir: dir}.List(context.Background(), "open")
		done <- err
	}
	waitForTickets := func(want int, within time.Duration) bool {
		for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if len(tickets(t, dir)) >= want {
				return true
			}
		}
		return false
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go listing("bd-a", first)
	if !waitForTickets(2, 5*time.Second) {
		t.Fatal("the first listing never joined the line")
	}
	go listing("bd-b", second)
	if !waitForTickets(3, 2*time.Second) {
		t.Errorf("the second listing did not join the line while the first waited for its turn: tickets %v", tickets(t, dir))
	}
	_ = syscall.Flock(int(held.Fd()), syscall.LOCK_UN)
	_ = os.Remove(held.Name())
	for _, done := range []chan error{first, second} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a listing was not answered once the call ahead of it ended")
		}
	}
	if got := strings.Join(order, " "); got != "bd-a bd-b" {
		t.Errorf("listings ran in the order %s, want the order they asked", got)
	}
}

// startRunner answers every bd call with one item at once, noting which bd
// binary it was started as.
type startRunner struct{ started func(string) }

func (r startRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.started(command.Name)
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: oneItemListing}, nil
}

// A call whose turn does not come within its bound is not made, reads as
// unanswered, and is still logged, with how long its caller waited, so the
// limits step counts it.
//
// Not parallel: the log is the whole process's.
func TestACallWhoseTurnNeverCameIsUnansweredAndLogged(t *testing.T) {
	kept := &keptCalls{}
	LogCallsTo(kept)
	t.Cleanup(func() { LogCallsTo(nil) })
	dir := listingStore(t)
	turns := filepath.Join(dir, ".beads", turnDirectory)
	if err := os.MkdirAll(turns, 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := os.OpenFile(filepath.Join(turns, turnName(time.Now().Add(-time.Minute), 2, 1)), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	runner := &orderRunner{}
	client := Client{Runner: runner, Binary: "bd-test", Dir: dir, Timeout: 100 * time.Millisecond, Holders: noHolders{}}
	_, err = client.Ready(context.Background())
	if !Unanswered(err) || !strings.Contains(err.Error(), "the harness's tracker calls ahead of this one did not finish within 100ms") {
		t.Fatalf("error = %v, want an unanswered call that says why", err)
	}
	if len(runner.order) != 0 {
		t.Errorf("bd ran %v, want nothing run", runner.order)
	}
	var unmade []LoggedCall
	for _, call := range kept.calls {
		if call.Verb == "ready" {
			unmade = append(unmade, call)
		}
	}
	if len(unmade) == 0 || !unmade[0].StoppedAtLimit || unmade[0].Took != 0 || unmade[0].Waited < 100*time.Millisecond {
		t.Errorf("logged %+v, want the call never made, with its wait", kept.calls)
	}
}

// noHolders names no process holding anything, so a test does not ask the
// operating system.
type noHolders struct{}

func (noHolders) Holders(context.Context, string) ([]StoreHolder, error) { return nil, nil }

func tickets(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, ".beads", turnDirectory))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
