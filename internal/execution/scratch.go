package execution

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// A run writes files its work needs and its change must not carry: the log it
// redirects a check into, a listing it means to diff against a second listing.
// Neither obvious place is one it can use. Inside the worktree is untracked
// content in the change, which every reviewer is then shown; and the machine's
// temporary directory is one directory per user rather than per run -- Claude
// Code sets TMPDIR itself in sandbox mode, so a TMPDIR the harness passed in
// would be replaced before the run's first command -- which means every run
// working beside this one is handed the same directory and an obvious name for a
// log is obvious to all of them.
//
// That second one has already cost a round. On 2026-09-01 two runs five seconds
// apart redirected `make check` into $TMPDIR/probe-check.log; one of them read
// the other's compile error back out of it and reported a broken toolchain while
// its own check was exiting 0.
// `docs/diagnoses/yoyodyne-ifd-238-probe-verdict-crosstalk.md` is the whole of
// it. What stood against that afterwards was prose asking every run to put the
// id of its work item into every scratch name, which holds exactly as far as
// each run's reading of it.
//
// So the harness cuts each run its own directory and names that directory in the
// developer contract. Two runs cannot read each other's scratch output because
// they are never handed the same directory, which is a fact about the path
// rather than about whether a run followed a convention.
//
// Where it goes is the harness's state directory, at scratch/<run id>/: outside
// the working tree, so nothing written there can enter the change or leave the
// tree dirty, and outside the repository and its Git directory altogether.
// docs/decisions/developer-run-scratch-outside-git.md is the ruling. A scratch
// folder used to be cut inside the Git directory, which every worktree of the
// repository shares, and a link a run left there made a declared git server,
// which checks the whole of that directory before it answers, refuse every
// call on the repository until somebody removed it. Under the state directory
// nothing a run leaves is anywhere that check reads.
//
// The state directory holds the run records and every other store the harness
// keeps, so the provider's sandbox is given this one folder and none of the
// rest of it (scratch/<run id>/ is the whole grant; see ConfirmScratchDirectory
// and the provider adapters that call it).
const scratchDirectoryName = "scratch"

// PrepareScratchDirectory creates the directory the run named by runID may write
// while it works, under the harness's state directory stateRoot, and reports
// where it is, with its links resolved. It is the only answer to where a run's
// scratch lives: making the directory and naming it are one step, so nothing
// reads a path that disagrees with what was made.
//
// It creates, where the build cache only names a path and lets the Go command
// make it, because an agent handed a directory in its contract has to find one
// there -- a contract naming a path that does not exist is a contract inviting
// the run to go and pick somewhere else.
//
// The write goes through the confinement primitive with the state directory as
// its declared root, as every harness-owned write does: a `scratch` that is a
// link out of the state directory is refused before anything is created.
// Preparing the same run again is the same directory with what the run wrote
// still in it, because a run resumed by a second process is handed the
// directory the first one was.
func PrepareScratchDirectory(stateRoot, runID string) (string, error) {
	root, relative, err := scratchUnder(stateRoot, runID)
	if err != nil {
		return "", err
	}
	created, err := prepareDirectory(root, relative)
	if err != nil {
		return "", fmt.Errorf("create the scratch directory for run %s: %w", runID, err)
	}
	if want := filepath.Join(root.Path(), filepath.FromSlash(relative)); created != want {
		return "", fmt.Errorf("the scratch directory for run %s is redirected by a symlink to %s", runID, created)
	}
	return created, nil
}

// RemoveScratchDirectory removes the scratch directory of the run named by
// runID, and everything in it, from under stateRoot. A directory already gone
// is a removal already made.
//
// It removes through held directories and never follows a link: a link the run
// left inside its folder is removed as a link and what it points at is left
// alone, and a `scratch` directory, or the run's own folder, that has been
// replaced by a link is removed as that link or refused, never followed out of
// the state directory.
func RemoveScratchDirectory(stateRoot, runID string) error {
	root, _, err := scratchUnder(stateRoot, runID)
	if err != nil {
		return err
	}
	id, _ := scratchRunID(runID)
	state, err := repowrite.OpenPinnedRoot(root.Path())
	if err != nil {
		return fmt.Errorf("open the state directory to remove run %s's scratch: %w", runID, err)
	}
	defer state.Close()
	scratch, err := state.OpenDirectory(scratchDirectoryName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open the scratch folders to remove run %s's: %w", runID, err)
	}
	defer scratch.Close()
	if err := scratch.RemoveAll(id); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the scratch directory of run %s: %w", runID, err)
	}
	return nil
}

