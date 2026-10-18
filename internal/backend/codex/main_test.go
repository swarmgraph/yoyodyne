package codex

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points every test that names no provider home at an empty one of
// its own. An invocation whose account home holds an instruction file writes
// the home it runs under inside that account home, so without this a test
// would write into the Codex home of whoever runs the suite.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "yoyodyne-codex-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv(ProviderHomeVariable, home); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
