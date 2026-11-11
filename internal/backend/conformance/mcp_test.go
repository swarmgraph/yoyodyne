package conformance

// No agent session holds an MCP server, a connector, or the credentials for
// either (docs/designs/mcp-tools-through-the-harness.md, section 6, and the
// invariant no-agent-session-holds-an-mcp-server).
//
// Each adapter keeps servers out its own way, and each is tested beside its own
// code: Claude Code by --strict-mcp-config and the connector settings
// (claudecode/context.go), Codex by launching a read-only role from an empty
// directory with the account's configuration ignored and by turning off, by
// name, every server a developer's configuration lists (codex/mcp.go). What no
// adapter's own tests can say is that the rule holds for every role on every
// provider at once, with a project that declares servers and stores their
// credentials, and with the provider's own files listing servers of their own.
// That is what this file says: a launch any adapter makes for any role carries
// no server, connector, or credential in its arguments, its environment, the
// directory it is started in, or the provider home it reads.
//
// The second half is what happens if that ever stops being true: an MCP call
// seen in a provider's own stream is access no role was launched with, and it
// files the harness's critical report whichever provider and role it was.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	compiled "github.com/mason-bryant/yoyodyne/internal/backend/adapters"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/mcpcredential"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/spend"
	"github.com/mason-bryant/yoyodyne/internal/tooluse"
)

// containmentProduct is the product the configured servers belong to.
const containmentProduct = domain.ProductID("containment")

// containmentConfig declares two servers, one the harness would start and one
// it would reach, gives every role an agent on each provider, and gives each
// role whose bundle holds a class of MCP tool a list of them. Server and tool
// names are unusual on purpose: each is looked for as a substring of
// everything a launch carries, so an ordinary word would match by accident.
const containmentConfig = `version: 1
product:
  id: containment
  repository: .
execution:
  max_concurrent_developers: 1
  repair_attempts_before_replan: 2
  worktree_root: auto
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
checks:
  - make test
mcp:
  servers:
    zqmailsrv:
      command: [zqmail-mcp-binary, --stdio]
      login: [zqmail-mcp-binary, login]
      credential: file
      credential_env: ZQMAIL_TOKEN
      tools:
        zqsearch: read
        zqsend: act
    zqticketsrv:
      url: https://zqtickets.example.invalid/mcp
      credential: file
      tools:
        zqlistopen: read
agents:
  developer-claude:
    role: developer
    backend: claude-code
    model: opus
    instances: 1
    mcp_tools: [zqmailsrv.zqsearch, zqticketsrv.zqlistopen]
  developer-codex:
    role: developer
    backend: codex
    model: gpt-6.1-sol
    instances: 1
    mcp_tools: [zqmailsrv.zqsearch]
  pm:
    role: product-manager
    backend: claude-code
    model: opus
    instances: 1
    mcp_tools: [zqmailsrv.zqsearch, zqmailsrv.zqsend, zqticketsrv.zqlistopen]
  architect:
    role: architect
    backend: codex
    model: gpt-6.1-sol
    instances: 1
    mcp_tools: [zqmailsrv.zqsend]
  dm:
    role: development-manager
    backend: claude-code
    model: opus
    instances: 1
    mcp_tools: [zqticketsrv.zqlistopen]
  program:
    role: program-manager
    backend: codex
    model: gpt-6.1-sol
    instances: 1
    mcp_tools: [zqmailsrv.zqsearch]
  reviewer:
    role: reviewer
    backend: claude-code
    model: opus
    instances: 1
`

// The servers the provider's own files list: the account's, in the provider
// home, and the repository's, in the worktree. Neither is the project's; both
// are what the CLI would start on its own if the adapter let it.
const (
	accountServer    = "zqaccountsrv"
	repositoryServer = "zqrepo.srv"
)

// Credential values, each stored where its kind of credential lives: the
// project's file store, a local server's own login directory, and the
// harness's own environment under the variable a server's credential_env
// names.
const (
	storedToken      = "zq-stored-token-7f3a91"
	loginSecret      = "zq-login-secret-52bd07"
	environmentToken = "zq-environment-token-c18e44"
	accountToken     = "zq-account-token-0d9b6e"
)

