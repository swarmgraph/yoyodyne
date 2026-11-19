package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// installedBuilds stands in for the binary a watch session was started from:
// builds are installed over it one after another, and the one a restart would
// re-execute is whichever is installed at the moment it does.
type installedBuilds struct {
	mu        sync.Mutex
	installed []string
}

func (b *installedBuilds) install(build string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.installed = append(b.installed, build)
}

func (b *installedBuilds) Replaced() (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.installed) > 0, nil
}

func (b *installedBuilds) newest() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.installed) == 0 {
		return ""
	}
	return b.installed[len(b.installed)-1]
}

// Two builds landing an hour apart give one move, onto the later build, and
// no sooner than three hours after the previous move: the least time between
// moves when the project sets nothing (yoyodyne-ifd.428.161).
func TestTwoBuildsAnHourApartGiveOneMoveOntoTheLaterBuildThreeHoursAfterTheLast(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.poll = 10 * time.Minute
	lastMove := harness.clock()
	builds := &installedBuilds{}
	sessions := &recordedSessions{}
	var installedWhenFound []time.Time
	harness.onSleep = func(h *scheduleHarness, _ int) bool {
		now := h.clock()
		switch {
		case len(installedWhenFound) == 0 && !now.Before(lastMove.Add(time.Hour)):
			builds.install("build-one")
			installedWhenFound = append(installedWhenFound, now)
		case len(installedWhenFound) == 1 && !now.Before(lastMove.Add(2*time.Hour)):
			builds.install("build-two")
			installedWhenFound = append(installedWhenFound, now)
		}
		// A session that never moves is stopped well past when it should have.
		return now.Before(lastMove.Add(12 * time.Hour))
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, SessionID: "watch-interval", Deployment: builds, LastBuildMove: lastMove}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	movedAt := harness.clock()
	if schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q at %s, want the session stopped once to move onto a new build", schedule.Stopped, movedAt)
	}
	if len(installedWhenFound) != 2 {
		t.Fatalf("builds installed before the move = %d, want both builds installed before the session moved", len(installedWhenFound))
	}
	if movedAt.Before(lastMove.Add(3 * time.Hour)) {
		t.Fatalf("the session moved at %s, %s after the last move, want no sooner than three hours", movedAt, movedAt.Sub(lastMove))
	}
	if movedAt.After(lastMove.Add(3*time.Hour + harness.poll)) {
		t.Fatalf("the session moved at %s, %s after the last move, want it within one poll of the three hours being up", movedAt, movedAt.Sub(lastMove))
	}
	// The restart re-executes whatever is installed when it is made, so the one
	// move is onto the later build and the first is never moved onto.
	if got := builds.newest(); got != "build-two" {
		t.Fatalf("the build the move takes up = %q, want build-two", got)
	}
	stops := 0
	for _, transition := range sessions.recorded() {
		if transition.state == runstate.WatchStopped {
			stops++
		}
	}
	if stops != 1 {
		t.Fatalf("the session stopped %d time(s), want one move", stops)
	}
	// While it waited it said once, in plain words, when it would move.
	waits := 0
	for _, transition := range sessions.recorded() {
		if strings.Contains(transition.reason, "a newer build is installed") {
			waits++
			if !strings.Contains(transition.reason, runstate.LocalStamp(lastMove.Add(3*time.Hour))) {
				t.Fatalf("wait said %q, want it to name when the move is due", transition.reason)
			}
		}
	}
	if waits != 1 {
		t.Fatalf("the wait was said %d time(s), want once", waits)
	}
}

// A session with no recorded move before it, and one whose least time between
// moves is already up, moves at once; a set interval is honoured in place of
// the default.
func TestABuildMoveIsDueOnceTheLeastTimeBetweenMovesIsUp(t *testing.T) {
	t.Parallel()

	last := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name  string
		last  time.Time
		every time.Duration
		now   time.Time
		due   bool
	}{
		{"no move recorded", time.Time{}, 0, last, true},
		{"an hour after, at the default", last, 0, last.Add(time.Hour), false},
		{"just short of the default", last, 0, last.Add(3*time.Hour - time.Second), false},
		{"exactly the default", last, 0, last.Add(3 * time.Hour), true},
		{"ninety minutes after, set to one hour", last, time.Hour, last.Add(90 * time.Minute), true},
		{"five hours after, set to six", last, 6 * time.Hour, last.Add(5 * time.Hour), false},
	} {
		if got := buildMoveDue(test.last, test.every, test.now); got != test.due {
			t.Errorf("%s: buildMoveDue = %v, want %v", test.name, got, test.due)
		}
	}
}
