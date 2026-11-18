package readmodel

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// storeRunner answers as bd does over one store every command opens under one
// exclusive lock: a command waits its turn, in the order it arrived, holds the
// store for hold, and is stopped at its own limit if its turn has not come.
//
// Time here is counted, not measured. The store keeps its own clock, which moves
// on by hold each time a command is answered, and a command is stopped when the
// clock has moved on by its limit before its turn came. How long a command waits
// is then the number of answers ahead of it times hold, and no pause on a busy
// machine, however long, changes it. Waiting on the wall clock made these tests
// fail when the machine was loaded enough to stretch a sleep past a limit.
type storeRunner struct {
	hold time.Duration

	mu    sync.Mutex
	asked map[string]int
	// calls counts every command asked of the store, and arrived is signalled
	// as each one is, so a test can wait until its contenders have asked.
	calls   int
	arrived *sync.Cond

	// clock is the store's own time; queue holds the commands waiting for their
	// turn, oldest first, and a command is answered when it is first in it.
	clock time.Duration
	queue []*storeTicket
	// stopped and longest, by verb, are how many commands the store stopped at
	// their limit and the longest any waited, in the store's own time.
	stopped map[string]int
	longest map[string]time.Duration
	// open is whether the store answers yet. It starts shut, so that every
	// command asked before the write has queued is ahead of it, however quickly
	// the machine would otherwise have answered them.
	open bool
}

type storeTicket struct{ arrived time.Duration }

func newStoreRunner(hold time.Duration) *storeRunner {
	runner := &storeRunner{hold: hold, asked: map[string]int{}, stopped: map[string]int{}, longest: map[string]time.Duration{}}
	runner.arrived = sync.NewCond(&runner.mu)
	return runner
}

// waitForCalls returns once at least n commands have been asked of the store.
func (r *storeRunner) waitForCalls(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for r.calls < n {
		r.arrived.Wait()
	}
}

func (r *storeRunner) Run(ctx context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	verb := command.Args[0]
	r.mu.Lock()
	r.asked[verb]++
	if verb == "list" && slices.Contains(command.Args, "--all") {
		r.asked[everyItem]++
	}
	r.calls++
	ticket := &storeTicket{arrived: r.clock}
	r.queue = append(r.queue, ticket)
	r.arrived.Broadcast()
	for r.queue[0] != ticket || !r.open {
		r.arrived.Wait()
	}
	waited := r.clock - ticket.arrived
	r.longest[verb] = max(r.longest[verb], waited)
	timedOut := waited >= command.Timeout || ctx.Err() != nil
	if timedOut {
		r.stopped[verb]++
	} else {
		r.clock += r.hold
	}
	r.queue = r.queue[1:]
	r.arrived.Broadcast()
	r.mu.Unlock()
	if timedOut {
		return execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1}, nil
	}
	item := `{"id":"yoyodyne-1","title":"Open work","status":"open","priority":1,"issue_type":"task","notes":"landed"}`
	switch verb {
	case "export":
		_, err := io.WriteString(command.RawStdout, item+"\n")
		return execution.ProcessResult{Status: execution.ProcessSucceeded}, err
	default:
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[" + item + "]"}, nil
	}
}

// writeAmongTheAsked runs write, waits until the store has been asked for it
// beside everything asked before it, and only then starts answering, so the
// write waits behind exactly the commands that were asked first.
func (r *storeRunner) writeAmongTheAsked(write func() error) error {
	done := make(chan error, 1)
	go func() { done <- write() }()
	r.mu.Lock()
	for r.asked[writeVerb] == 0 {
		r.arrived.Wait()
	}
	r.open = true
	r.arrived.Broadcast()
	r.mu.Unlock()
	return <-done
}

// writeVerb is the command a write to one item is asked of the store as.
const writeVerb = "update"

// writeWait is the longest a write waited for its turn, in the store's own
// time, and whether the store stopped any write at its limit.
func (r *storeRunner) writeWait() (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.longest[writeVerb], r.stopped[writeVerb] > 0
}

// everyItem is what storeRunner counts a listing of every item under, closed
// work included, beside counting it as a listing.
const everyItem = "list --all"

func (r *storeRunner) count(verb string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.asked[verb]
}

