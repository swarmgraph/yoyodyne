package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const testRunID = "run-0123456789abcdef0123456789abcdef"

// usageRow is the terminal's usage object read back under the names the
// harness's own price reader uses, which is the point of reading it here: a
// backend that wrote the provider's spelling instead would price every cache
// read as a fresh one and nothing on the reading side would notice.
type usageRow struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_input_tokens"`
}

func TestCheckAvailability(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{
		{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: "codex-cli 0.44.0\n"},
		{Status: execution.ProcessSucceeded, ExitCode: 0, Stdout: "Logged in using ChatGPT\n"},
	}}
	availability, err := (Backend{Runner: runner}).CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if !availability.Installed || !availability.Authenticated || availability.Version != "codex-cli 0.44.0" {
		t.Fatalf("CheckAvailability() = %#v", availability)
	}
	if availability.AuthMethod != "chatgpt" || availability.APIProvider != "openai" {
		t.Fatalf("CheckAvailability() credential = %#v", availability)
	}
	if runner.commands[0].Args[0] != "--version" || !reflect.DeepEqual(runner.commands[1].Args, []string{"login", "status"}) {
		t.Fatalf("availability asked %#v", runner.commands)
	}
}

// The login state is the command's exit status rather than its prose, because
// the status is the part of the answer that does not move between versions. A
// sentence this adapter cannot read leaves the credential unnamed instead of
// leaving the account misreported.
func TestCheckAvailabilityReadsTheLoginStateFromTheExitStatus(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name          string
		login         execution.ProcessResult
		authenticated bool
		method        string
	}{
		{
			name:          "an api key",
			login:         execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "Logged in using an API key\n"},
			authenticated: true,
			method:        "api-key",
		},
		{
			name:  "nobody logged in",
			login: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stdout: "Not logged in\n"},
		},
		{
			name:          "a sentence this version does not recognize",
			login:         execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "Authenticated somehow\n"},
			authenticated: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{results: []execution.ProcessResult{
				{Status: execution.ProcessSucceeded, Stdout: "codex-cli 0.44.0\n"},
				test.login,
			}}
			availability, err := (Backend{Runner: runner}).CheckAvailability(context.Background())
			if err != nil {
				t.Fatalf("CheckAvailability() error = %v", err)
			}
			if availability.Authenticated != test.authenticated || availability.AuthMethod != test.method {
				t.Fatalf("CheckAvailability() = %#v, want authenticated %t by %q", availability, test.authenticated, test.method)
			}
		})
	}
}

// An executable that is not on PATH is the one case that reads as not
// installed, and it says which PATH it was looked for on: a scheduler started by
// launchd searches one the operator's shell does not have.
func TestCheckAvailabilityMissingCLI(t *testing.T) {
	t.Setenv("PATH", "/nowhere/bin:/also/nowhere")

	availability, err := (Backend{Runner: &fakeRunner{errors: []error{exec.ErrNotFound}}}).CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if availability.Installed || availability.Missing != "codex was not found on PATH /nowhere/bin:/also/nowhere" {
		t.Fatalf("CheckAvailability() = %#v", availability)
	}
}

// A version check that ran and did not answer is not a missing executable. It is
// an error saying how it went -- out of time and after how long, stopped with
// whatever asked for it, or exited with a status and what it wrote to stderr --
// and never Installed false, which every caller renders as "not installed". The
// cancelled case is factory-flow-pm's first pass, refused on 2026-09-27 as a
// backend that was not installed because the scheduler was stopped under it.
func TestCheckAvailabilitySaysWhatAVersionCheckThatDidNotAnswerCameTo(t *testing.T) {
	t.Parallel()

	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name      string
		ctx       context.Context
		result    execution.ProcessResult
		want      string
		cancelled bool
	}{
		{
			name:   "timed out",
			ctx:    context.Background(),
			result: execution.ProcessResult{Status: execution.ProcessTimedOut, ExitCode: -1},
			want:   "`codex --version` did not answer and timed out after 10s",
		},
		{
			name:   "exited nonzero",
			ctx:    context.Background(),
			result: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 2, Stderr: "error: this install\nis damaged\n"},
			want:   "`codex --version` exited with status 2: error: this install is damaged",
		},
		{
			name:   "exited nonzero silently",
			ctx:    context.Background(),
			result: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
			want:   "`codex --version` exited with status 1 and wrote nothing to stderr",
		},
		{
			name:      "stopped with its caller",
			ctx:       stopped,
			result:    execution.ProcessResult{Status: execution.ProcessCancelled, ExitCode: -1},
			want:      "`codex --version` was stopped before it answered, because what asked for it was stopped: context canceled",
			cancelled: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			availability, err := (Backend{Runner: &fakeRunner{results: []execution.ProcessResult{test.result}}}).CheckAvailability(test.ctx)
			if err == nil {
				t.Fatalf("CheckAvailability() = %#v, want an error saying what the check came to", availability)
			}
			if !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "not installed") {
				t.Fatalf("CheckAvailability() error = %q, want it to say %q", err, test.want)
			}
			if errors.Is(err, context.Canceled) != test.cancelled {
				t.Fatalf("CheckAvailability() error = %v carries the cancellation: %t, want %t", err, !test.cancelled, test.cancelled)
			}
		})
	}
}

