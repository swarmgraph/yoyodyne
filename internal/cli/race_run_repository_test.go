package cli

import (
	"os/exec"
	"testing"
)

// raceRunTestPath is the suite for scripts/race-run.sh, which `make race` runs
// and which splits internal/orchestrator's tests into parts run side by side.
// It fabricates a small module in a scratch directory and compiles it with the
// Go command on PATH, without the race detector.
const raceRunTestPath = "../../scripts/race-run-test.sh"

// TestTheRaceCheckRunsEveryOrchestratorTestOnceWhenSplit runs that suite. The
// split is shell, so without this nothing in `make check` would execute it, and
// a split that dropped a test would pass every change that test should have
// caught without anybody seeing it.
func TestTheRaceCheckRunsEveryOrchestratorTestOnceWhenSplit(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"bash", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s needs %s, which is not on PATH", raceRunTestPath, tool)
		}
	}
	report, err := shellSuite(t, raceRunTestPath).CombinedOutput()
	if err != nil {
		t.Fatalf("%s did not pass (%v):\n%s", raceRunTestPath, err, report)
	}
}
