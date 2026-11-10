package runstate

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// TeamMerge is a run's landed change waiting for the team's merge. A project
// that sets execution.upstream_branch keeps its target branch (say staging) as
// a place work lands, and the team's branch (say main) as where the team
// merges it. A run whose change lands on the target branch therefore does not
// close its item: the item stays claimed, and this records the wait until a
// fetch of the team's branch finds the landing commit on it.
//
// LandingCommit is the commit on the target branch that has to appear on the
// team's branch, and Since is when the wait began. Once a fetch finds it there,
// FoundAt is when and FoundOn is the tip of the team's branch it was read at.
//
// PullRequest is the pull request into the team's branch that an outside tool
// said carries the change. When that pull request's merge ends the wait, FoundBy
// is its number and FoundOn is its merge commit. When it is closed without
// merging, Returned records that the item was given back to the Lead Product
// Manager, and the wait is over without the change reaching the team's branch.
type TeamMerge struct {
	TargetBranch   string     `json:"target_branch"`
	UpstreamBranch string     `json:"upstream_branch"`
	LandingCommit  string     `json:"landing_commit"`
	Since          time.Time  `json:"since"`
	FoundAt        *time.Time `json:"found_at,omitempty"`
	FoundOn        string     `json:"found_on,omitempty"`

	PullRequest *TeamPullRequest   `json:"pull_request,omitempty"`
	FoundBy     int                `json:"found_by_pull_request,omitempty"`
	Returned    *TeamMergeReturned `json:"returned,omitempty"`
}

// TeamMergeReturned records that the tracked pull request was closed without
// merging and the item was given back to the Lead Product Manager.
type TeamMergeReturned struct {
	Number   int       `json:"number"`
	ClosedBy string    `json:"closed_by,omitempty"`
	ClosedAt time.Time `json:"closed_at"`
	At       time.Time `json:"at"`
}

// Found reports a wait that ended in the landing commit appearing on the
// team's branch.
func (t TeamMerge) Found() bool { return t.FoundAt != nil }

// Waiting reports a wait that has not ended: the landing commit has not been
// found on the team's branch and the item has not been given back.
func (t TeamMerge) Waiting() bool { return !t.Found() && t.Returned == nil }

// Says is the wait in the words every surface uses for it.
func (t TeamMerge) Says() string {
	return fmt.Sprintf("landed on %s, waiting for the team to merge it into %s", t.TargetBranch, t.UpstreamBranch)
}

// Validate checks the wait names its branches and landing commit, and that a
// finding carries both when and where it was made.
func (t TeamMerge) Validate() error {
	var problems []error
	if !validLocalBranch(t.TargetBranch) {
		problems = append(problems, errors.New("team_merge target_branch must be a local branch name"))
	}
	if !validLocalBranch(t.UpstreamBranch) {
		problems = append(problems, errors.New("team_merge upstream_branch must be a local branch name"))
	}
	if !commitPattern.MatchString(t.LandingCommit) {
		problems = append(problems, errors.New("team_merge landing_commit is invalid"))
	}
	if t.Since.IsZero() {
		problems = append(problems, errors.New("team_merge since is required"))
	}
	if t.FoundOn != "" && !commitPattern.MatchString(t.FoundOn) {
		problems = append(problems, errors.New("team_merge found_on is invalid"))
	}
	if t.FoundAt != nil && t.FoundOn == "" {
		problems = append(problems, errors.New("team_merge found_at requires found_on"))
	}
	if t.FoundAt == nil && t.FoundOn != "" {
		problems = append(problems, errors.New("team_merge found_on requires found_at"))
	}
	if t.PullRequest != nil {
		if err := t.PullRequest.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("team_merge pull_request: %w", err))
		}
	}
	if t.FoundBy < 0 || (t.FoundBy > 0 && t.FoundAt == nil) {
		problems = append(problems, errors.New("team_merge found_by_pull_request requires found_at"))
	}
	if t.Returned != nil {
		if t.Returned.Number <= 0 {
			problems = append(problems, errors.New("team_merge returned number must be positive"))
		}
		if t.Returned.ClosedAt.IsZero() || t.Returned.At.IsZero() {
			problems = append(problems, errors.New("team_merge returned requires closed_at and at"))
		}
		if t.FoundAt != nil {
			problems = append(problems, errors.New("team_merge cannot be both found and returned"))
		}
	}
	return errors.Join(problems...)
}

// WaitingForTeamMerge reports a run whose change landed on the target branch
// and waits for the team to merge it into theirs. Such a run is over and holds
// no developer slot, and it holds its item's claim until a fetch finds the
// landing commit on the team's branch.
func (s State) WaitingForTeamMerge() bool {
	return s.TeamMerge != nil && s.TeamMerge.Waiting() && s.Retirement == nil &&
		s.Status == StatusSucceeded && s.Integration != nil
}

