package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

const (
	releaseItem   = "yoyodyne-fvn"
	readinessFix  = "yoyodyne-8mc"
	mergeQueueP0  = "yoyodyne-ifd.429.59.7"
	ranYesterday  = "yoyodyne-ran"
	day           = 24 * time.Hour
	wrongParking  = "waits on the operator"
	releaseTitled = "Cut v0.6.0: the release after v0.5.0"
)

type listedWaitingWork struct {
	reading WaitingWork
	err     error
}

func (l listedWaitingWork) Waiting(context.Context) (WaitingWork, error) { return l.reading, l.err }

// blockedRelease is a release item admitted four weeks ago, waiting on a
// readiness fix that last ran six weeks ago, with the latest tag two months old.
func blockedRelease() WaitingWork {
	return WaitingWork{
		LatestTag: "v0.5.0", TaggedAt: recurringNow.Add(-63 * day),
		Items: []WaitingItem{
			{ID: releaseItem, Title: releaseTitled, Priority: 1, Release: true, WaitsOn: []string{readinessFix}, AdmittedAt: recurringNow.Add(-28 * day)},
			{ID: readinessFix, Title: "Release readiness passes", Priority: 1, AdmittedAt: recurringNow.Add(-50 * day), LastRun: recurringNow.Add(-43 * day)},
		},
	}
}

// parkedPriority0 is a priority 0 item parked on a wrong reason, idle eight days.
func parkedPriority0() WaitingWork {
	return WaitingWork{
		LatestTag: "v0.6.0", TaggedAt: recurringNow.Add(-2 * day),
		Items: []WaitingItem{{ID: mergeQueueP0, Title: "Switch to the merge queue", Priority: 0, Parking: wrongParking, AdmittedAt: recurringNow.Add(-8 * day)}},
	}
}

func fireOverduePass(t *testing.T, reading WaitingWork, answers ...scriptedTurn) (*wokenRole, runstate.Sweep) {
	t.Helper()
	store := sweepStore(t)
	role := &wokenRole{answers: answers}
	trigger := Trigger{Tasks: productManagerSweep(), Claims: store, Reports: store, Roles: role,
		WaitingWork: listedWaitingWork{reading: reading}, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	recorded, _, err := store.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("recorded = %+v (%v), want one pass", recorded, err)
	}
	return role, recorded[0]
}

func actedOnOverdue(summary string, entries ...sweep.Overdue) *sweep.Result {
	result := complete(summary)
	result.Overdue = entries
	return result
}

func TestABlockedReleaseOlderThanSevenDaysIsSentBackWhenOnlyDeferredAndAcceptedWhenUnblocked(t *testing.T) {
	t.Parallel()
	unblocked := actedOnOverdue("Started the readiness fix.", sweep.Overdue{Item: releaseItem, Act: sweep.ActUnblocked, Reason: "placed the readiness fix first so the release can be built"})
	role, pass := fireOverduePass(t, blockedRelease(),
		scriptedTurn{result: complete("Not yet: the readiness fix is still open.")},
		scriptedTurn{result: unblocked, actedOn: []string{readinessFix}},
	)
	first := role.messages[0]
	for _, want := range []string{
		"## Releases and priority 0 work waiting over seven days",
		"The latest release tag is v0.5.0", "63 days ago",
		"PAST SEVEN DAYS: " + releaseItem + " (" + releaseTitled + "), release item, admitted 28 days ago",
		readinessFix + " (Release readiness passes): no run for 43 days",
		`"overdue"`, "There is no act for deferring",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("first message does not carry %q:\n%s", want, first)
		}
	}
	if len(role.messages) != 2 {
		t.Fatalf("turns = %d, want 2: the deferral is sent back once", len(role.messages))
	}
	if back := role.messages[1]; !strings.Contains(back, "This pass is ending with "+releaseItem+" (the account records nothing about it)") {
		t.Errorf("sent-back message = %q, want it to name the release item", back)
	}
	if pass.Failed || pass.Result == nil || pass.Result.Summary != "Started the readiness fix." {
		t.Fatalf("pass = %+v, want the corrected account recorded", pass)
	}
	if !strings.Contains(pass.Problem, "sent the pass back once") || strings.Contains(pass.Problem, "only deferred again") {
		t.Errorf("problem = %q, want the send-back said and nothing left deferred", pass.Problem)
	}
}

func TestAParkedPriority0ItemIdleEightDaysIsSentBackWhenOnlyDeferredAndAcceptedWhenItsParkingIsLifted(t *testing.T) {
	t.Parallel()
	deferred := actedOnOverdue("Left the merge queue switch.", sweep.Overdue{Item: mergeQueueP0, Act: "deferred", Reason: "still waiting"})
	lifted := actedOnOverdue("Lifted the merge queue switch's parking.", sweep.Overdue{Item: mergeQueueP0, Act: sweep.ActParkingLifted, Reason: "it never waited on the operator"})
	role, pass := fireOverduePass(t, parkedPriority0(),
		scriptedTurn{result: deferred},
		scriptedTurn{result: lifted, actedOn: []string{mergeQueueP0}},
	)
	first := role.messages[0]
	for _, want := range []string{
		"1 open priority 0 item(s) have had no developer run and no closing in the past seven days",
		"PAST SEVEN DAYS: " + mergeQueueP0 + " (Switch to the merge queue), admitted 8 days ago, never run",
		`parked: "` + wrongParking + `"`, "waits on no open item",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("first message does not carry %q:\n%s", want, first)
		}
	}
	if len(role.messages) != 2 || !strings.Contains(role.messages[1], mergeQueueP0+` ("deferred" is not an act on the list)`) {
		t.Fatalf("messages = %q, want one send-back naming the item", role.messages)
	}
	if pass.Failed || strings.Contains(pass.Problem, "only deferred again") {
		t.Errorf("pass = %+v, want the lifted parking accepted", pass)
	}
}

