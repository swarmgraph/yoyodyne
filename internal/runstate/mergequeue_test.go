package runstate

// The merge queue's admissions and its worker's lease. What these hold is the
// part a later slice builds on without looking again: every admission gets one
// order and keeps it, a retry is never a second entry, a write that may not
// have landed is read back before anybody is told, and one process works a
// queue at a time.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// mergeQueueWorkerRootEnv hands the state root to the child process that
// stands in for a worker in another harness process.
const mergeQueueWorkerRootEnv = "YOYODYNE_TEST_MERGE_QUEUE_WORKER_ROOT"

var mainQueue = MergeQueueKey{Repository: "yoyodyne", TargetBranch: "main"}

func TestConcurrentAdmissionsEachGetOneOrderAndKeepIt(t *testing.T) {
	t.Parallel()

	// Separate stores over one root are separate processes as far as the record
	// lock is concerned, so this is the contention the lock is for.
	root := t.TempDir()
	const admissions = 12
	asked := make([]MergeQueueAdmission, admissions)
	for i := range asked {
		asked[i] = testAdmission(t, mainQueue)
	}
	entries := make([]MergeQueueEntry, admissions)
	errs := make([]error, admissions)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := range asked {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			var admitted bool
			entries[i], admitted, errs[i] = newMergeQueueStore(t, root).Admit(context.Background(), asked[i])
			if errs[i] == nil && !admitted {
				errs[i] = errors.New("a first admission reported an entry already there")
			}
		}()
	}
	close(start)
	wait.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("Admit() error = %v", err)
	}

	recorded, err := newMergeQueueStore(t, root).Entries(mainQueue)
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if len(recorded) != admissions {
		t.Fatalf("Entries() = %d entries, want %d", len(recorded), admissions)
	}
	byRun := make(map[string]MergeQueueEntry, admissions)
	for i, entry := range recorded {
		if entry.Order != uint64(i+1) || entry.PredecessorOrder != uint64(i) {
			t.Fatalf("entry %d has order %d after %d, want %d after %d", i, entry.Order, entry.PredecessorOrder, i+1, i)
		}
		byRun[entry.RunID] = entry
	}
	for i, entry := range entries {
		if byRun[asked[i].RunID] != entry {
			t.Fatalf("Admit() reported %#v, but the record holds %#v", entry, byRun[asked[i].RunID])
		}
	}
}

func TestRepeatedAdmissionIsTheEntryAlreadyThere(t *testing.T) {
	t.Parallel()

	// Racing the same admission against itself is a retry from several places
	// at once, and is still one entry with one order.
	root := t.TempDir()
	first := testAdmission(t, mainQueue)
	if _, _, err := newMergeQueueStore(t, root).Admit(context.Background(), first); err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	same := testAdmission(t, mainQueue)
	const retries = 8
	reported := make([]MergeQueueEntry, retries)
	admitted := make([]bool, retries)
	errs := make([]error, retries)
	var wait sync.WaitGroup
	for i := range retries {
		wait.Add(1)
		go func() {
			defer wait.Done()
			retry := same
			// A retry made later carries a later time, and is still the same.
			retry.At = same.At.Add(time.Duration(i) * time.Minute)
			reported[i], admitted[i], errs[i] = newMergeQueueStore(t, root).Admit(context.Background(), retry)
		}()
	}
	wait.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	newly := 0
	for i := range retries {
		if admitted[i] {
			newly++
		}
		if reported[i] != reported[0] {
			t.Fatalf("retries reported %#v and %#v", reported[0], reported[i])
		}
	}
	if newly != 1 || reported[0].Order != 2 || reported[0].RunID != same.RunID {
		t.Fatalf("%d of the retries admitted, entry %#v; want one admission at order 2", newly, reported[0])
	}
	recorded, err := newMergeQueueStore(t, root).Entries(mainQueue)
	if err != nil || len(recorded) != 2 {
		t.Fatalf("Entries() = %d entries, %v; want 2", len(recorded), err)
	}
}

