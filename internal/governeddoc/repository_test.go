package governeddoc

// What keeps an escalation visible. `go test ./...` discards everything a
// passing package printed, so an escalation written to standard error reaches
// somebody only through the step in the Makefile's `test` target that runs the
// escalating tests again with -v and prints what they wrote. That step names
// those tests, their packages, and the start of an escalated line in three
// Makefile variables, and nothing but this file holds those against the code:
// rename a test, add a gate that escalates, or reword the prefix, and the step
// would go on printing "no defect in a governed document was escalated" over
// defects it can no longer see.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// repositoryRoot is the checkout these tests run in, reached from this package.
const repositoryRoot = "../.."

// An escalation is a line opening with the prefix, with the route on the lines
// under it indented by a tab: that is the shape the Makefile step picks out.
func TestAnEscalationOpensItsLineWithThePrefixAndIndentsItsRoute(t *testing.T) {
	t.Parallel()

	routed := Route(defaults(), Defect{Path: "docs/designs/v1-harness.md", Detail: "it supports nothing"})
	var written bytes.Buffer
	escalateTo(&written, "%s", routed[0])
	lines := strings.Split(strings.TrimSuffix(written.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("escalation = %q, want the defect and its route on two lines", written.String())
	}
	if !strings.HasPrefix(lines[0], EscalationPrefix+" docs/designs/v1-harness.md: ") {
		t.Errorf("first line = %q, want it to open with the prefix and the file", lines[0])
	}
	if !strings.HasPrefix(lines[1], "\t") || !strings.Contains(lines[1], "yoyo agent chat architect") {
		t.Errorf("second line = %q, want the route indented under it", lines[1])
	}
}

func TestTheMakefilePrintsWhatEveryEscalatingGateEscalates(t *testing.T) {
	t.Parallel()

	makefile, err := os.ReadFile(filepath.Join(repositoryRoot, "Makefile"))
	if err != nil {
		t.Fatalf("read the Makefile: %v", err)
	}
	prefix := makeVariable(t, makefile, "GOVERNED_DOCUMENT_PREFIX")
	if prefix == "" || !strings.HasPrefix(EscalationPrefix, prefix) {
		t.Errorf("GOVERNED_DOCUMENT_PREFIX = %q, which does not open EscalationPrefix %q, so the step would print none of them", prefix, EscalationPrefix)
	}
	for _, use := range []string{"-run '$(GOVERNED_DOCUMENT_TESTS)' $(GOVERNED_DOCUMENT_GATES)", "prefix='$(GOVERNED_DOCUMENT_PREFIX)'"} {
		if !bytes.Contains(makefile, []byte(use)) {
			t.Errorf("the Makefile does not run %q, so nothing prints what the gates escalate", use)
		}
	}

	listed := map[string]bool{}
	tests := makeVariable(t, makefile, "GOVERNED_DOCUMENT_TESTS")
	if !strings.HasPrefix(tests, "^(") || !strings.HasSuffix(tests, ")$$") {
		t.Fatalf("GOVERNED_DOCUMENT_TESTS = %q, want ^(Name|Name)$$", tests)
	}
	for _, name := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(tests, "^("), ")$$"), "|") {
		listed[name] = true
	}
	packages := map[string]bool{}
	for _, directory := range strings.Fields(makeVariable(t, makefile, "GOVERNED_DOCUMENT_GATES")) {
		packages[path.Clean(strings.TrimPrefix(directory, "./"))] = true
	}

	escalating := escalatingTests(t)
	if len(escalating) == 0 {
		t.Fatal("no test calls governeddoc.Escalate, so this check would hold nothing")
	}
	for name, directory := range escalating {
		if !listed[name] {
			t.Errorf("%s in %s escalates defects and GOVERNED_DOCUMENT_TESTS does not name it, so what it escalates is never printed", name, directory)
		}
		if !packages[directory] {
			t.Errorf("%s escalates defects and GOVERNED_DOCUMENT_GATES does not name its package %s", name, directory)
		}
	}
	for name := range listed {
		if _, found := escalating[name]; !found {
			t.Errorf("GOVERNED_DOCUMENT_TESTS names %s, which is not a test that escalates anything", name)
		}
	}
}

// escalatingTests is every test function outside this package that refers to
// Escalate, by name, with the package directory it is in. A reference anywhere
// but directly inside a test function fails, because the step runs tests by
// name and could not find the test a helper is called from.
func escalatingTests(t *testing.T) map[string]string {
	t.Helper()
	found := map[string]string{}
	var helpers []string
	err := filepath.WalkDir(filepath.Join(repositoryRoot, "internal"), func(file string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(file, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(repositoryRoot, file)
		if err != nil {
			return err
		}
		directory := filepath.ToSlash(filepath.Dir(relative))
		if directory == "internal/governeddoc" {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil || !refersToEscalate(function.Body) {
				continue
			}
			name := function.Name.Name
			if function.Recv == nil && strings.HasPrefix(name, "Test") {
				found[name] = directory
				continue
			}
			helpers = append(helpers, directory+": "+name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the tests: %v", err)
	}
	sort.Strings(helpers)
	for _, helper := range helpers {
		t.Errorf("%s refers to governeddoc.Escalate outside a test function; call it from the test, which the Makefile step can name", helper)
	}
	return found
}

func refersToEscalate(body *ast.BlockStmt) bool {
	refers := false
	ast.Inspect(body, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "Escalate" {
			if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "governeddoc" {
				refers = true
			}
		}
		return !refers
	})
	return refers
}

// makeVariable is the value a simple `NAME = value` line in the Makefile gives.
func makeVariable(t *testing.T, makefile []byte, name string) string {
	t.Helper()
	match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` = (.*)$`).FindSubmatch(makefile)
	if match == nil {
		t.Fatalf("the Makefile sets no %s", name)
	}
	return strings.TrimSpace(string(match[1]))
}
