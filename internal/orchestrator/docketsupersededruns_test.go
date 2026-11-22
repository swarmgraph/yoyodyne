package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// earlierStoppedRun is a run that stopped with its change kept on its branch.
func earlierStoppedRun(runID, workItemID string, started time.Time) runstate.State {
	state := stoppedState()
	state.RunID = runID
	state.WorkItemID = workItemID
	state.StartedAt = started
	completed := started.Add(time.Hour)
	state.UpdatedAt = completed
	state.CompletedAt = &completed
	state.Branch = "yoyodyne/" + workItemID + "/" + runID[len(runID)-8:]
	return state
}

// laterApprovedHolding is a run the reviewer approved that is at its
// promotion, holding the approved change on its branch.
func laterApprovedHolding(runID, workItemID string, started time.Time) runstate.State {
	state := redispatchedRun(runID, workItemID, started)
	state.Status = runstate.StatusRunning
	state.Phase = runstate.PhaseIntegrating
	state.CompletedAt = nil
	state.ReviewSessionID = "review-session-1"
	state.Branch = "yoyodyne/" + workItemID + "/" + runID[len(runID)-8:]
	return state
}

// laterLanded is a run whose change reached the target branch.
func laterLanded(runID, workItemID string, started time.Time) runstate.State {
	state := redispatchedRun(runID, workItemID, started)
	state.ReviewSessionID = "review-session-2"
	state.Integration = &runstate.Integration{
		TargetBranch:         "main",
		SourceCommit:         strings.Repeat("c", 40),
		TargetCommit:         strings.Repeat("d", 40),
		PreviousTargetCommit: strings.Repeat("a", 40),
	}
	return state
}

// laterThroughPullRequest is a run whose approved change was promoted through
// a pull request: its integration is recorded, and whether the forge has merged
// the pull request is what merged says.
func laterThroughPullRequest(runID, workItemID string, started time.Time, number int, merged bool) runstate.State {
	state := laterLanded(runID, workItemID, started)
	state.Integration.ThroughPullRequest = true
	state.PullRequest = &runstate.PullRequest{
		Remote:     "origin",
		Branch:     "yoyodyne/" + workItemID + "/" + runID[len(runID)-8:],
		Number:     number,
		HeadCommit: strings.Repeat("c", 40),
		State:      "OPEN",
	}
	if merged {
		state.PullRequest.State = "MERGED"
		state.PullRequest.Merged = true
		state.PullRequest.MergeCommit = strings.Repeat("e", 40)
	}
	return state
}

// laterStillWorking is a later run that stopped without an approval: it has
// replaced nothing.
func laterStillWorking(runID, workItemID string, started time.Time) runstate.State {
	state := earlierStoppedRun(runID, workItemID, started)
	state.ReviewDecision = runstate.ReviewRepair
	return state
}

