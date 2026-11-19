package orchestrator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestReadmeNamesOnlyFilesThatExist keeps the code map from going stale. The
// README does not list every file in the package, because a hand-kept list
// made every pair of changes that each added a file conflict there; a file
// describes itself in its own leading comment instead. What the README does
// name must still exist, so a rename or deletion fails here until the README
// follows it.
func TestReadmeNamesOnlyFilesThatExist(t *testing.T) {
	text, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	byBase := map[string]bool{}
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			byBase[d.Name()] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	named := regexp.MustCompile("`([A-Za-z0-9_./-]+\\.(?:go|md))`")
	for _, m := range named.FindAllStringSubmatch(string(text), -1) {
		name := m[1]
		if strings.Contains(name, "/") {
			if _, err := os.Stat(filepath.Join(root, name)); err != nil {
				t.Errorf("README.md names %s, which does not exist; update the code map", name)
			}
			continue
		}
		if !byBase[name] {
			t.Errorf("README.md names %s, which is in no package under internal; update the code map", name)
		}
	}
}

// declaredNames is every name declared anywhere under internal/: functions,
// methods, types, constants, variables, struct fields and interface methods.
func declaredNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl:
				names[n.Name.Name] = true
			case *ast.TypeSpec:
				names[n.Name.Name] = true
			case *ast.ValueSpec:
				for _, id := range n.Names {
					names[id.Name] = true
				}
			case *ast.Field:
				for _, id := range n.Names {
					names[id.Name] = true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// notSymbols are backticked words the README uses that are not Go
// declarations: shell commands, configuration keys and outside tools.
var notSymbols = map[string]bool{
	"bd": true, "rg": true, "go": true, "ls": true, "run": true, "yoyo": true,
}

var goIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// TestReadmeNamesOnlySymbolsThatExist holds the symbols the README names to
// the code: each backticked Go identifier, or the last part of a dotted
// selector such as `Pipeline.Run`, must be declared somewhere under internal/.
// A rename or deletion fails here until the README follows it. A backticked
// phrase that is not one identifier or selector (a command, a path, a key with
// an underscore) is not a symbol and is not read; a package name is accepted.
func TestReadmeNamesOnlySymbolsThatExist(t *testing.T) {
	text, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	declared := declaredNames(t, root)
	packages := map[string]bool{}
	_ = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			packages[d.Name()] = true
		}
		return nil
	})
	span := regexp.MustCompile("`([^`\n]+)`")
	seen := map[string]bool{}
	for _, m := range span.FindAllStringSubmatch(string(text), -1) {
		word := m[1]
		if strings.HasSuffix(word, ".go") || strings.HasSuffix(word, ".md") {
			continue
		}
		if strings.Contains(word, "_") {
			continue // a configuration key or environment variable
		}
		parts := strings.Split(word, ".")
		ok := true
		for _, p := range parts {
			if !goIdentifier.MatchString(p) {
				ok = false
			}
		}
		if !ok {
			continue
		}
		last := parts[len(parts)-1]
		if seen[last] || notSymbols[last] || notSymbols[word] {
			continue
		}
		seen[last] = true
		if !declared[last] && !packages[last] {
			t.Errorf("README.md names %s, which is declared nowhere under internal; update the code map", word)
		}
	}
}

// TestTwoChangesThatEachAddAFileMergeWithoutTouchingTheReadme is the scripted
// pair the README's rule promises: two branches that each add a different file
// beside the README merge with no conflict, and the README the merge leaves
// still passes the file check.
func TestTwoChangesThatEachAddAFileMergeWithoutTouchingTheReadme(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	pkg := filepath.Join(dir, "internal", "orchestrator")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "README.md"), readme, 0o644); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	for _, name := range []string{"first.go", "second.go"} {
		git("checkout", "-q", "-b", name, "main")
		if err := os.WriteFile(filepath.Join(pkg, name), []byte("package orchestrator\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", ".")
		git("commit", "-q", "-m", "add "+name)
	}
	git("checkout", "-q", "main")
	git("merge", "-q", "--no-edit", "first.go")
	git("merge", "-q", "--no-edit", "second.go")
	for _, name := range []string{"first.go", "second.go"} {
		if _, err := os.Stat(filepath.Join(pkg, name)); err != nil {
			t.Errorf("%s is missing after the merge: %v", name, err)
		}
	}
	merged, err := os.ReadFile(filepath.Join(pkg, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(merged) != string(readme) {
		t.Error("the merge changed the README")
	}
}
