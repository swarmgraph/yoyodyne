package cli

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

const chatImportPath = "github.com/mason-bryant/yoyodyne/internal/chat"

// Every conversation the harness opens — `yoyo chat`, `yoyo agent chat`, a
// recurring pass and a program manager instance's pass, the development
// manager's escalation, the Slack conversation, a correction turn — reaches
// chat.Open through one call, in (preparedChat).open, and that call hands the
// session the configured agent's role-definition tool set. A second call
// anywhere would be a conversation that falls back to the shipped role's whole
// row for an agent whose definition narrowed it, so this test fails on one.
func TestEveryConversationOpensThroughTheCallThatCarriesTheDefinitionsToolSet(t *testing.T) {
	t.Parallel()

	type site struct {
		file     string
		function string
		call     *ast.CallExpr
	}
	var sites []site
	root := filepath.Join(repositoryRootForTest(t), "internal")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if filepath.Base(path) == "chat" && filepath.Dir(path) == root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		local := ""
		for _, imported := range file.Imports {
			if value, _ := strconv.Unquote(imported.Path.Value); value == chatImportPath {
				local = "chat"
				if imported.Name != nil {
					local = imported.Name.Name
				}
			}
		}
		if local == "" {
			return nil
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "Open" {
					return true
				}
				if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == local {
					sites = append(sites, site{file: path, function: functionName(function), call: call})
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 {
		var where []string
		for _, found := range sites {
			where = append(where, found.file+" "+found.function)
		}
		t.Fatalf("chat.Open is called from %d place(s) outside the chat package (%s); every conversation must open through (preparedChat).open, which carries the agent's role-definition tool set", len(sites), strings.Join(where, ", "))
	}
	only := sites[0]
	if filepath.Base(only.file) != "chat.go" || only.function != "(preparedChat).open" {
		t.Fatalf("chat.Open is called from %s %s, want (preparedChat).open in internal/cli/chat.go", only.file, only.function)
	}
	if len(only.call.Args) != 1 {
		t.Fatalf("chat.Open takes %d argument(s) here, want the one options literal", len(only.call.Args))
	}
	options, ok := only.call.Args[0].(*ast.CompositeLit)
	if !ok {
		t.Fatal("chat.Open is not handed an options literal, so what it carries cannot be checked here")
	}
	for _, element := range options.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := field.Key.(*ast.Ident); !ok || key.Name != "Capabilities" {
			continue
		}
		call, ok := field.Value.(*ast.CallExpr)
		if !ok {
			break
		}
		if name, ok := call.Fun.(*ast.Ident); !ok || name.Name != "definitionCapabilities" || len(call.Args) != 1 {
			break
		}
		if argument, ok := call.Args[0].(*ast.SelectorExpr); ok && argument.Sel.Name == "agent" {
			return
		}
		break
	}
	t.Fatal("the options (preparedChat).open hands chat.Open do not set Capabilities: definitionCapabilities(p.agent), so an agent filling a role definition would be given its shipped role's whole row")
}

func functionName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return function.Name.Name
	}
	receiver := function.Recv.List[0].Type
	if star, ok := receiver.(*ast.StarExpr); ok {
		receiver = star.X
	}
	if ident, ok := receiver.(*ast.Ident); ok {
		return "(" + ident.Name + ")." + function.Name.Name
	}
	return function.Name.Name
}

func repositoryRootForTest(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
