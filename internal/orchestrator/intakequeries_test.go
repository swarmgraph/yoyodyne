package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeIntake is a query run that waits until it is released.
type fakeIntake struct {
	mu      sync.Mutex
	due     []string
	started map[string]int
	release chan struct{}
}

func (f *fakeIntake) Due(time.Time) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.due...), nil
}

func (f *fakeIntake) Run(ctx context.Context, query string) error {
	f.mu.Lock()
	f.started[query]++
	f.mu.Unlock()
	select {
	case <-f.release:
	case <-ctx.Done():
	}
	return nil
}

func TestAnIntakeQueryWithARunInFlightIsNotStartedAgain(t *testing.T) {
	intake := &fakeIntake{due: []string{"bugs", "ideas"}, started: map[string]int{}, release: make(chan struct{})}
	runs := newIntakeRuns(context.Background())
	defer runs.cancel()
	scheduler := Scheduler{}
	var schedule Schedule
	scheduler.startIntakeRuns(&schedule, intake, runs)
	// Pulls go on while the runs work: each finds both queries still due and
	// running, and starts neither again.
	for pull := 0; pull < 3; pull++ {
		collectIntakeRuns(&schedule, runs, false)
		scheduler.startIntakeRuns(&schedule, intake, runs)
	}
	if schedule.IntakeRuns != 2 || len(runs.inFlight) != 2 {
		t.Fatalf("started %d runs with %d in flight, want one of each query", schedule.IntakeRuns, len(runs.inFlight))
	}
	close(intake.release)
	collectIntakeRuns(&schedule, runs, true)
	if len(runs.inFlight) != 0 {
		t.Fatalf("%d runs still in flight after the session waited them out", len(runs.inFlight))
	}
	intake.mu.Lock()
	defer intake.mu.Unlock()
	if intake.started["bugs"] != 1 || intake.started["ideas"] != 1 {
		t.Fatalf("runs started: %v", intake.started)
	}
	// A query run holds no developer slot: nothing in the schedule was chosen.
	if schedule.Chosen() != 0 {
		t.Fatalf("an intake run was counted as chosen work: %d", schedule.Chosen())
	}
}
