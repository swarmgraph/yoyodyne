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

const configImportPath = "github.com/mason-bryant/yoyodyne/internal/config"

// unactivatedLoads are the configuration package's loaders that cannot read the
// activation record, so they refuse any agent whose role names a role
// definition rather than binding it. A reader of agents calling one of them
// works for a project on shipped roles and refuses one that uses definitions.
var unactivatedLoads = map[string]bool{"Load": true, "LoadResolved": true}

// allowedUnactivatedLoads are the one call outside the configuration package
// that may use those loaders, with the reason: `yoyo init` loads the file it has
// just generated from the template, which names no role definition.
var allowedUnactivatedLoads = map[string]string{
	"internal/cli/init.go": "init proves the configuration it just generated loads; a generated file names only shipped roles",
}

// Every reader of the project's agents outside the configuration package loads
// through the one loader that reads the activation record —
// config.LoadResolvedActivated, which the CLI's loadConfiguration and
// loadActivatedConfiguration call, and which doctor, the Slack process, and
// the watch session's per-pull read are all handed. A new reader that called
// config.Load or config.LoadResolved directly would refuse every project whose
// agents fill a role definition, so this test fails on one.
func TestEveryReaderOfAgentsLoadsThroughTheActivationRecord(t *testing.T) {
	t.Parallel()

	root := filepath.Join(repositoryRootForTest(t), "internal")
	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if filepath.Base(path) == "config" && filepath.Dir(path) == root {
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
			if value, _ := strconv.Unquote(imported.Path.Value); value == configImportPath {
				local = "config"
				if imported.Name != nil {
					local = imported.Name.Name
				}
			}
		}
		if local == "" {
			return nil
		}
		relative, err := filepath.Rel(repositoryRootForTest(t), path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !unactivatedLoads[selector.Sel.Name] {
				return true
			}
			if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == local {
				if _, allowed := allowedUnactivatedLoads[relative]; !allowed {
					found = append(found, relative+": config."+selector.Sel.Name)
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
		t.Fatalf("configuration loaded without the activation record at %s; load through loadActivatedConfiguration (config.LoadResolvedActivated) so an agent on an activated role definition is accepted", strings.Join(found, ", "))
	}
}
