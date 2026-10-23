package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// storeHolderVariable makes this test binary, run as bd, a cleanup that takes
// the store's lock and never lets it go: it names the lock file to hold, and
// storeHeldVariable the file it writes once it holds it. It is how a test puts
// a cleanup on the store that runs past its hold, which bd's own cleanup of a
// test-sized store never does.
const (
	storeHolderVariable = "YOYODYNE_BEADS_TEST_HOLD_STORE"
	storeHeldVariable   = "YOYODYNE_BEADS_TEST_STORE_HELD"
)

func TestMain(m *testing.M) {
	if lock := os.Getenv(storeHolderVariable); lock != "" {
		holdStoreLock(lock, os.Getenv(storeHeldVariable))
		return
	}
	os.Exit(m.Run())
}

// holdStoreLock takes the lock bd takes and keeps it until the process is
// killed, or for a minute, whichever comes first, so a test that dies before
// killing it leaves nothing holding a store behind it.
func holdStoreLock(lock, held string) {
	file, err := os.OpenFile(lock, os.O_RDWR, 0)
	if err != nil {
		os.Exit(2)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		os.Exit(3)
	}
	_ = os.WriteFile(held, []byte("held\n"), 0o600)
	time.Sleep(time.Minute)
	os.Exit(4)
}

// doltLock is the file Dolt locks in the store a tracker made by newTracker
// holds, which is the lock every bd process waits on.
func doltLock(t *testing.T, project string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(project, ".beads", "embeddeddolt", "*", ".dolt", "noms", "LOCK"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("the store's lock = %v, %v, want one", matches, err)
	}
	return matches[0]
}

// readable is everything a reader of the tracker can see: the whole listing,
// every item's full text, and each item's history, as bd prints them.
func readable(t *testing.T, client Client, ids []string) map[string][]byte {
	t.Helper()
	ctx := context.Background()
	seen := map[string][]byte{}
	listing, err := client.run(ctx, "list", everyStatus, unboundedListing, "--json")
	if err != nil {
		t.Fatalf("bd list error = %v", err)
	}
	seen["list"] = listing
	for _, id := range ids {
		shown, err := client.run(ctx, "show", id, "--json")
		if err != nil {
			t.Fatalf("bd show %s error = %v", id, err)
		}
		seen["show "+id] = shown
		history, err := client.run(ctx, "history", id, "--json")
		if err != nil {
			t.Fatalf("bd history %s error = %v", id, err)
		}
		seen["history "+id] = history
	}
	return seen
}

// trackerWithHistory is a tracker holding a few items, each changed more than
// once, so that its store has room for a cleanup to reclaim and history for it
// to keep.
func trackerWithHistory(t *testing.T) (string, Client, []string) {
	t.Helper()
	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()
	var ids []string
	for index := range 3 {
		created, err := client.Create(ctx, NewWorkItem{Title: fmt.Sprintf("Item %d", index), Description: "Kept whole through a cleanup.", Type: "task"})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		ids = append(ids, created.ID)
		for note := range 2 {
			if _, err := client.Update(ctx, created.ID, WorkItemChange{AppendNotes: fmt.Sprintf("Note %d on item %d.", note, index)}); err != nil {
				t.Fatalf("Update() error = %v", err)
			}
		}
	}
	if _, err := client.Complete(ctx, ids[0], "Done before the cleanup."); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	return project, client, ids
}