// containment is one machine set up as the test asks: a project whose
// configuration declares servers, with their credentials stored, and provider
// files that list servers of their own.
type containment struct {
	cfg         config.Config
	stateRoot   string
	repository  string
	worktree    string
	accountHome map[domain.Backend]string
	// markers are strings no launch may carry anywhere, each with what it is.
	markers map[string]string
	// providerServers are the server names the provider's own files list, which
	// a launch may name only to turn off.
	providerServers []string
}

func newContainment(t *testing.T) *containment {
	t.Helper()
	cfg, err := config.Decode(strings.NewReader(containmentConfig))
	if err != nil {
		t.Fatalf("the containment configuration does not load: %v", err)
	}
	if !cfg.MCP.Declared() {
		t.Fatal("the containment configuration declares no MCP server, so this test asks nothing")
	}
	c := &containment{
		cfg:         cfg,
		stateRoot:   t.TempDir(),
		accountHome: map[domain.Backend]string{},
		markers: map[string]string{
			storedToken:      "a server's stored credential",
			loginSecret:      "a local server's own login",
			environmentToken: "a credential in the harness's environment",
			accountToken:     "a credential the provider account's server configuration holds",
			"ZQMAIL_TOKEN":   "the variable a server's credential is handed in",
		},
		providerServers: []string{accountServer, repositoryServer},
	}

	// Every server the project declares, its command, its address, and its
	// tools are markers; and every one has its credential stored, and the local
	// one a login of its own.
	for name, server := range cfg.MCP.Servers {
		c.markers[name] = "a server the project declares"
		for _, part := range server.Command {
			if !strings.HasPrefix(part, "-") {
				c.markers[part] = "the command of a server the project declares"
			}
		}
		if server.URL != "" {
			c.markers[server.URL] = "the address of a server the project declares"
			c.markers["zqtickets.example.invalid"] = "the host of a server the project declares"
		}
		for tool := range server.Tools {
			c.markers[tool] = "a tool the project declares"
		}
		credential := mcpcredential.File(c.stateRoot, containmentProduct, name)
		writeFile(t, credential, storedToken+"\n", 0o600)
		if len(server.Command) > 0 {
			writeFile(t, filepath.Join(mcpcredential.ServerDirectory(c.stateRoot, containmentProduct, name), "login.json"),
				`{"refresh_token":"`+loginSecret+`"}`, 0o600)
		}
	}
	c.markers[mcpcredential.Directory(c.stateRoot, containmentProduct)] = "the project's MCP directory"
	c.markers[mcpcredential.KeychainItem(containmentProduct, "zqmailsrv")] = "a server's keychain item"

	// A repository and a linked worktree of it, as a run is given.
	c.repository = filepath.Join(t.TempDir(), "repository")
	administrative := filepath.Join(c.repository, ".git", "worktrees", "one")
	c.worktree = filepath.Join(t.TempDir(), "worktree")
	writeFile(t, filepath.Join(c.worktree, ".git"), "gitdir: "+administrative+"\n", 0o644)
	writeFile(t, filepath.Join(administrative, "commondir"), "../..\n", 0o644)

	// The repository's own files list a server for each provider and ask for
	// every one of them to be used.
	writeFile(t, filepath.Join(c.worktree, ".mcp.json"),
		`{"mcpServers":{"`+repositoryServer+`":{"command":"zqrepo-mcp-binary","env":{"TOKEN":"`+accountToken+`"}}}}`, 0o644)
	writeFile(t, filepath.Join(c.worktree, ".claude", "settings.json"),
		`{"enableAllProjectMcpServers":true,"enabledMcpjsonServers":["`+repositoryServer+`"]}`, 0o644)
	writeFile(t, filepath.Join(c.worktree, ".codex", "config.toml"),
		"[mcp_servers.\""+repositoryServer+"\"]\ncommand = \"zqrepo-mcp-binary\"\nenv = { TOKEN = \""+accountToken+"\" }\n", 0o644)

	// Each provider account's home lists a server of the account's own. The
	// Codex home also names an instruction file, so the adapter prepares a home
	// of its own from it, and that prepared home is asked about too.
	claudeHome := filepath.Join(t.TempDir(), "claude-account")
	writeFile(t, filepath.Join(claudeHome, ".claude.json"),
		`{"mcpServers":{"`+accountServer+`":{"type":"http","url":"https://zqaccount.example.invalid/mcp","headers":{"Authorization":"Bearer `+accountToken+`"}}}}`, 0o600)
	writeFile(t, filepath.Join(claudeHome, "settings.json"), `{"enableAllProjectMcpServers":true}`, 0o600)
	c.accountHome[domain.BackendClaudeCode] = claudeHome
	codexHome := filepath.Join(t.TempDir(), "codex-account")
	writeFile(t, filepath.Join(codexHome, "config.toml"),
		"model_instructions_file = \"personal.md\"\n\n[mcp_servers."+accountServer+"]\nurl = \"https://zqaccount.example.invalid/mcp\"\nbearer_token = \""+accountToken+"\"\n", 0o600)
	writeFile(t, filepath.Join(codexHome, "AGENTS.md"), "personal instructions\n", 0o600)
	c.accountHome[domain.BackendCodex] = codexHome
	return c
}

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// launch is one invocation the test asks an adapter to make.
type launch struct {
	role      domain.AgentRole
	posture   backend.Posture
	session   string
	subagents bool
}

