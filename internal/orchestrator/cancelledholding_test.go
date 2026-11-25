package orchestrator

// A run that ends cancelled while it holds its work item is on the development
// manager's list, and the hold the health check reads names the same runs.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

func TestACancelledDeveloperRunReachesTheListTheHealthCheckAgreesWith(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: docketedItem, Title: "Task", Status: "open"}}
	ctx, cancel := context.WithCancel(context.Background())
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("half written\n"), 0o600); err != nil {
			return err
		}
		cancel()
		return context.Canceled
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

	outcome, err := pipeline.Run(ctx, tracker.Item.ID)
	if err == nil || outcome.Status != runstate.StatusCancelled {
		t.Fatalf("Run() = %q, %v; want the run cancelled", outcome.Status, err)
	}
	// On the list as the run ended, before any build.
	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].RunID != outcome.RunID {
		t.Fatalf("docket = %#v, want the cancelled run on it", entries)
	}
	built, err := pipeline.Docket.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 || built.Entries[0].RunID != outcome.RunID {
		t.Fatalf("list = %#v, want the cancelled run open on it once", built.Entries)
	}
	rendered := built.Entries[0].Render()
	for _, want := range []string{"Cancelled while it held its work item", "ended cancelled at its developing step", "told to stop"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("entry does not say %q:\n%s", want, rendered)
		}
	}
	held, err := readmodel.HeldForAPerson(context.Background(), store, store.Triage(), nil, nil)
	if err != nil {
		t.Fatalf("HeldForAPerson() error = %v", err)
	}
	if reason, holding := held.Reason(docketedItem); !holding || !strings.Contains(reason, outcome.RunID) || held.Decided(docketedItem) {
		t.Fatalf("hold = %q (held=%t), want the item held for the development manager naming the run", reason, holding)
	}
}

// Every way a run ends cancelled holding its item, recorded and then read by the
// list and by the hold, which have to name the same runs.
func TestTheListAndTheHoldNameTheSameCancelledRuns(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		ending func(*runstate.State)
		// later is a run of the same item started after the cancelled one.
		later  bool
		listed bool
		says   string
	}{
		"the operator's stop":         {ending: func(s *runstate.State) { s.StopClass = runstate.StopOperator }, listed: true, says: "cancelled by the operator"},
		"a provider's usage limit":    {ending: func(s *runstate.State) { s.StopClass = runstate.CauseUsageWindow.StopClass() }, listed: true, says: "usage limit"},
		"a move to a new build":       {ending: func(s *runstate.State) { s.StopClass = runstate.StopRedeploy }, listed: true, says: "new build"},
		"a process that went away":    {ending: func(s *runstate.State) { s.StopClass = runstate.StopDeadClaim }, listed: true, says: "did not come back"},
		"nothing recorded":            {ending: func(*runstate.State) {}, listed: true, says: "does not name"},
		"no failure recorded":         {ending: func(s *runstate.State) { s.Failure = "" }, listed: true, says: "does not name"},
		"nothing kept":                {ending: func(s *runstate.State) { s.StopClass = runstate.StopCancelled; gone(s) }, listed: true, says: "told to stop"},
		"a dead claim, nothing kept":  {ending: func(s *runstate.State) { s.StopClass = runstate.StopDeadClaim; gone(s) }},
		"a run that never claimed":    {ending: func(s *runstate.State) { s.WorkItemClaimedAt = nil }},
		"a later run of the item":     {ending: func(s *runstate.State) { s.StopClass = runstate.StopCancelled }, later: true},
		"her own stop, decided by it": {ending: func(s *runstate.State) { s.StopClass = runstate.StopManager }},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runs, err := runstate.NewStore(t.TempDir(), "yoyodyne")
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
			if err != nil {
				t.Fatalf("NewDocketStore() error = %v", err)
			}
			state := stoppedState()
			claimed := state.StartedAt
			state.RunID = undocketedRunID
			state.WorkItemID = "yoyodyne-cancelled"
			state.Status, state.Blocker, state.StopClass = runstate.StatusCancelled, "", ""
			state.Failure = "the step was stopped"
			state.CheckFailure, state.ReviewFindingDetails, state.ReviewFindings, state.ReviewSummary, state.ReviewDecision = nil, nil, 0, "", ""
			state.WorkItemClaimedAt = &claimed
			test.ending(&state)
			if err := runs.Create(state); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			docketer := docketerOverStore(docket, runs, rearmConfig())
			if test.later {
				if _, err := docketer.RecordStoppedRun(state); err != nil {
					t.Fatalf("RecordStoppedRun() error = %v", err)
				}
				later := state
				later.RunID = "run-44444444444444444444444444444444"
				later.StartedAt = later.StartedAt.Add(time.Minute)
				later.Status, later.StopClass = runstate.StatusRunning, ""
				later.CompletedAt, later.Failure = nil, ""
				if err := runs.Create(later); err != nil {
					t.Fatalf("Create() error = %v", err)
				}
			}

			built, err := docketer.Build()
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			var listed []string
			for _, entry := range built.Entries {
				if entry.WorkItemID == state.WorkItemID {
					listed = append(listed, entry.RunID)
					if !strings.Contains(entry.Cancelled, test.says) {
						t.Errorf("entry says %q, want %q in it", entry.Cancelled, test.says)
					}
				}
			}
			holds, err := readmodel.HeldForAPerson(context.Background(), runs, runs.Triage(), nil, nil)
			if err != nil {
				t.Fatalf("HeldForAPerson() error = %v", err)
			}
			reason, held := holds.Reason(state.WorkItemID)
			if want := test.listed; slices.Contains(listed, state.RunID) != want || held != want {
				t.Fatalf("listed = %v (problems %v), held = %t (%q); want both %t", listed, built.NotDocketed, held, reason, want)
			}
		})
	}
}