func TestAnAdmittedEntryIsNeverRewritten(t *testing.T) {
	t.Parallel()

	// The same run with anything else changed is not a retry. The earlier entry
	// stands and the caller is told what it records.
	store := newMergeQueueStore(t, t.TempDir())
	original := testAdmission(t, mainQueue)
	entry, _, err := store.Admit(context.Background(), original)
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	changes := map[string]func(*MergeQueueAdmission){
		"head":        func(a *MergeQueueAdmission) { a.ApprovedHead = strings.Repeat("b", 40) },
		"title":       func(a *MergeQueueAdmission) { a.WorkItemTitle = "Another title" },
		"work item":   func(a *MergeQueueAdmission) { a.WorkItemID = "yoyodyne-other" },
		"publication": func(a *MergeQueueAdmission) { a.Publication = "https://example.test/pull/2" },
		"policy":      func(a *MergeQueueAdmission) { a.IntegrationPolicy = "local" },
		"mode":        func(a *MergeQueueAdmission) { a.Mode = MergeQueueForge },
	}
	for name, change := range changes {
		conflicting := original
		change(&conflicting)
		_, _, err := store.Admit(context.Background(), conflicting)
		var conflict MergeQueueConflictError
		if !errors.As(err, &conflict) || conflict.Admitted != entry {
			t.Fatalf("Admit() with another %s error = %v, want the conflict naming the entry", name, err)
		}
	}
	recorded, err := store.Entries(mainQueue)
	if err != nil || len(recorded) != 1 || recorded[0] != entry {
		t.Fatalf("Entries() = %#v, %v; want only the original entry", recorded, err)
	}
}

func TestAdmissionsSurviveARestart(t *testing.T) {
	t.Parallel()

	// A store built after the first one is gone is the harness restarted: it
	// reads every entry with every field, continues the order rather than
	// restarting it, and still knows a retry when it sees one.
	root := t.TempDir()
	first := testAdmission(t, mainQueue)
	before, _, err := newMergeQueueStore(t, root).Admit(context.Background(), first)
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}

	restarted := newMergeQueueStore(t, root)
	recorded, err := restarted.Entries(mainQueue)
	if err != nil || len(recorded) != 1 || recorded[0] != before {
		t.Fatalf("Entries() after a restart = %#v, %v; want %#v", recorded, err, before)
	}
	again, admitted, err := restarted.Admit(context.Background(), first)
	if err != nil || admitted || again != before {
		t.Fatalf("Admit() of the same run after a restart = %#v, %t, %v; want the entry already there", again, admitted, err)
	}
	second, _, err := restarted.Admit(context.Background(), testAdmission(t, mainQueue))
	if err != nil || second.Order != 2 || second.PredecessorOrder != 1 {
		t.Fatalf("Admit() after a restart = %#v, %v; want order 2 after 1", second, err)
	}
}

func TestASaveThatLandedButFailedIsReadBackAsAdmitted(t *testing.T) {
	t.Parallel()

	// The rename can succeed and the directory sync behind it fail. The record
	// says the admission landed, so that is what the caller is told.
	store := newMergeQueueStore(t, t.TempDir())
	store.save = func(queue *repowrite.PinnedRoot, encoded []byte) error {
		return errors.Join(writeQueue(queue, encoded), errors.New("sync failed"))
	}
	entry, admitted, err := store.Admit(context.Background(), testAdmission(t, mainQueue))
	if err != nil || !admitted || entry.Order != 1 {
		t.Fatalf("Admit() = %#v, %t, %v; want the landed entry admitted", entry, admitted, err)
	}
	assertQueue(t, store, entry)
}

func TestASaveThatDidNotLandIsReportedAndRetriedWithoutLosingTheOrder(t *testing.T) {
	t.Parallel()

	// A write that never reached the record is a failure the readback confirms,
	// and a retry assigns the order the failed one would have taken rather than
	// the one after it.
	root := t.TempDir()
	store := newMergeQueueStore(t, root)
	admission := testAdmission(t, mainQueue)
	store.save = func(*repowrite.PinnedRoot, []byte) error { return errors.New("disk full") }
	if _, _, err := store.Admit(context.Background(), admission); err == nil || errors.Is(err, ErrMergeQueueSaveUncertain) || !strings.Contains(err.Error(), "not saved") {
		t.Fatalf("Admit() error = %v, want the save failure confirmed by readback", err)
	}
	if recorded, err := store.Entries(mainQueue); err != nil || len(recorded) != 0 {
		t.Fatalf("Entries() = %#v, %v; want none", recorded, err)
	}
	entry, admitted, err := newMergeQueueStore(t, root).Admit(context.Background(), admission)
	if err != nil || !admitted || entry.Order != 1 || entry.PredecessorOrder != 0 {
		t.Fatalf("retried Admit() = %#v, %t, %v; want order 1", entry, admitted, err)
	}
}

