package maintain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const megabyte = int64(1) << 20

// fakeStore is the tracker's store as the cleanup step sees it: a size, and a
// cleanup that answers what it is told to, remembering that it was asked and
// what the pass had already done when it was.
type fakeStore struct {
	mu      sync.Mutex
	size    int64
	after   int64
	busy    []beads.StoreHolder
	stopped bool
	asked   []time.Duration
	order   *[]string
	// items is how many work items the store holds, and lose how many of them
	// a cleanup loses, naming them missing; fail is a cleanup bd fails.
	items int
	lose  int
	fail  error
}

func (s *fakeStore) StoreSize() (string, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return "/opt/calc/.beads/embeddeddolt", s.size, nil
}

func (s *fakeStore) CleanUpStore(_ context.Context, hold time.Duration) (beads.StoreCleanup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, hold)
	if s.order != nil {
		*s.order = append(*s.order, "cleanup")
	}
	cleanup := beads.StoreCleanup{Store: "/opt/calc/.beads/embeddeddolt", SizeBefore: s.size}
	if len(s.busy) > 0 {
		cleanup.Busy, cleanup.SizeAfter = s.busy, s.size
		return cleanup, nil
	}
	cleanup.StartedAt = time.Date(2026, 9, 29, 17, 0, 1, 0, time.UTC)
	cleanup.Took = 9 * time.Second
	cleanup.SizeAfter = s.after
	cleanup.Stopped = s.stopped
	cleanup.ItemsBefore = s.items
	s.size = s.after
	if s.fail != nil {
		return cleanup, s.fail
	}
	for lost := 0; lost < s.lose; lost++ {
		cleanup.Missing = append(cleanup.Missing, fmt.Sprintf("calc-%d", lost+1))
	}
	s.items -= s.lose
	cleanup.ItemsAfter, cleanup.Counted = s.items, true
	return cleanup, nil
}

func (s *fakeStore) cleanups() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.asked...)
}

func withTrackerStore(t *testing.T, f *fixture, store *fakeStore) *runstate.TrackerStoreCleanupStore {
	t.Helper()
	record, err := runstate.NewTrackerStoreCleanupStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	f.pass.Tracker, f.pass.Cleanups = store, record
	f.pass.TrackerCleanup = config.DefaultTrackerCleanup()
	return record
}

// A store whose last cleanup was recent but which has grown past the size
// bound since is cleaned up, under the configured hold, and the cleanup is
// recorded with when it ran, the size either side, and how long it took, and
// said on the pass's line.
func TestAStoreOverTheSizeBoundIsCleanedUpAndRecorded(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	store := &fakeStore{size: 700 * megabyte, after: 520 * megabyte, items: 1483}
	record := withTrackerStore(t, f, store)
	if err := record.Record(runstate.TrackerStoreCleanup{StartedAt: f.now.Add(-time.Hour), Took: time.Second, SizeBefore: 600 * megabyte, SizeAfter: 500 * megabyte}); err != nil {
		t.Fatal(err)
	}

	recorded := f.pass1(t)
	step := stepNamed(t, recorded, StepTrackerCleanup)
	if step.Outcome != runstate.StepRan {
		t.Fatalf("cleanup step = %+v, want a cleanup run", step)
	}
	for _, want := range []string{"grown 200 MB since its last cleanup left it at 500 MB, past the 128 MB bound", "from 700 MB to 520 MB in 9.0 seconds, 1483 work items before and after"} {
		if !strings.Contains(step.Detail, want) {
			t.Errorf("cleanup step = %q, want it to say %q", step.Detail, want)
		}
	}
	if asked := store.cleanups(); len(asked) != 1 || asked[0] != config.DefaultTrackerCleanupMaxHold {
		t.Errorf("cleanups asked = %v, want one under the %s hold", asked, config.DefaultTrackerCleanupMaxHold)
	}
	cleanups, err := record.Read()
	if err != nil || len(cleanups) != 2 {
		t.Fatalf("cleanups = %+v, %v, want the earlier one and this one", cleanups, err)
	}
	latest := cleanups[1]
	if latest.SizeBefore != 700*megabyte || latest.SizeAfter != 520*megabyte || latest.Took != 9*time.Second || latest.StartedAt.IsZero() || latest.Stopped {
		t.Errorf("recorded cleanup = %+v, want when, both sizes, and how long", latest)
	}
	if !latest.ItemsCounted || latest.ItemsBefore != 1483 || latest.ItemsAfter != 1483 || latest.ItemsChanged() || latest.Failed != "" {
		t.Errorf("recorded cleanup = %+v, want the work items counted either side and unchanged", latest)
	}
	if recorded.Problem != "" {
		t.Errorf("pass problem = %q, want none", recorded.Problem)
	}
	if said := f.pass.Describe(); !strings.Contains(said, "the tracker's store was last cleaned up at ") || !strings.Contains(said, "from 700 MB to 520 MB in 9.0 seconds, 1483 work items before and after") {
		t.Errorf("Describe() = %q, want the latest cleanup said in it", said)
	}
}

