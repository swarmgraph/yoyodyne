package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// forgeCommitListLimit is the most commits the forge lists for one pull request.
const forgeCommitListLimit = 250

// TeamPullRequest is one pull request read by its number: where it points, what
// state it is in, and the commits it carries. It is read to settle which waiting
// changes a pull request into the team's branch carries, so it is read fresh
// each time and never through the conditional ResponseCache.
type TeamPullRequest struct {
	Number     int
	URL        string
	State      string // OPEN, CLOSED or MERGED
	Merged     bool
	BaseBranch string
	HeadBranch string
	HeadCommit string
	// MergeCommit is the forge's merge_commit_sha, only meaningful when Merged.
	MergeCommit string
	// Commits is every commit the request carries, oldest first.
	Commits []string
	// CommitsCapped is set when the forge lists at most 250 commits of a request
	// and this one has at least that many, so a commit not found in Commits may
	// still be in the request.
	CommitsCapped bool
}

// TeamPullRequest reads the pull request with this number and the commits it
// carries.
func (g GitHub) TeamPullRequest(ctx context.Context, number int) (TeamPullRequest, error) {
	if number <= 0 {
		return TeamPullRequest{}, fmt.Errorf("pull request number %d is not a request", number)
	}
	what := fmt.Sprintf("ask the forge about pull request %d", number)
	endpoint := fmt.Sprintf("repos/{owner}/{repo}/pulls/%d", number)
	request, err := g.apiQuery(ctx, "GET", endpoint, "--jq",
		"{state: .state, merged: .merged, base: .base.ref, head: .head.ref, head_sha: .head.sha, merge_commit: .merge_commit_sha, url: .html_url, commits: .commits}")
	if err != nil {
		return TeamPullRequest{}, fmt.Errorf("%s: %w", what, err)
	}
	if request.Status != execution.ProcessSucceeded {
		if strings.Contains(strings.ToLower(request.Stderr), "http 404") {
			return TeamPullRequest{}, fmt.Errorf("pull request %d does not exist in this repository", number)
		}
		return TeamPullRequest{}, g.forgeFailure(what, request)
	}
	var body struct {
		State       string `json:"state"`
		Merged      bool   `json:"merged"`
		Base        string `json:"base"`
		Head        string `json:"head"`
		HeadSHA     string `json:"head_sha"`
		MergeCommit string `json:"merge_commit"`
		URL         string `json:"url"`
		Commits     int    `json:"commits"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(request.Stdout)), &body); err != nil {
		return TeamPullRequest{}, fmt.Errorf("decode pull request %d: %w", number, err)
	}
	reading := TeamPullRequest{
		Number:      number,
		URL:         body.URL,
		State:       strings.ToUpper(strings.TrimSpace(body.State)),
		Merged:      body.Merged,
		BaseBranch:  body.Base,
		HeadBranch:  body.Head,
		HeadCommit:  strings.TrimSpace(body.HeadSHA),
		MergeCommit: strings.TrimSpace(body.MergeCommit),
	}
	if body.Merged {
		reading.State = "MERGED"
	}

	listed, err := g.apiQuery(ctx, "GET", endpoint+"/commits", "--paginate", "-F", "per_page=100", "--jq", ".[].sha")
	if err != nil {
		return TeamPullRequest{}, fmt.Errorf("ask the forge for the commits of pull request %d: %w", number, err)
	}
	if listed.Status != execution.ProcessSucceeded {
		return TeamPullRequest{}, g.forgeFailure(fmt.Sprintf("ask the forge for the commits of pull request %d", number), listed)
	}
	for _, line := range strings.Split(listed.Stdout, "\n") {
		sha := strings.TrimSpace(line)
		if sha == "" {
			continue
		}
		if !commitPattern.MatchString(sha) {
			return TeamPullRequest{}, fmt.Errorf("the forge named a commit of pull request %d that is not a commit", number)
		}
		reading.Commits = append(reading.Commits, sha)
	}
	reading.CommitsCapped = body.Commits >= forgeCommitListLimit || body.Commits > len(reading.Commits)
	return reading, nil
}
