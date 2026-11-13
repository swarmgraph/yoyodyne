package runstate

import (
	"errors"
	"fmt"
	"time"
)

// TeamPullRequest is the pull request into the team's branch that an outside
// tool told the harness carries a waiting change, or that a forge check found
// carrying it.
type TeamPullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url,omitempty"`
	// Unverified is set when the tool recorded the request with --unverified:
	// the landing commit was not among the request's commits.
	Unverified bool `json:"unverified,omitempty"`
	// Found is set when a forge check added the request because the landing
	// commit was among its commits, rather than the tool naming it.
	Found     bool      `json:"found,omitempty"`
	TrackedAt time.Time `json:"tracked_at"`
}

// Validate checks the request has a number and a time, and is not both
// unverified and found (a found request is verified by how it was found).
func (p TeamPullRequest) Validate() error {
	var problems []error
	if p.Number <= 0 {
		problems = append(problems, errors.New("pull request number must be positive"))
	}
	if p.TrackedAt.IsZero() {
		problems = append(problems, errors.New("pull request tracked_at is required"))
	}
	if p.Unverified && p.Found {
		problems = append(problems, errors.New("a pull request cannot be both unverified and found"))
	}
	return errors.Join(problems...)
}

// TeamPullRequestTracked records the pull request into the team's branch that
// carries a waiting change. A request already recorded is replaced, which is how
// a change moves from one request to another. Recording the same request again
// as it already stands records nothing.
type TeamPullRequestTracked struct {
	LandingCommit string
	PullRequest   TeamPullRequest
	At            time.Time
}

func (u TeamPullRequestTracked) apply(state *State) (bool, error) {
	if !state.WaitingForTeamMerge() || state.TeamMerge.LandingCommit != u.LandingCommit {
		return false, fmt.Errorf("run %s is not waiting for the team to merge %s, so a pull request is not recorded on it", state.RunID, u.LandingCommit)
	}
	if err := u.PullRequest.Validate(); err != nil {
		return false, fmt.Errorf("run %s cannot record pull request #%d: %w", state.RunID, u.PullRequest.Number, err)
	}
	if have := state.TeamMerge.PullRequest; have != nil && have.Number == u.PullRequest.Number &&
		have.Unverified == u.PullRequest.Unverified && have.Found == u.PullRequest.Found {
		return false, nil
	}
	wait := *state.TeamMerge
	request := u.PullRequest
	wait.PullRequest = &request
	state.TeamMerge = &wait
	state.UpdatedAt = u.At
	return true, nil
}

// TeamPullRequestUntracked takes a pull request off a waiting change, when the
// change is still recorded under that number.
type TeamPullRequestUntracked struct {
	Number int
	At     time.Time
}

func (u TeamPullRequestUntracked) apply(state *State) (bool, error) {
	if !state.WaitingForTeamMerge() || state.TeamMerge.PullRequest == nil || state.TeamMerge.PullRequest.Number != u.Number {
		return false, nil
	}
	wait := *state.TeamMerge
	wait.PullRequest = nil
	state.TeamMerge = &wait
	state.UpdatedAt = u.At
	return true, nil
}

// TeamPullRequestClosed records that the pull request carrying a waiting change
// was closed without merging, and the item was given back to the Lead Product
// Manager.
type TeamPullRequestClosed struct {
	LandingCommit string
	Number        int
	ClosedBy      string
	ClosedAt      time.Time
	At            time.Time
}

func (u TeamPullRequestClosed) apply(state *State) (bool, error) {
	if !state.WaitingForTeamMerge() || state.TeamMerge.LandingCommit != u.LandingCommit {
		return false, fmt.Errorf("run %s is not waiting for the team to merge %s, so the closing of a pull request is not recorded on it", state.RunID, u.LandingCommit)
	}
	if state.TeamMerge.PullRequest == nil || state.TeamMerge.PullRequest.Number != u.Number {
		return false, fmt.Errorf("run %s is not tracked under pull request #%d, so its closing is not recorded on it", state.RunID, u.Number)
	}
	returned := TeamMergeReturned{Number: u.Number, ClosedBy: u.ClosedBy, ClosedAt: u.ClosedAt, At: u.At}
	wait := *state.TeamMerge
	wait.Returned = &returned
	if err := wait.Validate(); err != nil {
		return false, fmt.Errorf("run %s cannot record the closing of pull request #%d: %w", state.RunID, u.Number, err)
	}
	state.TeamMerge = &wait
	state.UpdatedAt = u.At
	return true, nil
}
