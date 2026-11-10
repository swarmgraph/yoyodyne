package cli

import (
	"fmt"
	"io"
	"runtime"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/mcpcall"
	"github.com/mason-bryant/yoyodyne/internal/mcpcredential"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

// conversationMCP is the one MCP call path a conversation asks through
// (docs/designs/mcp-tools-through-the-harness.md, section 2), or nil where
// the project declares no MCP server: then nothing is built, nothing is
// written under the state directory, and every role's instructions are
// exactly what they were. Credentials are read from the stores the
// declaration names, the keychain only on macOS, and the operator's pause is
// the same switch every provider invocation reads.
func conversationMCP(parts components, stderr io.Writer) chat.MCPCalls {
	if !parts.config.MCP.Declared() {
		return nil
	}
	if parts.holds == nil {
		fmt.Fprintln(stderr, "warning: MCP calls are off for this conversation, because the operator's pause cannot be read here")
		return nil
	}
	records, err := mcpcall.NewRecords(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		fmt.Fprintf(stderr, "warning: MCP calls are off for this conversation, because their record could not be opened: %v\n", err)
		return nil
	}
	stores := mcpcredential.Stores{StateRoot: parts.stateRoot, Product: parts.config.Product.ID}
	if runtime.GOOS == "darwin" {
		stores.Keychain = slack.Keychain{Runner: execution.OSProcessRunner{}}
	}
	return &mcpcall.Path{
		Config:        parts.config,
		Stores:        stores,
		Hold:          parts.holds,
		Records:       records,
		RedactValues:  parts.redactValues,
		ClientVersion: buildinfo.Commit(),
	}
}
