package epicprogress

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// writers is every way the three files are written: the functions of the package
// that owns each, and every file of another package that opens it for writing.
// Only a command the operator runs may be added to the operator's record.
// UnderLock is listed for the lock it takes: it writes nothing, as
// TestAJoinTakesTheOperatorsLockAndThenItsOwn shows.
var writers = []struct {
	files     []string // every name starts with the first, without its ending
	pkg       string
	opened    []string // the names another package opens it for writing through
	functions []string // what writes, or calls what writes
	outside   []string
}{
	{[]string{"epic-scope.json"}, "internal/epicscope", []string{"ForOperator", "Operator"},
		[]string{"AddCopying", "End", "Remove", "SetBudget", "Start", "TurnOff", "UnderLock", "change", "write"}, []string{"internal/cli/epic.go"}},
	{[]string{progressName, begunName}, "internal/epicprogress", []string{"Open", "Store"},
		[]string{"Ask", "AskEvidence", "Begin", "Call", "CallWhileOpen", "Checked", "CommentRead", "CopyLate", "Correcting", "EvidenceAnswered", "Join", "Judged", "Noted", "Point", "ReserveRun", "call", "change", "changeComment", "changePiece", "reserve", "settle", "write"}, []string{"internal/cli/run.go"}},
}

// TestEveryWriterOfTheThreeFilesIsListed reads every Go file of the module, tests
// aside. No file that writes is in a package that reads a role's block, and no
// such package can call the operator's commands, because only the program
// imports the package they are in and only its dispatch calls them. A file name
// put together from pieces at run time is what it does not see.
func TestEveryWriterOfTheThreeFilesIsListed(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	parsed, blocks := map[string]*ast.File{}, map[string]bool{}
	must(t, filepath.WalkDir(root, func(path string, found fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name := found.Name(); found.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		parsed[relative] = file
		if err == nil && holds(file, "```yoyodyne-") {
			blocks[filepath.ToSlash(filepath.Dir(relative))] = true
		}
		return err
	}))
	if len(blocks) < 10 {
		t.Fatalf("only %d packages that read a block were found, so this is looking in the wrong place", len(blocks))
	}
	for _, writer := range writers {
		var outside, functions []string
		for name, file := range parsed {
			pkg := filepath.ToSlash(filepath.Dir(name))
			if pkg == writer.pkg {
				functions = append(functions, writing(file)...)
				continue
			}
			if reaches(file, writer.pkg, writer.opened...) {
				outside = append(outside, name)
				if blocks[pkg] {
					t.Errorf("%s writes %v and its package reads a role's block", name, writer.files)
				}
			}
			if holds(file, strings.TrimSuffix(writer.files[0], ".json")) {
				t.Errorf("%s names %v", name, writer.files)
			}
		}
		slices.Sort(outside)
		slices.Sort(functions)
		if !reflect.DeepEqual(outside, writer.outside) || !reflect.DeepEqual(functions, writer.functions) || blocks[writer.pkg] {
			t.Errorf("%v are written by %v and opened for writing in %v (the package reads a block: %t), want %v and %v",
				writer.files, functions, outside, blocks[writer.pkg], writer.functions, writer.outside)
		}
	}
	for name, file := range parsed {
		if reaches(file, "internal/cli") && name != "cmd/yoyo/main.go" {
			t.Errorf("%s imports the package the operator's commands are in", name)
		}
		if strings.HasPrefix(name, "internal/cli/") && name != "internal/cli/epic.go" && name != "internal/cli/cli.go" && uses(file, "runEpic", "epicCommand") {
			t.Errorf("%s calls the operator's commands", name)
		}
	}
}

// holds reports whether a string in the file has the text in it.
func holds(file *ast.File, text string) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		found = found || ok && literal.Kind == token.STRING && strings.Contains(literal.Value, text)
		return !found
	})
	return found
}

// reaches reports whether a file imports a package and uses any of the names, or
// imports it at all where no name is given or it has no name of its own there.
func reaches(file *ast.File, pkg string, names ...string) bool {
	for _, spec := range file.Imports {
		if spec.Path.Value == `"github.com/mason-bryant/yoyodyne/`+pkg+`"` {
			imported := path.Base(pkg)
			if spec.Name != nil {
				imported = spec.Name.Name
			}
			found := imported == "." || len(names) == 0
			ast.Inspect(file, func(node ast.Node) bool {
				used, ok := node.(*ast.SelectorExpr)
				if ok && slices.Contains(names, used.Sel.Name) {
					from, plain := used.X.(*ast.Ident)
					found = found || plain && from.Name == imported
				}
				return !found
			})
			return found
		}
	}
	return false
}

func uses(node ast.Node, names ...string) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		name, ok := node.(*ast.Ident)
		found = found || ok && slices.Contains(names, name.Name)
		return !found
	})
	return found
}

// writing is every function of a file that writes a file or calls what does.
func writing(file *ast.File) []string {
	var found []string
	for _, declared := range file.Decls {
		if function, ok := declared.(*ast.FuncDecl); ok && uses(function.Body, "WriteFile", "FileWriter", "write", "change", "reserve", "settle") {
			found = append(found, function.Name.Name)
		}
	}
	return found
}
