package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// dependencyPhaseFixture is a run the pipeline paused on a dependency linked
// while its developer was working, with everything a restart needs to build a
// second pipeline over the same repository, worktrees, and run store. The
// configured check appends to checkLog, so a test can tell whether any check ran
// and, from inside the developer's invocation, whether one ran before it.
type dependencyPhaseFixture struct {
	repository, worktreeRoot string
	store                    *runstate.Store
	tracker                  *orchestratortest.Tracker
	checkLog                 string
	paused                   Outcome
}

func newDependencyPhaseFixture(t *testing.T) dependencyPhaseFixture {
	t.Helper()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	fixture := dependencyPhaseFixture{
		repository: repository, worktreeRoot: worktreeRoot, store: store, tracker: tracker,
		checkLog: filepath.Join(t.TempDir(), "checks.log"),
	}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		tracker.Item.Dependencies = blockedBy("yoyodyne-blocker")
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	paused, err := fixture.pipeline(t, provider).Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !paused.Paused || paused.PausedByDependency == nil {
		t.Fatalf("outcome = %#v, want a run paused for what its item waits on", paused)
	}
	if fixture.checksRan() {
		t.Fatal("a check ran before the dependency gate paused the run")
	}
	fixture.paused = paused
	return fixture
}

// pipeline builds a pipeline over the fixture's repository and run store. Each
// call is a harness process of its own: nothing passes between two of them but
// what the store and the repository hold, which is what a restart leaves.
func (f dependencyPhaseFixture) pipeline(t *testing.T, provider *orchestratortest.Backend) Pipeline {
	t.Helper()
	return automatic(newSharedPipeline(t, f.repository, f.worktreeRoot, f.store, f.tracker, provider,
		[]string{"echo ran >> '" + f.checkLog + "'"}), provider)
}

func (f dependencyPhaseFixture) checksRan() bool {
	_, err := os.Stat(f.checkLog)
	return err == nil
}

func (f dependencyPhaseFixture) load(t *testing.T) runstate.State {
	t.Helper()
	state, err := f.store.Load(f.paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return state
}

// recordWaitAt puts the paused run back at phase with no wait recorded, as a run
// stopped there would be, and then records the dependency wait on it the way the
// pipeline does. It returns the record carrying the wait, and asserts
// that recording changed nothing on it but the wait itself and when it was
// written: the phase is the one it had, and no counter moved.
func (f dependencyPhaseFixture) recordWaitAt(t *testing.T, phase runstate.Phase) runstate.State {
	t.Helper()
	state := f.load(t)
	state.DependencyPause = nil
	state.Phase = phase
	if err := f.store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	before := f.load(t)

	provider := orchestratortest.RoleBackend(nil, approveVerdict)
	run := &activeRun{pipeline: f.pipeline(t, provider), state: before}
	err := run.recordDependencyPause([]string{"yoyodyne-blocker"})
	var paused dependencyPause
	if !errors.As(err, &paused) {
		t.Fatalf("recordDependencyPause() error = %v, want the pause reported", err)
	}

	after := f.load(t)
	if after.Phase != phase {
		t.Fatalf("saved phase = %q after recording the wait, want %q as it was before", after.Phase, phase)
	}
	if after.DependencyPause == nil || after.DependencyPause.Summary() != "yoyodyne-blocker" {
		t.Fatalf("the wait was not made durable: %#v", after.DependencyPause)
	}
	after.DependencyPause = nil
	after.UpdatedAt = before.UpdatedAt
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("recording the wait changed more than the wait:\nbefore %#v\nafter  %#v", before, after)
	}
	return f.load(t)
}

// release closes what the run waited on, so the next pull continues it.
func (f dependencyPhaseFixture) release() {
	f.tracker.Item.Dependencies = []beads.Dependency{{ID: "yoyodyne-blocker", Type: blocksDependency, Status: "closed"}}
}