// gone is a run that ended before it recorded a branch or a worktree.
func gone(state *runstate.State) {
	state.Phase = runstate.PhaseDeveloping
	state.Branch, state.WorktreePath, state.BaseCommit, state.HarnessCommit, state.ProviderSessionID = "", "", "", "", ""
	state.RepairAttempts = 0
}

// A claim whose process died is released saying the run is hers to decide, and
// nothing pulls the item, only where something of the run was kept.
func TestADeadClaimsReleaseSaysWhoMovesNext(t *testing.T) {
	t.Parallel()

	released := runstate.ReleasedClaim{Because: "its process is gone", ReleasedAt: docketedNow}
	if note := releaseNotes(released, true); !strings.Contains(note, "development manager's to decide") || strings.Contains(note, "pulled again") {
		t.Fatalf("note = %q, want it on her list and not pulled again", note)
	}
	claimed := docketedNow
	run := runstate.State{Status: runstate.StatusRunning, WorkItemID: docketedItem, WorkItemClaimedAt: &claimed}
	if !run.HoldsItemOnceDeadClaim(true) || run.HoldsItemOnceDeadClaim(false) {
		t.Fatal("a dead claim's run is hers exactly where something of it was kept")
	}
}

// A cancelled run that kept nothing never strands its item: the claim is given
// back whatever has been decided, and the hold alone says when it may be pulled.
func TestACancelledRunThatKeptNothingNeverStrandsItsItem(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for name, test := range map[string]struct {
		decide func(*testing.T, *runstate.Store, runstate.State)
		held   bool
	}{
		"nothing decided": {decide: func(*testing.T, *runstate.Store, runstate.State) {}, held: true},
		"a re-scope decided": {decide: func(t *testing.T, store *runstate.Store, state runstate.State) {
			if _, err := store.Triage().RecordDecision(ctx, state.WorkItemID, triageDecided(runstate.TriageDecisionRescope, state.RunID), docketedNow); err != nil {
				t.Fatalf("RecordDecision() error = %v", err)
			}
		}},
		"a re-run refused for good": {decide: func(t *testing.T, store *runstate.Store, state runstate.State) {
			recordRerunDecision(t, store, state.WorkItemID, state.RunID)
			if _, err := store.Triage().RecordCarryOutRefusal(ctx, state.WorkItemID, runstate.TriageCarryOut{
				RunID: state.RunID, Decision: runstate.TriageDecisionRerun, DecidedAt: docketedNow,
				Cause: triage.CarryOutBranchGone, Gate: runstate.TriageGatePreservedWork,
				Refusal: "nothing of the run is left", Clears: "the development manager decides again",
			}, docketedNow.Add(time.Minute)); err != nil {
				t.Fatalf("RecordCarryOutRefusal() error = %v", err)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fixture := newFlaggedFixture(t)
			state := fixture.flaggedRun(t, "yoyodyne-flagged.5", 5, runstate.StatusCancelled)
			runPipelineGit(t, fixture.repository, "branch", "-D", state.Branch)
			test.decide(t, fixture.store, state)

			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: state.WorkItemID, Title: "Cancelled", Status: "in_progress"}}
			auditor := ClaimAuditor{
				Tracker: tracker, Runs: fixture.store, Releases: fixture.releases, ProductID: "yoyodyne",
				Remains: fixture.worktrees, Clock: fixedClock{at: fixture.now},
			}
			sweep, err := auditor.Audit(ctx, []beads.WorkItem{tracker.Item})
			if err != nil || len(sweep.Released) != 1 {
				t.Fatalf("Audit() = %#v, %v; want the claim given back", sweep, err)
			}
			if !strings.Contains(tracker.ReleaseReason, "nothing pulls the item until she has") {
				t.Fatalf("release note = %q, want it to say whose decision the item waits on", tracker.ReleaseReason)
			}
			holds, err := readmodel.HeldForAPerson(ctx, fixture.store, fixture.store.Triage(), fixture.worktrees, nil)
			if err != nil {
				t.Fatalf("HeldForAPerson() error = %v", err)
			}
			if reason, held := holds.Reason(state.WorkItemID); held != test.held {
				t.Fatalf("held = %t (%q), want %t", held, reason, test.held)
			}
		})
	}
}

// With its branch still there, a cancelled run keeps its claim and its hold.
func TestACancelledRunHoldingItsChangeKeepsItsClaim(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fixture := newFlaggedFixture(t)
	state := fixture.flaggedRun(t, "yoyodyne-flagged.6", 6, runstate.StatusCancelled)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: state.WorkItemID, Title: "Cancelled", Status: "in_progress"}}
	sweep, err := ClaimAuditor{
		Tracker: tracker, Runs: fixture.store, Releases: fixture.releases, ProductID: "yoyodyne",
		Remains: fixture.worktrees, Clock: fixedClock{at: fixture.now},
	}.Audit(ctx, []beads.WorkItem{tracker.Item})
	if err != nil || len(sweep.Released) != 0 {
		t.Fatalf("Audit() = %#v, %v; want the claim kept", sweep, err)
	}
	holds, err := readmodel.HeldForAPerson(ctx, fixture.store, fixture.store.Triage(), fixture.worktrees, nil)
	if err != nil {
		t.Fatalf("HeldForAPerson() error = %v", err)
	}
	if _, held := holds.Reason(state.WorkItemID); !held {
		t.Fatal("the item is not held, want the cancelled run's change held for the development manager")
	}
}
