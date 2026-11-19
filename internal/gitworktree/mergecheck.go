package gitworktree

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// MergeCheck is what comparing a merge commit the forge made against the
// candidate that was checked found. The target branch does not require pull
// requests to be up to date with it, so the forge may merge a candidate onto a
// base nobody checked; the checks and the review judged the candidate built on
// RecordedBase, and the merge is the same revision only where it landed on that
// base and changed exactly what the candidate changed.
type MergeCheck struct {
	MergeCommit  string `json:"merge_commit"`
	Candidate    string `json:"candidate"`
	RecordedBase string `json:"recorded_base"`
	// FirstParent is the merge commit's first parent: the target branch as the
	// forge found it.
	FirstParent string `json:"first_parent"`
	// FilesDiffer names each path the merge changed from its first parent
	// differently from how the candidate changed it from its recorded base,
	// sorted: a path only one of them changed, or one they left with different
	// content or mode.
	FilesDiffer []string `json:"files_differ,omitempty"`
}

// BaseDiffers reports a merge that landed on something other than the base the
// candidate was checked on.
func (c MergeCheck) BaseDiffers() bool { return c.FirstParent != c.RecordedBase }

// Matches reports a merge on the recorded base whose files are the candidate's.
func (c MergeCheck) Matches() bool { return !c.BaseDiffers() && len(c.FilesDiffer) == 0 }

// maxNamedPaths bounds the paths Says names, so a wholly different merge says
// so without listing a repository.
const maxNamedPaths = 10

// Says is the difference in plain words, and empty where the merge matches.
func (c MergeCheck) Says() string {
	var differences []string
	if c.BaseDiffers() {
		differences = append(differences, fmt.Sprintf("its first parent is %s, and the candidate was checked on %s", c.FirstParent, c.RecordedBase))
	}
	if len(c.FilesDiffer) > 0 {
		named := c.FilesDiffer
		more := ""
		if len(named) > maxNamedPaths {
			more = fmt.Sprintf(" and %d more", len(named)-maxNamedPaths)
			named = named[:maxNamedPaths]
		}
		differences = append(differences, fmt.Sprintf("its files are not the candidate's: %s%s differ", strings.Join(named, ", "), more))
	}
	return strings.Join(differences, "; ")
}

// CheckMergeCommit compares a merge commit with the candidate it landed. All
// three commits must be in this repository, which holds once the remote target
// has been read after the merge. It reads and changes nothing.
func (m *Manager) CheckMergeCommit(ctx context.Context, mergeCommit, recordedBase, candidate string) (MergeCheck, error) {
	check := MergeCheck{MergeCommit: mergeCommit, Candidate: candidate, RecordedBase: recordedBase}
	for _, commit := range []string{mergeCommit, recordedBase, candidate} {
		if !commitPattern.MatchString(commit) {
			return check, fmt.Errorf("commit %q is invalid", commit)
		}
	}
	parents, err := m.parentsOf(ctx, mergeCommit)
	if err != nil {
		return check, err
	}
	if len(parents) == 0 {
		return check, fmt.Errorf("merge commit %s has no parent, so it landed nothing on a base", mergeCommit)
	}
	check.FirstParent = parents[0]
	merged, err := m.changesBetween(ctx, check.FirstParent, mergeCommit)
	if err != nil {
		return check, err
	}
	checked, err := m.changesBetween(ctx, recordedBase, candidate)
	if err != nil {
		return check, err
	}
	differ := map[string]bool{}
	for path, change := range merged {
		if checked[path] != change {
			differ[path] = true
		}
	}
	for path, change := range checked {
		if merged[path] != change {
			differ[path] = true
		}
	}
	for path := range differ {
		check.FilesDiffer = append(check.FilesDiffer, path)
	}
	sort.Strings(check.FilesDiffer)
	return check, nil
}

// changesBetween reads what a commit did to its parent as each changed path's
// resulting mode and content, which is what two changes made on different
// bases have to share to be the same change.
func (m *Manager) changesBetween(ctx context.Context, from, to string) (map[string]string, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "diff-tree", "-r", "-z", "--raw", "--no-renames", "--no-abbrev", from, to)
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list what changed from %s to %s failed with exit code %d: %s", from, to, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	fields := strings.Split(result.Stdout, "\x00")
	changes := map[string]string{}
	for index := 0; index+1 < len(fields); index += 2 {
		// ":<old mode> <new mode> <old id> <new id> <status>" then the path.
		meta := strings.Fields(strings.TrimPrefix(fields[index], ":"))
		if len(meta) < 5 {
			return nil, fmt.Errorf("list what changed from %s to %s answered %q", from, to, fields[index])
		}
		changes[fields[index+1]] = meta[1] + " " + meta[3]
	}
	return changes, nil
}

// queueCandidateBranchMark is in the subject of the forge's merge of a merge
// queue candidate, which the forge names after the branch it merged. Such a
// candidate is a commit the harness built directly on the base it recorded, so
// the candidate's own first parent is that base.
const queueCandidateBranchMark = "/merge-queue/"

// LandedPullRequest is one commit on a target branch's first-parent history, as
// a landing through a pull request leaves it: the merge, the candidate it
// merged (its second parent), and the base that candidate was built on (the
// candidate's first parent).
type LandedPullRequest struct {
	Merge     string
	Candidate string
	Base      string
	Subject   string
	// QueueCandidate is a merge of a merge queue candidate, whose Base is the
	// base the harness recorded for it. For any other merge Base is only the
	// first parent of the merged commit, which says nothing about what was
	// checked, and the caller looks the recorded base up elsewhere.
	QueueCandidate bool
	// Unchecked is why no candidate or base could be read from the commit,
	// where none could: a squash or rebase merge leaves a single-parent commit.
	Unchecked string
}

// LandedPullRequests lists, oldest first, each commit on ref's first-parent
// history made after since. It reads this repository only.
func (m *Manager) LandedPullRequests(ctx context.Context, ref string, since time.Time) ([]LandedPullRequest, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "log", "--first-parent", "--reverse",
		"--since="+since.UTC().Format(time.RFC3339), "--format=%H %P%x09%s", ref, "--")
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list the landings on %s since %s failed with exit code %d: %s", ref, since.UTC().Format(time.RFC3339), result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	var landings []LandedPullRequest
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		if line == "" {
			continue
		}
		heads, subject, _ := strings.Cut(line, "\t")
		fields := strings.Fields(heads)
		landed := LandedPullRequest{Merge: fields[0], Subject: subject}
		if len(fields) != 3 {
			landed.Unchecked = fmt.Sprintf("it has %d parents, so it is not a merge of a candidate", len(fields)-1)
			landings = append(landings, landed)
			continue
		}
		landed.Candidate = fields[2]
		landed.QueueCandidate = strings.Contains(subject, queueCandidateBranchMark)
		if !landed.QueueCandidate {
			landings = append(landings, landed)
			continue
		}
		parents, err := m.parentsOf(ctx, landed.Candidate)
		if err != nil {
			return nil, err
		}
		if len(parents) == 0 {
			landed.Unchecked = "its merged commit has no parent, so no base can be read"
		} else {
			landed.Base = parents[0]
		}
		landings = append(landings, landed)
	}
	return landings, nil
}