func (l launch) String() string {
	name := string(l.role)
	if l.posture != "" {
		name += " held " + string(l.posture)
	}
	if l.subagents {
		name += " with sub-agents"
	}
	if l.session != "" {
		return name + ", resumed"
	}
	return name + ", fresh"
}

// launches is every role, fresh and resumed, and the developer also held
// read-only and, where the provider offers them, given sub-agents.
func launches(descriptor backend.Descriptor) []launch {
	var all []launch
	for _, role := range domain.Roles() {
		for _, session := range []string{"", "session-1"} {
			all = append(all, launch{role: role, session: session})
		}
	}
	all = append(all, launch{role: domain.RoleDeveloper, posture: backend.PostureReadOnly})
	if descriptor.Subagents.Eligible() {
		all = append(all, launch{role: domain.RoleDeveloper, subagents: true})
	}
	return all
}

// TestNoRoleOnAnyProviderIsLaunchedWithAnMCPServerOrItsCredentials is build
// piece 8's first claim. Every adapter this build ships launches every role
// with the project's servers configured and their credentials stored, and with
// the harness's own environment and the provider's own files offering servers
// too, and each command it makes is read as the provider would receive it.
//
// It sets the harness's environment, so it does not run beside other tests.
func TestNoRoleOnAnyProviderIsLaunchedWithAnMCPServerOrItsCredentials(t *testing.T) {
	// The harness's own environment carries a credential under the variable a
	// server's credential_env names, and asks Claude Code for the claude.ai
	// account's connectors: neither may reach a session.
	t.Setenv("ZQMAIL_TOKEN", environmentToken)
	t.Setenv("ENABLE_CLAUDEAI_MCP_SERVERS", "true")

	// One machine serves every launch, as one machine serves every run: each
	// launch is read on its own, and whatever an earlier one left in a home is
	// what the next one finds there.
	c := newContainment(t)
	for _, adapter := range BuiltInAdapters() {
		descriptor := adapter.Descriptor
		for _, each := range launches(descriptor) {
			t.Run(string(descriptor.ID)+"/"+each.String(), func(t *testing.T) {
				runner := &containmentRunner{containment: c, provider: descriptor.Adapter}
				provider, built := compiled.For(descriptor, descriptor.ID, runner, "", sampleExecutable)
				if !built {
					t.Fatalf("this build cannot construct the %s adapter", descriptor.ID)
				}
				request := backend.RunRequest{
					RunID:            conformanceRunID,
					Role:             each.role,
					Posture:          each.posture,
					Model:            modelFor(descriptor),
					WorkingDirectory: c.worktree,
					RepositoryRoot:   c.repository,
					Prompt:           "do the work",
					SessionID:        each.session,
					AccountConfigDir: c.accountHome[descriptor.Adapter],
				}
				if each.subagents {
					request.Subagents = &backend.SubagentRequest{Model: "sonnet"}
				}
				if _, err := provider.Run(context.Background(), request); err != nil {
					t.Fatalf("Run() error = %v", err)
				}
				if runner.turns == 0 {
					t.Fatal("the adapter made no turn, so nothing here was asked")
				}
				for _, problem := range runner.problems {
					t.Error(problem)
				}
			})
		}
	}
}

func modelFor(descriptor backend.Descriptor) string {
	if descriptor.Adapter == domain.BackendCodex {
		return "gpt-6.1-sol"
	}
	return "opus"
}

