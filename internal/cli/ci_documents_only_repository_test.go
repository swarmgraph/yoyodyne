package cli

import (
	"os/exec"
	"testing"
)

// ciDocumentsOnlyTestPath is the suite for the first step of every job in
// .github/workflows/ci.yml, the one that lets a job pass without running when a
// pull request changes only documents in the governed homes. It runs the
// step's script against a stub of GitHub's compare API, so it needs no network
// and reads none of this repository's history.
const ciDocumentsOnlyTestPath = "../../scripts/ci-documents-only-test.sh"

// TestCIPassesAtOnceOnlyForChangesMadeOfGovernedDocuments runs that suite. The
// step is shell inside the workflow and runs only on GitHub, so without this
// nothing in `make check` would execute it, and a rule that grew too wide would
// let a code change through every required check unchecked.
func TestCIPassesAtOnceOnlyForChangesMadeOfGovernedDocuments(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"bash", "awk", "sed"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s needs %s, which is not on PATH", ciDocumentsOnlyTestPath, tool)
		}
	}
	report, err := shellSuite(t, ciDocumentsOnlyTestPath).CombinedOutput()
	if err != nil {
		t.Fatalf("%s did not pass (%v):\n%s", ciDocumentsOnlyTestPath, err, report)
	}
}
