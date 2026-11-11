package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A conflicted merge is what a run that resolves a conflict starts from: the
// merge of a head onto the target branch, with every clean part of it applied
// and every conflicting file carrying Git's conflict markers, committed as it
// stands. The commit has the target commit as its only parent, so the change a
// run makes on top of it is exactly the developer's resolution, and every
// commit above it carries the harness's identity. It is built in a checkout of
// its own, the queue candidate's, which is removed afterwards.
//
// Nothing here moves the target branch or any other ref, and the commit it
// returns is held by whatever the caller keeps it with (a run's branch).

// ConflictedMergeRequest names the merge to leave conflicted.
type ConflictedMergeRequest struct {
	// Entry names the checkout the merge is built in, as for a queue candidate.
	Entry        string
	TargetBranch string
	// Head is merged onto the target branch's commit, which has to be Base.
	Head string
	// Base is the target commit the merge is built on. Empty builds it on the
	// target as it stands.
	Base    string
	Message string
}

// ConflictedMerge is a merge left conflicted, committed.
type ConflictedMerge struct {
	TargetBranch string
	// BaseCommit is the target commit, the new commit's only parent.
	BaseCommit string
	Head       string
	// Commit is the commit that carries the conflict markers.
	Commit string
	// Paths are the files that conflict, sorted.
	Paths []string
}

// ErrMergeDoesNotConflict is a merge asked to be left conflicted that merges
// cleanly.
var ErrMergeDoesNotConflict = errors.New("the head merges onto the target without a conflict, so there is nothing to leave conflicted")

// BuildConflictedMerge merges the head onto the target in a detached checkout,
// stages everything as Git left it, and commits it under the harness's identity
// with the target commit as its only parent.
func (m *Manager) BuildConflictedMerge(ctx context.Context, request ConflictedMergeRequest) (ConflictedMerge, error) {
	if !queueCandidatePattern.MatchString(request.Entry) {
		return ConflictedMerge{}, fmt.Errorf("entry %q is not one a checkout can be named for", request.Entry)
	}
	if !commitPattern.MatchString(request.Head) {
		return ConflictedMerge{}, fmt.Errorf("head %q is not a full commit id", request.Head)
	}
	if request.Base != "" && !commitPattern.MatchString(request.Base) {
		return ConflictedMerge{}, fmt.Errorf("base %q is not a full commit id", request.Base)
	}
	base := request.Base
	if base == "" {
		var err error
		if base, err = m.TargetCommit(ctx, request.TargetBranch); err != nil {
			return ConflictedMerge{}, err
		}
	}
	message := strings.TrimSpace(request.Message)
	if message == "" {
		message = fmt.Sprintf("yoyodyne: %s merged onto %s with its conflicts left in place", request.Head, request.TargetBranch)
	}
	path, err := m.cutCandidateCheckout(ctx, request.Entry, base)
	if err != nil {
		return ConflictedMerge{}, err
	}
	built, err := m.leaveConflicted(ctx, path, request, base, message)
	if removeErr := m.RemoveQueueCandidate(ctx, path); removeErr != nil {
		err = errors.Join(err, fmt.Errorf("remove the checkout at %s afterwards: %w", path, removeErr))
	}
	if err != nil {
		return ConflictedMerge{}, err
	}
	return built, nil
}

