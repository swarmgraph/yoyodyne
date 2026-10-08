package orchestrator

// A run whose developer is configured for Codex, driven through the real adapter
// over a CLI that is not there.
//
// This is where the second endpoint is actually held to being usable. The
// adapter's own tests build a backend.RunRequest by hand, so they can only show
// that the adapter honours what they asked for; what they cannot show is that
// the request the harness itself builds is one the adapter accepts. That gap is
// not academic: the adapter turns a role's tool posture into a Codex permission
// profile from
// a fixed table and refuses a role it has no entry for, and a refusal there lands
// inside Run — after the item is claimed, the worktree is created, and the
// operator is waiting — which is the opposite of the policy refusal a
// configuration is meant to earn. Letting the pipeline build the request is the
// only way to see that.
//
// This test keeps the reviewer on the other provider to exercise mixed-provider
// dispatch while the developer uses the Codex worktree-write permission profile.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/codex"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The permission profile the developer's tool posture requires by the time the
// invocation reaches the provider. It is spelled out here rather than imported
// from the adapter on purpose: this test is the other side of that statement,
// and two sides reading one constant would agree with each other whatever the
// adapter did.
const codexDeveloperProfile = "yoyodyne-developer"

func TestARunOnCodexReachesTheProviderWithThePostureItsRoleRequires(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	pipeline, store := newAutomaticPipeline(t, repository, tracker,
		orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict), []string{"exit 0"})

	// Selecting Codex in configuration, which is what this is about. The
	// developer names it, and the pipeline the run validates before it claims
	// anything has to accept that.
	pipeline.Config.Agents["developer"] = config.AgentConfig{
		Role: domain.RoleDeveloper, Backend: domain.BackendCodex, Model: "gpt-6.1-sol", Instances: 1,
	}
	// The real adapter over a CLI that is not there. What is under test is how the
	// invocation is launched, so this double's answers are the least interesting
	// part of it and its command lines are the whole point.
	cli := &scriptedCodexCLI{}
	pipeline.Backend = codex.Backend{Runner: cli}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() outcome = %#v, want a run that completed on Codex", outcome)
	}
	// The session comes off the provider's own stream, so a run carrying it is one
	// whose events this adapter really parsed rather than one a double answered.
	if outcome.ProviderSessionID != codexSessionID {
		t.Fatalf("recorded provider session = %q, want the one the Codex stream named", outcome.ProviderSessionID)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Backend != domain.BackendCodex {
		t.Fatalf("recorded backend = %q, want configured Codex provider", state.Backend)
	}
	if state.ProviderResolvedModel != codexResolvedModel {
		t.Fatalf("recorded resolved model = %q, want the model the Codex stream named", state.ProviderResolvedModel)
	}

	// The invocation was launched under the profile the developer's tool posture
	// requires — able to edit the worktree — and in the run's own worktree, which
	// the profile names as the directory it may write.
	developer := cli.developerInvocation(t)
	if !strings.Contains(strings.Join(developer.Args, "\n"), "--config\nmodel_reasoning_effort=\"low\"") {
		t.Errorf("the developer invocation lacks explicit default effort: %v", developer.Args)
	}
	if got := codexProfileOf(t, developer.Args); got != codexDeveloperProfile {
		t.Errorf("the developer ran under permission profile %q, want %q", got, codexDeveloperProfile)
	}
	worktree, _ := json.Marshal(outcome.WorktreePath)
	if !strings.Contains(strings.Join(developer.Args, "\n"), string(worktree)+`="write"`) {
		t.Errorf("the developer's profile does not let it write the run's worktree %q: %v", outcome.WorktreePath, developer.Args)
	}
	if strings.Contains(strings.Join(developer.Args, "\n"), "--sandbox") {
		t.Errorf("the developer invocation passes --sandbox, which replaces its permission profile: %v", developer.Args)
	}
	if developer.Dir != outcome.WorktreePath {
		t.Errorf("the developer ran in %q, want the run's worktree %q", developer.Dir, outcome.WorktreePath)
	}
}