// A diagnosis asks one pooled account whether it is authenticated, so both
// questions have to be asked in that account's own provider home. Asking where
// the machine happens to be signed in would report an account as healthy on
// somebody else's login.
func TestCheckAvailabilityAsksTheHomeThisValueWasBuiltFor(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{
		{Status: execution.ProcessSucceeded, Stdout: "codex-cli 0.44.0\n"},
		{Status: execution.ProcessSucceeded, Stdout: "Logged in using ChatGPT\n"},
	}}
	if _, err := (Backend{Runner: runner, ConfigDir: "/homes/two"}).CheckAvailability(context.Background()); err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	for _, command := range runner.commands {
		if !hasEnvironment(command.Env, ProviderHomeVariable+"=/homes/two") {
			t.Fatalf("%v was asked outside the account's own provider home", command.Args)
		}
	}

	// And naming no home asks where the machine is signed in, which is what a
	// single-account installation has always done and what the account plumbing
	// must not cost it.
	plain := &fakeRunner{results: []execution.ProcessResult{
		{Status: execution.ProcessSucceeded, Stdout: "codex-cli 0.44.0\n"},
		{Status: execution.ProcessSucceeded, Stdout: "Logged in using ChatGPT\n"},
	}}
	if _, err := (Backend{Runner: plain}).CheckAvailability(context.Background()); err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if hasEnvironmentName(plain.commands[0].Env, ProviderHomeVariable) {
		t.Fatalf("an installation naming no account was pointed at a provider home: %v", plain.commands[0].Env)
	}
}

// The environment an invocation is made in is built from the allowlist rather
// than inherited, so a Slack token exported where the harness would inherit it
// -- a shell profile -- never reaches the provider or anything it starts. What
// does reach it is what it needs to run, and the provider's own settings.
func TestAnInvocationIsGivenAnExplicitEnvironmentWithoutTheSlackTokens(t *testing.T) {
	// t.Setenv is this process's environment, so this cannot run in parallel.
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-exported-in-the-parent")
	t.Setenv("SLACK_APP_TOKEN", "xapp-exported-in-the-parent")
	t.Setenv("OPENAI_API_KEY", "sk-exported-in-the-parent")
	t.Setenv("OPENAI_BASE_URL", "https://proxy.example")

	runner := &fakeRunner{results: []execution.ProcessResult{{
		Status: execution.ProcessSucceeded,
		Stdout: lines(`{"id":"0","msg":{"type":"session_configured","session_id":"session-1"}}`,
			`{"id":"1","msg":{"type":"task_complete","last_agent_message":"done"}}`),
	}}}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: t.TempDir(),
		Prompt:           "implement the task",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	environment := runner.commands[0].Env
	for _, name := range []string{"SLACK_BOT_TOKEN", "SLACK_APP_TOKEN", "OPENAI_API_KEY"} {
		if hasEnvironmentName(environment, name) {
			t.Errorf("the invocation's environment carries %s, which was exported in the parent and must not reach it: %v", name, environment)
		}
	}
	for _, name := range []string{"PATH", "HOME"} {
		if !hasEnvironmentName(environment, name) {
			t.Errorf("the invocation's environment does not carry %s, without which the provider cannot run: %v", name, environment)
		}
	}
	if !hasEnvironment(environment, "OPENAI_BASE_URL=https://proxy.example") {
		t.Errorf("the invocation's environment does not carry the provider's own setting: %v", environment)
	}
	// And it says which role it was made for, which is what lets a verb that
	// records a person's decision refuse a shell this agent opens.
	if role, launched := execution.LaunchedForRole(environment); !launched || role != domain.RoleDeveloper {
		t.Errorf("the invocation's environment does not mark it as launched for the developer: %v", environment)
	}
}

func TestRunNormalizesTheProviderStream(t *testing.T) {
	t.Parallel()

	stream := lines(
		`{"id":"0","msg":{"type":"session_configured","session_id":"session-1","model":"gpt-6.1-sol"}}`,
		`{"id":"1","msg":{"type":"task_started"}}`,
		`{"id":"2","msg":{"type":"agent_message_delta","delta":"wor"}}`,
		`{"id":"3","msg":{"type":"agent_message","message":"working"}}`,
		`{"id":"4","msg":{"type":"exec_command_begin","call_id":"call-1","command":["bash","-lc","cat secrets.txt"],"cwd":"/worktree"}}`,
		`{"id":"5","msg":{"type":"exec_command_end","call_id":"call-1","exit_code":0,"stdout":"contents of the file","stderr":""}}`,
		`{"id":"6","msg":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":7}}}}`,
		`{"id":"7","msg":{"type":"task_complete","last_agent_message":"done"}}`,
	)
	runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}}
	var events []execution.Event
	result, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement the task",
		EventSink: func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.IsError || result.FinalText != "done" || result.SessionID != "session-1" || result.ResolvedModel != "gpt-6.1-sol" {
		t.Fatalf("Run() result = %#v", result)
	}
	if result.Backend != domain.BackendCodex {
		t.Fatalf("Run() recorded backend %q", result.Backend)
	}
	// A record has to be able to tell two harness builds reading one provider
	// apart, which is what the adapter version beside the provider's name is for.
	if result.AdapterVersion != backendapi.CodexAdapterVersion {
		t.Fatalf("Run() recorded adapter version %q, want %q", result.AdapterVersion, backendapi.CodexAdapterVersion)
	}
	wantTypes := []execution.EventType{
		execution.EventRunStarted,
		execution.EventProcessOutput, // task_started
		execution.EventAgentMessage,
		execution.EventCommandStarted,
		execution.EventCommandCompleted,
		execution.EventProcessOutput, // token_count
		execution.EventRunCompleted,
	}
	gotTypes := make([]execution.EventType, 0, len(events))
	for _, event := range events {
		gotTypes = append(gotTypes, event.Type)
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("event types = %#v, want %#v", gotTypes, wantTypes)
	}
	// The delta is dropped rather than recorded: it is the same text again in
	// pieces, and a log holding both says nothing extra while being harder to
	// read.
	for _, event := range events {
		if strings.Contains(string(event.Payload), `"wor"`) {
			t.Fatalf("a streamed fragment reached the event log: %s", event.Payload)
		}
	}
	// A shell line and its output are provider-authored text that may quote
	// anything in the worktree, so the record keeps their size and not them.
	for _, event := range events {
		payload := string(event.Payload)
		if strings.Contains(payload, "secrets.txt") || strings.Contains(payload, "contents of the file") {
			t.Fatalf("a command payload persisted the command or its output: %s", payload)
		}
	}
	if runner.prompts[0] != "implement the task" {
		t.Fatalf("prompt = %q", runner.prompts[0])
	}
	wantArgs := []string{"exec", "--sandbox", sandboxWorkspaceWrite, "--config", `model_reasoning_effort="low"`,
		"--config", "skills.include_instructions=false",
		"--disable", "skill_search", "--disable", "skill_mcp_dependency_install",
		"--disable", "plugins", "--disable", "remote_plugin", "--disable", "apps",
		"--config", `approval_policy="never"`,
		"--config", "sandbox_workspace_write.writable_roots=[]",
		"--config", "sandbox_workspace_write.network_access=false",
		"--cd", "/worktree", "--json", "--skip-git-repo-check", "--model", "gpt-6.1-sol", "-"}
	if !reflect.DeepEqual(runner.commands[0].Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.commands[0].Args, wantArgs)
	}
	if runner.commands[0].Dir != "/worktree" {
		t.Fatalf("the invocation ran in %q", runner.commands[0].Dir)
	}
}

