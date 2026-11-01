package cli

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// Every limit a command puts on a tracker call is the tracker's one setting, so
// raising it reaches the status reading, the dashboard, conversations, goals,
// cost, setup, and recurring passes together rather than leaving one of them
// timing out at the old figure.
func TestEveryCommandsTrackerLimitIsTheTrackersOwn(t *testing.T) {
	t.Parallel()
	for name, limit := range map[string]time.Duration{
		"chatTrackerTimeout":    chatTrackerTimeout,
		"goalsTrackerTimeout":   goalsTrackerTimeout,
		"costTrackerTimeout":    costTrackerTimeout,
		"trackerCommandTimeout": trackerCommandTimeout,
	} {
		if limit != beads.DefaultTimeout {
			t.Errorf("%s = %s, want beads.DefaultTimeout (%s)", name, limit, beads.DefaultTimeout)
		}
	}
	if beads.DefaultTimeout <= 30*time.Second {
		t.Fatalf("beads.DefaultTimeout = %s, want longer than the 30s an ordinary listing of the store now outruns", beads.DefaultTimeout)
	}
}
