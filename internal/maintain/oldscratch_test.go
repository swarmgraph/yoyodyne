package maintain

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A pass clears the scratch folders runs were given under the repository's
// .git directory before scratch moved to the state directory. An ended run's
// folder goes, and a link in it is removed as a link with what it points at
// left alone; a folder whose run still holds a developer slot stays; and the
// step says both. Once the live run ends, the next pass takes its folder, and
// the pass after that finds nothing.
func TestAPassClearsOldScratchFoldersOfEndedRunsAndLeavesLiveOnes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	runs, err := runstate.NewStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	f.pass.Runs = runs

	repository := t.TempDir()
	scratch := filepath.Join(repository, ".git", "worktrees", "one", "yoyodyne", "scratch")
	completed := f.now.Add(-time.Hour)
	record := func(item string, status runstate.Status) runstate.State {
		t.Helper()
		id, err := runstate.NewRunID()
		if err != nil {
			t.Fatal(err)
		}
		state := runstate.State{
			SchemaVersion: runstate.StateSchemaVersion, RunID: id, ProductID: "calc", RepositoryID: "calc",
			WorkItemID: item, Backend: "claude-code", Status: status, StartedAt: completed, UpdatedAt: completed,
		}
		if status.Terminal() {
			state.CompletedAt = &completed
		}
		if err := runs.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if err := os.MkdirAll(filepath.Join(scratch, id), 0o700); err != nil {
			t.Fatal(err)
		}
		return state
	}
	ended := record("calc-1", runstate.StatusFailed)
	live := record("calc-2", runstate.StatusRunning)
	if !live.HoldsDeveloperSlot() {
		t.Fatal("the live run's record does not hold a developer slot, so this test cannot tell what it is about")
	}
	f.pass.Repository = repository

	target := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(target, []byte("a program outside the repository\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	endedFolder, liveFolder := filepath.Join(scratch, ended.RunID), filepath.Join(scratch, live.RunID)
	for _, folder := range []string{endedFolder, liveFolder} {
		if err := os.Symlink(target, filepath.Join(folder, "apply_patch")); err != nil {
			t.Skipf("this filesystem does not support symlinks: %v", err)
		}
	}

	step := stepNamed(t, f.pass1(t), StepOldScratch)
	if step.Outcome != runstate.StepRan {
		t.Fatalf("scratch step = %+v, want it run", step)
	}
	if !strings.Contains(step.Detail, "removed 1 scratch folder(s)") || !strings.Contains(step.Detail, ended.RunID) {
		t.Errorf("the step does not say it removed the ended run's folder: %s", step.Detail)
	}
	if !strings.Contains(step.Detail, "left 1") || !strings.Contains(step.Detail, "run "+live.RunID+" still holds a developer slot") {
		t.Errorf("the step does not say it left the live run's folder and why: %s", step.Detail)
	}
	if _, err := os.Lstat(endedFolder); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the ended run's folder is still there (err = %v)", err)
	}
	if _, err := os.Lstat(filepath.Join(liveFolder, "apply_patch")); err != nil {
		t.Errorf("the live run's folder was touched: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("removing the ended run's folder removed what its link pointed at: %v", err)
	}

	// The live run ends.
	live.Status, live.CompletedAt = runstate.StatusSucceeded, &completed
	if err := runs.Save(live); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	f.now = f.now.Add(f.pass.Every)
	step = stepNamed(t, f.pass1(t), StepOldScratch)
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, live.RunID) || strings.Contains(step.Detail, "; left") {
		t.Fatalf("scratch step once the live run ended = %+v, want its folder removed and nothing left", step)
	}
	if _, err := os.Lstat(scratch); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the emptied scratch folder is still there (err = %v)", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("removing the second folder removed what its link pointed at: %v", err)
	}

	f.now = f.now.Add(f.pass.Every)
	step = stepNamed(t, f.pass1(t), StepOldScratch)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "no scratch folder is left") {
		t.Fatalf("scratch step with nothing left = %+v, want it skipped saying so", step)
	}
}

// A folder whose run has no record is left, because nothing shows the run
// ended; and a pass with no run store wired removes nothing and says why.
func TestOldScratchLeavesAFolderNoRecordShowsEnded(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	repository := t.TempDir()
	unknown, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(repository, ".git", "yoyodyne", "scratch", unknown)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	f.pass.Repository = repository

	step := stepNamed(t, f.pass1(t), StepOldScratch)
	if step.Outcome != runstate.StepSkipped || !strings.Contains(step.Detail, "no run store is wired") {
		t.Fatalf("scratch step with no run store = %+v, want it skipped saying why", step)
	}

	runs, err := runstate.NewStore(t.TempDir(), "calc")
	if err != nil {
		t.Fatal(err)
	}
	f.pass.Runs = runs
	f.now = f.now.Add(f.pass.Every)
	step = stepNamed(t, f.pass1(t), StepOldScratch)
	if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "no record of run "+unknown+" is kept") {
		t.Fatalf("scratch step for a run with no record = %+v, want the folder left saying why", step)
	}
	if _, err := os.Stat(folder); err != nil {
		t.Fatalf("a folder no record shows ended was removed: %v", err)
	}
}