// Codex reports what an invocation read and wrote and never what it cost. The
// two are separate facts: an invocation nobody priced and one priced at nothing
// are opposite answers to anything adding costs up, so the result says the cost
// was never reported rather than saying it was zero.
func TestATerminalCarriesTokensAndNoPrice(t *testing.T) {
	t.Parallel()

	result, events := runStream(t, domain.RoleDeveloper, lines(
		`{"id":"0","msg":{"type":"session_configured","session_id":"session-1","model":"gpt-6.1-sol"}}`,
		`{"id":"1","msg":{"type":"token_count","input_tokens":11,"cached_input_tokens":5,"output_tokens":3}}`,
		`{"id":"2","msg":{"type":"task_complete","last_agent_message":"done"}}`,
	))
	if result.CostReported || result.CostUSD != 0 {
		t.Fatalf("Run() priced an invocation the provider never priced: %#v", result)
	}
	terminal := events[len(events)-1]
	var payload struct {
		Role         string    `json:"role"`
		Usage        *usageRow `json:"usage"`
		TotalCostUSD *float64  `json:"total_cost_usd"`
	}
	if err := json.Unmarshal(terminal.Payload, &payload); err != nil {
		t.Fatalf("decode terminal payload: %v", err)
	}
	// The role is what makes the tokens beside it attributable: a run's log holds
	// the developer's invocations and the reviewer's, and where a terminal sits in
	// it is a fact about the order the harness happened to do things in.
	if payload.Role != string(domain.RoleDeveloper) {
		t.Fatalf("terminal role = %q, want the role the invocation was made as", payload.Role)
	}
	if payload.TotalCostUSD != nil {
		t.Fatalf("terminal carries a price the provider never stated: %v", *payload.TotalCostUSD)
	}
	// Codex calls its cached reads `cached_input_tokens`. Renaming it to the name
	// the harness's price reader looks for is what stops a run counting every
	// cache read as a fresh one.
	if payload.Usage == nil || payload.Usage.InputTokens != 11 || payload.Usage.OutputTokens != 3 || payload.Usage.CacheReadTokens != 5 {
		t.Fatalf("terminal usage = %+v, want the provider's counts under the names the price reader uses", payload.Usage)
	}
}

// A terminal carrying no token counts leaves the usage object out rather than
// writing an empty one: an invocation nobody has a measurement for and one
// measured at nothing are different facts.
func TestATerminalWithNoCountsCarriesNoUsage(t *testing.T) {
	t.Parallel()

	_, events := runStream(t, domain.RoleDeveloper, lines(
		`{"id":"0","msg":{"type":"session_configured","session_id":"session-1"}}`,
		`{"id":"1","msg":{"type":"task_complete","last_agent_message":"done"}}`,
	))
	if payload := string(events[len(events)-1].Payload); strings.Contains(payload, `"usage"`) {
		t.Fatalf("terminal payload = %s, want no usage object at all", payload)
	}
}

// Every known role maps to its native sandbox; unknown roles never inherit one.
func TestTheSandboxIsWhatTheRolesPostureRequires(t *testing.T) {
	t.Parallel()
	for _, role := range domain.Roles() {
		want := sandboxReadOnly
		if role == domain.RoleDeveloper {
			want = sandboxWorkspaceWrite
		}
		got, err := sandboxFor(role)
		if err != nil || got != want {
			t.Errorf("sandboxFor(%q) = (%q, %v), want %q", role, got, err, want)
		}
	}
	if got, err := sandboxFor("unknown"); err == nil || got != "" {
		t.Fatalf("unknown role = (%q, %v), want refusal", got, err)
	}
}

// A role or a policy this adapter cannot hold is refused before the provider is
// ever started, which is the same claim the configuration makes when it refuses
// the combination before work is assigned.
func TestRunRefusesRolesAndPoliciesItCannotHold(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		request backendapi.RunRequest
		wanted  string
	}{
		{
			// Which roles this backend serves is the registry's to say and the
			// configuration's to refuse, before any work is assigned; what this
			// adapter refuses is a role it could not assemble an invocation for at
			// all, because the only sandbox left to default to would be the
			// developer's.
			name:    "a name that is not a role at all",
			request: backendapi.RunRequest{Model: "gpt-6.1-sol", Role: "security-reviewer"},
			wanted:  `does not support role "security-reviewer"`,
		},
		{
			// Codex has no per-tool control, so a caller that asked for a narrower
			// set than the sandbox gives would otherwise get a wider one and be
			// told nothing.
			name:    "a tool list this provider cannot scope",
			request: backendapi.RunRequest{Model: "gpt-6.1-sol", Role: domain.RoleDeveloper, AllowedTools: []string{"Read"}},
			wanted:  "cannot be granted a tool list",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{}
			request := test.request
			request.RunID = testRunID
			request.WorkingDirectory = "/worktree"
			request.Prompt = "do the work"
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), request); err == nil || !strings.Contains(err.Error(), test.wanted) {
				t.Fatalf("Run() error = %v, want it to contain %q", err, test.wanted)
			}
			if len(runner.commands) != 0 {
				t.Fatalf("the provider was started for a request that should have been refused: %#v", runner.commands)
			}
		})
	}
}

