package runstate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// The design's races run against the run store and the memory store, which
// write through the confined writer with the state root as the root, as the
// conversation store's do in conversation_confinement_test.go. The tests that
// set a hook do not run in parallel, because the hook sees every confined
// write in the process.

// stateBase is a state root and a directory beside it that is outside it.
func stateBase(t *testing.T) (state, outside string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state, outside = filepath.Join(base, "state"), filepath.Join(base, "outside")
	for _, directory := range []string{state, outside} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return state, outside
}

// moveOutAt moves directory outside the state root the first time a change in
// it reaches point, for an entry whose name contains name when name is not
// empty. It keeps what the directory held when that change began, before any
// temporary file of the change's own, which is what it must hold again once
// the refused change is undone.
func moveOutAt(t *testing.T, point repowrite.Point, directory, name, moved string) (ran *bool, before *string) {
	return moveOutBetween(t, name, point, directory, name, moved)
}

// moveOutBetween is moveOutAt for a change whose first step names another
// entry than the one it ends on, as a move names its source before its
// destination: what the directory held is kept when a change to begins starts.
func moveOutBetween(t *testing.T, begins string, point repowrite.Point, directory, name, moved string) (ran *bool, before *string) {
	t.Helper()
	ran, before = new(bool), new(string)
	began := false
	t.Cleanup(repowrite.SetTestHook(func(event repowrite.HookEvent) {
		if *ran || event.Directory != directory {
			return
		}
		if !began && (begins == "" || strings.Contains(event.Name, begins)) && (event.Point == repowrite.PointBeforeChange || event.Point == point) {
			began = true
			*before = listingWithoutTemporaries(t, directory)
		}
		if event.Point != point || (name != "" && !strings.Contains(event.Name, name)) {
			return
		}
		*ran = true
		if err := os.Rename(directory, moved); err != nil {
			t.Errorf("move the directory out of the state root: %v", err)
		}
	}))
	return ran, before
}

func listingWithoutTemporaries(t *testing.T, directory string) string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("list %s: %v", directory, err)
	}
	var kept []string
	for _, entry := range entries {
		if repowrite.IsTemporaryFile(entry.Name()) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		kept = append(kept, entry.Name()+"="+string(content))
	}
	return strings.Join(kept, "\n")
}

type runStoreFixture struct {
	store   *Store
	state   State
	outside string
}

