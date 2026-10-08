package runstate

import (
	"strings"
	"testing"
	"time"
)

// A landing check that could not run is neither a pass nor a failure: the
// landing is green on the checks that ran, and says which one could not.
func TestALandingCheckThatCouldNotRunMakesNothingRed(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	finished := started.Add(time.Minute)
	landing := LandingChecks{
		Commit: "0123456789abcdef", StartedAt: started, FinishedAt: &finished, Ran: true, Green: true,
		Checks: []LandingCheckResult{
			{Command: "make test", Passed: true},
			{Command: "make codex-resume", ExitCode: 2, CouldNotRun: "codex is not installed"},
		},
	}
	if !landing.AllPassed() || landing.Red() {
		t.Fatalf("AllPassed = %t, Red = %t, want a green landing", landing.AllPassed(), landing.Red())
	}
	if _, failing := landing.Failing(); failing {
		t.Fatal("Failing() named the check that could not run")
	}
	if said := landing.Describe(); !strings.Contains(said, "green landing") || !strings.Contains(said, "make codex-resume could not run: codex is not installed") {
		t.Fatalf("Describe() = %q", said)
	}
}
