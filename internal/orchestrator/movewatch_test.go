package orchestrator

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// seenDeployment is a build installed over the session that says when the
// session first found it installed, which only the session's own timer can
// do while the pull is held inside a slow tracker write.
type seenDeployment struct {
	deployedOver
	once sync.Once
	seen chan struct{}
}

func (d *seenDeployment) Replaced() (bool, error) {
	replaced, err := d.deployedOver.Replaced()
	if replaced {
		d.once.Do(func() { close(d.seen) })
	}
	return replaced, err
}

// A move onto a newly installed build that falls due while a pull is held up
// by a slow tracker begins at the moment the session's own timer finds it due,
// not when the pull ends, and the pull starts nothing more once it is due.
// Before this the session looked for a new build only between pulls, so a move
// due at 21:02 PST on November 22 began at 21:23, when a fourteen-minute pull
// ended, and the pull went on starting work found after the move fell due.
//
// Here the pull that first finds work for the free slot is held up by one
// write, during which the build is installed and the clock passes the whole
// wind-down limit: the drain is dated from the timer's finding, so it has
// already run out when the pull goes on, and the session restarts with only
// the run it started before the build was installed.
func TestAMoveThatFallsDueDuringALongPullBeginsWhenTheTimerFindsIt(t *testing.T) {
	t.Parallel()

	const checking, late, waiting = "yoyodyne-checking", "yoyodyne-late", "yoyodyne-waiting"
	items := readyItems(checking, late)
	items = append(items, beads.WorkItem{ID: waiting, Title: waiting, Status: "open", Priority: 2,
		Dependencies: []beads.Dependency{{ID: checking, Type: beads.BlocksDependency}}})
	harness := newScheduleHarness(items...)
	harness.capacity = 2
	harness.poll = 5 * time.Millisecond
	harness.ReadyItems[late] = false
	harness.ReadyItems[waiting] = false
	deployment := &seenDeployment{seen: make(chan struct{})}
	sessions := &recordedSessions{}
	stageStarted := harness.clock()

	var mu sync.Mutex
	slow := false
	var installedAt time.Time
	checkingStarted := make(chan struct{}, 1)
	harness.onPull = func(h *scheduleHarness, pulls int) {
		switch pulls {
		case 1:
			<-checkingStarted
		case 2:
			// Work for the free slot is ready, and this pull is slow.
			h.mu.Lock()
			h.ReadyItems[late] = true
			h.mu.Unlock()
			mu.Lock()
			slow = true
			mu.Unlock()
		}
	}
	harness.onSchedulingWait = func(h *scheduleHarness) {
		mu.Lock()
		defer mu.Unlock()
		if !slow {
			return
		}
		slow = false
		// The build is installed while the write is held up, and the timer
		// finds it before the write returns.
		installedAt = h.clock()
		deployment.deploy()
		select {
		case <-deployment.seen:
		case <-time.After(10 * time.Second):
			t.Error("the session's timer never looked at the deployment while the pull was held up")
		}
		h.mu.Lock()
		h.now = h.now.Add(h.drainLimit + time.Minute)
		h.mu.Unlock()
	}
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		state := h.inFlight[id]
		state.Phase = runstate.PhaseChecking
		state.CheckStage = &runstate.CheckStage{StartedAt: stageStarted, BoundSeconds: int64((5 * time.Hour) / time.Second), Command: "make race"}
		h.inFlight[id] = state
		h.mu.Unlock()
		if id == checking {
			checkingStarted <- struct{}{}
		}
		<-ctx.Done()
		var drained RedeployDrain
		if !errors.As(context.Cause(ctx), &drained) {
			return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
		}
		return Outcome{WorkItemID: id, Status: runstate.StatusRunning, Paused: true, RedeployStop: &runstate.RedeployStop{At: drained.At, Phase: runstate.PhaseChecking, BoundSeconds: int64(drained.Bound / time.Second), SessionID: drained.SessionID}}, nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, SessionID: "watch-move", Deployment: deployment, moveCheckEvery: time.Millisecond}
	schedule, err := scheduleWithin(scheduler, time.Minute)
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	mu.Lock()
	installed := installedAt
	mu.Unlock()
	if installed.IsZero() {
		t.Fatal("no pull kept a reason while work for the free slot was ready, so no pull was held up")
	}
	if schedule.Stopped != ScheduleRedeployed || !sessions.restarted() {
		t.Fatalf("stopped = %q, want the session restarted on its own: %s", schedule.Stopped, schedule.Render())
	}
	// The move began when the timer found it, inside the pull, rather than when
	// the pull ended the wind-down limit and a minute later.
	if schedule.Drain == nil || !schedule.Drain.Since.Equal(installed) {
		t.Fatalf("drain = %#v, want it begun at %s, when the build was installed during the pull", schedule.Drain, installed)
	}
	// Nothing found after the move fell due was started on the old build.
	if order := harness.pullOrder(); !slices.Equal(order, []string{checking}) {
		t.Fatalf("started = %v, want only the run started before the build was installed", order)
	}
	if !schedule.Drain.BoundReached || !slices.Contains(schedule.Drain.Stopped, checking) {
		t.Fatalf("drain = %#v, want the bound reached and the running run stopped for the session that comes back", schedule.Drain)
	}
}

// A drain the session's timer began inside the pull now under way declines
// every start that pull would still make, however far off the wind-down limit
// is, and the pulls after it are made under the drain as before.
func TestADrainBegunInsideAPullDeclinesThatPullsStarts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 22, 21, 2, 0, 0, time.Local)
	drain := redeployDrain{limit: 15 * time.Minute}
	drain.begin(now)
	defer drain.stop()
	if _, declined := drain.declinesDeveloperStarts(time.Minute, now); declined {
		t.Fatal("a drain fifteen minutes from its limit declined a start, want it to fill free slots as before")
	}
	drain.foundInPull = true
	if remaining, declined := drain.declinesDeveloperStarts(time.Minute, now); !declined || remaining != 15*time.Minute {
		t.Fatalf("declinesDeveloperStarts = %s, %t; want the start declined by the pull that found the move due", remaining, declined)
	}
	if _, declined := drain.declinesStarts(time.Minute, now); !declined {
		t.Fatal("declinesStarts did not decline a document landing in the pull that found the move due")
	}
	if said := drain.stepDownSays(); said != movedInPullSays {
		t.Fatalf("stepDownSays() = %q, want what a pull that found the move due says", said)
	}
}

// The timer finds nothing before a pull has said how long moves are apart,
// nothing while no newer build is installed or the move is not yet due, and
// keeps the first moment it found the move due.
func TestTheMoveTimerKeepsTheFirstMomentTheMoveWasDue(t *testing.T) {
	t.Parallel()

	deployment := &deployedOver{}
	watch := &moveWatch{}
	last := time.Date(2026, 11, 22, 18, 0, 0, 0, time.Local)
	at := last.Add(time.Hour)
	deployment.deploy()
	if watch.look(deployment, last, at) {
		t.Fatal("the timer found a move due before any pull read the least time between moves")
	}
	watch.interval(3 * time.Hour)
	if watch.look(deployment, last, at) {
		t.Fatal("the timer found a move due an hour after the last one, want it due three hours after")
	}
	due := last.Add(3 * time.Hour)
	if !watch.look(deployment, last, due) {
		t.Fatal("the timer did not find the move due at its due moment")
	}
	watch.look(deployment, last, due.Add(time.Minute))
	if found, ok := watch.due(); !ok || !found.Equal(due) {
		t.Fatalf("due() = %s, %t; want the first moment it was found due, %s", found, ok, due)
	}
}