func (m *Manager) leaveConflicted(ctx context.Context, path string, request ConflictedMergeRequest, base, message string) (ConflictedMerge, error) {
	environment := harnessCommitEnvironment()
	merged, err := m.runWithEnvironment(ctx, environment, "-C", path,
		"-c", "core.hooksPath="+os.DevNull,
		"-c", "user.name="+harnessCommitAuthorName,
		"-c", "user.email="+harnessCommitAuthorEmail,
		"merge", "--no-ff", "--no-commit", "--no-gpg-sign", request.Head)
	if err != nil {
		return ConflictedMerge{}, err
	}
	if merged.Status == execution.ProcessSucceeded {
		_, _ = m.run(ctx, "-C", path, "merge", "--abort")
		return ConflictedMerge{}, ErrMergeDoesNotConflict
	}
	conflicted, err := m.conflictedPaths(ctx, path)
	if err != nil {
		return ConflictedMerge{}, err
	}
	if len(conflicted) == 0 {
		return ConflictedMerge{}, fmt.Errorf("merge %s onto %s failed with exit code %d and left no conflict: %s", request.Head, base, merged.ExitCode, strings.TrimSpace(merged.Stderr))
	}
	sort.Strings(conflicted)
	// What is staged is what Git left: the clean part of the merge applied and the
	// conflicting files as they stand in the working tree, markers and all.
	added, err := m.runWithEnvironment(ctx, environment, "-C", path, "add", "--all")
	if err != nil {
		return ConflictedMerge{}, err
	}
	if added.Status != execution.ProcessSucceeded {
		return ConflictedMerge{}, fmt.Errorf("stage the conflicted merge failed with exit code %d: %s", added.ExitCode, strings.TrimSpace(added.Stderr))
	}
	tree, err := m.runWithEnvironment(ctx, environment, "-C", path, "write-tree")
	if err != nil {
		return ConflictedMerge{}, err
	}
	treeID := strings.TrimSpace(tree.Stdout)
	if tree.Status != execution.ProcessSucceeded || !commitPattern.MatchString(treeID) {
		return ConflictedMerge{}, fmt.Errorf("write the conflicted merge's tree failed with exit code %d: %s", tree.ExitCode, strings.TrimSpace(tree.Stderr))
	}
	// One parent only: the merge's second parent is never recorded, so the
	// commit is the target with the conflict laid over it.
	commit, err := m.commitTree(ctx, path, treeID, message, base)
	if err != nil {
		return ConflictedMerge{}, err
	}
	return ConflictedMerge{TargetBranch: request.TargetBranch, BaseCommit: base, Head: request.Head, Commit: commit, Paths: conflicted}, nil
}

// CommitMergeOf writes a commit with the content of the source commit and the
// parents base then head, under the harness's identity: the merge the source's
// change would have been had the two been merged cleanly. It writes a commit
// object only and moves no ref.
func (m *Manager) CommitMergeOf(ctx context.Context, source, base, head, message string) (string, error) {
	for name, commit := range map[string]string{"source": source, "base": base, "head": head} {
		if !commitPattern.MatchString(commit) {
			return "", fmt.Errorf("%s %q is not a full commit id", name, commit)
		}
	}
	tree, err := m.run(ctx, "-C", m.repositoryRoot, "rev-parse", "--verify", source+"^{tree}")
	if err != nil {
		return "", err
	}
	treeID := strings.TrimSpace(tree.Stdout)
	if tree.Status != execution.ProcessSucceeded || !commitPattern.MatchString(treeID) {
		return "", fmt.Errorf("read the tree of %s failed with exit code %d: %s", source, tree.ExitCode, strings.TrimSpace(tree.Stderr))
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = fmt.Sprintf("yoyodyne: merge %s into %s", head, base)
	}
	return m.commitTree(ctx, m.repositoryRoot, treeID, message, base, head)
}

// commitTree writes a commit of the tree with the parents given, in order.
func (m *Manager) commitTree(ctx context.Context, directory, tree, message string, parents ...string) (string, error) {
	args := []string{"-C", directory, "-c", "core.hooksPath=" + os.DevNull, "commit-tree", "--no-gpg-sign", tree}
	for _, parent := range parents {
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", message)
	made, err := m.runWithEnvironment(ctx, harnessCommitEnvironment(), args...)
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(made.Stdout)
	if made.Status != execution.ProcessSucceeded || !commitPattern.MatchString(commit) {
		return "", fmt.Errorf("write the commit of tree %s failed with exit code %d: %s", tree, made.ExitCode, strings.TrimSpace(made.Stderr))
	}
	return commit, nil
}