// A store under both bounds is left alone: nothing is run, and the step says
// how the store stands and when the next cleanup is due.
func TestAStoreUnderBothBoundsIsNotCleanedUp(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	store := &fakeStore{size: 510 * megabyte, after: 500 * megabyte}
	record := withTrackerStore(t, f, store)
	if err := record.Record(runstate.TrackerStoreCleanup{StartedAt: f.now.Add(-time.Hour), Took: time.Second, SizeBefore: 600 * megabyte, SizeAfter: 500 * megabyte}); err != nil {
		t.Fatal(err)
	}

	recorded := f.pass1(t)
	step := stepNamed(t, recorded, StepTrackerCleanup)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "the tracker's store is 510 MB, 10 MB more than its last cleanup at") || !strings.Contains(step.Detail, "the next cleanup is due at") {
		t.Errorf("cleanup step = %+v, want it skipped saying when the next is due", step)
	}
	if asked := store.cleanups(); len(asked) != 0 {
		t.Errorf("cleanups asked = %v, want none", asked)
	}
	if cleanups, err := record.Read(); err != nil || len(cleanups) != 1 {
		t.Errorf("cleanups = %+v, %v, want only the earlier one", cleanups, err)
	}
}

// The time bound brings a cleanup round however little the store has grown,
// and a store the harness has never cleaned up is cleaned up at the first
// pass.
func TestTheTimeBoundAndAStoreNeverCleanedUpBothBringACleanup(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		earlier *runstate.TrackerStoreCleanup
		want    string
	}{
		"never cleaned up": {want: "the harness has never finished cleaning it up"},
		"a day since the last": {
			earlier: &runstate.TrackerStoreCleanup{StartedAt: time.Date(2026, 9, 28, 16, 0, 0, 0, time.UTC), SizeBefore: 600 * megabyte, SizeAfter: 500 * megabyte},
			want:    "was more than 24h0m0s ago",
		},
		"only a stopped one since": {
			earlier: &runstate.TrackerStoreCleanup{StartedAt: time.Date(2026, 9, 29, 16, 0, 0, 0, time.UTC), SizeBefore: 600 * megabyte, SizeAfter: 500 * megabyte, Stopped: true},
			want:    "the harness has never finished cleaning it up",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			store := &fakeStore{size: 501 * megabyte, after: 499 * megabyte}
			record := withTrackerStore(t, f, store)
			if tc.earlier != nil {
				if err := record.Record(*tc.earlier); err != nil {
					t.Fatal(err)
				}
			}
			step := stepNamed(t, f.pass1(t), StepTrackerCleanup)
			if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, tc.want) {
				t.Errorf("cleanup step = %+v, want a cleanup because %q", step, tc.want)
			}
		})
	}
}

