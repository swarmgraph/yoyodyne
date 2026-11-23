package claudecode

import (
	"context"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The configured effort level reaches Claude Code's own flag on every role's
// invocation, and an invocation that names none passes no flag at all, which
// leaves the provider resolving its own exactly as it did before the level was
// configurable.
func TestRunPassesTheEffortLevelOnlyWhereOneIsNamed(t *testing.T) {
	t.Parallel()

	stream := `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done"}` + "\n"
	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer, domain.RoleProductManager} {
		for _, effort := range []string{"medium", ""} {
			runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}}
			request := backendapi.RunRequest{
				RunID:            testRunID,
				Role:             role,
				ScratchDirectory: testScratch,
				WorkingDirectory: "/worktree",
				Prompt:           "go",
				Model:            "opus",
				Effort:           effort,
			}
			if role == domain.RoleReviewer || role == domain.RoleProductManager {
				request.AllowedTools = []string{}
			}
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), request); err != nil {
				t.Fatalf("%s, effort %q: Run() error = %v", role, effort, err)
			}
			args := runner.commands[0].Args
			passed, count := "", 0
			for index, arg := range args {
				if arg == "--effort" {
					count++
					if index+1 < len(args) {
						passed = args[index+1]
					}
				}
			}
			switch {
			case effort == "" && count != 0:
				t.Fatalf("%s: an invocation naming no effort passed --effort %q: %#v", role, passed, args)
			case effort != "" && (count != 1 || passed != effort):
				t.Fatalf("%s: effort %q was not passed once as --effort: %#v", role, effort, args)
			}
		}
	}
}
