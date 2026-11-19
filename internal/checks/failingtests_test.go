package checks

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// goReading is the reading this repository declares for its Go checks in
// .yoyodyne/config.yaml, with the module path of the recorded output below.
var goReading = TestReading{
	Checks:      []string{"make test", "make race"},
	Test:        `^\s*--- FAIL: (?P<test>[^\s/]+)`,
	File:        `^\s+(?P<file>[^\s/:]+_test\.go):[0-9]+: `,
	Group:       `^FAIL\s+(?P<directory>\S+)`,
	StripPrefix: "example.com/shop",
}

// The outputs below are what go test printed for a small module, recorded
// as it printed them.
const goOneFailure = `--- FAIL: TestTotals (0.00s)
    alpha_test.go:7: total = 3, want 4
FAIL
FAIL	example.com/shop/internal/alpha	0.151s
FAIL
`

const goSeveralFailures = `--- FAIL: TestTotals (0.00s)
    alpha_test.go:7: total = 3, want 4
FAIL
FAIL	example.com/shop/internal/alpha	0.226s
--- FAIL: TestOrders (0.00s)
    --- FAIL: TestOrders/empty (0.00s)
        beta_test.go:6: no orders
--- FAIL: TestRefunds (0.00s)
    more_test.go:5: refund missing
FAIL
FAIL	example.com/shop/internal/beta	0.417s
ok  	example.com/shop	0.120s
FAIL
`

// goBuildFailure has a package that failed to build beside one that named its
// failing test: the build failure names no test, so the whole failure cannot
// be read as named tests.
const goBuildFailure = `# example.com/shop/internal/gamma [example.com/shop/internal/gamma.test]
internal/gamma/gamma.go:3:28: cannot use "x" (untyped string constant) as int value in return statement
--- FAIL: TestTotals (0.00s)
    alpha_test.go:7: total = 3, want 4
FAIL
FAIL	example.com/shop/internal/alpha	0.148s
FAIL	example.com/shop/internal/gamma [build failed]
FAIL
`

func TestTheGoReadingNamesOneFailingTestWithItsFile(t *testing.T) {
	t.Parallel()

	got := ReadFailingTests(goReading, goOneFailure)
	want := FailingTests{Tests: []FailingTest{{Name: "TestTotals", File: "internal/alpha/alpha_test.go"}}}
	if !reflect.DeepEqual(got, want) || !got.Named() {
		t.Fatalf("ReadFailingTests() = %#v, want %#v", got, want)
	}
	if text := got.Describe(); text != "Failing tests, each with its file: TestTotals (internal/alpha/alpha_test.go)." {
		t.Fatalf("Describe() = %q", text)
	}
}

func TestTheGoReadingNamesSeveralFailingTestsAcrossPackages(t *testing.T) {
	t.Parallel()

	got := ReadFailingTests(goReading, goSeveralFailures)
	want := FailingTests{Tests: []FailingTest{
		{Name: "TestTotals", File: "internal/alpha/alpha_test.go"},
		// A subtest's failure is its test's, and the file is where it failed.
		{Name: "TestOrders", File: "internal/beta/beta_test.go"},
		{Name: "TestRefunds", File: "internal/beta/more_test.go"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadFailingTests() = %#v, want %#v", got, want)
	}
}

func TestOutputThatCannotBeReadAsNamedTestsSaysWhyAndNamesNone(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, output, unread string
	}{
		{"a package that did not build", goBuildFailure, "example.com/shop/internal/gamma failed without naming a failing test"},
		{"a test binary that timed out", "panic: test timed out after 1s\n\trunning tests:\n\t\tTestSlow (1s)\nFAIL\texample.com/shop/internal/alpha\t1.012s\nFAIL\n",
			"example.com/shop/internal/alpha failed without naming a failing test"},
		{"a race the detector found, reported from the testing package's own file", "--- FAIL: TestTotals (0.01s)\n    testing.go:1617: race detected during execution of test\nFAIL\nFAIL\texample.com/shop/internal/alpha\t0.3s\n",
			"TestTotals was named failing without the file it is in"},
		{"output cut short after a failing test", "--- FAIL: TestTotals (0.00s)\n    alpha_test.go:7: total = 3, want 4\n",
			"TestTotals were named failing with no line after them saying where, so the output may be cut short"},
		{"a package outside the module", "--- FAIL: TestX (0.00s)\n    x_test.go:1: no\nFAIL\tother.org/x\t0.1s\n",
			"other.org/x does not begin with example.com/shop, so it is not a directory in the repository"},
		{"nothing a runner would print", "make: *** [test] Error 1\n", "the output names no failing test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ReadFailingTests(goReading, tc.output)
			if got.Named() || len(got.Tests) != 0 || got.Unread != tc.unread {
				t.Fatalf("ReadFailingTests() = %#v, want unread %q and no tests", got, tc.unread)
			}
			if text := got.Describe(); !strings.HasPrefix(text, "The failing tests could not be named with their files") || !strings.Contains(text, tc.unread) {
				t.Fatalf("Describe() = %q", text)
			}
		})
	}
}