// A resume must receive the same read-only policy as a new invocation, even
// when the saved provider session was created with broader permissions.
func TestRunReadOnlyRolesOnFreshAndResumedInvocations(t *testing.T) {
	t.Parallel()
	for _, role := range domain.Roles() {
		if backendapi.PostureFor(role) != backendapi.PostureReadOnly {
			continue
		}
		for _, session := range []string{"", "existing-session"} {
			t.Run(string(role)+"/"+session, func(t *testing.T) {
				t.Parallel()
				runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"ok"}}`)}}}
				_, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol", RunID: testRunID, Role: role, WorkingDirectory: t.TempDir(), Prompt: "inspect and advise", SessionID: session})
				if err != nil {
					t.Fatal(err)
				}
				command := runner.commands[0]
				if _, err := os.Stat(command.Dir); !os.IsNotExist(err) {
					t.Fatalf("launch directory left behind: %q (%v)", command.Dir, err)
				}
				args := command.Args
				if got := sandboxArgument(t, args); got != sandboxReadOnly {
					t.Fatalf("sandbox = %q, want read-only", got)
				}
				joined := strings.Join(args, " ")
				for _, required := range []string{"--ignore-user-config", `approval_policy="never"`, `web_search="disabled"`, "agents.enabled=false", "orchestrator.mcp.enabled=false"} {
					if !strings.Contains(joined, required) {
						t.Errorf("args %q missing %q", args, required)
					}
				}
				if session != "" && strings.Index(joined, `approval_policy="never"`) > strings.Index(joined, "resume") {
					t.Fatal("read-only policy appears after resume")
				}
			})
		}
	}
}

// Resuming continues the provider's own session. It is an acceleration and never
// the record: what the harness knows about this work is in its own durable
// state, so a session the provider has forgotten costs context rather than work.
//
// The sandbox goes to `exec`, ahead of `resume`, because `exec resume` does not
// take one: the CLI refuses `--sandbox` after `resume` before anything starts.
// The options `exec resume` does list go after it. The sequence this replaced
// put the sandbox after `resume`, and this test asserted it, so every resumed
// session failed on its arguments while the suite stayed green.
func TestRunResumesTheProvidersSession(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{{
		Status: execution.ProcessSucceeded,
		Stdout: lines(`{"id":"0","msg":{"type":"session_configured","session_id":"session-1"}}`,
			`{"id":"1","msg":{"type":"task_complete","last_agent_message":"done"}}`),
	}}}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "carry on",
		SessionID:        "session-1",
		Model:            "gpt-6.1-sol",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantArgs := []string{"exec", "--sandbox", sandboxWorkspaceWrite, "--config", `model_reasoning_effort="low"`,
		"--config", "skills.include_instructions=false",
		"--disable", "skill_search", "--disable", "skill_mcp_dependency_install",
		"--disable", "plugins", "--disable", "remote_plugin", "--disable", "apps",
		"--config", `approval_policy="never"`,
		"--config", "sandbox_workspace_write.writable_roots=[]",
		"--config", "sandbox_workspace_write.network_access=false",
		"--cd", "/worktree", "resume", "session-1", "--json", "--skip-git-repo-check", "--model", "gpt-6.1-sol", "-"}
	if !reflect.DeepEqual(runner.commands[0].Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", runner.commands[0].Args, wantArgs)
	}
}

// The account an invocation is made under is the request's, falling back to the
// one this backend value was built for. Beside it the invocation carries the Go
// build cache pointed somewhere the run may write, because a developer's first
// act is to execute the project's checks and the default cache is under a home
// the run's sandbox does not grant.
func TestAnInvocationIsMadeUnderTheAccountItWasGiven(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		onValue   string
		onRequest string
		want      string
	}{
		{name: "the request's account", onValue: "/homes/one", onRequest: "/homes/two", want: "/homes/two"},
		{name: "the value's account where the request names none", onValue: "/homes/one", want: "/homes/one"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// The cache lives in the repository's Git directory, so the working
			// directory has to be one: a path in no repository has nowhere to
			// put a cache and is left alone, which is not what this asserts.
			worktree := t.TempDir()
			if err := os.MkdirAll(filepath.Join(worktree, ".git"), 0o755); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			runner := &fakeRunner{results: []execution.ProcessResult{{
				Status: execution.ProcessSucceeded,
				Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"done"}}`),
			}}}
			if _, err := (Backend{Runner: runner, Clock: fixedClock{}, ConfigDir: test.onValue}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
				RunID:            testRunID,
				Role:             domain.RoleDeveloper,
				WorkingDirectory: worktree,
				Prompt:           "implement",
				AccountConfigDir: test.onRequest,
			}); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if !hasEnvironment(runner.commands[0].Env, ProviderHomeVariable+"="+test.want) {
				t.Fatalf("the invocation was not made in %q: %v", test.want, runner.commands[0].Env)
			}
			if !hasEnvironmentName(runner.commands[0].Env, "GOCACHE") {
				t.Fatalf("the invocation carried no build cache the run may write: %v", runner.commands[0].Env)
			}
		})
	}
}

