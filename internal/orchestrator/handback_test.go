package orchestrator

// The founding case, replayed against a real repository.
//
// A run stopped with its repair budget spent and a ten-file change preserved in
// its worktree. The development manager granted a repair, and what the repair
// round was given was a fresh clean worktree — so the developer either delivered
// an empty change or reconstructed all ten files by reading the preserved
// worktree by hand, and nothing in the run's record afterwards said which. These
// two tests are the two halves of that: a handback carries the preserved change
// it names, and one that arrives without it refuses instead of proceeding.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// handbackFiles is the shape of the change the founding case lost: ten files
// across three directories, which is what a developer had to reconstruct by
// hand. Nested paths are part of the point — a worktree seeded from the target
// branch has none of the directories either.
var handbackFiles = []string{
	"internal/handback/carry.go",
	"internal/handback/carry_test.go",
	"internal/handback/seed.go",
	"internal/handback/seed_test.go",
	"internal/handback/refuse.go",
	"internal/handback/refuse_test.go",
	"internal/handback/doc.go",
	"docs/handback.md",
	"docs/handback-refusal.md",
	"README-handback.md",
}

// handbackCaps leaves room in the round budget so what these tests measure is
// the handback rather than a grant the cap cut.
var handbackCaps = runstate.TriageCaps{ReviewRounds: 50, RepairGrants: 1, Reruns: 1, MergeRearms: 2}

// What the item is for: the repair round is handed the worktree the stopped run
// preserved, with every file of the change still in it, rather than a clean one
// to reinvent the change in.
func TestARepairHandbackCarriesThePreservedChange(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	docket := &memoryDocket{}

	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, docket)

	// The development manager's decision, which spends the item's repair budget
	// before anything acts on it.
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, triageDecided(runstate.TriageDecisionRepair, stopped.RunID), 2, docketedNow, handbackCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}

	// What the continued developer was actually given, read at the moment it was
	// given: a run that integrates afterwards removes the worktree, so this is
	// the only moment the question can be asked.
	var handedTo backend.RunRequest
	var handedFiles []string
	second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		handedTo = request
		handedFiles = presentHandbackFiles(request.WorkingDirectory)
		return nil
	}, approveVerdict)
	continuing := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)

	result, err := repairContinuerOver(t, continuing, store, docket, tracker).
		Continue(context.Background(), RepairContinueRequest{Run: stopped.RunID})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued {
		t.Fatalf("result = %#v, want the handback carried out", result)
	}
	if !result.Outcome.continuationAccepted {
		t.Fatalf("outcome = %+v; want acceptance confirmed by the adopted pipeline", result.Outcome)
	}
	served, err := store.Load(stopped.RunID)
	if err != nil || len(served.RepairContinuations) == 0 || served.RepairContinuations[len(served.RepairContinuations)-1].DispatchPending {
		t.Fatalf("run = %+v, %v; want the real accepted handback acknowledged", served, err)
	}
	// Nothing started over: the run the docket entry names is the run that went
	// on, in the worktree it stopped in.
	if result.Outcome.RunID != stopped.RunID || result.Outcome.WorktreePath != stopped.WorktreePath {
		t.Fatalf("continued run = %s in %s, want the stopped run %s in %s",
			result.Outcome.RunID, result.Outcome.WorktreePath, stopped.RunID, stopped.WorktreePath)
	}
	// The handback carried the preserved worktree it names rather than a fresh one.
	if handedTo.WorkingDirectory != stopped.WorktreePath {
		t.Fatalf("the repair was handed %q, want the preserved worktree %q", handedTo.WorkingDirectory, stopped.WorktreePath)
	}
	// And the change was in it: all ten files, as the stopped run left them.
	if len(handedFiles) != len(handbackFiles) {
		t.Fatalf("the repair was handed %d of the change's %d file(s): %v", len(handedFiles), len(handbackFiles), handedFiles)
	}
	// It is the same developer carrying on rather than a new one, which is the
	// other half of continuing a change instead of re-deriving it.
	if handedTo.SessionID != second.DeveloperSession {
		t.Fatalf("the repair ran in session %q, want the recorded developer session %q", handedTo.SessionID, second.DeveloperSession)
	}
	// And it was handed the findings it is a repair of, not a fresh work item.
	if !strings.Contains(handedTo.Prompt, "add the missing file") {
		t.Fatalf("the repair prompt is not the reviewer's findings:\n%s", handedTo.Prompt)
	}
}

