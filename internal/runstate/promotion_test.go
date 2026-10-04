package runstate

// The promotion lease is what makes integration serial while development stays
// parallel, so these tests are about the queue rather than about the file: two
// promotions into one branch happen one at a time, two into different branches
// do not wait for each other, and a holder that dies leaves nothing behind.

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// leaseHolderRootEnv hands the state root to the child process that stands in
// for a promotion whose holder dies mid-flight.
const leaseHolderRootEnv = "YOYODYNE_TEST_PROMOTION_LEASE_ROOT"

func TestPromotionsIntoOneTargetBranchHappenOneAtATime(t *testing.T) {
	t.Parallel()

	// Two stores over one root are two processes as far as the lock is
	// concerned, which is the contention the lease exists for.
	root := t.TempDir()
	first, second := newPromotionStore(t, root), newPromotionStore(t, root)
	ctx := context.Background()

	held, err := first.LeasePromotion(ctx, "main")
	if err != nil {
		t.Fatalf("first LeasePromotion() error = %v", err)
	}

	// The second promotion is admitted only once the first has settled, so the
	// release has to be what unblocks it rather than merely precede it. The
	// store says when the second has found the lease held and queued, and the
	// second says when it got through; the test waits on those and on no clock.
	queued := make(chan struct{})
	second.promotionQueued = func() { close(queued) }
	admitted := make(chan error, 1)
	released := make(chan struct{})
	go func() {
		lease, err := second.LeasePromotion(ctx, "main")
		select {
		case <-released:
		default:
			// Reaching here means the second promotion started while the first was
			// still holding the branch, which is the race the lease removes.
			err = errors.Join(err, errors.New("a second promotion was admitted while the first still held the lease"))
		}
		admitted <- errors.Join(err, lease.Release())
	}()

	select {
	case <-queued:
	case err := <-admitted:
		t.Fatalf("second LeasePromotion() returned %v while the first was held", err)
	}

	close(released)
	if err := held.Release(); err != nil {
		t.Fatalf("first Release() error = %v", err)
	}
	if err := <-admitted; err != nil {
		t.Fatalf("second LeasePromotion() error = %v", err)
	}
}

func TestPromotionsIntoDifferentTargetBranchesDoNotQueue(t *testing.T) {
	t.Parallel()

	// Serializing every promotion in the product would make one branch's queue
	// everybody's, so the lease is per target branch. `release/1.2` and
	// `release-1.2` are here because they are different branches that a lease
	// file named by flattening the slash would collapse into one.
	root := t.TempDir()
	ctx := context.Background()
	for _, branch := range []string{"main", "release/1.2", "release-1.2"} {
		lease, err := newPromotionStore(t, root).LeasePromotion(ctx, branch)
		if err != nil {
			t.Fatalf("LeasePromotion(%q) error = %v", branch, err)
		}
		// Held for the rest of the test: every branch's lease is open at once, so
		// nothing here could have queued behind anything else.
		t.Cleanup(func() {
			if err := lease.Release(); err != nil {
				t.Errorf("Release(%q) error = %v", branch, err)
			}
		})
	}
}

func TestPromotionLeaseWaitIsBounded(t *testing.T) {
	t.Parallel()

	// A holder that never finishes must not hold every other run on the branch
	// until its process dies, and what the waiting run is told has to name the
	// queue rather than read like its own run being stopped.
	root := t.TempDir()
	holder := newPromotionStore(t, root)
	waiter := newPromotionStore(t, root)
	waiter.promotionWait = 50 * time.Millisecond
	ctx := context.Background()

	lease, err := holder.LeasePromotion(ctx, "main")
	if err != nil {
		t.Fatalf("LeasePromotion() error = %v", err)
	}
	defer lease.Release()

	_, err = waiter.LeasePromotion(ctx, "main")
	if err == nil {
		t.Fatal("LeasePromotion() was admitted while another promotion held the branch")
	}
	if !strings.Contains(err.Error(), "another promotion held the lease") || !strings.Contains(err.Error(), "main") {
		t.Fatalf("LeasePromotion() error = %v, want the branch and the queue named", err)
	}
	var bound StopError
	if !errors.As(err, &bound) || bound.Class != StopPromotionWait {
		t.Fatalf("LeasePromotion() error = %v, want the promotion-wait cause", err)
	}
	if ctx.Err() != nil {
		t.Fatal("the bounded wait spent the caller's own context")
	}
}

