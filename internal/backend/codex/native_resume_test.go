package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const nativeProbeReply = "native sandbox probe passed"

// Match the generous subprocess budget used by the Git and tracker conformance
// tests. Native invocation and legacy shell APIs require a budget; completion
// polling and local HTTP requests need no separate deadline.
const nativeProbeSubprocessBudget = 10 * time.Minute

// The provider is a local scripted Responses server, not a paid model. It asks
// the real CLI to execute one shell command, then ends the turn. The CLI creates
// and restores its own session; neither the rollout nor its policy is faked.
// There is no opt-in flag: an installed CLI whose sandbox cannot run fails this
// check instead of turning absent confinement evidence into a passing suite.
func TestNativeResumeReplacesSavedDirectoryGrants(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skipf("native resume requires an installed Codex CLI: %v", err)
	}
	home := t.TempDir()
	// Codex 0.159.2's macOS sandbox rejects double quotes in writable roots
	// while compiling its Seatbelt profile. Keep those in the argument-encoding
	// tests; native execution exercises supported paths, including spaces.
	oldRepository, oldWorktree := sandboxRepositoryNamed(t, true, "repository with spaces")
	newRepository, newWorktree := sandboxRepositoryNamed(t, true, "repository with spaces")
	oldPaths := nativeProbeDirectories(t, oldRepository, oldWorktree)
	newPaths := nativeProbeDirectories(t, newRepository, newWorktree)
	outside := t.TempDir()
	otherScratch := filepath.Join(filepath.Dir(newPaths[1]), "another-run")
	if err := os.Mkdir(otherScratch, 0o700); err != nil {
		t.Fatal(err)
	}
	model := &sandboxResponses{}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("local scripted Responses server unavailable: %v", err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: model}}
	server.Start()
	defer server.Close()
	runner := &sandboxCLIRunner{home: home, url: server.URL}
	provider := Backend{Binary: binary, Runner: runner}
	request := backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: oldWorktree,
		RepositoryRoot: oldRepository, AccountConfigDir: home, Model: "gpt-6.1-sol",
		Prompt:  "Execute the supplied sandbox probe, then finish.",
		Timeout: nativeProbeSubprocessBudget, IdleTimeout: nativeProbeSubprocessBudget, AfterReplyTimeout: nativeProbeSubprocessBudget,
	}
	denied := []string{outside, otherScratch, filepath.Join(oldRepository, ".git"), filepath.Join(newRepository, ".git")}
	freshProof := nativeProbeReply + ": fresh launch"
	freshDenied := append(denied, append(newPaths, newWorktree)...)
	model.begin(nativeProbeCommand(t, oldWorktree, oldPaths, freshDenied, freshProof))
	fresh := nativeProbeTurn(t, provider, request, freshProof, model)
	if fresh.SessionID == "" {
		t.Fatal("the fresh CLI invocation did not create a resumable session")
	}
	model.requireTurn(t)
	nativeProbeFiles(t, oldWorktree, oldPaths, freshDenied, freshProof)

	request.SessionID = fresh.SessionID
	request.RepositoryRoot, request.WorkingDirectory = newRepository, newWorktree
	resumeProof := nativeProbeReply + ": changed grants"
	resumeDenied := append(denied, append(oldPaths, oldWorktree)...)
	model.begin(nativeProbeCommand(t, newWorktree, newPaths, resumeDenied, resumeProof))
	resumed := nativeProbeTurn(t, provider, request, resumeProof, model)
	if resumed.SessionID != fresh.SessionID {
		t.Fatalf("resume created session %q instead of restoring %q", resumed.SessionID, fresh.SessionID)
	}
	model.requireTurn(t)
	nativeProbeFiles(t, newWorktree, newPaths, resumeDenied, resumeProof)

	// Restore that same previously writable session under the reviewer's native
	// posture. Old and current cache, scratch, worktrees, and unrelated paths
	// must all be read-only; permission retained from either turn fails the test.
	request.Role = domain.RoleReviewer
	allDenied := append(append(denied, oldPaths...), newPaths...)
	allDenied = append(allDenied, oldWorktree, newWorktree)
	reviewProof := nativeProbeReply + ": reviewer resume"
	model.begin(nativeProbeCommand(t, newWorktree, nil, allDenied, reviewProof))
	readOnly := nativeProbeTurn(t, provider, request, reviewProof, model)
	if readOnly.SessionID != fresh.SessionID {
		t.Fatalf("read-only resume created session %q instead of restoring %q", readOnly.SessionID, fresh.SessionID)
	}
	model.requireTurn(t)
	nativeProbeFiles(t, newWorktree, nil, allDenied, reviewProof)
}

