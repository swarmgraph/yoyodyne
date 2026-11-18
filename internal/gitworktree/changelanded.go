package gitworktree

// Whether a published change is already on its target branch by another route.
//
// A run's pull request carries one change. That change can reach the target
// without that pull request ever merging: a later run of the same item
// publishes it again, another item's change carries it, or a merge queue lands
// a rebuilt copy. Containment of the run's own head commit says nothing in any
// of those cases, because the commit that landed is a different commit with
// the same content. What answers the question is the content: merging the
// run's head into the target, from the point the two last shared, leaves the
// target's tree exactly as it was. A change that does that is already there.
//
// The test is Git's own three-way merge of the trees, written to the object
// store and never to a branch, so it moves nothing and reads only what the
// repository has.

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// ChangeLanding is what one read of the remote target found about one change.
type ChangeLanding struct {
	TargetBranch string `json:"target_branch"`
	// Tip is where the remote target branch stood when it was read.
	Tip string `json:"tip,omitempty"`
	// Landed reports the target already carrying the change: its head commit is
	// in the target's history, or merging the head into the target changes no
	// file.
	Landed bool `json:"landed"`
	// Commit is the commit on the target's first-parent line that brought the
	// change in: the earliest one that carries it. It is empty where the change
	// has not landed.
	Commit string `json:"commit,omitempty"`
	// Subject is Commit's first line, and Number the pull request it names where
	// it is the forge's merge commit or a squash of one ("Merge pull request #12
	// from ..." or "... (#12)"), zero where it names none.
	Subject string `json:"subject,omitempty"`
	Number  int    `json:"number,omitempty"`
	// Unread says why the question could not be answered: the head commit is
	// not in this repository, or the change is empty. Landed is false then, and
	// the answer is "not known" rather than "no".
	Unread string `json:"unread,omitempty"`
}

// ChangeLanded reads the remote target branch and reports whether the change a
// run published at head is already on it, and which commit brought it there.
//
// It reads and moves nothing but the scratch ref the fetch writes. A head this
// repository does not have is reported as unread rather than fetched: the run
// branch it lived on may be gone, and a guess about a change nobody can read
// must never be the reason a pull request is closed.
func (m *Manager) ChangeLanded(ctx context.Context, targetBranch, head string) (ChangeLanding, error) {
	landing := ChangeLanding{TargetBranch: targetBranch}
	if err := validateTargetBranch(targetBranch); err != nil {
		return landing, err
	}
	if !commitPattern.MatchString(head) {
		return landing, fmt.Errorf("head commit %q is invalid", head)
	}
	tip, exists, err := m.remoteCommit(ctx, m.remote, targetBranch)
	if err != nil {
		return landing, err
	}
	if !exists {
		landing.Unread = fmt.Sprintf("the remote has no branch %s", targetBranch)
		return landing, nil
	}
	landing.Tip = tip
	if err := m.fetchRemoteBranch(ctx, targetBranch, tip); err != nil {
		return landing, err
	}
	present, err := m.run(ctx, "-C", m.repositoryRoot, "cat-file", "-e", head+"^{commit}")
	if err != nil {
		return landing, err
	}
	if present.Status != execution.ProcessSucceeded {
		landing.Unread = fmt.Sprintf("commit %s is not in this repository, so what it changed cannot be read", head)
		return landing, nil
	}
	carried, err := m.carriesChange(ctx, tip, head)
	if err != nil {
		return landing, err
	}
	if carried.empty {
		landing.Unread = fmt.Sprintf("commit %s changes nothing beside %s, so there is no change to look for", head, targetBranch)
		return landing, nil
	}
	if !carried.landed {
		return landing, nil
	}
	landing.Landed = true
	commit, err := m.earliestCarrying(ctx, tip, head)
	if err != nil {
		return landing, err
	}
	landing.Commit = commit
	subject, err := m.run(ctx, "-C", m.repositoryRoot, "log", "-1", "--format=%s", commit)
	if err != nil {
		return landing, err
	}
	if subject.Status != execution.ProcessSucceeded {
		return landing, fmt.Errorf("read the subject of %s failed with exit code %d: %s", commit, subject.ExitCode, strings.TrimSpace(subject.Stderr))
	}
	landing.Subject = strings.TrimSpace(subject.Stdout)
	landing.Number = PullRequestNamed(landing.Subject)
	return landing, nil
}

// changeCarried is one commit's answer: whether it carries the change, and
// whether there was a change to carry at all.
type changeCarried struct {
	landed bool
	empty  bool
}