// ConfirmScratchDirectory checks that directory is a scratch directory
// PrepareScratchDirectory cut for the run named by runID, before a provider's
// sandbox is given write access to it: an absolute path with no link anywhere
// along it, ending in scratch/<run id>, that is a directory and not a link, and
// that is neither inside the repository nor inside the repository's Git
// directory. A provider adapter is handed the path on the request rather than
// the state directory, so this is how it knows the grant it is about to make is
// the one folder and not a wider one.
func ConfirmScratchDirectory(directory, runID, repositoryRoot string) error {
	id, named := scratchRunID(runID)
	if !named {
		return fmt.Errorf("run id %q is not a name a scratch directory can be cut for", runID)
	}
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return fmt.Errorf("the scratch directory %q for run %s is not an absolute, clean path", directory, runID)
	}
	if filepath.Base(directory) != id || filepath.Base(filepath.Dir(directory)) != scratchDirectoryName {
		return fmt.Errorf("the scratch directory %q is not scratch/%s under the state directory", directory, id)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect the scratch directory for run %s: %w", runID, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("the scratch directory %q for run %s is not a directory", directory, runID)
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("resolve the scratch directory for run %s: %w", runID, err)
	}
	if resolved != directory {
		return fmt.Errorf("the scratch directory %q for run %s is reached through a symlink to %q", directory, runID, resolved)
	}
	for _, outside := range []string{repositoryRoot, gitDirectoryOf(repositoryRoot)} {
		if strings.TrimSpace(outside) == "" {
			continue
		}
		boundary, err := filepath.EvalSymlinks(outside)
		if err != nil {
			continue
		}
		if relative, err := filepath.Rel(boundary, resolved); err == nil && relative != ".." && !strings.HasPrefix(filepath.ToSlash(relative), "../") {
			return fmt.Errorf("the scratch directory %q for run %s is inside the repository at %q", directory, runID, boundary)
		}
	}
	return nil
}

func gitDirectoryOf(repositoryRoot string) string {
	directory, _ := repositoryGitDirectory(repositoryRoot)
	return directory
}

// scratchUnder is the state directory as a confinement root and the run's
// scratch folder as a path relative to it.
func scratchUnder(stateRoot, runID string) (repowrite.Root, string, error) {
	id, named := scratchRunID(runID)
	if !named {
		return repowrite.Root{}, "", fmt.Errorf("run id %q is not a name a scratch directory can be cut for", runID)
	}
	if strings.TrimSpace(stateRoot) == "" {
		return repowrite.Root{}, "", fmt.Errorf("no state directory was given to cut run %s a scratch directory in", runID)
	}
	root, err := repowrite.NewRoot(stateRoot)
	if err != nil {
		return repowrite.Root{}, "", fmt.Errorf("confine the scratch directory for run %s to the state directory: %w", runID, err)
	}
	return root, path.Join(scratchDirectoryName, id), nil
}

// PrepareDeveloperDirectories names the shared build cache, inside the
// repository's Git directory, and, where scratch is not empty, the run's own
// scratch directory, as the folders beside its worktree a developer's sandbox
// may write. Resolving the cache through the repository's Git directory refuses
// a worktree pointer or symlink that would grant access outside it, and the
// scratch directory is confirmed to be the one folder cut for this run
// (ConfirmScratchDirectory). The cache is created through the confined writer so
// native sandbox policies can admit it on the first run of a repository.
func PrepareDeveloperDirectories(repositoryRoot, workingDirectory, runID, scratch string) ([]string, error) {
	var directories []string
	if _, found := repositoryGitDirectory(repositoryRoot); found {
		cache, err := prepareBuildCache(repositoryRoot, workingDirectory, runID)
		if err != nil {
			return nil, err
		}
		directories = append(directories, cache)
	}
	if scratch != "" {
		if err := ConfirmScratchDirectory(scratch, runID, repositoryRoot); err != nil {
			return nil, err
		}
		directories = append(directories, scratch)
	}
	return directories, nil
}