func nativeProbeFiles(t *testing.T, worktree string, allowed, denied []string, proof string) {
	t.Helper()
	if len(allowed) != 0 {
		for _, directory := range []string{worktree, allowed[0]} {
			body, err := os.ReadFile(filepath.Join(directory, "allowed"))
			if err != nil || string(body) != proof {
				t.Errorf("probe write in %s: %q, %v; want %q", directory, body, err, proof)
			}
		}
		log, err := os.ReadFile(filepath.Join(allowed[1], "check.log"))
		if err != nil || len(log) == 0 {
			t.Errorf("probe compile log: %q, %v; want a retained Go check log", log, err)
		}
	}
	for _, directory := range denied {
		if _, err := os.Stat(filepath.Join(directory, "forbidden")); !os.IsNotExist(err) {
			t.Errorf("forbidden probe write in %s: %v; want no file", directory, err)
		}
	}
}

func nativeProbeDirectories(t *testing.T, repository, worktree string) []string {
	t.Helper()
	for file, body := range map[string]string{"go.mod": "module sandboxprobe\n\ngo 1.23\n", "probe.go": "package sandboxprobe\n"} {
		if err := os.WriteFile(filepath.Join(worktree, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := execution.PrepareDeveloperDirectories(repository, worktree, testRunID)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func nativeProbeCommand(t *testing.T, worktree string, allowed, denied []string, proof string) []string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	mode, cache, scratch := "read-only", "", ""
	if len(allowed) != 0 {
		mode, cache, scratch = "developer", allowed[0], allowed[1]
	}
	script := `set -eu
mode=$1; worktree=$2; cache=$3; scratch=$4; proof=$5; shift 5
if [ "$mode" = developer ]; then
  test "$(pwd -P)" = "$worktree"
  test "$GOCACHE" = "$cache"
  export GOTMPDIR="$scratch/go-tmp" GOTELEMETRY=off
  mkdir -p "$GOTMPDIR"
  if ! go test ./... > "$scratch/check.log" 2>&1; then
    cat "$scratch/check.log"; exit 21
  fi
  test -s "$scratch/check.log"
  printf '%s' "$proof" > "$cache/allowed"
  printf '%s' "$proof" > "$worktree/allowed"
else
  test "$(pwd -P)" != "$worktree"
fi
for directory do
  if (printf forbidden > "$directory/forbidden") 2>/dev/null; then
    printf 'unexpected write access: %s\n' "$directory"; exit 20
  fi
done
printf '%s\n' "$proof"`
	return append([]string{"sh", "-c", script, "sandbox-probe", mode, physical, cache, scratch, proof}, denied...)
}

func nativeProbeTurn(t *testing.T, provider Backend, request backendapi.RunRequest, proof string, model *sandboxResponses) backendapi.RunResult {
	t.Helper()
	result, err := provider.Run(t.Context(), request)
	process := provider.Runner.(*sandboxCLIRunner).process
	if err != nil || result.IsError || result.Process.Status != execution.ProcessSucceeded {
		t.Fatalf("native CLI turn: %v; status=%s, exit=%d, stop=%q, reply=%q\n%s\n%s",
			err, process.Status, process.ExitCode, result.StopReason, result.FinalText, process.Stdout, process.Stderr)
	}
	// A fake provider's final reply is not execution evidence. Require the CLI's
	// completed command item or its unified-exec tool result to hold this turn's
	// stdout and a successful exit;
	// replaying an earlier turn's successful command cannot satisfy a resume.
	for _, line := range strings.Split(process.Stdout, "\n") {
		var event struct {
			Type string `json:"type"`
			Item struct {
				Type     string `json:"type"`
				Output   string `json:"aggregated_output"`
				ExitCode *int   `json:"exit_code"`
			} `json:"item"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && event.Type == "item.completed" && event.Item.Type == "command_execution" &&
			event.Item.ExitCode != nil && *event.Item.ExitCode == 0 && strings.TrimSpace(event.Item.Output) == proof {
			return result
		}
	}
	var toolResult string
	if model != nil {
		verified, output := model.commandEvidence(proof)
		toolResult = output
		if verified {
			return result
		}
	}
	// Keep checking subsequent native resumes when the CLI saved the session,
	// even if this turn's sandbox refused to execute. The test remains failed.
	t.Errorf("the CLI did not report a successful confinement command; tool result = %q:\n%s\n%s", toolResult, process.Stdout, process.Stderr)
	return result
}

// The fixture selects a local transport and excludes
// default temporary-directory grants. The adapter's cwd, sandbox, approvals,
// and explicit writable roots stay intact.
type sandboxCLIRunner struct {
	home, url string
	runner    execution.ProcessRunner
	process   execution.ProcessResult
}

func (r *sandboxCLIRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	runner := r.runner
	if runner == nil {
		runner = execution.OSProcessRunner{}
	}
	if len(command.Args) == 0 || command.Args[0] != "exec" {
		return runner.Run(ctx, command, observer)
	}
	prefix := []string{"exec", "--disable", "code_mode",
		"--config", `model_provider="sandbox_probe"`,
		"--config", fmt.Sprintf(`model_providers.sandbox_probe={name="sandbox probe",base_url=%q,wire_api="responses",requires_openai_auth=false,supports_websockets=false,request_max_retries=0,stream_max_retries=0}`, r.url),
		// Temporary fixture siblings would otherwise be writable by default.
		"--config", "sandbox_workspace_write.exclude_tmpdir_env_var=true",
		"--config", "sandbox_workspace_write.exclude_slash_tmp=true"}
	command.Args = append(prefix, command.Args[1:]...)
	command.Env = append(execution.ExplicitEnvironment(command.Env), ProviderHomeVariable+"="+r.home)
	process, err := runner.Run(ctx, command, observer)
	// Backend.Run deliberately discards raw stdout after parsing it. Keep the
	// fixture's own runner result for command evidence and launch diagnostics.
	r.process = process
	return process, err
}

func TestNativeProbeReadsCommandEvidenceBeforeAdapterDiscardsRawOutput(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, true)
	runner := &sandboxCLIRunner{home: t.TempDir(), url: "http://127.0.0.1:0", runner: &fakeRunner{
		results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: lines(
			`{"type":"thread.started","thread_id":"native-session"}`,
			`{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"native sandbox probe passed\n","exit_code":0}}`,
			`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`,
		)}},
	}}
	result := nativeProbeTurn(t, Backend{Runner: runner}, backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree,
		RepositoryRoot: repository, Prompt: "probe",
	}, nativeProbeReply, nil)
	if result.Process.Stdout != "" || result.SessionID != "native-session" {
		t.Fatalf("adapter result = %+v, want normalized output and the saved session", result)
	}
}

type sandboxResponses struct {
	mu         sync.Mutex
	command    []string
	calls      int
	turn       int
	finished   bool
	err        error
	unified    bool
	started    bool
	output     string
	lastOutput string
	exitCode   *int
}

func (s *sandboxResponses) begin(command []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.command, s.calls, s.err = command, 0, nil
	s.finished = false
	s.unified, s.started, s.output, s.lastOutput, s.exitCode = false, false, "", "", nil
	s.turn++
}

func (s *sandboxResponses) requireTurn(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil || s.calls < 2 || !s.finished {
		t.Fatalf("scripted Responses turn: requests = %d, error = %v", s.calls, s.err)
	}
}

func (s *sandboxResponses) commandEvidence(proof string) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err == nil && s.finished && s.exitCode != nil && *s.exitCode == 0 && strings.TrimSpace(s.output) == proof, s.lastOutput
}

func (s *sandboxResponses) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var request struct {
		Tools []sandboxTool  `json:"tools"`
		Input []sandboxInput `json:"input"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&request); err != nil {
		s.err = err
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.calls++
	if s.finished {
		s.err = fmt.Errorf("unexpected request after a completed turn")
		http.Error(w, s.err.Error(), http.StatusBadRequest)
		return
	}
	responseID := fmt.Sprintf("resp_probe_%d_%d", s.turn, s.calls)
	retry := false
	if s.unified && s.calls > 1 {
		output, found := sandboxCurrentOutput(request.Input, fmt.Sprintf("call_probe_%d_%d", s.turn, s.calls-1))
		s.lastOutput = output
		if !found {
			s.err = fmt.Errorf("the CLI submitted no result for the current confinement command")
		} else if strings.TrimSpace(output) == "unified exec is unavailable in this session" && !s.started {
			// A scripted response can reach the CLI before its execution environment
			// is ready. Retry only this explicit pre-execution refusal under the
			// invocation's subprocess budget; never rerun a started command.
			s.err = r.Context().Err()
			retry = s.err == nil
		} else {
			exitCode, _, running, stdout := sandboxExecResult(output)
			if exitCode == nil && !running {
				s.err = fmt.Errorf("the CLI refused the confinement command: %s", output)
			} else {
				s.started = true
				s.output += stdout
				s.exitCode = exitCode
				if exitCode != nil && *exitCode != 0 {
					s.err = fmt.Errorf("confinement command exited with code %d: %s", *exitCode, s.output)
				}
			}
		}
	}
	var item map[string]any
	if s.calls == 1 || retry {
		name, namespace := sandboxShellTool(request.Tools, "")
		s.unified = name == "exec_command"
		var arguments any
		switch name {
		case "shell":
			arguments = map[string]any{"command": s.command, "timeout_ms": nativeProbeSubprocessBudget.Milliseconds()}
		case "shell_command", "exec_command":
			words := make([]string, len(s.command))
			for i, word := range s.command {
				words[i] = "'" + strings.ReplaceAll(word, "'", "'\"'\"'") + "'"
			}
			if name == "exec_command" {
				arguments = map[string]any{"cmd": strings.Join(words, " "), "login": false, "yield_time_ms": 1000, "max_output_tokens": 4000}
			} else {
				arguments = map[string]any{"command": strings.Join(words, " "), "timeout_ms": nativeProbeSubprocessBudget.Milliseconds()}
			}
		}
		if name == "" {
			s.err = fmt.Errorf("the CLI advertised no supported shell tool: %v", request.Tools)
			http.Error(w, s.err.Error(), http.StatusBadRequest)
			return
		}
		if name == "local_shell" {
			item = map[string]any{"type": "local_shell_call", "id": fmt.Sprintf("lsh_probe_%d", s.turn),
				"call_id": fmt.Sprintf("call_probe_%d_%d", s.turn, s.calls), "status": "completed",
				"action": map[string]any{"type": "exec", "command": s.command, "timeout_ms": nativeProbeSubprocessBudget.Milliseconds()}}
		} else {
			encoded, _ := json.Marshal(arguments)
			item = map[string]any{"type": "function_call", "id": fmt.Sprintf("fc_probe_%d_%d", s.turn, s.calls),
				"call_id": fmt.Sprintf("call_probe_%d_%d", s.turn, s.calls), "name": name, "arguments": string(encoded)}
			if namespace != "" {
				item["namespace"] = namespace
			}
		}
	} else if session, running := sandboxRunningSession(request.Input, fmt.Sprintf("call_probe_%d_%d", s.turn, s.calls-1)); running && s.err == nil {
		namespace, found := sandboxFunctionTool(request.Tools, "", "write_stdin")
		if !found {
			s.err = fmt.Errorf("the CLI returned running session %d without a write_stdin tool", session)
			http.Error(w, s.err.Error(), http.StatusBadRequest)
			return
		}
		encoded, _ := json.Marshal(map[string]any{"session_id": session, "chars": "", "yield_time_ms": 1000, "max_output_tokens": 4000})
		item = map[string]any{"type": "function_call", "id": fmt.Sprintf("fc_probe_%d_%d", s.turn, s.calls),
			"call_id": fmt.Sprintf("call_probe_%d_%d", s.turn, s.calls), "name": "write_stdin", "arguments": string(encoded)}
		if namespace != "" {
			item["namespace"] = namespace
		}
	} else {
		s.finished = true
		reply := "probe complete"
		if s.err != nil {
			reply = "probe failed"
		}
		item = map[string]any{"type": "message", "id": fmt.Sprintf("msg_probe_%d", s.turn), "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": reply, "annotations": []any{}}}}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range []any{
		map[string]any{"type": "response.created", "response": map[string]any{"id": responseID}},
		map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item},
		map[string]any{"type": "response.completed", "response": map[string]any{"id": responseID, "output": []any{item},
			"usage": map[string]any{"input_tokens": 1, "input_tokens_details": nil, "output_tokens": 1, "output_tokens_details": nil, "total_tokens": 2}}},
	} {
		encoded, _ := json.Marshal(event)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.(map[string]any)["type"], encoded)
	}
}

type sandboxInput struct {
	Type   string          `json:"type"`
	CallID string          `json:"call_id"`
	Output json.RawMessage `json:"output"`
}

// exec_command can yield before compilation finishes. Only the current call's
// result may request a poll; saved outputs from earlier turns are not evidence.
func sandboxRunningSession(input []sandboxInput, callID string) (int64, bool) {
	output, found := sandboxCurrentOutput(input, callID)
	if !found {
		return 0, false
	}
	_, session, running, _ := sandboxExecResult(output)
	return session, running
}

func sandboxCurrentOutput(input []sandboxInput, callID string) (string, bool) {
	for _, item := range input {
		if item.Type != "function_call_output" || item.CallID != callID {
			continue
		}
		var output string
		if json.Unmarshal(item.Output, &output) == nil {
			return output, true
		}
		var content []struct{ Type, Text string }
		if json.Unmarshal(item.Output, &content) == nil {
			for _, part := range content {
				if part.Type == "input_text" {
					output += part.Text
				}
			}
			return output, true
		}
	}
	return "", false
}

func sandboxExecResult(output string) (exitCode *int, session int64, running bool, stdout string) {
	header, stdout, found := strings.Cut(output, "\nOutput:\n")
	if !found {
		return nil, 0, false, ""
	}
	for _, line := range strings.Split(header, "\n") {
		if value, found := strings.CutPrefix(line, "Process exited with code "); found {
			if code, err := strconv.Atoi(value); err == nil {
				exitCode = &code
			}
		}
		if value, found := strings.CutPrefix(line, "Process running with session ID "); found {
			if id, err := strconv.ParseInt(value, 10, 64); err == nil && id >= 0 {
				session, running = id, true
			}
		}
	}
	return exitCode, session, running, stdout
}

type sandboxTool struct {
	Type  string        `json:"type"`
	Name  string        `json:"name"`
	Tools []sandboxTool `json:"tools"`
}

func sandboxShellTool(tools []sandboxTool, namespace string) (string, string) {
	for _, tool := range tools {
		if tool.Type == "local_shell" {
			return "local_shell", ""
		} else if tool.Type == "namespace" {
			if name, space := sandboxShellTool(tool.Tools, tool.Name); name != "" {
				return name, space
			}
		} else if tool.Type == "function" && (tool.Name == "shell" || tool.Name == "shell_command" || tool.Name == "exec_command") {
			return tool.Name, namespace
		}
	}
	return "", ""
}

func sandboxFunctionTool(tools []sandboxTool, namespace, name string) (string, bool) {
	for _, tool := range tools {
		if tool.Type == "namespace" {
			if space, found := sandboxFunctionTool(tool.Tools, tool.Name, name); found {
				return space, true
			}
		} else if tool.Type == "function" && tool.Name == name {
			return namespace, true
		}
	}
	return "", false
}

func TestScriptedSandboxProviderUsesAdvertisedShellTool(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, tools, callType, function, namespace string
	}{
		{"built-in local shell", `[{"type":"local_shell"}]`, "local_shell_call", "", ""},
		{"shell function", `[{"type":"function","name":"shell"}]`, "function_call", "shell", ""},
		{"shell command function", `[{"type":"function","name":"shell_command"}]`, "function_call", "shell_command", ""},
		{"exec command function", `[{"type":"function","name":"exec_command"},{"type":"function","name":"write_stdin"}]`, "function_call", "exec_command", ""},
		{"namespaced exec command", `[{"type":"namespace","name":"functions","tools":[{"type":"function","name":"exec_command"}]}]`, "function_call", "exec_command", "functions"},
		{"namespaced shell", `[{"type":"namespace","name":"functions","tools":[{"type":"function","name":"shell"}]}]`, "function_call", "shell", "functions"},
		{"unsupported tool", `[{"type":"function","name":"apply_patch"}]`, "", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := []string{"sh", "-c", "printf 'quoted'"}
			model := &sandboxResponses{}
			model.begin(command)
			response := httptest.NewRecorder()
			model.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(`{"tools":`+test.tools+`}`)))
			if test.callType == "" {
				if response.Code != http.StatusBadRequest || model.err == nil {
					t.Fatalf("unsupported tool response = %d, error = %v", response.Code, model.err)
				}
				return
			}
			if response.Code != http.StatusOK {
				t.Fatalf("scripted response = %d: %s", response.Code, response.Body)
			}
			var item struct {
				Type, Name, Namespace, Arguments string
				Action                           struct {
					Type    string
					Command []string
					Timeout int64 `json:"timeout_ms"`
				}
			}
			for _, line := range strings.Split(response.Body.String(), "\n") {
				if data, found := strings.CutPrefix(line, "data: "); found {
					var event struct {
						Type string
						Item json.RawMessage
					}
					if err := json.Unmarshal([]byte(data), &event); err != nil {
						t.Fatal(err)
					}
					if event.Type == "response.output_item.done" {
						if err := json.Unmarshal(event.Item, &item); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if item.Type != test.callType || item.Name != test.function || item.Namespace != test.namespace {
				t.Fatalf("tool call = %+v, want %s %s in %s", item, test.callType, test.function, test.namespace)
			}
			if test.callType == "local_shell_call" {
				if item.Action.Type != "exec" || !reflect.DeepEqual(item.Action.Command, command) || item.Action.Timeout != nativeProbeSubprocessBudget.Milliseconds() {
					t.Fatalf("local shell action = %+v, want exec %q", item.Action, command)
				}
			} else if test.function == "shell_command" || test.function == "exec_command" {
				var arguments struct {
					Command, Cmd string
					YieldTimeMS  int   `json:"yield_time_ms"`
					Timeout      int64 `json:"timeout_ms"`
				}
				if err := json.Unmarshal([]byte(item.Arguments), &arguments); err != nil {
					t.Fatal(err)
				}
				command := arguments.Command
				if test.function == "exec_command" {
					command = arguments.Cmd
					if arguments.YieldTimeMS != 1000 {
						t.Fatalf("exec yield = %d, want a bounded poll", arguments.YieldTimeMS)
					}
				} else if arguments.Timeout != nativeProbeSubprocessBudget.Milliseconds() {
					t.Fatalf("shell timeout = %d, want the generous subprocess budget", arguments.Timeout)
				}
				if command != `'sh' '-c' 'printf '"'"'quoted'"'"''` {
					t.Fatalf("shell command = %q", command)
				}
			} else {
				var arguments struct {
					Command []string
					Timeout int64 `json:"timeout_ms"`
				}
				if err := json.Unmarshal([]byte(item.Arguments), &arguments); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(arguments.Command, command) || arguments.Timeout != nativeProbeSubprocessBudget.Milliseconds() {
					t.Fatalf("shell arguments = %+v, want %q with the generous subprocess budget", arguments, command)
				}
			}
			body := `{"tools":[]}`
			if test.function == "exec_command" {
				body = `{"input":[{"type":"function_call_output","call_id":"call_probe_1_1","output":"Process exited with code 0\nOutput:\nnative sandbox probe passed\n"}]}`
			}
			model.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(body)))
			model.requireTurn(t)
		})
	}
}