// carriesChange answers for one commit on the target. The head being in its
// history is the plain case; otherwise the head is merged into it from the
// point the two last shared, and a clean merge whose tree is the commit's own
// tree is a change already there. A merge with conflicts is a change not there.
func (m *Manager) carriesChange(ctx context.Context, commit, head string) (changeCarried, error) {
	contained, err := m.descendsFrom(ctx, head, commit)
	if err != nil {
		return changeCarried{}, err
	}
	if contained {
		return changeCarried{landed: true}, nil
	}
	base, err := m.run(ctx, "-C", m.repositoryRoot, "merge-base", commit, head)
	if err != nil {
		return changeCarried{}, err
	}
	if base.Status != execution.ProcessSucceeded {
		// No shared history: nothing on the target came from this change.
		return changeCarried{}, nil
	}
	shared := strings.TrimSpace(base.Stdout)
	if !commitPattern.MatchString(shared) {
		return changeCarried{}, fmt.Errorf("merge base %q of %s and %s is invalid", shared, commit, head)
	}
	unchanged, err := m.sameContent(ctx, shared, head)
	if err != nil {
		return changeCarried{}, err
	}
	if unchanged {
		return changeCarried{empty: true}, nil
	}
	merged, err := m.run(ctx, "-C", m.repositoryRoot, "merge-tree", "--write-tree", "--merge-base="+shared, commit, head)
	if err != nil {
		return changeCarried{}, err
	}
	switch {
	case merged.ExitCode == 1:
		return changeCarried{}, nil
	case merged.Status != execution.ProcessSucceeded:
		return changeCarried{}, fmt.Errorf("merge %s into %s in memory failed with exit code %d: %s", head, commit, merged.ExitCode, strings.TrimSpace(merged.Stderr))
	}
	lines := strings.Fields(merged.Stdout)
	if len(lines) == 0 || !commitPattern.MatchString(lines[0]) {
		return changeCarried{}, fmt.Errorf("merge %s into %s in memory answered %q", head, commit, strings.TrimSpace(merged.Stdout))
	}
	tree, err := m.resolveCommit(ctx, commit+"^{tree}")
	if err != nil {
		return changeCarried{}, err
	}
	return changeCarried{landed: lines[0] == tree}, nil
}

// earliestCarrying finds the commit on the target's first-parent line that
// brought the change in: the oldest one that carries it. Once a change is on a
// branch every later commit carries it too, so the line is searched by halves
// and the search costs a handful of merges however long the history is.
func (m *Manager) earliestCarrying(ctx context.Context, tip, head string) (string, error) {
	listed, err := m.run(ctx, "-C", m.repositoryRoot, "rev-list", "--first-parent", tip)
	if err != nil {
		return "", err
	}
	if listed.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("list the history of %s failed with exit code %d: %s", tip, listed.ExitCode, strings.TrimSpace(listed.Stderr))
	}
	line := strings.Fields(listed.Stdout)
	if len(line) == 0 {
		return "", fmt.Errorf("the history of %s lists no commits", tip)
	}
	if line[0] != tip {
		return "", fmt.Errorf("the history of %s begins at %s", tip, line[0])
	}
	// line is newest first. lo is known to carry the change; hi is the oldest
	// index still to rule out.
	lo, hi := 0, len(line)-1
	for lo < hi {
		middle := (lo + hi + 1) / 2
		carried, err := m.carriesChange(ctx, line[middle], head)
		if err != nil {
			return "", err
		}
		if carried.landed {
			lo = middle
		} else {
			hi = middle - 1
		}
	}
	return line[lo], nil
}

var (
	forgeMergeSubject  = regexp.MustCompile(`^Merge pull request #([0-9]+)\b`)
	forgeSquashSubject = regexp.MustCompile(`\(#([0-9]+)\)\s*$`)
)

// PullRequestNamed reads the pull request number a forge writes into the
// subject of the commit it lands one with: its merge commit, or a squash or
// rebase that ends the subject with the number. Zero is a subject naming none.
func PullRequestNamed(subject string) int {
	for _, pattern := range []*regexp.Regexp{forgeMergeSubject, forgeSquashSubject} {
		if found := pattern.FindStringSubmatch(strings.TrimSpace(subject)); found != nil {
			if number, err := strconv.Atoi(found[1]); err == nil && number > 0 {
				return number
			}
		}
	}
	return 0
}

// ReleaseReplacedBranch deletes a run branch whose pull request has been closed
// because its work landed another way, or was let go with its item. It is a
// compare-and-swap on the commit the pull request carried: a branch that moved
// past that commit holds something the pull request never had, and is left.
// Nothing at that commit is lost, because the forge keeps a closed pull
// request's commits. A branch still checked out is refused, and one already
// gone is reported as deleted, for the reason deleteIntegratedBranch gives.
func (m *Manager) ReleaseReplacedBranch(ctx context.Context, branch, commit string) (bool, error) {
	if err := validateRef(branch); err != nil {
		return false, err
	}
	if !commitPattern.MatchString(commit) {
		return false, fmt.Errorf("published commit %q is invalid", commit)
	}
	return m.deleteIntegratedBranch(ctx, branch, commit)
}