// The other half: a handback that arrives on a worktree holding none of the
// change refuses rather than putting a developer to work in it. This drives the
// pipeline's own gate rather than the triage action's, because that is the one
// every route into a repair loop passes — the triage action, an interrupted
// process picked up later, and whatever re-entry is built next.
func TestAResumedRepairRefusesAWorktreeThatLostThePreservedChange(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})

	// The failure this item was filed for, in the state it leaves on disk: the
	// worktree is registered, its HEAD is exactly where the harness left it, and
	// it holds none of the change the findings are about.
	emptyPreservedWorktree(t, stopped.WorktreePath)
	// The run made live again and the item put back, which is what a handback
	// records before the pipeline is asked to continue it.
	reEnterAt(t, store, tracker, stopped.RunID, runstate.PhaseDeveloping)

	second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked for a repair of a change that is not in %s", request.WorkingDirectory)
		return nil
	}, approveVerdict)
	continuing := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)

	if _, err := continuing.Run(context.Background(), tracker.Item.ID); !errors.Is(err, ErrPreservedChangeMissing) {
		t.Fatalf("Run() error = %v, want the handback refused for holding none of its change", err)
	}
	if invocations := len(second.RequestsForRole(domain.RoleDeveloper)); invocations != 0 {
		t.Fatalf("developer invocations = %d, want the refusal to have spent nothing on a provider", invocations)
	}
	// It refuses loudly: the run ends carrying a blocker that says what happened,
	// and the item is handed to a person rather than left looking like work that
	// went quiet.
	refused, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !refused.Status.Terminal() || refused.Blocker == "" {
		t.Fatalf("refused run = %#v, want it ended on a durable blocker", refused)
	}
	if !tracker.Blocked || !strings.Contains(tracker.BlockReason, "holds none of the change") {
		t.Fatalf("item blocked = %t, reason = %q", tracker.Blocked, tracker.BlockReason)
	}
	// The reason says the change may still be on the branch, because a reader who
	// concluded the work was gone would replan work that still exists.
	if !strings.Contains(tracker.BlockReason, "nothing was deleted") {
		t.Fatalf("blocker does not say where the preserved work is: %q", tracker.BlockReason)
	}
}

// The same loss caught one step further on. A run re-entered at the review has
// completed a developer attempt, so what the reviewer is about to judge is that
// attempt's change — and an empty worktree there buys a review round spent on an
// empty diff, which is one of the field instances this item was filed for. The
// gate is not conditioned on the repair input for exactly this reason.
func TestAResumedReviewRefusesAWorktreeThatLostThePreservedChange(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	emptyPreservedWorktree(t, stopped.WorktreePath)
	reEnterAt(t, store, tracker, stopped.RunID, runstate.PhaseReviewing)

	second := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked from %s, which a review-phase resume never does", request.WorkingDirectory)
		return nil
	}, approveVerdict)
	continuing := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)

	if _, err := continuing.Run(context.Background(), tracker.Item.ID); !errors.Is(err, ErrPreservedChangeMissing) {
		t.Fatalf("Run() error = %v, want the re-entry refused for holding none of its change", err)
	}
	if invocations := len(second.Requests); invocations != 0 {
		t.Fatalf("provider invocations = %d, want the reviewer never asked to judge an empty diff", invocations)
	}
}

// The substitution the field instances describe: no handback at all, but a fresh
// run dispatched into a clean worktree off the target branch for an item whose
// last run stopped owing a repair. The fresh worktree is perfectly valid, so
// nothing downstream notices — this refuses before anything is reserved.
func TestAFreshRunIsRefusedWhereARepairIsOwed(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	// Somebody puts the item back, which is what lets a fresh run past every
	// other gate and says nothing about whether starting over is right.
	tracker.Item.Status = "open"

	fresh := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		t.Errorf("a fresh run reached a developer in %s, in place of the repair run %s is owed", request.WorkingDirectory, stopped.RunID)
		return nil
	}, approveVerdict)
	starting := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, fresh, []string{"exit 0"}), fresh)
	starting.NewRunID = runstate.NewRunID

	_, err := starting.Run(context.Background(), tracker.Item.ID)
	if !errors.Is(err, ErrHandbackSubstituted) {
		t.Fatalf("Run() error = %v, want the fresh run refused as a substituted handback", err)
	}
	// The refusal names both ways out, because which one is right is the
	// development manager's to decide and neither is guessable from the record.
	for _, want := range []string{"yoyo triage repair", "yoyo triage rerun", stopped.Branch} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal = %v, is missing %q", err, want)
		}
	}
	if len(fresh.Requests) != 0 {
		t.Fatalf("provider invocations = %d, want nothing spent", len(fresh.Requests))
	}
	// Nothing was reserved and nothing was created: the stoppage is exactly as it
	// was, so carrying out either decision afterwards costs it nothing.
	inFlight, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(inFlight) != 0 {
		t.Fatalf("runs in flight = %#v, want the refusal to have reserved nothing", inFlight)
	}
}

