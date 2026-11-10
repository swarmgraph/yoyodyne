package mcpcall

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// mcpWords are the strings only code speaking MCP writes: the methods a client
// sends, the field the opening exchange settles the version in, and the
// headers of the remote transport. Written in lower case; a string literal is
// compared in lower case too.
var mcpWords = []string{"tools/call", "tools/list", "notifications/initialized", "protocolversion", "mcp-session-id", "mcp-protocol-version"}

// mcpModules are the parts of an import path that name an MCP library: the
// official SDK and the other Go clients.
var mcpModules = []string{"modelcontextprotocol", "mark3labs/mcp-go", "metoro-io/mcp-golang"}

// Only this package speaks MCP (docs/designs/mcp-tools-through-the-harness.md,
// "What is enforced in code", and the invariant no-agent-session-holds-an-mcp-
// server). This fails where any Go file outside it, tests included, writes one
// of the protocol's own strings or imports an MCP library, so a second client
// cannot appear anywhere without a reviewer being shown this test failing.
func TestNoOtherPackageSpeaksMCP(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	own, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	files := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); path != root && (strings.HasPrefix(name, ".") || name == "vendor" || path == own) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		parsed, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		for _, spec := range parsed.Imports {
			imported, _ := strconv.Unquote(spec.Path.Value)
			for _, module := range mcpModules {
				if strings.Contains(imported, module) {
					found = append(found, relative+": imports "+imported)
				}
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value := strings.ToLower(literal.Value)
			for _, word := range mcpWords {
				if strings.Contains(value, word) {
					found = append(found, relative+": writes "+strconv.Quote(word))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("only internal/mcpcall speaks MCP, and these do too:\n%s", strings.Join(found, "\n"))
	}
}

// The check above has to see what it is looking for: this package's own
// client writes every one of its words, so a word it lists that the client
// does not use would be a word nothing could ever be caught writing.
func TestTheCheckFindsTheClientsOwnWords(t *testing.T) {
	t.Parallel()
	var source strings.Builder
	for _, name := range []string{"mcpcall.go", "remote.go"} {
		parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				source.WriteString(strings.ToLower(literal.Value) + "\n")
			}
			return true
		})
	}
	for _, word := range mcpWords {
		if !strings.Contains(source.String(), word) {
			t.Errorf("the client never writes %q, so the check could not find another package writing it", word)
		}
	}
}
