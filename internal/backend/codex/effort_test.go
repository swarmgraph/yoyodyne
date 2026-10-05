package codex

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestEveryRolePassesCodexEffortOnFreshAndResumedInvocations(t *testing.T) {
	t.Parallel()
	for _, role := range domain.Roles() {
		for _, session := range []string{"", "session-1"} {
			for _, effort := range []string{"high", ""} {
				repository, worktree := sandboxRepository(t, true)
				runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: lines(`{"type":"turn.completed"}`)}}}
				_, err := (Backend{Runner: runner}).Run(context.Background(), backend.RunRequest{
					RunID: testRunID, Role: role, WorkingDirectory: worktree, RepositoryRoot: repository,
					Prompt: "do the work", Model: "gpt-6-astra", Effort: effort, SessionID: session,
				})
				if err != nil {
					t.Fatal(err)
				}
				want := effort
				if want == "" {
					want = "low"
				}
				args, count := runner.commands[0].Args, 0
				for i, arg := range args {
					if strings.HasPrefix(arg, "model_reasoning_effort=") {
						count++
						if i == 0 || args[i-1] != "--config" || arg != `model_reasoning_effort="`+want+`"` {
							t.Fatalf("%s, session %q, effort %q: %v", role, session, effort, args)
						}
					}
					if arg == "resume" && count != 1 {
						t.Fatalf("effort must be applied before resume: %v", args)
					}
				}
				if count != 1 {
					t.Fatalf("effort override occurs %d times: %v", count, args)
				}
				if err := recordedContract(t).misplacedOption(args); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestCodexEffortIsReportedOnlyWhenTheStreamNamesIt(t *testing.T) {
	t.Parallel()
	for _, reported := range []bool{false, true} {
		session := `{"type":"session_configured","session_id":"session-1","model":"gpt-6-astra"`
		if reported {
			session += `,"reasoning_effort":"medium"`
		}
		result, _ := runStream(t, domain.RoleDeveloper, lines(`{"msg":`+session+`}}`, `{"msg":{"type":"task_complete","last_agent_message":"done"}}`))
		want := ""
		if reported {
			want = "medium"
		}
		if result.EffortReported != reported || result.ResolvedEffort != want {
			t.Fatalf("reported=%v: result=%+v", reported, result)
		}
	}
}
