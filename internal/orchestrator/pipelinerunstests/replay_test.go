package pipelinerunstests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestPipelineReplaysAndRetriesAPromotionWhoseTargetMoved(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	moved := ""
	developed := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		// The target moves once, after this run's worktree was cut from it, which
		// is exactly the window a losing promotion opens.
		developed++
		if developed > 1 {
			return nil
		}
		testharness.WritePipelineFile(t, repository, "elsewhere.txt", "somebody else's work\n")
		testharness.RunPipelineGit(t, repository, "add", "elsewhere.txt")
		testharness.RunPipelineGit(t, repository, "commit", "-m", "concurrent target change")
		moved = testharness.GitLine(t, repository, "rev-parse", "refs/heads/main")
		return nil
	}, testharness.ApproveVerdict)
	// The check records the worktree HEAD it ran against, so what it proves is
	// not only that the checks ran twice but that the second run judged the
	// replayed change rather than the one that lost the race. It writes outside
	// the worktree, so running it never becomes part of the change it describes.
	checked := filepath.Join(t.TempDir(), "checked-heads")
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider,
		[]string{"git rev-parse HEAD >> " + strconv.Quote(checked) + " && test -f feature.txt"})
	base := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main")

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("Run() outcome = %#v", outcome)
	}
	if outcome.IntegrationRetries != 1 {
		t.Fatalf("integration retries = %d, want 1", outcome.IntegrationRetries)
	}
	// The promotion was made from where the target actually was, not from the
	// base the run was cut at.
	if outcome.BaseCommit != moved || outcome.Integration.PreviousTargetCommit != moved {
		t.Fatalf("promotion base = %q / %q, want the moved target %q", outcome.BaseCommit, outcome.Integration.PreviousTargetCommit, moved)
	}
	// Nothing was lost and nothing was forced: base, then the change that moved
	// the target, then the replayed promotion, in a straight line.
	history := strings.Fields(testharness.GitOutput(t, repository, "rev-list", "refs/heads/main"))
	if len(history) != 3 || history[0] != outcome.Integration.TargetCommit || history[1] != moved || history[2] != base {
		t.Fatalf("main history = %v, want %q, %q, %q", history, outcome.Integration.TargetCommit, moved, base)
	}
	for _, name := range []string{"feature.txt", "elsewhere.txt"} {
		if _, err := os.Stat(filepath.Join(repository, name)); err != nil {
			t.Fatalf("main is missing %s after the retry: %v", name, err)
		}
	}

	// The approval that authorized the first promotion described a diff on the
	// old base, so the replayed change was reviewed again by its own invocation.
	if reviews := provider.RequestsForRole(domain.RoleReviewer); len(reviews) != 2 {
		t.Fatalf("reviewer invocations = %d, want 2", len(reviews))
	}
	// The deterministic checks are re-run against the replayed change, not
	// inherited from the pass that was approved before the replay.
	heads := strings.Fields(testharness.ReadPipelineFile(t, filepath.Dir(checked), filepath.Base(checked)))
	if len(heads) != 2 {
		t.Fatalf("check runs = %d (%v), want the checks run again after the replay", len(heads), heads)
	}
	// The first check ran on the commit the developer's attempt was recorded in,
	// which sits directly on the base the run was cut at: what it judged is the
	// change as it stood before the replay.
	if parent := testharness.GitLine(t, repository, "rev-parse", heads[0]+"^"); parent != base {
		t.Fatalf("first check ran against %q, whose parent is %q, want a commit on the original base %q", heads[0], parent, base)
	}
	if heads[1] != outcome.Integration.SourceCommit {
		t.Fatalf("second check ran against %q, want the replayed commit that was promoted %q", heads[1], outcome.Integration.SourceCommit)
	}
	// The retry is not a repair: nothing was handed back to the developer.
	if developers := provider.RequestsForRole(domain.RoleDeveloper); len(developers) != 1 {
		t.Fatalf("developer invocations = %d, want 1", len(developers))
	}
	// And so neither review is a round the item spent. Both approved, and an
	// approval is the end of the argument the cap bounds rather than another turn
	// of it; the second one also judged the same developer attempt on moved
	// ground, which would charge the item for losing a race it did not cause. The
	// case where those two reasons come apart — a replay whose fresh verdict sends
	// the work back — is next door, in
	// TestPipelineChargesNoRoundForAReplayedChangeSentBack.
	counters, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 0 {
		t.Fatalf("review rounds = %d, want none: two approvals and a replay cost the item nothing", counters.ReviewRounds)
	}
	if outcome.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want the retry to spend none", outcome.RepairAttempts)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The run's own count is the other half of that: two verdicts were reached
	// here, and the item was charged for neither, so the zero above is an
	// exclusion rather than a review that never happened.
	if state.ReviewRounds != 2 {
		t.Fatalf("run review rounds = %d, want the two verdicts this run reached", state.ReviewRounds)
	}
	if state.IntegrationRetries != 1 || state.BaseCommit != moved {
		t.Fatalf("durable retry evidence = %#v", state)
	}
	if !strings.Contains(tracker.Notes, "Landing attempts lost to other work: 1. 1 of them were replayed cleanly") {
		t.Fatalf("notes are missing the retry evidence: %q", tracker.Notes)
	}
}

