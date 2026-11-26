package claudecode

import (
	"fmt"
	"os"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// testScratch is a scratch folder cut for testRunID, for the tests that are not
// about it: every developer invocation has to carry one.
var testScratch string

// testState is the state directory it is cut in, for a session of any other role.
var testState string

func TestMain(m *testing.M) {
	if handled := actAsLocalServiceProbe(); handled {
		return
	}
	state, err := os.MkdirTemp("", "yoyodyne-claudecode-test-")
	if err == nil {
		testState = state
		testScratch, err = execution.PrepareScratchDirectory(state, testRunID)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(state)
	os.Exit(code)
}
