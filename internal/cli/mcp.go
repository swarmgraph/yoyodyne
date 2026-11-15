package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/mcpcall"
	"github.com/mason-bryant/yoyodyne/internal/mcpcredential"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// runMCP is `yoyo mcp`: logging a declared MCP server in on this machine, and
// reading the record of the calls the harness made
// (docs/designs/mcp-tools-through-the-harness.md, sections 1 and 4). Making a
// call is the harness's alone and has no command here.
func runMCP(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printMCPUsage(stdout)
		return 0
	}
	switch args[0] {
	case "login":
		return loginMCPServer(ctx, args[1:], stdin, stdout, stderr)
	case "calls":
		return listMCPCalls(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown mcp command %q\n\n", args[0])
		printMCPUsage(stderr)
		return 2
	}
}

// mcpLoginRunner runs the declared login attached to the operator's terminal.
// It is a variable so a test can see what would run without running it.
var mcpLoginRunner = func(ctx context.Context, directory string, environment, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	process := exec.CommandContext(ctx, command[0], command[1:]...)
	process.Dir = directory
	process.Env = environment
	process.Stdin, process.Stdout, process.Stderr = stdin, stdout, stderr
	return process.Run()
}

// loginMCPServer runs one server's declared login at the operator's terminal,
// in the server's own folder under the state directory, which is its working
// directory and its home: the login's files land there, where the server will
// find them and no agent can read them. It is a person's act, so a process the
// harness launched for a role is refused it before anything else is read.
func loginMCPServer(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mcp login", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "mcp login takes one server name, as the mcp section of the configuration declares it")
		printMCPUsage(stderr)
		return 2
	}
	server := positional[0]
	if err := refusedToAgentProcess("yoyo mcp login", "a person logs an MCP server in, at their own terminal, so its credential never passes through an agent"); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	declared, ok := resolved.Config.MCP.Servers[server]
	if !ok {
		fmt.Fprintf(stderr, "the configuration declares no MCP server %q; it declares %s\n", server, describeMCPServers(resolved.Config.MCP))
		return 1
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	productID := resolved.Config.Product.ID
	if len(declared.Login) == 0 {
		if declared.Credential == config.MCPCredentialNone {
			fmt.Fprintf(stderr, "MCP server %q declares no login and names no credential, so there is nothing to log in\n", server)
			return 1
		}
		fmt.Fprintf(stderr, "MCP server %q declares no login; its credential is kept in the %s store, and this stores it:\n  %s\n",
			server, declared.Credential, mcpcredential.Remedy(stateRoot, productID, server, declared))
		return 1
	}

	root, err := repowrite.NewRoot(stateRoot)
	if err != nil {
		fmt.Fprintf(stderr, "the state directory %s could not be opened: %v\n", stateRoot, err)
		return 1
	}
	directory, err := root.MakeDirectory(mcpcredential.ServerDirectoryWithin(stateRoot, productID, server), 0o700)
	if err != nil {
		fmt.Fprintf(stderr, "the folder for MCP server %q could not be made: %v\n", server, err)
		return 1
	}
	fmt.Fprintf(stdout, "running the login for MCP server %q in %s\n", server, directory)
	if err := mcpLoginRunner(ctx, directory, mcpLoginEnvironment(os.Environ(), directory), declared.Login, stdin, stdout, stderr); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			fmt.Fprintf(stderr, "the login for MCP server %q exited with status %d\n", server, exit.ExitCode())
		} else {
			fmt.Fprintf(stderr, "the login for MCP server %q could not be run: %v\n", server, err)
		}
		return 1
	}
	fmt.Fprintf(stdout, "the login for MCP server %q finished\n", server)
	return 0
}

// mcpLoginEnvironment is the operator's environment with the server's folder as
// home. The XDG directories are left out so a login that honours them keeps its
// files under that home too, where the server, started with the same home, will
// look for them.
func mcpLoginEnvironment(environment []string, directory string) []string {
	kept := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if name == "HOME" || strings.HasPrefix(name, "XDG_") {
			continue
		}
		kept = append(kept, entry)
	}
	return append(kept, "HOME="+directory)
}

