package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// servicesSettings is the part of a developer's settings that keeps it out of
// the services folder.
type servicesSettings struct {
	Sandbox struct {
		Filesystem struct {
			DenyRead  []string `json:"denyRead"`
			AllowRead []string `json:"allowRead"`
		} `json:"filesystem"`
		Network struct {
			AllowLocalBinding   bool     `json:"allowLocalBinding"`
			AllowAllUnixSockets *bool    `json:"allowAllUnixSockets"`
			AllowUnixSockets    []string `json:"allowUnixSockets"`
		} `json:"network"`
	} `json:"sandbox"`
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
	} `json:"permissions"`
}

// developerServicesSettings is the settings a developer run is launched with
// over a state directory.
func developerServicesSettings(t *testing.T, state, session string, subagents *backendapi.SubagentRequest) servicesSettings {
	t.Helper()
	scratch, err := execution.PrepareScratchDirectory(state, testRunID)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{results: claudeCompletedTurn()}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: t.TempDir(),
		ScratchDirectory: scratch, Prompt: "do the work", SessionID: session, Subagents: subagents,
	}); err != nil {
		t.Fatal(err)
	}
	encoded, _ := optionValue(runner.commands[0].Args, "--settings")
	var settings servicesSettings
	if err := json.Unmarshal([]byte(encoded), &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

// Every developer launch, fresh or resumed, with sub-agents or without, makes
// the services folder and is refused it: for reading by the shell and by the
// file tools, and for connecting to any socket file in it.
func TestEveryDeveloperIsRefusedTheServicesFolder(t *testing.T) {
	t.Parallel()
	for _, session := range []string{"", "session-one"} {
		for _, subagents := range []*backendapi.SubagentRequest{nil, {Model: "sonnet", Effort: "medium"}} {
			state, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			settings := developerServicesSettings(t, state, session, subagents)
			services := filepath.Join(state, "services")
			if info, err := os.Stat(services); err != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("services folder = %v, %v; want it made for its owner only", info, err)
			}
			if got := settings.Sandbox.Filesystem; !slices.Equal(got.DenyRead, []string{services}) || len(got.AllowRead) != 0 {
				t.Fatalf("sandbox file system = %+v, want reading %q denied", got, services)
			}
			if want := []string{"Read(/" + services + ")", "Read(/" + services + "/**)"}; !slices.Equal(settings.Permissions.Deny, want) || len(settings.Permissions.Allow) != 0 {
				t.Fatalf("permission rules = %+v, want only %q denied", settings.Permissions, want)
			}
			network := settings.Sandbox.Network
			if network.AllowAllUnixSockets == nil || *network.AllowAllUnixSockets || len(network.AllowUnixSockets) != 0 {
				t.Fatalf("sandbox network = %+v, want no socket file allowed", network)
			}
		}
	}
}

// A developer with no scratch folder, whose services folder could not be found,
// is refused rather than started without the denial.
func TestADeveloperWhoseServicesFolderCannotBeFoundIsNotStarted(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	_, err := (Backend{Runner: runner}).Run(context.Background(), backendapi.RunRequest{
		RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: t.TempDir(), ScratchDirectory: "", Prompt: "do the work",
	})
	if err == nil || len(runner.commands) != 0 {
		t.Fatalf("Run() = %v, commands = %v, want a refusal", err, runner.commands)
	}
}

