package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// A developer's sandbox never grants the folder a project's install steps keep
// their package cache in (docs/designs/project-dependencies-in-runs.md): the
// worktree is cut under the product's worktrees and the cache is kept among its
// records beside them, so no folder the profile lets the developer write is the
// cache or holds it. The worktree, the declared Go build cache and the run's
// scratch are what it grants by path; the machine's temporary directories it
// grants by name, and the state directory is never one of them outside a test.
func TestADevelopersSandboxCannotWriteTheInstallCache(t *testing.T) {
	t.Parallel()

	stateRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(stateRoot, filepath.FromSlash(home.InstallCacheDirectoryWithin(stateRoot, "project")))
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(stateRoot, "checkout")
	administrative := filepath.Join(repository, ".git", "worktrees", "one")
	worktree := filepath.Join(home.WorktreeDirectory(stateRoot, "project", "repository"), "run-one")
	for _, directory := range []string{administrative, worktree} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for file, body := range map[string]string{
		filepath.Join(worktree, ".git"):            "gitdir: " + administrative + "\n",
		filepath.Join(administrative, "commondir"): "../..\n",
	} {
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, session := range []string{"", "session-one"} {
		command := sandboxCommand(t, repository, worktree, session, domain.RoleDeveloper)
		granted := 0
		for entry, access := range profileFilesystem(t, command.Args) {
			if access != "write" || strings.HasPrefix(entry, ":") {
				continue
			}
			granted++
			if entry == cache || strings.HasPrefix(cache+string(filepath.Separator), strings.TrimSuffix(entry, string(filepath.Separator))+string(filepath.Separator)) {
				t.Fatalf("developer profile (session %q) lets the developer write %s, which holds the install cache %s", session, entry, cache)
			}
		}
		if granted == 0 {
			t.Fatalf("developer profile (session %q) grants no folder by path, so this shows nothing about what it refuses", session)
		}
	}
}
