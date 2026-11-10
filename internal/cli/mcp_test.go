package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/mcpcall"
	"github.com/mason-bryant/yoyodyne/internal/mcpcredential"
)

const mcpCLIConfig = validConfig + `    mcp_tools: [mail.search]
  pm:
    role: product-manager
    backend: claude-code
    model: opus
    mcp_tools: [mail.search, mail.send]
mcp:
  servers:
    mail:
      command: [mail-mcp, --stdio]
      login: [mail-mcp, login, --fresh]
      tools:
        search: read
        send: act
    tickets:
      url: https://mcp.example.com/
      credential: file
      tools:
        list_open: read
`

type recordedLogin struct {
	directory   string
	environment []string
	command     []string
}

// stubMCPLogin replaces the login runner for one test, recording what would run.
func stubMCPLogin(t *testing.T) *[]recordedLogin {
	t.Helper()
	var recorded []recordedLogin
	previous := mcpLoginRunner
	mcpLoginRunner = func(_ context.Context, directory string, environment, command []string, _ io.Reader, _, _ io.Writer) error {
		recorded = append(recorded, recordedLogin{directory: directory, environment: environment, command: command})
		return nil
	}
	t.Cleanup(func() { mcpLoginRunner = previous })
	return &recorded
}

// `yoyo mcp login` is a person's act: a process the harness launched for a role
// is refused it, and nothing is run.
func TestMCPLoginIsRefusedToAnAgentsShell(t *testing.T) {
	// Not parallel: the state root, the marker, and the runner are this process's.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	recorded := stubMCPLogin(t)
	configPath := writeConfig(t, mcpCLIConfig)
	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleProductManager} {
		t.Setenv(execution.AgentRoleVariable, string(role))
		stdout, stderr, code := runCLI(t, "mcp", "login", "mail", "--config", configPath)
		if code != 1 || !strings.Contains(stderr, "refused from a process the harness launched for the "+string(role)) || !strings.Contains(stderr, "a person logs an MCP server in") {
			t.Fatalf("mcp login as the %s: code %d, stdout %q, stderr %q; want it refused", role, code, stdout, stderr)
		}
	}
	if len(*recorded) != 0 {
		t.Fatalf("a refused login ran %v", *recorded)
	}
}