func TestScriptedSandboxProviderPollsOnlyTheCurrentCommandUntilItFinishes(t *testing.T) {
	t.Parallel()
	model := &sandboxResponses{}
	model.begin([]string{"sh", "-c", "probe"})
	tools := []sandboxTool{{Type: "namespace", Name: "functions", Tools: []sandboxTool{
		{Type: "function", Name: "exec_command"}, {Type: "function", Name: "write_stdin"},
	}}}
	request := func(input ...sandboxInput) map[string]json.RawMessage {
		t.Helper()
		body, err := json.Marshal(map[string]any{"tools": tools, "input": input})
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		model.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(string(body))))
		if response.Code != http.StatusOK {
			t.Fatalf("scripted response = %d: %s", response.Code, response.Body)
		}
		for _, line := range strings.Split(response.Body.String(), "\n") {
			if data, found := strings.CutPrefix(line, "data: "); found {
				var event struct {
					Type string
					Item map[string]json.RawMessage
				}
				if err := json.Unmarshal([]byte(data), &event); err != nil {
					t.Fatal(err)
				}
				if event.Type == "response.output_item.done" {
					return event.Item
				}
			}
		}
		t.Fatal("scripted response had no output item")
		return nil
	}
	request()
	output, _ := json.Marshal("Chunk ID: 123\nProcess running with session ID 42\nOutput:\n")
	// A loaded compilation can require more polls than the former 64-call cap.
	// Drive progress by command results, without sleeping or timing the wait.
	const polls = 80
	for call := 1; call <= polls; call++ {
		callID := fmt.Sprintf("call_probe_1_%d", call)
		item := request(sandboxInput{Type: "function_call_output", CallID: callID, Output: output})
		if string(item["name"]) != `"write_stdin"` || string(item["namespace"]) != `"functions"` {
			t.Fatalf("running command response = %v, want namespaced write_stdin", item)
		}
		var encoded string
		if err := json.Unmarshal(item["arguments"], &encoded); err != nil {
			t.Fatal(err)
		}
		var arguments struct {
			SessionID int64  `json:"session_id"`
			Chars     string `json:"chars"`
			Yield     int    `json:"yield_time_ms"`
		}
		if err := json.Unmarshal([]byte(encoded), &arguments); err != nil {
			t.Fatal(err)
		}
		if arguments.SessionID != 42 || arguments.Chars != "" || arguments.Yield != 1000 || model.finished {
			t.Fatalf("poll = %+v, finished = %v", arguments, model.finished)
		}
	}
	completed, _ := json.Marshal("Process exited with code 0\nOutput:\nnative sandbox probe passed\n")
	item := request(
		sandboxInput{Type: "function_call_output", CallID: "call_probe_1_1", Output: output},
		sandboxInput{Type: "function_call_output", CallID: fmt.Sprintf("call_probe_1_%d", polls+1), Output: completed},
	)
	if string(item["type"]) != `"message"` {
		t.Fatalf("completed command response = %v, want the final reply", item)
	}
	model.requireTurn(t)
	if verified, result := model.commandEvidence(nativeProbeReply); !verified {
		t.Fatalf("completed command evidence = %q, want this turn's successful probe", result)
	}
}

