package orchestrator

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestFailingTestsAreNamedWithTheirPackageFromGoTestOutput(t *testing.T) {
	t.Parallel()

	log := "2026-11-10T17:17:01.1234567Z ok  \texample.com/m/internal/cli\t3.2s\n" +
		"2026-11-10T17:17:02.0000000Z --- FAIL: TestOne (0.10s)\n" +
		"    --- FAIL: TestOne/sub_case (0.01s)\n" +
		"--- FAIL: TestTwo (0.20s)\n" +
		"FAIL\n" +
		"FAIL\texample.com/m/internal/a\t1.0s\n" +
		"--- FAIL: TestThree (0.30s)\n" +
		"FAIL\texample.com/m/internal/b\t2.0s\n" +
		"FAIL\n"
	tests, unnamed := forgeTestNames(log)
	want := []forgeTestName{{"TestOne", "example.com/m/internal/a"}, {"TestTwo", "example.com/m/internal/a"}, {"TestThree", "example.com/m/internal/b"}}
	if !reflect.DeepEqual(tests, want) || len(unnamed) != 0 {
		t.Fatalf("forgeTestNames() = %v, %v; want %v and nothing unnamed", tests, unnamed, want)
	}

	for name, log := range map[string]string{
		"build failure":        "FAIL\texample.com/m/internal/a [build failed]\n",
		"timed out":            "panic: test timed out after 10m0s\nFAIL\texample.com/m/internal/a\t600.0s\n",
		"cut short":            "--- FAIL: TestOne (0.10s)\n",
		"not a Go test at all": "##[error]Process completed with exit code 1.",
	} {
		if _, unnamed := forgeTestNames(log); len(unnamed) == 0 {
			t.Errorf("%s: nothing reported unnamed", name)
		}
	}
}

func TestATestIsTouchedByAChangedPathInItsOwnDirectory(t *testing.T) {
	t.Parallel()

	pkg := "example.com/m/internal/gitworktree"
	for _, c := range []struct {
		changed []string
		touched bool
	}{
		{[]string{"internal/gitworktree/worktree_test.go"}, true},
		{[]string{"internal/gitworktree/README.md"}, true},
		{[]string{"internal/cli/run.go", "docs/operations.md"}, false},
		{[]string{"internal/gitworktree/deep/file.go"}, false},
		{[]string{"worktree/file.go"}, false},
		{[]string{"README.md", "Makefile"}, false},
		{[]string{"go.mod"}, true},
		{[]string{"main.go"}, true},
		// A rename is listed under its old path and its new one.
		{[]string{"internal/cli/moved.go", "internal/gitworktree/moved.go"}, true},
	} {
		if got := forgeTestTouched(pkg, c.changed); got != c.touched {
			t.Errorf("forgeTestTouched(%q, %v) = %t, want %t", pkg, c.changed, got, c.touched)
		}
	}
}

type fixedLog struct {
	log string
	err error
}

func (l fixedLog) JobLogTail(context.Context, int64, int) (string, error) { return l.log, l.err }

func TestAFailureIsConfinedToUntouchedTestsOnlyWhenEveryOneIsNamedUntouchedAndThereAreFewOfThem(t *testing.T) {
	t.Parallel()

	failing := []runstate.FailingCheck{{Name: "build", CheckRun: 5}}
	changed := []string{"internal/cli/run.go"}
	var six strings.Builder
	for _, name := range []string{"TestA", "TestB", "TestC", "TestD", "TestE", "TestF"} {
		six.WriteString("--- FAIL: " + name + " (0.01s)\n")
	}
	six.WriteString("FAIL\texample.com/m/internal/far\t1.0s\n")

	for _, c := range []struct {
		name      string
		logs      ForgeJobLogTail
		changed   []string
		unknown   string
		untouched bool
	}{
		{"one untouched", fixedLog{log: "--- FAIL: TestA (0.01s)\nFAIL\texample.com/m/internal/far\t1.0s\n"}, changed, "", true},
		{"one touched", fixedLog{log: "--- FAIL: TestA (0.01s)\nFAIL\texample.com/m/internal/cli\t1.0s\n"}, changed, "", false},
		{"six untouched", fixedLog{log: six.String()}, changed, "", false},
		{"log unread", fixedLog{err: errors.New("HTTP 404")}, changed, "", false},
		{"paths unknown", fixedLog{log: "--- FAIL: TestA (0.01s)\nFAIL\texample.com/m/internal/far\t1.0s\n"}, nil, "GitHub listed no file", false},
		{"no logs wired", nil, changed, "", false},
	} {
		judgment := judgeForgeTests(context.Background(), c.logs, failing, c.changed, c.unknown)
		if judgment.Untouched() != c.untouched {
			t.Errorf("%s: Untouched() = %t, want %t (%s)", c.name, judgment.Untouched(), c.untouched, judgment.Describe())
		}
	}
}
