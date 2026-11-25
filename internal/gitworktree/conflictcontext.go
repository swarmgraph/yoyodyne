package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// RangeChanges is what one commit changed over another, as a bounded change.
// It reads the repository rather than a worktree, so it still describes a
// change whose worktree has since been moved onto the target.
func (m *Manager) RangeChanges(ctx context.Context, baseCommit, headCommit string, limits DiffLimits) (ChangeDiff, error) {
	limits, err := limits.resolve()
	if err != nil {
		return ChangeDiff{}, err
	}
	if !commitPattern.MatchString(baseCommit) || !commitPattern.MatchString(headCommit) {
		return ChangeDiff{}, fmt.Errorf("a change lies between two full commit hashes, got %q and %q", baseCommit, headCommit)
	}
	return m.rangeDiff(ctx, baseCommit, headCommit, limits.MaxTotalBytes)
}

// CommitsTouching lists the commits headCommit carries over baseCommit that
// touch any of paths, oldest first, keeping the newest maxCommits of them, and
// how many older ones it left out.
func (m *Manager) CommitsTouching(ctx context.Context, baseCommit, headCommit string, paths []string, maxCommits int) ([]Commit, int, error) {
	if !commitPattern.MatchString(baseCommit) || !commitPattern.MatchString(headCommit) {
		return nil, 0, fmt.Errorf("commits lie between two full commit hashes, got %q and %q", baseCommit, headCommit)
	}
	if len(paths) == 0 {
		return nil, 0, errors.New("the paths whose commits are wanted are required")
	}
	// Paths are literal: a file whose name holds a wildcard names itself alone.
	// History is not simplified, so a commit that reached the target only through
	// a merge is listed too.
	run := func(command string, options ...string) (string, error) {
		args := append([]string{"--literal-pathspecs", "-C", m.repositoryRoot, command, "--full-history"}, options...)
		args = append(append(args, baseCommit+".."+headCommit, "--"), paths...)
		result, err := m.run(ctx, args...)
		if err != nil {
			return "", err
		}
		if result.Status != execution.ProcessSucceeded {
			return "", fmt.Errorf("list the commits touching the paths failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
		}
		return strings.TrimSpace(result.Stdout), nil
	}
	counted, err := run("rev-list", "--count")
	if err != nil {
		return nil, 0, err
	}
	total, err := strconv.Atoi(counted)
	if err != nil {
		return nil, 0, fmt.Errorf("parse the count of commits touching the paths: %w", err)
	}
	listed, err := run("log", "--reverse", "--max-count="+strconv.Itoa(maxCommits), "--format=%H%x00%s")
	if err != nil {
		return nil, 0, err
	}
	var commits []Commit
	for _, line := range strings.Split(listed, "\n") {
		if commit, subject, found := strings.Cut(line, "\x00"); found {
			commits = append(commits, Commit{Commit: commit, Subject: boundSubject(subject)})
		}
	}
	return commits, total - len(commits), nil
}