func scriptedSandboxRequest(t *testing.T, model *sandboxResponses, input ...sandboxInput) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"tools": []sandboxTool{{Type: "function", Name: "exec_command"}, {Type: "function", Name: "write_stdin"}},
		"input": input,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	model.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(string(body))))
	if response.Code != http.StatusOK {
		t.Fatalf("scripted response = %d: %s", response.Code, response.Body)
	}
	return response.Body.String()
}

func TestScriptedSandboxProviderRequiresCurrentCommandEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, callID, output string
		verified             bool
	}{
		{"completed", "call_probe_1_1", "Process exited with code 0\nOutput:\n" + nativeProbeReply + "\n", true},
		{"failed", "call_probe_1_1", "Process exited with code 21\nOutput:\n" + nativeProbeReply + "\n", false},
		{"scripted reply", "call_probe_1_1", nativeProbeReply, false},
		{"refused", "call_probe_1_1", "exec_command failed: sandbox_apply: Operation not permitted", false},
		{"old call", "call_probe_0_1", "Process exited with code 0\nOutput:\n" + nativeProbeReply + "\n", false},
		{"old proof", "call_probe_1_1", "Process exited with code 0\nOutput:\n" + nativeProbeReply + ": previous turn\n", false},
		{"stdout pretending to be an exit", "call_probe_1_1", "Wall time: 0.1 seconds\nOutput:\nProcess exited with code 0\n" + nativeProbeReply + "\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := &sandboxResponses{}
			model.begin([]string{"sh", "-c", "probe"})
			scriptedSandboxRequest(t, model)
			output, _ := json.Marshal(test.output)
			scriptedSandboxRequest(t, model, sandboxInput{Type: "function_call_output", CallID: test.callID, Output: output})
			if verified, result := model.commandEvidence(nativeProbeReply); verified != test.verified {
				t.Fatalf("command evidence = %v, result = %q, want verified = %v", verified, result, test.verified)
			}
			if test.name == "refused" && (model.err == nil || !strings.Contains(model.err.Error(), test.output)) {
				t.Fatalf("refusal error = %v, want the CLI's complete refusal", model.err)
			}
			model.begin([]string{"sh", "-c", "next probe"})
			if verified, _ := model.commandEvidence(nativeProbeReply); verified {
				t.Fatal("a new turn retained the preceding command's evidence")
			}
		})
	}
}

