package composition

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// The document checks are `make doccheck` and nothing beside it, so the list of
// tests a document landing runs is written in one place, the Makefile.
func TestTheDocumentChecksAreTheDoccheckTarget(t *testing.T) {
	t.Parallel()

	declared, err := config.Load(repositoryConfigPath)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if want := []string{"make doccheck"}; !slices.Equal(declared.DocumentChecks, want) {
		t.Fatalf("document_checks = %q, want %q: the tests that read the documents are listed in the Makefile's doccheck target, so a second list here is one the two can disagree in", declared.DocumentChecks, want)
	}
}

// Every test the doccheck target names is one its package has. `go test -run`
// passes when its pattern matches nothing, so a test renamed or removed would
// otherwise leave the target reading as covering a document it no longer
// checks.
func TestEveryTestTheDoccheckTargetNamesExists(t *testing.T) {
	t.Parallel()

	invocations, err := runDoccheck(t, "")
	if err != nil {
		t.Fatalf("make doccheck error = %v", err)
	}
	named := map[string][]string{}
	for _, invocation := range invocations {
		named[invocation.pkg] = append(named[invocation.pkg], invocation.pattern)
		if invocation.pattern == "." {
			continue
		}
		names, ok := strings.CutPrefix(invocation.pattern, "^")
		if names, ok = strings.CutSuffix(names, "$"); !ok {
			t.Errorf("%s runs %q, which is not an anchored list of test names", invocation.pkg, invocation.pattern)
			continue
		}
		names = strings.TrimSuffix(strings.TrimPrefix(names, "("), ")")
		declared := testFunctions(t, invocation.pkg)
		for _, name := range strings.Split(names, "|") {
			if !declared[name] {
				t.Errorf("make doccheck runs %s in %s, which has no such test", name, invocation.pkg)
			}
		}
	}
	// The ones the development manager's decision names, held so a list that
	// shrank past them fails here.
	for pkg, pattern := range map[string]string{
		"./internal/terms":       ".",
		"./scripts/vocabulary":   ".",
		"./internal/doclink":     ".",
		"./internal/artifact":    ".",
		"./internal/conformance": ".",
	} {
		if !slices.Contains(named[pkg], pattern) {
			t.Errorf("make doccheck no longer runs the whole of %s", pkg)
		}
	}
	if patterns := strings.Join(named["./internal/contextbundle"], " "); !strings.Contains(patterns, "TestShippedDocumentationNamesDocumentsThisRepositoryHas") {
		t.Error("make doccheck no longer runs the shipped-documentation size gate")
	}
}

// One failing package fails the target, and the packages after it still run,
// so a landing is told every test its document broke rather than the first.
func TestTheDoccheckTargetRunsEveryEntryAndFailsIfAnyFails(t *testing.T) {
	t.Parallel()

	passing, err := runDoccheck(t, "")
	if err != nil {
		t.Fatalf("make doccheck error = %v", err)
	}
	failing, err := runDoccheck(t, "./internal/doclink")
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("make doccheck with a failing package error = %v, want it to exit non-zero", err)
	}
	if len(failing) != len(passing) {
		t.Fatalf("with one package failing the target ran %d entries, want all %d", len(failing), len(passing))
	}
}

type doccheckInvocation struct {
	pkg     string
	pattern string
}

// runDoccheck runs the repository's doccheck target over a stand-in go command
// that records each `go test` it is given and fails the one for failPackage.
func runDoccheck(t *testing.T, failPackage string) ([]doccheckInvocation, error) {
	t.Helper()

	requireTool(t, "make")
	makefile, err := filepath.Abs(filepath.Join(repositoryRoot, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	record := filepath.Join(root, "invocations.txt")
	stand := "#!/bin/sh\n" +
		"if [ \"$1\" = env ]; then echo '" + filepath.Join(root, "cache") + "'; exit 0; fi\n" +
		"for argument in \"$@\"; do last=$argument; done\n" +
		"printf '%s\\n' \"$*\" >> '" + record + "'\n" +
		"[ \"$last\" != '" + failPackage + "' ]\n"
	goCommand := filepath.Join(root, "go")
	if err := os.WriteFile(goCommand, []byte(stand), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("make", "--no-print-directory", "-f", makefile, "doccheck", "GO="+goCommand, "VERSION=doccheck-test")
	command.Dir = root
	command.Env = append(os.Environ(), "MAKEFLAGS=", "MFLAGS=", "MAKELEVEL=0")
	output, runErr := command.CombinedOutput()
	recorded, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("make doccheck ran no go test: %v\n%s", err, output)
	}
	var invocations []doccheckInvocation
	for _, line := range strings.Split(strings.TrimSpace(string(recorded)), "\n") {
		fields := strings.Fields(line)
		run := slices.Index(fields, "-run")
		if len(fields) == 0 || fields[0] != "test" || run < 0 || run+2 != len(fields)-1 {
			t.Fatalf("make doccheck ran go %q, want go test ... -run <pattern> <package>", line)
		}
		invocations = append(invocations, doccheckInvocation{pkg: fields[len(fields)-1], pattern: fields[run+1]})
	}
	if len(invocations) == 0 {
		t.Fatalf("make doccheck ran no go test\n%s", output)
	}
	return invocations, runErr
}

var testFunction = regexp.MustCompile(`(?m)^func (Test\w+)\(t \*testing\.T\)`)

// testFunctions is the test functions declared in one package's directory.
func testFunctions(t *testing.T, pkg string) map[string]bool {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(repositoryRoot, filepath.FromSlash(pkg), "*_test.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("%s holds no test files (%v)", pkg, err)
	}
	declared := map[string]bool{}
	for _, file := range files {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range testFunction.FindAllStringSubmatch(string(source), -1) {
			declared[match[1]] = true
		}
	}
	return declared
}
