package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// mcpReaders is every file outside tests that reads the mcp section, an agent's
// MCP tools, or where a credential is kept: the configuration that loads them,
// `yoyo config show`, `yoyo mcp login`, `yoyo doctor`, and the client in
// internal/mcpcall that starts a declared server in the harness's own process
// and reads its credential from the stores, with the one call path beside it
// that resolves each caller's list and scrubs results of every server's
// credential. None of them builds a
// role's launch, so a project that declares nothing — whose revision is
// unchanged too (TestAProjectWithNoMCPSectionIsUnchanged) — launches every
// role exactly as it did before the keys existed.
//
// Three files hand it to a conversation's instructions and its replies:
// internal/cli/chatmcp.go builds the call path only where the project
// declares a server, internal/mcpcall/describe.go describes the tools on an
// agent's own list, and internal/chat/mcp.go puts that description in the
// instructions of an agent that holds a tool and carries out its block. An
// agent with an empty list is told nothing, so its instructions are unchanged
// (TestAConversationWithNoMCPToolsIsToldNothing in internal/chat).
//
// A later piece that hands any of this to a launch or a prompt adds its file
// here, which is where a reviewer sees that it does.
var mcpReaders = map[string]bool{
	"internal/config/document.go":  true,
	"internal/config/mcp.go":       true,
	"internal/config/resolve.go":   true,
	"internal/cli/cli.go":          true,
	"internal/cli/mcp.go":          true,
	"internal/doctor/mcp.go":       true,
	"internal/cli/chatmcp.go":      true,
	"internal/chat/mcp.go":         true,
	"internal/mcpcall/describe.go": true,
	"internal/mcpcall/command.go":  true,
	"internal/mcpcall/mcpcall.go":  true,
	"internal/mcpcall/path.go":     true,
	"internal/mcpcall/remote.go":   true,
}

func TestNothingOnTheLaunchOrInstructionPathReadsTheMCPConfiguration(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(root, "internal", "mcpcredential")
	var found []string
	files := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || path == credentials) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		if mcpReaders[relative] {
			return nil
		}
		parsed, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, spec := range parsed.Imports {
			if imported, _ := strconv.Unquote(spec.Path.Value); imported == "github.com/mason-bryant/yoyodyne/internal/mcpcredential" {
				found = append(found, relative+": imports internal/mcpcredential")
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok {
				switch selector.Sel.Name {
				case "MCP", "MCPTools", "MCPToolsFor":
					found = append(found, relative+": reads "+selector.Sel.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	if len(found) > 0 {
		t.Fatalf("code outside the files that load and report the MCP configuration reads it:\n%s", strings.Join(found, "\n"))
	}
}