func TestNativeProbeReadsUnifiedToolResultWithoutACommandEvent(t *testing.T) {
	t.Parallel()
	model := &sandboxResponses{}
	model.begin([]string{"sh", "-c", "probe"})
	scriptedSandboxRequest(t, model)
	output, _ := json.Marshal("Process exited with code 0\nOutput:\n" + nativeProbeReply + "\n")
	scriptedSandboxRequest(t, model, sandboxInput{Type: "function_call_output", CallID: "call_probe_1_1", Output: output})
	model.requireTurn(t)
	repository, worktree := sandboxRepository(t, true)
	runner := &sandboxCLIRunner{home: t.TempDir(), url: "http://127.0.0.1:0", runner: &fakeRunner{
		results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: lines(
			`{"type":"thread.started","thread_id":"native-session"}`,
			`{"type":"item.completed","item":{"type":"agent_message","text":"probe complete"}}`,
			`{"type":"turn.completed","usage":{"input_tokens":2,"output_tokens":2}}`,
		)}},
	}}
	result := nativeProbeTurn(t, Backend{Runner: runner}, backendapi.RunRequest{Model: "gpt-6.1-sol",
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree,
		RepositoryRoot: repository, Prompt: "probe",
	}, nativeProbeReply, model)
	if result.Process.Stdout != "" || result.SessionID != "native-session" {
		t.Fatalf("adapter result = %+v, want normalized output and the saved session", result)
	}
}

