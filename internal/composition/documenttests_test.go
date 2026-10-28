package composition

// What a change made only of documents is checked by. scripts/check-scope.sh
// lets `make test` run, for a change touching Markdown outside every Go
// package, the tests the Makefile's DOCUMENT_TESTS names rather than the whole
// module, so that list has to hold every test that reads this checkout. The
// tests below find those tests in the source and hold the list against them.
//
// A test missing from it is still found before anything is promoted, because
// the merge candidate is checked with the whole module; what this saves is the
// repair round that would cost.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Every test DOCUMENT_TESTS names is one its package has, as for doccheck.
func TestEveryTestTheDocumentTestsNameExists(t *testing.T) {
	t.Parallel()

	entries := documentTests(t)
	if len(entries) == 0 {
		t.Fatal("the Makefile's DOCUMENT_TESTS names nothing")
	}
	for pkg, names := range entries {
		if names == nil {
			if _, err := os.Stat(filepath.Join(repositoryRoot, filepath.FromSlash(pkg))); err != nil {
				t.Errorf("DOCUMENT_TESTS runs the whole of %s, which is not a directory here: %v", pkg, err)
			}
			continue
		}
		declared := testFunctions(t, pkg)
		for name := range names {
			if !declared[name] {
				t.Errorf("DOCUMENT_TESTS runs %s in %s, which has no such test", name, pkg)
			}
		}
	}
}

// Every test that reaches this checkout from its package is among the document
// tests: its whole package is listed, or its name is. Reaching the checkout is
// read from the source -- a string climbing out of the package to the top of
// the checkout, or filepath.Join climbing there one ".." at a time -- directly
// in the test, or through a constant, variable, or helper function of the same
// package that does. That is wider than reading a document, since some of these
// tests read Go source or configuration instead, and a wider list costs a
// document change seconds where a narrower one costs a repair round.
func TestEveryTestThatReadsThisCheckoutIsAmongTheDocumentTests(t *testing.T) {
	t.Parallel()

	entries := documentTests(t)
	readers := checkoutReaders(t)
	if len(readers) == 0 {
		t.Fatal("no test reaches this checkout, so this check would hold nothing")
	}
	var missing []string
	for pkg, tests := range readers {
		named, listed := entries[pkg]
		if listed && named == nil {
			continue
		}
		for _, test := range tests {
			if !named[test] {
				missing = append(missing, pkg+" "+test)
			}
		}
	}
	sort.Strings(missing)
	for _, test := range missing {
		t.Errorf("%s reads this checkout and DOCUMENT_TESTS in the Makefile does not run it, "+
			"so a change to a document it reads would not run it before review; "+
			"add its name to its package's entry, or the package as ./dir=.", test)
	}
}

// documentTests is DOCUMENT_TESTS as the Makefile runs it: each package with
// the names of the tests it runs there, joined across the package's entries,
// or nil where an entry runs the whole package.
func documentTests(t *testing.T) map[string]map[string]bool {
	t.Helper()

	makefile, err := os.ReadFile(filepath.Join(repositoryRoot, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]map[string]bool{}
	whole := map[string]bool{}
	for _, word := range makeList(t, string(makefile), "DOCUMENT_TESTS") {
		pkg, pattern, ok := strings.Cut(word, "=")
		if !ok {
			t.Fatalf("DOCUMENT_TESTS entry %q is not package=pattern", word)
		}
		pattern = strings.ReplaceAll(strings.Trim(pattern, "'"), "$$", "$")
		if pattern == "." {
			whole[pkg] = true
			continue
		}
		if entries[pkg] == nil {
			entries[pkg] = map[string]bool{}
		}
		for name := range namesIn(t, pkg, pattern) {
			entries[pkg][name] = true
		}
	}
	for pkg := range whole {
		entries[pkg] = nil
	}
	return entries
}

// makeList is the words a `NAME = ...` variable in the Makefile holds, its
// continuation lines joined and any $(OTHER) it names expanded the same way.
func makeList(t *testing.T, makefile, name string) []string {
	t.Helper()

	start := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` = `).FindStringIndex(makefile)
	if start == nil {
		t.Fatalf("the Makefile sets no %s", name)
	}
	var value strings.Builder
	for _, line := range strings.Split(makefile[start[1]:], "\n") {
		continued := strings.HasSuffix(line, "\\")
		value.WriteString(strings.TrimSuffix(line, "\\"))
		value.WriteString(" ")
		if !continued {
			break
		}
	}
	var words []string
	for _, word := range strings.Fields(value.String()) {
		if inner, ok := strings.CutPrefix(word, "$("); ok && strings.HasSuffix(inner, ")") {
			words = append(words, makeList(t, makefile, strings.TrimSuffix(inner, ")"))...)
			continue
		}
		words = append(words, word)
	}
	return words
}

