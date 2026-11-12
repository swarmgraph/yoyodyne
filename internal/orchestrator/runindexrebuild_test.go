package orchestrator

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// rebuildingRuns is a run store that records, in order, each rebuild of its
// index and each listing of the runs in flight, and fails the first rebuilds
// it is told to.
type rebuildingRuns struct {
	*scheduleHarness
	log *askedOf
}

type askedOf struct {
	sync.Mutex
	asked []string
	fail  int
}

func (r rebuildingRuns) RebuildRunIndex() error {
	r.log.Lock()
	defer r.log.Unlock()
	r.log.asked = append(r.log.asked, "rebuild")
	if r.log.fail > 0 {
		r.log.fail--
		return errors.New("the index lock could not be taken")
	}
	return nil
}

func (r rebuildingRuns) Incomplete() ([]runstate.State, error) {
	r.log.Lock()
	r.log.asked = append(r.log.asked, "incomplete")
	r.log.Unlock()
	return r.scheduleHarness.Incomplete()
}

func (a *askedOf) said() []string {
	a.Lock()
	defer a.Unlock()
	return append([]string(nil), a.asked...)
}

// A session rebuilds the index of run records as it starts, once, and before
// anything counts the runs in flight, so a record an older build changed in
// place during a redeploy is counted from the first pull. A rebuild that fails
// is said on the schedule and tried again at the next pull.
func TestASessionRebuildsTheRunIndexBeforeCountingTheRunsInFlight(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		fail int
	}{
		{name: "the rebuild works", fail: 0},
		{name: "the first rebuild fails", fail: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			harness := newScheduleHarness(readyItems("yoyodyne-ifd.500")...)
			log := &askedOf{fail: test.fail}
			runs := rebuildingRuns{scheduleHarness: harness, log: log}
			open := func(ctx context.Context) (Pull, error) {
				pull, err := harness.open(ctx)
				pull.Runs = runs
				return pull, err
			}
			schedule, err := (Scheduler{Open: open}).Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			asked := log.said()
			if len(asked) == 0 || asked[0] != "rebuild" {
				t.Fatalf("asked = %v, want the index rebuilt before the runs in flight were first listed", asked)
			}
			rebuilds := 0
			for _, call := range asked {
				if call == "rebuild" {
					rebuilds++
				}
			}
			if test.fail == 0 && (rebuilds != 1 || schedule.RunIndexProblem != "") {
				t.Fatalf("rebuilds = %d, problem = %q; want one rebuild for the session and no problem", rebuilds, schedule.RunIndexProblem)
			}
			// A session that pulled once has only the failure to say; one that
			// pulled again has tried the rebuild again.
			if test.fail > 0 && rebuilds == 1 && !strings.Contains(schedule.RunIndexProblem, "could not be rebuilt") {
				t.Fatalf("problem = %q; want the failed rebuild said on the schedule", schedule.RunIndexProblem)
			}
		})
	}
}
