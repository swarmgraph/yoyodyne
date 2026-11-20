package runstate_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// This test is outside runstate so it can drive the pipeline without an import
// cycle. The test-only export shortens the existing per-store queue clock; the
// lease refusal and terminal write both run their production code.
func TestPromotionQueueBoundSavesTheRunsStopCause(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "Yoyodyne Test"},
		{"config", "user.email", "yoyodyne@example.invalid"},
		{"config", "maintenance.auto", "false"},
		{"config", "gc.auto", "0"},
		{"-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "initial"},
	} {
		command := exec.Command("git", args...)
		command.Dir = repository
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	cfg, err := config.Decode(strings.NewReader(fmt.Sprintf("version: %d\nextends: %s\nproduct:\n  id: yoyodyne\n  repository_id: yoyodyne\n  repository: %q\n", config.CurrentVersion, config.BuiltinV1, repository)))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Approvals.Integration = domain.ApprovalAutomatic
	cfg.Checks = []string{"exit 0"}
	cfg.Execution.DeclarativeDelivery = true
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	runstate.SetPromotionWaitForTest(store, 20*time.Millisecond)
	lease, err := store.LeasePromotion(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	process := execution.OSProcessRunner{}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: process, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"), Timeout: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	directives, err := runstate.NewDirectiveStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	holds, err := runstate.NewOperatorHoldStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, `{"decision":"approve","approves":"implementation","summary":"the change matches the work item"}`)
	reviewerModel := cfg.Agents["reviewer"].Model
	pipeline := orchestrator.Pipeline{
		Repository: repository, Config: cfg, Tracker: tracker, Store: store,
		Worktrees: worktrees, Backend: provider, Checks: checks.Runner{Process: process},
		Reviewer:   review.Reviewer{Backend: provider, Model: reviewerModel},
		Directives: directives, Holds: holds, Intake: intake, NewRunID: runstate.NewRunID,
		StateRoot: t.TempDir(),
	}
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "another promotion held the lease") {
		t.Fatalf("Run() error = %v; want the real promotion queue to reach its bound", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Status.Terminal() || state.StopClass != runstate.StopPromotionWait || outcome.Integration != nil {
		t.Fatalf("saved status = %q, stop_class = %q, integration = %+v; want an unlanded run ended by the promotion wait", state.Status, state.StopClass, outcome.Integration)
	}
}
