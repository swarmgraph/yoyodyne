package readmodel

import (
	"context"
	"io"
	"os"
	"path/filepath"
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
type storeRunner struct {
	store chan struct{}
	hold  time.Duration

	mu    sync.Mutex
	asked map[string]int
}

func newStoreRunner(hold time.Duration) *storeRunner {
	return &storeRunner{store: make(chan struct{}, 1), hold: hold, asked: map[string]int{}}
}

func (r *storeRunner) Run(ctx context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	verb := command.Args[0]
	r.mu.Lock()
	r.asked[verb]++
	r.mu.Unlock()
	limit := time.NewTimer(command.Timeout)
	defer limit.Stop()
	select {
	case r.store <- struct{}{}:
	case <-limit.C:
		return execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1}, nil
	case <-ctx.Done():
		return execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1}, nil
	}
	time.Sleep(r.hold)
	<-r.store
	item := `{"id":"yoyodyne-1","title":"Open work","status":"open","priority":1,"issue_type":"task","notes":"landed"}`
	switch verb {
	case "export":
		_, err := io.WriteString(command.RawStdout, item+"\n")
		return execution.ProcessResult{Status: execution.ProcessSucceeded}, err
	default:
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[" + item + "]"}, nil
	}
}

func (r *storeRunner) count(verb string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.asked[verb]
}

// noHolders answers a stopped call's question of who held the store without
// asking the operating system, which knows nothing of the stand-in store.
type noHolders struct{}

func (noHolders) Holders(context.Context, string) ([]beads.StoreHolder, error) { return nil, nil }

// trackerContention runs readers status readers, each reading where things
// stand rounds times, and exporters worktree exports, all at once and each
// with tracker clients of its own as separate processes would be, and a write
// to one item once they are all under way. It returns how long the write took and
// how it ended.
func trackerContention(t *testing.T, runner *storeRunner, shared bool, readers, rounds, exporters int, writeLimit time.Duration) (time.Duration, error) {
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
				sources.Tracker = client(5 * time.Second)
				sources.TrackerTimeout = 5 * time.Second
				if shared {
					sources.TrackerReadings = readings
				}
				ReadStanding(context.Background(), sources)
			}
		})
	}
	for range exporters {
		contenders.Go(func() {
			if err := client(5 * time.Second).RefreshExport(context.Background()); err != nil {
				t.Errorf("worktree export: %v", err)
			}
		})
	}
	defer contenders.Wait()

	// Every contender is queued on the store before the write arrives.
	time.Sleep(20 * time.Millisecond)
	started := time.Now()
	_, err = client(writeLimit).RecordOutcome(context.Background(), "yoyodyne-1", "landed")
	return time.Since(started), err
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
		hold       = 50 * time.Millisecond
		readers    = 12
		rounds     = 3
		exporters  = 3
		writeLimit = time.Second
	)

	t.Run("each reading asking for itself", func(t *testing.T) {
		t.Parallel()
		runner := newStoreRunner(hold)
		// A write stopped at its limit is read back and asked again, so what
		// says it was stopped is how long it took to answer, not whether it did.
		took, err := trackerContention(t, runner, false, readers, rounds, exporters, writeLimit)
		if err == nil && took < writeLimit {
			t.Fatalf("the write answered in %s with every status reading asking the tracker itself; want it stopped at its %s limit, or this test shows nothing", took, writeLimit)
		}
	})

	t.Run("the readings shared", func(t *testing.T) {
		t.Parallel()
		runner := newStoreRunner(hold)
		took, err := trackerContention(t, runner, true, readers, rounds, exporters, writeLimit)
		if err != nil || took >= writeLimit {
			t.Fatalf("the write = %v after %s; want it answered inside its %s limit", err, took, writeLimit)
		}
		// Every reading on the machine, all of them within one freshness window,
		// asked the tracker for every item once and for what is ready once.
		if lists, readies := runner.count("list"), runner.count("ready"); lists != 1 || readies != 1 {
			t.Fatalf("%d status readings made %d listing(s) and %d readiness quer(ies); want one of each", readers*rounds, lists, readies)
		}
	})
}
