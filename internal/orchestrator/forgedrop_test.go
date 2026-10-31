package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
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

// queuedMergeCommit is the merge the forge's merge queue built of the request,
// which is where the queue ran the checks it removed the request for.
const queuedMergeCommit = "4444444444444444444444444444444444444444"

// removedForFailedChecks is the forge's timeline for a request its merge queue
// took at 06:22 PDT and removed at 06:39 PDT, as it removed pull request 1543.
func removedForFailedChecks() publish.QueueRemoval {
	queued := time.Date(2026, 10, 30, 13, 22, 0, 0, time.UTC)
	return publish.QueueRemoval{
		MergeStatus: "CLEAN",
		Events: []publish.QueueEvent{
			{Kind: "AddedToMergeQueueEvent", At: queued},
			{Kind: "RemovedFromMergeQueueEvent", At: queued.Add(17 * time.Minute)},
		},
	}
}

// A request the forge's merge queue removed for failed checks, whose own head
// passes every check, is stopped naming the checks the queue ran on the merge it
// built and failed: on the run's record, with the end of each failing check's
// output; on the item's blocker and in its notes; and on the development
// manager's docket. Nothing it records says the checks pass.
func TestADropForChecksFailedOnTheQueuedMergeNamesEachFailingCheckAndTheEndOfItsOutput(t *testing.T) {
	t.Parallel()

	fixture, reconciler := droppedFromTheQueue(t, removedForFailedChecks(), nil)
	reconciler.Drops.(*orchestratortest.Forge).QueuedMerge = publish.QueuedMergeChecks{
		Commit:  queuedMergeCommit,
		Failing: []publish.FailedCheck{{Name: "test", ID: 901, Conclusion: "failure", URL: "https://example.invalid/acme/thing/actions/runs/9/job/901"}},
		Passing: 4,
	}
	logs := &orchestratortest.JobLogs{Job: "test", Step: "Run make race", Tail: "--- FAIL: TestTrackerReadsWithinTheBound (120.01s)\n##[error]Process completed with exit code 1."}
	reconciler.JobLogs = logs
	spent, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	spent.RepairAttempts = 2
	if err := fixture.store.Save(spent); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	failure, blocker := settleDrop(t, fixture, reconciler)

	named := "The checks the forge's merge queue ran on its merge of pull request"
	for name, text := range map[string]string{"publication failure": failure, "blocker": blocker} {
		for _, want := range []string{named, "(commit " + queuedMergeCommit[:12] + ") failed: test (failure)", "which is not what the queue checked"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not say %q:\n%s", name, want, text)
			}
		}
		if strings.Contains(text, "checks passing") || strings.Contains(text, "did not fail") {
			t.Errorf("%s says the checks pass over a queued merge that failed them:\n%s", name, text)
		}
	}
	assertTheDevelopmentManagerMovesNext(t, failure, blocker)

	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	queued := settled.MergeDrop.QueuedChecks
	if queued == nil || queued.Commit != queuedMergeCommit || len(queued.Failing) != 1 || queued.Failing[0].Name != "test" {
		t.Fatalf("queued checks on the record = %#v, want the failing check on the queued merge", queued)
	}
	for _, want := range []string{"--- FAIL: TestTrackerReadsWithinTheBound", "Run make race", "Commit: " + queuedMergeCommit} {
		if !strings.Contains(queued.Failing[0].Account, want) {
			t.Errorf("the record's account of the failing check does not carry %q:\n%s", want, queued.Failing[0].Account)
		}
	}
	if len(logs.Asked) != 1 || logs.Asked[0] != 901 {
		t.Errorf("job logs asked for %v, want the failing check's job on the queued merge", logs.Asked)
	}
	if notes := fixture.tracker.Record().Notes; !strings.Contains(notes, "--- FAIL: TestTrackerReadsWithinTheBound") || !strings.Contains(notes, "which are the checks the queue removed it for") {
		t.Errorf("item notes do not carry the queued merge's failing output:\n%s", notes)
	}

	entries, err := fixture.docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Class != triage.ClassStoppedRun {
		t.Fatalf("docket = %#v, want the stopped run in front of the development manager", entries)
	}
	if rendered := entries[0].Render(); !strings.Contains(rendered, "failed: test (failure)") || strings.Contains(rendered, "checks passing") {
		t.Errorf("the development manager's list does not name the failing check, or says the checks pass:\n%s", rendered)
	}
}

// Where the checks the queue ran cannot be read, or no merge the queue built is
// found, the stop says so in as many words and says the head's own checks are
// not the queue's; it never reads the head's as the answer.
func TestADropWhoseQueuedMergeChecksCannotBeReadSaysSo(t *testing.T) {
	t.Parallel()

	fixture, reconciler := droppedFromTheQueue(t, removedForFailedChecks(), nil)
	reconciler.Drops.(*orchestratortest.Forge).QueuedMergeErr = errors.New("ask the forge for its merge queue's runs for pull request 1: exit code 1: HTTP 502")
	failure, blocker := settleDrop(t, fixture, reconciler)
	for name, text := range map[string]string{"publication failure": failure, "blocker": blocker} {
		for _, want := range []string{"could not be read (", "HTTP 502", "so whether they passed is not known", "the checks below are the pull request's own, which are not the ones the queue ran"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not say %q:\n%s", name, want, text)
			}
		}
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if queued := settled.MergeDrop.QueuedChecks; queued == nil || queued.Read() || !strings.Contains(queued.ReadError, "HTTP 502") {
		t.Errorf("queued checks on the record = %#v, want them recorded as unread with the forge's words", queued)
	}

	missing, reconciler := droppedFromTheQueue(t, removedForFailedChecks(), nil)
	failure, _ = settleDrop(t, missing, reconciler)
	if !strings.Contains(failure, "found no merge the forge's merge queue built of pull request") || !strings.Contains(failure, "what the queue's checks said is not known") {
		t.Errorf("publication failure does not say no queued merge was found:\n%s", failure)
	}
}

// A drop the merge queue had no part in reads nothing of it and records nothing.
func TestADropTheMergeQueueHadNoPartInReadsNoQueuedMerge(t *testing.T) {
	t.Parallel()

	fixture, reconciler := droppedFromTheQueue(t, publish.QueueRemoval{
		MergeStatus: "DIRTY",
		Events:      []publish.QueueEvent{{Kind: "AutoMergeDisabledEvent", At: time.Date(2026, 10, 5, 20, 32, 0, 0, time.UTC), Reason: "Merge conflict"}},
	}, nil)
	reconciler.Drops.(*orchestratortest.Forge).QueuedMergeErr = errors.New("the queued merge must not be asked about")
	failure, _ := settleDrop(t, fixture, reconciler)
	if strings.Contains(failure, "merge queue ran") || strings.Contains(failure, "must not be asked") {
		t.Errorf("publication failure speaks of a queued merge the queue never built:\n%s", failure)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.MergeDrop.QueuedChecks != nil {
		t.Errorf("queued checks = %#v, want none recorded", settled.MergeDrop.QueuedChecks)
	}
}
