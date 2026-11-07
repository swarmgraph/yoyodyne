package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/mcpcredential"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// runMCP is `yoyo mcp`: for now, logging a declared MCP server in on this
// machine (docs/designs/mcp-tools-through-the-harness.md, section 1). Making a
// call is the harness's alone and has no command here.
func runMCP(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printMCPUsage(stdout)
		return 0
	}
	switch args[0] {
	case "login":
		return loginMCPServer(ctx, args[1:], stdin, stdout, stderr)
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
	fmt.Fprintln(writer, `Usage: yoyo mcp login <server> [options]

Logs a declared MCP server in on this machine. It runs the login command the
server's entry in the configuration declares, at your terminal, with the
server's own folder under the state directory as its working directory and its
home, so the files it writes stay where the server finds them and no agent can
read them. It is refused to a process the harness started for a role. A server
whose credential is stored by hand declares no login, and this prints the
command that stores it instead.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)`)
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