// Codex has no separate channel for a system prompt, so the role's contract is
// prepended to the prompt rather than dropped: a role invoked without the
// contract it was given is a role doing some other job.
func TestTheRolesContractReachesTheProvider(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{{
		Status: execution.ProcessSucceeded,
		Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"done"}}`),
	}}}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement the task",
		SystemPrompt:     "you are the developer",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if runner.prompts[0] != "you are the developer\n\nimplement the task" {
		t.Fatalf("prompt = %q, want the contract in front of the work", runner.prompts[0])
	}
}

// An invocation the provider ended badly is a failure carrying the provider's
// own words, and what kind of failure it was is the dialect's answer rather than
// this adapter's.
func TestRunRecordsAProviderError(t *testing.T) {
	t.Parallel()

	result, events := runStream(t, domain.RoleDeveloper, lines(
		`{"id":"0","msg":{"type":"session_configured","session_id":"session-1"}}`,
		`{"id":"1","msg":{"type":"error","message":"stream failed: 500 internal server error"}}`,
	))
	if !result.IsError || result.StopReason != eventError {
		t.Fatalf("Run() result = %#v", result)
	}
	if result.ServerOverload == nil {
		t.Fatalf("a transiently unavailable server was not read as one: %#v", result)
	}
	if events[len(events)-1].Type != execution.EventRunFailed {
		t.Fatalf("terminal event = %q, want a failure", events[len(events)-1].Type)
	}
}

// A provider that stopped without saying how the invocation ended has produced
// no outcome, and the run fails with exactly that rather than with an answer
// nobody gave.
func TestRunFailsWhenNoTerminalArrives(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{{
		Status: execution.ProcessSucceeded,
		Stdout: lines(`{"id":"0","msg":{"type":"session_configured","session_id":"session-1"}}`),
	}}}
	_, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
	})
	if err == nil || !strings.Contains(err.Error(), "without a terminal event") {
		t.Fatalf("Run() error = %v", err)
	}
}

// A stream in a vocabulary this adapter does not speak is named as that — the
// CLI's version and the first event it did not recognize — rather than as a
// terminal that never arrived, which reads as a provider that stopped mid-run.
// The lines below are written by hand: the first two are the shape a recorded
// codex-cli 0.159.2 stream opens with, and the third is a turn.* event no
// recorded stream has shown, which this parser does not take for an ending.
func TestAStreamThisAdapterCannotReadNamesTheVersionAndTheFirstUnknownEvent(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{
		{
			Status: execution.ProcessSucceeded,
			Stdout: lines(
				`{"type":"thread.started","thread_id":"thread-1"}`,
				`{"type":"turn.started"}`,
				`{"type":"turn.interrupted"}`,
			),
		},
		{Status: execution.ProcessSucceeded, Stdout: "codex-cli 9.9.9\n"},
	}}
	_, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
	})
	if err == nil {
		t.Fatal("Run() error = nil, want the stream named as unreadable")
	}
	for _, want := range []string{"codex-cli 9.9.9", `"turn.interrupted"`, "cannot read"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Run() error = %v, want it to name %s", err, want)
		}
	}
	if strings.Contains(err.Error(), "without a terminal event") {
		t.Fatalf("Run() error = %v, reported as a missing terminal", err)
	}
	if got := runner.commands[1].Args; len(got) != 1 || got[0] != "--version" {
		t.Fatalf("second command = %#v, want the version asked for", got)
	}
}

// A CLI that will not say its version does not stop the unreadable stream being
// reported; the version is named as unknown.
func TestAnUnreadableStreamIsReportedWhenTheVersionIsNot(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{
		{Status: execution.ProcessSucceeded, Stdout: lines(`{"type":"turn.interrupted"}`)},
		{Status: execution.ProcessFailed, ExitCode: 2},
	}}
	_, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
	})
	if err == nil || !strings.Contains(err.Error(), "(version unknown)") || !strings.Contains(err.Error(), `"turn.interrupted"`) {
		t.Fatalf("Run() error = %v", err)
	}
}

// A line this adapter cannot read is recorded rather than fatal: Codex writes to
// standard output and nothing guarantees every line there is one of its events,
// so a banner must not fail a run whose work was fine. Nothing is lost by being
// lenient, because a stream that says nothing readable still produces no
// terminal and still fails.
func TestAnUnreadableLineIsRecordedRatherThanFatal(t *testing.T) {
	t.Parallel()

	result, events := runStream(t, domain.RoleDeveloper, lines(
		`Reading prompt from stdin...`,
		`{"id":"0","msg":{"type":"task_complete","last_agent_message":"done"}}`,
	))
	if result.IsError || result.FinalText != "done" {
		t.Fatalf("Run() result = %#v", result)
	}
	if events[0].Type != execution.EventProcessOutput || !strings.Contains(string(events[0].Payload), "Reading prompt") {
		t.Fatalf("the unreadable line was not recorded: %s", events[0].Payload)
	}
}

// loginRefusedByCodex is a login refusal in this CLI's own shape: the title and
// the remedy it names. No recorded Codex process carries one; it is what a CLI
// that refuses before writing any event has to say on one of its two plain
// channels.
const loginRefusedByCodex = "Not logged in. Run `codex login` to authenticate."

// runProcessFailure runs a process that wrote the given stream, said the given
// things on stderr, and exited 1, and returns what the adapter made of it
// beside every event it recorded.
func runProcessFailure(t *testing.T, stream, stderr string) (backendapi.RunResult, []execution.Event) {
	t.Helper()
	var events []execution.Event
	result, err := (Backend{
		Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stdout: stream, Stderr: stderr}}},
		Clock:  fixedClock{},
	}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
		EventSink: func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return result, events
}

// A CLI that refuses an expired login before it writes any event hands the
// dialect no terminal, and until yoyodyne-ifd.400 this adapter ended that
// attempt as a process failure nobody classified — by decision, pending a
// recorded occurrence — which is the relaunch-and-block shape yoyodyne-ifd.377
// closed for Claude Code, replayed on this provider. The refusal is read off
// whichever plain channel carried it, stderr or stdout, as the same wait the
// terminal form earns, with the channel recorded beside it.
func TestRunClassifiesARefusalMadeAsProseBeforeAnyEvent(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		stream  string
		stderr  string
		cause   domain.ProviderOutageCause
		channel domain.ProviderChannel
		words   string
	}{
		{name: "a login refused on stderr", stderr: loginRefusedByCodex + "\n", cause: domain.ProviderUnauthenticated, channel: domain.ProviderChannelStderr, words: "Not logged in"},
		{name: "a login refused as plain text on stdout", stream: loginRefusedByCodex + "\n", cause: domain.ProviderUnauthenticated, channel: domain.ProviderChannelStdout, words: "Not logged in"},
		{
			// A banner ahead of the refusal is more prose, and the refusal has to
			// be found behind it.
			name:    "a login refused behind a banner on stdout",
			stream:  "Reading prompt from stdin...\n" + loginRefusedByCodex + "\n",
			cause:   domain.ProviderUnauthenticated,
			channel: domain.ProviderChannelStdout,
			words:   "Not logged in",
		},
		{name: "nothing answering, said on stderr", stderr: "error sending request: dns error: failed to lookup address information\n", cause: domain.ProviderUnreachable, channel: domain.ProviderChannelStderr, words: "dns error"},
		{name: "nothing answering, said as plain text on stdout", stream: "error sending request: dns error: failed to lookup address information\n", cause: domain.ProviderUnreachable, channel: domain.ProviderChannelStdout, words: "dns error"},
		{
			// A CLI that said it on both leaves one channel on the record, and it
			// is stderr.
			name:    "a login refused on both channels names stderr",
			stream:  loginRefusedByCodex + "\n",
			stderr:  loginRefusedByCodex + "\n",
			cause:   domain.ProviderUnauthenticated,
			channel: domain.ProviderChannelStderr,
			words:   "Not logged in",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result, events := runProcessFailure(t, testCase.stream, testCase.stderr)
			if result.ProviderOutage == nil {
				t.Fatalf("Run() reported no provider outage: %#v", result)
			}
			if result.ProviderOutage.Cause != testCase.cause {
				t.Fatalf("outage cause = %q, want %q", result.ProviderOutage.Cause, testCase.cause)
			}
			if result.ProviderOutage.Channel != testCase.channel {
				t.Fatalf("outage channel = %q, want %q: the record has to say where the provider said it", result.ProviderOutage.Channel, testCase.channel)
			}
			if !strings.Contains(result.ProviderOutage.Detail, testCase.words) {
				t.Fatalf("outage detail = %q, want the CLI's own words %q", result.ProviderOutage.Detail, testCase.words)
			}
			// The process still ended the way it ended: the exit is the stop reason
			// and the failure stands beside the wait.
			if !result.IsError || result.StopReason != "process_exit_1" {
				t.Fatalf("Run() = IsError %t, StopReason %q, want the process failure kept beside the wait", result.IsError, result.StopReason)
			}
			// A wait that spends nothing is never also a death to relaunch on.
			if result.TransientFailure != nil || result.ServerOverload != nil || result.UsageLimit != nil {
				t.Fatalf("a refusal read off prose also became something to relaunch or wait on a clock for: %#v", result)
			}
			// What the process said is in the record whether or not the dialect
			// read anything off it, and the record says which stream it was on.
			var recorded bool
			for _, event := range events {
				if event.Type == execution.EventProcessOutput && strings.Contains(string(event.Payload), `"stream":"`+string(testCase.channel)+`"`) && strings.Contains(string(event.Payload), testCase.words) {
					recorded = true
				}
			}
			if !recorded {
				t.Fatalf("the plain channel was read and not recorded: %#v", events)
			}
		})
	}
}

// Prose is read narrowly and only when nothing else answered. A process that
// died with a terminal has been answered by the terminal, whatever its
// diagnostics say; one whose prose says something the dialect does not read for
// — a limit, a crash, a banner — stays the process failure it always was
// rather than becoming a wait nobody can justify; and a refusal that arrives
// after an event is a stream that broke rather than a CLI that refused before
// writing one.
func TestRunReadsProseOnlyForAProcessNothingElseAnswered(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		stream string
		stderr string
		// wantOutage is the outage the terminal earns, and nil for a process
		// failure that stays unclassified.
		wantOutage *backendapi.ProviderOutage
	}{
		{name: "a limit said on stderr is not a wait", stderr: "You've hit your usage limit\n"},
		{name: "a crash that mentions nothing this dialect reads", stderr: "thread 'main' panicked at src/main.rs:1:1\n"},
		{name: "a banner on stdout is not a wait", stream: "Reading prompt from stdin...\n"},
		{
			name:   "a refusal after an event is not read",
			stream: `{"id":"0","msg":{"type":"session_configured","session_id":"session-1","model":"gpt-6.1-sol"}}` + "\n" + loginRefusedByCodex + "\n",
		},
		{
			// The terminal is the provider's account of the ending and prose does
			// not second-guess it.
			name:       "a terminal the provider wrote is not overridden by stderr",
			stream:     lines(`{"id":"0","msg":{"type":"error","message":"400 your request was rejected"}}`),
			stderr:     loginRefusedByCodex + "\n",
			wantOutage: nil,
		},
		{
			// A terminal outage still says which channel it came on.
			name:       "a terminal outage names the envelope",
			stream:     lines(`{"id":"0","msg":{"type":"error","message":"401 Unauthorized: check your credentials"}}`),
			wantOutage: &backendapi.ProviderOutage{Cause: domain.ProviderUnauthenticated, Detail: "error: 401 Unauthorized: check your credentials", Channel: domain.ProviderChannelEnvelope},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result, _ := runProcessFailure(t, testCase.stream, testCase.stderr)
			if !result.IsError {
				t.Fatalf("Run() lost the reported failure: %#v", result)
			}
			if !reflect.DeepEqual(result.ProviderOutage, testCase.wantOutage) {
				t.Fatalf("ProviderOutage = %#v, want %#v", result.ProviderOutage, testCase.wantOutage)
			}
		})
	}
}

// A line the runner had to cut is not an envelope any more, and failing the
// invocation over one line the harness could not hold would be a self-inflicted
// death. It is recorded as the anomaly it is, named so a reader can tell it from
// a line the provider wrote badly, and the stream carries on.
func TestACutLineIsRecordedAsAnAnomalyAndTheStreamCarriesOn(t *testing.T) {
	t.Parallel()

	var events []execution.Event
	result, err := (Backend{
		Runner: &cuttingRunner{
			cut:   `{"id":"0","msg":{"type":"agent_message","message":"a very long`,
			after: `{"id":"1","msg":{"type":"task_complete","last_agent_message":"done"}}`,
		},
		Clock: fixedClock{},
	}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
		EventSink:        func(event execution.Event) error { events = append(events, event); return nil },
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.IsError || result.FinalText != "done" {
		t.Fatalf("Run() result = %#v, want the invocation the rest of the stream finished", result)
	}
	if payload := string(events[0].Payload); !strings.Contains(payload, truncatedStreamLine) {
		t.Fatalf("the cut line was recorded as ordinary output: %s", payload)
	}
}

