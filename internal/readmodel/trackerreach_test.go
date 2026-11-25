package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

type reachedTracker struct {
	Tracker
	reach beads.StoreReach
}

func (r reachedTracker) Reach(context.Context) (beads.StoreReach, bool) { return r.reach, true }

func TestTheStandingSaysHowTheTrackerIsReached(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 11, 25, 9, 0, 0, 0, time.Local)
	for want, reach := range map[string]beads.StoreReach{
		"Tracker: the tracker is reached through its server, process 42 on port 41877, since 2026-11-25 09:00": {ThroughServer: true, PID: 42, Port: 41877, Since: since},
		"Tracker: the tracker's store is opened directly, since 2026-11-25 09:00":                              {Since: since, Why: "the product was stopped"},
		"Tracker: the tracker's store is opened directly: no dolt":                                             {Why: "no dolt"},
		"Tracker: the tracker is not being reached: its calls wait for its server":                             {Waiting: true, Why: "it died"},
	} {
		standing := Standing{TrackerReach: readTrackerReach(t.Context(), Sources{Tracker: reachedTracker{reach: reach}})}
		if said := standing.RenderTrackerReach(); !strings.HasPrefix(said, want) || !strings.Contains(said, reach.Why) {
			t.Errorf("RenderTrackerReach() = %q, want %q", said, want)
		}
	}
}
