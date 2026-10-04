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
	if len(r.PriorFailure) > MaxBlockerBytes || len(r.PriorBlocker) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("retirement history exceeds the %d byte bound", MaxBlockerBytes))
	}
	return errors.Join(problems...)
}