// containmentRunner stands in for the provider's executable. Every command an
// adapter makes is read at the moment it is made, while the directory it is
// started in and the home it reads still exist, and what is wrong with it is
// kept. A Codex listing is answered as the CLI would answer it, from the
// configuration files the command would read; a turn is answered with a short
// successful stream.
type containmentRunner struct {
	containment *containment
	provider    domain.Backend
	turns       int
	problems    []string
}

func (r *containmentRunner) Run(_ context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	listing := len(command.Args) >= 2 && command.Args[0] == "mcp" && command.Args[1] == "list"
	stdin := ""
	if command.Stdin != nil {
		read, _ := io.ReadAll(command.Stdin)
		stdin = string(read)
	}
	r.problems = append(r.problems, r.containment.problems(r.provider, command, stdin, listing)...)
	if listing {
		servers := []map[string]any{}
		for _, name := range r.containment.listedFor(command) {
			servers = append(servers, map[string]any{"name": name, "enabled": true})
		}
		encoded, _ := json.Marshal(servers)
		observer(execution.Output{Stream: execution.StreamStdout, Text: string(encoded)})
		return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
	}
	r.turns++
	stream := []string{
		`{"type":"system","subtype":"init","session_id":"session-1","model":"claude-test"}`,
		`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done","total_cost_usd":0.01}`,
	}
	if r.provider == domain.BackendCodex {
		stream = []string{
			`{"type":"thread.started","thread_id":"session-1"}`,
			`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"done"}}`,
			`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`,
		}
	}
	for _, line := range stream {
		observer(execution.Output{Stream: execution.StreamStdout, Text: line})
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: strings.Join(stream, "\n") + "\n"}, nil
}

// codexServerTable finds the server names a config.toml lists, quoted or not.
var codexServerTable = regexp.MustCompile(`(?m)^\[mcp_servers\.(?:"([^"]+)"|([^\]\s.]+))\]`)

// listedFor is every server name the configuration files a Codex command reads
// list: the config.toml in the home its environment names, and the one in the
// project directory it is started in. That is what `codex mcp list` resolves
// for the command (codex/mcp.go); TestTheInstalledCLIStartsNoMCPServerForADeveloper
// asks the real CLI the same question where one is installed.
func (c *containment) listedFor(command execution.Command) []string {
	var files []string
	if home := environmentValue(command.Env, "CODEX_HOME"); home != "" {
		files = append(files, filepath.Join(home, "config.toml"))
	}
	files = append(files, filepath.Join(command.Dir, ".codex", "config.toml"))
	var names []string
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for _, match := range codexServerTable.FindAllStringSubmatch(string(content), -1) {
			name := match[1] + match[2]
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

func environmentValue(environment []string, name string) string {
	value := ""
	for _, entry := range environment {
		if strings.HasPrefix(entry, name+"=") {
			value = strings.TrimPrefix(entry, name+"=")
		}
	}
	return value
}

// problems is everything wrong with one command: a marker anywhere it carries,
// an environment that would hand a session a server or a connector, provider
// files listing a server the command leaves on, and a home or launch directory
// holding anything of the project's MCP setup.
func (c *containment) problems(provider domain.Backend, command execution.Command, stdin string, listing bool) []string {
	var found []string
	say := func(format string, args ...any) {
		what := "the turn"
		if listing {
			what = "the server listing"
		}
		found = append(found, what+": "+fmt.Sprintf(format, args...))
	}
	look := func(where, text string) {
		for marker, what := range c.markers {
			if strings.Contains(text, marker) {
				say("%s carries %s (%q)", where, what, marker)
			}
		}
	}
	for index, arg := range command.Args {
		look(fmt.Sprintf("argument %d", index), arg)
	}
	for _, entry := range command.Env {
		look("the environment", entry)
	}
	look("the prompt", stdin)
	look("the directory it is started in", command.Dir)

	// The places the session reads files from: the directory it starts in and
	// the provider home its environment names. Neither may hold anything of the
	// project's MCP setup.
	homes := map[domain.Backend]string{domain.BackendClaudeCode: "CLAUDE_CONFIG_DIR", domain.BackendCodex: "CODEX_HOME"}
	home := environmentValue(command.Env, homes[provider])
	for _, directory := range []string{command.Dir, home} {
		if directory == "" {
			continue
		}
		found = append(found, c.directoryProblems(directory, listing)...)
	}

	if listing {
		return found
	}
	switch provider {
	case domain.BackendClaudeCode:
		found = append(found, c.claudeProblems(command)...)
	case domain.BackendCodex:
		found = append(found, c.codexProblems(command)...)
	}
	return found
}

// directoryProblems walks a directory a session reads and reports any file in
// it that holds a marker. The account's own files are expected to list the
// account's servers, which the adapter turns off; what they may never hold is
// anything the harness configured or stored.
func (c *containment) directoryProblems(directory string, listing bool) []string {
	var found []string
	what := "the turn"
	if listing {
		what = "the server listing"
	}
	_ = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for marker, kind := range c.markers {
			if marker == accountToken {
				// The account's own credential is the account's, in its own files.
				continue
			}
			if strings.Contains(string(content), marker) {
				found = append(found, fmt.Sprintf("%s: %s, which the session reads, holds %s (%q)", what, path, kind, marker))
			}
		}
		return nil
	})
	return found
}