// The worktree's own settings cannot open the folder: each setting that would
// refuses the run.
func TestAWorktreeCannotOpenTheServicesFolder(t *testing.T) {
	t.Parallel()
	for content, refused := range map[string]bool{
		`{"sandbox":{"filesystem":{"allowRead":["~/x"]}}}`:    true,
		`{"sandbox":{"filesystem":{"disabled":true}}}`:        true,
		`{"sandbox":{"network":{"allowUnixSockets":["/x"]}}}`: true,
		`{"sandbox":{"excludedCommands":["docker *"]}}`:       true,
		`{"sandbox":`: true,
		`{"sandbox":{"network":{"allowAllUnixSockets":true}}}`: false,
		`{"permissions":{"allow":["Read(//**)"]},"hooks":{}}`:  false,
	} {
		worktree := t.TempDir()
		if err := os.MkdirAll(filepath.Join(worktree, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(worktree, ".claude", "settings.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		runner := &fakeRunner{results: claudeCompletedTurn()}
		_, err := (Backend{Runner: runner, Clock: fixedClock{}}).Run(context.Background(), backendapi.RunRequest{
			RunID: testRunID, Role: domain.RoleDeveloper, WorkingDirectory: worktree, ScratchDirectory: testScratch, Prompt: "do the work",
		})
		if (err != nil) != refused || refused && len(runner.commands) != 0 {
			t.Errorf("%s: Run() = %v after %d commands, want refused %v", content, err, len(runner.commands), refused)
		}
	}
}

// The launch check notices settings from elsewhere that open the folder or take
// the deny rule away, and leaves a repository's other permission rules alone.
func TestTheLaunchCheckNoticesTheServicesDenialMissing(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		change func(effective map[string]any)
		want   string
	}{
		"other rules": {func(effective map[string]any) {
			rules := effective["permissions"].(map[string]any)
			rules["allow"] = []any{"Bash(make *)"}
			rules["deny"] = append([]any{"Read(//elsewhere)"}, rules["deny"].([]any)...)
		}, ""},
		"no deny rule": {func(effective map[string]any) { delete(effective, "permissions") }, "the setting permissions is not applied"},
		"read allowed again": {func(effective map[string]any) {
			effective["sandbox"].(map[string]any)["filesystem"].(map[string]any)["allowRead"] = []any{"/"}
		}, "the setting sandbox is not applied"},
		"socket allowed": {func(effective map[string]any) {
			effective["sandbox"].(map[string]any)["network"].(map[string]any)["allowUnixSockets"] = []any{"/"}
		}, "the setting sandbox is not applied"},
	} {
		accepted := acceptingCLI(t)
		cli := &scriptedCLI{version: "2.1.286 (Claude Code)", answers: func(settings string) string {
			var changed map[string]any
			if err := json.Unmarshal([]byte(settings), &changed); err != nil {
				t.Fatal(err)
			}
			test.change(changed)
			encoded, _ := json.Marshal(changed)
			return accepted(string(encoded))
		}}
		check, err := (Backend{Runner: cli}).CheckLaunchSettings(context.Background(), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if said := check.Says(); (test.want == "") != (said == "") || !strings.Contains(said, test.want) {
			t.Errorf("%s: check says %q, want %q", name, said, test.want)
		}
	}
}

// localServiceProbe is what a command in a developer's sandbox came to: a
// server of its own on a port the system chose, and the harness's service.
type localServiceProbe struct {
	Server, Credential, Socket string
}

const localServiceProbeVariable = "YOYODYNE_TEST_LOCAL_SERVICE_PROBE"

const probeSucceeded = "ok"

// actAsLocalServiceProbe makes this test binary the command the probe below
// runs, and reports whether it did.
func actAsLocalServiceProbe() bool {
	services := os.Getenv(localServiceProbeVariable)
	if services == "" {
		return false
	}
	outcome := func(err error) string {
		if err != nil {
			return err.Error()
		}
		return probeSucceeded
	}
	_, credential := os.ReadFile(filepath.Join(services, "token"))
	connection, socket := net.DialTimeout("unix", filepath.Join(services, "s"), 5*time.Second)
	if socket == nil {
		connection.Close()
	}
	_ = json.NewEncoder(os.Stdout).Encode(localServiceProbe{
		Server: outcome(serveOnce()), Credential: outcome(credential), Socket: outcome(socket),
	})
	return true
}

// serveOnce starts a server on a port the system chooses and asks it once.
func serveOnce() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	go func() {
		if connection, err := listener.Accept(); err == nil {
			_, _ = io.WriteString(connection, "answer")
			connection.Close()
		}
	}()
	connection, err := net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	answer, err := io.ReadAll(connection)
	if err == nil && string(answer) != "answer" {
		err = fmt.Errorf("the server answered %q", answer)
	}
	return err
}

// seatbeltProfile is the rules Claude Code 2.1.322 writes for macOS from these
// settings, for what the settings decide here: reads of a denied path, and the
// network. They were read from its shipped code with no provider call.
func seatbeltProfile(settings servicesSettings) string {
	rules := []string{"(version 1)", "(allow default)", "(deny network*)"}
	if settings.Sandbox.Network.AllowLocalBinding {
		rules = append(rules, `(allow network-bind (local ip "*:*"))`, `(allow network-inbound (local ip "*:*"))`, `(allow network-outbound (remote ip "localhost:*"))`)
	}
	if allowed := settings.Sandbox.Network.AllowAllUnixSockets; allowed != nil && *allowed {
		rules = append(rules, `(allow network-outbound (remote unix-socket (path-regex #"^/")))`)
	}
	for _, path := range settings.Sandbox.Filesystem.DenyRead {
		rules = append(rules, fmt.Sprintf("(deny file-read* (subpath %q))", path))
	}
	return strings.Join(rules, "\n")
}

// Under a developer run's settings a server on a port the system chooses starts
// and answers, and the harness's own service is out of reach: its credential
// cannot be read and its socket file cannot be connected to.
//
// On macOS the probe runs under the sandbox rules the settings become. A
// developer run cannot put a sandbox inside its own, so there the probe runs
// directly: the run's own sandbox is the settings this is about, and only the
// server is asked about. Elsewhere only the server is asked about too.
func TestAServerOnAPortTheSystemChoosesAnswersUnderADevelopersSettings(t *testing.T) {
	t.Parallel()
	insideADeveloperRun := os.Getenv(execution.AgentRoleVariable) == string(domain.RoleDeveloper)
	sandboxed := runtime.GOOS == "darwin" && !insideADeveloperRun
	// A socket file's path has to be short, which a test's own directory, or a
	// temporary directory a check was given, may not be.
	parent := ""
	if sandboxed {
		parent = "/tmp"
	}
	state, err := os.MkdirTemp(parent, "yy")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(state) })
	if state, err = filepath.EvalSymlinks(state); err != nil {
		t.Fatal(err)
	}
	settings := developerServicesSettings(t, state, "", nil)
	if !settings.Sandbox.Network.AllowLocalBinding {
		t.Fatal("a developer's settings no longer let a command listen on a local port")
	}
	services := filepath.Join(state, "services")
	if err := os.WriteFile(filepath.Join(services, "token"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := func(name string, args ...string) (localServiceProbe, string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, name, args...)
		command.Env = append(os.Environ(), localServiceProbeVariable+"="+services)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		var found localServiceProbe
		if err == nil {
			err = json.Unmarshal(stdout.Bytes(), &found)
		}
		return found, stderr.String(), err
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	if sandboxed {
		listener, err := net.Listen("unix", filepath.Join(services, "s"))
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		// Unconfined, all three are reached, so a refusal below is the settings'.
		if found, stderr, err := probe(binary); err != nil || found != (localServiceProbe{probeSucceeded, probeSucceeded, probeSucceeded}) {
			t.Fatalf("unconfined probe = %+v, %v: %s", found, err, stderr)
		}
		found, stderr, err := probe("/usr/bin/sandbox-exec", "-p", seatbeltProfile(settings), binary)
		if err != nil {
			t.Fatalf("probe under a developer's settings: %v: %s", err, stderr)
		}
		if found.Server != probeSucceeded {
			t.Errorf("the server did not answer under a developer's settings: %s", found.Server)
		}
		for what, outcome := range map[string]string{"the credential": found.Credential, "the socket file": found.Socket} {
			if !strings.Contains(outcome, "not permitted") {
				t.Errorf("under a developer's settings, reaching %s came to %q, want it refused", what, outcome)
			}
		}
		return
	}
	if found, stderr, err := probe(binary); err != nil || found.Server != probeSucceeded {
		t.Fatalf("the server did not answer: %+v, %v: %s", found, err, stderr)
	}
}

// servicesBeside is the services folder of the state directory a run's scratch
// folder was cut in.
func servicesBeside(scratch string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(scratch)), "services")
}

