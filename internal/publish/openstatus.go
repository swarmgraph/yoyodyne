package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// OpenPullRequestStatus is one open pull request as the factory-flow program
// manager's pass is shown it: what ListOpen reports, and how old it is, whether
// the forge says it can merge, and the state of its checks.
type OpenPullRequestStatus struct {
	PullRequest
	Title     string    `json:"title,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Draft     bool      `json:"draft,omitempty"`
	// Mergeable is the forge's own word for whether the request's branch merges
	// into its base without conflict: MERGEABLE, CONFLICTING, or UNKNOWN while
	// the forge has not worked it out yet.
	Mergeable string `json:"mergeable,omitempty"`
	// MergeState is the forge's word for what stands between the request and a
	// merge (CLEAN, BLOCKED, BEHIND, DIRTY, UNSTABLE, ...); MergeRequirement says
	// it in words.
	MergeState string        `json:"merge_state,omitempty"`
	Checks     ChecksSummary `json:"checks"`
}

// ChecksSummary is the state of a request's checks, counted from what the forge
// reports for its head commit.
type ChecksSummary struct {
	Passed  int `json:"passed"`
	Pending int `json:"pending"`
	// Failed names each check that failed, in name order.
	Failed []string `json:"failed,omitempty"`
}

// Total is how many checks the forge reported.
func (c ChecksSummary) Total() int { return c.Passed + c.Pending + len(c.Failed) }

// Says is the state of the checks in words.
func (c ChecksSummary) Says() string {
	switch {
	case c.Total() == 0:
		return "no checks reported"
	case len(c.Failed) > 0:
		return fmt.Sprintf("%d of %d failed (%s)", len(c.Failed), c.Total(), strings.Join(c.Failed, ", "))
	case c.Pending > 0:
		return fmt.Sprintf("%d of %d still running", c.Pending, c.Total())
	default:
		return fmt.Sprintf("all %d passed", c.Total())
	}
}

// ListOpenStatus is ListOpen with each request's age, mergeability, and checks,
// read in the same single listing and under the same bound. It is the reading
// the factory-flow program manager's pass is handed, which roles cannot take
// themselves because no role reaches the forge.
func (g GitHub) ListOpenStatus(ctx context.Context) ([]OpenPullRequestStatus, error) {
	scope, err := g.repoArgs(ctx)
	if err != nil {
		return nil, err
	}
	result, err := g.exec(ctx, append([]string{"pr", "list"}, append(scope,
		"--state", "open",
		"--limit", strconv.Itoa(maxListedOpenRequests),
		"--json", "number,url,title,headRefName,baseRefName,headRefOid,createdAt,isDraft,mergeable,mergeStateStatus,statusCheckRollup")...)...)
	if err != nil {
		return nil, fmt.Errorf("list open pull requests with their checks: %w", err)
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list open pull requests with their checks failed with exit code %d: %s", result.ExitCode, g.redact(strings.TrimSpace(result.Stderr)))
	}
	var reported []struct {
		Number            int             `json:"number"`
		URL               string          `json:"url"`
		Title             string          `json:"title"`
		HeadRefName       string          `json:"headRefName"`
		BaseRefName       string          `json:"baseRefName"`
		HeadRefOid        string          `json:"headRefOid"`
		CreatedAt         time.Time       `json:"createdAt"`
		IsDraft           bool            `json:"isDraft"`
		Mergeable         string          `json:"mergeable"`
		MergeStateStatus  string          `json:"mergeStateStatus"`
		StatusCheckRollup []checkReported `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &reported); err != nil {
		return nil, fmt.Errorf("decode open pull requests with their checks: %w", err)
	}
	open := make([]OpenPullRequestStatus, 0, len(reported))
	for _, one := range reported {
		if one.Number <= 0 {
			return nil, errors.New("an open pull request reported no number")
		}
		open = append(open, OpenPullRequestStatus{
			PullRequest: PullRequest{
				Number:     one.Number,
				URL:        one.URL,
				State:      "OPEN",
				HeadCommit: strings.TrimSpace(one.HeadRefOid),
				HeadBranch: strings.TrimSpace(one.HeadRefName),
				BaseBranch: strings.TrimSpace(one.BaseRefName),
			},
			Title:      strings.TrimSpace(one.Title),
			CreatedAt:  one.CreatedAt,
			Draft:      one.IsDraft,
			Mergeable:  strings.TrimSpace(one.Mergeable),
			MergeState: strings.TrimSpace(one.MergeStateStatus),
			Checks:     summarizeChecks(one.StatusCheckRollup),
		})
	}
	return open, nil
}

// checkReported is one entry of the forge's check rollup: a check run, which
// reports a status and, once complete, a conclusion; or a commit status, which
// reports a state.
type checkReported struct {
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

func summarizeChecks(checks []checkReported) ChecksSummary {
	var summary ChecksSummary
	for _, check := range checks {
		name := strings.TrimSpace(check.Name)
		if name == "" {
			name = strings.TrimSpace(check.Context)
		}
		if name == "" {
			name = "an unnamed check"
		}
		switch checkOutcome(check) {
		case "passed":
			summary.Passed++
		case "failed":
			summary.Failed = append(summary.Failed, name)
		default:
			summary.Pending++
		}
	}
	sort.Strings(summary.Failed)
	return summary
}

// checkOutcome reads one check as passed, failed, or still pending. A check
// skipped or judged neutral passes nothing and blocks nothing, so it is counted
// with the passed.
func checkOutcome(check checkReported) string {
	if state := strings.ToUpper(strings.TrimSpace(check.State)); state != "" {
		switch state {
		case "SUCCESS":
			return "passed"
		case "FAILURE", "ERROR":
			return "failed"
		default:
			return "pending"
		}
	}
	if !strings.EqualFold(strings.TrimSpace(check.Status), "COMPLETED") {
		return "pending"
	}
	switch strings.ToUpper(strings.TrimSpace(check.Conclusion)) {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return "passed"
	default:
		return "failed"
	}
}
