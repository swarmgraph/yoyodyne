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

	entry := runstate.MergeQueueEntry{EntryID: "mqe-1", Order: 49, TargetBranch: "main", WorkItemID: "yoyodyne-task", WorkItemTitle: "Release the queue head", RunID: "run-1", ApprovedHead: "abc"}
	recorded := time.Now()
	for _, test := range []struct {
		name   string
		result orchestrator.MergeQueuePromotion
		want   string
	}{
		{"landed and recorded", orchestrator.MergeQueuePromotion{Entry: entry, HandedBack: &runstate.MergeQueueHandback{Continuation: runstate.MergeQueueLandedOutside, HandedBackAt: &recorded}}, "as landed: its approved commit abc is on main"},
		{"landed, recording unfinished", orchestrator.MergeQueuePromotion{Entry: entry, HandedBack: &runstate.MergeQueueHandback{Continuation: runstate.MergeQueueLandedOutside}}, "is not finished"},
		{"handed back", orchestrator.MergeQueuePromotion{Entry: entry, HandedBack: &runstate.MergeQueueHandback{Continuation: runstate.MergeQueueReleased}}, `handed it back to "Release the queue head" (yoyodyne-task)`},
		{"landed through the queue", orchestrator.MergeQueuePromotion{Entry: entry, Completed: true}, "through the merge queue itself, so it was recorded"},
	} {
		got := queueReleaseSays(test.result)
		if !strings.Contains(got, test.want) || !strings.Contains(got, "entry 49") {
			t.Errorf("%s: queueReleaseSays() = %q, want it to say %q", test.name, got, test.want)
		}
		// A work item is named by what it is, never by its identifier alone.
		if strings.Contains(got, "yoyodyne-task") && !strings.Contains(got, `"Release the queue head" (yoyodyne-task)`) {
			t.Errorf("%s: queueReleaseSays() = %q, names the item by its identifier alone", test.name, got)
		}
	}
}