func TestAnUncertainSaveIsSettledByReadingItBackBeforeTheRetry(t *testing.T) {
	t.Parallel()

	// A save that failed and a readback that failed too say nothing about
	// whether the entry is there, and the caller is told exactly that. The
	// retry reads the record before it writes, finds the entry the uncertain
	// save did land, and admits nothing more.
	root := t.TempDir()
	store := newMergeQueueStore(t, root)
	admission := testAdmission(t, mainQueue)
	store.save = func(queue *repowrite.PinnedRoot, encoded []byte) error {
		return errors.Join(writeQueue(queue, encoded), errors.New("connection to the disk lost"))
	}
	store.readback = func(*repowrite.PinnedRoot) ([]byte, error) { return nil, errors.New("still lost") }
	if _, _, err := store.Admit(context.Background(), admission); !errors.Is(err, ErrMergeQueueSaveUncertain) {
		t.Fatalf("Admit() error = %v, want the save named uncertain", err)
	}

	retried := admission
	retried.At = admission.At.Add(time.Hour)
	entry, admitted, err := newMergeQueueStore(t, root).Admit(context.Background(), retried)
	if err != nil {
		t.Fatalf("retried Admit() error = %v", err)
	}
	if admitted || entry.Order != 1 || !entry.AdmittedAt.Equal(admission.At) || entry.ApprovedHead != admission.ApprovedHead ||
		entry.WorkItemTitle != admission.WorkItemTitle || entry.Publication != admission.Publication {
		t.Fatalf("retried Admit() = %#v, %t; want the entry the uncertain save landed, unchanged", entry, admitted)
	}
	assertQueue(t, newMergeQueueStore(t, root), entry)
}

func TestAnAdmissionRecordsEveryField(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	admission := testAdmission(t, mainQueue)
	entry, _, err := store.Admit(context.Background(), admission)
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	if !mergeQueueEntryIDPattern.MatchString(entry.EntryID) || entry.ProductID != "yoyodyne" ||
		entry.Key() != admission.Key || entry.WorkItemID != admission.WorkItemID || entry.WorkItemTitle != admission.WorkItemTitle ||
		entry.RunID != admission.RunID || entry.Publication != admission.Publication || entry.ApprovedHead != admission.ApprovedHead ||
		entry.IntegrationPolicy != admission.IntegrationPolicy || entry.Mode != admission.Mode ||
		!entry.AdmittedAt.Equal(admission.At) || entry.Order != 1 || entry.PredecessorOrder != 0 {
		t.Fatalf("Admit() = %#v, want every field of %#v", entry, admission)
	}
}

func TestAnAdmissionThatCannotBeRecordedAsGivenIsRefused(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	refusals := map[string]func(*MergeQueueAdmission){
		"no repository":   func(a *MergeQueueAdmission) { a.Key.Repository = "" },
		"no branch":       func(a *MergeQueueAdmission) { a.Key.TargetBranch = "refs/heads/main" },
		"no work item":    func(a *MergeQueueAdmission) { a.WorkItemID = "" },
		"no title":        func(a *MergeQueueAdmission) { a.WorkItemTitle = "" },
		"title with line": func(a *MergeQueueAdmission) { a.WorkItemTitle = "one\ntwo" },
		"padded title":    func(a *MergeQueueAdmission) { a.WorkItemTitle = " title" },
		"no run":          func(a *MergeQueueAdmission) { a.RunID = "run" },
		"short head":      func(a *MergeQueueAdmission) { a.ApprovedHead = "abc123" },
		"no policy":       func(a *MergeQueueAdmission) { a.IntegrationPolicy = "" },
		"unknown mode":    func(a *MergeQueueAdmission) { a.Mode = "github" },
		"long title":      func(a *MergeQueueAdmission) { a.WorkItemTitle = strings.Repeat("a", MaxMergeQueueTextBytes+1) },
	}
	for name, change := range refusals {
		admission := testAdmission(t, mainQueue)
		change(&admission)
		if _, _, err := store.Admit(context.Background(), admission); err == nil {
			t.Fatalf("Admit() with %s was accepted", name)
		}
	}
}