// claudeProblems is what a Claude Code turn has to carry to start no server:
// MCP servers read only from --mcp-config, which is never passed, the
// claude.ai connectors off in both the settings passed and the environment,
// and no provider-listed server named anywhere on it.
func (c *containment) claudeProblems(command execution.Command) []string {
	var found []string
	if !slices.Contains(command.Args, "--strict-mcp-config") {
		found = append(found, "the turn does not pass --strict-mcp-config, so the CLI reads MCP servers from the account's home and the repository")
	}
	for _, arg := range command.Args {
		if arg == "--mcp-config" || strings.HasPrefix(arg, "--mcp-config=") {
			found = append(found, "the turn passes --mcp-config, so a server reaches the session")
		}
	}
	settings := map[string]any{}
	if at := slices.Index(command.Args, "--settings"); at < 0 || at+1 >= len(command.Args) {
		found = append(found, "the turn passes no settings, so nothing turns the claude.ai connectors off")
	} else if err := json.Unmarshal([]byte(command.Args[at+1]), &settings); err != nil {
		found = append(found, fmt.Sprintf("the turn's settings are not JSON: %v", err))
	}
	if settings["disableClaudeAiConnectors"] != true {
		found = append(found, "the turn's settings do not turn the claude.ai connectors off")
	}
	for _, key := range []string{"enableAllProjectMcpServers", "enabledMcpjsonServers", "mcpServers"} {
		if _, set := settings[key]; set {
			found = append(found, fmt.Sprintf("the turn's settings set %s", key))
		}
	}
	if got := environmentValue(command.Env, "ENABLE_CLAUDEAI_MCP_SERVERS"); got != "false" {
		found = append(found, fmt.Sprintf("the turn's environment has ENABLE_CLAUDEAI_MCP_SERVERS=%q, not false", got))
	}
	for _, server := range c.providerServers {
		for index, arg := range command.Args {
			if strings.Contains(arg, server) {
				found = append(found, fmt.Sprintf("argument %d names the provider-listed server %q", index, server))
			}
		}
	}
	return found
}

// codexProblems is what a Codex turn has to carry to start no server. Every
// server the files it reads list is turned off by name, in the one table the
// adapter writes; a read-only role reads neither file, so it lists none and
// names none, and is started outside the repository.
func (c *containment) codexProblems(command execution.Command) []string {
	var found []string
	listed := c.listedFor(command)
	readOnly := slices.Contains(command.Args, "--ignore-user-config")
	off := ""
	for index := 0; index+1 < len(command.Args); index++ {
		if command.Args[index] == "--config" && strings.HasPrefix(command.Args[index+1], "mcp_servers=") {
			off = command.Args[index+1]
		}
	}
	if readOnly {
		if strings.HasPrefix(command.Dir, c.worktree) || strings.HasPrefix(command.Dir, c.repository) {
			found = append(found, fmt.Sprintf("a read-only turn is started in %s, where the repository's own configuration is read", command.Dir))
		}
		if _, err := os.Stat(filepath.Join(command.Dir, ".codex")); err == nil {
			found = append(found, "a read-only turn is started in a directory holding .codex configuration")
		}
		if off != "" {
			found = append(found, "a read-only turn, which reads no server configuration, carries an MCP setting anyway")
		}
		for _, server := range c.providerServers {
			for index, arg := range command.Args {
				if strings.Contains(arg, server) {
					found = append(found, fmt.Sprintf("argument %d names the provider-listed server %q", index, server))
				}
			}
		}
		return found
	}
	if len(listed) == 0 {
		found = append(found, "the developer's turn reads no file listing a server, so this test is not asking what it means to")
	}
	for _, name := range listed {
		if !strings.Contains(off, `"`+name+`"={enabled=false}`) {
			found = append(found, fmt.Sprintf("the developer's turn leaves the server %q its configuration lists switched on", name))
		}
	}
	for _, server := range c.providerServers {
		for index, arg := range command.Args {
			if arg != off && strings.Contains(arg, server) {
				found = append(found, fmt.Sprintf("argument %d names the provider-listed server %q other than to turn it off", index, server))
			}
		}
	}
	return found
}