func describeMCPServers(section config.MCP) string {
	if !section.Declared() {
		return "none"
	}
	names := make([]string, 0, len(section.Servers))
	for name := range section.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// describeMCPTools is one agent's MCP tools as `config show` reports them, each
// with its class.
func describeMCPTools(tools []config.MCPTool) string {
	described := make([]string, 0, len(tools))
	for _, tool := range tools {
		described = append(described, fmt.Sprintf("%s (%s)", tool.Name(), tool.Class))
	}
	return strings.Join(described, ", ")
}

func printMCPUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo mcp <command> [options]

MCP is the Model Context Protocol: a way for a program, an MCP server, to offer
named tools. No agent holds a server; an agent asks the harness, and the
harness makes the call itself. docs/mcp.md says how to set one up.

Commands:
  login <server>    log a declared MCP server in on this machine, at your terminal
  calls             list the MCP calls the harness made, from its record

yoyo mcp login <server> [--config <path>]

  Runs the login command the server's entry in the configuration declares, at
  your terminal, with the server's own folder under the state directory as its
  working directory and its home, so the files it writes stay where the server
  finds them and no agent can read them. It is refused to a process the
  harness started for a role. A server whose credential is stored by hand
  declares no login, and this prints the command that stores it instead.

yoyo mcp calls [--since <duration>] [--tool <server.tool>] [--unknown]
               [--entry <id>] [--json] [--config <path>]

  Lists every call in the record, oldest first: when it started, who asked,
  the tool, and how it ended, with the size of what came back. It shows no
  arguments and no result. --entry prints one call whole, with the arguments
  as recorded and the result as kept, both with secrets removed; --json prints
  every listed call whole. Those two show what a server returned, which may be
  private, so they are refused to a process the harness started for a role.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --since <d>       only calls that started within this long, such as 24h
  --tool <name>     only calls of this tool, written as server and tool
  --unknown         only act calls whose outcome is not known
  --entry <id>      one call, by the entry the list names, printed whole
  --json            every listed call as one JSON object per line`)
}

// listMCPCalls is `yoyo mcp calls`: the record of MCP calls, read back. The
// list names, outcomes, and sizes; a call's arguments and result are printed
// only for --entry and --json, which a process launched for a role is refused,
// because a result may be private and is the asking agent's alone.
func listMCPCalls(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mcp calls", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	since := flags.Duration("since", 0, "only calls that started within this long")
	toolName := flags.String("tool", "", "only calls of this tool, written as server and tool")
	unknown := flags.Bool("unknown", false, "only act calls whose outcome is not known")
	entry := flags.String("entry", "", "one call, by its entry, printed whole")
	asJSON := flags.Bool("json", false, "every listed call whole, one JSON object per line")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "mcp calls takes no arguments, only options")
		printMCPUsage(stderr)
		return 2
	}
	if *since < 0 {
		fmt.Fprintln(stderr, "--since must not be negative")
		return 2
	}
	if *entry != "" || *asJSON {
		if err := refusedToAgentProcess("yoyo mcp calls --entry and --json", "they print what MCP servers returned, which may be private and reaches only the agent that asked for it"); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	record, err := mcpcall.NewRecords(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	calls, unreadable, err := record.Read()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if unreadable > 0 {
		fmt.Fprintf(stderr, "%d line(s) of the record in %s could not be read\n", unreadable, record.Directory())
	}
	zone := operatorZone()
	if *entry != "" {
		for _, call := range calls {
			if call.Entry == *entry {
				printMCPCallWhole(stdout, call, zone)
				return 0
			}
		}
		fmt.Fprintf(stderr, "the record in %s holds no MCP call with the entry %q\n", record.Directory(), *entry)
		return 1
	}
	now := time.Now()
	var listed []mcpcall.Record
	for _, call := range calls {
		if *since > 0 && call.StartedAt.Before(now.Add(-*since)) {
			continue
		}
		if *toolName != "" && (call.Server == "" || call.Tool != *toolName) {
			continue
		}
		if *unknown && (call.Outcome != mcpcall.OutcomeUnknown || call.Class != config.MCPAct) {
			continue
		}
		listed = append(listed, call)
	}
	if *asJSON {
		encoder := json.NewEncoder(stdout)
		for _, call := range listed {
			if err := encoder.Encode(call); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
		return 0
	}
	if len(listed) == 0 {
		if len(calls) == 0 {
			fmt.Fprintf(stdout, "the harness has made no MCP calls for %s on this machine\n", resolved.Config.Product.ID)
		} else {
			fmt.Fprintf(stdout, "no MCP call in the record matches; it holds %d in all\n", len(calls))
		}
		return 0
	}
	for _, call := range listed {
		fmt.Fprintln(stdout, describeMCPCall(call, zone))
	}
	return 0
}

// mcpCallTool is the tool as the list names it. A call refused as not on the
// caller's list has no server, and its name is the agent's own text, so it is
// said as that.
func mcpCallTool(call mcpcall.Record) string {
	if call.Server == "" {
		return fmt.Sprintf("%q, which is not on the caller's list", call.Tool)
	}
	return fmt.Sprintf("%s (%s)", call.Tool, call.Class)
}

// mcpCaller is who asked, in words.
func mcpCaller(caller mcpcall.CallerRecord) string {
	switch caller.Kind {
	case mcpcall.CallerConversation:
		if caller.Conversation == "" {
			return fmt.Sprintf("%s (%s) in pass %s", caller.Agent, caller.Role, caller.Pass)
		}
		return fmt.Sprintf("%s (%s) in conversation %s, turn %d", caller.Agent, caller.Role, caller.Conversation, caller.Turn)
	case mcpcall.CallerDeveloper:
		return fmt.Sprintf("%s (developer) in run %s, attempt %d", caller.Agent, caller.Run, caller.Attempt)
	case mcpcall.CallerScheduled:
		return "recurring task " + caller.Task
	}
	return string(caller.Kind)
}

// mcpCallOutcome is how a call ended, with the refusal's reason or what came
// back, by size and never by content.
func mcpCallOutcome(call mcpcall.Record) string {
	switch call.Outcome {
	case mcpcall.OutcomeRefused:
		return "refused (" + string(call.Refusal) + ")"
	case mcpcall.OutcomePerformed, mcpcall.OutcomeFailed:
		if call.ResultBytes == 0 && call.ResultDigest == "" {
			return string(call.Outcome)
		}
		words := fmt.Sprintf("%s, %d bytes back", call.Outcome, call.ResultBytes)
		if call.ResultCut {
			words += ", the record kept part of it"
		}
		if len(call.Dropped) > 0 {
			words += fmt.Sprintf(", %d item(s) dropped as not text", len(call.Dropped))
		}
		return words
	case mcpcall.OutcomeUnknown:
		return "outcome not known: it may have happened; check before asking again"
	}
	return string(call.Outcome)
}

// describeMCPCall is one call on one line, with no arguments and no result.
func describeMCPCall(call mcpcall.Record, zone *time.Location) string {
	id := ""
	if call.ID != "" {
		id = fmt.Sprintf(" id %q,", call.ID)
	}
	return fmt.Sprintf("%s  %s asked for %s,%s %s  [entry %s]",
		call.StartedAt.In(zone).Format("2006-01-02 15:04:05 MST"), mcpCaller(call.Caller), mcpCallTool(call), id, mcpCallOutcome(call), call.Entry)
}

// printMCPCallWhole is one call with everything the record kept of it.
func printMCPCallWhole(writer io.Writer, call mcpcall.Record, zone *time.Location) {
	fmt.Fprintf(writer, "entry:      %s\n", call.Entry)
	fmt.Fprintf(writer, "asked by:   %s\n", mcpCaller(call.Caller))
	fmt.Fprintf(writer, "tool:       %s\n", mcpCallTool(call))
	if call.ID != "" {
		fmt.Fprintf(writer, "id:         %s\n", call.ID)
	}
	fmt.Fprintf(writer, "started:    %s\n", call.StartedAt.In(zone).Format("2006-01-02 15:04:05 MST"))
	if !call.EndedAt.IsZero() {
		fmt.Fprintf(writer, "ended:      %s\n", call.EndedAt.In(zone).Format("2006-01-02 15:04:05 MST"))
	}
	fmt.Fprintf(writer, "outcome:    %s\n", mcpCallOutcome(call))
	if call.Reason != "" {
		fmt.Fprintf(writer, "why it ended so: %s\n", call.Reason)
	}
	if call.Why != "" {
		fmt.Fprintf(writer, "why it was asked: %s\n", call.Why)
	}
	if call.Tries > 1 {
		fmt.Fprintf(writer, "tries:      %d\n", call.Tries)
	}
	if call.ListingDigest != "" {
		fmt.Fprintf(writer, "the server's tool listing: %s\n", call.ListingDigest)
	}
	if call.ConfigRevision != "" {
		fmt.Fprintf(writer, "configuration: %s\n", call.ConfigRevision)
	}
	if len(call.Arguments) > 0 {
		fmt.Fprintf(writer, "arguments, secrets removed:\n%s\n", call.Arguments)
	}
	if call.ResultDigest != "" {
		fmt.Fprintf(writer, "result: %d bytes, %s", call.ResultBytes, call.ResultDigest)
		if call.ResultCut {
			fmt.Fprintf(writer, "; the record kept the first %d bytes", len(call.Result))
		}
		fmt.Fprintln(writer, ", secrets removed; it is what the server returned, not checked by anybody:")
		fmt.Fprintln(writer, call.Result)
	}
}

// agentMCPToolNames is every agent's MCP tools by name, for `config show
// --json`, holding only the agents that name some.
func agentMCPToolNames(cfg config.Config) map[string][]string {
	named := map[string][]string{}
	for name := range cfg.Agents {
		for _, tool := range cfg.MCPToolsFor(name) {
			named[name] = append(named[name], tool.Name())
		}
	}
	return named
}
