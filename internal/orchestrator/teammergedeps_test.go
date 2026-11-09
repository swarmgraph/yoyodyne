package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeTeamMergeWaits lists fixed waiting runs.
type fakeTeamMergeWaits []runstate.State

func (f fakeTeamMergeWaits) WaitingForTeamMerge() ([]runstate.State, error) { return f, nil }

func waitingRun(itemID, target string) runstate.State {
	return runstate.State{
		RunID: "run-" + itemID, WorkItemID: itemID, Status: runstate.StatusSucceeded,
		Integration: &runstate.Integration{TargetBranch: target},
		TeamMerge: &runstate.TeamMerge{
			TargetBranch: target, UpstreamBranch: "main", LandingCommit: "0123456789abcdef0123456789abcdef01234567",
			Since: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		},
	}
}

func dependingOn(id, status string, on ...beads.Dependency) beads.WorkItem {
	return beads.WorkItem{ID: id, Title: id, Status: status, Priority: 2, Dependencies: on}
}

func blocks(id, status string) beads.Dependency {
	return beads.Dependency{ID: id, Type: "blocks", Status: status}
}

type noGates struct{}

func (noGates) DischargedGates() (map[string][]string, error) { return nil, nil }

func TestTeamMergeWaitingOnKeepsOnlyTheTargetsWaits(t *testing.T) {
	t.Parallel()

	waits := fakeTeamMergeWaits{waitingRun("a", "staging"), waitingRun("b", "other")}
	got, err := TeamMergeWaitingOn(waits, "staging")
	if err != nil || len(got) != 1 || !got["a"] {
		t.Fatalf("TeamMergeWaitingOn(staging) = %v, %v; want only a", got, err)
	}
	if all, _ := TeamMergeWaitingOn(waits, ""); len(all) != 2 {
		t.Fatalf("TeamMergeWaitingOn() = %v, want both with no target named", all)
	}
}

// The queue offers an item whose only unclosed dependency waits for the team's
// merge on the pull's target, and nothing else the tracker holds back.
func TestQueueOffersWhatDependsOnWorkWaitingForTheTeamsMerge(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.ScheduleTracker{
		Items: []beads.WorkItem{
			dependingOn("waiting", "in_progress"),
			dependingOn("waiting-elsewhere", "in_progress"),
			dependingOn("ordinary", "in_progress"),
			dependingOn("on-waiting", "open", blocks("waiting", "in_progress")),
			dependingOn("on-waiting-elsewhere", "open", blocks("waiting-elsewhere", "in_progress")),
			dependingOn("on-ordinary", "open", blocks("ordinary", "in_progress")),
			dependingOn("on-both", "open", blocks("waiting", "in_progress"), blocks("ordinary", "in_progress")),
			dependingOn("on-waiting-and-closed", "open", blocks("waiting", "in_progress"), blocks("done", "closed")),
			dependingOn("not-ready-otherwise", "open"),
			dependingOn("ready", "open"),
		},
		ReadyItems: map[string]bool{"ready": true},
	}
	pull := Pull{
		Tracker: tracker,
		Gates:   noGates{},
		TeamMerges: fakeTeamMergeWaits{
			waitingRun("waiting", "staging"), waitingRun("waiting-elsewhere", "other"),
		},
		TargetBranch: func(context.Context) (string, error) { return "staging", nil },
	}
	pulled, err := pull.queue(context.Background(), nil, true)
	if err != nil {
		t.Fatalf("queue() error = %v", err)
	}
	var offered []string
	for _, entry := range pulled.queue.Entries {
		if entry.Ready {
			offered = append(offered, entry.ID)
		}
	}
	slices.Sort(offered)
	if want := []string{"on-waiting", "on-waiting-and-closed", "ready"}; !slices.Equal(offered, want) {
		t.Fatalf("offered = %v, want %v", offered, want)
	}
}

// A pull wired without the waits offers exactly what the tracker reports ready.
func TestQueueWithoutTeamMergeWaitsOffersOnlyWhatTheTrackerReports(t *testing.T) {
	t.Parallel()

	tracker := &orchestratortest.ScheduleTracker{
		Items:      []beads.WorkItem{dependingOn("on-waiting", "open", blocks("waiting", "in_progress")), dependingOn("ready", "open")},
		ReadyItems: map[string]bool{"ready": true},
	}
	pulled, err := Pull{Tracker: tracker, Gates: noGates{}}.queue(context.Background(), nil, true)
	if err != nil {
		t.Fatalf("queue() error = %v", err)
	}
	if got := pulled.queue.Ready(); got != 1 {
		t.Fatalf("ready = %d, want 1", got)
	}
}

