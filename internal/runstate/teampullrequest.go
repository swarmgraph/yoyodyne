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
	// Feedback is how far the forge checks have read the reviews and comments
	// on the request, and the approvals people gave there, kept as information.
	// Every run the request carries keeps its own copy, moved together by the
	// check that acted on what it read (TeamPullRequestFeedbackRead).
	Feedback *PullRequestFeedback `json:"feedback,omitempty"`
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
	if p.Feedback != nil {
		if err := p.Feedback.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("pull request feedback: %w", err))
		}
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
	// The same request recorded again keeps what the checks have read of it, so
	// changing how it is recorded never acts on its feedback a second time.
	if have := state.TeamMerge.PullRequest; have != nil && have.Number == request.Number && request.Feedback == nil {
		request.Feedback = have.Feedback
	}
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

// TeamPullRequestFeedbackRead moves a waiting change's copy of the feedback
// cursor of the pull request that carries it past what a forge check acted on,
// and keeps the approvals among it as information. Nothing it records starts or
// stops anything: what was asked for went on the development manager's list
// before this was written.
type TeamPullRequestFeedbackRead struct {
	LandingCommit string
	Number        int
	Read          []ForgeFeedbackItem
	At            time.Time
}

func (u TeamPullRequestFeedbackRead) apply(state *State) (bool, error) {
	if !state.WaitingForTeamMerge() || state.TeamMerge.LandingCommit != u.LandingCommit {
		return false, fmt.Errorf("run %s is not waiting for the team to merge %s, so what was read of pull request #%d is not recorded on it", state.RunID, u.LandingCommit, u.Number)
	}
	if state.TeamMerge.PullRequest == nil || state.TeamMerge.PullRequest.Number != u.Number {
		return false, fmt.Errorf("run %s is not tracked under pull request #%d, so what was read of it is not recorded on it", state.RunID, u.Number)
	}
	if len(u.Read) == 0 {
		return false, nil
	}
	request := *state.TeamMerge.PullRequest
	feedback := PullRequestFeedback{}
	if request.Feedback != nil {
		feedback = *request.Feedback
		feedback.ThroughIDs = append([]string(nil), feedback.ThroughIDs...)
		feedback.Approvals = append([]ForgeFeedbackItem(nil), feedback.Approvals...)
	}
	for _, item := range u.Read {
		if err := item.Validate(); err != nil {
			return false, fmt.Errorf("run %s cannot record feedback %s on pull request #%d: %w", state.RunID, item.ID, u.Number, err)
		}
		if item.Kind == FeedbackApproval {
			feedback.RecordApproval(item)
		}
	}
	feedback.Advance(u.Read)
	feedback.ReadAt = u.At
	feedback.ReadError = ""
	request.Feedback = &feedback
	wait := *state.TeamMerge
	wait.PullRequest = &request
	state.TeamMerge = &wait
	state.UpdatedAt = u.At
	return true, nil
}
