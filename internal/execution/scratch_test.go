package execution

// Where a run's scratch files go.
//
// The whole point of the directory is that no two runs are handed the same one,
// so what is asserted here is that separation rather than the convenience: two
// runs never collide, nothing lands in the repository or the working tree the
// change is read from, and neither a run id nor a link standing in the state
// directory can send the write, or a removal, somewhere else.

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestARunScratchesInItsOwnFolderInTheStateDirectory(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	directory, err := PrepareScratchDirectory(state, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	if want := filepath.Join(resolve(t, state), "scratch", "run-one"); directory != want {
		t.Fatalf("PrepareScratchDirectory() = %q, want %q", directory, want)
	}
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() {
		t.Fatalf("stat the prepared directory = %v, %v; want a directory", info, err)
	}
	if err := ConfirmScratchDirectory(directory, "run-one", ""); err != nil {
		t.Fatalf("ConfirmScratchDirectory() of the prepared directory = %v", err)
	}
}

// The collision this exists for: two runs redirecting a check into a name they
// both picked. They cannot, because the directory they are given differs before
// either of them names anything inside it.
func TestTwoRunsAreNeverGivenTheSameDirectory(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	first, err := PrepareScratchDirectory(state, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	second, err := PrepareScratchDirectory(state, "run-two")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	if first == second {
		t.Fatalf("both runs were given %q", first)
	}
	if err := ConfirmScratchDirectory(second, "run-one", ""); err == nil {
		t.Fatalf("ConfirmScratchDirectory() accepted run-two's directory %q for run-one", second)
	}
}

// A run resumed by a second process is handed the directory the first one was,
// so preparing it again has to be the same answer rather than a failure.
func TestPreparingTheSameRunTwiceIsTheSameDirectory(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	first, err := PrepareScratchDirectory(state, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	writeFile(t, filepath.Join(first, "check.log"), "what the first process wrote\n")
	second, err := PrepareScratchDirectory(state, "run-one")
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

	state := t.TempDir()
	for _, id := range []string{"", "   ", ".", "..", "../elsewhere", "one/two", string(filepath.Separator) + "absolute"} {
		if directory, err := PrepareScratchDirectory(state, id); err == nil {
			t.Errorf("PrepareScratchDirectory(%q) = %q, want a refusal", id, directory)
		}
		if err := RemoveScratchDirectory(state, id); err == nil {
			t.Errorf("RemoveScratchDirectory(%q) = nil, want a refusal", id)
		}
	}
	if entries, err := os.ReadDir(state); err != nil || len(entries) != 0 {
		t.Fatalf("the refusals left %d entry/entries in the state directory (err = %v)", len(entries), err)
	}
}

// No state directory is a refusal rather than a guess at somewhere writable.
func TestNoStateDirectoryHasNoScratchDirectory(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"", "   ", filepath.Join(t.TempDir(), "missing")} {
		if directory, err := PrepareScratchDirectory(state, "run-one"); err == nil {
			t.Errorf("PrepareScratchDirectory(%q) = %q, want a refusal", state, directory)
		}
	}
}

// A `scratch` that is a link out of the state directory is refused before
// anything is created, and so is a run's own folder that is one, so the
// harness never makes a directory, or grants one, somewhere nothing confined.
func TestAScratchLinkedOutOfTheStateDirectoryIsRefused(t *testing.T) {
	t.Parallel()

	for _, linked := range []string{"scratch", "run"} {
		t.Run(linked, func(t *testing.T) {
			t.Parallel()
			state := t.TempDir()
			elsewhere := t.TempDir()
			link := filepath.Join(state, "scratch")
			if linked == "run" {
				makeDirectory(t, link)
				link = filepath.Join(link, "run-one")
			}
			if err := os.Symlink(elsewhere, link); err != nil {
				t.Skipf("this filesystem does not support symlinks: %v", err)
			}
			if directory, err := PrepareScratchDirectory(state, "run-one"); err == nil {
				t.Fatalf("PrepareScratchDirectory() = %q, want a refusal for a link out of the state directory", directory)
			}
			if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
				t.Fatalf("the refused write left %d entry/entries in %q (err = %v)", len(entries), elsewhere, err)
			}
			if err := ConfirmScratchDirectory(filepath.Join(resolve(t, state), "scratch", "run-one"), "run-one", ""); err == nil {
				t.Fatal("ConfirmScratchDirectory() accepted a scratch directory reached through a link")
			}
		})
	}
}

// Removal takes the folder and everything in it, and a link inside is removed
// as a link: what it points at, a file or a directory outside, is untouched.
func TestRemovingScratchRemovesLinksAsLinks(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	directory, err := PrepareScratchDirectory(state, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	other, err := PrepareScratchDirectory(state, "run-two")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	writeFile(t, filepath.Join(other, "check.log"), "another run's log\n")
	outside := t.TempDir()
	program := filepath.Join(outside, "codex")
	writeFile(t, program, "a program outside\n")
	keptDirectory := filepath.Join(outside, "kept")
	makeDirectory(t, keptDirectory)
	writeFile(t, filepath.Join(keptDirectory, "inside"), "kept\n")
	makeDirectory(t, filepath.Join(directory, "home", "tmp", "arg0"))
	writeFile(t, filepath.Join(directory, "check.log"), "a log\n")
	for link, target := range map[string]string{
		filepath.Join(directory, "home", "tmp", "arg0", "apply_patch"): program,
		filepath.Join(directory, "a-directory"):                        keptDirectory,
		filepath.Join(directory, "gone"):                               filepath.Join(outside, "nothing-here"),
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("this filesystem does not support symlinks: %v", err)
		}
	}

	if err := RemoveScratchDirectory(state, "run-one"); err != nil {
		t.Fatalf("RemoveScratchDirectory() error = %v", err)
	}
	if _, err := os.Lstat(directory); !os.IsNotExist(err) {
		t.Fatalf("the scratch directory is still there after removal (err = %v)", err)
	}
	for _, kept := range []string{program, filepath.Join(keptDirectory, "inside"), filepath.Join(other, "check.log")} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed with the run's scratch: %v", kept, err)
		}
	}
	if err := RemoveScratchDirectory(state, "run-one"); err != nil {
		t.Fatalf("removing a directory already gone = %v, want nothing", err)
	}
	if err := RemoveScratchDirectory(t.TempDir(), "run-one"); err != nil {
		t.Fatalf("removing from a state directory with no scratch folders = %v, want nothing", err)
	}
}