// The check above can fail. Each command here carries one way a server or a
// credential could reach a session, and each has to be named.
func TestTheContainmentCheckFindsEachWayAServerCouldReachASession(t *testing.T) {
	c := newContainment(t)
	clean := execution.Command{
		Args: []string{"--settings", `{"disableClaudeAiConnectors":true}`, "--strict-mcp-config"},
		Env:  []string{"ENABLE_CLAUDEAI_MCP_SERVERS=false"},
		Dir:  t.TempDir(),
	}
	if problems := c.problems(domain.BackendClaudeCode, clean, "", false); len(problems) != 0 {
		t.Fatalf("a clean Claude Code turn was found wanting: %q", problems)
	}
	doctored := func(change func(*execution.Command)) execution.Command {
		command := clean
		command.Args = slices.Clone(clean.Args)
		command.Env = slices.Clone(clean.Env)
		change(&command)
		return command
	}
	leakyHome := t.TempDir()
	writeFile(t, filepath.Join(leakyHome, ".claude.json"), `{"mcpServers":{"zqmailsrv":{"command":"zqmail-mcp-binary"}}}`, 0o600)
	cases := map[string]execution.Command{
		"a stored credential in the environment": doctored(func(command *execution.Command) {
			command.Env = append(command.Env, "ZQMAIL_TOKEN="+storedToken)
		}),
		"a server handed over on the command line": doctored(func(command *execution.Command) {
			command.Args = append(command.Args, "--mcp-config", `{"mcpServers":{}}`)
		}),
		"the provider's own servers left on": doctored(func(command *execution.Command) {
			command.Args = slices.DeleteFunc(command.Args, func(arg string) bool { return arg == "--strict-mcp-config" })
		}),
		"the claude.ai connectors left on": doctored(func(command *execution.Command) {
			command.Env = []string{"ENABLE_CLAUDEAI_MCP_SERVERS=true"}
		}),
		"a project server written into the home": doctored(func(command *execution.Command) {
			command.Env = append(command.Env, "CLAUDE_CONFIG_DIR="+leakyHome)
		}),
		"a credential in the prompt": clean,
	}
	for name, command := range cases {
		stdin := ""
		if name == "a credential in the prompt" {
			stdin = "use " + loginSecret
		}
		if problems := c.problems(domain.BackendClaudeCode, command, stdin, false); len(problems) == 0 {
			t.Errorf("%s: the check found nothing", name)
		}
	}

	// A Codex developer's turn reading a home that lists a server, with nothing
	// turning it off, is the gap the Codex developer fix closed.
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[mcp_servers."+accountServer+"]\nurl = \"https://x.invalid/\"\n", 0o600)
	unguarded := execution.Command{Args: []string{"exec", "--cd", c.worktree}, Env: []string{"CODEX_HOME=" + home}, Dir: c.worktree}
	if problems := c.problems(domain.BackendCodex, unguarded, "", false); !slices.ContainsFunc(problems, func(problem string) bool {
		return strings.Contains(problem, accountServer) && strings.Contains(problem, "switched on")
	}) {
		t.Errorf("a Codex developer's turn leaving its servers on was not found: %q", problems)
	}
}

