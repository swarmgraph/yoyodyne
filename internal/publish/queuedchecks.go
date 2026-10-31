package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// MaxQueueRunsRead bounds the merge-queue workflow runs one reading lists. The
// forge lists the newest first and pages at a hundred, and a queued merge
// dropped long enough ago to have fallen off that page is one whose checks are
// not this reading's to find.
const MaxQueueRunsRead = 100

// queueBranchPrefix is where GitHub's merge queue puts the merge it builds of a
// queued pull request: gh-readonly-queue/<base>/pr-<number>-<head>.
const queueBranchPrefix = "gh-readonly-queue/"

// QueuedMergeChecks is how the forge's checks ended on the merge its merge queue
// built of a pull request. The queue does not run the checks on the request's
// own head: it merges the head onto the base branch, and the requests queued
// ahead of it, as a commit of its own on a temporary branch, and runs the
// checks there. So a request whose own head passes every check can still be
// removed from the queue for failed checks, and only this reading says which.
//
// Commit and Branch are empty where the forge lists no merge-queue run for the
// request: nothing was found, which says nothing about whether the checks
// passed.
type QueuedMergeChecks struct {
	Number  int
	Commit  string
	Branch  string
	Failing []FailedCheck
	// Pending names the checks on the queued merge that have not finished.
	Pending []string
	Passing int
}

// Found reports a reading that found the forge's queued merge of the request.
func (q QueuedMergeChecks) Found() bool { return q.Commit != "" }

// QueuedMergeChecks reads the checks the forge ran on the latest merge its merge
// queue built of a pull request: the newest merge-queue workflow run on a
// branch the queue made for the request names the commit, and every check run
// on that commit is read as a pull request head's are, annotations included.
//
// It reads and asks the forge to do nothing. A failed read is an error, so the
// caller says the checks went unread rather than that they passed.
func (g GitHub) QueuedMergeChecks(ctx context.Context, number int) (QueuedMergeChecks, error) {
	if number <= 0 {
		return QueuedMergeChecks{}, fmt.Errorf("pull request number %d is not a request", number)
	}
	// The listing carries far more than this needs, so gh selects the branch,
	// commit and time of each run before the runner bounds what it keeps.
	listed, err := g.apiQuery(ctx, "GET", "repos/{owner}/{repo}/actions/runs?event=merge_group&per_page="+strconv.Itoa(MaxQueueRunsRead),
		"--jq", "[(.workflow_runs // [])[] | {head_branch, head_sha, created_at}]")
	if err != nil {
		return QueuedMergeChecks{}, fmt.Errorf("ask the forge for its merge queue's runs for pull request %d: %w", number, err)
	}
	if listed.Status != execution.ProcessSucceeded {
		return QueuedMergeChecks{}, fmt.Errorf("ask the forge for its merge queue's runs for pull request %d: exit code %d: %s",
			number, listed.ExitCode, g.redact(firstLine(strings.TrimSpace(listed.Stderr))))
	}
	if listed.OutputTruncation != "" {
		return QueuedMergeChecks{}, fmt.Errorf("the forge's list of its merge queue's runs was cut and is not read: %s", listed.OutputTruncation)
	}
	var runs []struct {
		HeadBranch string    `json:"head_branch"`
		HeadSHA    string    `json:"head_sha"`
		CreatedAt  time.Time `json:"created_at"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(listed.Stdout)), &runs); err != nil {
		return QueuedMergeChecks{}, fmt.Errorf("decode the forge's list of its merge queue's runs: %w", err)
	}
	queued := QueuedMergeChecks{Number: number}
	var newest time.Time
	for _, run := range runs {
		branch, commit := strings.TrimSpace(run.HeadBranch), strings.TrimSpace(run.HeadSHA)
		if !queuedFor(branch, number) || !commitPattern.MatchString(commit) {
			continue
		}
		if queued.Commit == "" || run.CreatedAt.After(newest) {
			queued.Commit, queued.Branch, newest = commit, branch, run.CreatedAt
		}
	}
	if !queued.Found() {
		return queued, nil
	}
	queued.Failing, queued.Pending, queued.Passing, err = g.checkRuns(ctx, queued.Commit)
	if err != nil {
		return QueuedMergeChecks{}, fmt.Errorf("read the checks on the queued merge of pull request %d: %w", number, err)
	}
	return queued, nil
}

// queuedFor reports a branch the merge queue made for the given pull request.
// The request's number is followed by its head, so pr-15 does not match a
// branch made for pr-154.
func queuedFor(branch string, number int) bool {
	if !strings.HasPrefix(branch, queueBranchPrefix) {
		return false
	}
	return strings.Contains(branch[len(queueBranchPrefix):], "/pr-"+strconv.Itoa(number)+"-")
}