// A run's folder replaced by a link is removed as the link, never followed:
// what it points at, outside the state directory, keeps everything in it.
func TestRemovingAScratchFolderThatIsALinkRemovesOnlyTheLink(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	makeDirectory(t, filepath.Join(state, "scratch"))
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "precious"), "not the run's\n")
	link := filepath.Join(state, "scratch", "run-one")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("this filesystem does not support symlinks: %v", err)
	}
	if err := RemoveScratchDirectory(state, "run-one"); err != nil {
		t.Fatalf("RemoveScratchDirectory() error = %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("the link is still there (err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "precious")); err != nil {
		t.Fatalf("removing the link removed what it pointed at: %v", err)
	}

	// The same with `scratch` itself a link: nothing outside is touched.
	state = t.TempDir()
	if err := os.Symlink(outside, filepath.Join(state, "scratch")); err != nil {
		t.Fatal(err)
	}
	makeDirectory(t, filepath.Join(outside, "run-one"))
	_ = RemoveScratchDirectory(state, "run-one")
	if _, err := os.Stat(filepath.Join(outside, "run-one")); err != nil {
		t.Fatalf("removal followed a scratch folder that is a link out of the state directory: %v", err)
	}
}

// What ConfirmScratchDirectory refuses, each of which would be a grant wider
// than, or other than, the one folder cut for the run.
func TestConfirmScratchDirectoryRefusesAnythingButTheRunsOwnFolder(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	directory, err := PrepareScratchDirectory(state, "run-one")
	if err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	makeDirectory(t, filepath.Join(checkout, ".git"))
	insideGit := filepath.Join(resolve(t, checkout), ".git", "scratch", "run-one")
	makeDirectory(t, insideGit)
	notADirectory := filepath.Join(resolve(t, state), "scratch", "run-file")
	writeFile(t, notADirectory, "x")
	for name, test := range map[string]struct{ directory, run, repository string }{
		"the state directory itself":   {resolve(t, state), "run-one", ""},
		"the scratch folders":          {filepath.Dir(directory), "run-one", ""},
		"another run's folder":         {directory, "run-two", ""},
		"a relative path":              {"scratch/run-one", "run-one", ""},
		"an unclean path":              {directory + "/.", "run-one", ""},
		"a folder that does not exist": {filepath.Join(filepath.Dir(directory), "run-three"), "run-three", ""},
		"a file":                       {notADirectory, "run-file", ""},
		"inside the Git directory":     {insideGit, "run-one", checkout},
		"a run id that is a path":      {directory, "../run-one", ""},
	} {
		if err := ConfirmScratchDirectory(test.directory, test.run, test.repository); err == nil {
			t.Errorf("%s: ConfirmScratchDirectory(%q) = nil, want a refusal", name, test.directory)
		}
	}
}

// What the move is for (docs/decisions/developer-run-scratch-outside-git.md):
// a link a run leaves in its scratch folder is nowhere under the repository,
// and so nowhere a git server confined to it checks. Git agrees the worktree is
// clean with it there, and nothing under the checkout, its Git directory
// included, is a link.
func TestALinkLeftInScratchIsNowhereUnderTheRepository(t *testing.T) {
	t.Parallel()

	checkout, worktree := gitRepositoryWithWorktree(t)
	state := t.TempDir()
	directory, err := PrepareScratchDirectory(state, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	outside := filepath.Join(t.TempDir(), "codex")
	writeFile(t, outside, "a program outside the repository\n")
	if err := os.Symlink(outside, filepath.Join(directory, "codex-arg0")); err != nil {
		t.Skipf("this filesystem does not support symlinks: %v", err)
	}
	for _, root := range []string{checkout, worktree} {
		if strings.HasPrefix(directory, resolve(t, root)+string(filepath.Separator)) {
			t.Fatalf("scratch %q is under %q", directory, root)
		}
		if links := linksUnder(t, root); len(links) != 0 {
			t.Fatalf("links under %s: %v, want none", root, links)
		}
	}
	status := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	status.Dir = worktree
	dirty, err := status.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, dirty)
	}
	if strings.TrimSpace(string(dirty)) != "" {
		t.Fatalf("the scratch directory made the worktree dirty:\n%s", dirty)
	}
}

func linksUnder(t *testing.T, root string) []string {
	t.Helper()
	var links []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			links = append(links, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return links
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
	// Where a run's scratch was cut before it moved to the state directory,
	// which is what this clearing still reaches.
	directory := filepath.Join(checkout, ".git", "worktrees", "one", "yoyodyne", "scratch", "run-one")
	makeDirectory(t, directory)
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
