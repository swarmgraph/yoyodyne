package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// A worktree is cut from a commit, so a file that is derived from a store and
// committed on a cadence of its own arrives as old as the last commit that
// carried one rather than as old as the run. The tracker's export is the file
// this matters for: a run reads it to see the work around its own, the store it
// comes from is authoritative and kept current in the primary checkout, and the
// committed copy can be many items behind. A run that reads that copy does not
// read a stale picture so much as a confidently wrong one — an item its own work
// item names is simply absent, and nothing in the file says it was ever there.
//
// So the harness gives a new worktree the primary checkout's copy, and then
// keeps that copy out of the change the run is making. Both halves are needed.
// An export left as an ordinary working-tree edit is staged by the commit every
// attempt makes, promoted into the target with the work, and conflicts against
// every other run that refreshed the same file — one derived file would turn
// parallel development into a queue of merge conflicts. Git is told to leave the
// path alone instead, which is what keeps the refreshed copy out of status, out
// of staging, and out of every diff a reviewer is shown.
//
// That bit is where the hold is kept rather than what enforces it. It lives in
// the worktree's index under `.git`, which a developer's sandbox grants writes
// to, so a run that flipped it back would hand the harness the refreshed export
// as part of its change. The change-path gate refuses a diff containing one of
// these exports for that reason — `internal/protectedpath/export.go` — and what
// the gate reads is CurrentExports below, so the paths held and the paths
// refused are one list.

// CurrentExports names the exports this manager refreshes and holds out of a
// run's change, in the form every path here is compared in.
//
// It is read by the gate in front of the checks, which refuses a change that
// contains one of them. The hold is an index bit inside `.git`, and a developer
// sandbox grants writes there, so the paths a run may not commit have to be
// known where the change is judged rather than trusted to survive whatever ran
// in the worktree. Asking the manager that holds them is what keeps the two
// lists from being two lists.
func (m *Manager) CurrentExports() []string {
	return append([]string(nil), m.currentExports...)
}

// refreshExports gives a worktree the primary checkout's copy of each declared
// export. Which of them can be refreshed at all is decided per path below, so a
// caller only has to say when.
//
// A preparation that failed is ErrExportsUnprepared, and is safe to ask for
// again. The tracker's preparation is `bd export` to standard output, which bd
// declares a read-only command and opens its store read-only for; what it
// printed is published only once it has exited successfully and every line
// decoded as a work item (internal/beads, RefreshExport). So a preparation that
// failed changed neither the store nor the primary copy, and nothing here was
// copied from it.
func (m *Manager) refreshExports(ctx context.Context, path string) error {
	if len(m.currentExports) > 0 && m.prepareExports != nil {
		if err := m.prepareExports(ctx); err != nil {
			return fmt.Errorf("%w: prepare the primary exports: %w", ErrExportsUnprepared, err)
		}
	}
	for _, export := range m.currentExports {
		if err := m.refreshExport(ctx, path, export); err != nil {
			return fmt.Errorf("refresh %s: %w", export, err)
		}
	}
	return nil
}

// refreshExport copies one export across, and refuses to do it where the copy
// would land in the developer's change.
//
// A tracked path is held out of that change by the index and is the case this
// exists for. A path the project ignores is already outside it and needs
// nothing. A path that is neither would arrive as an untracked file the run
// never wrote and every reviewer would then be shown, which is a worse thing to
// hand somebody than an export a few items behind, so it is left alone.
func (m *Manager) refreshExport(ctx context.Context, path, export string) error {
	content, present, err := readPrimaryExport(m.repositoryRoot, export)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	tracked, err := m.tracksPath(ctx, path, export)
	if err != nil {
		return err
	}
	if tracked {
		// Said before the bytes land rather than after, so a write that fails
		// leaves a path Git is no longer comparing at exactly the content the
		// branch carries, rather than a modification nobody asked for.
		if err := m.holdOutOfChange(ctx, path, export); err != nil {
			return err
		}
	} else {
		ignored, err := m.ignoresPath(ctx, path, export)
		if err != nil {
			return err
		}
		if !ignored {
			return nil
		}
	}
	return writeExport(path, export, content)
}

// readPrimaryExport is the primary checkout's copy of one export, and whether
// there is one at all: a project whose tracker exports nothing here has nothing
// to refresh, which is not a reason to refuse it a run. The exports to refresh
// are named once for every product the harness manages, so that is the ordinary
// case for a product tracking its work some other way rather than an unusual
// one, and it must cost such a product nothing.
//
// Absence is taken as absence wherever along the path it is reported. Today it
// is the read that reports it, because resolving a path that does not exist yet
// is what lets a writer create a directory and its document in one go — but
// which of the two answers first is a property of the resolver rather than
// something this depends on. What is not absence is a component that exists and
// is not what it has to be: a project that put a file where the export's
// directory goes is refused rather than passed over.
func readPrimaryExport(repositoryRoot, export string) ([]byte, bool, error) {
	primary, err := repowrite.NewRoot(repositoryRoot)
	if err != nil {
		return nil, false, fmt.Errorf("resolve the primary checkout: %w", err)
	}
	source, err := primary.Resolve(export)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	content, err := os.ReadFile(source)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("read the primary checkout's copy: %w", err)
	}
	return content, true, nil
}

