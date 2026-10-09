package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A merge queue candidate is what the queue verifies before anything lands:
// the target branch as it stands, with an admitted change's approved head
// merged onto it. It is built in a checkout of its own under the worktree root,
// detached, for the reason a landing checkout is: it is nobody's change, and a
// branch for it would be one more ref every sweep has to account for. The
// run's own branch and the approved head are only read — the candidate is a
// merge commit whose second parent is the approved head, so the commit the
// original review approved stays in the candidate's history exactly as it was.
//
// Nothing here moves the target branch or any other ref.

// queueCandidatePattern is what a candidate checkout may be named for: the
// queue's entry identity, whose shape the queue owns.
var queueCandidatePattern = regexp.MustCompile(`^mqe-[a-f0-9]{32}$`)

// QueueCandidateRequest names a candidate to build: the target branch it is
// built on, and the heads merged onto it in order.
type QueueCandidateRequest struct {
	// Entry names the queue entry the candidate is for, and so its checkout.
	Entry        string
	TargetBranch string
	Heads        []string
	// Message is the candidate commit's message.
	Message string
}

// QueueCandidate is a built candidate and where it is checked out.
type QueueCandidate struct {
	Path         string
	TargetBranch string
	BaseCommit   string
	Heads        []string
	Commit       string
	// Tree is the candidate's content: what a promotion would put on the target.
	Tree string
}

// ErrQueueCandidateEmpty is a head the target already holds, so merging it
// would change nothing and there is no candidate to verify.
var ErrQueueCandidateEmpty = errors.New("the target branch already holds the admitted head, so there is no candidate to build")

// QueueCandidateConflict is a head that would not merge onto the target
// without somebody resolving it. Nothing is left behind: the checkout is
// removed before this is reported.
type QueueCandidateConflict struct {
	TargetBranch string
	BaseCommit   string
	Head         string
	Paths        []string
	Err          error
}

func (c *QueueCandidateConflict) Error() string {
	return fmt.Sprintf("the approved head %s does not merge onto %s at %s (conflicting: %s): %v",
		c.Head, c.TargetBranch, c.BaseCommit, strings.Join(c.Paths, ", "), c.Err)
}

func (c *QueueCandidateConflict) Unwrap() error { return c.Err }

func queueCandidateDirectoryName(entry string) string {
	return "candidate-" + strings.TrimPrefix(entry, "mqe-")[:12]
}

// TargetCommit reports where a target branch stands now.
func (m *Manager) TargetCommit(ctx context.Context, branch string) (string, error) {
	if err := validateTargetBranch(branch); err != nil {
		return "", err
	}
	return m.resolveBranchCommit(ctx, branch)
}

// BuildQueueCandidate cuts a detached checkout of the target branch as it
// stands and merges each head onto it in order, under the harness's identity
// and with the repository's hooks off. A checkout a dead worker left at the
// same path is removed first, because the path is the entry's own and nothing
// else is ever put there.
func (m *Manager) BuildQueueCandidate(ctx context.Context, request QueueCandidateRequest) (QueueCandidate, error) {
	if !queueCandidatePattern.MatchString(request.Entry) {
		return QueueCandidate{}, fmt.Errorf("entry %q is not one a candidate checkout can be named for", request.Entry)
	}
	if len(request.Heads) == 0 {
		return QueueCandidate{}, errors.New("a candidate merges at least one head")
	}
	for _, head := range request.Heads {
		if !commitPattern.MatchString(head) {
			return QueueCandidate{}, fmt.Errorf("head %q is not a full commit id", head)
		}
	}
	base, err := m.TargetCommit(ctx, request.TargetBranch)
	if err != nil {
		return QueueCandidate{}, err
	}
	message := strings.TrimSpace(request.Message)
	if message == "" {
		message = fmt.Sprintf("yoyodyne: merge queue candidate for %s\n\nTarget: %s at %s\n", request.Entry, request.TargetBranch, base)
	}
	path, err := m.cutCandidateCheckout(ctx, request.Entry, base)
	if err != nil {
		return QueueCandidate{}, err
	}
	candidate, err := m.mergeHeads(ctx, path, request, base, message)
	if err != nil {
		if removeErr := m.RemoveQueueCandidate(ctx, path); removeErr != nil {
			return QueueCandidate{}, errors.Join(err, fmt.Errorf("remove the candidate checkout at %s afterwards: %w", path, removeErr))
		}
		return QueueCandidate{}, err
	}
	return candidate, nil
}

// RestoreQueueCandidate puts the checkout of an already-built candidate back
// where a restarted worker expects it: the checkout standing there is kept if
// it is at the candidate with no tracked file changed, and cut again from the
// commit otherwise. Untracked files do not count against it: they are what the
// checks built, which is worth keeping, and they change nothing the candidate
// commit says.
// The commit is not rebuilt, so the candidate and everything recorded against
// it are the same ones as before the restart.
func (m *Manager) RestoreQueueCandidate(ctx context.Context, entry, commit string) (string, error) {
	if !queueCandidatePattern.MatchString(entry) {
		return "", fmt.Errorf("entry %q is not one a candidate checkout can be named for", entry)
	}
	if !commitPattern.MatchString(commit) {
		return "", fmt.Errorf("candidate %q is not a full commit id", commit)
	}
	path := filepath.Join(m.worktreeRoot, queueCandidateDirectoryName(entry))
	if head, err := m.resolveWorktreeHead(ctx, path); err == nil && head == commit {
		status, err := m.run(ctx, "-C", path, "status", "--porcelain", "--untracked-files=no")
		if err == nil && status.Status == execution.ProcessSucceeded && strings.TrimSpace(status.Stdout) == "" {
			return path, nil
		}
	}
	return m.cutCandidateCheckout(ctx, entry, commit)
}

