package checkrun

import (
	"fmt"
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

// askedOutsideTheRunner is every function in the scheduler's package that may
// ask a provider about its login or look a tool up on the search path itself,
// and why. Anything else there that does is a second schedule re-checking a
// hold's cause.
var askedOutsideTheRunner = map[string]string{
	// The dispatch-time check before a run starts, which decides that one start
	// and holds nothing.
	"requireBackendReady": "a run's own start",
	// Developer routing by endpoint, which picks where one run goes.
	"endpointProblem": "a run's own routing",
	// A watch session wired with no check runner at all.
	"loginAskedWithoutARunner": "a session with no check runner",
}

// The scheduler asks no provider about its login and looks no tool up on the
// search path except in the functions named above: a hold whose cause either
// would confirm waits on its follow-up, and the check runner asks.
func TestTheSchedulerReChecksNoHoldsCauseItself(t *testing.T) {
	t.Parallel()
	directory, err := filepath.Abs(filepath.Join("..", "..", "orchestrator"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("reading the scheduler's package: %v", err)
	}
	var found []string
	files := token.NewFileSet()
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			if _, allowed := askedOutsideTheRunner[function.Name.Name]; allowed {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok && (selector.Sel.Name == "CheckAvailability" || selector.Sel.Name == "LookPath") {
					found = append(found, fmt.Sprintf("%s: %s calls %s", files.Position(call.Pos()), function.Name.Name, selector.Sel.Name))
				}
				return true
			})
		}
	}
	if len(found) > 0 {
		t.Fatalf("the scheduler re-checks a hold's cause outside the check runner:\n%s", strings.Join(found, "\n"))
	}
}
