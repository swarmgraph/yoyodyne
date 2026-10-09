package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// droppedFromTheQueue is the forge removing an approved change's pull request
// from its merge queue after its checks passed: the head level with main, every
// check green, and the forge's merge gone.
func droppedFromTheQueue(t *testing.T, removal publish.QueueRemoval, unread error) (queuedFixture, Reconciler) {
	t.Helper()
	fixture, forge, _ := queuedOnProtectedTarget(t)
	forge.Reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 3}
	forge.Removal = removal
	forge.RemovalErr = unread
	fixture.docket = &memoryDocket{}
	reconciler := fixture.sweep(t, forge, false)
	forge.DropQueuedMerge()
	return fixture, reconciler
}

// settleDrop runs the sweep over the dropped merge and returns what it recorded:
// the run's publication failure and the item's blocker.
func settleDrop(t *testing.T, fixture queuedFixture, reconciler Reconciler) (string, string) {
	t.Helper()
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the dropped merge settled on a blocker", results)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.MergeDrop == nil || settled.MergeDrop.Reason != settled.PublishFailure {
		t.Fatalf("merge drop = %#v, want the drop recorded with the publication failure as its reason", settled.MergeDrop)
	}
	return settled.PublishFailure, fixture.tracker.Record().BlockReason
}

// assertTheDevelopmentManagerMovesNext is the half of every drop's record that
// does not depend on what the forge said: the development manager moves next,
// and nothing says a person is needed, because nothing in a dropped merge is on
// the closed list of acts only a person can perform.
func assertTheDevelopmentManagerMovesNext(t *testing.T, recorded ...string) {
	t.Helper()
	for _, text := range recorded {
		if !strings.Contains(text, "the next move is the development manager's") {
			t.Errorf("record does not name the development manager as moving next:\n%s", text)
		}
		for _, refused := range []string{"needs a person", "the operator", "somebody"} {
			if strings.Contains(text, refused) {
				t.Errorf("record says %q:\n%s", refused, text)
			}
		}
	}
}

// Where the forge says why it removed the request, the record carries its own
// words and the requirement its merge state reports unmet.
func TestADroppedMergeRecordsTheForgesOwnReasonAndTheUnmetRequirement(t *testing.T) {
	t.Parallel()

	queued := time.Date(2026, 10, 5, 20, 10, 0, 0, time.UTC)
	fixture, reconciler := droppedFromTheQueue(t, publish.QueueRemoval{
		MergeStatus: "BLOCKED",
		Events: []publish.QueueEvent{
			{Kind: "AddedToMergeQueueEvent", At: queued},
			{Kind: "RemovedFromMergeQueueEvent", At: queued.Add(22 * time.Minute), Reason: "Required status check \"merge-queue build\" is expected."},
		},
	}, nil)
	failure, blocker := settleDrop(t, fixture, reconciler)
	for _, want := range []string{
		`The forge's reason: "Required status check \"merge-queue build\" is expected."`,
		"The requirement of main the forge reports unmet: the base branch's protection rules are not satisfied (BLOCKED)",
		"removed from the merge queue at " + queued.Add(22*time.Minute).Local().Format("2006-01-02 15:04 MST"),
	} {
		for name, text := range map[string]string{"publication failure": failure, "blocker": blocker} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not say %q:\n%s", name, want, text)
			}
		}
	}
	assertTheDevelopmentManagerMovesNext(t, failure, blocker)
}

// Where the forge gives no reason and reports nothing unmet, the record says so
// in as many words and carries what the harness could read at that moment: the
// checks on the head and the queue events on the request's timeline.
func TestADroppedMergeTheForgeGaveNoReasonForSaysSoWithWhatCouldBeRead(t *testing.T) {
	t.Parallel()

	queued := time.Date(2026, 10, 5, 20, 10, 0, 0, time.UTC)
	fixture, reconciler := droppedFromTheQueue(t, publish.QueueRemoval{
		MergeStatus: "CLEAN",
		Events: []publish.QueueEvent{
			{Kind: "AddedToMergeQueueEvent", At: queued},
			{Kind: "RemovedFromMergeQueueEvent", At: queued.Add(22 * time.Minute)},
		},
	}, nil)
	failure, blocker := settleDrop(t, fixture, reconciler)
	for _, want := range []string{
		"The forge gave no reason for the drop and reports no unmet requirement of main (its merge state is CLEAN)",
		"checks passing",
		"level with main",
		"added to the merge queue at " + queued.Local().Format("2006-01-02 15:04 MST"),
		"removed from the merge queue at " + queued.Add(22*time.Minute).Local().Format("2006-01-02 15:04 MST") + " (no reason given)",
	} {
		if !strings.Contains(failure, want) {
			t.Errorf("publication failure does not say %q:\n%s", want, failure)
		}
	}
	assertTheDevelopmentManagerMovesNext(t, failure, blocker)

	// A forge that cannot be asked for its account says that rather than
	// inventing one, and the move is still hers.
	unread, reconciler := droppedFromTheQueue(t, publish.QueueRemoval{}, errors.New("HTTP 502 from the forge"))
	failure, blocker = settleDrop(t, unread, reconciler)
	for _, want := range []string{"The forge's reason and its merge state were not read, because they could not be read: HTTP 502 from the forge", "checks passing", "merge queue events: not read"} {
		if !strings.Contains(failure, want) {
			t.Errorf("publication failure does not say %q:\n%s", want, failure)
		}
	}
	assertTheDevelopmentManagerMovesNext(t, failure, blocker)
}

// A change whose repair allowance is already spent is not left with nothing
// sending it anywhere: the drop puts it on the development manager's docket,
// and the entry carries the forge's account and the spent repairs, which is the
// evidence her decision is made on.
func TestADroppedMergeWithItsRepairsSpentIsPutOnTheDevelopmentManagersDocket(t *testing.T) {
	t.Parallel()

	fixture, reconciler := droppedFromTheQueue(t, publish.QueueRemoval{
		MergeStatus: "BLOCKED",
		Events:      []publish.QueueEvent{{Kind: "RemovedFromMergeQueueEvent", At: time.Date(2026, 10, 5, 20, 32, 0, 0, time.UTC), Reason: "Merge conflict with main"}},
	}, nil)
	spent, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	spent.RepairAttempts = 2
	if err := fixture.store.Save(spent); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	_, blocker := settleDrop(t, fixture, reconciler)

	entries, err := fixture.docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Class != triage.ClassStoppedRun || entries[0].RunID != pipelineRunID {
		t.Fatalf("docket = %#v, want the stopped run put in front of the development manager", entries)
	}
	for _, want := range []string{
		`The forge's reason: "Merge conflict with main"`,
		"(BLOCKED)",
		"Repair attempts already spent: 2",
		"Next move: the development manager's",
		"whatever repair attempts remain",
	} {
		if !strings.Contains(entries[0].Blocker, want) {
			t.Errorf("docket entry does not carry %q:\n%s", want, entries[0].Blocker)
		}
	}
	if entries[0].Blocker != blocker {
		t.Errorf("docket entry blocker differs from the item's:\n%s\n---\n%s", entries[0].Blocker, blocker)
	}
	assertTheDevelopmentManagerMovesNext(t, blocker)
}
