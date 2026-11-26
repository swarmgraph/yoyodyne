package gitworktree

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// resolutionRepository is a target, main, and an approved head that conflicts
// with it in conflict.txt and merges cleanly in settled.txt. It answers the
// repository, main's commit and the approved head.
func resolutionRepository(t *testing.T) (string, string, string) {
	t.Helper()
	repository, _ := queueRepository(t)
	runGit(t, repository, "switch", "-c", "approved", "main")
	writeFile(t, repository, "conflict.txt", "the change's line\n")
	writeFile(t, repository, "settled.txt", "added by the change\n")
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-m", "the approved change")
	approved := gitLine(t, repository, "rev-parse", "HEAD")
	runGit(t, repository, "switch", "main")
	writeFile(t, repository, "conflict.txt", "main's line\n")
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-m", "main moved")
	return repository, gitLine(t, repository, "rev-parse", "HEAD"), approved
}

// resolvedOn commits a resolution on target with the files given.
func resolvedOn(t *testing.T, repository, target string, files map[string]string) string {
	t.Helper()
	runGit(t, repository, "switch", "-c", "resolved-"+strings.Join(slices.Sorted(mapKeys(files)), "-"), target)
	for name, content := range files {
		writeFile(t, repository, name, content)
	}
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-m", "resolved")
	head := gitLine(t, repository, "rev-parse", "HEAD")
	runGit(t, repository, "switch", "main")
	return head
}

func mapKeys(files map[string]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for name := range files {
			if !yield(name) {
				return
			}
		}
	}
}

func TestAResolutionThatChangesOnlyTheConflictingFileIsConfined(t *testing.T) {
	t.Parallel()

	repository, target, approved := resolutionRepository(t)
	resolved := resolvedOn(t, repository, target, map[string]string{"conflict.txt": "main's line\nthe change's line\n", "settled.txt": "added by the change\n"})
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	comparison, err := manager.CompareResolution(context.Background(), target, approved, resolved)
	if err != nil {
		t.Fatalf("CompareResolution() error = %v", err)
	}
	if !comparison.Confined() || !slices.Equal(comparison.Conflicting, []string{"conflict.txt"}) || !commitPattern.MatchString(comparison.MergedTree) {
		t.Fatalf("comparison = %+v, want confined to conflict.txt", comparison)
	}
	if !strings.Contains(comparison.Resolutions, "-=======") || !strings.Contains(comparison.Resolutions, "-<<<<<<<") || strings.Contains(comparison.Resolutions, "settled.txt") {
		t.Fatalf("resolutions = %q, want the conflicting file's markers replaced and nothing else", comparison.Resolutions)
	}
}

func TestAResolutionThatChangesAFileGitSettledIsNotConfined(t *testing.T) {
	t.Parallel()

	repository, target, approved := resolutionRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	for name, files := range map[string]map[string]string{
		"a settled file changed": {"conflict.txt": "resolved\n", "settled.txt": "changed again\n"},
		"a settled file dropped": {"conflict.txt": "resolved\n"},
		"a new file":             {"conflict.txt": "resolved\n", "settled.txt": "added by the change\n", "extra.txt": "more\n"},
	} {
		resolved := resolvedOn(t, repository, target, files)
		comparison, err := manager.CompareResolution(context.Background(), target, approved, resolved)
		if err != nil {
			t.Fatalf("%s: CompareResolution() error = %v", name, err)
		}
		if comparison.Confined() || len(comparison.Outside) != 1 || comparison.Outside[0] == "conflict.txt" {
			t.Fatalf("%s: comparison = %+v, want one file found outside the conflicting one", name, comparison)
		}
	}
}

func TestAResolutionNotBuiltOnTheTargetCannotBeCompared(t *testing.T) {
	t.Parallel()

	repository, target, approved := resolutionRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	if _, err := manager.CompareResolution(context.Background(), target, approved, approved); err == nil || !strings.Contains(err.Error(), "not built on") {
		t.Fatalf("CompareResolution() error = %v, want the resolution refused as not built on the target", err)
	}
}
