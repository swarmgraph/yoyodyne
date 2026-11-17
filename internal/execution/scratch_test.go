package execution

// Where a run's scratch files go.
//
// The whole point of the directory is that no two runs are handed the same one,
// so what is asserted here is that separation rather than the convenience: two
// runs never collide, nothing lands in the working tree the change is read from,
// and neither a run id nor a worktree's own `.git` pointer can send the write
// somewhere the repository does not contain.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAWorktreeScratchesInItsOwnAdministrativeDirectory(t *testing.T) {
	t.Parallel()

	checkout, worktree := repositoryWithWorktree(t)
	administrative := filepath.Join(checkout, ".git", "worktrees", "one")

	directory, err := PrepareScratchDirectory(checkout, worktree, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	if want := resolve(t, filepath.Join(administrative, "yoyodyne", "scratch", "run-one")); directory != want {
		t.Fatalf("PrepareScratchDirectory() = %q, want the worktree's own %q", directory, want)
	}
	// The repository's Git directory is where the build cache goes, deliberately
	// shared by every worktree. Scratch must not land there: a shared directory is
	// the thing this exists to stop.
	cache, _ := goBuildCache(worktree)
	if strings.HasPrefix(directory, filepath.Dir(cache)+string(filepath.Separator)) {
		t.Fatalf("scratch landed at %q, under the shared repository directory holding %q", directory, cache)
	}
}

