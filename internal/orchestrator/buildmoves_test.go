package orchestrator

import (
	"context"
	"fmt"
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

// Builds landing every few minutes give one move, begun as soon as the first
// is installed and onto the newest installed when the session restarts: there
// is no least time between moves, and a newer landing never puts the move off
// (yoyodyne-ifd.428.225).
func TestBuildsLandingEveryFewMinutesGiveOneMoveOntoTheNewestBegunAtTheFirst(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.poll = time.Minute
	start := harness.clock()
	builds := &installedBuilds{}
	sessions := &recordedSessions{}
	var found time.Time
	harness.onSleep = func(h *scheduleHarness, _ int) bool {
		now := h.clock()
		if found.IsZero() {
			builds.install("build-one")
			found = now
		} else if len(builds.installed) < 4 && !now.Before(found.Add(time.Duration(len(builds.installed))*3*time.Minute)) {
			builds.install(fmt.Sprintf("build-%d", len(builds.installed)+1))
		}
		// A session that never moves is stopped well past when it should have.
		return now.Before(start.Add(6 * time.Hour))
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, SessionID: "watch-landings", Deployment: builds}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	movedAt := harness.clock()
	if schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q at %s, want the session stopped once to move onto a new build", schedule.Stopped, movedAt)
	}
	if limit := found.Add(15 * time.Minute); movedAt.After(limit) {
		t.Fatalf("the session moved at %s, %s after the first build was installed, want it within 15 minutes of it however many builds landed since", movedAt, movedAt.Sub(found))
	}
	stops := 0
	for _, transition := range sessions.recorded() {
		if transition.state == runstate.WatchStopped {
			stops++
		}
		if strings.Contains(transition.reason, "a newer build is installed") {
			t.Fatalf("the session said %q, want no wait for a least time between moves", transition.reason)
		}
	}
	if stops != 1 {
		t.Fatalf("the session stopped %d time(s), want one move", stops)
	}
}