// The provider keeps writing after the terminal. Those events are recorded so
// their payload stays diagnosable, and none of them may replace the outcome that
// was already decided.
func TestNothingAfterTheTerminalReplacesTheOutcome(t *testing.T) {
	t.Parallel()

	result, events := runStream(t, domain.RoleDeveloper, lines(
		`{"id":"0","msg":{"type":"task_complete","last_agent_message":"done"}}`,
		`{"id":"1","msg":{"type":"error","message":"something afterwards"}}`,
		`{"id":"2","msg":{"type":"shutdown_complete"}}`,
	))
	if result.IsError || result.FinalText != "done" || result.StopReason != eventTaskComplete {
		t.Fatalf("Run() result = %#v, want the first terminal standing", result)
	}
	if payload := string(events[1].Payload); !strings.Contains(payload, "terminal_after_terminal") {
		t.Fatalf("a second terminal was recorded as ordinary output: %s", payload)
	}
}

// Nothing the harness was told to keep out of a durable record may reach one
// through the provider's own stream.
func TestRunRedactsWhatTheProviderEchoesBack(t *testing.T) {
	t.Parallel()

	var events []execution.Event
	result, err := (Backend{
		Runner: &fakeRunner{results: []execution.ProcessResult{{
			Status: execution.ProcessSucceeded,
			Stdout: lines(`{"id":"0","msg":{"type":"agent_message","message":"the token is sk-secret-value"}}`,
				`{"id":"1","msg":{"type":"task_complete","last_agent_message":"used sk-secret-value"}}`),
		}}},
		Clock: fixedClock{},
	}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
		RedactValues:     []string{"sk-secret-value"},
		EventSink:        func(event execution.Event) error { events = append(events, event); return nil },
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.Contains(result.FinalText, "sk-secret-value") {
		t.Fatalf("the result kept a redacted value: %q", result.FinalText)
	}
	for _, event := range events {
		if strings.Contains(string(event.Payload), "sk-secret-value") {
			t.Fatalf("an event kept a redacted value: %s", event.Payload)
		}
	}
}

// The adapter reads its own description from the same place a configuration is
// validated against it, so the two cannot drift apart.
func TestCapabilitiesAreTheOnesTheHarnessValidatesAgainst(t *testing.T) {
	t.Parallel()

	descriptor, known := backendapi.BuiltInDescriptor(domain.BackendCodex)
	if !known {
		t.Fatal("this build ships no description of the Codex backend")
	}
	if (Backend{}).Capabilities() != descriptor.Capabilities {
		t.Fatalf("Capabilities() = %#v, want %#v", (Backend{}).Capabilities(), descriptor.Capabilities)
	}
	// Codex enforces no schema on what an agent finally says, so the description
	// must not claim it does: a capability nobody has is how a role comes to be
	// given work it cannot do.
	if descriptor.Capabilities.StructuredOutput {
		t.Fatal("the Codex description claims structured output, which this provider does not enforce")
	}
	// And the description says this build can launch it, which is what makes a
	// Codex endpoint runnable rather than expressed and refused.
	if !descriptor.Runnable() || descriptor.AdapterVersion != backendapi.CodexAdapterVersion {
		t.Fatalf("the Codex description = %#v, want the adapter this build ships", descriptor)
	}
}

// A project that declared a provider running on this adapter is a different
// backend from the one that ships here, and every run, conversation, and line of
// spend has to say which one it was.
func TestADeclaredProviderIsRecordedUnderItsOwnName(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: []execution.ProcessResult{{
		Status: execution.ProcessSucceeded,
		Stdout: lines(`{"id":"0","msg":{"type":"task_complete","last_agent_message":"done"}}`),
	}}}
	result, err := (Backend{Runner: runner, Clock: fixedClock{}, Provider: "my-harness", Binary: "my-harness"}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "implement",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Backend != "my-harness" {
		t.Fatalf("Run() recorded backend %q, want the backend the agent named", result.Backend)
	}
	// The provider is the declaration's and the adapter version is this build's:
	// they are separate facts, and a record carrying only the first cannot say
	// which harness code read what the provider said.
	if result.AdapterVersion != backendapi.CodexAdapterVersion {
		t.Fatalf("Run() recorded adapter version %q, want this build's", result.AdapterVersion)
	}
	if runner.commands[0].Name != "my-harness" {
		t.Fatalf("the invocation ran %q, want the executable the declaration named", runner.commands[0].Name)
	}
}

func runStream(t *testing.T, role domain.AgentRole, stream string) (backendapi.RunResult, []execution.Event) {
	t.Helper()

	var events []execution.Event
	result, err := (Backend{
		Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: stream}}},
		Clock:  fixedClock{},
	}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID:            testRunID,
		Role:             role,
		WorkingDirectory: "/worktree",
		Prompt:           "do the work",
		EventSink:        func(event execution.Event) error { events = append(events, event); return nil },
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return result, events
}

