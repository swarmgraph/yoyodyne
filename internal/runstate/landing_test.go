package runstate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Landings on one target branch queue: the second is told it is waiting as the
// wait begins, and is let in only once the first lets go.
func TestLandingsOnOneTargetBranchHappenOneAtATime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first, second := newPromotionStore(t, root), newPromotionStore(t, root)
	ctx := context.Background()

	held, err := first.LeaseLanding(ctx, "main", time.Minute, nil)
	if err != nil {
		t.Fatalf("first LeaseLanding() error = %v", err)
	}
	queued := make(chan struct{})
	released := make(chan struct{})
	admitted := make(chan error, 1)
	go func() {
		lease, err := second.LeaseLanding(ctx, "main", time.Minute, func() { close(queued) })
		select {
		case <-released:
		default:
			err = errors.Join(err, errors.New("a second landing was admitted while the first still held the lease"))
		}
		admitted <- errors.Join(err, lease.Release())
	}()

	select {
	case <-queued:
	case err := <-admitted:
		t.Fatalf("second LeaseLanding() returned %v while the first was held", err)
	}
	close(released)
	if err := held.Release(); err != nil {
		t.Fatalf("first Release() error = %v", err)
	}
	if err := <-admitted; err != nil {
		t.Fatalf("second LeaseLanding() error = %v", err)
	}
}

// A landing holds its own lease and not the promotion lease, so a suite running
// for hours holds nobody out of integration, and landings on different branches
// do not queue on each other.
func TestALandingHoldsNeitherThePromotionLeaseNorAnotherBranchsLanding(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx := context.Background()
	landing, err := newPromotionStore(t, root).LeaseLanding(ctx, "main", time.Minute, nil)
	if err != nil {
		t.Fatalf("LeaseLanding() error = %v", err)
	}
	defer landing.Release()

	promoter := newPromotionStore(t, root)
	promoter.promotionWait = 50 * time.Millisecond
	promotion, err := promoter.LeasePromotion(ctx, "main")
	if err != nil {
		t.Fatalf("LeasePromotion() error = %v while a landing held main", err)
	}
	defer promotion.Release()

	other, err := newPromotionStore(t, root).LeaseLanding(ctx, "release/1.2", 50*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("LeaseLanding(release/1.2) error = %v while a landing held main", err)
	}
	defer other.Release()
}

// A landing that waits out its bound is told the queue did not drain, in words
// that are not its own context being cancelled; a cancelled one is told that.
func TestALandingsWaitIsBoundedAndTellsTheBoundFromACancellation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx := context.Background()
	lease, err := newPromotionStore(t, root).LeaseLanding(ctx, "main", time.Minute, nil)
	if err != nil {
		t.Fatalf("LeaseLanding() error = %v", err)
	}
	defer lease.Release()

	waiter := newPromotionStore(t, root)
	_, err = waiter.LeaseLanding(ctx, "main", 50*time.Millisecond, nil)
	if err == nil || !strings.Contains(err.Error(), "another landing held the lease") || !strings.Contains(err.Error(), "main") {
		t.Fatalf("LeaseLanding() error = %v, want the branch and the queue named", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := waiter.LeaseLanding(cancelled, "main", time.Minute, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("LeaseLanding() error = %v, want the cancellation", err)
	}
	for _, branch := range []string{"", "HEAD", "../escape"} {
		if _, err := waiter.LeaseLanding(ctx, branch, time.Minute, nil); err == nil {
			t.Fatalf("LeaseLanding(%q) was accepted", branch)
		}
	}
}

// A landing waiting its turn reads as waiting, behind what, and since when —
// which is the line `yoyo status` prints — and one whose process died waiting is
// settled saying it died waiting rather than running.
func TestAWaitingLandingSaysSoAndDyingWhileWaitingIsSaidAsSuch(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	waiting := started.Add(time.Second)
	landed := LandingChecks{Commit: strings.Repeat("a", 40), StartedAt: started, BoundSeconds: 7200, TargetBranch: "main", WaitingSince: &waiting}
	if got, want := landed.Describe(time.Now()), "landing checks waiting over aaaaaaaaaaaa behind another landing on main, since 2026-09-25T10:00:01Z"; got != want {
		t.Fatalf("Describe() = %q, want %q", got, want)
	}

	died := landed
	died.CloseInterrupted(started.Add(time.Hour))
	if !died.Unverified() || !strings.Contains(died.Problem, "waiting its turn behind another landing died before the landing checks started") {
		t.Fatalf("settled = %#v, want it unverified and said as a death while waiting", died)
	}

	admitted := waiting.Add(4 * time.Minute)
	finished := admitted.Add(18 * time.Minute)
	ran := landed
	ran.AdmittedAt, ran.FinishedAt, ran.Ran, ran.Green = &admitted, &finished, true, true
	ran.Checks = []LandingCheckResult{{Command: "make race", Passed: true}}
	if got, want := ran.Describe(time.Now()), "green landing: 1 landing check passed over aaaaaaaaaaaa in 18m, after waiting 4m behind another landing on main"; got != want {
		t.Fatalf("Describe() = %q, want %q", got, want)
	}
}
