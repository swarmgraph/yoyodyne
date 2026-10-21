package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// Revert is one commit on a branch that undid earlier ones, as git's own revert
// message names them: "This reverts commit <full id>." A revert written by hand
// with some other message names nothing this can read, and is not one.
type Revert struct {
	Commit string
	At     time.Time
	// Reverts is every commit the message says it reverts, as full ids.
	Reverts []string
}

// revertedCommit is the line git writes into every revert it makes. A revert of
// a merge says the same and adds which parent it kept, after the id.
var revertedCommit = regexp.MustCompile(`(?m)^This reverts commit ([0-9a-f]{40})\b`)

// The separators the log is read with: one between a commit's fields, and one
// after each commit, neither of which a commit message carries.
const (
	revertFieldSeparator  = "\x1f"
	revertCommitSeparator = "\x1e"
)

// RevertsOn lists the reverts made on branch since the moment given, oldest
// first. The branch is read where the remote holds it and, where this
// repository has no copy of the remote's, where the local branch stands: a
// change merged by the forge reaches the remote's branch first, and a revert
// of it is made there too.
func (m *Manager) RevertsOn(ctx context.Context, branch string, since time.Time) ([]Revert, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return nil, errors.New("a branch is required to read its reverts")
	}
	ref := branch
	remote := m.remote
	if remote == "" {
		remote = "origin"
	}
	if probe, err := m.run(ctx, "-C", m.repositoryRoot, "rev-parse", "--verify", "--quiet", "refs/remotes/"+remote+"/"+branch); err == nil && probe.Status == execution.ProcessSucceeded {
		ref = "refs/remotes/" + remote + "/" + branch
	}
	args := []string{"-C", m.repositoryRoot, "log",
		"--format=%H" + revertFieldSeparator + "%cI" + revertFieldSeparator + "%B" + revertCommitSeparator,
		"--grep=^This reverts commit ", "--extended-regexp"}
	if !since.IsZero() {
		args = append(args, "--since="+since.UTC().Format(time.RFC3339))
	}
	args = append(args, ref, "--")
	result, err := m.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("read the reverts on %s failed with exit code %d: %s", ref, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return parseReverts(result.Stdout)
}

// parseReverts reads the log RevertsOn asks for, newest first as git writes it,
// into reverts oldest first.
func parseReverts(log string) ([]Revert, error) {
	var reverts []Revert
	for _, entry := range strings.Split(log, revertCommitSeparator) {
		entry = strings.TrimLeft(entry, "\n")
		if strings.TrimSpace(entry) == "" {
			continue
		}
		fields := strings.SplitN(entry, revertFieldSeparator, 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("read a revert from the log: %q is not a commit, a time, and a message", oneline.Fold(entry, 80))
		}
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[1]))
		if err != nil {
			return nil, fmt.Errorf("read when %s was committed: %w", strings.TrimSpace(fields[0]), err)
		}
		revert := Revert{Commit: strings.TrimSpace(fields[0]), At: at}
		for _, found := range revertedCommit.FindAllStringSubmatch(fields[2], -1) {
			revert.Reverts = append(revert.Reverts, found[1])
		}
		if len(revert.Reverts) == 0 {
			continue
		}
		reverts = append(reverts, revert)
	}
	for first, last := 0, len(reverts)-1; first < last; first, last = first+1, last-1 {
		reverts[first], reverts[last] = reverts[last], reverts[first]
	}
	return reverts, nil
}
