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
	oldRepository, oldWorktree := sandboxRepository(t, true)
	newRepository, newWorktree := sandboxRepository(t, true)
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
	server := &httptest.Server{Listener: listener, Config: &http.Server{
		Handler: model, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second,
	}}
	server.Start()
	defer server.Close()
	runner := &sandboxCLIRunner{home: home, url: server.URL}
	provider := Backend{Binary: binary, Runner: runner}
	request := backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: oldWorktree,
		RepositoryRoot: oldRepository, AccountConfigDir: home, Model: "gpt-5",
		Prompt: "Execute the supplied sandbox probe, then finish.", Timeout: time.Minute, IdleTimeout: time.Minute,
	}
	denied := []string{outside, otherScratch, filepath.Join(oldRepository, ".git"), filepath.Join(newRepository, ".git")}
	freshProof := nativeProbeReply + ": fresh launch"
	model.begin(nativeProbeCommand(t, oldWorktree, oldPaths, append(denied, append(newPaths, newWorktree)...), freshProof))
	fresh := nativeProbeTurn(t, provider, request, freshProof)
	if fresh.SessionID == "" {
		t.Fatal("the fresh CLI invocation did not create a resumable session")
	}
	model.requireTurn(t)

	request.SessionID = fresh.SessionID
	request.RepositoryRoot, request.WorkingDirectory = newRepository, newWorktree
	resumeProof := nativeProbeReply + ": changed grants"
	model.begin(nativeProbeCommand(t, newWorktree, newPaths, append(denied, append(oldPaths, oldWorktree)...), resumeProof))
	resumed := nativeProbeTurn(t, provider, request, resumeProof)
	if resumed.SessionID != fresh.SessionID {
		t.Fatalf("resume created session %q instead of restoring %q", resumed.SessionID, fresh.SessionID)
	}
	model.requireTurn(t)

	// Restore that same previously writable session under the reviewer's native
	// posture. Old and current cache, scratch, worktrees, and unrelated paths
	// must all be read-only; permission retained from either turn fails the test.
	request.Role = domain.RoleReviewer
	allDenied := append(append(denied, oldPaths...), newPaths...)
	allDenied = append(allDenied, oldWorktree, newWorktree)
	reviewProof := nativeProbeReply + ": reviewer resume"
	model.begin(nativeProbeCommand(t, newWorktree, nil, allDenied, reviewProof))
	readOnly := nativeProbeTurn(t, provider, request, reviewProof)
	if readOnly.SessionID != fresh.SessionID {
		t.Fatalf("read-only resume created session %q instead of restoring %q", readOnly.SessionID, fresh.SessionID)
	}
	model.requireTurn(t)
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
  printf allowed > "$cache/allowed"
  printf allowed > "$worktree/allowed"
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

func nativeProbeTurn(t *testing.T, provider Backend, request backendapi.RunRequest, proof string) backendapi.RunResult {
	t.Helper()
	result, err := provider.Run(context.Background(), request)
	process := provider.Runner.(*sandboxCLIRunner).process
	if err != nil || result.IsError || result.Process.Status != execution.ProcessSucceeded {
		t.Fatalf("native CLI turn: %v; status=%s, exit=%d, stop=%q, reply=%q\n%s\n%s",
			err, process.Status, process.ExitCode, result.StopReason, result.FinalText, process.Stdout, process.Stderr)
	}
	// A fake provider's final reply is not execution evidence. Require the CLI's
	// completed command item to hold this turn's stdout and a successful exit;
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
	// Keep checking subsequent native resumes when the CLI saved the session,
	// even if this turn's sandbox refused to execute. The test remains failed.
	t.Errorf("the CLI did not report a successful confinement command:\n%s\n%s", process.Stdout, process.Stderr)
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
	result := nativeProbeTurn(t, Backend{Runner: runner}, backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree,
		RepositoryRoot: repository, Prompt: "probe",
	}, nativeProbeReply)
	if result.Process.Stdout != "" || result.SessionID != "native-session" {
		t.Fatalf("adapter result = %+v, want normalized output and the saved session", result)
	}
}

