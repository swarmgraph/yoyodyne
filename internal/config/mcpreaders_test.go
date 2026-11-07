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
// `yoyo config show`, `yoyo mcp login`, and `yoyo doctor`. None of them builds a
// role's launch or its instructions, so a project that declares nothing —
// whose revision is unchanged too (TestAProjectWithNoMCPSectionIsUnchanged) —
// launches and instructs every role exactly as it did before the keys existed.
//
// A later piece that hands any of this to a launch or a prompt adds its file
// here, which is where a reviewer sees that it does.
var mcpReaders = map[string]bool{
	"internal/config/document.go": true,
	"internal/config/mcp.go":      true,
	"internal/config/resolve.go":  true,
	"internal/cli/cli.go":         true,
	"internal/cli/mcp.go":         true,
	"internal/doctor/mcp.go":      true,
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
