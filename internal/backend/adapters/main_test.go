package adapters

import (
	"fmt"
	"os"
	"testing"
)

// TestMain gives these tests an empty user home and no CODEX_HOME, so a Codex
// invocation they make falls back to an empty Codex home of its own. The Codex
// adapter keeps the home it runs under inside an account home that holds an
// instruction file, so without this a test would write into the Codex home of
// whoever runs the suite (internal/backend/codex/context.go).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "yoyodyne-test-home-")
	if err == nil {
		err = os.Setenv("HOME", home)
	}
	if err == nil {
		err = os.Unsetenv("CODEX_HOME")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