func TestScriptedSandboxProviderRetriesOnlyBeforeExecutionStarts(t *testing.T) {
	t.Parallel()
	model := &sandboxResponses{}
	model.begin([]string{"sh", "-c", "probe"})
	scriptedSandboxRequest(t, model)
	unavailable, _ := json.Marshal("unified exec is unavailable in this session")
	response := scriptedSandboxRequest(t, model, sandboxInput{Type: "function_call_output", CallID: "call_probe_1_1", Output: unavailable})
	if !strings.Contains(response, `"name":"exec_command"`) || model.err != nil || model.started {
		t.Fatalf("startup retry = %s, error = %v, started = %v", response, model.err, model.started)
	}
	running, _ := json.Marshal("Process running with session ID 42\nOutput:\n")
	scriptedSandboxRequest(t, model, sandboxInput{Type: "function_call_output", CallID: "call_probe_1_2", Output: running})
	response = scriptedSandboxRequest(t, model, sandboxInput{Type: "function_call_output", CallID: "call_probe_1_3", Output: unavailable})
	if strings.Contains(response, `"name":"exec_command"`) || model.err == nil {
		t.Fatalf("started command was retried: %s, error = %v", response, model.err)
	}

	model.begin([]string{"sh", "-c", "next probe"})
	scriptedSandboxRequest(t, model)
	// Startup readiness is not inferred from how many requests have passed.
	for call := 1; call <= 80; call++ {
		response = scriptedSandboxRequest(t, model, sandboxInput{Type: "function_call_output", CallID: fmt.Sprintf("call_probe_2_%d", call), Output: unavailable})
		if !strings.Contains(response, `"name":"exec_command"`) || model.err != nil || model.started {
			t.Fatalf("startup retry = %s, error = %v, started = %v", response, model.err, model.started)
		}
	}
	completed, _ := json.Marshal("Process exited with code 0\nOutput:\n" + nativeProbeReply + "\n")
	scriptedSandboxRequest(t, model, sandboxInput{Type: "function_call_output", CallID: "call_probe_2_81", Output: completed})
	model.requireTurn(t)
}

func TestSandboxToolResultDecodesContentAndIgnoresStdoutSessionMarkers(t *testing.T) {
	t.Parallel()
	body := json.RawMessage(`[{"type":"input_text","text":"Process exited with code 0\nOutput:\nProcess running with session ID 42\n"}]`)
	input := []sandboxInput{{Type: "function_call_output", CallID: "current-call", Output: body}}
	output, found := sandboxCurrentOutput(input, "current-call")
	if !found {
		t.Fatal("content-array tool result was not decoded")
	}
	exit, _, running, stdout := sandboxExecResult(output)
	if exit == nil || *exit != 0 || running || stdout != "Process running with session ID 42\n" {
		t.Fatalf("command result = %v, %v, %q, want a completed command with a literal stdout marker", exit, running, stdout)
	}
	if _, running := sandboxRunningSession(input, "current-call"); running {
		t.Fatal("a session marker printed by the command requested a poll")
	}
}
