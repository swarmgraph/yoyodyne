package beads

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// gatedRunner answers every bd call once release is closed, counting the calls
// made and how many were running at the same moment.
type gatedRunner struct {
	release chan struct{}
	started chan struct{}
	stdout  string
	status  execution.ProcessStatus
	calls   atomic.Int32
	running atomic.Int32
	peak    *atomic.Int32
}

func (g *gatedRunner) Run(_ context.Context, _ execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	g.calls.Add(1)
	now := g.running.Add(1)
	defer g.running.Add(-1)
	if g.peak != nil {
		for {
			seen := g.peak.Load()
			if now <= seen || g.peak.CompareAndSwap(seen, now) {
				break
			}
		}
	}
	if g.started != nil {
		g.started <- struct{}{}
	}
	<-g.release
	if g.status == execution.ProcessFailed {
		return execution.ProcessResult{Status: g.status, ExitCode: 1, Stderr: "store broken"}, nil
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: g.stdout}, nil
}

func listingStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "config.yaml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

const oneItemListing = `[{"id":"yoyodyne-1","title":"One","status":"open"}]`

func TestConcurrentRequestsForOneListingShareOneRead(t *testing.T) {
	dir := listingStore(t)
	runner := &gatedRunner{release: make(chan struct{}), started: make(chan struct{}, 8), stdout: oneItemListing}
	const askers = 5
	var wg sync.WaitGroup
	results := make([][]WorkItem, askers)
	errs := make([]error, askers)
	ask := func(i int) {
		defer wg.Done()
		results[i], errs[i] = Client{Runner: runner, Binary: "bd-test", Dir: dir}.List(context.Background(), "open")
	}
	wg.Add(1)
	go ask(0)
	<-runner.started
	for i := 1; i < askers; i++ {
		wg.Add(1)
		go ask(i)
	}
	time.Sleep(100 * time.Millisecond)
	close(runner.release)
	wg.Wait()
	if got := runner.calls.Load(); got != 1 {
		t.Fatalf("bd ran %d times for %d concurrent requests, want 1", got, askers)
	}
	for i := range results {
		if errs[i] != nil || len(results[i]) != 1 || results[i][0].ID != "yoyodyne-1" {
			t.Fatalf("asker %d got %v, %v", i, results[i], errs[i])
		}
	}
	results[0][0].Title = "changed"
	if results[1][0].Title == "changed" {
		t.Fatal("askers share one decoded list")
	}
}

func TestFailedListingIsReportedToEveryWaiter(t *testing.T) {
	dir := listingStore(t)
	runner := &gatedRunner{release: make(chan struct{}), started: make(chan struct{}, 8), status: execution.ProcessFailed}
	const askers = 4
	var wg sync.WaitGroup
	errs := make([]error, askers)
	ask := func(i int) {
		defer wg.Done()
		_, errs[i] = Client{Runner: runner, Binary: "bd-test", Dir: dir}.Ready(context.Background())
	}
	wg.Add(1)
	go ask(0)
	<-runner.started
	for i := 1; i < askers; i++ {
		wg.Add(1)
		go ask(i)
	}
	time.Sleep(100 * time.Millisecond)
	close(runner.release)
	wg.Wait()
	if got := runner.calls.Load(); got != 1 {
		t.Fatalf("bd ran %d times, want 1", got)
	}
	for i, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "store broken") {
			t.Fatalf("asker %d error = %v, want the failure", i, err)
		}
	}
}

func TestWaiterReadsAgainWhenTheAskerGaveUp(t *testing.T) {
	dir := listingStore(t)
	runner := &cancelableRunner{started: make(chan struct{}, 4), stdout: oneItemListing}
	first, cancel := context.WithCancel(context.Background())
	client := Client{Runner: runner, Binary: "bd-test", Dir: dir}
	done := make(chan error, 1)
	go func() { _, err := client.List(first, "open"); done <- err }()
	<-runner.started
	second := make(chan error, 1)
	go func() { _, err := client.List(context.Background(), "open"); second <- err }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("the asker that gave up got an answer")
	}
	if err := <-second; err != nil {
		t.Fatalf("waiter inherited the first asker's giving up: %v", err)
	}
}

type cancelableRunner struct {
	started chan struct{}
	stdout  string
	calls   atomic.Int32
}

func (r *cancelableRunner) Run(ctx context.Context, _ execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	if r.calls.Add(1) == 1 {
		r.started <- struct{}{}
		<-ctx.Done()
		return execution.ProcessResult{}, ctx.Err()
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: r.stdout}, nil
}

func TestTwoProcessesNeverHoldTheStoreForListingsTogether(t *testing.T) {
	dir := listingStore(t)
	var peak atomic.Int32
	// Distinct bd binaries keep the two from sharing a read in memory, standing
	// for two processes that only the lock file joins.
	a := &gatedRunner{release: make(chan struct{}), started: make(chan struct{}, 2), stdout: oneItemListing, peak: &peak}
	b := &gatedRunner{release: a.release, started: make(chan struct{}, 2), stdout: oneItemListing, peak: &peak}
	var wg sync.WaitGroup
	for i, runner := range []*gatedRunner{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := (Client{Runner: runner, Binary: []string{"bd-a", "bd-b"}[i], Dir: dir}).List(context.Background(), "open"); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(300 * time.Millisecond)
	if running := a.running.Load() + b.running.Load(); running != 1 {
		t.Fatalf("%d listings held the store at once, want 1", running)
	}
	close(a.release)
	wg.Wait()
	if peak.Load() != 1 {
		t.Fatalf("peak concurrent listings = %d, want 1", peak.Load())
	}
	if a.calls.Load() != 1 || b.calls.Load() != 1 {
		t.Fatalf("each process reads for itself: %d and %d", a.calls.Load(), b.calls.Load())
	}
}

// valueRunner is shaped like execution.OSProcessRunner: a value holding a
// function, which can never be compared.
type valueRunner struct {
	run   func() execution.ProcessResult
	calls *atomic.Int32
	hold  chan struct{}
}

func (v valueRunner) Run(context.Context, execution.Command, execution.OutputObserver) (execution.ProcessResult, error) {
	v.calls.Add(1)
	<-v.hold
	return v.run(), nil
}

func TestClientsBuiltLikeProductionShareOneRead(t *testing.T) {
	dir := listingStore(t)
	var calls atomic.Int32
	hold := make(chan struct{})
	answer := func() execution.ProcessResult {
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: oneItemListing}
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each caller builds its own client with its own runner value.
			client := Client{Runner: valueRunner{run: answer, calls: &calls, hold: hold}, Binary: "bd-test", Dir: dir}
			if _, err := client.List(context.Background(), "open"); err != nil {
				t.Error(err)
			}
		}()
		time.Sleep(50 * time.Millisecond)
	}
	close(hold)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("bd ran %d times for 4 concurrent requests from separate clients, want 1", got)
	}
}
