package testharness

import (
	"os"
	"path/filepath"
	"testing"
)

// WriteUpstream puts a file where an upstream artifact lives, which is what a
// developer with an editor in its worktree does when it decides the intent it
// was given should say something else.
func WriteUpstream(t *testing.T, worktree, relative, content string) error {
	t.Helper()
	full := filepath.Join(worktree, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o600)
}