// A runner that prints the test and its file on one line needs no file or
// group expression, which is what keeps the reading from knowing any language.
func TestAReadingMayNameTheTestAndItsFileOnOneLine(t *testing.T) {
	t.Parallel()

	reading := TestReading{Checks: []string{"pytest"}, Test: `^FAILED (?P<file>[^:\s]+)::(?P<test>\S+)`}
	if problems := reading.Problems(); len(problems) != 0 {
		t.Fatalf("Problems() = %q", problems)
	}
	got := ReadFailingTests(reading, "tests/test_cart.py F.\nFAILED tests/test_cart.py::test_total - assert 3 == 4\nFAILED tests/test_refund.py::test_missing\n1 failed\n")
	want := FailingTests{Tests: []FailingTest{{Name: "test_total", File: "tests/test_cart.py"}, {Name: "test_missing", File: "tests/test_refund.py"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadFailingTests() = %#v, want %#v", got, want)
	}
}

func TestOnlyTheFirstTwentyFailingTestsAreListedAndTheRestCounted(t *testing.T) {
	t.Parallel()

	var output strings.Builder
	for index := range 23 {
		output.WriteString("--- FAIL: Test" + strings.Repeat("A", index+1) + " (0.00s)\n    a_test.go:1: no\n")
	}
	output.WriteString("FAIL\texample.com/shop\t0.1s\n")
	got := ReadFailingTests(goReading, output.String())
	if len(got.Tests) != MaxFailingTests || got.More != 3 || got.Tests[0].File != "a_test.go" {
		t.Fatalf("ReadFailingTests() = %d tests, %d more, first %#v", len(got.Tests), got.More, got.Tests[0])
	}
	if !strings.HasSuffix(got.Describe(), ", and 3 more.") {
		t.Fatalf("Describe() = %q", got.Describe())
	}
}

func TestAReadingThatCannotBeUsedIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		reading TestReading
		problem string
	}{
		{TestReading{Test: `x(?P<test>.)`, File: `(?P<file>.)`}, "names no check it applies to"},
		{TestReading{Checks: []string{"make test"}}, "has no test expression"},
		{TestReading{Checks: []string{"make test"}, Test: `(`}, "has a test expression that is not a regular expression"},
		{TestReading{Checks: []string{"make test"}, Test: `--- FAIL: (\S+)`, File: `(?P<file>.)`}, "has a test expression with no (?P<test>...) group"},
		{TestReading{Checks: []string{"make test"}, Test: `(?P<test>.)`}, "nothing names a failing test's file"},
		{TestReading{Checks: []string{"make test"}, Test: `(?P<test>.)`, File: `(?P<file>.)`, Group: `FAIL (\S+)`}, "has a group expression with no (?P<directory>...) group"},
		{TestReading{Checks: []string{"make test"}, Test: `(?P<test>.)`, File: `(?P<file>.)`, StripPrefix: "example.com"}, "strip_prefix but no group expression"},
	} {
		problems := strings.Join(tc.reading.Problems(), "; ")
		if !strings.Contains(problems, tc.problem) {
			t.Errorf("Problems() of %#v = %q, want it to say %q", tc.reading, problems, tc.problem)
		}
	}
	if problems := goReading.Problems(); len(problems) != 0 {
		t.Fatalf("the Go reading's Problems() = %q", problems)
	}
}