// A checkout that is not a worktree has one Git directory and scratches in it,
// which is still per run: the run id is the last element either way.
func TestACheckoutScratchesInItsGitDirectory(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	makeDirectory(t, filepath.Join(checkout, ".git"))

	directory, err := PrepareScratchDirectory(checkout, checkout, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	if want := resolve(t, filepath.Join(checkout, ".git", "yoyodyne", "scratch", "run-one")); directory != want {
		t.Fatalf("PrepareScratchDirectory() = %q, want %q", directory, want)
	}
}

// The collision this exists for: two runs redirecting a check into a name they
// both picked. They cannot, because the directory they are given differs before
// either of them names anything inside it.
func TestTwoRunsAreNeverGivenTheSameDirectory(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	makeDirectory(t, filepath.Join(checkout, ".git"))

	first, err := PrepareScratchDirectory(checkout, checkout, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	second, err := PrepareScratchDirectory(checkout, checkout, "run-two")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	if first == second {
		t.Fatalf("both runs were given %q", first)
	}
}

// Nothing a run writes to its scratch directory may reach the change, so the
// directory is outside the working tree the diff is read from.
func TestScratchIsOutsideTheWorkingTree(t *testing.T) {
	t.Parallel()

	checkout, worktree := repositoryWithWorktree(t)

	directory, err := PrepareScratchDirectory(checkout, worktree, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	if strings.HasPrefix(directory, resolve(t, worktree)+string(filepath.Separator)) {
		t.Fatalf("PrepareScratchDirectory() = %q, which is inside the worktree %q", directory, worktree)
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("stat the prepared directory: %v", err)
	}
}

// A run resumed by a second process is handed the directory the first one was,
// so preparing it again has to be the same answer rather than a failure.
func TestPreparingTheSameRunTwiceIsTheSameDirectory(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	makeDirectory(t, filepath.Join(checkout, ".git"))

	first, err := PrepareScratchDirectory(checkout, checkout, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	writeFile(t, filepath.Join(first, "check.log"), "what the first process wrote\n")
	second, err := PrepareScratchDirectory(checkout, checkout, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() again error = %v", err)
	}
	if second != first {
		t.Fatalf("PrepareScratchDirectory() = %q, want the same %q", second, first)
	}
	if _, err := os.Stat(filepath.Join(first, "check.log")); err != nil {
		t.Fatalf("preparing again lost what the run had written: %v", err)
	}
}

// The run id becomes a path element, so anything that is not one is refused
// rather than cleaned up: a caller passing a path here has a bug, and joining it
// would put a run's scratch somewhere nobody named.
func TestARunIdThatIsNotAPlainNameIsRefused(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	makeDirectory(t, filepath.Join(checkout, ".git"))

	for _, id := range []string{"", "   ", ".", "..", "../elsewhere", "one/two", string(filepath.Separator) + "absolute"} {
		if directory, err := PrepareScratchDirectory(checkout, checkout, id); err == nil {
			t.Errorf("PrepareScratchDirectory(%q) = %q, want a refusal", id, directory)
		}
	}
}

// A working directory in no repository has no Git directory to hold a scratch
// directory, and neither has a repository root that is not one. Both are a
// refusal rather than a guess at somewhere writable.
func TestADirectoryInNoRepositoryHasNoScratchDirectory(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	makeDirectory(t, filepath.Join(checkout, ".git"))

	for name, test := range map[string]struct{ repository, working string }{
		"working directory in no repository": {repository: checkout, working: t.TempDir()},
		"working directory named nothing":    {repository: checkout, working: "   "},
		"repository root in no repository":   {repository: t.TempDir(), working: checkout},
		"repository root named nothing":      {repository: "   ", working: checkout},
	} {
		if directory, err := PrepareScratchDirectory(test.repository, test.working, "run-one"); err == nil {
			t.Errorf("%s: PrepareScratchDirectory() = %q, want a refusal", name, directory)
		}
	}
}

// The `.git` pointer the path is worked out from lives inside the worktree the
// run being handed the directory can write, so it is not taken on trust: a
// pointer aimed out of the repository is refused before anything is created,
// rather than becoming a directory somewhere nothing confined.
func TestAWorktreePointerAimedOutOfTheRepositoryIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	makeDirectory(t, filepath.Join(checkout, ".git"))
	elsewhere := filepath.Join(root, "elsewhere")
	makeDirectory(t, elsewhere)

	worktree := filepath.Join(root, "worktree")
	makeDirectory(t, worktree)
	writeFile(t, filepath.Join(worktree, ".git"), "gitdir: "+elsewhere+"\n")

	if directory, err := PrepareScratchDirectory(checkout, worktree, "run-one"); err == nil {
		t.Fatalf("PrepareScratchDirectory() = %q, want a refusal for a pointer out of the repository", directory)
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Fatalf("the refused write left %d entry/entries in %q (err = %v)", len(entries), elsewhere, err)
	}
}

// The same refusal one component further in: a symlink standing where the
// harness's own directory goes, pointing out of the repository's Git directory.
// The confinement primitive resolves every existing component below the root
// before it creates anything, which is what catches this.
func TestASymlinkOutOfTheGitDirectoryIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	gitDirectory := filepath.Join(checkout, ".git")
	makeDirectory(t, gitDirectory)
	elsewhere := filepath.Join(root, "elsewhere")
	makeDirectory(t, elsewhere)
	if err := os.Symlink(elsewhere, filepath.Join(gitDirectory, "yoyodyne")); err != nil {
		t.Skipf("this filesystem does not support symlinks: %v", err)
	}

	if directory, err := PrepareScratchDirectory(checkout, checkout, "run-one"); err == nil {
		t.Fatalf("PrepareScratchDirectory() = %q, want a refusal for a symlink out of the Git directory", directory)
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Fatalf("the refused write left %d entry/entries in %q (err = %v)", len(entries), elsewhere, err)
	}
}

// Nothing removes a run's scratch, because Git does: the directory it goes in
// belongs to one worktree and goes when that worktree does. This is the whole of
// why there is no cleanup step anywhere in the harness for it.
func TestGitRemovesAWorktreeScratchWithTheWorktree(t *testing.T) {
	t.Parallel()

	checkout, worktree := gitRepositoryWithWorktree(t)
	directory, err := PrepareScratchDirectory(checkout, worktree, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	writeFile(t, filepath.Join(directory, "check.log"), "what the run wrote\n")

	// Whatever the run left there is not part of the change: the working tree is
	// clean with the log already written.
	status := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	status.Dir = worktree
	dirty, err := status.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, dirty)
	}
	if strings.TrimSpace(string(dirty)) != "" {
		t.Fatalf("the scratch directory made the worktree dirty:\n%s", dirty)
	}

	remove := exec.Command("git", "worktree", "remove", worktree)
	remove.Dir = checkout
	if output, err := remove.CombinedOutput(); err != nil {
		t.Fatalf("git worktree remove: %v\n%s", err, output)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("stat the scratch directory after the worktree was removed = %v, want it gone", err)
	}
}

// repositoryWithWorktree builds the shape Git leaves behind, without needing Git:
// a checkout, one worktree's administrative directory under it, and a worktree
// whose `.git` file points at that directory.
func repositoryWithWorktree(t *testing.T) (checkout, worktree string) {
	t.Helper()

	root := t.TempDir()
	checkout = filepath.Join(root, "checkout")
	administrative := filepath.Join(checkout, ".git", "worktrees", "one")
	makeDirectory(t, administrative)
	writeFile(t, filepath.Join(administrative, "commondir"), "../..\n")

	worktree = filepath.Join(root, "worktree")
	makeDirectory(t, worktree)
	writeFile(t, filepath.Join(worktree, ".git"), "gitdir: "+administrative+"\n")
	return checkout, worktree
}

// gitRepositoryWithWorktree builds the same shape with Git itself, for the one
// assertion that is about what Git does rather than about what the harness
// derives.
func gitRepositoryWithWorktree(t *testing.T) (checkout, worktree string) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	root := t.TempDir()
	checkout = filepath.Join(root, "checkout")
	makeDirectory(t, checkout)
	run := func(directory string, args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = directory
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@invalid",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@invalid")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	run(checkout, "init", "--quiet", ".")
	run(checkout, "commit", "--quiet", "--allow-empty", "-m", "base")
	worktree = filepath.Join(root, "worktree")
	run(checkout, "worktree", "add", "--quiet", "-b", "scratch-probe", worktree, "HEAD")
	return checkout, worktree
}

