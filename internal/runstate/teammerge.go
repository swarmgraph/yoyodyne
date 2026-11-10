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
type TeamMerge struct {
	TargetBranch   string     `json:"target_branch"`
	UpstreamBranch string     `json:"upstream_branch"`
	LandingCommit  string     `json:"landing_commit"`
	Since          time.Time  `json:"since"`
	FoundAt        *time.Time `json:"found_at,omitempty"`
	FoundOn        string     `json:"found_on,omitempty"`
}

// Found reports a wait that ended in the landing commit appearing on the
// team's branch.
func (t TeamMerge) Found() bool { return t.FoundAt != nil }

// Waiting reports a wait that has not ended.
func (t TeamMerge) Waiting() bool { return !t.Found() }

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
	At            time.Time
}

func (u TeamMergeFound) apply(state *State) (bool, error) {
	if !state.WaitingForTeamMerge() || state.TeamMerge.LandingCommit != u.LandingCommit {
		return false, fmt.Errorf("run %s is not waiting for the team to merge %s, so finding it on the team's branch is not recorded on it", state.RunID, u.LandingCommit)
	}
	wait := *state.TeamMerge
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
	return fmt.Sprintf("%s: landed on %s as %s, waiting for the team to merge it into %s", name, w.TargetBranch, commit, w.UpstreamBranch)
}
