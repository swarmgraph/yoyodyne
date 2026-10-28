package checkrun

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

// The check runner is the only schedule that re-checks a hold's cause
// (docs/designs/follow-ups.md, "What ends a follow-up"). This fails where any
// code outside this package runs a check itself: calls an evaluator's Evaluate,
// or starts xcodebuild, whose license check is the cause the October 24 stop
// waited on. A watch session's holds wait on their follow-ups, which the
// session's own tests hold to: the license hold starts no run to find out, and
// the lapsed login asks the provider nothing itself
// (internal/orchestrator/followups_test.go).
func TestNothingOutsideTheCheckRunnerRunsAHoldsCheck(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
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
			if name := entry.Name(); path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || path == own) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		importsRunner := ""
		for _, spec := range parsed.Imports {
			if imported, _ := strconv.Unquote(spec.Path.Value); imported == "github.com/mason-bryant/yoyodyne/internal/followup/checkrun" {
				importsRunner = "checkrun"
				if spec.Name != nil {
					importsRunner = spec.Name.Name
				}
			}
		}
		relative, _ := filepath.Rel(root, path)
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.BasicLit:
				if node.Kind == token.STRING && strings.Contains(node.Value, "xcodebuild") && !strings.Contains(node.Value, " ") {
					found = append(found, relative+": starts xcodebuild")
				}
			case *ast.CallExpr:
				selector, ok := node.Fun.(*ast.SelectorExpr)
				if ok && importsRunner != "" && selector.Sel.Name == "Evaluate" {
					found = append(found, relative+": runs a check's Evaluate itself")
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
		t.Fatalf("checks are run outside the check runner, which is the one schedule that re-checks a hold's cause:\n%s", strings.Join(found, "\n"))
	}
}
