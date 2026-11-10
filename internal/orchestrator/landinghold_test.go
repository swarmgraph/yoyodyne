package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// holdClock is a clock the stubbed sleep moves, so a test waits out a hold
// without waiting.
type holdClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *holdClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *holdClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// heldRun is an automatic run whose target has a landing hold placed on it,
// with its checks counted and the target's movement watched while it waits.
type heldRun struct {
	pipeline   Pipeline
	holds      *runstate.LandingHoldStore
	clock      *holdClock
	repository string
	base       string
	checkLog   string
	sleeps     []time.Duration
	movedEarly bool
	onSleep    func(sleeps int)
}

func newHeldRun(t *testing.T, hold time.Duration) (*heldRun, func() (Outcome, error)) {
	t.Helper()
	repository, tracker, provider, pipeline, _ := automaticFixture(t)
	h := &heldRun{pipeline: pipeline, repository: repository, clock: &holdClock{now: time.Now()}}
	h.checkLog = filepath.Join(t.TempDir(), "checks.log")
	h.pipeline.Config.Checks = []string{"echo ran >> " + h.checkLog}
	var err error
	if h.holds, err = runstate.NewLandingHoldStore(t.TempDir(), "yoyodyne"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.holds.Place("main", "cutting a release", hold, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	h.base = gitLine(t, repository, "rev-parse", "refs/heads/main")
	h.pipeline.LandingHolds = h.holds
	h.pipeline.Clock = h.clock
	h.pipeline.Sleep = func(_ context.Context, d time.Duration) error {
		h.sleeps = append(h.sleeps, d)
		if gitLine(t, repository, "rev-parse", "refs/heads/main") != h.base {
			h.movedEarly = true
		}
		if h.onSleep != nil {
			h.onSleep(len(h.sleeps))
		}
		h.clock.advance(d)
		return nil
	}
	reviews := func() int { return len(provider.RequestsForRole(domain.RoleReviewer)) }
	return h, func() (Outcome, error) {
		outcome, err := h.pipeline.Run(context.Background(), tracker.Item.ID)
		if reviews() == 0 {
			t.Error("the review did not run while landing was held")
		}
		return outcome, err
	}
}

func (h *heldRun) checksRan(t *testing.T) bool {
	t.Helper()
	logged, err := os.ReadFile(h.checkLog)
	return err == nil && strings.Contains(string(logged), "ran")
}

func TestARunWaitsOutAHoldOnItsTargetWithItsChecksAndReviewDone(t *testing.T) {
	t.Parallel()

	h, run := newHeldRun(t, 5*time.Minute)
	outcome, err := run()
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !h.checksRan(t) {
		t.Fatal("the configured checks did not run while landing was held")
	}
	if h.movedEarly {
		t.Fatal("the target moved while the hold stood")
	}
	if len(h.sleeps) != 10 {
		t.Fatalf("slept %v, want ten probes of %s across the five minute hold", h.sleeps, landingHoldProbe)
	}
	if outcome.Status != runstate.StatusSucceeded || gitLine(t, h.repository, "rev-parse", "refs/heads/main") == h.base {
		t.Fatalf("outcome = %#v; want the run landed once the hold ended on its own", outcome)
	}
}

func TestAHoldReleasedWhileARunWaitsLetsItLandAtTheNextProbe(t *testing.T) {
	t.Parallel()

	h, run := newHeldRun(t, 4*time.Hour)
	h.onSleep = func(sleeps int) {
		if sleeps == 2 {
			if _, found, err := h.holds.Release("main"); err != nil || !found {
				t.Errorf("Release() = %v, %v", found, err)
			}
		}
	}
	outcome, err := run()
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(h.sleeps) != 2 || h.sleeps[0] != landingHoldProbe || h.movedEarly {
		t.Fatalf("slept %v (moved early %v); want two probes, then the landing", h.sleeps, h.movedEarly)
	}
	if outcome.Status != runstate.StatusSucceeded || gitLine(t, h.repository, "rev-parse", "refs/heads/main") == h.base {
		t.Fatalf("outcome = %#v; want the run landed", outcome)
	}
}

type unreadableLandingHolds struct{}

func (unreadableLandingHolds) Standing(string, time.Time) (runstate.LandingHold, bool, error) {
	return runstate.LandingHold{}, false, errors.New("the hold file is garbled")
}

func TestAHoldThatCannotBeReadStopsTheIntegrationWithoutMovingTheTarget(t *testing.T) {
	t.Parallel()

	repository, tracker, _, pipeline, _ := automaticFixture(t)
	pipeline.LandingHolds = unreadableLandingHolds{}
	base := gitLine(t, repository, "rev-parse", "refs/heads/main")
	_, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "read whether landing on main is held") || !strings.Contains(err.Error(), "garbled") {
		t.Fatalf("Run() error = %v; want the stop to say the hold could not be read", err)
	}
	if got := gitLine(t, repository, "rev-parse", "refs/heads/main"); got != base {
		t.Fatalf("main moved to %s through a hold nobody could read", got)
	}
}