// namesIn is the test names an anchored ^(Name|Name)$ pattern runs.
func namesIn(t *testing.T, pkg, pattern string) map[string]bool {
	t.Helper()

	inner, ok := strings.CutPrefix(pattern, "^(")
	if inner, ok = strings.CutSuffix(inner, ")$"); !ok {
		inner, ok = strings.CutPrefix(pattern, "^")
		if inner, ok = strings.CutSuffix(inner, "$"); !ok || strings.ContainsAny(inner, "(|)") {
			t.Fatalf("DOCUMENT_TESTS runs %q in %s, which is not an anchored list of test names", pattern, pkg)
		}
	}
	names := map[string]bool{}
	for _, name := range strings.Split(inner, "|") {
		names[name] = true
	}
	return names
}

// checkoutReaders is, for every package directory holding tests, the tests in
// it that reach this checkout, keyed as DOCUMENT_TESTS names packages.
func checkoutReaders(t *testing.T) map[string][]string {
	t.Helper()

	files := map[string][]*ast.File{}
	err := filepath.WalkDir(repositoryRoot, func(file string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); file != repositoryRoot && (name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(file, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			return err
		}
		directory, err := filepath.Rel(repositoryRoot, filepath.Dir(file))
		if err != nil {
			return err
		}
		files[filepath.ToSlash(directory)] = append(files[filepath.ToSlash(directory)], parsed)
		return nil
	})
	if err != nil {
		t.Fatalf("walk the tests: %v", err)
	}
	readers := map[string][]string{}
	for directory, parsed := range files {
		if tests := readersIn(directory, parsed); len(tests) > 0 {
			readers["./"+directory] = tests
		}
	}
	return readers
}

// readersIn is the tests among one directory's test files that reach the top
// of the checkout, directly or through a name of the same directory that does.
// Files of an external _test package are read with the rest: a test there
// reaches the checkout the same way.
func readersIn(directory string, files []*ast.File) []string {
	depth := strings.Count(directory, "/") + 1
	up := strings.TrimSuffix(strings.Repeat("../", depth), "/")

	// The names declared at the top of these files, and what each one's
	// declaration holds.
	declarations := map[string]ast.Node{}
	var tests []*ast.FuncDecl
	for _, file := range files {
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				if value.Recv != nil || value.Body == nil {
					continue
				}
				if isTest(value) {
					tests = append(tests, value)
					continue
				}
				declarations[value.Name.Name] = value.Body
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					if values, ok := spec.(*ast.ValueSpec); ok {
						for _, name := range values.Names {
							declarations[name.Name] = values
						}
					}
				}
			}
		}
	}
	reaching := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for name, node := range declarations {
			if !reaching[name] && reaches(node, up, depth, reaching) {
				reaching[name] = true
				changed = true
			}
		}
	}
	var found []string
	for _, test := range tests {
		if reaches(test.Body, up, depth, reaching) {
			found = append(found, test.Name.Name)
		}
	}
	sort.Strings(found)
	return found
}

func isTest(function *ast.FuncDecl) bool {
	return strings.HasPrefix(function.Name.Name, "Test") && function.Type.Params != nil && len(function.Type.Params.List) == 1
}

// reaches reports whether a node climbs to the top of the checkout -- a string
// that is the climb, or starts with it and names something there, or a Join
// whose first arguments are each ".." -- or names something that does.
func reaches(node ast.Node, up string, depth int, reaching map[string]bool) bool {
	found := false
	ast.Inspect(node, func(inner ast.Node) bool {
		if found {
			return false
		}
		switch value := inner.(type) {
		case *ast.Ident:
			found = reaching[value.Name]
		case *ast.BasicLit:
			if value.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(value.Value)
			if err != nil {
				return true
			}
			found = climbs(text, up)
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Join" || len(value.Args) < depth {
				return true
			}
			found = true
			for _, argument := range value.Args[:depth] {
				literal, ok := argument.(*ast.BasicLit)
				found = found && ok && literal.Value == `".."`
			}
		}
		return !found
	})
	return found
}

// climbs reports whether a string is a path to the top of the checkout or to
// something that is there, rather than a fixture's path that happens to climb.
func climbs(text, up string) bool {
	if text == up || text == up+"/" {
		return true
	}
	rest, ok := strings.CutPrefix(text, up+"/")
	if !ok {
		return false
	}
	first, _, _ := strings.Cut(rest, "/")
	if first == "" || first == "." || first == ".." {
		return false
	}
	_, err := os.Stat(filepath.Join(repositoryRoot, first))
	return err == nil
}