type sandboxResponses struct {
	mu       sync.Mutex
	command  []string
	calls    int
	turn     int
	finished bool
	err      error
}

func (s *sandboxResponses) begin(command []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.command, s.calls, s.err = command, 0, nil
	s.finished = false
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
	if s.finished || s.calls > 64 {
		s.err = fmt.Errorf("unexpected request after a completed turn or beyond the probe's call bound")
		http.Error(w, s.err.Error(), http.StatusBadRequest)
		return
	}
	responseID := fmt.Sprintf("resp_probe_%d_%d", s.turn, s.calls)
	var item map[string]any
	if s.calls == 1 {
		name, namespace := sandboxShellTool(request.Tools, "")
		var arguments any
		switch name {
		case "shell":
			arguments = map[string]any{"command": s.command, "timeout_ms": 45000}
		case "shell_command", "exec_command":
			words := make([]string, len(s.command))
			for i, word := range s.command {
				words[i] = "'" + strings.ReplaceAll(word, "'", "'\"'\"'") + "'"
			}
			if name == "exec_command" {
				arguments = map[string]any{"cmd": strings.Join(words, " "), "login": false, "yield_time_ms": 1000, "max_output_tokens": 4000}
			} else {
				arguments = map[string]any{"command": strings.Join(words, " "), "timeout_ms": 45000}
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
				"action": map[string]any{"type": "exec", "command": s.command, "timeout_ms": 45000}}
		} else {
			encoded, _ := json.Marshal(arguments)
			item = map[string]any{"type": "function_call", "id": fmt.Sprintf("fc_probe_%d", s.turn),
				"call_id": fmt.Sprintf("call_probe_%d_%d", s.turn, s.calls), "name": name, "arguments": string(encoded)}
			if namespace != "" {
				item["namespace"] = namespace
			}
		}
	} else if session, running := sandboxRunningSession(request.Input, fmt.Sprintf("call_probe_%d_%d", s.turn, s.calls-1)); running {
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
		item = map[string]any{"type": "message", "id": fmt.Sprintf("msg_probe_%d", s.turn), "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": "probe complete", "annotations": []any{}}}}
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
	for _, item := range input {
		if item.Type != "function_call_output" || item.CallID != callID {
			continue
		}
		var output string
		if json.Unmarshal(item.Output, &output) != nil {
			continue
		}
		_, suffix, found := strings.Cut(output, "Process running with session ID ")
		if fields := strings.Fields(suffix); found && len(fields) > 0 {
			session, err := strconv.ParseInt(fields[0], 10, 64)
			return session, err == nil && session >= 0
		}
	}
	return 0, false
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
				if item.Action.Type != "exec" || !reflect.DeepEqual(item.Action.Command, command) {
					t.Fatalf("local shell action = %+v, want exec %q", item.Action, command)
				}
			} else if test.function == "shell_command" || test.function == "exec_command" {
				var arguments struct {
					Command, Cmd string
					YieldTimeMS  int `json:"yield_time_ms"`
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
				}
				if command != `'sh' '-c' 'printf '"'"'quoted'"'"''` {
					t.Fatalf("shell command = %q", command)
				}
			} else {
				var arguments struct{ Command []string }
				if err := json.Unmarshal([]byte(item.Arguments), &arguments); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(arguments.Command, command) {
					t.Fatalf("shell arguments = %q, want %q", arguments.Command, command)
				}
			}
			model.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(`{"tools":[]}`)))
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
	for _, callID := range []string{"call_probe_1_1", "call_probe_1_2"} {
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
		sandboxInput{Type: "function_call_output", CallID: "call_probe_1_3", Output: completed},
	)
	if string(item["type"]) != `"message"` {
		t.Fatalf("completed command response = %v, want the final reply", item)
	}
	model.requireTurn(t)
}