func prepareBuildCache(repositoryRoot, workingDirectory, runID string) (string, error) {
	if _, named := scratchRunID(runID); !named {
		return "", fmt.Errorf("run id %q is not a name a developer directory can be granted for", runID)
	}
	root, _, err := worktreeLocation(repositoryRoot, workingDirectory, runID)
	if err != nil {
		return "", err
	}
	relative := path.Join(harnessGitSubdirectory, "go-build")
	want := filepath.Join(root.Path(), harnessGitSubdirectory, "go-build")
	// A link to another directory inside .git is still a wider grant than the
	// declared cache. In particular, a cache linked to .git itself must not make
	// all Git state writable.
	cache, err := root.Resolve(relative)
	if err != nil {
		return "", fmt.Errorf("confine the build cache for run %s: %w", runID, err)
	}
	if cache != want {
		return "", fmt.Errorf("the build cache for run %s is redirected by a symlink", runID)
	}
	cache, err = prepareDirectory(root, relative)
	if err != nil {
		return "", fmt.Errorf("prepare the build cache for run %s: %w", runID, err)
	}
	if cache != want {
		return "", fmt.Errorf("the build cache for run %s is redirected by a symlink", runID)
	}
	return cache, nil
}

// ClearOutwardScratchLinks removes from the scratch directory the run named by
// runID was given inside the repository's Git directory, before scratch moved to
// the state directory, every symbolic link that leads outside the Git
// directory, and returns the paths it removed. A scratch directory that was
// never made there, which is every run's since the move, has nothing to clear.
// It goes with the maintenance pass that clears what earlier runs left under
// `.git` (docs/decisions/developer-run-scratch-outside-git.md, Consequences).
//
// The scratch directory sits inside the Git directory, and a tool confined to
// the repository can refuse it whole for one link that leads out. The git MCP
// server does: given `--repository`, it walks every directory of the
// repository's Git directory, every worktree's scratch included, and refuses
// every call while any link there leads outside the repository. A run writes
// such links without meaning to, the Codex CLI's `tmp/arg0` links to its own
// program under a provider home made in scratch among them, and nothing else
// removes them while the worktree is kept. So the harness clears them at the
// end of each developer turn, before any call that turn asked for is made. A
// link inside the Git directory, and every file, is left as it is.
//
// Each removal goes through a held directory and is made only while the entry
// is still a link, so nothing outside the scratch directory and nothing that
// is not a link is ever removed.
func ClearOutwardScratchLinks(repositoryRoot, workingDirectory, runID string) ([]string, error) {
	root, relative, err := worktreeLocation(repositoryRoot, workingDirectory, runID)
	if err != nil {
		return nil, err
	}
	boundary, err := filepath.EvalSymlinks(root.Path())
	if err != nil {
		return nil, fmt.Errorf("resolve the Git directory for run %s: %w", runID, err)
	}
	gitDirectory, err := repowrite.OpenPinnedRoot(root.Path())
	if err != nil {
		return nil, fmt.Errorf("open the Git directory for run %s: %w", runID, err)
	}
	defer gitDirectory.Close()
	scratch, err := gitDirectory.OpenDirectory(relative)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open the scratch directory for run %s: %w", runID, err)
	}
	defer scratch.Close()
	var removed []string
	err = clearOutwardLinks(scratch, boundary, &removed)
	return removed, err
}

func clearOutwardLinks(directory *repowrite.PinnedRoot, boundary string, removed *[]string) error {
	entries, err := directory.ReadDirectory(".")
	if err != nil {
		return fmt.Errorf("read %s: %w", directory.Path(), err)
	}
	var problems []error
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			if !leadsOutside(filepath.Join(directory.Path(), name), boundary) {
				continue
			}
			info, err := directory.Lstat(name)
			if err != nil || info.Mode()&fs.ModeSymlink == 0 {
				continue
			}
			if err := directory.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				problems = append(problems, fmt.Errorf("remove the link %s: %w", filepath.Join(directory.Path(), name), err))
				continue
			}
			*removed = append(*removed, filepath.Join(directory.Path(), name))
		case entry.IsDir():
			child, err := directory.OpenDirectory(name)
			if err != nil {
				problems = append(problems, fmt.Errorf("open %s: %w", filepath.Join(directory.Path(), name), err))
				continue
			}
			problems = append(problems, clearOutwardLinks(child, boundary, removed))
			child.Close()
		}
	}
	return errors.Join(problems...)
}