// TestStoreCleanupConformance holds the cleanup to what the harness promises
// of it: no item, note, or history a reader can see today is removed. The
// listing, every item's full text, and every item's history read byte for byte
// the same after a cleanup as before it, and the store it cleaned is measured
// either side.
func TestStoreCleanupConformance(t *testing.T) {
	t.Parallel()
	_, client, ids := trackerWithHistory(t)
	client.Holders = fixedHolders{}
	before := readable(t, client, ids)

	cleaned, err := client.CleanUpStore(context.Background(), conformanceTimeout)
	if err != nil {
		t.Fatalf("CleanUpStore() error = %v", err)
	}
	if !cleaned.Ran() || cleaned.Stopped || cleaned.SizeBefore <= 0 || cleaned.SizeAfter <= 0 || cleaned.Took <= 0 {
		t.Fatalf("CleanUpStore() = %+v, want a finished cleanup measured either side", cleaned)
	}
	if cleaned.SizeAfter > cleaned.SizeBefore {
		t.Errorf("CleanUpStore() grew the store from %d to %d bytes", cleaned.SizeBefore, cleaned.SizeAfter)
	}
	after := readable(t, client, ids)
	for key, was := range before {
		if !bytes.Equal(was, after[key]) {
			t.Errorf("%s after a cleanup =\n%s\nwant it as it was before:\n%s", key, after[key], was)
		}
	}
	var history []json.RawMessage
	if err := json.Unmarshal(after["history "+ids[0]], &history); err != nil || len(history) < 3 {
		t.Errorf("history of %s after a cleanup = %d entries, %v, want every change it had", ids[0], len(history), err)
	}
}

// TestStoppedStoreCleanupConformance stops bd's cleanup at holds short enough
// to land anywhere in it, and holds every outcome to the same promise: the
// store reads exactly as it did, so a cleanup stopped at its hold is one the
// next pass can finish rather than damage to repair.
func TestStoppedStoreCleanupConformance(t *testing.T) {
	t.Parallel()
	_, client, ids := trackerWithHistory(t)
	client.Holders = fixedHolders{}
	before := readable(t, client, ids)

	for _, hold := range []time.Duration{50 * time.Millisecond, 300 * time.Millisecond, 700 * time.Millisecond, 1500 * time.Millisecond} {
		cleaned, err := client.CleanUpStore(context.Background(), hold)
		if err != nil {
			t.Fatalf("CleanUpStore(%s) error = %v", hold, err)
		}
		if cleaned.Stopped && !strings.Contains(cleaned.Said, "cleaning up the tracker's store was stopped after") {
			t.Errorf("CleanUpStore(%s) said %q, want the stop said in words", hold, cleaned.Said)
		}
		after := readable(t, client, ids)
		for key, was := range before {
			if !bytes.Equal(was, after[key]) {
				t.Fatalf("%s after a cleanup stopped at %s (stopped: %t) =\n%s\nwant it as it was:\n%s", key, hold, cleaned.Stopped, after[key], was)
			}
		}
	}
}

