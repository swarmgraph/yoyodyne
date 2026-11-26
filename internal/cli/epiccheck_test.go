package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epiccheck"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// checkingCLIRunner is a provider session that answers the text given.
type checkingCLIRunner struct {
	commands []execution.Command
	text     string
}

func (r *checkingCLIRunner) Run(_ context.Context, command execution.Command, observe execution.OutputObserver) (execution.ProcessResult, error) {
	r.commands = append(r.commands, command)
	for _, line := range []string{`{"type":"thread.started","thread_id":"check-session"}`, `{"type":"item.completed","item":{"type":"agent_message","text":` + strconvQuote(r.text) + `}}`, `{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`} {
		observe(execution.Output{Stream: execution.StreamStdout, Text: line})
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

func strconvQuote(text string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(text, `\`, `\\`), `"`, `\"`) + `"`
}

// The check is a new read-only session of the reviewer's, its cost is recorded
// under its own identifier, and what it answered is kept in its log, where a
// restart looks it up; an answer that cannot be read is kept as never sent.
func TestTheCheckOfTheWholeEpicIsANewReadOnlyReviewerSession(t *testing.T) {
	t.Parallel()
	cfg := answeringConfig()
	if epicChecksFrom(components{config: cfg, branchReviews: &runstate.BranchReviewStore{}}) != nil {
		t.Fatal("a project with no reviewer makes checks")
	}
	binary := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.Providers = map[string]backend.ProviderPlugin{string(domain.BackendCodex): {Binary: binary}}
	cfg.Accounts = map[string]config.Account{"reviewing": {Provider: domain.BackendCodex}}
	cfg.Agents["reviewer"] = config.AgentConfig{Role: domain.RoleReviewer, Backend: domain.BackendCodex, Model: "gpt-6.1-sol", Account: "reviewing"}
	root := t.TempDir()
	spends, err := runstate.NewSpendStore(root, cfg.Product.ID)
	if err != nil {
		t.Fatal(err)
	}
	reviews, err := runstate.NewBranchReviewStore(root, cfg.Product.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		text   string
		looked epicprogress.Looked
	}{
		{`{"findings":[{"asks":"it","answer":"met","says":"the test"}]}`, epicprogress.Found},
		{"I could not decide.", epicprogress.NotSent},
	} {
		cli := &checkingCLIRunner{text: test.text}
		checks := epicChecks{components{config: cfg, repository: t.TempDir(), stateRoot: root, spend: spends, branchReviews: reviews}, cli}
		id, err := runstate.NewBranchReviewID()
		if err != nil {
			t.Fatal(err)
		}
		if _, looked, _ := checks.lookUp(id); looked != epicprogress.NotSent {
			t.Fatalf("a check never sent looks up as %v", looked)
		}
		var line runstate.Spend
		says, err := checks.judge(context.Background(), epiccheck.Request{ID: id, System: "check it", Prompt: "the epic"}, func(recorded runstate.Spend) { line = recorded })
		if (err == nil) != (test.looked == epicprogress.Found) {
			t.Fatalf("%q: %v", test.text, err)
		}
		args := cli.commands[0].Args
		if slices.Contains(args, "resume") || !strings.Contains(strings.Join(args, "\n"), `default_permissions="yoyodyne-read-only-`) {
			t.Errorf("the session may write, or continues another: %v", args)
		}
		if line.Role != domain.RoleReviewer || line.BranchReviewID != id || line.AccountAlias != "reviewing" {
			t.Errorf("the cost line is %+v", line)
		}
		kept, looked, err := checks.lookUp(id)
		if err != nil || looked != test.looked || kept != says || test.looked == epicprogress.Found && !strings.Contains(says, `"session":"check-session"`) {
			t.Fatalf("%q looks up as %v, %q, %v; answered %q", test.text, looked, kept, err, says)
		}
	}
}

// A member is given with the commit its change landed as and its review, and
// every session that wrote or reviewed it; one whose change waits on a person is
// given with its approved head.
func TestAMemberIsGivenWithItsChangeItsReviewAndItsSessions(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	failed := runstate.State{RunID: "run-1", StartedAt: at, ProviderSessionID: "dev-1", ReviewSessionID: "rev-1"}
	merged := runstate.State{RunID: "run-2", StartedAt: at.Add(time.Hour), ProviderSessionID: "dev-2", ReviewSessionID: "rev-2",
		Status: runstate.StatusSucceeded, ReviewDecision: runstate.ReviewApprove, ReviewSummary: "it does it",
		PullRequest: &runstate.PullRequest{Number: 3, HeadCommit: "head2", MergeCommit: "merge2", Merged: true}}
	member, handed := memberOf("yoyodyne-ep.1", "the first thing", []runstate.State{failed, merged})
	if !merged.Discharges() {
		t.Fatal("the fixture run does not discharge its item")
	}
	if handed || member.Change != "merge2" || !strings.Contains(member.Review, "it does it") || strings.Join(member.Sessions, ",") != "dev-1,rev-1,dev-2,rev-2" {
		t.Fatalf("merged: %+v, handed %t", member, handed)
	}
	merged.PullRequest.MergeCommit, merged.PullRequest.Merged = "", false
	if member, handed := memberOf("yoyodyne-ep.1", "", []runstate.State{merged}); !handed || member.Change != "head2" {
		t.Fatalf("handed over: %+v, handed %t", member, handed)
	}
}
