package claudecode

// What a harness-invoked session is made of, and what it is guaranteed not to
// carry.
//
// Claude Code's plan mode is not a permission. It is the interactive layer's
// workflow, and asking for it puts that layer's instructions into the session's
// system prompt: do not execute yet, write a plan file, launch Explore and Plan
// agents, finish by calling ExitPlanMode. A harness-invoked role receives them
// on top of a role contract, and the two say opposite things -- the reviewer on
// run-fe0ad8461100ca399c4d2dee371afd53 was told to plan while its contract
// forbade it tools and required a single JSON verdict, and it reported the
// injection rather than obeying it. The same instructions reaching a developer
// forbid the edits the run exists to make, which is an empty worktree nobody
// can explain from the run record.
//
// The construction path is what makes that impossible: a request carries no
// session mode, so the mode is a function of the role and is decided in exactly
// one place. That is a claim about assembled invocations rather than about the
// one line that assembles them, so it is checked here as one: every role the
// harness can invoke, driven through the real Run, with the whole assembled
// command and everything entering the session's context read back.

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// planModeVocabulary is how the interactive layer's plan workflow reads when it
// reaches a session. The tool name is the load-bearing one -- nothing else in
// this harness has any reason to say it -- and the instructions beside it are
// what the reviewer quoted, kept so a leak through some other channel than the
// mode flag is caught by what it says rather than only by how it arrived.
var planModeVocabulary = []string{"ExitPlanMode", "plan mode", "exit_plan_mode", "write a plan file"}

// TestNoRoleSessionIsAssembledInPlanMode is the conformance check: every role
// this backend can invoke, assembled for real, and nothing in the result asking
// the provider to plan. It covers the roles rather than the call sites because
// the mode is now a function of the role -- a role added to the vocabulary
// arrives here without anybody remembering to add it.
func TestNoRoleSessionIsAssembledInPlanMode(t *testing.T) {
	t.Parallel()

	roles := domain.Roles()
	if len(roles) == 0 {
		t.Fatal("no roles, so the coverage below asserts nothing")
	}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			stream := `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done"}` + "\n"
			runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}
			request := backendapi.RunRequest{
				RunID:            testRunID,
				Role:             role,
				WorkingDirectory: "/worktree",
				Prompt:           "the harness's framing and the evidence",
				SystemPrompt:     "the role contract",
			}
			if readOnlyRole(role) {
				request.AllowedTools = []string{}
			}
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), request); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if len(runner.commands) != 1 {
				t.Fatalf("the invocation started %d process(es), want 1", len(runner.commands))
			}

			args := runner.commands[0].Args
			mode, named := sessionModeArgument(args)
			if !named {
				t.Fatalf("args = %#v, want exactly one --permission-mode", args)
			}
			if mode == "plan" {
				t.Fatalf("%s is invoked in plan mode, which puts the interactive layer's planning workflow into its session", role)
			}
			if want := sessionModeFor(role); mode != want {
				t.Fatalf("session mode = %q, want %q", mode, want)
			}

			// Everything the harness puts into the session, read as one body of
			// text: the command it assembled, the system prompt carrying the role
			// contract, and the prompt carrying the harness's framing.
			assembled := strings.Join(append(append([]string{}, args...), request.SystemPrompt, strings.Join(runner.prompts, "\n")), "\n")
			for _, marker := range planModeVocabulary {
				if strings.Contains(strings.ToLower(assembled), strings.ToLower(marker)) {
					t.Fatalf("the assembled %s session carries the interactive layer's plan workflow (%q)", role, marker)
				}
			}
		})
	}
}

// TestSessionModeFollowsThePosture pins the two modes to the two postures. It is
// what stops the read-only mode drifting back to "plan" on the reasoning that a
// role with no tools is unaffected by it: the mode is read before the tools are,
// and what it carries into the session is instructions rather than permissions.
func TestSessionModeFollowsThePosture(t *testing.T) {
	t.Parallel()

	for _, role := range domain.Roles() {
		mode := sessionModeFor(role)
		want := worktreeWriteSessionMode
		if backendapi.PostureFor(role) == backendapi.PostureReadOnly {
			want = readOnlySessionMode
		}
		if mode != want {
			t.Errorf("sessionModeFor(%q) = %q, want %q", role, mode, want)
		}
		if mode == "plan" {
			t.Errorf("sessionModeFor(%q) is plan mode", role)
		}
	}
}

// sessionModeArgument reads the mode out of an assembled command, and reports
// false unless exactly one was named. Two would leave which one the provider
// honours up to its own argument parsing, which is not something to assert
// around.
func sessionModeArgument(args []string) (string, bool) {
	mode, found := "", false
	for index, arg := range args {
		if arg != "--permission-mode" {
			continue
		}
		if found || index+1 >= len(args) {
			return "", false
		}
		mode, found = args[index+1], true
	}
	return mode, found
}

// A developer agent filling a role definition that removed the worktree write
// is held read-only: no tools, the read-only session, and safe mode, exactly as
// a role that reasons over supplied evidence. Asking for wider access than a
// role has is refused before anything starts.
func TestARequestedPostureNarrowsAndNeverWidens(t *testing.T) {
	t.Parallel()

	stream := `{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done"}` + "\n"
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: stream}}}
	request := backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		Posture:          backendapi.PostureReadOnly,
		WorkingDirectory: "/worktree",
		Prompt:           "the change to make",
	}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	args := runner.commands[0].Args
	if mode, _ := sessionModeArgument(args); mode != readOnlySessionMode {
		t.Fatalf("session mode = %q, want the read-only mode", mode)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--tools ") || strings.Contains(joined, "--allowedTools") || strings.Contains(joined, developerSettings) || !strings.Contains(joined, "--safe-mode") {
		t.Fatalf("args = %#v, want a tool-free, safe-mode session", args)
	}

	widened := &fakeRunner{}
	_, err := (Backend{Runner: widened, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleReviewer,
		Posture:          backendapi.PostureWorktreeWrite,
		WorkingDirectory: "/worktree",
		Prompt:           "the change to judge",
		AllowedTools:     []string{},
	})
	if err == nil || len(widened.commands) != 0 {
		t.Fatalf("Run() for a reviewer asking for worktree-write = %v after %d process(es), want a refusal before any", err, len(widened.commands))
	}
}