// At the operator's terminal it runs the declared login in the server's own
// folder under the state directory, with that folder as its home.
func TestMCPLoginRunsTheDeclaredLoginInTheServersOwnFolder(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	t.Setenv(execution.AgentRoleVariable, "")
	t.Setenv("XDG_CONFIG_HOME", "/somewhere/else")
	recorded := stubMCPLogin(t)
	configPath := writeConfig(t, mcpCLIConfig)

	stdout, stderr, code := runCLI(t, "mcp", "login", "mail", "--config", configPath)
	if code != 0 {
		t.Fatalf("mcp login: code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if len(*recorded) != 1 {
		t.Fatalf("mcp login ran %d logins, want one", len(*recorded))
	}
	login := (*recorded)[0]
	if strings.Join(login.command, " ") != "mail-mcp login --fresh" {
		t.Fatalf("ran %v, want the declared login", login.command)
	}
	resolvedRoot, err := filepath.EvalSymlinks(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := mcpcredential.ServerDirectory(resolvedRoot, "yoyodyne", "mail")
	if login.directory != want {
		t.Fatalf("ran in %s, want %s", login.directory, want)
	}
	info, err := os.Stat(want)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("the server's folder = %v, %v; want a directory only its owner reads", info, err)
	}
	homes := 0
	for _, entry := range login.environment {
		if strings.HasPrefix(entry, "HOME=") {
			homes++
			if entry != "HOME="+want {
				t.Fatalf("the login's home is %s, want %s", entry, want)
			}
		}
		if strings.HasPrefix(entry, "XDG_") {
			t.Fatalf("the login inherited %s, which would keep its files outside its own folder", entry)
		}
	}
	if homes != 1 {
		t.Fatalf("the login's environment sets HOME %d times", homes)
	}
}

func TestMCPLoginNamesWhatToDoForAServerWithNoLogin(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	t.Setenv(execution.AgentRoleVariable, "")
	recorded := stubMCPLogin(t)
	configPath := writeConfig(t, mcpCLIConfig)

	_, stderr, code := runCLI(t, "mcp", "login", "tickets", "--config", configPath)
	if code != 1 || !strings.Contains(stderr, "declares no login") || !strings.Contains(stderr, "tickets.token") {
		t.Fatalf("mcp login tickets: code %d, stderr %q; want the command that stores the file", code, stderr)
	}
	_, stderr, code = runCLI(t, "mcp", "login", "calendar", "--config", configPath)
	if code != 1 || !strings.Contains(stderr, `declares no MCP server "calendar"`) || !strings.Contains(stderr, "mail, tickets") {
		t.Fatalf("mcp login calendar: code %d, stderr %q; want the declared servers named", code, stderr)
	}
	if len(*recorded) != 0 {
		t.Fatalf("ran %v, want nothing", *recorded)
	}
}

// `yoyo config show` reports each agent's MCP tools with their class, and a
// project naming none prints no MCP line at all.
func TestConfigShowReportsEachAgentsMCPTools(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := runCLI(t, "config", "show", "--config", writeConfig(t, mcpCLIConfig))
	if code != 0 {
		t.Fatalf("config show: code %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"# MCP tools for developer: mail.search (read)\n",
		"# MCP tools for pm: mail.search (read), mail.send (act)\n",
		"mcp_tools:",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("config show does not say %q:\n%s", want, stdout)
		}
	}

	stdout, _, code = runCLI(t, "config", "show", "--json", "--config", writeConfig(t, mcpCLIConfig))
	var payload struct {
		MCPTools map[string][]string `json:"mcp_tools"`
	}
	if code != 0 || json.Unmarshal([]byte(stdout), &payload) != nil || strings.Join(payload.MCPTools["pm"], ",") != "mail.search,mail.send" {
		t.Fatalf("config show --json mcp_tools = %v (code %d)", payload.MCPTools, code)
	}

	stdout, _, code = runCLI(t, "config", "show", "--config", writeConfig(t, validConfig))
	if code != 0 || strings.Contains(stdout, "MCP tools") || strings.Contains(stdout, "mcp:") || strings.Contains(stdout, "mcp_tools") {
		t.Fatalf("config show with no mcp section mentions MCP:\n%s", stdout)
	}
	stdout, _, _ = runCLI(t, "config", "show", "--json", "--config", writeConfig(t, validConfig))
	if strings.Contains(stdout, `"mcp`) {
		t.Fatalf("config show --json with no mcp section mentions MCP:\n%s", stdout)
	}
}

// writeMCPCalls writes calls into the product's record as the call path writes
// them: one settled line each, in a file of one process's own.
func writeMCPCalls(t *testing.T, stateRoot string, calls ...mcpcall.Record) {
	t.Helper()
	record, err := mcpcall.NewRecords(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(record.Directory(), 0o700); err != nil {
		t.Fatal(err)
	}
	var lines []byte
	for _, call := range calls {
		call.SchemaVersion, call.ProductID, call.Phase, call.PID = mcpcall.RecordSchemaVersion, "yoyodyne", mcpcall.PhaseSettled, 4242
		encoded, err := json.Marshal(call)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(append(lines, encoded...), '\n')
	}
	if err := os.WriteFile(filepath.Join(record.Directory(), "20261110-4242.jsonl"), lines, 0o600); err != nil {
		t.Fatal(err)
	}
}

// mcpCallsWithContent are three calls whose arguments, reasons and results
// carry words a listing must never print.
func mcpCallsWithContent() []mcpcall.Record {
	started := time.Now().Add(-time.Hour).UTC()
	return []mcpcall.Record{
		{Entry: "e-search", Scope: "conversation:c1/pm", StartedAt: started, EndedAt: started.Add(time.Second),
			Caller: mcpcall.CallerRecord{Kind: mcpcall.CallerConversation, Role: domain.RoleProductManager, Agent: "pm", Conversation: "c1", Turn: 3},
			Server: "mail", Tool: "mail.search", Class: "read", Arguments: json.RawMessage(`{"query":"PRIVATE-ARGUMENT"}`), Why: "PRIVATE-WHY",
			Outcome: mcpcall.OutcomePerformed, Result: "PRIVATE-RESULT", ResultBytes: 14, ResultDigest: "sha256:abc", Tries: 1},
		{Entry: "e-send", Scope: "conversation:c1/pm", StartedAt: started.Add(time.Minute),
			Caller: mcpcall.CallerRecord{Kind: mcpcall.CallerConversation, Role: domain.RoleProductManager, Agent: "pm", Conversation: "c1", Turn: 4},
			Server: "mail", Tool: "mail.send", Class: "act", ID: "a1", Arguments: json.RawMessage(`{"to":"PRIVATE-ARGUMENT"}`),
			Outcome: mcpcall.OutcomeUnknown, Reason: "the server stopped answering; whether the act happened is not known"},
		{Entry: "e-refused", Scope: "run:r1", StartedAt: started.Add(2 * time.Minute),
			Caller: mcpcall.CallerRecord{Kind: mcpcall.CallerDeveloper, Role: domain.RoleDeveloper, Agent: "developer", Run: "r1", Attempt: 1},
			Tool:   "mail.delete", Outcome: mcpcall.OutcomeRefused, Refusal: mcpcall.RefusedNotOnList},
	}
}

// `yoyo mcp calls` lists who asked for what and how it ended, with no
// argument and no result; --entry prints one call whole.
func TestMCPCallsListsTheRecordWithoutContent(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	t.Setenv(execution.AgentRoleVariable, "")
	t.Setenv("TZ", "America/Los_Angeles")
	configPath := writeConfig(t, mcpCLIConfig)

	stdout, stderr, code := runCLI(t, "mcp", "calls", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "has made no MCP calls") {
		t.Fatalf("mcp calls with no record: code %d, stdout %q, stderr %q", code, stdout, stderr)
	}

	writeMCPCalls(t, stateRoot, mcpCallsWithContent()...)
	stdout, stderr, code = runCLI(t, "mcp", "calls", "--config", configPath)
	if code != 0 {
		t.Fatalf("mcp calls: code %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"pm (product-manager) in conversation c1, turn 3 asked for mail.search (read), performed, 14 bytes back  [entry e-search]",
		`asked for mail.send (act), id "a1", outcome not known: it may have happened`,
		`developer (developer) in run r1, attempt 1 asked for "mail.delete", which is not on the caller's list, refused (not_on_list)`,
		"PST",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("mcp calls does not say %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "PRIVATE") {
		t.Fatalf("mcp calls printed a call's content:\n%s", stdout)
	}

	stdout, _, _ = runCLI(t, "mcp", "calls", "--unknown", "--config", configPath)
	if strings.Count(stdout, "[entry ") != 1 || !strings.Contains(stdout, "e-send") {
		t.Fatalf("mcp calls --unknown:\n%s", stdout)
	}
	stdout, _, _ = runCLI(t, "mcp", "calls", "--tool", "mail.search", "--config", configPath)
	if strings.Count(stdout, "[entry ") != 1 || !strings.Contains(stdout, "e-search") {
		t.Fatalf("mcp calls --tool mail.search:\n%s", stdout)
	}

	stdout, stderr, code = runCLI(t, "mcp", "calls", "--entry", "e-search", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "PRIVATE-RESULT") || !strings.Contains(stdout, "PRIVATE-ARGUMENT") || !strings.Contains(stdout, "why it was asked: PRIVATE-WHY") {
		t.Fatalf("mcp calls --entry: code %d, stdout %q, stderr %q; want the call whole", code, stdout, stderr)
	}
	_, stderr, code = runCLI(t, "mcp", "calls", "--entry", "e-none", "--config", configPath)
	if code != 1 || !strings.Contains(stderr, `no MCP call with the entry "e-none"`) {
		t.Fatalf("mcp calls --entry e-none: code %d, stderr %q", code, stderr)
	}
	stdout, _, code = runCLI(t, "mcp", "calls", "--json", "--config", configPath)
	if code != 0 || strings.Count(stdout, "\n") != 3 || !strings.Contains(stdout, "PRIVATE-RESULT") {
		t.Fatalf("mcp calls --json: code %d:\n%s", code, stdout)
	}
}

// What a server returned is the asking agent's alone, so an agent's process is
// refused the two forms that print it, and still gets the list.
func TestMCPCallsContentIsRefusedToAnAgentsShell(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	t.Setenv(execution.AgentRoleVariable, string(domain.RoleDeveloper))
	configPath := writeConfig(t, mcpCLIConfig)
	writeMCPCalls(t, stateRoot, mcpCallsWithContent()...)

	for _, args := range [][]string{{"--entry", "e-search"}, {"--json"}} {
		stdout, stderr, code := runCLI(t, append([]string{"mcp", "calls", "--config", configPath}, args...)...)
		if code != 1 || strings.Contains(stdout, "PRIVATE") || !strings.Contains(stderr, "refused from a process the harness launched for the developer") {
			t.Fatalf("mcp calls %v as the developer: code %d, stdout %q, stderr %q; want it refused", args, code, stdout, stderr)
		}
	}
	stdout, _, code := runCLI(t, "mcp", "calls", "--config", configPath)
	if code != 0 || strings.Count(stdout, "[entry ") != 3 || strings.Contains(stdout, "PRIVATE") {
		t.Fatalf("mcp calls as the developer: code %d:\n%s", code, stdout)
	}
}
