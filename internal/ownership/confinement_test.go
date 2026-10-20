package ownership

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The operator's mover value is named only here. Any other package may ask an
// answer whether it names him (Mover.IsOperator), but naming him — the
// constant, or the mover vocabulary converted from the word "operator" — is
// the registry's alone, so no surface or derivation can decide on its own that
// something is his. The scan reads every non-test Go file in the module.
func TestTheOperatorsMoverIsNamedOnlyInTheOwnershipPackage(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	own := filepath.Join(root, "internal", "ownership")
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Dir(path) == own {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.Ident:
				if n.Name == "MoverOperator" {
					violations = append(violations, fset.Position(n.Pos()).String()+": names MoverOperator")
				}
			case *ast.CallExpr:
				if namesMoverType(n.Fun) && len(n.Args) == 1 {
					if literal, ok := n.Args[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
						if word, err := strconv.Unquote(literal.Value); err == nil && word == string(MoverOperator) {
							violations = append(violations, fset.Position(n.Pos()).String()+": converts \"operator\" to a mover")
						}
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scanning the module: %v", err)
	}
	for _, violation := range violations {
		t.Errorf("%s; only internal/ownership names the operator as a mover, and anything else asks Mover.IsOperator of the registry's answer", violation)
	}
}

// namesMoverType reports a call whose function is the mover type: Mover(...)
// or <package>.Mover(...).
func namesMoverType(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name == "Mover"
	case *ast.SelectorExpr:
		return f.Sel.Name == "Mover"
	}
	return false
}

// moduleRoot is the directory holding go.mod, above this package.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the ownership package")
		}
		dir = parent
	}
}