// Run goes ahead on an item whose only unclosed dependency waits for the
// team's merge on its own target, and still pauses on one that does not.
func TestRunAcceptsADependencyWaitingForTheTeamsMerge(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	provider := teamMergeProvider()
	first := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	upstream := newSharedPipeline(t, repository, worktreeRoot, store, first, provider, []string{"exit 0"})
	upstream = automatic(upstream, provider)
	upstream.Config.Execution.UpstreamBranch = "release"
	if outcome, err := upstream.Run(context.Background(), first.Item.ID); err != nil || first.Closed {
		t.Fatalf("first Run() = %#v, %v, closed %v; want the item left waiting", outcome, err, first.Closed)
	}

	dependent := func(on string, runID string) (Outcome, error, *orchestratortest.Tracker) {
		provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
			return os.WriteFile(filepath.Join(request.WorkingDirectory, runID+".txt"), []byte("implemented\n"), 0o600)
		}, approveVerdict)
		tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
			ID: "yoyodyne-dependent", Title: "Dependent", Status: "open",
			Dependencies: []beads.Dependency{blocks(on, "in_progress")},
		}}
		pipeline := newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"})
		pipeline = automatic(pipeline, provider)
		pipeline.Config.Execution.UpstreamBranch = "release"
		pipeline.NewRunID = func() (string, error) { return runID, nil }
		outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
		return outcome, err, tracker
	}

	outcome, err, _ := dependent("yoyodyne-task", "run-00000000000000000000000000000001")
	if err != nil || outcome.Paused || outcome.PausedByDependency != nil || outcome.Integration == nil {
		t.Fatalf("Run() = %#v, %v; want the dependent run to go ahead and land", outcome, err)
	}
	if outcome, err, _ := dependent("yoyodyne-unrelated", "run-00000000000000000000000000000002"); err != nil || !outcome.Paused || outcome.PausedByDependency == nil {
		t.Fatalf("Run() = %#v, %v; want a dependency on ordinary unclosed work to pause the run", outcome, err)
	}
}

// A stopped dependent whose only unclosed dependency waits for the team's merge
// passes the item checks the continuation, resumption and re-run paths make; a
// dependency on ordinary unfinished work still fails them.
func TestAContinuationIsNotRefusedForADependencyWaitingForTheTeamsMerge(t *testing.T) {
	t.Parallel()

	runs := fakeTeamMergeWaitsStore{fakeTeamMergeWaits{waitingRun("waiting", "staging")}}
	item := func(on string) beads.WorkItem {
		return beads.WorkItem{ID: "dependent", Status: "in_progress", Dependencies: []beads.Dependency{blocks(on, "in_progress")}}
	}
	if err := continuableItem(item("waiting"), "dependent"); err == nil {
		t.Fatal("continuableItem() without the filter = nil, want the dependency to refuse")
	}
	for _, target := range []string{"staging", ""} {
		released := releaseTeamMergedFrom(runs, item("waiting"), target)
		if err := continuableItem(released, "dependent"); err != nil {
			t.Fatalf("continuableItem(target %q) = %v, want it accepted", target, err)
		}
		if err := validateClaimedItem(released, "dependent"); err != nil {
			t.Fatalf("validateClaimedItem(target %q) = %v, want it accepted", target, err)
		}
	}
	if err := continuableItem(releaseTeamMergedFrom(runs, item("waiting"), "other"), "dependent"); err == nil {
		t.Fatal("a wait on another target released the dependency")
	}
	if err := continuableItem(releaseTeamMergedFrom(runs, item("ordinary"), "staging"), "dependent"); err == nil {
		t.Fatal("a dependency on ordinary work was released")
	}
	if got := BlockingDependenciesAfterTeamMerges(runs, item("waiting")); len(got) != 0 {
		t.Fatalf("BlockingDependenciesAfterTeamMerges() = %v, want none", got)
	}
	if got := releaseTeamMergedFrom(struct{}{}, item("waiting"), ""); len(got.Dependencies) != 1 {
		t.Fatal("a store that cannot list waits released a dependency")
	}
}

// fakeTeamMergeWaitsStore is a value of an unrelated type that happens to list
// waits, as a run store reached through another interface does.
type fakeTeamMergeWaitsStore struct{ fakeTeamMergeWaits }
