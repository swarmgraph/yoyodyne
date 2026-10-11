package cli

import (
	"os"
	"os/exec"
	"testing"
)

// raceSkipTestPath is the suite for the Makefile's race target deciding to
// skip. It fabricates scratch repositories with a copy of the Makefile and a
// stub Go command, so it compiles nothing and reads none of this repository's
// history.
const raceSkipTestPath = "../../scripts/race-skip-test.sh"

// TestTheRaceCheckSkipsOnlyChangesThatCannotReachGo runs that suite. The skip
// is shell in the Makefile and scripts/race-skip.sh, so without this nothing in
// `make check` would execute it, and a skip that grew too wide would pass every
// change it wrongly let through without anybody seeing it.
func TestTheRaceCheckSkipsOnlyChangesThatCannotReachGo(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"bash", "git", "make"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s needs %s, which is not on PATH", raceSkipTestPath, tool)
		}
	}
	suite := shellSuite(t, raceSkipTestPath)
	suite.Env = append(os.Environ(), "MAKEFLAGS=", "MFLAGS=", "MAKELEVEL=0")
	report, err := suite.CombinedOutput()
	if err != nil {
		t.Fatalf("%s did not pass (%v):\n%s", raceSkipTestPath, err, report)
	}
}
