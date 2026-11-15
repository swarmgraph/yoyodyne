package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// What the development manager's reply is told a release of the head of a
// merge queue did: landed outside the queue, handed back, or found already
// landed through the queue itself.
func TestQueueReleaseSaysWhatBecameOfTheEntry(t *testing.T) {
	t.Parallel()

	entry := runstate.MergeQueueEntry{EntryID: "mqe-1", Order: 49, TargetBranch: "main", WorkItemID: "yoyodyne-task", RunID: "run-1", ApprovedHead: "abc"}
	recorded := time.Now()
	for _, test := range []struct {
		name   string
		result orchestrator.MergeQueuePromotion
		want   string
	}{
		{"landed and recorded", orchestrator.MergeQueuePromotion{Entry: entry, HandedBack: &runstate.MergeQueueHandback{Continuation: runstate.MergeQueueLandedOutside, HandedBackAt: &recorded}}, "as landed: its approved commit abc is on main"},
		{"landed, recording unfinished", orchestrator.MergeQueuePromotion{Entry: entry, HandedBack: &runstate.MergeQueueHandback{Continuation: runstate.MergeQueueLandedOutside}}, "is not finished"},
		{"handed back", orchestrator.MergeQueuePromotion{Entry: entry, HandedBack: &runstate.MergeQueueHandback{Continuation: runstate.MergeQueueReleased}}, "handed it back to yoyodyne-task"},
		{"landed through the queue", orchestrator.MergeQueuePromotion{Entry: entry, Completed: true}, "through the merge queue itself, so it was recorded"},
	} {
		if got := queueReleaseSays(test.result); !strings.Contains(got, test.want) || !strings.Contains(got, "entry 49") {
			t.Errorf("%s: queueReleaseSays() = %q, want it to say %q", test.name, got, test.want)
		}
	}
}