func TestDifferentQueuesAreIndependent(t *testing.T) {
	t.Parallel()

	// Each repository and target branch is its own queue: its own orders from
	// 1, and its own worker, with every worker's lease held at once. The
	// branches `release/1.2` and `release-1.2` are different branches.
	root := t.TempDir()
	keys := []MergeQueueKey{
		mainQueue,
		{Repository: "yoyodyne", TargetBranch: "release/1.2"},
		{Repository: "yoyodyne", TargetBranch: "release-1.2"},
		{Repository: "another", TargetBranch: "main"},
	}
	store := newMergeQueueStore(t, root)
	for _, key := range keys {
		lease, held, err := store.LeaseWorker(context.Background(), key)
		if err != nil || !held {
			t.Fatalf("LeaseWorker(%v) = %t, %v; want it held beside every other queue's", key, held, err)
		}
		t.Cleanup(func() { _ = lease.Release() })
		entry, _, err := store.Admit(context.Background(), testAdmission(t, key))
		if err != nil || entry.Order != 1 {
			t.Fatalf("Admit(%v) = %#v, %v; want its own first order", key, entry, err)
		}
	}
	for _, key := range keys {
		if recorded, err := store.Entries(key); err != nil || len(recorded) != 1 || recorded[0].Key() != key {
			t.Fatalf("Entries(%v) = %#v, %v; want its own entry alone", key, recorded, err)
		}
	}
}

func TestOneWorkerOwnsAQueueAndCanHandItOn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const workers = 8
	leases := make([]*Lease, workers)
	errs := make([]error, workers)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			store := newMergeQueueStore(t, root)
			store.leaseWait = 0
			lease, held, err := store.LeaseWorker(context.Background(), mainQueue)
			if held {
				leases[i] = lease
			}
			errs[i] = err
		}()
	}
	close(start)
	wait.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("LeaseWorker() error = %v", err)
	}
	var owner *Lease
	for _, lease := range leases {
		if lease == nil {
			continue
		}
		if owner != nil {
			t.Fatal("two workers hold one queue")
		}
		owner = lease
	}
	if owner == nil {
		t.Fatal("no worker took the queue")
	}

	// The promotion lease is another lease: a worker holding the queue does not
	// hold the target's promotions out.
	promotion, err := newPromotionStore(t, root).LeasePromotion(context.Background(), mainQueue.TargetBranch)
	if err != nil {
		t.Fatalf("LeasePromotion() while a worker holds the queue error = %v", err)
	}
	if err := promotion.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	if err := owner.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := owner.Release(); err != nil {
		t.Fatalf("second Release() error = %v", err)
	}
	next, held, err := newMergeQueueStore(t, root).LeaseWorker(context.Background(), mainQueue)
	if err != nil || !held {
		t.Fatalf("LeaseWorker() after the owner released = %t, %v; want it taken", held, err)
	}
	if err := next.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