// The replayed change is judged afresh, and the fresh verdict can disagree with
// the one that authorized the promotion: the ground moved, and what was right
// against the old base can be wrong against the new one. That verdict sends the
// work back, and it is still not a round the item spent — it is the same
// developer attempt judged twice, and the second judgement is the race's doing.
//
// This is the case an approval that went unrecorded would break, and it is why
// one is recorded. The deduplication is what excludes the replay, and it has
// nothing to work from unless the approval that preceded the promotion named the
// attempt it approved.
func TestPipelineChargesNoRoundForAReplayedChangeSentBack(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	developed := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		// The target moves once, after this run's worktree was cut from it, which
		// is the window a losing promotion opens. The repair that follows the
		// replay's verdict must not open a second one.
		developed++
		if developed > 1 {
			return nil
		}
		testharness.WritePipelineFile(t, repository, "elsewhere.txt", "somebody else's work\n")
		testharness.RunPipelineGit(t, repository, "add", "elsewhere.txt")
		testharness.RunPipelineGit(t, repository, "commit", "-m", "concurrent target change")
		return nil
	}, testharness.ApproveVerdict, testharness.RepairVerdict, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("Run() outcome = %#v, want the repaired replay integrated", outcome)
	}
	// Three verdicts: the approval that authorized the promotion, the replay's
	// repair, and the approval of what the repair produced.
	if reviews := provider.RequestsForRole(domain.RoleReviewer); len(reviews) != 3 {
		t.Fatalf("reviewer invocations = %d, want the approval, the replay's repair, and the approval after it", len(reviews))
	}
	if outcome.IntegrationRetries != 1 || outcome.RepairAttempts != 1 {
		t.Fatalf("outcome = %#v, want one replay and the one repair its verdict asked for", outcome)
	}

	// And the item was charged for none of them. The two approvals are not rounds,
	// and the repair verdict in between judged the attempt the first approval had
	// already been obtained on — so it is the replay exclusion rather than the
	// approval exclusion doing the work here, which is the whole point.
	counters, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 0 {
		t.Fatalf("review rounds = %d, want none: the repair verdict judged a replayed attempt the item had already been answered about", counters.ReviewRounds)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The run's own count is what says the zero above is an exclusion rather than
	// a review that never happened.
	if state.ReviewRounds != 3 {
		t.Fatalf("run review rounds = %d, want the three verdicts this run reached", state.ReviewRounds)
	}
}

// A repair verdict whose whole residue is one out-of-scope finding costs the item
// nothing either, by the operator's direction of 2026-09-05. The reviewer said
// the work is right and named one thing beside it that is not this change's to
// do, which is the end of the argument the cap bounds rather than another turn of
// it — and four items in a week reached their caps on rounds of that shape.
//
// The work still goes back to the developer and the run still spends an attempt
// on it: what changes is the item's bill, not what happens to the change.
func TestPipelineChargesNoRoundForARepairWithOneTrivialFinding(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.OutOfScopeVerdict, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("Run() outcome = %#v, want the repaired change integrated", outcome)
	}
	// The note was acted on: the verdict sent the work back exactly as any repair
	// does, and the run spent one of its own attempts doing it.
	if outcome.RepairAttempts != 1 {
		t.Fatalf("repair attempts = %d, want the one the out-of-scope finding asked for", outcome.RepairAttempts)
	}
	counters, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 0 {
		t.Fatalf("review rounds = %d, want none: one out-of-scope finding is a trivial residue, and the approval after it is not a round either", counters.ReviewRounds)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The run's own count is what says the zero above is an exclusion rather than
	// two reviews that never happened.
	if state.ReviewRounds != 2 {
		t.Fatalf("run review rounds = %d, want the two verdicts this run reached", state.ReviewRounds)
	}
}

