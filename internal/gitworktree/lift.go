package gitworktree

// Starting a fresh run from a change an earlier run of the same item left on
// its branch.
//
// A worktree is always cut from exactly the branch it will be promoted into,
// because integration insists on that equality later. So a change an earlier run
// preserved cannot be the fresh worktree's base; it is lifted into it instead,
// as the working tree's uncommitted content, before the developer is invoked.
// The fresh run's change is then whatever the developer leaves on top of it, and
// every reader of that change — the checks, the reviewer, the promotion — reads
// it base-relative against the target exactly as it reads any other run's.
//
// What is lifted is what the branch carries past where it and the target last
// agreed, so a target that has moved since the earlier run is merged with rather
// than written over. A change that no longer merges cleanly is not half-applied:
// the worktree is put back as it was cut and the conflict is the error, because a
// developer handed conflict markers it did not write would be judged on them.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// ErrNothingToLift is what a lift of a branch that carries nothing past the
// target reports. It is not a failure of the fresh run: the change it would have
// started from is already on the target, or was never committed to the branch.
var ErrNothingToLift = errors.New("the preserved branch carries nothing past the target branch")

// ErrLiftBranchGone is what a lift of a branch the repository no longer has
// reports. The worktree was not touched.
var ErrLiftBranchGone = errors.New("the preserved branch is not there to lift from")

// ErrLiftDoesNotApply is what a lift of a change that no longer merges cleanly
// with the target reports, once the worktree has been put back as it was cut. A
// lift that could not put the worktree back does not report it, because that
// worktree is no longer one a run can start from.
var ErrLiftDoesNotApply = errors.New("the preserved change does not apply cleanly to the target branch")

// Lift is what one lift put into a fresh worktree: the branch it came from and
// the commit that branch stood at.
type Lift struct {
	Branch string `json:"branch"`
	Commit string `json:"commit"`
}

// LiftChange applies what branch carries past the worktree's base into the
// worktree, uncommitted, and reports the commit it lifted.
//
// It is asked of a worktree nothing has written to yet, and refuses one that is
// not: a lift over a developer's work would mix two changes nobody could tell
// apart afterwards. The branch is read from the repository rather than from any
// record, so a branch somebody removed is refused by name rather than lifted as
// nothing.
func (m *Manager) LiftChange(ctx context.Context, worktree Worktree, branch string) (Lift, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return Lift{}, errors.New("a lift names the branch it lifts from")
	}
	if branch == worktree.Branch {
		return Lift{}, fmt.Errorf("branch %s is the worktree's own branch, so there is nothing to lift into it", branch)
	}
	path, head, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return Lift{}, err
	}
	if head != worktree.BaseCommit {
		return Lift{}, fmt.Errorf("worktree %s already carries commit %s past its base, so a lift into it would mix two changes", path, head)
	}
	dirty, err := m.isDirty(ctx, path)
	if err != nil {
		return Lift{}, err
	}
	if dirty {
		return Lift{}, fmt.Errorf("worktree %s already holds uncommitted work, so a lift into it would mix two changes", path)
	}
	commit, err := m.resolveBranchCommit(ctx, branch)
	if err != nil {
		return Lift{}, fmt.Errorf("%w: %s: %w", ErrLiftBranchGone, branch, err)
	}
	ahead, err := m.run(ctx, "-C", m.repositoryRoot, "rev-list", "--count", worktree.BaseCommit+".."+commit)
	if err != nil {
		return Lift{}, err
	}
	if ahead.Status != execution.ProcessSucceeded {
		return Lift{}, fmt.Errorf("count what %s carries past %s failed with exit code %d: %s", branch, worktree.BaseCommit, ahead.ExitCode, strings.TrimSpace(ahead.Stderr))
	}
	if strings.TrimSpace(ahead.Stdout) == "0" {
		return Lift{Branch: branch, Commit: commit}, fmt.Errorf("%w: %s stands at %s", ErrNothingToLift, branch, commit)
	}
	merged, err := m.run(ctx, "-C", path, "merge", "--squash", "--no-verify", commit)
	if err != nil {
		return Lift{}, err
	}
	if merged.Status != execution.ProcessSucceeded {
		detail := fmt.Sprintf("the change on %s at %s does not apply cleanly to %s (exit code %d): %s",
			branch, commit, worktree.BaseCommit, merged.ExitCode, strings.TrimSpace(merged.Stdout+"\n"+merged.Stderr))
		if restoreErr := m.restoreLift(ctx, path); restoreErr != nil {
			return Lift{}, errors.Join(errors.New(detail), fmt.Errorf("put the worktree back as it was cut: %w", restoreErr))
		}
		return Lift{}, fmt.Errorf("%w: %s", ErrLiftDoesNotApply, detail)
	}
	return Lift{Branch: branch, Commit: commit}, nil
}

// restoreLift puts a worktree a lift could not complete back to its base, which
// is what it was a moment before: nothing but the lift had written to it.
func (m *Manager) restoreLift(ctx context.Context, path string) error {
	result, err := m.run(ctx, "-C", path, "reset", "--hard", "--quiet", "HEAD")
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("reset failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return nil
}