// TestAnMCPCallInAProvidersStreamFilesTheCriticalReport is build piece 8's
// second claim. Every role is launched with no MCP server, so a call to one in
// the provider's own stream means that rule was broken somewhere. Whichever
// provider and role it was, the adapter reads it as a tool served from outside
// the provider and the harness files its critical report naming the tool.
func TestAnMCPCallInAProvidersStreamFilesTheCriticalReport(t *testing.T) {
	t.Parallel()

	streams := map[domain.Backend]map[string]string{
		domain.BackendClaudeCode: {
			// A server the CLI ran a tool of, as a tool_use block named the way
			// the CLI names MCP tools.
			"mcp__zqmailsrv__zqsend": lines(claudeInit,
				`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call-mcp","name":"mcp__zqmailsrv__zqsend","input":{"to":"someone"}}]}}`,
				`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call-mcp","content":"sent"}]}}`,
				`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done"}`),
			// A remote server the provider called on the session's behalf, which
			// arrives as a block of its own with no result.
			"zqsearch": lines(claudeInit,
				`{"type":"assistant","message":{"content":[{"type":"mcp_tool_use","id":"call-remote","name":"zqsearch","server_name":"zqmailsrv","input":{}}]}}`,
				`{"type":"result","subtype":"success","session_id":"session-1","is_error":false,"result":"done"}`),
		},
		domain.BackendCodex: {
			"zqmailsrv.zqsend": lines(
				`{"type":"thread.started","thread_id":"session-1"}`,
				`{"type":"item.started","item":{"id":"item_1","type":"mcp_tool_call","server":"zqmailsrv","tool":"zqsend","arguments":{},"status":"in_progress"}}`,
				`{"type":"item.completed","item":{"id":"item_1","type":"mcp_tool_call","server":"zqmailsrv","tool":"zqsend","arguments":{},"status":"completed"}}`,
				`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`),
		},
	}
	for _, adapter := range BuiltInAdapters() {
		descriptor := adapter.Descriptor
		samples := streams[descriptor.Adapter]
		if len(samples) == 0 {
			t.Errorf("%s: no stream says how this provider reports an MCP call, so nothing shows one would be reported", descriptor.ID)
			continue
		}
		for tool, stream := range samples {
			for _, role := range domain.Roles() {
				t.Run(fmt.Sprintf("%s/%s/%s", descriptor.ID, role, tool), func(t *testing.T) {
					t.Parallel()
					repository := t.TempDir()
					if err := os.MkdirAll(filepath.Join(repository, ".git"), 0o755); err != nil {
						t.Fatal(err)
					}
					provider, built := compiled.For(descriptor, descriptor.ID, streamRunner{sample: Sample{Stream: stream}}, "", sampleExecutable)
					if !built {
						t.Fatalf("this build cannot construct the %s adapter", descriptor.ID)
					}
					request := backend.RunRequest{
						RunID: conformanceRunID, Role: role, Model: modelFor(descriptor),
						WorkingDirectory: repository, RepositoryRoot: repository, Prompt: "do the work",
					}
					result, err := provider.Run(context.Background(), request)
					if err != nil {
						t.Fatalf("Run() error = %v", err)
					}
					var external []backend.ToolCall
					for _, call := range result.ToolCalls {
						if call.Kind == backend.ToolKindExternal {
							external = append(external, call)
						}
					}
					if len(external) != 1 || external[0].Tool != tool {
						t.Fatalf("ToolCalls = %#v, want the one call to %q read as a tool served from outside the provider", result.ToolCalls, tool)
					}

					reports := &reportPile{}
					tooluse.Watch{Reports: reports, RepositoryID: "containment", Problem: func(err error) { t.Error(err) }}.
						ObserveToolUse(spend.Attribution{ProductID: containmentProduct, Backend: descriptor.ID, RunID: "run-1", WorkItemID: "containment-1"}, request, result)
					var critical []report.Report
					for _, filed := range reports.reports {
						if filed.Severity == report.SeverityCritical {
							critical = append(critical, filed)
						}
					}
					if len(critical) != 1 {
						t.Fatalf("filed %#v, want one critical report for the MCP call", reports.reports)
					}
					filed := critical[0]
					if filed.Role != report.HarnessReporter || !strings.HasPrefix(filed.Message, tooluse.ErrorLabel) ||
						!strings.Contains(filed.Message, `"`+tool+`"`) || !strings.Contains(filed.Message, "no MCP server") {
						t.Fatalf("report = %#v, want the harness's labelled error naming the tool and the rule it means was broken", filed)
					}
				})
			}
		}
	}
}

// reportPile is an in-memory pile of reports.
type reportPile struct {
	reports []report.Report
}

func (p *reportPile) Append(filed report.Report) error {
	p.reports = append(p.reports, filed)
	return nil
}

func (p *reportPile) List() ([]report.Report, error) {
	return append([]report.Report(nil), p.reports...), nil
}
