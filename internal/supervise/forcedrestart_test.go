package supervise

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// forcedScheduler is an overdue scheduler that ignores the request to stop, so
// its stop ends only by being forced: it reports the forced stop the way
// StopHolderForcing does, gone and with when and why.
type forcedScheduler struct {
	overdueScheduler
	at time.Time
}

func (f forcedScheduler) Stop(ctx context.Context) (Stopped, error) {
	stopped, err := f.overdueScheduler.Stop(ctx)
	if err != nil || !stopped.WasRunning {
		return stopped, err
	}
	stopped.ForcedAt = f.at
	stopped.Forced = "it was still holding its lease 2m30s after being asked to stop, so the supervisor forced it and everything it had started to stop; the run it was carrying (run-1) keeps what it wrote and is continued by the scheduler started in its place"
	return stopped, nil
}

// A scheduler that would not stop when asked is not left holding the product
// on the old build: its stop is forced, the scheduler is started again from
// the deployed binary at the same look, and the record says it was forced,
// when, and why, on the line status shows for it. It counts as a restart into
// the deployed build and not as a death.
func TestASchedulerThatIgnoresTheStopIsForcedAndReplacedOnTheDeployedBuild(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 11, 22, 15, 30, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	overdue := ""
	scheduler := forcedScheduler{
		overdueScheduler: overdueScheduler{
			selfRestartingChild: selfRestartingChild{newDeployable(config.ServiceScheduler, binary, nil)},
			overdue:             &overdue,
		},
		at: time.Date(2026, 11, 22, 15, 37, 31, 0, time.UTC),
	}
	supervisor := deploySupervisor(t, store, clock, binary, scheduler)
	supervisor.Tick(context.Background())

	binary.deploy(t, "2222222222222222")
	overdue = "has been past its limit for winding down to move onto a new build for 5m7s"
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())

	if scheduler.stopCount() != 1 || scheduler.startCount() != 2 {
		t.Fatalf("stops = %d starts = %d, want the scheduler forced to stop once and started again at the same look", scheduler.stopCount(), scheduler.startCount())
	}
	got := child(t, loaded(t, store), config.ServiceScheduler)
	if got.State != runstate.ChildRunning || got.Build != "2222222222222222" || got.RestartingInto != "" || got.Restarts != 1 || got.Failures != 0 {
		t.Fatalf("scheduler = %+v, want it running on the deployed build after one restart and no death", got)
	}
	if got.ForcedStops != 1 || !got.ForcedStopAt.Equal(scheduler.at) || !strings.Contains(got.ForcedStop, "forced it and everything it had started to stop") {
		t.Fatalf("scheduler = %+v, want the forced stop recorded with when and why", got)
	}
	said := DescribeChild(got)
	if !strings.Contains(said, "forced to stop by the supervisor once, most recently at "+scheduler.at.Local().Format("2006-01-02 15:04 MST")) ||
		!strings.Contains(said, "continued by the scheduler started in its place") {
		t.Fatalf("DescribeChild() = %q, want the forced stop said with when and why", said)
	}
}
