package checks

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestFailureOutputKeepsTheEndWhereNoLineReportsAFailure(t *testing.T) {
	output := strings.Repeat("make: *** [build] Error 1\n", 1000) + "last diagnostic"
	rendered := FailureOutput(Result{Process: processWith(output)})
	for _, required := range []string{"so only its end is kept", failureCut, "last diagnostic"} {
		if !strings.Contains(rendered, required) {
			t.Errorf("missing %q: %s", required, rendered)
		}
	}
	if strings.Contains(rendered, failureWindowTitle) {
		t.Errorf("kept a window with no failure line: %s", rendered)
	}
	if len(rendered) > failureOutputLimit {
		t.Errorf("output is %d bytes, over the bound", len(rendered))
	}
}

func TestFailureOutputKeepsAShortOutputOnce(t *testing.T) {
	output := "=== RUN   TestBroken\n--- FAIL: TestBroken (0.01s)\n    broken_test.go:12: wanted 3\nFAIL\n"
	rendered := FailureOutput(Result{Process: processWith(output)})
	if strings.Contains(rendered, failureWindowTitle) || strings.Count(rendered, "wanted 3") != 1 {
		t.Errorf("short output was not kept whole and once: %s", rendered)
	}
}

func TestFailureOutputWindowIsBoundedAroundTheFirstFailure(t *testing.T) {
	output := strings.Repeat(strings.Repeat("x", 300)+"\n", 10) +
		"--- FAIL: TestFirst (0.01s)\n" + strings.Repeat(strings.Repeat("y", 1000)+"\n", 10) +
		"--- FAIL: TestSecond (0.01s)\n" + strings.Repeat("z\n", 10000)
	var capture failureCapture
	for _, line := range strings.SplitAfter(output, "\n") {
		capture.add(strings.TrimSuffix(line, "\n"))
	}
	rendered := capture.render()
	window, _, found := strings.Cut(rendered, failureTailTitle)
	if !found {
		t.Fatalf("no end of output: %s", rendered)
	}
	for _, required := range []string{failureWindowTitle, failureWindowLeadCut, "--- FAIL: TestFirst", failureWindowRestCut} {
		if !strings.Contains(window, required) {
			t.Errorf("window missing %q", required)
		}
	}
	if strings.Contains(window, "TestSecond (") || strings.Count(window, strings.Repeat("x", 300)) > failureWindowLead {
		t.Errorf("window is not bounded around the first failure: %s", window)
	}
	if len(rendered) > failureOutputLimit {
		t.Errorf("output is %d bytes, over the bound", len(rendered))
	}
}

func processWith(stdout string) execution.ProcessResult {
	return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stdout: stdout}
}