// TestCallsDuringAStoreCleanupConformance runs tracker calls while a cleanup
// holds the store's lock and runs past its hold, and holds each of them to its
// own limit: the cleanup is stopped when its hold runs out, and every call that
// queued behind it answers, inside its limit, having waited for it.
//
// The cleanup is this test binary standing in for bd, holding the lock bd's
// cleanup takes and never letting it go, because bd's own cleanup of a store
// this size finishes long before any hold runs out. The calls are real bd.
func TestCallsDuringAStoreCleanupConformance(t *testing.T) {
	project, _, ids := trackerWithHistory(t)

	// The same calls with no cleanup under way say how long bd itself takes on
	// this machine now. A loaded machine can take tens of seconds over calls
	// that queue on one another, and that is bd's time rather than the
	// cleanup's, so it is added to the limit and only the cleanup's own wait is
	// held to the 45s every tracker call is given.
	unhindered := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout, listingPause: func(context.Context, time.Duration) bool { return false }}
	baselineStarted := time.Now()
	var baseline sync.WaitGroup
	for _, id := range ids {
		baseline.Add(1)
		go func() {
			defer baseline.Done()
			if _, err := unhindered.Show(context.Background(), id); err != nil {
				t.Errorf("bd show %s with no cleanup under way error = %v", id, err)
			}
		}()
	}
	baseline.Add(1)
	go func() {
		defer baseline.Done()
		if _, err := unhindered.List(context.Background(), ""); err != nil {
			t.Errorf("bd list with no cleanup under way error = %v", err)
		}
	}()
	baseline.Wait()
	if t.Failed() {
		t.FailNow()
	}
	bdTakes := time.Since(baselineStarted)

	held := filepath.Join(t.TempDir(), "held")
	t.Setenv(storeHolderVariable, doltLock(t, project))
	t.Setenv(storeHeldVariable, held)

	const hold = 4 * time.Second
	limit := 45*time.Second + 2*bdTakes
	cleaner := Client{Runner: execution.OSProcessRunner{}, Binary: os.Args[0], Dir: project, Holders: fixedHolders{}}
	type ended struct {
		cleanup StoreCleanup
		err     error
	}
	cleanedUp := make(chan ended, 1)
	go func() {
		cleanup, err := cleaner.CleanUpStore(context.Background(), hold)
		cleanedUp <- ended{cleanup, err}
	}()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(held); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stand-in cleanup never took the store's lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	caller := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: limit, listingPause: func(context.Context, time.Duration) bool { return false }}
	started := time.Now()
	type answered struct {
		call string
		took time.Duration
		err  error
	}
	answers := make(chan answered, len(ids)+1)
	var calls sync.WaitGroup
	for _, id := range ids {
		calls.Add(1)
		go func() {
			defer calls.Done()
			_, err := caller.Show(context.Background(), id)
			answers <- answered{"bd show " + id, time.Since(started), err}
		}()
	}
	calls.Add(1)
	go func() {
		defer calls.Done()
		_, err := caller.List(context.Background(), "")
		answers <- answered{"bd list", time.Since(started), err}
	}()
	calls.Wait()
	close(answers)

	waitedOnTheCleanup := false
	for answer := range answers {
		if answer.err != nil {
			t.Errorf("%s during a cleanup error = %v, want it answered inside its %s limit", answer.call, answer.err, limit)
			continue
		}
		if answer.took >= limit {
			t.Errorf("%s during a cleanup took %s, past its own %s limit", answer.call, answer.took, limit)
		}
		if answer.took >= time.Second {
			waitedOnTheCleanup = true
		}
	}
	if !waitedOnTheCleanup {
		t.Error("no call waited on the cleanup, so nothing here held one to its limit while a cleanup was under way")
	}
	stopped := <-cleanedUp
	if stopped.err != nil || !stopped.cleanup.Stopped {
		t.Errorf("CleanUpStore() = %+v, %v, want the cleanup stopped at its %s hold", stopped.cleanup, stopped.err, hold)
	}
	if took := stopped.cleanup.Took; took < hold || took > hold+15*time.Second {
		t.Errorf("the cleanup held the store for %s, want it stopped once its %s hold ran out", took, hold)
	}
}

// TestTimeoutHolderConformance makes a real call time out behind a held store
// lock, and holds the account of it to naming the holder: this process, which
// took the lock bd waits on.
func TestTimeoutHolderConformance(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skipf("SKIPPED, not passed: lsof is not installed, so no holder can be named: %v", err)
	}
	project, _, ids := trackerWithHistory(t)
	lock, err := os.OpenFile(doltLock(t, project), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	caller := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: 3 * time.Second, listingPause: func(context.Context, time.Duration) bool { return false }}
	_, err = caller.Show(context.Background(), ids[0])
	if err == nil || !timedOut(err) {
		t.Fatalf("Show() behind a held lock error = %v, want it stopped at its limit", err)
	}
	// Holders are named in process-number order, so another process that has
	// the store open at the same moment may be named ahead of this one; what is
	// held to is that this one is named among them.
	said, want := "when it was stopped, the tracker's store was held open by ", fmt.Sprintf("process %d (this one)", os.Getpid())
	if at := strings.Index(err.Error(), said); at < 0 || !strings.Contains(err.Error()[at:], want) {
		t.Fatalf("Show() error = %q, want it to say %q naming %q", err, said, want)
	}
	// Since when is ps's to say, and a sandbox may refuse ps; then the account
	// says that instead, which is the other answer the record may give.
	if !strings.Contains(err.Error(), "running since ") && !strings.Contains(err.Error(), "since when could not be read: ps ") {
		t.Errorf("Show() error = %q, want it to say since when the holder had been running, or why that could not be read", err)
	}
}
