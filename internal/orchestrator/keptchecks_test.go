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
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// keptChecksFixture is three checks that each count their runs in a file
// outside the worktree. The second says when it is running and then runs until
// the restart stops it, unless it has been released.
type keptChecksFixture struct {
	dir      string
	commands []string
}

func newKeptChecksFixture(t *testing.T) keptChecksFixture {
	t.Helper()
	dir := t.TempDir()
	at := func(name string) string { return "'" + filepath.Join(dir, name) + "'" }
	return keptChecksFixture{dir: dir, commands: []string{
		"printf x >> " + at("first"),
		"printf x >> " + at("second") + " && touch " + at("running") + " && { [ -f " + at("release") + " ] || sleep 60; }",
		"printf x >> " + at("third"),
	}}
}

func (f keptChecksFixture) runs(t *testing.T, name string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return len(data)
}

// stopAtSecondCheck runs the first pipeline until its second check is running,
// then stops it the way a watch session restarting onto a new build does once
// its wait runs out.
func (f keptChecksFixture) stopAtSecondCheck(t *testing.T, pipeline Pipeline, itemID string) Outcome {
	t.Helper()
	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	go func() {
		for {
			if _, err := os.Stat(filepath.Join(f.dir, "running")); err == nil {
				stop(RedeployDrain{At: time.Date(2026, 10, 11, 12, 46, 0, 0, time.UTC), Bound: 15 * time.Minute, SessionID: "watch-before"})
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	paused, err := pipeline.Run(ctx, itemID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a run stopped for a restart reported as a pause", err)
	}
	if !paused.Paused || paused.RedeployStop == nil || paused.RedeployStop.Phase != runstate.PhaseChecking {
		t.Fatalf("outcome = %#v, want a run paused at its checks", paused)
	}
	// The second check is released, so the picked-up run can finish it.
	if err := os.WriteFile(filepath.Join(f.dir, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return paused
}

// A restart in the middle of a run's checks keeps the check that had already
// passed: the picked-up run runs only the check the restart stopped and the one
// after it, and says so on the item and in its outcome.
func TestARunPickedUpAfterARestartAtItsChecksRunsOnlyTheChecksThatHadNotPassed(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	fixture := newKeptChecksFixture(t)
	develop := func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}
	first := orchestratortest.RoleBackend(develop, approveVerdict)
	paused := fixture.stopAtSecondCheck(t, automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, fixture.commands), first), tracker.Item.ID)

	stopped, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stopped.KeptChecks == nil || len(stopped.KeptChecks.Checks) != 1 || stopped.KeptChecks.Checks[0].Command != fixture.commands[0] {
		t.Fatalf("kept checks at the stop = %#v, want only the first check, which had passed", stopped.KeptChecks)
	}

	second := orchestratortest.RoleBackend(develop, approveVerdict)
	outcome, err := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, fixture.commands), second).Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the picked-up run did not finish the same change: %#v", outcome)
	}
	if developers := second.RequestsForRole(domain.RoleDeveloper); len(developers) != 0 {
		t.Fatalf("developer invocations = %d, want none for a run stopped at its checks", len(developers))
	}
	if got := []int{fixture.runs(t, "first"), fixture.runs(t, "second"), fixture.runs(t, "third")}; !slices.Equal(got, []int{1, 2, 1}) {
		t.Fatalf("runs of each check = %v, want [1 2 1]: the passed check kept, the stopped one and the one after it run", got)
	}
	if outcome.CheckStage == nil || !slices.Equal(outcome.CheckStage.Kept, fixture.commands[:1]) || !slices.Equal(outcome.CheckStage.Ran, fixture.commands[1:]) {
		t.Fatalf("check stage = %#v, want the first check kept and the other two run", outcome.CheckStage)
	}
	if len(outcome.Checks) != 3 || !outcome.Checks[0].Passed || outcome.Checks[0].Command != fixture.commands[0] {
		t.Fatalf("checks = %#v, want all three results, the kept one first", outcome.Checks)
	}
	finished, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.ChecksPassed == nil || !slices.Equal(finished.ChecksPassed.Commands, fixture.commands) {
		t.Fatalf("checks passed = %#v, want every configured check named", finished.ChecksPassed)
	}
	if finished.KeptChecks != nil {
		t.Fatalf("a run whose checks all passed still carries kept results: %#v", finished.KeptChecks)
	}
	for _, want := range []string{
		"Kept their results, because they had already passed on this same change before the restart: " + fixture.commands[0] + ".",
		"Running again, because they had not finished before the restart: " + fixture.commands[1] + ", " + fixture.commands[2] + ".",
		"kept from before the restart onto a new build, not run again",
	} {
		if !strings.Contains(tracker.Notes, want) {
			t.Fatalf("the item's notes do not say %q:\n%s", want, tracker.Notes)
		}
	}
}

// A kept result is for exactly the change it passed on. A picked-up run whose
// change differs from the one the checks passed on runs every check again, and
// says why.
func TestARunPickedUpAfterARestartWithAChangedCandidateRunsEveryCheckAgain(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	fixture := newKeptChecksFixture(t)
	develop := func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}
	first := orchestratortest.RoleBackend(develop, approveVerdict)
	paused := fixture.stopAtSecondCheck(t, automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, fixture.commands), first), tracker.Item.ID)

	// The change moves between the stop and the pick-up, so the result kept for
	// the first check is not a result for the change about to be checked.
	if err := os.WriteFile(filepath.Join(paused.WorktreePath, "feature.txt"), []byte("implemented differently\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	second := orchestratortest.RoleBackend(develop, approveVerdict)
	outcome, err := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, fixture.commands), second).Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.Integration == nil || !tracker.Closed {
		t.Fatalf("the picked-up run did not finish: %#v", outcome)
	}
	if got := []int{fixture.runs(t, "first"), fixture.runs(t, "second"), fixture.runs(t, "third")}; !slices.Equal(got, []int{2, 2, 1}) {
		t.Fatalf("runs of each check = %v, want [2 2 1]: every check run again over the changed candidate", got)
	}
	if outcome.CheckStage == nil || len(outcome.CheckStage.Kept) != 0 || !slices.Equal(outcome.CheckStage.Ran, fixture.commands) {
		t.Fatalf("check stage = %#v, want nothing kept and every check run", outcome.CheckStage)
	}
	if want := "Running every check again, because the change is not the one the checks passed on before the restart, so no result was kept"; !strings.Contains(tracker.Notes, want) {
		t.Fatalf("the item's notes do not say %q:\n%s", want, tracker.Notes)
	}
	if strings.Contains(tracker.Notes, "Kept their results") {
		t.Fatalf("the item's notes claim a result was kept for a changed candidate:\n%s", tracker.Notes)
	}
}

// A kept check's output is its last bytes, cut at a line and never inside a
// character, so what the reviewer is quoted of it is whole text.
func TestKeptTailCutsAtALineAndNeverInsideACharacter(t *testing.T) {
	t.Parallel()

	if got := keptTail("short\n", 64); got != "short\n" {
		t.Fatalf("keptTail() of output inside the bound = %q, want it whole", got)
	}
	if got := keptTail("first line\nsecond line\n", 15); got != "second line\n" {
		t.Fatalf("keptTail() = %q, want the cut moved to the next line", got)
	}
	// "é" is two bytes; a bound landing between them moves forward past it.
	if got := keptTail("aéb", 2); got != "b" {
		t.Fatalf("keptTail() = %q, want the cut moved past the split character", got)
	}
}