// WaitingForTeamMerge lists every run waiting for the team's merge. The index
// chooses the records (runindex.go); a record it could not read is left out
// rather than refusing the listing, as WaitingForPersonMerge does.
func (s *Store) WaitingForTeamMerge() ([]State, error) {
	return s.indexed("waiting for the team to merge", func(entry runIndexEntry) bool { return entry.TeamMerge },
		func(unreadableRun) bool { return false }, func(state State) bool { return state.WaitingForTeamMerge() })
}

// TeamMergeFound records a wait ended by a fetch finding the landing commit on
// the team's branch: when, and the tip of the team's branch it was read at.
type TeamMergeFound struct {
	LandingCommit string
	FoundOn       string
	// PullRequest, when set, is the tracked pull request whose merge ended the
	// wait; FoundOn is then its merge commit.
	PullRequest int
	At          time.Time
}

func (u TeamMergeFound) apply(state *State) (bool, error) {
	if !state.WaitingForTeamMerge() || state.TeamMerge.LandingCommit != u.LandingCommit {
		return false, fmt.Errorf("run %s is not waiting for the team to merge %s, so finding it on the team's branch is not recorded on it", state.RunID, u.LandingCommit)
	}
	if u.PullRequest != 0 && (state.TeamMerge.PullRequest == nil || state.TeamMerge.PullRequest.Number != u.PullRequest) {
		return false, fmt.Errorf("run %s is not tracked under pull request #%d, so its merge is not recorded on it", state.RunID, u.PullRequest)
	}
	wait := *state.TeamMerge
	wait.FoundBy = u.PullRequest
	at := u.At
	wait.FoundAt = &at
	wait.FoundOn = u.FoundOn
	state.TeamMerge = &wait
	state.UpdatedAt = u.At
	return true, nil
}

// WaitingForTeamMergeItem is one item whose change waits for the team's merge.
type WaitingForTeamMergeItem struct {
	WorkItemID     string    `json:"work_item_id"`
	Title          string    `json:"title,omitempty"`
	RunID          string    `json:"run_id"`
	TargetBranch   string    `json:"target_branch"`
	UpstreamBranch string    `json:"upstream_branch"`
	LandingCommit  string    `json:"landing_commit"`
	Since          time.Time `json:"since"`

	PullRequest    int    `json:"pull_request,omitempty"`
	PullRequestURL string `json:"pull_request_url,omitempty"`
	Unverified     bool   `json:"unverified,omitempty"`
}

// WaitingForTeamMergeOf keeps only the runs waiting for the team's merge, one
// per work item (the latest wait wins), oldest wait first and then by work item.
func WaitingForTeamMergeOf(states []State) []WaitingForTeamMergeItem {
	latest := map[string]WaitingForTeamMergeItem{}
	for _, state := range states {
		if !state.WaitingForTeamMerge() {
			continue
		}
		item := WaitingForTeamMergeItem{
			WorkItemID:     state.WorkItemID,
			Title:          strings.TrimSpace(state.WorkItemTitle),
			RunID:          state.RunID,
			TargetBranch:   state.TeamMerge.TargetBranch,
			UpstreamBranch: state.TeamMerge.UpstreamBranch,
			LandingCommit:  state.TeamMerge.LandingCommit,
			Since:          state.TeamMerge.Since,
		}
		if pr := state.TeamMerge.PullRequest; pr != nil {
			item.PullRequest = pr.Number
			item.PullRequestURL = pr.URL
			item.Unverified = pr.Unverified
		}
		if earlier, ok := latest[item.WorkItemID]; ok && earlier.Since.After(item.Since) {
			continue
		}
		latest[item.WorkItemID] = item
	}
	items := make([]WaitingForTeamMergeItem, 0, len(latest))
	for _, item := range latest {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].Since.Equal(items[j].Since) {
			return items[i].Since.Before(items[j].Since)
		}
		return items[i].WorkItemID < items[j].WorkItemID
	})
	return items
}

// Says is the item by title and id, with its landing commit shortened.
func (w WaitingForTeamMergeItem) Says() string {
	commit := w.LandingCommit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	name := w.WorkItemID
	if w.Title != "" {
		name = fmt.Sprintf("%s (%s)", w.Title, w.WorkItemID)
	}
	carried := "no pull request carries it yet"
	if w.PullRequest > 0 {
		carried = fmt.Sprintf("pull request #%d carries it", w.PullRequest)
		if w.Unverified {
			carried += " (its landing commit was not found among the pull request's commits)"
		}
	}
	return fmt.Sprintf("%s: landed on %s as %s, waiting for the team to merge it into %s; %s", name, w.TargetBranch, commit, w.UpstreamBranch, carried)
}
