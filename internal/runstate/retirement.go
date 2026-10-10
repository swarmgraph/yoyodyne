package runstate

import (
	"errors"
	"fmt"
	"time"
)

// RunRetirement records why another run's confirmed publication ended this
// run's remaining work. Its artifacts and execution history remain available.
type RunRetirement struct {
	RunID            string     `json:"run_id"`
	Commit           string     `json:"commit"`
	TargetBranch     string     `json:"target_branch"`
	Number           int        `json:"number"`
	At               time.Time  `json:"at"`
	NotedAt          *time.Time `json:"noted_at,omitempty"`
	PriorStatus      Status     `json:"prior_status"`
	PriorCompletedAt *time.Time `json:"prior_completed_at,omitempty"`
	PriorFailure     string     `json:"prior_failure,omitempty"`
	PriorBlocker     string     `json:"prior_blocker,omitempty"`
	PriorWait        string     `json:"prior_wait,omitempty"`
}

func (r RunRetirement) Validate(runID string) error {
	var problems []error
	if !ValidRunID(r.RunID) || r.RunID == runID {
		problems = append(problems, errors.New("retirement must name another run"))
	}
	if !commitPattern.MatchString(r.Commit) || !validLocalBranch(r.TargetBranch) || r.Number <= 0 || r.At.IsZero() {
		problems = append(problems, errors.New("retirement requires the confirmed merge, target, pull request, and time"))
	}
	if !r.PriorStatus.Valid() {
		problems = append(problems, errors.New("retirement requires the previous status"))
	}
	if r.NotedAt != nil && (r.NotedAt.IsZero() || r.NotedAt.Before(r.At)) {
		problems = append(problems, errors.New("retirement note cannot precede retirement"))
	}
	if len(r.PriorFailure) > MaxBlockerBytes || len(r.PriorBlocker) > MaxBlockerBytes || len(r.PriorWait) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("retirement history exceeds the %d byte bound", MaxBlockerBytes))
	}
	return errors.Join(problems...)
}

// MergedAndComplete reports a run that succeeded and finished, whose own change
// merged through its own pull request at a recorded merge commit, with nothing
// about its publication, cleanup or landing left outstanding. It is the one
// reading of "this run's work landed" that retiring an older run, finding an
// older recovery no longer applies, and saying a run stopped all ask, so none of
// them can call one run finished and stopped at once. It says nothing about the
// item's acceptance criteria.
func (s State) MergedAndComplete() bool {
	p := s.PullRequest
	return s.Status == StatusSucceeded && s.Phase == PhaseComplete &&
		s.Integration != nil && p != nil && p.Merged && p.MergeCommit != "" && p.HeadCommit == s.Integration.SourceCommit &&
		p.Superseded == "" && p.HandedBack == nil && !s.Outstanding() && s.PublishFailure == "" && s.CleanupFailure == "" &&
		(s.LandingOutcome == "" || s.LandingOutcome == LandingDischarged) && s.LandingProblem == ""
}