// newRunStoreFixture is a run store whose directory already holds one run,
// its lease, its holder stamp, and one event.
func newRunStoreFixture(t *testing.T) runStoreFixture {
	t.Helper()
	root, outside := stateBase(t)
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.AppendEvent(conversationEvent(t, Conversation{ConversationID: state.RunID}, 1, "")); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	lease, held, err := store.takeLease(context.Background(), state.RunID)
	if err != nil || !held {
		t.Fatalf("takeLease() = %v, %v", held, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	return runStoreFixture{store: store, state: state, outside: outside}
}

func runStoreWriters() []struct {
	name  string
	write func(t *testing.T, f runStoreFixture) error
} {
	return []struct {
		name  string
		write func(t *testing.T, f runStoreFixture) error
	}{
		{"take the reservation lock", func(t *testing.T, f runStoreFixture) error {
			unlock, err := f.store.lockReservations(context.Background())
			if err == nil {
				unlock()
			}
			return err
		}},
		{"take a run's lease", func(t *testing.T, f runStoreFixture) error {
			lease, _, err := f.store.takeLease(context.Background(), f.state.RunID)
			if err == nil && lease != nil {
				err = lease.Release()
			}
			return err
		}},
		{"create a run", func(t *testing.T, f runStoreFixture) error {
			return f.store.Create(testState(t, StatusPending))
		}},
		{"replace a run's record", func(t *testing.T, f runStoreFixture) error {
			return f.store.writeState(f.state)
		}},
		{"append a run event", func(t *testing.T, f runStoreFixture) error {
			return f.store.AppendEvent(conversationEvent(t, Conversation{ConversationID: f.state.RunID}, 2, ""))
		}},
		{"take the promotion lease", func(t *testing.T, f runStoreFixture) error {
			lease, err := f.store.LeasePromotion(context.Background(), "main")
			if err == nil {
				err = lease.Release()
			}
			return err
		}},
		{"take the landing lease", func(t *testing.T, f runStoreFixture) error {
			lease, err := f.store.LeaseLanding(context.Background(), "main", time.Second, nil)
			if err == nil {
				err = lease.Release()
			}
			return err
		}},
		{"take the account rotation lease", func(t *testing.T, f runStoreFixture) error {
			lease, err := f.store.LeaseRotation(context.Background())
			if err == nil {
				err = lease.Release()
			}
			return err
		}},
		{"stamp a run's holder", func(t *testing.T, f runStoreFixture) error {
			path, err := f.store.holderPath(f.state.RunID)
			if err != nil {
				return err
			}
			return f.store.stampRunHolder(path, WorkerIdentity{PID: os.Getpid()})
		}},
	}
}

// The run store's directory moved out of the state root on either side of a
// change: every writer refuses, and the directory now outside holds what it
// held when the change began.
func TestRunStoreWritersRefuseAndUndoWhenTheirDirectoryLeavesTheStateRoot(t *testing.T) {
	for _, point := range []repowrite.Point{repowrite.PointBeforeChange, repowrite.PointAfterChange} {
		for _, writer := range runStoreWriters() {
			t.Run(string(point)+"/"+writer.name, func(t *testing.T) {
				f := newRunStoreFixture(t)
				moved := filepath.Join(f.outside, "runs")
				ran, before := moveOutAt(t, point, f.store.Root(), "", moved)
				err := writer.write(t, f)
				if !*ran {
					t.Fatalf("%s never reached %s (error %v)", writer.name, point, err)
				}
				wantConversationRefusal(t, err, repowrite.ReasonMoved)
				if after := listingWithoutTemporaries(t, moved); after != *before {
					t.Fatalf("the run directory outside the state root changed:\n before: %s\n after:  %s", *before, after)
				}
			})
		}
	}
}

// A run's event log and lease with a second link from outside are refused, and
// a run record linked from outside is replaced without changing the outside
// link.
func TestRunStoreFilesLinkedFromOutsideAreNeverWrittenThrough(t *testing.T) {
	t.Parallel()
	f := newRunStoreFixture(t)
	events, err := f.store.eventPath(f.state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.store.leasePath(f.state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	record, err := f.store.statePath(f.state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{events, lease, record} {
		if err := os.Link(path, filepath.Join(f.outside, filepath.Base(path))); err != nil {
			t.Fatal(err)
		}
	}
	before := conversationListing(t, f.outside)
	wantConversationRefusal(t, f.store.AppendEvent(conversationEvent(t, Conversation{ConversationID: f.state.RunID}, 2, "")), repowrite.ReasonLinked)
	if taken, _, err := f.store.takeLease(context.Background(), f.state.RunID); err == nil {
		taken.Release()
		t.Fatal("a run lease with a second link outside the state root was taken")
	} else {
		wantConversationRefusal(t, err, repowrite.ReasonLinked)
	}
	f.state.Branch = "yoyodyne/changed/01234567"
	if err := f.store.writeState(f.state); err != nil {
		t.Fatalf("writeState() error = %v", err)
	}
	if after := conversationListing(t, f.outside); after != before {
		t.Fatalf("the outside links changed:\n before: %s\n after:  %s", before, after)
	}
}

func newMemoryFixture(t *testing.T) (*MemoryStore, string, string) {
	t.Helper()
	root, outside := stateBase(t)
	store := newMemoryStore(t, root)
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	return store, store.Root(), outside
}

func rememberAnother(store *MemoryStore) error {
	revision := testMemoryRevision()
	revision.Memory = "another-memory"
	_, err := store.Remember(context.Background(), revision)
	return err
}

// The memory directory moved out of the state root around each of the memory
// store's changes: the lock, the append, the tip and the archive move. The
// change is refused and undone, so the directory now outside holds what it
// held when the change began.
func TestMemoryWritersRefuseAndUndoWhenTheirDirectoryLeavesTheStateRoot(t *testing.T) {
	for _, row := range []struct {
		name  string
		point repowrite.Point
		entry string
		// refused is whether Remember reports the refusal: a tip that cannot be
		// recorded is one the next write rebuilds, so Remember does not.
		refused bool
		roll    bool
		// begins is the entry the change's first step names, where it is not
		// entry itself.
		begins string
	}{
		{"the lock, before", repowrite.PointBeforeChange, memoryLockSuffix, true, false, ""},
		{"the lock, after", repowrite.PointAfterChange, memoryLockSuffix, true, false, ""},
		{"the append, before", repowrite.PointBeforeChange, memoryLogSuffix, true, false, ""},
		{"the append, after", repowrite.PointAfterChange, memoryLogSuffix, true, false, ""},
		{"the tip, before", repowrite.PointBeforeChange, memoryTipSuffix, false, false, ""},
		{"the tip, after", repowrite.PointAfterChange, memoryTipSuffix, false, false, ""},
		{"the archive move, after", repowrite.PointAfterChange, memoryArchiveMiddle, true, true, memoryLogSuffix},
	} {
		t.Run(row.name, func(t *testing.T) {
			store, directory, outside := newMemoryFixture(t)
			if row.roll {
				// A second revision of the same memory leaves a line the live
				// set no longer needs, which is what makes a roll worth making.
				revision := testMemoryRevision()
				revision.Text = "the operator reads reports in the morning"
				if _, err := store.Remember(context.Background(), revision); err != nil {
					t.Fatalf("Remember() error = %v", err)
				}
				store.rollAt = 1
			}
			moved := filepath.Join(outside, "memory")
			begins := row.begins
			if begins == "" {
				begins = row.entry
			}
			ran, before := moveOutBetween(t, begins, row.point, directory, row.entry, moved)
			err := rememberAnother(store)
			if !*ran {
				t.Fatalf("the write never reached %s for %s (error %v)", row.point, row.entry, err)
			}
			if row.refused {
				wantConversationRefusal(t, err, repowrite.ReasonMoved)
			}
			if after := listingWithoutTemporaries(t, moved); after != *before {
				t.Fatalf("the memory directory outside the state root changed:\n before: %s\n after:  %s", *before, after)
			}
		})
	}
}

// A memory log or lock with a second link from outside is never written
// through: the lock is refused, and a log linked after it was opened stops
// before its first batch.
func TestMemoryFilesLinkedFromOutsideAreRefused(t *testing.T) {
	t.Run("the lock", func(t *testing.T) {
		store, directory, outside := newMemoryFixture(t)
		lock := filepath.Join(directory, "product-manager"+memoryLockSuffix)
		if err := os.Link(lock, filepath.Join(outside, "lock")); err != nil {
			t.Fatal(err)
		}
		wantConversationRefusal(t, rememberAnother(store), repowrite.ReasonLinked)
	})
	t.Run("the log, linked once it is open", func(t *testing.T) {
		store, directory, outside := newMemoryFixture(t)
		log := filepath.Join(directory, "product-manager"+memoryLogSuffix)
		stolen := filepath.Join(outside, "stolen")
		ran := hookConversationOnce(t, repowrite.PointAfterChange, directory, func() {
			if err := os.Link(log, stolen); err != nil {
				t.Errorf("link the log from outside: %v", err)
			}
		})
		before, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		err = rememberAnother(store)
		if !*ran {
			t.Fatal("the append never opened the log")
		}
		wantConversationRefusal(t, err, repowrite.ReasonLinked)
		if seen, err := os.ReadFile(stolen); err != nil || string(seen) != string(before) {
			t.Fatalf("the outside link sees %d bytes, want the log as it was before (%d): %v", len(seen), len(before), err)
		}
	})
}

// A torn end is cut by publishing what is kept as a new file, so a link to the
// log from outside the state root keeps exactly what it had.
func TestMemoryTornEndIsCutWithoutChangingAnOutsideLink(t *testing.T) {
	t.Parallel()
	store, directory, outside := newMemoryFixture(t)
	log := filepath.Join(directory, "product-manager"+memoryLogSuffix)
	appendToMemoryLog(t, log, []byte(`{"torn`))
	kept := filepath.Join(outside, "kept")
	if err := os.Link(log, kept); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(kept)
	if err != nil {
		t.Fatal(err)
	}
	if err := rememberAnother(store); err != nil {
		t.Fatalf("Remember() after a torn end error = %v", err)
	}
	if after, err := os.ReadFile(kept); err != nil || string(after) != string(before) {
		t.Fatalf("the outside link changed: %q, %v", after, err)
	}
	if current, err := os.ReadFile(log); err != nil || strings.Contains(string(current), `{"torn`) {
		t.Fatalf("the torn end was not cut from the log: %q, %v", current, err)
	}
}

// mergeQueueStreamFixture is a merge queue store whose event stream for one
// generation already holds one event.
func mergeQueueStreamFixture(t *testing.T) (store *MergeQueueStore, stream, events, outside string) {
	t.Helper()
	root, outside := stateBase(t)
	store, err := NewMergeQueueStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	stream = "mqe-0123456789abcdef0123456789abcdef-generation-1"
	if err := store.AppendEvent(conversationEvent(t, Conversation{ConversationID: stream}, 1, "")); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	return store, stream, filepath.Join(store.Root(), "events"), outside
}

// The merge queue's event stream is appended to through the checked writer: its
// directory moved out of the state root on either side of an append refuses it
// and leaves the directory as it was.
func TestMergeQueueEventStreamRefusesAndUndoesWhenItsDirectoryLeavesTheStateRoot(t *testing.T) {
	for _, point := range []repowrite.Point{repowrite.PointBeforeChange, repowrite.PointAfterChange} {
		t.Run(string(point), func(t *testing.T) {
			store, stream, events, outside := mergeQueueStreamFixture(t)
			moved := filepath.Join(outside, "events")
			ran, before := moveOutAt(t, point, events, "", moved)
			err := store.AppendEvent(conversationEvent(t, Conversation{ConversationID: stream}, 2, ""))
			if !*ran {
				t.Fatalf("the append never reached %s (error %v)", point, err)
			}
			wantConversationRefusal(t, err, repowrite.ReasonMoved)
			if after := listingWithoutTemporaries(t, moved); after != *before {
				t.Fatalf("the event directory outside the state root changed:\n before: %s\n after:  %s", *before, after)
			}
		})
	}
}

// A link to the merge queue's event stream added from outside between two
// batches of a large event stops the append after the batch written before
// the link appeared.
func TestMergeQueueEventStreamStopsAtALinkAddedBetweenBatches(t *testing.T) {
	store, stream, events, outside := mergeQueueStreamFixture(t)
	log := filepath.Join(events, stream+".jsonl")
	stolen := filepath.Join(outside, "stolen.jsonl")
	ran := hookConversationOnce(t, repowrite.PointBetweenBatches, events, func() {
		if err := os.Link(log, stolen); err != nil {
			t.Errorf("link the event stream from outside: %v", err)
		}
	})
	before, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	err = store.AppendEvent(conversationEvent(t, Conversation{ConversationID: stream}, 2, strings.Repeat("x", 200<<10)))
	if !*ran {
		t.Fatal("a large event was appended in one batch")
	}
	wantConversationRefusal(t, err, repowrite.ReasonLinked)
	if seen, err := os.ReadFile(stolen); err != nil || len(seen) != len(before)+64<<10 {
		t.Fatalf("the outside link sees %d bytes, want the stream before and one batch (%d): %v", len(seen), len(before)+64<<10, err)
	}
}

// A store that replaces one record, appends to a log and takes a lock, the
// sweep record, with its directory moved out of the state root on either side
// of each change: every change is refused and undone.
func TestSweepStoreWritersRefuseAndUndoWhenTheirDirectoryLeavesTheStateRoot(t *testing.T) {
	writers := []struct {
		name  string
		write func(store *SweepStore) error
	}{
		{"append a sweep", func(store *SweepStore) error {
			at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
			return store.Append(Sweep{Task: "maintenance", StartedAt: at, EndedAt: at.Add(time.Minute), Problem: "reconcile failed", Steps: []SweepStep{{Name: "reconcile", Outcome: StepFailed, Detail: "reconcile failed"}}})
		}},
		{"replace a task's claim", func(store *SweepStore) error {
			at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
			return store.save("maintenance", SweepClaim{SchemaVersion: SweepSchemaVersion, ProductID: "yoyodyne", Task: "maintenance", FiredAt: at, Firings: 1, UpdatedAt: at})
		}},
		{"take a task's lock", func(store *SweepStore) error {
			unlock, err := store.lock(context.Background(), "maintenance")
			if err == nil {
				unlock()
			}
			return err
		}},
	}
	for _, point := range []repowrite.Point{repowrite.PointBeforeChange, repowrite.PointAfterChange} {
		for _, writer := range writers {
			t.Run(string(point)+"/"+writer.name, func(t *testing.T) {
				f := newRunStoreFixture(t)
				store := f.store.Sweeps()
				if err := writers[0].write(store); err != nil {
					t.Fatalf("the first sweep error = %v", err)
				}
				moved := filepath.Join(f.outside, "sweeps")
				ran, before := moveOutAt(t, point, store.Root(), "", moved)
				err := writer.write(store)
				if !*ran {
					t.Fatalf("%s never reached %s (error %v)", writer.name, point, err)
				}
				wantConversationRefusal(t, err, repowrite.ReasonMoved)
				if after := listingWithoutTemporaries(t, moved); after != *before {
					t.Fatalf("the sweep directory outside the state root changed:\n before: %s\n after:  %s", *before, after)
				}
			})
		}
	}
}

// A lease taken by path declares the lease's own directory as its root, so a
// lease file that is a link, or has a second link from outside, is refused
// rather than locked.
func TestALeaseTakenByPathRefusesALinkedLeaseFile(t *testing.T) {
	t.Parallel()
	root, outside := stateBase(t)
	leases := filepath.Join(root, "leases")
	if err := os.MkdirAll(leases, 0o700); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(outside, "shared.lock")
	if err := os.WriteFile(shared, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(shared, filepath.Join(leases, "linked.lease")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(leases, "symlinked.lease")); err != nil {
		t.Fatal(err)
	}
	for name, reason := range map[string]repowrite.Reason{"linked.lease": repowrite.ReasonLinked, "symlinked.lease": repowrite.ReasonFinalLink} {
		lease, _, err := TryLeasePath(filepath.Join(leases, name), "a test lease")
		if err == nil {
			lease.Release()
		}
		wantConversationRefusal(t, err, reason)
	}
}