// leadsOutside reports whether the link at path resolves outside boundary. A
// link whose target does not exist is judged by where it names, as the git
// server judges it.
func leadsOutside(path, boundary string) bool {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		named, readErr := os.Readlink(path)
		if readErr != nil {
			return false
		}
		if !filepath.IsAbs(named) {
			named = filepath.Join(filepath.Dir(path), named)
		}
		target = filepath.Clean(named)
	}
	relative, err := filepath.Rel(boundary, target)
	if err != nil {
		return true
	}
	slashed := filepath.ToSlash(relative)
	return slashed == ".." || strings.HasPrefix(slashed, "../")
}

// Directory creation holds the root's handle across the write. Resolve alone
// cannot stop a path replaced after validation from redirecting a mkdir.
func prepareDirectory(root repowrite.Root, relative string) (string, error) {
	if _, err := root.Resolve(relative); err != nil {
		return "", err
	}
	pinned, err := repowrite.OpenPinnedRoot(root.Path())
	if err != nil {
		return "", err
	}
	defer pinned.Close()
	if err := pinned.MakeDirectory(relative, 0o700); err != nil {
		return "", err
	}
	if err := pinned.Unchanged(); err != nil {
		return "", err
	}
	return root.Resolve(relative)
}

// worktreeLocation is the repository's Git directory as a confinement root and,
// relative to it, where the worktree's scratch directory was cut before scratch
// moved to the state directory. Where the worktree's administrative directory
// is is worked out from the `.git` pointer inside the worktree, which the run
// can write, so a pointer aimed outside the repository's Git directory is
// refused here rather than followed.
func worktreeLocation(repositoryRoot, workingDirectory, runID string) (repowrite.Root, string, error) {
	id, named := scratchRunID(runID)
	if !named {
		return repowrite.Root{}, "", fmt.Errorf("run id %q is not a name a scratch directory can be cut for", runID)
	}
	repositoryGit, found := repositoryGitDirectory(repositoryRoot)
	if !found {
		return repowrite.Root{}, "", fmt.Errorf("no Git directory holds the repository %q", repositoryRoot)
	}
	root, err := repowrite.NewRoot(repositoryGit)
	if err != nil {
		return repowrite.Root{}, "", fmt.Errorf("confine the scratch directory for run %s: %w", runID, err)
	}
	worktreeGit, found := WorktreeGitDirectory(workingDirectory)
	if !found {
		return repowrite.Root{}, "", fmt.Errorf("no Git directory holds a scratch directory for run %q working in %q", runID, workingDirectory)
	}
	inside, err := gitDirectoryWithin(root.Path(), worktreeGit)
	if err != nil {
		return repowrite.Root{}, "", fmt.Errorf("place the scratch directory for run %s: %w", runID, err)
	}
	return root, path.Join(inside, harnessGitSubdirectory, scratchDirectoryName, id), nil
}

// gitDirectoryWithin names one worktree's administrative directory as a path
// relative to the repository's own Git directory, which is what the confinement
// below is decided on.
//
// Both sides are resolved through their own symlinks first, because the
// comparison is between two paths on this filesystem rather than between two
// strings: on macOS the same directory is spelled `/var/…` and `/private/var/…`
// depending on which of the two something read it from, and a comparison that
// took either at face value would refuse a legitimate worktree.
//
// A worktree whose administrative directory is the repository's own is a plain
// checkout rather than a worktree, and its scratch goes straight under the root.
func gitDirectoryWithin(repositoryGit, worktreeGit string) (string, error) {
	resolved, err := filepath.EvalSymlinks(worktreeGit)
	if err != nil {
		return "", fmt.Errorf("resolve the Git directory %q: %w", worktreeGit, err)
	}
	relative, err := filepath.Rel(repositoryGit, resolved)
	if err != nil {
		return "", fmt.Errorf("place %q inside %q: %w", resolved, repositoryGit, err)
	}
	slashed := filepath.ToSlash(relative)
	if slashed == ".." || strings.HasPrefix(slashed, "../") {
		return "", fmt.Errorf("the Git directory %q is not inside the repository's own %q", resolved, repositoryGit)
	}
	if slashed == "." {
		return "", nil
	}
	return slashed, nil
}

// scratchRunID is the run id as the one path element it becomes. Anything that
// could climb out of the directory below is refused rather than cleaned up: a
// run id that is not a plain name is a caller's mistake and not a path.
func scratchRunID(runID string) (string, bool) {
	id := strings.TrimSpace(runID)
	if id == "" || id != filepath.Base(id) || id == "." || id == ".." {
		return "", false
	}
	return id, true
}