// writeExport is the one write a refresh makes, and the only place it is decided
// where the bytes land. A worktree's path comes from the harness rather than
// from a project's configuration, but an export's own path does not, and a
// symlink anywhere below it would otherwise put a file the harness says it wrote
// into the worktree somewhere nobody reviews and nobody finds again.
func writeExport(worktreePath, export string, content []byte) error {
	worktree, err := repowrite.NewRoot(worktreePath)
	if err != nil {
		return fmt.Errorf("resolve the worktree: %w", err)
	}
	if _, err := worktree.WriteFile(export, content); err != nil {
		return fmt.Errorf("write the worktree's copy: %w", err)
	}
	return nil
}

// restoreExports puts every held export back to what the worktree's branch
// carries and lets Git compare it again.
//
// It is what a replay needs. Git will not move a HEAD across a path the index
// has been told to leave alone, and a target that committed a newer export is
// exactly the case a replay exists for — the release cut that commits one is on
// a daily cadence, so without this every replay after a cut would abort on a
// file that is not part of anybody's change.
func (m *Manager) restoreExports(ctx context.Context, path string) error {
	for _, export := range m.currentExports {
		held, err := m.heldOutOfChange(ctx, path, export)
		if err != nil {
			return fmt.Errorf("restore %s: %w", export, err)
		}
		if !held {
			continue
		}
		if err := m.compareAgain(ctx, path, export); err != nil {
			return fmt.Errorf("restore %s: %w", export, err)
		}
		if err := m.restoreCommitted(ctx, path, export); err != nil {
			return fmt.Errorf("restore %s: %w", export, err)
		}
	}
	return nil
}

// tracksPath reports whether this worktree's index carries the path, which is
// what decides whether a refreshed copy is an edit to a file the branch already
// has or a file nobody asked for.
func (m *Manager) tracksPath(ctx context.Context, path, export string) (bool, error) {
	result, err := m.run(ctx, "-C", path, "ls-files", "--error-unmatch", "--", export)
	if err != nil {
		return false, err
	}
	switch {
	case result.Status == execution.ProcessSucceeded:
		return true, nil
	case result.ExitCode == 1:
		return false, nil
	default:
		return false, fmt.Errorf("ask whether %s is tracked failed with exit code %d: %s", export, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
}

// ignoresPath reports whether the project's own ignore rules already keep the
// path out of every change made in this worktree.
func (m *Manager) ignoresPath(ctx context.Context, path, export string) (bool, error) {
	result, err := m.run(ctx, "-C", path, "check-ignore", "--quiet", "--", export)
	if err != nil {
		return false, err
	}
	switch {
	case result.Status == execution.ProcessSucceeded:
		return true, nil
	case result.ExitCode == 1:
		return false, nil
	default:
		return false, fmt.Errorf("ask whether %s is ignored failed with exit code %d: %s", export, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
}

// holdOutOfChange tells this worktree's index to stop comparing the file on disk
// against what the branch carries. The bit is per worktree, so it says nothing
// about the primary checkout or about any other run.
func (m *Manager) holdOutOfChange(ctx context.Context, path, export string) error {
	result, err := m.run(ctx, "-C", path, "update-index", "--skip-worktree", "--", export)
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("hold %s out of the change failed with exit code %d: %s", export, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return nil
}

// compareAgain undoes holdOutOfChange.
func (m *Manager) compareAgain(ctx context.Context, path, export string) error {
	result, err := m.run(ctx, "-C", path, "update-index", "--no-skip-worktree", "--", export)
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("compare %s again failed with exit code %d: %s", export, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return nil
}

// heldOutOfChange reports whether this worktree is holding the path out of its
// change. Git prints one letter per file, and the lower-case letters are its
// ordinary states; `S` is the one this set.
func (m *Manager) heldOutOfChange(ctx context.Context, path, export string) (bool, error) {
	result, err := m.run(ctx, "-C", path, "ls-files", "-v", "--", export)
	if err != nil {
		return false, err
	}
	if result.Status != execution.ProcessSucceeded {
		return false, fmt.Errorf("read the index state of %s failed with exit code %d: %s", export, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.HasPrefix(line, "S ") {
			return true, nil
		}
	}
	return false, nil
}

// restoreCommitted writes the branch's own copy of the path back over the
// refreshed one. Hooks are off for the same reason every other harness Git
// command turns them off: a tracker installs one that rewrites the very files
// this is putting back.
func (m *Manager) restoreCommitted(ctx context.Context, path, export string) error {
	result, err := m.run(ctx, "-C", path, "-c", "core.hooksPath="+os.DevNull, "checkout", "HEAD", "--", export)
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("put %s back failed with exit code %d: %s", export, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return nil
}

// cleanExportPaths is where a configured export is refused for naming something
// that cannot be a file inside a repository at all, before any worktree is cut
// from it.
func cleanExportPaths(paths []string) ([]string, error) {
	cleaned := make([]string, 0, len(paths))
	for _, value := range paths {
		relative, err := repowrite.Relative(value)
		if err != nil {
			return nil, fmt.Errorf("current export %q must be a repository-relative file path: %w", value, err)
		}
		cleaned = append(cleaned, relative)
	}
	return cleaned, nil
}