// A store another process has open is not cleaned up, and that is a skip
// naming the holder rather than a failure: the pass records no problem, no
// cleanup is recorded, and the next pass tries again.
func TestABusyStoreIsSkippedAndTriedAgainWithoutFailingThePass(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	store := &fakeStore{size: 700 * megabyte, after: 500 * megabyte, busy: []beads.StoreHolder{{PID: 4242, Command: "bd export -o .beads/issues.jsonl"}}}
	record := withTrackerStore(t, f, store)

	recorded := f.pass1(t)
	step := stepNamed(t, recorded, StepTrackerCleanup)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "process 4242 (bd export -o .beads/issues.jsonl)") || !strings.Contains(step.Detail, "the next pass tries again") {
		t.Errorf("cleanup step = %+v, want it skipped naming the holder", step)
	}
	if recorded.Problem != "" {
		t.Errorf("pass problem = %q, want a busy store to fail nothing", recorded.Problem)
	}
	if cleanups, err := record.Read(); err != nil || len(cleanups) != 0 {
		t.Errorf("cleanups = %+v, %v, want nothing recorded for a cleanup that never started", cleanups, err)
	}

	store.mu.Lock()
	store.busy = nil
	store.mu.Unlock()
	f.now = f.now.Add(11 * time.Minute)
	if step := stepNamed(t, f.pass1(t), StepTrackerCleanup); step.Outcome != runstate.StepRan {
		t.Errorf("cleanup step at the next pass = %+v, want the cleanup it skipped run", step)
	}
}

// A cleanup stopped at its hold is recorded as stopped and is a skip, and it
// does not count as the store cleaned up: the next pass is due one again.
func TestACleanupStoppedAtItsHoldIsRecordedAndFinishedLater(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	store := &fakeStore{size: 700 * megabyte, after: 690 * megabyte, stopped: true}
	record := withTrackerStore(t, f, store)
	f.pass.TrackerCleanup.MaxHold = config.Duration(5 * time.Second)

	recorded := f.pass1(t)
	step := stepNamed(t, recorded, StepTrackerCleanup)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "stopped once it had held the store for 5s") || !strings.Contains(step.Detail, "the next pass finishes it") {
		t.Errorf("cleanup step = %+v, want it stopped and left for the next pass", step)
	}
	if recorded.Problem != "" {
		t.Errorf("pass problem = %q, want a stopped cleanup to fail nothing", recorded.Problem)
	}
	cleanups, err := record.Read()
	if err != nil || len(cleanups) != 1 || !cleanups[0].Stopped {
		t.Fatalf("cleanups = %+v, %v, want the stopped one recorded as stopped", cleanups, err)
	}
	if asked := store.cleanups(); len(asked) != 1 || asked[0] != 5*time.Second {
		t.Errorf("cleanups asked = %v, want one under the configured 5s hold", asked)
	}

	store.mu.Lock()
	store.stopped, store.after = false, 500*megabyte
	store.mu.Unlock()
	f.now = f.now.Add(11 * time.Minute)
	if step := stepNamed(t, f.pass1(t), StepTrackerCleanup); step.Outcome != runstate.StepRan {
		t.Errorf("cleanup step at the next pass = %+v, want the stopped cleanup finished", step)
	}
}

// A cleanup after which the store holds fewer work items than before is a
// failure of the pass, said in words naming what is missing, and it is not
// tried again: every later pass fails saying why, without starting a cleanup,
// until the record of the one that changed the count is removed.
func TestACleanupThatChangesTheItemCountStopsLaterCleanups(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	store := &fakeStore{size: 700 * megabyte, after: 520 * megabyte, items: 812, lose: 2}
	record := withTrackerStore(t, f, store)

	recorded := f.pass1(t)
	step := stepNamed(t, recorded, StepTrackerCleanup)
	for _, want := range []string{"812 work items before and 810 after, calc-1, calc-2 missing", "may have lost work items", "no cleanup runs again until that is looked at and its record is removed from " + record.Path()} {
		if step.Outcome != runstate.StepFailed || !strings.Contains(step.Detail, want) {
			t.Errorf("cleanup step = %+v, want it failed saying %q", step, want)
		}
	}
	if !strings.Contains(recorded.Problem, "may have lost work items") {
		t.Errorf("pass problem = %q, want the changed count said as the pass's problem", recorded.Problem)
	}
	cleanups, err := record.Read()
	if err != nil || len(cleanups) != 1 || !cleanups[0].ItemsChanged() || cleanups[0].ItemsBefore != 812 || cleanups[0].ItemsAfter != 810 {
		t.Fatalf("cleanups = %+v, %v, want the cleanup recorded with both counts", cleanups, err)
	}

	// A day later the store is due again by time, and is still not cleaned up.
	store.mu.Lock()
	store.lose = 0
	store.mu.Unlock()
	f.now = f.now.Add(25 * time.Hour)
	later := f.pass1(t)
	step = stepNamed(t, later, StepTrackerCleanup)
	if step.Outcome != runstate.StepFailed || !strings.Contains(step.Detail, "its last cleanup, at ") || !strings.Contains(step.Detail, cleanups[0].File) || later.Problem == "" {
		t.Errorf("cleanup step a day later = %+v, problem %q, want it failed naming the record that holds cleanups back", step, later.Problem)
	}
	if asked := store.cleanups(); len(asked) != 1 {
		t.Errorf("cleanups asked = %v, want none after the one that changed the count", asked)
	}

	if err := os.Remove(filepath.Join(record.Path(), cleanups[0].File)); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(11 * time.Minute)
	if step := stepNamed(t, f.pass1(t), StepTrackerCleanup); step.Outcome != runstate.StepRan {
		t.Errorf("cleanup step once the record is removed = %+v, want a cleanup run", step)
	}
}

