package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/budget"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicjoin"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The judgment is a read-only session of the reviewer's that resumes none, and its
// cost is recorded under its own identifier.
func TestTheJudgmentOfAPieceIsAFreshReadOnlyReviewerSession(t *testing.T) {
	t.Parallel()
	cfg := answeringConfig()
	if epicJoinsFrom(components{config: cfg, branchReviews: &runstate.BranchReviewStore{}}) != nil {
		t.Fatal("a project with no reviewer judges pieces")
	}
	if epicCommentsFrom(components{config: cfg, branchReviews: &runstate.BranchReviewStore{}}) != nil {
		t.Fatal("a project with no reviewer judges review comments")
	}
	binary := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.Providers = map[string]backend.ProviderPlugin{string(domain.BackendCodex): {Binary: binary}}
	cfg.Accounts = map[string]config.Account{"reviewing": {Provider: domain.BackendCodex}}
	cfg.Agents["reviewer"] = config.AgentConfig{Role: domain.RoleReviewer, Backend: domain.BackendCodex, Model: "gpt-6.1-sol", Account: "reviewing"}
	if epicCommentsFrom(components{config: cfg, branchReviews: &runstate.BranchReviewStore{}}) == nil {
		t.Fatal("a project with a reviewer does not judge review comments")
	}
	cli := &answeringCLIRunner{}
	root := t.TempDir()
	spends, err := runstate.NewSpendStore(root, cfg.Product.ID)
	if err != nil {
		t.Fatal(err)
	}
	joins := epicJoins{components{config: cfg, repository: t.TempDir(), stateRoot: root, spend: spends}, cli}
	id, err := runstate.NewBranchReviewID()
	if err != nil {
		t.Fatal(err)
	}
	request := epicjoin.Request{ID: id, System: "judge it", Prompt: "the piece", Sink: func(execution.Event) error { return nil }}
	if _, err := joins.judge(context.Background(), request, nil); err == nil || len(cli.commands) != 0 {
		t.Fatalf("a call nothing permitted was made: %v", err)
	}
	request.Permission = budget.Decide(budget.Spent{}, budget.Limits{}).Permission
	var line runstate.Spend
	result, err := joins.judge(context.Background(), request, func(recorded runstate.Spend) { line = recorded })
	if err != nil || result.SessionID != "answer-session" || result.FinalText != "The answer." {
		t.Fatalf("result = %+v, %v", result, err)
	}
	args := cli.commands[0].Args
	if slices.Contains(args, "resume") || !strings.Contains(strings.Join(args, "\n"), `default_permissions="yoyodyne-read-only-`) {
		t.Errorf("the session may write, or continues another: %v", args)
	}
	if line.Role != domain.RoleReviewer || line.BranchReviewID != id || line.AccountAlias != "reviewing" {
		t.Errorf("the cost line is %+v", line)
	}
}
