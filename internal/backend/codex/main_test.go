package codex

import (
	"fmt"
	"os"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// testScratch is a scratch folder cut for the run these tests make, for the
// tests that are not about it: every developer invocation has to carry one.
var testScratch string

// TestMain gives every test that names no provider home an empty user home, so
// the Codex home such a test falls back to is an empty one of its own. An
// invocation whose account home holds an instruction file keeps the home it
// runs under inside that account home, so without this a test would write into
// the Codex home of whoever runs the suite.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "yoyodyne-codex-test-home-")
	if err == nil {
		err = os.Setenv("HOME", home)
	}
	if err == nil {
		err = os.Unsetenv(ProviderHomeVariable)
	}
	if err == nil {
		testScratch, err = execution.PrepareScratchDirectory(home, testRunID)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