// sessionServicesSettings is the settings a session with no run folder is
// launched with over a state directory, and the tools it is given.
func sessionServicesSettings(t *testing.T, state string, request backendapi.RunRequest) (encoded string, settings servicesSettings, tools string) {
	t.Helper()
	runner := &fakeRunner{results: claudeCompletedTurn()}
	if _, err := (Backend{Runner: runner, Clock: fixedClock{}, StateRoot: state}).Run(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	encoded, _ = optionValue(runner.commands[0].Args, "--settings")
	if err := json.Unmarshal([]byte(encoded), &settings); err != nil {
		t.Fatal(err)
	}
	tools, _ = optionValue(runner.commands[0].Args, "--tools")
	return encoded, settings, tools
}

// Every session of a role other than the developer -- a review, a management
// conversation, a helper answering for a role -- is refused the services folder
// of the adapter's state directory, fresh and resumed. A reviewer, which has
// tools, is refused it by its shell's sandbox too; every other one has none, and
// the deny rules are what stop its prompt naming a file there. Nothing else is
// refused for reading that was not before. One whose folder cannot be found is
// not started.
func TestEverySessionOfAnyOtherRoleIsRefusedTheServicesFolder(t *testing.T) {
	t.Parallel()
	state, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	services := filepath.Join(state, "services")
	denied := []string{"Read(/" + services + ")", "Read(/" + services + "/**)"}
	for _, role := range domain.Roles() {
		if role == domain.RoleDeveloper {
			continue
		}
		for name, request := range map[string]backendapi.RunRequest{
			"a fresh session":   {},
			"a resumed session": {SessionID: "session-one"},
			// As repositoryask launches the reading helper.
			"a helper": {AllowedTools: []string{}},
		} {
			request.RunID, request.Role, request.WorkingDirectory, request.Prompt = testRunID, role, t.TempDir(), "answer"
			encoded, settings, tools := sessionServicesSettings(t, state, request)
			var reads []string
			for _, rule := range settings.Permissions.Deny {
				if strings.HasPrefix(rule, "Read(") {
					reads = append(reads, rule)
				}
			}
			if !slices.Equal(reads, denied) {
				t.Errorf("%s, %s: reading is refused by %q, want %q", role, name, reads, denied)
			}
			if role == domain.RoleReviewer {
				network := settings.Sandbox.Network
				if tools != "Read,Glob,Grep,Bash" || !slices.Equal(settings.Sandbox.Filesystem.DenyRead, []string{services}) || len(settings.Sandbox.Filesystem.AllowRead) != 0 {
					t.Errorf("%s: tools = %q and settings = %s, want its reads kept and its shell refused %q alone", name, tools, encoded, services)
				}
				if network.AllowLocalBinding || network.AllowAllUnixSockets == nil || *network.AllowAllUnixSockets || len(network.AllowUnixSockets) != 0 {
					t.Errorf("%s: sandbox network = %+v, want no socket file allowed", name, network)
				}
			} else if tools != "" || len(settings.Permissions.Allow) != 0 {
				t.Errorf("%s, %s: tools = %q and settings = %s, want no tool", role, name, tools, encoded)
			}
			for _, missing := range []string{"", filepath.Join(state, "state*"), filepath.Join(state, "absent")} {
				refused := &fakeRunner{}
				if _, err := (Backend{Runner: refused, StateRoot: missing}).Run(context.Background(), request); err == nil || len(refused.commands) != 0 {
					t.Errorf("%s, %s: Run() over state directory %q = %v, commands = %v, want a refusal", role, name, missing, err, refused.commands)
				}
			}
		}
	}
	if info, err := os.Stat(services); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("services folder = %v, %v; want it made for its owner only", info, err)
	}
}

