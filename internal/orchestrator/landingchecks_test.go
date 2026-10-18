package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A target branch left red is one item, not one per landing. A second landing
// whose landing check fails the same way finds the item the first filed still
// open, notes the later commit on it, and files nothing — so the front of the
// queue holds one red-landing item the scheduler starts once, rather than one
// per landing started concurrently.
func TestASecondRedLandingOfTheSameCheckIsNotedOnTheOpenItemRatherThanFiledAgain(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	pipeline.Landings = pipeline.Worktrees.(*gitworktree.Manager)
	filer := &orchestratortest.RecordingFiler{Open: []beads.WorkItem{{
		ID: "yoyodyne-red-earlier", Status: "open",
		Notes: "Filed by the harness for the red landing of yoyodyne-other.\n" + redLandingMarker("main", "false"),
	}}}
	pipeline.Filer = filer
	pipeline.Config.LandingChecks = []string{"false"}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() = %#v, %v, want a succeeded run", outcome, err)
	}
	landed := outcome.LandingChecks
	if landed == nil || !landed.Red() || landed.FiledWorkItem != "yoyodyne-red-earlier" || !landed.FiledEarlier || landed.FilingProblem != "" {
		t.Fatalf("landing = %#v, want the earlier item named and nothing filed", landed)
	}
	if len(filer.Filed) != 0 {
		t.Fatalf("filed = %#v, want nothing filed beside the open item", filer.Filed)
	}
	notes := strings.Join(tracker.NoteRecords, "\n")
	if !strings.Contains(notes, "Red again at "+outcome.Integration.TargetCommit[:12]+" on main, after yoyodyne-task ("+outcome.RunID+") integrated: false exited 1.") {
		t.Fatalf("the open item was not told about the later landing:\n%s", notes)
	}
	if !strings.Contains(notes, "red again on yoyodyne-red-earlier, filed by an earlier landing") {
		t.Fatalf("the landed item's note does not name the earlier item:\n%s", notes)
	}
}
