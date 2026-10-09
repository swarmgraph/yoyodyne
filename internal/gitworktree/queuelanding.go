package gitworktree

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The Git half of landing a merge queue candidate: moving the local target
// onto it, and publishing it on a branch of its own for a pull request to
// carry. Both are compare-and-swaps on a commit the caller names, and neither
// ever forces anything: a target that is not where the candidate was built
// loses the race rather than being overwritten, and a candidate branch that is
// not where the queue last left it is refused rather than replaced.

// queueCandidateBranchPrefix is runstate.MergeQueueCandidateBranchPrefix,
// repeated here because this package does not import the run state.
const queueCandidateBranchPrefix = "yoyodyne/merge-queue/"

// PromoteQueueCandidate moves the local target from base to candidate and
// nothing else: the target must stand at base, and the candidate must descend
// from it. A target that has moved is ErrTargetDrift, and losing the race
// between the read and the move is ErrNotFastForward; either leaves the
// target exactly where it was. The primary checkout is held to what a run's
// promotion holds it to when the target is checked out there.
func (m *Manager) PromoteQueueCandidate(ctx context.Context, branch, base, candidate string) error {
	if err := validateTargetBranch(branch); err != nil {
		return err
	}
	if !commitPattern.MatchString(base) || !commitPattern.MatchString(candidate) {
		return fmt.Errorf("a candidate is promoted between two full commit ids, not %q and %q", base, candidate)
	}
	descends, err := m.descendsFrom(ctx, base, candidate)
	if err != nil {
		return err
	}
	if !descends {
		return fmt.Errorf("%w: candidate %s does not descend from %s", ErrNotFastForward, candidate, base)
	}
	inPrimary, err := m.targetCheckout(ctx, branch)
	if err != nil {
		return err
	}
	if inPrimary {
		if err := m.ValidateReady(ctx); err != nil {
			return fmt.Errorf("primary checkout is not ready for integration: %w", err)
		}
	}
	current, err := m.resolveBranchCommit(ctx, branch)
	if err != nil {
		return err
	}
	if current != base {
		return fmt.Errorf("%w: %s is at %s, the candidate was built on %s", ErrTargetDrift, branch, current, base)
	}
	if err := m.fastForward(ctx, "the merge queue candidate", branch, base, candidate, inPrimary); err != nil {
		return err
	}
	moved, err := m.resolveBranchCommit(ctx, branch)
	if err != nil {
		return err
	}
	if moved != candidate {
		return fmt.Errorf("%w: %s is at %s after the update, want %s", ErrNotFastForward, branch, moved, candidate)
	}
	return nil
}

// TargetHolds reports whether a branch's history contains a commit.
func (m *Manager) TargetHolds(ctx context.Context, branch, commit string) (bool, error) {
	if err := validateTargetBranch(branch); err != nil {
		return false, err
	}
	if !commitPattern.MatchString(commit) {
		return false, fmt.Errorf("%q is not a full commit id", commit)
	}
	tip, err := m.resolveBranchCommit(ctx, branch)
	if err != nil {
		return false, err
	}
	return m.descendsFrom(ctx, commit, tip)
}

// QueueCandidateBranchCommit reports where a candidate branch stands on the
// push remote, and false where it does not exist.
func (m *Manager) QueueCandidateBranchCommit(ctx context.Context, branch string) (string, bool, error) {
	if err := validateQueueCandidateBranch(branch); err != nil {
		return "", false, err
	}
	return m.remoteCommit(ctx, m.pushRemote, branch)
}

// PublishQueueCandidate puts a candidate on its candidate branch on the push
// remote, as a compare-and-swap on expected: the commit the branch held, or
// empty for a branch that must not exist yet. A branch already at the
// candidate is left alone. The branch is the queue's own and never a run's or
// a target's, so what is replaced is only ever an earlier candidate the queue
// put there.
func (m *Manager) PublishQueueCandidate(ctx context.Context, branch, candidate, expected string) error {
	if err := validateQueueCandidateBranch(branch); err != nil {
		return err
	}
	if !commitPattern.MatchString(candidate) || (expected != "" && !commitPattern.MatchString(expected)) {
		return fmt.Errorf("a candidate is published as a full commit id over another or none, not %q over %q", candidate, expected)
	}
	if expected == candidate {
		return nil
	}
	result, err := m.runRemote(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"push", "--force-with-lease=refs/heads/"+branch+":"+expected,
		m.pushRemote, candidate+":refs/heads/"+branch)
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("%w: publish %s on %s over %q failed with exit code %d: %s",
			ErrRemotePushRejected, candidate, branch, expected, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	published, exists, err := m.remoteCommit(ctx, m.pushRemote, branch)
	if err != nil {
		return err
	}
	if !exists || published != candidate {
		return fmt.Errorf("%w: %s on %s is at %q after the push, want %s", ErrRemotePushRejected, branch, m.pushRemote, published, candidate)
	}
	return nil
}

func validateQueueCandidateBranch(branch string) error {
	if err := validateRef(branch); err != nil {
		return err
	}
	if !strings.HasPrefix(branch, queueCandidateBranchPrefix) || !queueCandidatePattern.MatchString(strings.TrimPrefix(branch, queueCandidateBranchPrefix)) {
		return fmt.Errorf("%s is not a merge queue candidate branch", branch)
	}
	return nil
}

// CandidatePaths lists every path a candidate changes against the base it
// lands on, measured from where the two meet, so a head that lands on a target
// that has since moved is measured by its own change and not by the target's.
// Renames are listed as the path removed and the path added, so a move out of
// a protected path is caught as surely as a move into one.
func (m *Manager) CandidatePaths(ctx context.Context, base, candidate string) ([]string, error) {
	if !commitPattern.MatchString(base) || !commitPattern.MatchString(candidate) {
		return nil, fmt.Errorf("a candidate's paths are listed between two full commit ids, not %q and %q", base, candidate)
	}
	result, err := m.run(ctx, "-C", m.repositoryRoot, "diff", "--name-only", "-z", "--no-renames", "--no-ext-diff", base+"..."+candidate, "--")
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list the paths %s changes against %s failed with exit code %d: %s", candidate, base, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	var paths []string
	for _, path := range strings.Split(result.Stdout, "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}