func sandboxArgument(t *testing.T, args []string) string {
	t.Helper()

	for index, arg := range args {
		if arg == "--sandbox" && index+1 < len(args) {
			return args[index+1]
		}
	}
	t.Fatalf("no sandbox in %#v", args)
	return ""
}

func hasEnvironment(environment []string, entry string) bool {
	for _, value := range environment {
		if value == entry {
			return true
		}
	}
	return false
}

func hasEnvironmentName(environment []string, name string) bool {
	for _, value := range environment {
		if strings.HasPrefix(value, name+"=") {
			return true
		}
	}
	return false
}

func lines(values ...string) string {
	return strings.Join(values, "\n") + "\n"
}

type fakeRunner struct {
	results  []execution.ProcessResult
	errors   []error
	commands []execution.Command
	prompts  []string
}

func (f *fakeRunner) Run(_ context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	index := len(f.commands)
	f.commands = append(f.commands, command)
	if command.Stdin != nil {
		data, _ := io.ReadAll(command.Stdin)
		f.prompts = append(f.prompts, string(data))
	}
	if index < len(f.errors) && f.errors[index] != nil {
		return execution.ProcessResult{}, f.errors[index]
	}
	if index >= len(f.results) {
		return execution.ProcessResult{}, errors.New("unexpected process call")
	}
	result := f.results[index]
	if observer != nil {
		for _, line := range strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n") {
			if line != "" {
				observer(execution.Output{Stream: execution.StreamStdout, Text: line, Timestamp: time.Now()})
			}
		}
		for _, line := range strings.Split(strings.TrimSuffix(result.Stderr, "\n"), "\n") {
			if line != "" {
				observer(execution.Output{Stream: execution.StreamStderr, Text: line, Timestamp: time.Now()})
			}
		}
	}
	return result, nil
}

