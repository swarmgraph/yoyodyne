package cli

import (
	"os"
	"os/exec"
	"testing"
)

// checkScopeTestPath is the suite for the Makefile's test and race targets
// choosing the packages they run over from the ones the harness says a change
// touches. It fabricates scratch repositories with a copy of the Makefile and a
// stub Go command, so it compiles nothing and reads none of this repository's
// history.
const checkScopeTestPath = "../../scripts/check-scope-test.sh"

// TestTheChecksNarrowOnlyToWhatTheChangeCanReach runs that suite. The choice is
// shell in the Makefile and scripts/check-scope.sh, so without this nothing in
// `make check` would execute it, and a scope that grew too narrow would pass
// every change it wrongly left untested without anybody seeing it.
func TestTheChecksNarrowOnlyToWhatTheChangeCanReach(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"bash", "git", "make"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s needs %s, which is not on PATH", checkScopeTestPath, tool)
		}
	}
	suite := shellSuite(t, checkScopeTestPath)
	// As the race-skip suite is, this one is run with the scope the harness's own
	// checks name already in its environment, and must not be swayed by it.
	suite.Env = append(os.Environ(), "MAKEFLAGS=", "MFLAGS=", "MAKELEVEL=0", "CHANGED_GO_PACKAGES=./internal/cli", "RACE_BASE=HEAD")
	report, err := suite.CombinedOutput()
	if err != nil {
		t.Fatalf("%s did not pass (%v):\n%s", checkScopeTestPath, err, report)
	}
}