// A run configured for a provider whose CLI is not installed says so about that
// provider. The check is the same one every run makes; what this holds is that
// it stopped naming one backend for all of them, because sending an operator to
// install the other provider is a remedy for a machine that is not theirs.
func TestARunNamesTheBackendWhoseCLIIsMissing(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	pipeline, _ := newAutomaticPipeline(t, repository, tracker,
		orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict), []string{"exit 0"})
	pipeline.Config.Agents["developer"] = config.AgentConfig{
		Role: domain.RoleDeveloper, Backend: domain.BackendCodex, Model: "gpt-6.1-sol", Instances: 1,
	}
	pipeline.Backend = codex.Backend{Runner: &scriptedCodexCLI{absent: true}}

	_, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "codex backend cannot run in this environment") || !strings.Contains(err.Error(), "codex was not found on PATH") {
		t.Fatalf("Run() error = %v, want it to name the backend the agents selected", err)
	}
	if tracker.Claimed {
		t.Error("a run whose provider is not installed claimed the work item anyway")
	}
}

const (
	codexSessionID     = "codex-session"
	codexResolvedModel = "gpt-5-codex"
)

// scriptedCodexCLI stands in for the installed Codex CLI. It records how each
// invocation was launched and answers with a stream in the provider's own shape:
// the availability probes the run makes first, then the developer's turn.
type scriptedCodexCLI struct {
	mu sync.Mutex
	// absent makes every invocation fail to start, which is what a CLI that is
	// not on the machine does — and is a different failure from one that ran and
	// refused.
	absent   bool
	commands []execution.Command
}

func (c *scriptedCodexCLI) Run(_ context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if c.absent {
		return execution.ProcessResult{}, exec.ErrNotFound
	}
	c.mu.Lock()
	turn := len(c.commands)
	c.commands = append(c.commands, command)
	c.mu.Unlock()

	// The run asks whether the provider is there and logged in before it claims
	// anything, so those two answers come before the developer's turn.
	switch {
	case len(command.Args) == 0:
		return execution.ProcessResult{}, exec.ErrNotFound
	case command.Args[0] == "--version":
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "codex-cli 0.44.0\n"}, nil
	case command.Args[0] == "login":
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "Logged in using ChatGPT\n"}, nil
	}

	if turn == codexDeveloperTurn {
		// The developer's invocation is the one that changes the worktree, so it
		// leaves behind the change the checks and the review are then about.
		if err := os.WriteFile(filepath.Join(command.Dir, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return execution.ProcessResult{}, err
		}
	}
	for _, line := range codexStream(orchestratortest.WithVerification("implemented the work item")) {
		if observer != nil {
			observer(execution.Output{Stream: execution.StreamStdout, Text: line})
		}
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

// The two availability probes come first, so the developer's turn is the third
// invocation this double is asked for.
const codexDeveloperTurn = 2

// developerInvocation is the developer's launch, and a failure naming what
// actually happened when the run did not make it.
func (c *scriptedCodexCLI) developerInvocation(t *testing.T) execution.Command {
	t.Helper()

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.commands) != codexDeveloperTurn+1 {
		t.Fatalf("the provider was launched %d times, want two availability probes and one turn for the developer: %#v",
			len(c.commands), c.commands)
	}
	return c.commands[codexDeveloperTurn]
}

func codexStream(message string) []string {
	configured, err := json.Marshal(map[string]any{
		"id":  "0",
		"msg": map[string]any{"type": "session_configured", "session_id": codexSessionID, "model": codexResolvedModel},
	})
	if err != nil {
		panic(err)
	}
	complete, err := json.Marshal(map[string]any{
		"id":  "1",
		"msg": map[string]any{"type": "task_complete", "last_agent_message": message},
	})
	if err != nil {
		panic(err)
	}
	return []string{string(configured), string(complete)}
}

func codexProfileOf(t *testing.T, args []string) string {
	t.Helper()

	for index, arg := range args {
		if arg != "--config" || index+1 >= len(args) {
			continue
		}
		if selected, found := strings.CutPrefix(args[index+1], "default_permissions="); found {
			var profile string
			if err := json.Unmarshal([]byte(selected), &profile); err != nil {
				t.Fatalf("default_permissions=%s is not a quoted name: %v", selected, err)
			}
			return profile
		}
	}
	t.Fatalf("no permission profile on the command line: %#v", args)
	return ""
}
