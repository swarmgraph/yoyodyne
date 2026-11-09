package orchestrator

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
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func teamMergeProvider() backend.Backend {
	return orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
}

// With no team branch configured, a change that lands closes its item as ever.
func TestTeamMergeUnsetClosesAnIntegratedItem(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := teamMergeProvider()
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !outcome.WorkItemClosed || !tracker.Closed || outcome.TeamMerge != nil {
		t.Fatalf("outcome = %#v, tracker closed = %v; want the item closed", outcome, tracker.Closed)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.UpstreamBranch != "" || state.TeamMerge != nil || state.WaitingForTeamMerge() {
		t.Fatalf("state = upstream %q, wait %#v; want neither", state.UpstreamBranch, state.TeamMerge)
	}
}

// With a team branch configured, the change lands on the target and the item
// stays claimed, waiting for the team's merge.
func TestTeamMergeSetLeavesAnIntegratedItemWaiting(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := teamMergeProvider()
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.UpstreamBranch = "release"

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || outcome.WorkItemClosed {
		t.Fatalf("outcome = %#v, want a landed, succeeded run whose item is not closed", outcome)
	}
	if tracker.Closed || slices.Contains(tracker.Calls, "complete") || tracker.Item.Status != "in_progress" {
		t.Fatalf("tracker closed = %v, status = %q, calls = %v; want the item left in_progress", tracker.Closed, tracker.Item.Status, tracker.Calls)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.UpstreamBranch != "release" {
		t.Fatalf("UpstreamBranch = %q, want release", state.UpstreamBranch)
	}
	if state.TeamMerge == nil || state.TeamMerge.LandingCommit != state.Integration.TargetCommit ||
		state.TeamMerge.TargetBranch != "main" || state.TeamMerge.UpstreamBranch != "release" {
		t.Fatalf("TeamMerge = %#v, want a wait on %s for main into release", state.TeamMerge, state.Integration.TargetCommit)
	}
	if state.Status != runstate.StatusSucceeded || !state.WaitingForTeamMerge() {
		t.Fatalf("state status = %s, waiting = %v; want succeeded and waiting", state.Status, state.WaitingForTeamMerge())
	}
	if !strings.Contains(tracker.Notes, "waiting for the team to merge it into release") {
		t.Fatalf("notes = %q, want the wait said in plain words", tracker.Notes)
	}
}

// A sweep that finds a landed run whose record lost the wait puts it back on
// the run and does not close the item; one that finds the wait recorded leaves
// the item alone.
func TestTeamMergeReconcileDoesNotCloseAWaitingItem(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := teamMergeProvider()
	halting := &haltingStore{StateStore: store, at: runstate.PhaseCleaningUp}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	pipeline.Config.Execution.UpstreamBranch = "release"
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Whatever the interrupted run managed to write, the sweep has to cope with
	// a record that does not carry the wait yet.
	state.TeamMerge = nil
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	tracker.Item.Status = "in_progress"
	tracker.Calls = nil

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v", results)
	}
	if tracker.Closed || slices.Contains(tracker.Calls, "complete") {
		t.Fatalf("the sweep closed a waiting item: calls %v", tracker.Calls)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.TeamMerge == nil || !settled.WaitingForTeamMerge() {
		t.Fatalf("settled state = %#v, want the wait recorded", settled.TeamMerge)
	}
	if !strings.Contains(tracker.Notes, "waiting for the team to merge it into release") {
		t.Fatalf("notes = %q", tracker.Notes)
	}

	// A second sweep makes no further call on the tracker.
	tracker.Calls = nil
	reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if slices.Contains(tracker.Calls, "complete") || slices.Contains(tracker.Calls, "release") || slices.Contains(tracker.Calls, "reopen") {
		t.Fatalf("second sweep acted on a waiting item: calls %v", tracker.Calls)
	}
}

func TestTeamMergeItemSettled(t *testing.T) {
	t.Parallel()

	wait := runstate.TeamMerge{Since: time.Unix(1, 0)}
	found := time.Unix(2, 0)
	foundWait := wait
	foundWait.FoundAt = &found
	for _, test := range []struct {
		name   string
		state  runstate.State
		status string
		want   bool
	}{
		{"waiting and claimed", runstate.State{TeamMerge: &wait}, "in_progress", true},
		{"waiting and closed", runstate.State{TeamMerge: &wait}, "closed", true},
		{"found and claimed", runstate.State{TeamMerge: &foundWait}, "in_progress", false},
		{"no wait and claimed", runstate.State{}, "in_progress", false},
	} {
		if got := itemSettled(test.state, test.status); got != test.want {
			t.Errorf("%s: itemSettled = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestRecordTeamMergeWaitWritesTheNoteOnce(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Status: "in_progress"}}
	state := runstate.State{RunID: "run-1", WorkItemID: "yoyodyne-task", TargetBranch: "staging", UpstreamBranch: "main"}
	commit := strings.Repeat("a", 40)
	at := time.Unix(10, 0)
	for range 2 {
		if err := recordTeamMergeWait(context.Background(), tracker, &state, commit, at); err != nil {
			t.Fatalf("recordTeamMergeWait() error = %v", err)
		}
	}
	if len(tracker.NoteRecords) != 1 {
		t.Fatalf("notes written = %d, want 1: %q", len(tracker.NoteRecords), tracker.NoteRecords)
	}
	if !strings.Contains(tracker.Notes, "waiting for the team to merge it into main") || tracker.Closed || tracker.Item.Status != "in_progress" {
		t.Fatalf("notes = %q, closed = %v, status = %q", tracker.Notes, tracker.Closed, tracker.Item.Status)
	}
	if state.TeamMerge == nil || state.TeamMerge.LandingCommit != commit || state.TeamMerge.TargetBranch != "staging" {
		t.Fatalf("TeamMerge = %#v", state.TeamMerge)
	}

	closed := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Status: "closed"}}
	fresh := runstate.State{RunID: "run-1", WorkItemID: "yoyodyne-task", TargetBranch: "staging", UpstreamBranch: "main"}
	if err := recordTeamMergeWait(context.Background(), closed, &fresh, commit, at); err != nil {
		t.Fatalf("recordTeamMergeWait() on a closed item error = %v", err)
	}
	if len(closed.NoteRecords) != 0 || fresh.TeamMerge != nil {
		t.Fatalf("a closed item was written to: notes %q, wait %#v", closed.NoteRecords, fresh.TeamMerge)
	}
}
