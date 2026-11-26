package claudecode

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// launchedFor is the command one invocation of request is launched as.
func launchedFor(t *testing.T, request backendapi.RunRequest) execution.Command {
	t.Helper()
	runner := &fakeRunner{results: claudeCompletedTurn()}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}, StateRoot: testState}).Run(context.Background(), request); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return runner.commands[0]
}

// Every session launched with no tools -- each management role, fresh and
// resumed, a helper given an empty tool list, and a developer its role
// definition holds read-only -- runs with attachments off, whatever the
// harness's own environment says. A developer and a reviewer, which hold tools,
// are given the harness's environment for it unchanged.
func TestEveryToolFreeSessionRunsWithAttachmentsOff(t *testing.T) {
	// t.Setenv is this process's environment, so this cannot run in parallel.
	t.Setenv(attachmentsVariable, "0")
	request := func(role domain.AgentRole) backendapi.RunRequest {
		return backendapi.RunRequest{RunID: testRunID, Role: role, ScratchDirectory: testScratch, WorkingDirectory: t.TempDir(), Prompt: "answer"}
	}
	cases := map[string]backendapi.RunRequest{}
	for _, role := range domain.Roles() {
		if role == domain.RoleDeveloper || role == domain.RoleReviewer {
			continue
		}
		fresh, resumed, helper := request(role), request(role), request(role)
		resumed.SessionID = "session-one"
		helper.AllowedTools = []string{}
		cases[string(role)+", fresh"], cases[string(role)+", resumed"], cases[string(role)+", helper"] = fresh, resumed, helper
	}
	held := request(domain.RoleDeveloper)
	held.Posture = backendapi.PostureReadOnly
	cases["developer held read-only"] = held
	for name, request := range cases {
		launched := launchedFor(t, request)
		if tools, _ := optionValue(launched.Args, "--tools"); tools != "" {
			t.Fatalf("%s: --tools = %q, want none", name, tools)
		}
		if !slices.Contains(launched.Env, attachmentsVariable+"=1") || slices.Contains(launched.Env, attachmentsVariable+"=0") {
			t.Errorf("%s: environment does not hold attachments off alone, so a file its prompt names with @ is sent to the model: %v", name, launched.Env)
		}
	}
	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer} {
		if launched := launchedFor(t, request(role)); !slices.Contains(launched.Env, attachmentsVariable+"=0") || slices.Contains(launched.Env, attachmentsVariable+"=1") {
			t.Errorf("a %s's environment for attachments was changed: %v", role, launched.Env)
		}
	}
}

// The installed CLI, launched as a management role is and given a prompt naming
// a file by @ -- one outside the working directory by its absolute path and one
// inside it by a relative one -- sends a local stand-in for the provider nothing
// either file holds. The control turns attachments back on and has to see both
// sent, since without that the check would pass on a CLI that never read a file
// named with @ at all. It skips where Claude Code is not installed.
func TestTheInstalledCLISendsNoFileAToolFreePromptNames(t *testing.T) {
	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Skipf("Claude Code is not installed, so what it reads cannot be asked here: %v", err)
	}
	// t.Setenv is this process's environment, so this cannot run in parallel.
	t.Setenv(attachmentsVariable, "")
	elsewhere, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(elsewhere, "outside.txt")
	if err := os.WriteFile(outside, []byte("text-of-the-outside-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "inside.txt"), []byte("text-of-the-inside-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	prompt := "judge @" + outside + " and @inside.txt now"
	launched := launchedFor(t, backendapi.RunRequest{RunID: testRunID, Role: domain.RoleProductManager, WorkingDirectory: worktree, Prompt: prompt})
	// A request naming no cached opening is sent its prompt plain on standard
	// input (promptInput).
	sent, output := bodiesSentForAPrompt(t, binary, worktree, prompt, launched.Args, launched.Env)
	if strings.Contains(sent, "text-of-the-outside-file") || strings.Contains(sent, "text-of-the-inside-file") {
		t.Errorf("a session with no tools sent the outside file %t and the inside file %t, want neither: %s",
			strings.Contains(sent, "text-of-the-outside-file"), strings.Contains(sent, "text-of-the-inside-file"), output)
	}
	if !strings.Contains(sent, "judge") {
		t.Errorf("the stand-in was not sent the prompt, so this check shows nothing: %s", output)
	}
	control := slices.DeleteFunc(slices.Clone(launched.Env), func(entry string) bool { return strings.HasPrefix(entry, attachmentsVariable+"=") })
	if sent, output := bodiesSentForAPrompt(t, binary, worktree, prompt, launched.Args, control); !strings.Contains(sent, "text-of-the-outside-file") || !strings.Contains(sent, "text-of-the-inside-file") {
		t.Errorf("with attachments on the files were not sent either, so this check shows nothing: %s", output)
	}
}

// bodiesSentForAPrompt runs the installed CLI once in environment, with a
// provider that is a local server refusing every request, and is every request
// body it was sent and what the CLI printed.
func bodiesSentForAPrompt(t *testing.T, binary, directory, prompt string, args, environment []string) (string, string) {
	t.Helper()
	var mutex sync.Mutex
	var sent strings.Builder
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no local server can be started to stand in for the provider: %v", err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		mutex.Lock()
		sent.Write(body)
		mutex.Unlock()
		response.Header().Set("content-type", "application/json")
		response.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(response, `{"type":"error","error":{"type":"invalid_request_error","message":"stand-in"}}`)
	})}}
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = directory
	command.Stdin = strings.NewReader(prompt)
	// A later entry for a name wins, so these stand in for the account.
	command.Env = append(slices.Clone(environment), "CLAUDE_CONFIG_DIR="+t.TempDir(), "ANTHROPIC_BASE_URL="+server.URL,
		"ANTHROPIC_API_KEY=stand-in", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1")
	// The CLI exits with a failure, the stand-in having refused it; what it sent
	// first is the answer.
	output, _ := command.CombinedOutput()
	mutex.Lock()
	defer mutex.Unlock()
	return sent.String(), string(output)
}
