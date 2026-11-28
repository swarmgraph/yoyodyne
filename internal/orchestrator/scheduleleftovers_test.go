package orchestrator

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// countedLeftovers is a stop of leftover processes that counts how often it is
// taken and what had been listed of the runs in flight when it first was.
type countedLeftovers struct {
	mu      sync.Mutex
	stops   int
	listing *askedOf
	before  []string
}

func (c *countedLeftovers) Stop(context.Context) runstate.SweepStep {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stops++
	if c.stops == 1 {
		c.before = c.listing.said()
	}
	return runstate.SweepStep{Name: "leftovers", Outcome: runstate.StepRan, Detail: "nothing is left running for run-1"}
}

// A session stops what runs with nothing going left running once, as it
// starts and before it lists the runs in flight to start anything, and says
// what it did in its log. Later pulls leave it to the maintenance pass.
func TestASessionStopsLeftoverProcessesOnceAsItStarts(t *testing.T) {
	t.Parallel()
	harness := newScheduleHarness(readyItems("yoyodyne-ifd.501")...)
	listing := &askedOf{}
	runs := rebuildingRuns{scheduleHarness: harness, log: listing}
	leftovers := &countedLeftovers{listing: listing}
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Runs = runs
		pull.Leftovers = leftovers
		return pull, err
	}
	var log bytes.Buffer
	if _, err := (Scheduler{Open: open, Log: &log}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if leftovers.stops != 1 {
		t.Fatalf("the leftovers were stopped %d times, want once", leftovers.stops)
	}
	for _, asked := range leftovers.before {
		if asked == "incomplete" {
			t.Fatalf("the runs in flight were listed before the leftovers were stopped: %v", leftovers.before)
		}
	}
	if !strings.Contains(log.String(), "as the session started, the harness looked for processes runs left running: ran, nothing is left running for run-1") {
		t.Fatalf("the session's log does not say what the stop did:\n%s", log.String())
	}
}