// The other half of yoyodyne-ifd.359: a repair whose whole residue is one minor
// finding with no disposition is charged a round like any repair. Minor is how
// serious the reviewer judged the problem, not whether the change has to fix it,
// and a budget read off the severity made a real defect labelled minor a free
// round.
func TestPipelineChargesARoundForARepairWithOneMinorFinding(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.MinorVerdict, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("Run() outcome = %#v, want the repaired change integrated", outcome)
	}
	counters, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 1 {
		t.Fatalf("review rounds = %d, want the one the minor repair spent and none for the approval after it", counters.ReviewRounds)
	}
}

// And the line holds on the other side of itself: a repair carrying a blocker is
// the reviewer arguing with the change, and the item is charged for it.
func TestPipelineChargesARoundForARepairThatIsNotTrivial(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, testharness.RepairVerdict, testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.RepairAttempts != 1 {
		t.Fatalf("Run() outcome = %#v, want the repaired change integrated after one attempt", outcome)
	}
	counters, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 1 {
		t.Fatalf("review rounds = %d, want the one the blocker cost", counters.ReviewRounds)
	}
}

// The budget is bounded, and what spends it is a replay that stops on the
// change, never the race. At a budget of zero the replayed change drawing a
// repair verdict stops the run right there and records it on the item rather
// than disappearing, with the target keeping what moved it and the replayed
// change preserved for whoever picks it up.
func TestPipelineBlocksWhenTheIntegrationRetryBudgetIsSpent(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		testharness.WritePipelineFile(t, repository, "elsewhere.txt", "somebody else's work\n")
		testharness.RunPipelineGit(t, repository, "add", "elsewhere.txt")
		testharness.RunPipelineGit(t, repository, "commit", "-m", "concurrent target change")
		return nil
	}, testharness.ApproveVerdict, testharness.RepairVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation = 0

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	testharness.AssertSavedStopClass(t, store, outcome.RunID, runstate.StopIntegrationBudget)
	if outcome.StopClass != runstate.StopIntegrationBudget {
		t.Fatalf("stop class = %q, want %q", outcome.StopClass, runstate.StopIntegrationBudget)
	}
	moved := testharness.GitLine(t, repository, "rev-parse", "refs/heads/main")
	if err == nil || !strings.Contains(err.Error(), "stopped on the change") {
		t.Fatalf("Run() error = %v", err)
	}
	if !outcome.Blocked || outcome.Integration != nil || tracker.Closed {
		t.Fatalf("Run() outcome = %#v, closed = %t", outcome, tracker.Closed)
	}
	if !tracker.Blocked || !strings.Contains(tracker.BlockReason, "Replays that stopped on the change: 1 of 0 permitted") {
		t.Fatalf("blocker = %t: %q", tracker.Blocked, tracker.BlockReason)
	}
	// The target keeps whatever moved it, and the replayed change stays where a
	// person can pick it up, on the base it was replayed onto.
	if parent := testharness.GitLine(t, outcome.WorktreePath, "rev-parse", "HEAD^"); parent != moved {
		t.Fatalf("worktree HEAD^ = %q, want the moved target %q it was replayed onto", parent, moved)
	}
	if _, err := os.Stat(filepath.Join(outcome.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("worktree was not preserved: %v", err)
	}
	// Nothing was handed back: the one developer invocation is the first attempt.
	if developers := provider.RequestsForRole(domain.RoleDeveloper); len(developers) != 1 {
		t.Fatalf("developer invocations = %d, want 1", len(developers))
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Integration != nil || state.IntegrationRetries != 1 || state.ChargedReplays != 1 || state.BaseCommit != moved {
		t.Fatalf("state = %#v", state)
	}
}

// A replay that conflicts is the one case the harness must not resolve. Once
// the run's three reconciliation attempts are used, a fourth conflict blocks
// with both sides intact rather than choosing between them, and leaves the
// repair attempts the run still has for the development manager to grant.
func TestPipelineBlocksOnAReplayConflictWithoutResolvingIt(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// The target moves onto the same line, so the replay is a decision rather
	// than a mechanical one.
	provider := orchestratortest.RoleBackend(testharness.ConflictsAtEveryLanding(t, repository, filepath.Join("docs", "design.md"),
		"this run's answer\n", "somebody else's answer\n", testharness.SomebodyElsesSecondAnswer), testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 2

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "cannot be replayed onto the moved target branch") ||
		!strings.Contains(err.Error(), "again after this run's 3 reconciliation attempts were already used") {
		t.Fatalf("Run() error = %v, want the fourth conflict stopping the run and naming the reconciliation attempts as used", err)
	}
	if !outcome.Blocked || outcome.Integration != nil || tracker.Closed {
		t.Fatalf("Run() outcome = %#v, closed = %t", outcome, tracker.Closed)
	}
	if !tracker.Blocked || !strings.Contains(tracker.BlockReason, "conflicts with what the target now holds") {
		t.Fatalf("blocker = %t: %q", tracker.Blocked, tracker.BlockReason)
	}
	if !strings.Contains(tracker.BlockReason, "Nothing was force-merged") {
		t.Fatalf("blocker does not say what was not done: %q", tracker.BlockReason)
	}
	// Both sides survive: the target keeps its answer, the worktree keeps this
	// run's, and neither was merged into the other.
	if content := testharness.ReadPipelineFile(t, repository, filepath.Join("docs", "design.md")); content != testharness.SomebodyElsesLastAnswer {
		t.Fatalf("target content = %q", content)
	}
	if content := testharness.ReadPipelineFile(t, outcome.WorktreePath, filepath.Join("docs", "design.md")); content != "this run's answer\n" {
		t.Fatalf("preserved worktree content = %q", content)
	}
	if status := testharness.GitOutput(t, outcome.WorktreePath, "status", "--porcelain=v1"); strings.TrimSpace(status) != "" {
		t.Fatalf("preserved worktree is mid-replay: %q", status)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Integration != nil || state.IntegrationRetries != 1+runstate.MaxReconciliations || state.HarnessCommit == "" ||
		state.ReconciliationsUsed() != runstate.MaxReconciliations || state.RepairAttempts != 0 {
		t.Fatalf("state = %#v", state)
	}
	if head := testharness.GitLine(t, outcome.WorktreePath, "rev-parse", "HEAD"); head != state.HarnessCommit {
		t.Fatalf("worktree HEAD = %q, want the recorded harness commit %q", head, state.HarnessCommit)
	}
	// The conflict is on the record as its own fact, and the docket names a person
	// as the next mover from it rather than from the blocker's prose.
	if state.ReplayConflict == nil || state.ReplayConflict.TargetBranch != "main" || state.ReplayConflict.Phase != runstate.PhaseIntegrating {
		t.Fatalf("replay conflict = %#v, want the conflict onto main recorded at the integrating phase", state.ReplayConflict)
	}
	if state.IntegrationStop != nil {
		t.Fatalf("a replay conflict was recorded as an environmental stop: %#v", state.IntegrationStop)
	}
	// The conflict stays on the record, unmoved, so a repair granted afterwards
	// hands the same developer the same disagreement rather than leaving a re-run
	// as the only way on.
	if state.ReplayConflict == nil || state.ReplayConflict.Moved || state.ReplayConflict.TargetBranch != "main" {
		t.Fatalf("recorded conflict = %#v, want the refused replay kept and not yet moved", state.ReplayConflict)
	}
	if want := []string{"docs/design.md"}; strings.Join(state.ReplayConflict.Paths, ",") != strings.Join(want, ",") {
		t.Fatalf("conflicted paths = %v, want %v", state.ReplayConflict.Paths, want)
	}
	if !strings.Contains(tracker.BlockReason, "The replay stopped on: docs/design.md") || !strings.Contains(tracker.BlockReason, "Repair attempts: 0 of 2 permitted") {
		t.Fatalf("blocker does not name the conflict and the spent budget: %q", tracker.BlockReason)
	}
	// The stop reason says the reconciliation attempts were already used, each
	// with the commit it conflicted with and, after the first, that no hold was
	// applied, and what each lost landing made of the change.
	for _, want := range []string{"after the run's 3 reconciliation attempts were already used", "Reconciliation attempt 1 of 3: used.",
		"Reconciliation attempt 2 of 3: used.", "Reconciliation attempt 3 of 3: used.", "It was not prevented because the merge queue held no entries that change the same files for it.",
		"Landing attempts lost to other work: 4. 0 of them were replayed cleanly", "Lost landing 1:", "it conflicted with what other work had changed there", "Lost landing 4:"} {
		if !strings.Contains(tracker.BlockReason, want) {
			t.Fatalf("blocker does not say %q:\n%s", want, tracker.BlockReason)
		}
	}

}

// Run run-c4f75e5b's shape: an approved change whose replay conflicts, and whose
// blocker the tracker never took because the `bd update` recording it timed out.
// The error ending the run carries the timeout beside the conflict, and the
// timeout on its own reads as the transport class — which is how the conflict
// reached the record as an integration stop and the docket named a resume that
// could only conflict again. The conflict is what stopped the run whatever
// failed while it was being written down, so nothing resumable is recorded and
// the docket names the development manager (yoyodyne-ifd.429.10).
func TestAReplayConflictWhoseBlockerCouldNotBeWrittenIsNeverAResumableStop(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{
		Item:     beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"},
		BlockErr: errors.New("bd update failed with status cancelled and exit code -1: signal: killed"),
	}
	provider := orchestratortest.RoleBackend(testharness.ConflictsAtEveryLanding(t, repository, filepath.Join("docs", "design.md"),
		"this run's answer\n", "somebody else's answer\n", testharness.SomebodyElsesSecondAnswer), testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	// The run's reconciliation attempts answer the first three conflicts, and the
	// fourth goes straight to the blocker write.
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0
	// With no blocker on the item, the run reaches the docket as a death that
	// preserved its change, which is written as the run ends rather than found by
	// a later scan.
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	pipeline.Docket = testharness.DocketerOverStore(docket, store, pipeline.Config)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if !errors.Is(err, gitworktree.ErrRebaseConflict) || !errors.Is(err, tracker.BlockErr) {
		t.Fatalf("Run() error = %v, want the conflict and the failed recording both reported", err)
	}
	// The premise: what failed while recording is, read alone, a failure the
	// harness would resume past. Without it this test proves nothing.
	if !recovery.Recoverable(tracker.BlockErr) {
		t.Fatalf("the recording failure %q is not the transport class; the test no longer drives the misclassification", tracker.BlockErr)
	}
	if outcome.IntegrationStop != nil {
		t.Fatalf("Run() outcome integration stop = %#v, want none for a conflict", outcome.IntegrationStop)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.IntegrationStop != nil {
		t.Fatalf("recorded integration stop = %#v, want none: a conflict is never resumable", state.IntegrationStop)
	}
	if !state.ApprovedAwaitingIntegration() {
		t.Fatalf("run = decision %q, want the approval standing so the stop would have been recorded had it been classified as one", state.ReviewDecision)
	}
	// The conflict is on the record even though the blocker never reached the
	// item.
	if !strings.Contains(state.Failure, "cannot be replayed onto the moved target branch") {
		t.Fatalf("recorded failure = %q, want the conflict", state.Failure)
	}

	built, err := testharness.DocketerOverStore(docket, store, pipeline.Config).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 {
		t.Fatalf("docket = %#v, want the conflicted run on it", built.Entries)
	}
	entry := built.Entries[0]
	if entry.IntegrationStop != nil {
		t.Fatalf("docket entry integration stop = %#v, want none", entry.IntegrationStop)
	}
	// The entry names the resume only to say it is not the answer
	// (yoyodyne-ifd.429.1); what it must not do is send anybody to it.
	rendered := entry.Render()
	if strings.Contains(rendered, "Next mover: the harness") || !strings.Contains(rendered, "Next mover: you") || !strings.Contains(rendered, "`yoyo triage resume` is not the answer") {
		t.Fatalf("the docket does not name the development manager for a conflict:\n%s", rendered)
	}
}

// The conflict half of yoyodyne-ifd.349's shape through the pipeline: an item
// one round from its cap whose approving round is followed by a replay conflict.
// The approval charges nothing, the conflict charges nothing — no verdict was
// reached about it — and the run stops on the conflict path with the approval
// standing on its record, so the re-run the development manager records to run
// the change again on the moved base is permitted without an override. The
// reservation half — the approving round was a granted continuation, and the
// grant's reservation is released by it — is asserted where a real continuation
// runs, at the end of TestARepairContinuationLandsTheChangeTheStoppedRunAlreadyHad;
// what this run's approval must not do is touch a reservation standing for some
// other stoppage of the item, which it never judged.
func TestAReplayConflictAfterApprovalChargesNothingAndLeavesTheApprovalStanding(t *testing.T) {
	t.Parallel()

	repository := testharness.PipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-ifd.349", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(testharness.ConflictsAtEveryLanding(t, repository, filepath.Join("docs", "design.md"),
		"this run's answer\n", "somebody else's answer\n", testharness.SomebodyElsesSecondAnswer), testharness.ApproveVerdict)
	pipeline, store := testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	// The run's reconciliation attempts answer the first three conflicts, and
	// the fourth stops the run: the shape this test is about.
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0
	// Two rounds already spent across the item's earlier runs and a repair grant
	// of one standing on an earlier stoppage, so the item is committed to three
	// of the cap's four before this run reaches its reviewer.
	caps := orchestrator.TriageCaps(pipeline.Config.Execution, pipeline.Config.Triage)
	for round := range 2 {
		if _, err := store.Triage().RecordReviewRound(context.Background(), tracker.Item.ID, runstate.RoundKey(testharness.PriorRunID, round), "pid-1-000000000000000a", time.Now()); err != nil {
			t.Fatalf("RecordReviewRound() error = %v", err)
		}
	}
	granted, err := store.Triage().GrantRepair(context.Background(), tracker.Item.ID, testharness.TriageDecided(runstate.TriageDecisionRepair, testharness.PriorRunID), 1, time.Now(), caps)
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if granted.Rounds != 1 || granted.Counters.CommittedRounds != 3 {
		t.Fatalf("grant = %+v, want one round reserved on the earlier stoppage", granted)
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "cannot be replayed onto the moved target branch") {
		t.Fatalf("Run() error = %v", err)
	}
	if !outcome.Blocked || outcome.Integration != nil {
		t.Fatalf("Run() outcome = %#v, want the conflict blocking the run", outcome)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The approval stands on the stopped run: a conflict is not a verdict, and
	// the verdict that was reached is what the decision about the stoppage reads.
	if state.ReviewDecision != string(review.DecisionApprove) || state.ReviewRounds != 1+runstate.MaxReconciliations {
		t.Fatalf("stopped run = decision %q, %d review round(s); want the approval standing and the verdicts it reached, before and after each reconciliation", state.ReviewDecision, state.ReviewRounds)
	}
	counters, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	// Neither the approval nor the conflict spent anything, and the approval
	// released nothing either: the round it judged was this run's, and the
	// reservation standing is the earlier stoppage's.
	if counters.ReviewRounds != 2 || counters.CommittedRounds != 3 {
		t.Fatalf("counters after the conflict = %d spent, %d committed; want the two earlier rounds and the other stoppage's reservation untouched", counters.ReviewRounds, counters.CommittedRounds)
	}
	if want := runstate.RoundKey(outcome.RunID, 0); counters.LastJudged != want {
		t.Fatalf("last judged attempt = %q, want the approved attempt %q recorded without being charged", counters.LastJudged, want)
	}
	// The development manager's re-run, recorded against the same cap, needs no
	// override.
	if _, err := store.Triage().RecordRerun(context.Background(), tracker.Item.ID, testharness.TriageDecided(runstate.TriageDecisionRerun, outcome.RunID), time.Now(), caps); err != nil {
		t.Fatalf("RecordRerun() after the approved-then-conflicted run = %v, want it permitted without an override", err)
	}
}
