package readmodel

import (
	"context"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// TrackerReach is how the tracker's store is reached: through its server, or
// opened directly and why. Said is the sentence every surface shows.
type TrackerReach struct {
	beads.StoreReach
	Said string `json:"said"`
}

// readTrackerReach asks the tracker, where it is one that answers, as the
// harness's own client is.
func readTrackerReach(ctx context.Context, sources Sources) *TrackerReach {
	tracker, ok := sources.Tracker.(interface {
		Reach(context.Context) (beads.StoreReach, bool)
	})
	if !ok {
		return nil
	}
	reach, found := tracker.Reach(ctx)
	if !found {
		return nil
	}
	return &TrackerReach{StoreReach: reach, Said: reach.Says()}
}

// RenderTrackerReach is that sentence on a line of its own, which `yoyo status`
// prints under the services.
func (s Standing) RenderTrackerReach() string {
	if s.TrackerReach == nil {
		return ""
	}
	return "Tracker: " + s.TrackerReach.Said + "\n"
}