func TestAWorkerInAnotherProcessOwnsTheQueueUntilItDies(t *testing.T) {
	t.Parallel()

	// Another harness process holding the worker's lease is the ownership that
	// matters, and the operating system is what ends it, so a worker killed
	// mid-flight leaves the queue to whoever asks next.
	root := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=TestMergeQueueWorkerHolderProcess")
	child.Env = append(os.Environ(), mergeQueueWorkerRootEnv+"="+root)
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	if err := child.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	}()
	if err := awaitLine(output, "merge queue worker held"); err != nil {
		t.Fatalf("the child never took the worker lease: %v", err)
	}

	store := newMergeQueueStore(t, root)
	if lease, held, err := store.LeaseWorker(context.Background(), mainQueue); err != nil || held {
		_ = lease.Release()
		t.Fatalf("LeaseWorker() while another process works the queue = %t, %v; want it refused", held, err)
	}
	// Admitting is not working the queue: a change is admitted while another
	// process owns it.
	if _, _, err := store.Admit(context.Background(), testAdmission(t, mainQueue)); err != nil {
		t.Fatalf("Admit() while another process works the queue error = %v", err)
	}

	if err := child.Process.Kill(); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}
	_ = child.Wait()
	lease, held, err := store.LeaseWorker(context.Background(), mainQueue)
	if err != nil || !held {
		t.Fatalf("LeaseWorker() after the worker died = %t, %v; want it taken", held, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// TestMergeQueueWorkerHolderProcess is the child of the test above rather than
// a test of its own: it takes the worker lease, says so, and waits to be
// killed. It does nothing at all in an ordinary run.
func TestMergeQueueWorkerHolderProcess(t *testing.T) {
	root := os.Getenv(mergeQueueWorkerRootEnv)
	if root == "" {
		t.Skip("not the merge queue worker holder process")
	}
	store, err := NewMergeQueueStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMergeQueueStore() error = %v", err)
	}
	lease, held, err := store.LeaseWorker(context.Background(), mainQueue)
	if err != nil || !held {
		t.Fatalf("LeaseWorker() = %t, %v", held, err)
	}
	runtime.GC()
	os.Stdout.WriteString("merge queue worker held\n")
	// The parent kills this process; the wait is only a bound on a parent that
	// never does, so the child cannot outlive the test run.
	time.Sleep(2 * time.Minute)
	runtime.KeepAlive(lease)
}

func TestQueueingHoldsNoDeveloperSlot(t *testing.T) {
	t.Parallel()

	// A queue with entries waiting and a worker working it leaves the one
	// developer slot free for a run.
	root := t.TempDir()
	queues := newMergeQueueStore(t, root)
	for range 3 {
		if _, _, err := queues.Admit(context.Background(), testAdmission(t, mainQueue)); err != nil {
			t.Fatalf("Admit() error = %v", err)
		}
	}
	worker, held, err := queues.LeaseWorker(context.Background(), mainQueue)
	if err != nil || !held {
		t.Fatalf("LeaseWorker() = %t, %v", held, err)
	}
	defer worker.Release()

	runs, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	lease, err := runs.Reserve(context.Background(), testState(t, StatusPending), 1)
	if err != nil {
		t.Fatalf("Reserve() with one slot while the queue waits and its worker works error = %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

func TestAMergeQueueRecordThatCannotBeReadIsNotAnEmptyQueue(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	if _, _, err := store.Admit(context.Background(), testAdmission(t, mainQueue)); err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	path := filepath.Join(store.Root(), mainQueue.directory(), mergeQueueRecordName)
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.Entries(mainQueue); err == nil {
		t.Fatal("Entries() read a broken record as a queue")
	}
	if _, _, err := store.Admit(context.Background(), testAdmission(t, mainQueue)); err == nil {
		t.Fatal("Admit() wrote over a record it could not read")
	}
}

// awaitLine waits for the child to say a line, so the parent never races what
// the child is doing before it says it.
func awaitLine(output io.Reader, line string) error {
	scanner := bufio.NewScanner(output)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), line) {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("the child exited without saying it")
}

func assertQueue(t *testing.T, store *MergeQueueStore, want ...MergeQueueEntry) {
	t.Helper()
	recorded, err := store.Entries(mainQueue)
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if fmt.Sprint(recorded) != fmt.Sprint(want) {
		t.Fatalf("Entries() = %#v, want %#v", recorded, want)
	}
}

func testAdmission(t *testing.T, key MergeQueueKey) MergeQueueAdmission {
	t.Helper()
	runID, err := NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	return MergeQueueAdmission{
		Key:               key,
		WorkItemID:        "yoyodyne-test",
		WorkItemTitle:     "Persist merge-queue admissions",
		RunID:             runID,
		Publication:       "https://example.test/pull/1",
		ApprovedHead:      strings.Repeat("a", 40),
		IntegrationPolicy: "pull-request",
		Mode:              MergeQueueHarness,
		At:                time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC),
	}
}

func newMergeQueueStore(t *testing.T, root string) *MergeQueueStore {
	t.Helper()
	store, err := NewMergeQueueStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMergeQueueStore() error = %v", err)
	}
	return store
}
