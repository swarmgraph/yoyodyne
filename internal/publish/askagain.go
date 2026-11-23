package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// askAgainSettleReads bounds how often a workflow run GitHub was asked to
// cancel is read again, waiting for it to end before it is run again, and
// askAgainSettlePause is the pause between those reads.
const (
	askAgainSettleReads = 6
	askAgainSettlePause = 5 * time.Second
)

// workflowRun is one Actions workflow run GitHub holds for a commit.
type workflowRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// AskChecksAgain asks GitHub to run a required check again on a commit where
// it ended without judging the commit, such as skipped or cancelled, or never
// started at all. A check that ended names its job (checkRun), and that job is
// run again. A check GitHub never started names no job, so the workflow runs
// GitHub holds for the commit are run again instead: one that finished
// without passing is re-run, and one stuck before it started is cancelled
// first, which is the only way GitHub offers to free a run that will not
// start, and re-run once GitHub reports it ended. It returns what it asked
// GitHub for, in plain words, and an error saying what GitHub would not do.
func (g GitHub) AskChecksAgain(ctx context.Context, commit string, checkRun int64) (string, error) {
	if checkRun > 0 {
		if err := g.RerunCheck(ctx, checkRun); err != nil {
			return "", err
		}
		return fmt.Sprintf("asked GitHub to run job %d again", checkRun), nil
	}
	commit = strings.ToLower(strings.TrimSpace(commit))
	if !fullCommitPattern.MatchString(commit) {
		return "", fmt.Errorf("commit %q is not a full commit identifier", commit)
	}
	runs, err := g.workflowRuns(ctx, commit)
	if err != nil {
		return "", err
	}
	if len(runs) == 0 {
		return "", fmt.Errorf("GitHub holds no workflow run for %s, so there is none to run again", commit)
	}
	var asked []string
	var refused []error
	for _, run := range runs {
		if strings.EqualFold(run.Status, "completed") && strings.EqualFold(run.Conclusion, "success") {
			continue
		}
		if !strings.EqualFold(run.Status, "completed") {
			if err := g.forceCancel(ctx, run); err != nil {
				refused = append(refused, err)
				continue
			}
		}
		if err := g.rerunWorkflow(ctx, run.ID); err != nil {
			refused = append(refused, err)
			continue
		}
		asked = append(asked, fmt.Sprintf("workflow run %d (%s)", run.ID, nonEmptyName(run.Name)))
	}
	if len(asked) == 0 {
		if len(refused) == 0 {
			return "", fmt.Errorf("every workflow run GitHub holds for %s already passed, so there is none to run again", commit)
		}
		return "", errors.Join(refused...)
	}
	said := "asked GitHub to run " + strings.Join(asked, " and ") + " again"
	if len(refused) > 0 {
		said += fmt.Sprintf("; GitHub would not run the rest again: %v", errors.Join(refused...))
	}
	return said, nil
}

// workflowRuns lists the Actions workflow runs GitHub holds for a commit.
func (g GitHub) workflowRuns(ctx context.Context, commit string) ([]workflowRun, error) {
	what := "ask GitHub for the workflow runs on " + commit
	result, err := g.apiQuery(ctx, "GET", "repos/{owner}/{repo}/actions/runs?head_sha="+commit+"&per_page=50",
		"--jq", "[(.workflow_runs // [])[] | {id, name, status, conclusion}]")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, g.forgeFailure(what, result)
	}
	var runs []workflowRun
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &runs); err != nil {
		return nil, fmt.Errorf("decode the workflow runs on %s: %w", commit, err)
	}
	return runs, nil
}

// forceCancel asks GitHub to end a workflow run that has not finished, and
// reads it again until GitHub reports it ended or the reads run out.
func (g GitHub) forceCancel(ctx context.Context, run workflowRun) error {
	what := fmt.Sprintf("ask GitHub to cancel workflow run %d, which had not finished", run.ID)
	result, err := g.apiMethod(ctx, "POST", fmt.Sprintf("repos/{owner}/{repo}/actions/runs/%d/force-cancel", run.ID))
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if result.Status != execution.ProcessSucceeded {
		return g.forgeFailure(what, result)
	}
	for read := 0; read < askAgainSettleReads; read++ {
		result, err := g.apiQuery(ctx, "GET", fmt.Sprintf("repos/{owner}/{repo}/actions/runs/%d", run.ID), "--jq", ".status")
		if err == nil && result.Status == execution.ProcessSucceeded && strings.EqualFold(strings.TrimSpace(result.Stdout), "completed") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(askAgainSettlePause):
		}
	}
	return fmt.Errorf("GitHub was asked to cancel workflow run %d, which had not finished, and had not ended it after %s", run.ID, askAgainSettlePause*askAgainSettleReads)
}

// rerunWorkflow asks GitHub to run a finished workflow run again.
func (g GitHub) rerunWorkflow(ctx context.Context, runID int64) error {
	what := fmt.Sprintf("ask GitHub to run workflow run %d again", runID)
	result, err := g.apiMethod(ctx, "POST", fmt.Sprintf("repos/{owner}/{repo}/actions/runs/%d/rerun", runID))
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if result.Status != execution.ProcessSucceeded {
		return g.forgeFailure(what, result)
	}
	return nil
}

func nonEmptyName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "unnamed"
	}
	return strings.TrimSpace(name)
}