// cuttingRunner is a process runner that reports its first line as one it had to
// cut at the per-line bound, which is the one thing the fake above cannot say.
type cuttingRunner struct {
	cut   string
	after string
}

func (c *cuttingRunner) Run(_ context.Context, _ execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	observer(execution.Output{Stream: execution.StreamStdout, Text: c.cut, LineTruncated: true, Timestamp: time.Now()})
	observer(execution.Output{Stream: execution.StreamStdout, Text: c.after, Timestamp: time.Now()})
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time {
	return time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
}

// An effort outside the model catalog is refused, and a request carrying
// one is refused before anything is launched rather than run with the
// level dropped, so no record can say a level was asked of Codex that it never
// received.
func TestRunRefusesAnUnacceptedEffortLevel(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	_, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: "/worktree",
		Prompt:           "go",
		Model:            "gpt-6.1-sol",
		Effort:           "extreme",
	})
	if err == nil || !strings.Contains(err.Error(), "accepted values: low, medium, high, xhigh, max, or ultra") {
		t.Fatalf("Run() error = %v, want the refusal naming accepted levels", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("a refused request launched %d process(es)", len(runner.commands))
	}
}

// Provider failure still removes the isolated directory, and repository paths
// remain absolute after moving the CLI outside the checkout.
func TestReadOnlyLaunchCleanupAndRelativeRepository(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, repository)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{errors: []error{errors.New("provider failed")}}
	_, err = (Backend{Runner: runner}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol", RunID: testRunID, Role: domain.RoleReviewer, WorkingDirectory: relative, Prompt: "review"})
	if err == nil || !strings.Contains(err.Error(), "provider failed") {
		t.Fatalf("error = %v", err)
	}
	command := runner.commands[0]
	if command.Dir == repository {
		t.Fatal("launched in repository")
	}
	if _, err := os.Stat(command.Dir); !os.IsNotExist(err) {
		t.Fatalf("launch directory not removed: %v", err)
	}
	canonical, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runner.prompts[0], canonical) {
		t.Fatalf("prompt lacks canonical repository %q", canonical)
	}
}

func TestReadOnlyLaunchRejectsTemporaryDirectoryInsideRepository(t *testing.T) {
	repository := t.TempDir()
	t.Setenv("TMPDIR", repository)
	_, _, err := prepareReadOnlyLaunch(repository)
	if err == nil {
		t.Fatal("accepted a launch inside the repository")
	}
	entries, readErr := os.ReadDir(repository)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("failed preparation leaked directory: %v, %v", entries, readErr)
	}
}

func TestReadOnlyEnvironmentRetainsAuthWithoutControlChannels(t *testing.T) {
	t.Setenv("CODEX_HOME", "/provider-home")
	t.Setenv("CODEX_CONTROL_SOCKET", "/control")
	t.Setenv("OPENAI_BASE_URL", "http://unexpected")
	t.Setenv("SSH_AUTH_SOCK", "/ssh-agent")
	env := strings.Join(readOnlyEnvironment(""), "\n")
	if !strings.Contains(env, "CODEX_HOME=/provider-home") {
		t.Fatal("provider home lost")
	}
	for _, name := range []string{"CODEX_CONTROL_SOCKET=", "OPENAI_BASE_URL=", "SSH_AUTH_SOCK="} {
		if strings.Contains(env, name) {
			t.Errorf("retained %s", name)
		}
	}
	if env := strings.Join(readOnlyEnvironment("/selected-account"), "\n"); !strings.Contains(env, "CODEX_HOME=/selected-account") || strings.Contains(env, "CODEX_HOME=/provider-home") {
		t.Fatalf("wrong account environment: %s", env)
	}
}

func TestReadOnlyLaunchRejectsSiblingTemporaryDirectoryInSameCheckout(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".git", "nested", "tmp"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMPDIR", filepath.Join(root, "tmp"))
	if _, _, err := prepareReadOnlyLaunch(filepath.Join(root, "nested")); err == nil {
		t.Fatal("accepted temporary directory inside same checkout")
	}
}

func TestReadOnlyLaunchRequiresDirectory(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareReadOnlyLaunch(file); err == nil {
		t.Fatal("accepted file as inspection directory")
	}
}

func TestReadOnlyRelativeProviderHomeKeepsRepositoryResolution(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	runner := &fakeRunner{errors: []error{errors.New("provider failed")}}
	_, _ = (Backend{Runner: runner, ConfigDir: "account-home"}).Run(context.Background(), backendapi.RunRequest{Model: "gpt-6.1-sol", RunID: testRunID, Role: domain.RoleReviewer, WorkingDirectory: repository, Prompt: "review"})
	if len(runner.commands) != 1 {
		t.Fatal("provider did not start")
	}
	canonical, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	want := "CODEX_HOME=" + filepath.Join(canonical, "account-home")
	found := false
	for _, value := range runner.commands[0].Env {
		if value == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("provider environment lost %q", want)
	}
}