// A wait recorded on a run whose developer had not finished keeps the run at
// developing, and releasing it — in a harness process started after the one
// that recorded it — hands the same developer back the same session in the same
// worktree on the same branch. No check runs until that attempt has returned,
// and neither recording nor releasing spends a repair attempt, a re-run, or a
// review round.
func TestADependencyWaitRecordedAtDevelopingResumesTheSameDeveloperBeforeAnyCheck(t *testing.T) {
	t.Parallel()

	fixture := newDependencyPhaseFixture(t)
	before := fixture.recordWaitAt(t, runstate.PhaseDeveloping)
	if before.ProviderSessionID == "" || before.WorktreePath == "" || before.Branch == "" {
		t.Fatalf("the paused run recorded no session, worktree, or branch to hand back: %#v", before)
	}

	// The harness restarts here: everything below is a fresh pipeline reading the
	// run back from the store.
	fixture.release()
	var (
		resumedSession, resumedDirectory string
		checksRanFirst                   bool
		atResume                         runstate.State
	)
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		resumedSession, resumedDirectory = request.SessionID, request.WorkingDirectory
		checksRanFirst = fixture.checksRan()
		atResume = fixture.load(t)
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("finished\n"), 0o600)
	}, approveVerdict)
	resumed, err := fixture.pipeline(t, provider).Run(context.Background(), fixture.tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if resumed.RunID != fixture.paused.RunID {
		t.Fatalf("resumed run = %q, want the waiting run %q continued rather than re-run", resumed.RunID, fixture.paused.RunID)
	}
	if developed := len(provider.RequestsForRole(domain.RoleDeveloper)); developed != 1 {
		t.Fatalf("developer invocations after release = %d, want the interrupted attempt handed back once", developed)
	}
	if resumedSession != before.ProviderSessionID || resumedDirectory != before.WorktreePath {
		t.Fatalf("developer resumed in session %q at %q, want session %q at %q",
			resumedSession, resumedDirectory, before.ProviderSessionID, before.WorktreePath)
	}
	if checksRanFirst {
		t.Fatal("a check ran before the resumed developer had finished")
	}
	if atResume.Phase != runstate.PhaseDeveloping || atResume.DependencyPause != nil || atResume.Branch != before.Branch {
		t.Fatalf("at resume the run is at %q on %q with wait %#v, want developing on %q with the wait released",
			atResume.Phase, atResume.Branch, atResume.DependencyPause, before.Branch)
	}
	if atResume.RepairAttempts != before.RepairAttempts || atResume.ReviewRounds != before.ReviewRounds || atResume.TransientRelaunches != before.TransientRelaunches {
		t.Fatalf("releasing the wait moved a counter: repairs %d→%d, reviews %d→%d, relaunches %d→%d",
			before.RepairAttempts, atResume.RepairAttempts, before.ReviewRounds, atResume.ReviewRounds, before.TransientRelaunches, atResume.TransientRelaunches)
	}
	if !fixture.checksRan() || resumed.Integration == nil || !fixture.tracker.Closed {
		t.Fatalf("the resumed run did not go on through its checks to integration: %#v (closed=%t)", resumed, fixture.tracker.Closed)
	}
}

// A wait recorded on a run at its checks keeps it there, and releasing it after
// a restart runs the checks on the change the run already has: no developer is
// invoked, and no counter moves until the review the resumed run earns.
func TestADependencyWaitRecordedAtCheckingResumesAtTheChecks(t *testing.T) {
	t.Parallel()

	fixture := newDependencyPhaseFixture(t)
	before := fixture.recordWaitAt(t, runstate.PhaseChecking)
	fixture.assertResumesAtTheChecks(t, before)
}

// A record saved before the wait kept its phase: the run was at developing when
// the wait was recorded, and the old code advanced the saved phase to checking
// as it wrote the wait. Nothing on that record says whether the developer's
// attempt had returned — the old code recorded a wait only once one had, which
// is what the advance asserted — so the record is read as it says and resumes
// at the checks, on the change the attempt left, with no developer invoked.
func TestARecordSavedWhenTheWaitAdvancedTheDevelopingPhaseResumesAtTheChecks(t *testing.T) {
	t.Parallel()

	fixture := newDependencyPhaseFixture(t)
	saved := fixture.load(t)
	saved.Phase = runstate.PhaseChecking
	saved.DependencyPause = &runstate.DependencyPause{Blockers: []string{"yoyodyne-blocker"}}
	if err := fixture.store.Save(saved); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	fixture.assertResumesAtTheChecks(t, fixture.load(t))
}

// assertResumesAtTheChecks releases the wait in a fresh pipeline and asserts the
// run continued at its checks: the same run, no developer invoked, the checks
// run, one review earned, and no repair attempt or relaunch spent.
func (f dependencyPhaseFixture) assertResumesAtTheChecks(t *testing.T, before runstate.State) {
	t.Helper()
	if before.Phase != runstate.PhaseChecking || before.DependencyPause == nil {
		t.Fatalf("waiting record = phase %q, wait %#v; want checking with a wait", before.Phase, before.DependencyPause)
	}
	f.release()
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if request.Role == domain.RoleDeveloper {
			t.Error("a developer was invoked for a run resumed at its checks")
		}
		return nil
	}, approveVerdict)
	resumed, err := f.pipeline(t, provider).Run(context.Background(), f.tracker.Item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if resumed.RunID != f.paused.RunID {
		t.Fatalf("resumed run = %q, want the waiting run %q continued rather than re-run", resumed.RunID, f.paused.RunID)
	}
	if developed := len(provider.RequestsForRole(domain.RoleDeveloper)); developed != 0 {
		t.Fatalf("developer invocations after release = %d, want none", developed)
	}
	if !f.checksRan() || resumed.Integration == nil || !f.tracker.Closed {
		t.Fatalf("the resumed run did not run its checks and finish: %#v (closed=%t)", resumed, f.tracker.Closed)
	}
	finished := f.load(t)
	if finished.RepairAttempts != before.RepairAttempts || finished.TransientRelaunches != before.TransientRelaunches || finished.ReviewRounds != before.ReviewRounds+1 {
		t.Fatalf("counters moved beyond the one review earned: repairs %d→%d, relaunches %d→%d, reviews %d→%d",
			before.RepairAttempts, finished.RepairAttempts, before.TransientRelaunches, finished.TransientRelaunches, before.ReviewRounds, finished.ReviewRounds)
	}
}
