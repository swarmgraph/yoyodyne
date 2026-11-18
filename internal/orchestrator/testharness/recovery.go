package testharness

import (
	"errors"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
)

// TrackerStalled is the tracker client giving up on a write bd never answered:
// its own read-backs could not say whether the write landed, so the run's
// retry has to ask before it makes it again.
var TrackerStalled = errors.New("bd update on yoyodyne-task did not answer within its 30s bound: 3 write(s) and 2 read-back(s) over 2m40s, and whether the last write landed is not known: bd update failed with status timed_out and exit code -1: ")

func Calls(tracker *orchestratortest.Tracker, call string) int {
	count := 0
	for _, made := range tracker.Calls {
		if made == call {
			count++
		}
	}
	return count
}