func TestAnOverdueItemStillOnlyDeferredAfterTheSendBackIsNamedOnThePassRecord(t *testing.T) {
	t.Parallel()
	// The second turn claims an act no tracker action bears out.
	claimed := actedOnOverdue("Ordered it.", sweep.Overdue{Item: mergeQueueP0, Act: sweep.ActOrdered, Reason: "first among priority 0"})
	role, pass := fireOverduePass(t, parkedPriority0(),
		scriptedTurn{result: complete("Nothing to do.")},
		scriptedTurn{result: claimed},
	)
	if len(role.messages) != 2 {
		t.Fatalf("turns = %d, want 2: a pass is sent back once only", len(role.messages))
	}
	for _, want := range []string{"ended with " + mergeQueueP0, `the account says "ordered", but no tracker action of this pass changed it`, "only deferred again", "after it was sent back once"} {
		if !strings.Contains(pass.Problem, want) {
			t.Errorf("problem = %q, want %q", pass.Problem, want)
		}
	}
	if pass.Failed || pass.Result == nil {
		t.Errorf("pass = %+v, want it recorded with its account, not failed", pass)
	}
}

func TestAnExplanationByNameIsAcceptedWithoutATrackerAction(t *testing.T) {
	t.Parallel()
	explained := actedOnOverdue("Explained the merge queue switch.", sweep.Overdue{Item: mergeQueueP0, Act: sweep.ActExplained, Reason: "the reliability seat takes it after the run in flight lands"})
	role, pass := fireOverduePass(t, parkedPriority0(), scriptedTurn{result: explained})
	if len(role.messages) != 1 || strings.Contains(pass.Problem, mergeQueueP0) {
		t.Errorf("turns = %d, problem = %q, want an explained item accepted on the first turn", len(role.messages), pass.Problem)
	}
}

func TestARecentReleaseAndAPriority0ItemThatRanYesterdayAreUnaffected(t *testing.T) {
	t.Parallel()
	reading := WaitingWork{
		LatestTag: "v0.6.0", TaggedAt: recurringNow.Add(-2 * day),
		Items: []WaitingItem{
			// A release item admitted long ago, but the last release is recent.
			{ID: releaseItem, Title: "Cut v0.7.0: next", Release: true, WaitsOn: []string{readinessFix}, AdmittedAt: recurringNow.Add(-10 * day)},
			{ID: readinessFix, Title: "Release readiness passes", Priority: 1, AdmittedAt: recurringNow.Add(-20 * day), LastRun: recurringNow.Add(-9 * day)},
			{ID: ranYesterday, Title: "A priority 0 item", Priority: 0, AdmittedAt: recurringNow.Add(-30 * day), LastRun: recurringNow.Add(-day)},
		},
	}
	role, pass := fireOverduePass(t, reading, scriptedTurn{result: complete("Nothing waits.")})
	first := role.messages[0]
	if strings.Contains(first, "PAST SEVEN DAYS") || strings.Contains(first, `"overdue"`) {
		t.Errorf("first message holds the pass to something:\n%s", first)
	}
	for _, want := range []string{"made", "2 days ago", releaseItem + " (Cut v0.7.0: next), release item", readinessFix + " (Release readiness passes): no run for 9 days",
		"Every open priority 0 item has had a developer run, or a closing, in the past seven days"} {
		if !strings.Contains(first, want) {
			t.Errorf("first message does not carry %q:\n%s", want, first)
		}
	}
	if len(role.messages) != 1 || pass.Failed || pass.Problem != "" {
		t.Errorf("turns = %d, pass = %+v, want one ordinary turn", len(role.messages), pass)
	}
}

func TestAnUnreadableWaitingWorkReadingIsSaidAndHoldsThePassToNothing(t *testing.T) {
	t.Parallel()
	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{{result: complete("looked")}}}
	trigger := Trigger{Tasks: productManagerSweep(), Claims: store, Reports: store, Roles: role,
		WaitingWork: listedWaitingWork{err: errors.New("the tracker did not answer")}, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(role.messages) != 1 || !strings.Contains(role.messages[0], "could not be read, so this pass is held to none of them: the tracker did not answer") {
		t.Errorf("messages = %q, want the failed reading said once", role.messages)
	}
}

func TestOtherRolesAreHandedNoWaitingWork(t *testing.T) {
	t.Parallel()
	store := sweepStore(t)
	role := &wokenRole{answers: []scriptedTurn{{result: complete("looked")}}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role,
		WaitingWork: listedWaitingWork{reading: parkedPriority0()}, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(role.messages[0], "Releases and priority 0 work") {
		t.Errorf("another role's pass was handed the waiting work:\n%s", role.messages[0])
	}
}