// And the one fresh run of such an item that is right still starts. A re-run is
// the development manager deciding the ground moved and the work is to be done
// again, and it says so in the record before it starts, so the refusal above
// reads that record rather than the shape of the stoppage alone.
func TestAClaimedReRunStartsFreshWhereARepairIsOwed(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	tracker.Item.Status = "open"

	if _, err := store.Reruns().Claim(context.Background(), runstate.Rerun{
		DocketKey:  triage.Key(triage.ClassStoppedRun, stopped.RunID),
		PriorRunID: stopped.RunID,
		WorkItemID: tracker.Item.ID,
		Reason:     "the ground under this change moved, so it is the item that needs running again rather than the change that needs repairing",
	}); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	var startedIn string
	fresh := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		startedIn = request.WorkingDirectory
		return writeHandbackChange(request.WorkingDirectory)
	}, approveVerdict)
	starting := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, fresh, []string{"exit 0"}), fresh)
	starting.NewRunID = runstate.NewRunID

	outcome, err := starting.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want the claimed re-run started", err)
	}
	if outcome.RunID == stopped.RunID || startedIn == "" || startedIn == stopped.WorktreePath {
		t.Fatalf("re-run = %s in %q, want a fresh run in a worktree of its own rather than the stopped run %s in %s",
			outcome.RunID, startedIn, stopped.RunID, stopped.WorktreePath)
	}
}

// emptyPreservedWorktree leaves the worktree registered and its HEAD exactly
// where the harness left it, holding none of the change: the state a worktree
// seeded from the target branch rather than from the preserved one is in, and
// the one the ownership gate passes.
func emptyPreservedWorktree(t *testing.T, path string) {
	t.Helper()
	for _, relative := range handbackFiles {
		if err := os.Remove(filepath.Join(path, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
	}
}

// reEnterAt records what a re-entry records before the pipeline is asked to
// continue a run: the run live again at the phase it is picked up in, and the
// item put back so the resumed run is not one the pipeline refuses.
func reEnterAt(t *testing.T, store *runstate.Store, tracker recordingTracker, runID string, phase runstate.Phase) {
	t.Helper()
	state, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	state.Status = runstate.StatusRunning
	state.Phase = phase
	state.Blocker = ""
	state.Failure = ""
	state.CompletedAt = nil
	if phase == runstate.PhaseDeveloping {
		// A handback counts the attempt it is about before the developer is
		// invoked, exactly as the repair loop's own attempts are counted.
		state.RepairAttempts++
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, _, err := tracker.Claim(context.Background(), tracker.Record().Item.ID); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
}

// stopWithPreservedChange runs one item to the stoppage the founding case
// started from: a developer that wrote the ten-file change, a reviewer that kept
// asking for repair until the budget was spent, and a run that ended on a
// durable blocker with its branch and worktree preserved.
func stopWithPreservedChange(t *testing.T, repository, worktreeRoot string, store *runstate.Store, tracker recordingTracker, docket *memoryDocket) Outcome {
	t.Helper()
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return writeHandbackChange(request.WorkingDirectory)
	}, repairVerdict)
	stopping := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	stopping.Docket = docketerOverStore(docket, store, stopping.Config)

	stopped, err := stopping.Run(context.Background(), tracker.Record().Item.ID)
	if err == nil {
		t.Fatal("Run() ended without stopping, so there is no handback to make")
	}
	if !stopped.Blocked || stopped.WorktreePath == "" {
		t.Fatalf("stopped run = %#v, want a blocked run with its worktree preserved", stopped)
	}
	if present := presentHandbackFiles(stopped.WorktreePath); len(present) != len(handbackFiles) {
		t.Fatalf("the stopped run preserved %d of the change's %d file(s): %v", len(present), len(handbackFiles), present)
	}
	return stopped
}

func writeHandbackChange(directory string) error {
	for _, relative := range handbackFiles {
		path := filepath.Join(directory, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("the preserved change: "+relative+"\n"), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// presentHandbackFiles is which of the change's files a worktree actually holds.
func presentHandbackFiles(directory string) []string {
	var present []string
	for _, relative := range handbackFiles {
		if _, err := os.Stat(filepath.Join(directory, filepath.FromSlash(relative))); err == nil {
			present = append(present, relative)
		}
	}
	return present
}

// repairContinuerOver wires the triage action over the same durable records the
// pipeline it continues acts on, so what the handback reads and what the resumed
// run enforces are one product's state rather than two.
func repairContinuerOver(t *testing.T, continuing Pipeline, store *runstate.Store, docket *memoryDocket, tracker WorkTracker) RepairContinuer {
	t.Helper()
	worktrees, ok := continuing.Worktrees.(*gitworktree.Manager)
	if !ok {
		t.Fatalf("pipeline worktrees = %T, want the real manager the handback proves the change with", continuing.Worktrees)
	}
	return RepairContinuer{
		Docket:             docket,
		Runs:               store,
		Intake:             newIntakeHoldStore(t),
		Decisions:          store.Triage(),
		Items:              tracker,
		Worktrees:          worktrees,
		ConfiguredAttempts: continuing.Config.Execution.RepairAttemptsBeforeReplan,
		Capacity:           continuing.Config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return continuing.Continue(ctx, workItemID, runID)
		},
	}
}
