package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// No MCP server reaches a developer.
//
// A developer's turn reads the account's config.toml, unlike a read-only
// role's, and a trusted worktree's own .codex/config.toml too. Both can list
// servers under [mcp_servers], and Codex starts every one of them when the
// session opens, before the model says anything: asked without a provider call,
// `codex debug prompt-input` ran an account's listed server under the
// developer's own arguments. Plugins, apps, and the servers a skill would
// install are kept out separately, by contextArgs.
//
// Nothing turns them all off at once. An empty table given as configuration is
// merged with the files rather than replacing what they list, and
// `orchestrator.mcp.enabled=false` does not stop them. What does is each server
// set `enabled=false` by name — and a name that no layer defines is a server
// with no transport, which the CLI refuses the whole configuration for. So the
// names are the CLI's own: before every developer turn, fresh and resumed, the
// adapter asks `codex mcp list --json`, under the same provider home, directory,
// and settings the turn is made with, which servers it resolves from every file
// it reads, and turns each one off in a single inline table. A dotted path does
// not work for this: the CLI splits `--config` keys on every dot and keeps
// quotes, so a server with a dot in its name could not be reached by one.
//
// A listing that fails or cannot be read refuses the turn rather than starting
// it with servers nobody looked for. The listing is configuration only; asked
// with a server that records being started, it started none.
//
// Established against codex-cli 0.166.0-alpha.5.1 without a provider call;
// TestTheInstalledCLIStartsNoMCPServerForADeveloper asks the installed CLI the
// same thing on every run where Codex is installed.

// mcpListTimeout bounds the listing. It reads configuration and nothing else,
// and answers in well under a second.
const mcpListTimeout = 30 * time.Second

// mcpListArgs is the listing asked with the configuration and feature settings
// of the invocation it is made for, so it resolves the files that invocation
// would. Everything else on the invocation's command line is left out: the
// listing takes no prompt, no model, and no session.
func mcpListArgs(invocation []string) []string {
	args := []string{"mcp", "list", "--json"}
	for index := 0; index < len(invocation); index++ {
		switch invocation[index] {
		case "resume":
			return args
		case "--config", "--enable", "--disable":
			if index+1 < len(invocation) {
				args = append(args, invocation[index], invocation[index+1])
				index++
			}
		}
	}
	return args
}

// readMCPServerNames is the server names a `codex mcp list --json` listing
// names, each once and in order.
func readMCPServerNames(listing string) ([]string, error) {
	var servers []struct {
		Name *string `json:"name"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(listing)), &servers); err != nil {
		return nil, fmt.Errorf("read the MCP servers Codex listed: %w", err)
	}
	var names []string
	for _, server := range servers {
		if server.Name == nil {
			return nil, errors.New("read the MCP servers Codex listed: a server has no name")
		}
		if !slices.Contains(names, *server.Name) {
			names = append(names, *server.Name)
		}
	}
	return names, nil
}

// mcpServersOffArgs turns off every named server, as one inline table merged
// over whatever the configuration files list. No names is no setting at all:
// an override naming no server changes nothing, and leaving it off keeps the
// command line of a turn with nothing to turn off what it was.
func mcpServersOffArgs(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	entries := make([]string, 0, len(names))
	for _, name := range names {
		entries = append(entries, tomlString(name)+"={enabled=false}")
	}
	return []string{"--config", "mcp_servers={" + strings.Join(entries, ",") + "}"}
}

// withMCPServersOff is the invocation's arguments with every server the CLI
// lists for it turned off, placed on `exec` ahead of `--cd` and of `resume`,
// where the rest of its configuration is.
func (b Backend) withMCPServersOff(ctx context.Context, args []string, directory string, environment []string) ([]string, error) {
	var stdout strings.Builder
	result, err := b.Runner.Run(ctx, execution.Command{
		Name:    b.binary(),
		Args:    mcpListArgs(args),
		Dir:     directory,
		Env:     environment,
		Timeout: mcpListTimeout,
	}, func(output execution.Output) {
		if output.Stream == execution.StreamStdout {
			stdout.WriteString(output.Text)
			stdout.WriteString("\n")
		}
	})
	if err != nil {
		return nil, fmt.Errorf("ask Codex which MCP servers a developer would start: %w", err)
	}
	if result.Status != execution.ProcessSucceeded {
		// The CLI's complaint is about configuration that can hold a server's
		// credentials, so it is redacted by shape before it is recorded.
		detail := strings.TrimSpace(execution.NewRedactor().WithSecretShapes().Redact(result.Stderr))
		if detail == "" {
			detail = fmt.Sprintf("exit status %d", result.ExitCode)
		}
		return nil, fmt.Errorf("ask Codex which MCP servers a developer would start: %s", detail)
	}
	names, err := readMCPServerNames(stdout.String())
	if err != nil {
		return nil, err
	}
	off := mcpServersOffArgs(names)
	if len(off) == 0 {
		return args, nil
	}
	at := slices.Index(args, "--cd")
	if at < 0 {
		return nil, errors.New("place the Codex MCP settings: the invocation names no directory")
	}
	placed := slices.Clone(args[:at])
	placed = append(placed, off...)
	return append(placed, args[at:]...), nil
}
