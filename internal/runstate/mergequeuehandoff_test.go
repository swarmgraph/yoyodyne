package runstate

// The waits from a landing to the queue's next check are kept beside the
// queue, one for each landing, and read back by a process that did not write
// them.

import (
	"context"
	"testing"
	"time"
)

func TestAQueueKeepsOneHandoffForEachLanding(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newMergeQueueStore(t, root)
	ctx := context.Background()
	merged := time.Date(2026, 11, 9, 23, 22, 12, 0, time.UTC)
	landed := MergeQueueEntry{EntryID: "entry-20", WorkItemID: "yoyodyne-one"}
	next := MergeQueueEntry{EntryID: "entry-21", WorkItemID: "yoyodyne-two", AdmittedAt: merged.Add(-time.Hour)}

	handoff := NewMergeQueueHandoff(landed, merged, merged.Add(36*time.Minute), next, merged.Add(38*time.Minute))
	if handoff.Wait() != 38*time.Minute {
		t.Fatalf("wait = %s, want the 38 minutes from the merge to the check", handoff.Wait())
	}
	if added, err := store.RecordHandoff(ctx, mainQueue, handoff); err != nil || !added {
		t.Fatalf("RecordHandoff() = %t, %v; want it added", added, err)
	}
	// A second check after the same landing — the next candidate rebuilt — is
	// not a second handoff.
	again := NewMergeQueueHandoff(landed, merged, merged.Add(36*time.Minute), next, merged.Add(50*time.Minute))
	if added, err := store.RecordHandoff(ctx, mainQueue, again); err != nil || added {
		t.Fatalf("RecordHandoff() of the same landing = %t, %v; want it kept as it was", added, err)
	}

	// An entry admitted after the merge waited only from its admission.
	later := MergeQueueEntry{EntryID: "entry-22", AdmittedAt: merged.Add(2 * time.Hour)}
	idle := NewMergeQueueHandoff(next, merged.Add(time.Hour), merged.Add(time.Hour), later, merged.Add(2*time.Hour+3*time.Minute))
	if idle.Wait() != 3*time.Minute {
		t.Fatalf("wait = %s, want the 3 minutes from the admission to the check", idle.Wait())
	}
	if _, err := store.RecordHandoff(ctx, mainQueue, idle); err != nil {
		t.Fatalf("RecordHandoff() error = %v", err)
	}

	handoffs, err := newMergeQueueStore(t, root).Handoffs(mainQueue)
	if err != nil || len(handoffs) != 2 || handoffs[0] != handoff || handoffs[1] != idle {
		t.Fatalf("Handoffs() = %#v, %v; want the two handoffs as written", handoffs, err)
	}
}

func TestAHandoffMissingWhatItMeasuresIsRefused(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	if _, err := store.RecordHandoff(context.Background(), mainQueue, MergeQueueHandoff{LandedEntry: "entry-1", NextEntry: "entry-2"}); err == nil {
		t.Fatal("RecordHandoff() accepted a handoff with no times")
	}
}