// A cleanup bd fails is a failure of the pass, in words, and recorded with the
// failure; the pass after it does not try again, and says the last one failed,
// and the next cleanup is tried only once one is due again.
func TestAFailedCleanupIsRecordedAndNotRetriedAtTheNextPass(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	store := &fakeStore{size: 700 * megabyte, after: 700 * megabyte, items: 812, fail: errors.New("bd gc failed with status failed and exit code 1: dolt gc: disk full")}
	record := withTrackerStore(t, f, store)

	recorded := f.pass1(t)
	step := stepNamed(t, recorded, StepTrackerCleanup)
	if step.Outcome != runstate.StepFailed || !strings.Contains(step.Detail, "the cleanup failed after 9.0 seconds: bd gc failed with status failed and exit code 1: dolt gc: disk full") || !strings.Contains(step.Detail, "it is not tried again until the next cleanup is due") {
		t.Errorf("cleanup step = %+v, want it failed in words", step)
	}
	if !strings.Contains(recorded.Problem, "disk full") {
		t.Errorf("pass problem = %q, want the failure said as the pass's problem", recorded.Problem)
	}
	cleanups, err := record.Read()
	if err != nil || len(cleanups) != 1 || !strings.Contains(cleanups[0].Failed, "disk full") || cleanups[0].ItemsBefore != 812 {
		t.Fatalf("cleanups = %+v, %v, want the failed cleanup recorded with its failure", cleanups, err)
	}

	f.now = f.now.Add(11 * time.Minute)
	step = stepNamed(t, f.pass1(t), StepTrackerCleanup)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "that cleanup failed: bd gc failed") || !strings.Contains(step.Detail, "the next cleanup is due at") {
		t.Errorf("cleanup step at the next pass = %+v, want it not retried, saying the last one failed", step)
	}
	if asked := store.cleanups(); len(asked) != 1 {
		t.Errorf("cleanups asked = %v, want the failed one only", asked)
	}

	store.mu.Lock()
	store.fail = nil
	store.mu.Unlock()
	f.now = f.now.Add(25 * time.Hour)
	if step := stepNamed(t, f.pass1(t), StepTrackerCleanup); step.Outcome != runstate.StepRan {
		t.Errorf("cleanup step a day later = %+v, want the cleanup tried again", step)
	}
}

// orderedRunner is a reconcile that says when it ran, beside the cleanup.
type orderedRunner struct {
	mu    *sync.Mutex
	order *[]string
}

func (r orderedRunner) Run(context.Context, execution.Command, execution.OutputObserver) (execution.ProcessResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.order = append(*r.order, "reconcile")
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

// The cleanup starts only once the pass's own reconcile is over, so the pass
// never keeps its own tracker calls waiting on it.
func TestTheCleanupStartsOnlyAfterReconcile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var order []string
	store := &fakeStore{size: 700 * megabyte, after: 500 * megabyte}
	store.order = &order
	withTrackerStore(t, f, store)
	f.pass.Runner = orderedRunner{mu: &store.mu, order: &order}

	f.pass1(t)
	store.mu.Lock()
	defer store.mu.Unlock()
	if strings.Join(order, ",") != "reconcile,cleanup" {
		t.Errorf("order = %v, want reconcile and then the cleanup", order)
	}
}
