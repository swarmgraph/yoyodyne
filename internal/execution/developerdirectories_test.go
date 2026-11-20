package execution

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeveloperDirectoriesAreTheBuildCacheAndTheRunsOwnScratch(t *testing.T) {
	t.Parallel()
	checkout, worktree := repositoryWithWorktree(t)
	scratch, err := PrepareScratchDirectory(t.TempDir(), "run-one")
	if err != nil {
		t.Fatal(err)
	}
	directories, err := PrepareDeveloperDirectories(checkout, worktree, "run-one", scratch)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(resolve(t, filepath.Join(checkout, ".git")), "yoyodyne", "go-build")
	if len(directories) != 2 || directories[0] != cache || directories[1] != scratch {
		t.Fatalf("PrepareDeveloperDirectories() = %q, want cache %q and scratch %q", directories, cache, scratch)
	}
	// Native policies must be able to resolve both roots on their first launch.
	for _, directory := range directories {
		if info, err := os.Stat(directory); err != nil || !info.IsDir() {
			t.Fatalf("stat developer directory %q = %v, %v", directory, info, err)
		}
	}
	// No scratch folder in the Git directory: nothing is cut there any more.
	if _, err := os.Lstat(filepath.Join(checkout, ".git", "worktrees", "one", "yoyodyne", "scratch")); !os.IsNotExist(err) {
		t.Fatalf("a scratch folder was made inside the Git directory (err = %v)", err)
	}
	// A replaced commondir must not move the cache away from the harness's
	// checkout, including on a later native resume.
	administrative, _ := WorktreeGitDirectory(worktree)
	writeFile(t, filepath.Join(administrative, "commondir"), t.TempDir())
	resumed, err := PrepareDeveloperDirectories(checkout, worktree, "run-one", scratch)
	if err != nil || len(resumed) != 2 || resumed[0] != directories[0] || resumed[1] != scratch {
		t.Fatalf("resumed directories = %q, %v, want the original grants", resumed, err)
	}
	// No scratch named is no scratch granted.
	if only, err := PrepareDeveloperDirectories(checkout, worktree, "run-one", ""); err != nil || len(only) != 1 || only[0] != cache {
		t.Fatalf("PrepareDeveloperDirectories() with no scratch = %q, %v, want the cache alone", only, err)
	}
}

func TestDeveloperDirectoriesRefuseEscapingPointersAndRedirectedGrants(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"git pointer", "cache outside", "cache inside", "dangling cache", "run id", "another run's scratch", "the state directory"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			checkout, worktree := repositoryWithWorktree(t)
			state := t.TempDir()
			scratch, err := PrepareScratchDirectory(state, "run-one")
			if err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			run := "run-one"
			cache := filepath.Join(checkout, ".git", "yoyodyne", "go-build")
			switch change {
			case "git pointer":
				writeFile(t, filepath.Join(worktree, ".git"), "gitdir: "+outside)
			case "cache outside", "cache inside", "dangling cache":
				makeDirectory(t, filepath.Dir(cache))
				target := outside
				if change == "cache inside" {
					target = filepath.Join(checkout, ".git")
				} else if change == "dangling cache" {
					target = filepath.Join(outside, "missing")
				}
				if err := os.Symlink(target, cache); err != nil {
					t.Fatal(err)
				}
			case "run id":
				run = "../another-run"
			case "another run's scratch":
				run = "run-two"
			case "the state directory":
				scratch = resolve(t, state)
			}
			if paths, err := PrepareDeveloperDirectories(checkout, worktree, run, scratch); err == nil || paths != nil {
				t.Fatalf("PrepareDeveloperDirectories() = %q, %v, want a refusal for %s", paths, err, change)
			}
		})
	}
}

func TestDeveloperDirectoriesGrantNothingOutsideARepository(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	paths, err := PrepareDeveloperDirectories(directory, directory, "run-one", "")
	if err != nil || len(paths) != 0 {
		t.Fatalf("PrepareDeveloperDirectories() = %q, %v, want no additional roots", paths, err)
	}
}