func TestTheLongestCheckNameDecidesWhichReadingACommandGets(t *testing.T) {
	t.Parallel()

	race := TestReading{Checks: []string{"make race"}, Test: "race"}
	readings := []TestReading{{Checks: []string{"make"}, Test: "any"}, race}
	if got, ok := ReadingFor(readings, `make race CHANGED_GO_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"`); !ok || got.Test != "race" {
		t.Fatalf("ReadingFor(make race ...) = %#v, %t", got, ok)
	}
	if _, ok := ReadingFor(readings, "maker"); ok {
		t.Fatal("ReadingFor(maker) applied a reading named make")
	}
	if _, ok := ReadingFor(nil, "make test"); ok {
		t.Fatal("ReadingFor with no readings applied one")
	}
}

// A failed check a reading names has its failing tests on its result and at
// the top of its failure output; a check no reading names is recorded as it
// always was.
func TestTheRunnerRecordsAFailedChecksFailingTestsThroughItsReading(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	for name, output := range map[string]string{"several.txt": goSeveralFailures, "build.txt": goBuildFailure} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(output), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(command string) Result {
		t.Helper()
		results, _, err := (Runner{Process: execution.OSProcessRunner{}, FailingTests: []TestReading{goReading}}).Run(
			context.Background(),
			Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: directory, Commands: []string{command}},
			nil,
		)
		if err != nil || len(results) != 1 || results[0].Passed {
			t.Fatalf("Run(%q) = %#v, %v", command, results, err)
		}
		return results[0]
	}

	named := run("make test || { cat several.txt; exit 1; }")
	if named.FailingTests == nil || len(named.FailingTests.Tests) != 3 || named.FailingTests.Tests[2] != (FailingTest{Name: "TestRefunds", File: "internal/beta/more_test.go"}) {
		t.Fatalf("FailingTests = %#v", named.FailingTests)
	}
	lines := strings.SplitN(named.FailureOutput, "\n", 3)
	if !strings.HasPrefix(lines[0], failingNamesTitle) || lines[1] != named.FailingTests.Describe() {
		t.Fatalf("FailureOutput opens %q", lines[:2])
	}
	// The names the output has always opened with are still read back first.
	if names := FailingNames(named); !reflect.DeepEqual(names, []string{"TestTotals", "example.com/shop/internal/alpha", "TestOrders", "TestOrders/empty", "TestRefunds", "example.com/shop/internal/beta"}) {
		t.Fatalf("FailingNames() = %q", names)
	}

	unread := run("make test || { cat build.txt; exit 1; }")
	if unread.FailingTests == nil || unread.FailingTests.Named() || unread.FailingTests.Unread == "" || unread.CouldNotRun != "" {
		t.Fatalf("FailingTests = %#v, could not run %q", unread.FailingTests, unread.CouldNotRun)
	}
	if !strings.Contains(unread.FailureOutput, "could not be named with their files") {
		t.Fatalf("FailureOutput = %q", unread.FailureOutput)
	}

	plain := run("make vet || { cat several.txt; exit 1; }")
	if plain.FailingTests != nil || strings.Contains(plain.FailureOutput, "each with its file") {
		t.Fatalf("a check no reading names recorded %#v and %q", plain.FailingTests, plain.FailureOutput)
	}
}
