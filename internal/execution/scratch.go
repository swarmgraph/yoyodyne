package execution

import (
	"fmt"
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
// Where it goes is decided by the same two constraints as the build cache beside
// it. It is outside the working tree, so nothing written there can enter the
// change or leave the tree dirty; and it is inside the Git directory, where the
// provider can admit it explicitly. Codex grants this run's directory rather
// than all of Git's state, beside the worktree and temporary directory. The
// harness's own state tree is none of those: a directory cut there is one the
// run is refused at its first write, which is why this is not where a reader
// might expect the harness to keep it.
//
// The worktree's own administrative directory rather than the repository's is
// the other half. Git creates it for that worktree and removes it with that
// worktree, so a run's scratch goes when its worktree goes and nothing has to
// remember to delete it.
const scratchDirectoryName = "scratch"

// PrepareScratchDirectory creates the directory the run named by runID may write
// while it works in workingDirectory, and reports where it is. It is the only
// answer to where a run's scratch lives: making the directory and naming it are
// one step, so nothing reads a path that disagrees with what was made.
//
// The run id is the last element even though the directory above it already
// belongs to one worktree. It costs nothing, it says which run a directory
// belongs to wherever somebody finds it, and it is what makes two runs' scratch
// distinct even where they were somehow given one Git directory to share.
//
// It creates, where the build cache beside it only names a path and lets the Go
// command make it. The difference is who is told: a tool asked for a cache makes
// its own, and an agent handed a directory in its contract has to find one there
// -- a contract naming a path that does not exist is a contract inviting the run
// to go and pick somewhere else.
//
// The write goes through the confinement primitive with a root it declares,
// because every harness-owned write does and this one has the hazard that rule
// was written for. Where the directory goes is worked out from the `.git`
// pointer inside the run's own worktree, which is a file the run being handed
// the directory can write: a replaced pointer or a symlink planted along the way
// is a path chosen by the thing the harness is cutting the directory for.
//
// So neither end of it is taken on trust. The declared root is the repository's
// own Git directory, derived from the repository root the harness was configured
// with rather than from anything inside a worktree, and the worktree's
// administrative directory is named as a path relative to that root -- so a
// pointer aimed anywhere else is a relative path that climbs out of the root and
// is refused before anything is created, and every existing component below the
// root is resolved against the filesystem on the way down.
func PrepareScratchDirectory(repositoryRoot, workingDirectory, runID string) (string, error) {
	root, relative, err := scratchLocation(repositoryRoot, workingDirectory, runID)
	if err != nil {
		return "", err
	}
	created, err := prepareDirectory(root, relative)
	if err != nil {
		return "", fmt.Errorf("create the scratch directory for run %s: %w", runID, err)
	}
	return created, nil
}

// PrepareDeveloperDirectories names only the shared build cache and this run's
// scratch directory. Resolving them through the same root as scratch creation
// refuses a worktree pointer or symlink that would grant access outside the
// repository's Git directory. Both directories are created through the confined
// writer so native sandbox policies can admit them on the first run of a repository.
func PrepareDeveloperDirectories(repositoryRoot, workingDirectory, runID string) ([]string, error) {
	if _, found := repositoryGitDirectory(repositoryRoot); !found {
		return nil, nil
	}
	root, relative, err := scratchLocation(repositoryRoot, workingDirectory, runID)
	if err != nil {
		return nil, err
	}
	cache, err := root.Resolve(path.Join(harnessGitSubdirectory, "go-build"))
	if err != nil {
		return nil, fmt.Errorf("confine the build cache for run %s: %w", runID, err)
	}
	scratch, err := root.Resolve(relative)
	if err != nil {
		return nil, fmt.Errorf("confine the scratch directory for run %s: %w", runID, err)
	}
	// A link to another directory inside .git is still a wider grant than the
	// declared cache or scratch. In particular, a cache linked to .git itself
	// must not make all Git state writable.
	if cache != filepath.Join(root.Path(), harnessGitSubdirectory, "go-build") || scratch != filepath.Join(root.Path(), filepath.FromSlash(relative)) {
		return nil, fmt.Errorf("developer cache or scratch directory for run %s is redirected by a symlink", runID)
	}
	cache, err = prepareDirectory(root, path.Join(harnessGitSubdirectory, "go-build"))
	if err != nil {
		return nil, fmt.Errorf("prepare the build cache for run %s: %w", runID, err)
	}
	scratch, err = prepareDirectory(root, relative)
	if err != nil {
		return nil, fmt.Errorf("prepare the scratch directory for run %s: %w", runID, err)
	}
	if cache != filepath.Join(root.Path(), harnessGitSubdirectory, "go-build") || scratch != filepath.Join(root.Path(), filepath.FromSlash(relative)) {
		return nil, fmt.Errorf("developer cache or scratch directory for run %s is redirected by a symlink", runID)
	}
	return []string{cache, scratch}, nil
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

func scratchLocation(repositoryRoot, workingDirectory, runID string) (repowrite.Root, string, error) {
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