// The installed CLI is asked, against a local stand-in for the provider, what it
// sends when a prompt names a file in the services folder and one outside it:
// the second, and nothing of the first, in a session with no tools, and in a
// reviewer's. The control drops the deny rules and has to see the first sent,
// since without that the check would pass on a CLI that never read either. It
// skips where Claude Code is not installed.
func TestTheInstalledCLIIsRefusedTheServicesFolderInAPrompt(t *testing.T) {
	t.Parallel()
	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Skipf("Claude Code is not installed, so what it reads cannot be asked here: %v", err)
	}
	state, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(state, "outside")
	if err := os.WriteFile(outside, []byte("text-of-the-other-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := execution.PrepareServicesDirectory(state)
	if err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(services, "token")
	if err := os.WriteFile(token, []byte("text-of-the-credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleReviewer} {
		worktree := t.TempDir()
		if err := os.Symlink(token, filepath.Join(worktree, "link")); err != nil {
			t.Fatal(err)
		}
		request := backendapi.RunRequest{RunID: testRunID, Role: role, WorkingDirectory: worktree, Prompt: "answer"}
		runner := &fakeRunner{results: claudeCompletedTurn()}
		if _, err := (Backend{Runner: runner, Clock: fixedClock{}, StateRoot: state}).Run(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		args := slices.Clone(runner.commands[0].Args)
		prompt := fmt.Sprintf("judge @%s and @link and @%s now", token, outside)
		sent, output := sentToTheProvider(t, binary, worktree, prompt, args)
		if !strings.Contains(sent, "text-of-the-other-file") || strings.Contains(sent, "text-of-the-credential") {
			t.Errorf("%s: sent the other file %t and the credential %t, want only the other file: %s", role,
				strings.Contains(sent, "text-of-the-other-file"), strings.Contains(sent, "text-of-the-credential"), output)
		}
		settings := slices.Index(args, "--settings") + 1
		args[settings] = strings.ReplaceAll(args[settings], services, filepath.Join(state, "elsewhere"))
		if sent, output := sentToTheProvider(t, binary, worktree, prompt, args); !strings.Contains(sent, "text-of-the-credential") {
			t.Errorf("%s: without the denial the credential was not sent either, so this check shows nothing: %s", role, output)
		}
	}
}

// sentToTheProvider runs the installed CLI once with a provider that is a local
// server refusing every request, and is every request body it was sent.
func sentToTheProvider(t *testing.T, binary, directory, prompt string, args []string) (string, string) {
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
	command.Env = append(execution.ExplicitEnvironment(nil), "CLAUDE_CONFIG_DIR="+t.TempDir(), "ANTHROPIC_BASE_URL="+server.URL,
		"ANTHROPIC_API_KEY=stand-in", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1")
	// The CLI exits with a failure, the stand-in having refused it; what it sent
	// first is the answer.
	output, _ := command.CombinedOutput()
	mutex.Lock()
	defer mutex.Unlock()
	return sent.String(), string(output)
}