// An earlier stopped run is settled by the harness once a later run of the same
// item holds an approved change or has landed it, naming that later run, and a
// wait standing over it gives way. An earlier run whose later run has not been
// approved stays on the docket, and so does a run that started after a landing.
func TestAnEarlierRunIsSettledOnceALaterRunHoldsOrLandsTheApprovedChange(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	const (
		heldItem     = "yoyodyne-ifd.601"
		landedItem   = "yoyodyne-ifd.602"
		workingItem  = "yoyodyne-ifd.603"
		reopenedItem = "yoyodyne-ifd.604"
		openPRItem   = "yoyodyne-ifd.605"
		mergedPRItem = "yoyodyne-ifd.606"

		heldEarlier     = "run-a1111111111111111111111111111111"
		heldLater       = "run-a2222222222222222222222222222222"
		landedEarlier   = "run-b1111111111111111111111111111111"
		landedLater     = "run-b2222222222222222222222222222222"
		workingEarlier  = "run-c1111111111111111111111111111111"
		workingLater    = "run-c2222222222222222222222222222222"
		reopenedLanding = "run-d1111111111111111111111111111111"
		reopenedAfter   = "run-d2222222222222222222222222222222"
		openPREarlier   = "run-e1111111111111111111111111111111"
		openPRLater     = "run-e2222222222222222222222222222222"
		mergedPREarlier = "run-f1111111111111111111111111111111"
		mergedPRLater   = "run-f2222222222222222222222222222222"
	)
	early := docketedNow.Add(-12 * time.Hour)
	late := docketedNow.Add(-4 * time.Hour)
	earlier := []runstate.State{
		earlierStoppedRun(heldEarlier, heldItem, early),
		earlierStoppedRun(landedEarlier, landedItem, early),
		earlierStoppedRun(workingEarlier, workingItem, early),
		laterLanded(reopenedLanding, reopenedItem, early),
		earlierStoppedRun(reopenedAfter, reopenedItem, late),
		earlierStoppedRun(openPREarlier, openPRItem, early),
		earlierStoppedRun(mergedPREarlier, mergedPRItem, early),
	}
	recorded := &recordedDecisions{}
	docketer := Docketer{
		Docket:    store,
		Runs:      recordedRuns{states: earlier},
		Decisions: recorded,
		Reruns:    recorded,
		Caps:      docketedCaps,
		Triage:    docketedTriage,
		Clock:     docketClockAt{at: late},
	}
	first, err := docketer.Build()
	if err != nil {
		t.Fatalf("first Build() error = %v", err)
	}
	if len(first.Entries) != 6 {
		t.Fatalf("first build entries = %#v, want the six stopped runs docketed", first.Entries)
	}

	// The development manager waits two hours on the landed item's earlier run,
	// which is the wait that put such entries back every two hours.
	keyOf := func(runID string) string { return triage.Key(triage.ClassStoppedRun, runID) }
	if took, err := store.Close(triage.Closure{
		SchemaVersion: triage.ClosureSchemaVersion,
		Key:           keyOf(landedEarlier),
		ProductID:     "yoyodyne",
		RunID:         landedEarlier,
		WorkItemID:    landedItem,
		Decision:      "wait",
		Reason:        "a later run of the item is under way",
		DecidedBy:     "the development manager in conversation chat-0123456789abcdef",
		ClosedAt:      late,
		RevisitAfter:  late.Add(2 * time.Hour),
	}); err != nil || !took {
		t.Fatalf("Close(wait) = %v, %v, want the wait recorded", took, err)
	}

	docketer.Runs = recordedRuns{states: append(earlier,
		laterApprovedHolding(heldLater, heldItem, late),
		laterLanded(landedLater, landedItem, late),
		laterStillWorking(workingLater, workingItem, late),
		laterThroughPullRequest(openPRLater, openPRItem, late, 2701, false),
		laterThroughPullRequest(mergedPRLater, mergedPRItem, late, 2702, true),
	)}
	docketer.Clock = docketClock{}
	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	listed := map[string]bool{}
	for _, entry := range built.Listed() {
		listed[entry.RunID] = true
	}
	for _, runID := range []string{heldEarlier, landedEarlier, openPREarlier, mergedPREarlier} {
		if listed[runID] {
			t.Errorf("run %s is still on the development manager's list, want it settled by the later run", runID)
		}
	}
	for _, runID := range []string{workingEarlier, workingLater, reopenedAfter} {
		if !listed[runID] {
			t.Errorf("run %s is off the development manager's list, want it left for her: no later run holds or landed its approved change", runID)
		}
	}

	entries, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	wantLater := map[string]string{heldEarlier: heldLater, landedEarlier: landedLater, openPREarlier: openPRLater, mergedPREarlier: mergedPRLater}
	// What the reason says the later run did, and what it must not say: a pull
	// request the forge has not merged is never described as landed.
	wantSays := map[string]string{
		heldEarlier:     "holds a change the reviewer approved, on branch yoyodyne/" + heldItem,
		landedEarlier:   "landed its change on main at commit " + strings.Repeat("c", 40),
		openPREarlier:   "has its approved change in pull request #2701 into main, waiting for the forge to merge it",
		mergedPREarlier: "landed its change on main through pull request #2702, merged at commit " + strings.Repeat("e", 40),
	}
	for _, entry := range entries {
		later, settled := wantLater[entry.RunID]
		if !settled {
			if entry.Closed != nil && entry.Closed.Decision == triage.SupersededByLaterRunDecision {
				t.Errorf("run %s was settled as superseded, want it left alone", entry.RunID)
			}
			continue
		}
		closed := entry.Closed
		if closed == nil || closed.Decision != triage.SupersededByLaterRunDecision {
			t.Fatalf("closure of run %s = %#v, want it settled as superseded by a later run", entry.RunID, closed)
		}
		if !strings.Contains(closed.Reason, later) || !strings.Contains(closed.Reason, entry.RunID) {
			t.Errorf("reason = %q, want it to name the later run %s and the earlier run %s", closed.Reason, later, entry.RunID)
		}
		if !strings.Contains(closed.Reason, wantSays[entry.RunID]) {
			t.Errorf("reason = %q, want it to say %q", closed.Reason, wantSays[entry.RunID])
		}
		if entry.RunID == openPREarlier && strings.Contains(closed.Reason, "landed") {
			t.Errorf("reason = %q, want a pull request the forge has not merged never called landed", closed.Reason)
		}
		if !strings.Contains(closed.DecidedBy, "the harness") {
			t.Errorf("decided by %q, want the harness named", closed.DecidedBy)
		}
	}

	// Once the two hours the wait named are past, the settled entry does not come
	// back, and nothing is docketed again.
	docketer.Clock = docketClockAt{at: docketedNow.Add(3 * time.Hour)}
	rebuilt, err := docketer.Build()
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if rebuilt.Added != 0 {
		t.Errorf("second build added %d entries, want none", rebuilt.Added)
	}
	for _, entry := range rebuilt.Listed() {
		if wantLater[entry.RunID] != "" {
			t.Errorf("run %s came back on the list after its wait ran out", entry.RunID)
		}
	}
}