// resolve is the path with its own symlinks followed, which is the form a
// confined write reports: on macOS a temporary directory is reached through one.
func resolve(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", path, err)
	}
	return resolved
}

// The git MCP server, confined to a repository, walks the repository's whole Git
// directory and refuses every call while any link there leads outside it. The
// Codex CLI leaves exactly such links in a provider home made in scratch: under
// tmp/arg0, to its own program. Those go, and with them a relative link that
// climbs out and one whose target is gone; a link to somewhere inside the Git
// directory, and every file, stay.
func TestLinksLeadingOutOfTheGitDirectoryAreClearedFromScratch(t *testing.T) {
	t.Parallel()

	checkout, worktree := repositoryWithWorktree(t)
	directory, err := PrepareScratchDirectory(checkout, worktree, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	program := filepath.Join(t.TempDir(), "codex")
	writeFile(t, program, "a program outside the repository\n")
	arg0 := filepath.Join(directory, "home", "tmp", "arg0", "codex-arg0abc")
	makeDirectory(t, arg0)
	writeFile(t, filepath.Join(directory, "check.log"), "a log\n")
	writeFile(t, filepath.Join(checkout, ".git", "HEAD"), "ref: refs/heads/main\n")

	outward := []string{
		filepath.Join(arg0, "apply_patch"),
		filepath.Join(arg0, "codex-execve-wrapper"),
		filepath.Join(directory, "climbs-out"),
		filepath.Join(directory, "gone"),
	}
	links := map[string]string{
		outward[0]:                        program,
		outward[1]:                        program,
		outward[2]:                        "../../../../../../../elsewhere",
		outward[3]:                        filepath.Join(t.TempDir(), "nothing-here"),
		filepath.Join(directory, "head"):  filepath.Join(checkout, ".git", "HEAD"),
		filepath.Join(directory, "local"): "check.log",
	}
	for link, target := range links {
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("this filesystem does not support symlinks: %v", err)
		}
	}

	removed, err := ClearOutwardScratchLinks(checkout, worktree, "run-one")
	if err != nil {
		t.Fatalf("ClearOutwardScratchLinks() error = %v", err)
	}
	if len(removed) != len(outward) {
		t.Errorf("removed %d links %v, want the %d leading out", len(removed), removed, len(outward))
	}
	for _, link := range outward {
		if _, err := os.Lstat(link); !os.IsNotExist(err) {
			t.Errorf("the link %s, leading out of the Git directory, is still there (err = %v)", link, err)
		}
	}
	for _, kept := range []string{filepath.Join(directory, "head"), filepath.Join(directory, "local"), filepath.Join(directory, "check.log"), program} {
		if _, err := os.Lstat(kept); err != nil {
			t.Errorf("%s was removed, and it leads nowhere outside: %v", kept, err)
		}
	}

	again, err := ClearOutwardScratchLinks(checkout, worktree, "run-one")
	if err != nil || len(again) != 0 {
		t.Fatalf("a second clearing removed %v (err = %v), want nothing", again, err)
	}
}

// A run whose scratch directory was never made has nothing to clear.
func TestClearingScratchThatWasNeverMadeIsNothing(t *testing.T) {
	t.Parallel()

	checkout, worktree := repositoryWithWorktree(t)
	removed, err := ClearOutwardScratchLinks(checkout, worktree, "run-one")
	if err != nil || len(removed) != 0 {
		t.Fatalf("ClearOutwardScratchLinks() = %v, %v; want nothing and no error", removed, err)
	}
}