func TestPromotionLeaseStopsWaitingWithTheRun(t *testing.T) {
	t.Parallel()

	// A cancelled run stops waiting for its turn immediately, and says it was
	// cancelled rather than reporting a queue that never drained.
	root := t.TempDir()
	holder, waiter := newPromotionStore(t, root), newPromotionStore(t, root)
	background := context.Background()
	lease, err := holder.LeasePromotion(background, "main")
	if err != nil {
		t.Fatalf("LeasePromotion() error = %v", err)
	}
	defer lease.Release()

	ctx, cancel := context.WithCancel(background)
	cancel()
	if _, err := waiter.LeasePromotion(ctx, "main"); !errors.Is(err, context.Canceled) {
		t.Fatalf("LeasePromotion() error = %v, want the cancellation", err)
	}
}

func TestPromotionLeaseDiesWithItsHolder(t *testing.T) {
	t.Parallel()

	// The operating system owns the release, so a promotion whose process was
	// killed mid-flight leaves no lock for anybody to clear. What it does leave —
	// a half-finished promotion in durable run state — is reconciliation's, and
	// this is what lets the next run reach the branch at all.
	root := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=TestPromotionLeaseHolderProcess")
	child.Env = append(os.Environ(), leaseHolderRootEnv+"="+root)
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
	if err := awaitHeldLease(output); err != nil {
		t.Fatalf("the holder never took the promotion lease: %v", err)
	}

	ctx := context.Background()
	store := newPromotionStore(t, root)
	store.promotionWait = 100 * time.Millisecond
	if _, err := store.LeasePromotion(ctx, "main"); err == nil {
		t.Fatal("LeasePromotion() was admitted while the holder was alive")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}
	_ = child.Wait()

	store.promotionWait = 30 * time.Second
	lease, err := store.LeasePromotion(ctx, "main")
	if err != nil {
		t.Fatalf("LeasePromotion() error = %v after the holder died", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// TestPromotionLeaseHolderProcess is the child of the test above rather than a
// test of its own: it takes the promotion lease, says so, and then waits to be
// killed. It does nothing at all in an ordinary run.
func TestPromotionLeaseHolderProcess(t *testing.T) {
	root := os.Getenv(leaseHolderRootEnv)
	if root == "" {
		t.Skip("not the promotion lease holder process")
	}
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	lease, err := store.LeasePromotion(context.Background(), "main")
	if err != nil {
		t.Fatalf("LeasePromotion() error = %v", err)
	}
	// Collection must not release the lease while this process is still alive.
	runtime.GC()
	os.Stdout.WriteString("promotion lease held\n")
	// The parent kills this process; the wait is only a bound on a parent that
	// never does, so the child cannot outlive the test run.
	time.Sleep(2 * time.Minute)
	runtime.KeepAlive(lease)
}

func TestPromotionLeaseRefusesWhatIsNotALocalBranch(t *testing.T) {
	t.Parallel()

	// The branch names the lease file, so anything that is not a local branch is
	// refused before it reaches the filesystem.
	store := newPromotionStore(t, t.TempDir())
	for _, branch := range []string{"", "  ", "HEAD", "refs/heads/main", "../escape", "main/"} {
		if _, err := store.LeasePromotion(context.Background(), branch); err == nil {
			t.Fatalf("LeasePromotion(%q) was accepted", branch)
		}
	}
	// `+` is what the lock name encodes a slash as, so the encoding is only
	// reversible while a branch cannot contain one itself. Git accepts `+` in a
	// branch name and validLocalBranch does not, which is the whole reason the
	// encoding is safe — and it is an assumption about code elsewhere, so it is
	// pinned here rather than left to be discovered when `release/1.2` and
	// `release+1.2` quietly start sharing a lease.
	for _, branch := range []string{"+x", "release+1.2"} {
		if _, err := store.LeasePromotion(context.Background(), branch); err == nil {
			t.Fatalf("LeasePromotion(%q) was accepted; the lock name encoding is no longer reversible", branch)
		}
	}
}

func TestPromotionLockNameKeepsBranchesApart(t *testing.T) {
	t.Parallel()

	// A branch may contain a slash and a file name may not, so the encoding has
	// to separate names that flattening would collapse. A name too long to be a
	// file falls back to a digest, which is still exactly one lease per branch.
	if promotionLockName("release/1.2") == promotionLockName("release-1.2") {
		t.Fatal("branches that differ only by their separator share a promotion lease")
	}
	long := strings.Repeat("a", maxPromotionLockNameBytes+1)
	name := promotionLockName(long)
	if len(name) > maxPromotionLockNameBytes || name == promotionLockName(long+"a") {
		t.Fatalf("promotionLockName(long) = %q", name)
	}
}

// awaitHeldLease waits for the child to report that it holds the lease, so the
// parent never races the child's own acquisition.
func awaitHeldLease(output io.Reader) error {
	scanner := bufio.NewScanner(output)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "promotion lease held") {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("the holder exited without taking the lease")
}

func newPromotionStore(t *testing.T, root string) *Store {
	t.Helper()
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}
