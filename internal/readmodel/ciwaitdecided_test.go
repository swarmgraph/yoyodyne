package readmodel

import (
	"strings"
	"testing"
	"time"
)

// A run waiting on GitHub's checks whose decision the scheduler has recorded is
// shown with that decision, and a continuation the development manager asked
// for that has not started is shown with why, so neither reads as a wait on
// checks still running or a request carried out.
func TestAPausedRunSaysGitHubsDecisionAndWhyHerContinuationWaits(t *testing.T) {
	standing := Standing{
		ObservedAt: time.Date(2026, 11, 20, 23, 40, 0, 0, time.UTC),
		PausedRuns: []RunningRun{{
			WorkItemID:          "yoyodyne-ifd.429.127.2",
			WaitingForCI:        true,
			WaitingOn:           "the forge's checks (test, race, vet) on commit afee2181",
			CIWaited:            2 * time.Hour,
			CIDecided:           "every required check passed on the commit pushed (read at 2026-11-20 14:58 PST)",
			ContinuationWaiting: "the development manager's request dm-1120-1427-429127 to continue it has not started: developer slot 2 is held by run run-a15c973e",
			NotContinued:        "not continued since 2026-11-20 15:00 PST: the developer slots free at this pull went to written work ahead of it",
		}, {
			WorkItemID:   "yoyodyne-ifd.435.52",
			WaitingOn:    "yoyodyne-ifd.435.51",
			PausedSince:  time.Date(2026, 11, 20, 18, 7, 0, 0, time.UTC),
			NotContinued: "not continued since 2026-11-20 15:01 PST: run run-1 was paused waiting on yoyodyne-ifd.435.51, which has closed, and is continued when a developer slot is free",
		}},
	}
	rendered := standing.renderPaused()
	for _, want := range []string{
		"waiting for CI: the forge's checks (test, race, vet) on commit afee2181",
		"GitHub has decided: every required check passed on the commit pushed (read at 2026-11-20 14:58 PST); the run is continued ahead of new work",
		"the development manager's request dm-1120-1427-429127 to continue it has not started: developer slot 2 is held by run run-a15c973e",
		"    not continued since 2026-11-20 15:00 PST: the developer slots free at this pull went to written work ahead of it",
		"    not continued since 2026-11-20 15:01 PST: run run-1 was paused waiting on yoyodyne-ifd.435.51",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("paused runs = %q, want it to say %q", rendered, want)
		}
	}
}
