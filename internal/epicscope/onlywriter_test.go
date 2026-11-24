package epicscope

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

const packagePath = "github.com/mason-bryant/yoyodyne/internal/epicscope"

// operatorCommands is every file outside this package that may open the record
// for writing. Each is a command the operator runs; a role's block, a
// conversation and the scheduler are never among them.
var operatorCommands = map[string]bool{"internal/cli/epic.go": true}

// TestOnlyAnOperatorsCommandWritesTheRecord reads every Go file of the module
// outside this package, tests aside, and fails on one that names the file, or
// that opens the record for writing and is not an operator's command. It fails
// on a listed command that no longer does, too. A name put together from pieces
// at run time is what it does not see.
func TestOnlyAnOperatorsCommandWritesTheRecord(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	own, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	read := 0
	opened := map[string]bool{}
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
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		read++
		names, opens, err := writesRecord(relative, source)
		if err != nil {
			return err
		}
		if names {
			t.Errorf("%s names the record's file; read it through epicscope.Read and write it through the operator's acts", relative)
		}
		if opens {
			opened[relative] = true
			if !operatorCommands[relative] {
				t.Errorf("%s opens the record of named epics for writing; only a command the operator runs may", relative)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if read < 100 {
		t.Fatalf("only %d files were read, so this is looking in the wrong place", read)
	}
	for command := range operatorCommands {
		if !opened[command] {
			t.Errorf("%s no longer opens the record for writing; take it off operatorCommands", command)
		}
	}
}

func TestTheWriterSweepSeesEachWayIn(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		source       string
		names, opens bool
	}{
		"reads only":        {`import "` + packagePath + `"; var _, _ = epicscope.Read("", "")`, false, false},
		"opens it":          {`import "` + packagePath + `"; var _, _ = epicscope.ForOperator("", "")`, false, true},
		"under an alias":    {`import e "` + packagePath + `"; var open = e.ForOperator`, false, true},
		"into its own file": {`import . "` + packagePath + `"; var _, _ = Read("", "")`, false, true},
		"takes one":         {`import "` + packagePath + `"; func f(o *epicscope.Operator) {}`, false, true},
		"names the file":    {`var path = "state/epic-scope.json"`, true, false},
		"another's name":    {`import "example.com/epicscope"; var _ = epicscope.ForOperator`, false, false},
	} {
		names, opens, err := writesRecord("planted.go", []byte("package planted\n"+test.source+"\n"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if names != test.names || opens != test.opens {
			t.Errorf("%s: names the file %t and opens it %t, want %t and %t", name, names, opens, test.names, test.opens)
		}
	}
}

// writesRecord reports whether a source file names the record's file and
// whether it reaches what writes the record. Importing this package without a
// name of its own counts, because its functions are then called with none.
func writesRecord(name string, source []byte) (names, opens bool, err error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), name, source, parser.SkipObjectResolution)
	if err != nil {
		return false, false, err
	}
	imported := ""
	for _, spec := range parsed.Imports {
		if path, _ := strconv.Unquote(spec.Path.Value); path == packagePath {
			imported = "epicscope"
			if spec.Name != nil {
				imported = spec.Name.Name
			}
		}
	}
	opens = imported == "."
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.BasicLit:
			if node.Kind == token.STRING && strings.Contains(node.Value, strings.TrimSuffix(fileName, ".json")) {
				names = true
			}
		case *ast.SelectorExpr:
			from, ok := node.X.(*ast.Ident)
			if ok && imported != "" && from.Name == imported && (node.Sel.Name == "ForOperator" || node.Sel.Name == "Operator") {
				opens = true
			}
		}
		return true
	})
	return names, opens, nil
}