// RemoveQueueCandidate removes a candidate checkout. It refuses a path that is
// not one, because it is handed a path a record carried and a record can be
// wrong.
func (m *Manager) RemoveQueueCandidate(ctx context.Context, path string) error {
	if filepath.Dir(path) != m.worktreeRoot || !strings.HasPrefix(filepath.Base(path), "candidate-") {
		return fmt.Errorf("%s is not a merge queue candidate checkout under %s", path, m.worktreeRoot)
	}
	ctx, lease, err := m.leaseRegistry(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = lease.release() }()
	return m.removeCheckout(ctx, path)
}

// HoldsCommit reports whether the repository still has a commit.
func (m *Manager) HoldsCommit(ctx context.Context, commit string) (bool, error) {
	if !commitPattern.MatchString(commit) {
		return false, fmt.Errorf("%q is not a full commit id", commit)
	}
	result, err := m.run(ctx, "-C", m.repositoryRoot, "cat-file", "-e", commit+"^{commit}")
	if err != nil {
		return false, err
	}
	return result.Status == execution.ProcessSucceeded, nil
}

// CandidateChanges describes what a candidate adds to the base it was built
// on, as one bounded change for its reviewer.
func (m *Manager) CandidateChanges(ctx context.Context, baseCommit, candidate string, limits DiffLimits) (BranchChange, error) {
	limits, err := limits.resolve()
	if err != nil {
		return BranchChange{}, err
	}
	if !commitPattern.MatchString(baseCommit) || !commitPattern.MatchString(candidate) {
		return BranchChange{}, fmt.Errorf("a candidate is described between two full commit ids, not %q and %q", baseCommit, candidate)
	}
	change := BranchChange{BaseRef: baseCommit, BaseCommit: baseCommit, HeadCommit: candidate}
	total, err := m.countCommits(ctx, baseCommit, candidate)
	if err != nil {
		return BranchChange{}, err
	}
	if total == 0 {
		return BranchChange{}, ErrNoAccumulatedChange
	}
	change.Commits, err = m.describeCommits(ctx, baseCommit, candidate, limits.MaxCommits)
	if err != nil {
		return BranchChange{}, err
	}
	change.CommitsOmitted = total - len(change.Commits)
	change.Changes, err = m.rangeDiff(ctx, baseCommit, candidate, limits.MaxTotalBytes)
	if err != nil {
		return BranchChange{}, err
	}
	if change.CommitsOmitted > 0 {
		change.Changes.Truncated = true
	}
	return change, nil
}

func (m *Manager) cutCandidateCheckout(ctx context.Context, entry, commit string) (string, error) {
	if err := os.MkdirAll(m.worktreeRoot, 0o700); err != nil {
		return "", fmt.Errorf("create worktree root: %w", err)
	}
	path := filepath.Join(m.worktreeRoot, queueCandidateDirectoryName(entry))
	ctx, lease, err := m.leaseRegistry(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = lease.release() }()
	if _, err := os.Lstat(path); err == nil {
		if err := m.removeCheckout(ctx, path); err != nil {
			return "", fmt.Errorf("remove the candidate checkout a previous worker left at %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect candidate checkout path: %w", err)
	}
	result, err := m.run(ctx, "-C", m.repositoryRoot, "worktree", "add", "--detach", path, commit)
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("create candidate checkout failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return path, nil
}

// mergeHeads merges each head onto the checkout in order and reads back what
// it built. A head that will not merge is abandoned rather than resolved.
func (m *Manager) mergeHeads(ctx context.Context, path string, request QueueCandidateRequest, base, message string) (QueueCandidate, error) {
	for _, head := range request.Heads {
		contained, err := m.run(ctx, "-C", path, "merge-base", "--is-ancestor", head, "HEAD")
		if err != nil {
			return QueueCandidate{}, err
		}
		if contained.Status == execution.ProcessSucceeded {
			return QueueCandidate{}, ErrQueueCandidateEmpty
		}
		merged, err := m.runWithEnvironment(ctx, harnessCommitEnvironment(), "-C", path,
			"-c", "core.hooksPath="+os.DevNull,
			"-c", "user.name="+harnessCommitAuthorName,
			"-c", "user.email="+harnessCommitAuthorEmail,
			"merge", "--no-ff", "--no-gpg-sign", "--no-edit", "-m", message, head)
		if err != nil {
			return QueueCandidate{}, err
		}
		if merged.Status != execution.ProcessSucceeded {
			failed := fmt.Errorf("merge %s onto %s failed with exit code %d: %s", head, base, merged.ExitCode, strings.TrimSpace(merged.Stderr))
			conflicted, _ := m.conflictedPaths(ctx, path)
			if len(conflicted) == 0 {
				return QueueCandidate{}, failed
			}
			return QueueCandidate{}, &QueueCandidateConflict{TargetBranch: request.TargetBranch, BaseCommit: base, Head: head, Paths: conflicted, Err: failed}
		}
	}
	commit, err := m.resolveWorktreeHead(ctx, path)
	if err != nil {
		return QueueCandidate{}, err
	}
	tree, err := m.run(ctx, "-C", path, "rev-parse", "--verify", commit+"^{tree}")
	if err != nil {
		return QueueCandidate{}, err
	}
	content := strings.TrimSpace(tree.Stdout)
	if tree.Status != execution.ProcessSucceeded || !commitPattern.MatchString(content) {
		return QueueCandidate{}, fmt.Errorf("read the candidate's tree failed with exit code %d: %s", tree.ExitCode, strings.TrimSpace(tree.Stderr))
	}
	return QueueCandidate{
		Path: path, TargetBranch: request.TargetBranch, BaseCommit: base,
		Heads: append([]string(nil), request.Heads...), Commit: commit, Tree: content,
	}, nil
}
