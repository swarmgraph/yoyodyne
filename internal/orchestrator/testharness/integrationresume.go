package testharness

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// DocketConfig is the configuration the harness's docket is built against here:
// the triage thresholds every other docket in these tests uses.
func DocketConfig() config.Config {
	return config.Config{Execution: config.Execution{IntegrationRetriesBeforeReconciliation: 1}, Triage: DocketedTriage}
}

// ResumeHarness is the durable state a resumption acts on, held together so a
// test can drive one without rebuilding four stores.
type ResumeHarness struct {
	// Docket is the real store rather than the memory one, because the closure a
	// resumption writes carries a decision word no role's vocabulary has, and
	// what accepts it is the store's own validation.
	Docket    *runstate.DocketStore
	Runs      *runstate.Store
	Intake    *runstate.IntakeHoldStore
	Tracker   *orchestratortest.Tracker
	Ownership *orchestratortest.ResumeOwnership
	Started   []ContinuedRun
	Outcome   orchestrator.Outcome
	Failure   error
	Capacity  int
	// Recorded is the run as it was created, which is what a refusal has to
	// leave.
	Recorded runstate.State
	// RunsRoot is the state root the stores are opened over, so a test can open
	// them again as a session started afterwards would.
	RunsRoot string
}

func (h *ResumeHarness) Resumer() orchestrator.IntegrationResumer {
	return orchestrator.IntegrationResumer{
		Docket:    h.Docket,
		Runs:      h.Runs,
		Intake:    h.Intake,
		Items:     h.Tracker,
		Worktrees: h.Ownership,
		Capacity:  h.Capacity,
		Clock:     DocketClock{},
		Start: func(_ context.Context, workItemID, runID string) (orchestrator.Outcome, error) {
			h.Started = append(h.Started, ContinuedRun{WorkItemID: workItemID, RunID: runID})
			return h.Outcome, h.Failure
		},
	}
}

// ApprovedStoppedState is the run this action is about: one whose change the
// reviewer approved, whose promotion the environment then refused, with the
// branch, the worktree, and the session it stopped in all preserved. It is the
// shape yoyodyne-ifd.309's rerun stopped in on 2026-09-18, twice.
func ApprovedStoppedState() runstate.State {
	completed := DocketedNow.Add(-time.Hour)
	return runstate.State{
		SchemaVersion:     runstate.StateSchemaVersion,
		RunID:             DocketedRunID,
		ProductID:         "yoyodyne",
		RepositoryID:      "yoyodyne",
		WorkItemID:        DocketedItem,
		WorkItemTitle:     DocketedTitle,
		Backend:           "claude-code",
		Status:            runstate.StatusFailed,
		Phase:             runstate.PhaseIntegrating,
		StartedAt:         completed.Add(-time.Hour),
		UpdatedAt:         completed,
		CompletedAt:       &completed,
		WorktreePath:      "/state/worktrees/task",
		Branch:            "yoyodyne/task/abc",
		BaseCommit:        strings.Repeat("a", 40),
		HarnessCommit:     strings.Repeat("c", 40),
		TargetBranch:      "main",
		ProviderSessionID: "developer-session",
		ProviderModel:     "opus",
		ReviewSessionID:   "reviewer-session",
		ReviewModel:       "opus",
		ReviewDecision:    runstate.ReviewApprove,
		ReviewApproves:    "implementation",
		ReviewSummary:     "the change matches the acceptance criteria",
		ReviewRounds:      1,
		RepairAttempts:    1,
		Failure:           "integrate approved change: primary checkout is not ready for integration: the primary checkout is not as the harness left it: primary repository has uncommitted changes: AGENTS.md",
		Environmental:     &runstate.EnvironmentalRefusal{Cause: runstate.CauseDirtyPrimary, RecordedAt: completed, Settled: true},
		IntegrationStop: &runstate.IntegrationStop{
			Cause:      runstate.CauseDirtyPrimary,
			Detail:     "integrate approved change: primary checkout is not ready for integration",
			Phase:      runstate.PhaseIntegrating,
			RecordedAt: completed,
		},
	}
}

func NewResumeHarness(t *testing.T, state runstate.State) *ResumeHarness {
	t.Helper()
	root := t.TempDir()
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	docket, err := runstate.NewDocketStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewDocketStore() error = %v", err)
	}
	// The docket and the resumption share the one fixed clock, as the two share
	// the wall clock in the harness: the store refuses a closure made before the
	// stoppage it settles was docketed.
	docketer := DocketerOverStore(docket, runs, DocketConfig())
	docketer.Clock = DocketClock{}
	if _, err := docketer.RecordStoppedRun(state); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	return &ResumeHarness{
		Docket:  docket,
		Runs:    runs,
		Intake:  intake,
		Tracker: &orchestratortest.Tracker{Item: beads.WorkItem{ID: state.WorkItemID, Title: state.WorkItemTitle, Status: "in_progress"}},
		// The checkout is clean again, which is the ordinary case: the operator
		// committed the edit that stopped the run.
		Ownership: &orchestratortest.ResumeOwnership{},
		Capacity:  2,
		Outcome:   orchestrator.Outcome{RunID: state.RunID, WorkItemID: state.WorkItemID, Status: runstate.StatusSucceeded},
		Recorded:  state,
		RunsRoot:  root,
	}
}

func (h *ResumeHarness) Reload(t *testing.T) runstate.State {
	t.Helper()
	state, err := h.Runs.Load(DocketedRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return state
}

func ResumeRequest() orchestrator.IntegrationResumeRequest {
	return orchestrator.IntegrationResumeRequest{Run: DocketedRunID}
}

// Closure is the decision the docket now carries about the stopped run, and
// whether it carries one.
func (h *ResumeHarness) Closure(t *testing.T) (triage.Closure, bool) {
	t.Helper()
	entries, err := h.Docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, entry := range entries {
		if entry.Key == triage.Key(triage.ClassStoppedRun, DocketedRunID) && entry.Closed != nil {
			return *entry.Closed, true
		}
	}
	return triage.Closure{}, false
}

// AssertNothingWritten is what every refusal has to leave: the run exactly as it
// stopped, the item untouched, and nothing dispatched.
func (h *ResumeHarness) AssertNothingWritten(t *testing.T) {
	t.Helper()
	state := h.Reload(t)
	if state.Status != runstate.StatusFailed || len(state.IntegrationResumptions) != len(h.Recorded.IntegrationResumptions) || (state.IntegrationStop == nil) != (h.Recorded.IntegrationStop == nil) {
		t.Fatalf("a refused resumption changed the run: %s/%s, resumptions %#v, stop %#v", state.Status, state.Phase, state.IntegrationResumptions, state.IntegrationStop)
	}
	if len(h.Tracker.Calls) != 0 {
		t.Fatalf("a refused resumption wrote to the item: %v", h.Tracker.Calls)
	}
	if len(h.Started) != 0 {
		t.Fatalf("a refused resumption dispatched something: %#v", h.Started)
	}
	if _, closed := h.Closure(t); closed {
		t.Fatal("a refused resumption closed the docket entry")
	}
	if len(h.Ownership.Restored) != 0 {
		t.Fatalf("a refused resumption restored a worktree: %#v", h.Ownership.Restored)
	}
}