// contenderLimit is the limit every status reading and export in the test is
// given. They are not what the test measures, so it is far longer than any of
// them waits even on a loaded machine; only the write's own limit is tight.
const contenderLimit = 30 * time.Second

// noHolders answers a stopped call's question of who held the store without
// asking the operating system, which knows nothing of the stand-in store.
type noHolders struct{}

func (noHolders) Holders(context.Context, string) ([]beads.StoreHolder, error) { return nil, nil }

// trackerContention runs readers status readers, each reading where things
// stand rounds times, and exporters worktree exports, all at once and each
// with tracker clients of its own as separate processes would be, and a write
// to one item once they are all under way. It returns how the write ended; how long
// it waited is the runner's to say, in the store's own time.
func trackerContention(t *testing.T, runner *storeRunner, shared bool, readers, rounds, exporters int, writeLimit time.Duration) error {
	t.Helper()
	repository := t.TempDir()
	if err := os.Mkdir(filepath.Join(repository, ".beads"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".beads", "metadata.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	readings, err := runstate.NewTrackerReadingStore(t.TempDir(), domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatal(err)
	}
	client := func(limit time.Duration) beads.Client {
		return beads.Client{Runner: runner, Dir: repository, Timeout: limit, Holders: noHolders{}}
	}

	var contenders sync.WaitGroup
	for range readers {
		contenders.Go(func() {
			for range rounds {
				sources := quietSources()
				sources.Tracker = client(contenderLimit)
				sources.TrackerTimeout = contenderLimit
				if shared {
					sources.TrackerReadings = readings
				}
				ReadStanding(context.Background(), sources)
			}
		})
	}
	for range exporters {
		contenders.Go(func() {
			err := client(contenderLimit).RefreshExport(context.Background())
			// With every reading asking for itself the store is held long enough
			// that, on a loaded machine, an export queued behind another can wait
			// past its own limit for the export lock: that is the same long hold the
			// write is stopped by, so it is what the test shows rather than a fault.
			// Any other failure, and any failure with the readings shared, is one.
			if err != nil && (shared || !errors.Is(err, beads.ErrExportBusy)) {
				t.Errorf("worktree export: %v", err)
			}
		})
	}
	defer contenders.Wait()

	// Every contender is queued on the store before the write arrives: each
	// reading asking for itself has asked for every item and for what is
	// ready, which it asks at once, and the first export has asked, or, where
	// the readings share, the first of them has. The exports take the export
	// lock one at a time, so only one of them asks at first. Waiting for fewer
	// asks than that lets the write in ahead of the readings that have not
	// asked yet, where it can be answered inside its limit. This is waited on
	// rather than slept for, so a loaded machine cannot let the write in ahead
	// of them either.
	if shared {
		runner.waitForCalls(1)
	} else {
		runner.waitForCalls(2*readers + min(exporters, 1))
	}
	return runner.writeAmongTheAsked(func() error {
		_, err := client(writeLimit).RecordOutcome(context.Background(), "yoyodyne-1", "landed")
		return err
	})
}

// A tracker write arriving among repeated status readings and the exports for
// new worktrees finishes inside its own limit, because the status readings on
// the machine share one listing of every item and one readiness query between
// them, and the exports queued behind one begun after they asked share it.
//
// The same readings each asking the tracker for themselves, which is how they
// stood before the shared reading, keep the store long enough that the write is
// stopped at its limit: that is the case the first half of the test exists for.
func TestATrackerWriteFinishesInsideItsLimitBesideStatusReadingsAndExports(t *testing.T) {
	t.Parallel()
	const (
		hold       = 100 * time.Millisecond
		readers    = 12
		rounds     = 3
		exporters  = 3
		writeLimit = 2 * time.Second
	)

	t.Run("each reading asking for itself", func(t *testing.T) {
		t.Parallel()
		runner := newStoreRunner(hold)
		// A write stopped at its limit is read back and asked again, so what
		// says it was stopped is the store's record of it, not how it ended.
		err := trackerContention(t, runner, false, readers, rounds, exporters, writeLimit)
		if waited, stopped := runner.writeWait(); !stopped {
			t.Fatalf("the write = %v after waiting %s of the store's time with every status reading asking the tracker itself; want it stopped at its %s limit, or this test shows nothing", err, waited, writeLimit)
		}
	})

	t.Run("the readings shared", func(t *testing.T) {
		t.Parallel()
		runner := newStoreRunner(hold)
		began := time.Now()
		err := trackerContention(t, runner, true, readers, rounds, exporters, writeLimit)
		windows := 1 + int(time.Since(began)/sharedReadingFresh)
		if waited, stopped := runner.writeWait(); err != nil || stopped || waited >= writeLimit {
			t.Fatalf("the write = %v after waiting %s of the store's time; want it answered inside its %s limit", err, waited, writeLimit)
		}
		// Every reading on the machine asked the tracker for every item and for
		// what is ready at most once in each freshness window the readings
		// spanned: once, unless a loaded machine stretched them past one.
		if lists, readies := runner.count("list"), runner.count("ready"); lists < 1 || lists > windows || readies != lists {
			t.Fatalf("%d status readings over %d freshness window(s) made %d listing(s) and %d readiness quer(ies); want one of each per window", readers*rounds, windows, lists, readies)
		}
	})
}

// madeEveryTime is the shared reading with no reading ever fresh enough to hand
// over, so every dashboard refresh makes one of its own, queued on the same
// lock: the most a dashboard can ask of the tracker, whatever its timing.
type madeEveryTime struct{ store *runstate.TrackerReadingStore }

func (m madeEveryTime) Share(ctx context.Context, _ time.Duration, read func(*runstate.TrackerReading) (runstate.TrackerReading, bool)) (runstate.TrackerReading, error) {
	return m.store.Share(ctx, 0, read)
}

// A tracker write arriving among repeated dashboard refreshes — each reading
// the standing, the titles its throughput is named with, and the delivery
// measures, and each making a reading of its own — finishes inside its own
// limit, and only the first reading lists every item: the rest list the
// unfinished work and carry the closed items forward.
func TestATrackerWriteFinishesInsideItsLimitBesideRepeatedDashboardRefreshes(t *testing.T) {
	t.Parallel()
	const (
		hold       = 25 * time.Millisecond
		dashboards = 3
		refreshes  = 3
		writeLimit = 2 * time.Second
	)
	runner := newStoreRunner(hold)
	repository := t.TempDir()
	if err := os.Mkdir(filepath.Join(repository, ".beads"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".beads", "metadata.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := runstate.NewTrackerReadingStore(t.TempDir(), domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatal(err)
	}
	readings := madeEveryTime{store}
	client := func(limit time.Duration) beads.Client {
		return beads.Client{Runner: runner, Dir: repository, Timeout: limit, Holders: noHolders{}}
	}

	var contenders sync.WaitGroup
	for range dashboards {
		contenders.Go(func() {
			for range refreshes {
				sources := quietSources()
				sources.Tracker, sources.TrackerTimeout, sources.TrackerReadings = client(contenderLimit), contenderLimit, readings
				ReadStanding(context.Background(), sources)
				if _, err := ReadWorkItemTitles(context.Background(), sources); err != nil {
					t.Errorf("titles: %v", err)
				}
				ReadDelivery(context.Background(), DeliverySources{Tracker: client(contenderLimit), TrackerTimeout: contenderLimit, TrackerReadings: readings, Now: sources.Now})
			}
		})
	}

	// The write arrives once the refreshes are under way, which is waited on
	// rather than slept for, so a loaded machine cannot let it in before them.
	runner.waitForCalls(1)
	err = runner.writeAmongTheAsked(func() error {
		_, err := client(writeLimit).RecordOutcome(context.Background(), "yoyodyne-1", "landed")
		return err
	})
	contenders.Wait()
	if waited, stopped := runner.writeWait(); err != nil || stopped || waited >= writeLimit {
		t.Fatalf("the write = %v after waiting %s of the store's time; want it answered inside its %s limit", err, waited, writeLimit)
	}
	if every, lists := runner.count(everyItem), runner.count("list"); every != 1 || lists < 2 {
		t.Fatalf("%d dashboard refreshes made %d listing(s), %d of them of every item; want only the first of every item", dashboards*refreshes, lists, every)
	}
}
