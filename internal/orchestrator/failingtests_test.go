package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// goTestReading is the shape of the reading this repository declares for its
// Go checks, for a module named example.com/shop.
func goTestReading(names ...string) checks.TestReading {
	return checks.TestReading{
		Checks:      names,
		Test:        `^\s*--- FAIL: (?P<test>[^\s/]+)`,
		File:        `^\s+(?P<file>[^\s/:]+_test\.go):[0-9]+: `,
		Group:       `^FAIL\s+(?P<directory>\S+)`,
		StripPrefix: "example.com/shop",
	}
}

// After a failed check its run's record lists each failing test with its
// file, and a failure the reading cannot name is recorded as such and is
// otherwise the same failed check.
func TestAFailedChecksRunRecordListsEachFailingTestWithItsFile(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	outputs := map[string]string{
		"named.txt": "--- FAIL: TestTotals (0.00s)\n    alpha_test.go:7: total = 3, want 4\nFAIL\nFAIL\texample.com/shop/internal/alpha\t0.2s\n" +
			"--- FAIL: TestRefunds (0.00s)\n    more_test.go:5: refund missing\nFAIL\nFAIL\texample.com/shop/internal/beta\t0.4s\nFAIL\n",
		"unread.txt": "# example.com/shop/internal/gamma\ninternal/gamma/gamma.go:3:28: cannot use \"x\"\nFAIL\texample.com/shop/internal/gamma [build failed]\nFAIL\n",
	}
	for name, output := range outputs {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(output), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	record := func(command string, changed ...string) runstate.State {
		t.Helper()
		results, _, err := (checks.Runner{Process: execution.OSProcessRunner{}, FailingTests: []checks.TestReading{goTestReading("cat")}}).Run(
			context.Background(), checks.Request{RunID: "run-failing-tests", Directory: directory, Commands: []string{command}}, nil)
		if err != nil || len(results) != 1 || results[0].Passed {
			t.Fatalf("Run(%q) = %#v, %v", command, results, err)
		}
		a := activeRun{state: runstate.State{}}
		a.recordCheckFailure(checkFailure{result: results[0], changed: changed, changedListed: changed != nil})
		return a.state
	}

	named := record("cat named.txt; exit 1").CheckFailure
	want := []checks.FailingTest{{Name: "TestTotals", File: "internal/alpha/alpha_test.go"}, {Name: "TestRefunds", File: "internal/beta/more_test.go"}}
	if named.FailingTests == nil || len(named.FailingTests.Tests) != 2 || named.FailingTests.Tests[0] != want[0] || named.FailingTests.Tests[1] != want[1] {
		t.Fatalf("check_failure.failing_tests = %#v, want %#v", named.FailingTests, want)
	}
	if !strings.Contains(named.Output, "Failing tests, each with its file: TestTotals (internal/alpha/alpha_test.go), TestRefunds (internal/beta/more_test.go).") {
		t.Fatalf("check_failure.output = %q, want it to name the tests with their files", named.Output)
	}

	unread := record("cat unread.txt; exit 1").CheckFailure
	if unread.FailingTests == nil || unread.FailingTests.Named() || !strings.Contains(unread.FailingTests.Unread, "internal/gamma failed without naming a failing test") {
		t.Fatalf("check_failure.failing_tests = %#v, want it to say the failure could not be read", unread.FailingTests)
	}
	if unread.Command != "cat unread.txt; exit 1" || unread.ExitCode != 1 {
		t.Fatalf("check_failure = %#v, want the failed check recorded as before", unread)
	}
	if unread.TouchedTests == nil || unread.TouchedTests.Confined {
		t.Fatalf("check_failure.touched_tests = %#v, want an unread failure judged an ordinary one", unread.TouchedTests)
	}
}

// After a failed check whose failing tests were named, the run's record says
// whether the failure is confined to tests the change does not touch, and why.
func TestAFailedChecksRunRecordSaysWhetherItsFailureIsConfinedToUntouchedTests(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	output := "--- FAIL: TestTotals (0.00s)\n    alpha_test.go:7: total = 3, want 4\nFAIL\nFAIL\texample.com/shop/internal/alpha\t0.2s\nFAIL\n"
	if err := os.WriteFile(filepath.Join(directory, "named.txt"), []byte(output), 0o600); err != nil {
		t.Fatal(err)
	}
	record := func(failing checkFailure) *runstate.CheckFailure {
		t.Helper()
		results, _, err := (checks.Runner{Process: execution.OSProcessRunner{}, FailingTests: []checks.TestReading{goTestReading("cat")}}).Run(
			context.Background(), checks.Request{RunID: "run-touched-tests", Directory: directory, Commands: []string{"cat named.txt; exit 1"}}, nil)
		if err != nil || len(results) != 1 || results[0].Passed {
			t.Fatalf("Run = %#v, %v", results, err)
		}
		failing.result = results[0]
		a := activeRun{state: runstate.State{}}
		a.recordCheckFailure(failing)
		return a.state.CheckFailure
	}

	untouched := record(checkFailure{changed: []string{"internal/beta/beta.go"}, changedListed: true}).TouchedTests
	if untouched == nil || !untouched.Confined || len(untouched.Tests) != 1 || untouched.Tests[0].Touched ||
		!strings.Contains(untouched.Reason, "Every failing test is one the change does not touch: TestTotals (internal/alpha/alpha_test.go)") {
		t.Fatalf("check_failure.touched_tests = %#v, want the failure confined to the untouched test", untouched)
	}
	touched := record(checkFailure{changed: []string{"internal/alpha/alpha.go"}, changedListed: true}).TouchedTests
	if touched == nil || touched.Confined || !touched.Tests[0].Touched || !strings.Contains(touched.Reason, "TestTotals (internal/alpha/alpha.go changed in its directory)") {
		t.Fatalf("check_failure.touched_tests = %#v, want an ordinary failure naming what touches the test", touched)
	}
	unlisted := record(checkFailure{}).TouchedTests
	if unlisted == nil || unlisted.Confined {
		t.Fatalf("check_failure.touched_tests = %#v, want a failure whose paths were not listed judged an ordinary one", unlisted)
	}
}

// A merge queue candidate handed back for a check it failed names each failing
// test with its file, so its developer is told which test failed rather than
// only which check.
func TestACandidateHandedBackForAFailedCheckNamesItsFailingTests(t *testing.T) {
	t.Parallel()

	f := newPromotionFixture(t, landLocally)
	failing := `! grep -q implemented feature.txt || { printf -- '--- FAIL: TestFeature (0.00s)\n    feature_test.go:3: implemented\nFAIL\nFAIL\texample.com/shop/internal/feature\t0.1s\n'; exit 1; }`
	f.worker.Pipeline.Config.Checks = []string{"test -f feature.txt", failing}
	f.worker.Pipeline.Config.Execution.RepairAttemptsBeforeReplan = 2
	runner := f.checks.runner.(checks.Runner)
	runner.FailingTests = []checks.TestReading{goTestReading("!")}
	f.checks.runner = runner
	f.run.admittedRun.state.Branch = "change"
	f.promoter.Repair = &recordingRepair{}
	if verification, err := f.work(); err != nil || verification.Verified {
		t.Fatalf("Work() = %#v, %v; want the candidate refused", verification, err)
	}
	const named = "Failing tests, each with its file: TestFeature (internal/feature/feature_test.go)"
	result := f.generations()[0].CheckRun.Results[1]
	if result.Passed || !strings.HasPrefix(result.FailingTests, named) {
		t.Fatalf("the candidate's record of %s = %#v, want its failing test named", failing, result)
	}
	recovered, err := f.recover()
	if err != nil || recovered.Failure.Class != runstate.MergeQueueCandidateDefect || !strings.Contains(recovered.Failure.Reason, named) {
		t.Fatalf("Recover() = %#v, %v; want the change's defect, naming its failing test", recovered, err)
	}
	if handback := f.landing().Handback; handback == nil || !strings.Contains(handback.Reason, named) || !strings.HasPrefix(handback.Reason, "! grep -q implemented feature.txt") {
		t.Fatalf("handback = %#v, want it to name the failed check and its failing test", handback)
	}
}
