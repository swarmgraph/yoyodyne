package runstate

// The development manager's decision to repair an item whose change waits on a
// pull request the harness opened, for a person to merge
// (docs/designs/integrating-to-a-local-branch.md, section 2, "Deciding a repair
// of an item whose run has ended").
//
// It is the same kind of decision as the one for a tracked pull request
// (TeamFeedbackRepair), kept on the wait of the run that published the request
// (PersonMerge.Repairs) for the reasons that one is kept on the team's wait: the
// run that published it ended, so no stopped run holds a decision, and a decision
// is refused once the wait ends.

import (
	"fmt"
	"time"
)

// Repair is the decision answering one piece of feedback on this request, and
// whether this wait holds one.
func (p PersonMerge) Repair(number int, feedbackID string) (TeamFeedbackRepair, bool) {
	for _, repair := range p.Repairs {
		if repair.PullRequest == number && repair.FeedbackID == feedbackID {
			return repair, true
		}
	}
	return TeamFeedbackRepair{}, false
}

// RepairsOwed lists the decisions recorded that no pass has taken to carry out.
func (p PersonMerge) RepairsOwed() []TeamFeedbackRepair {
	var owed []TeamFeedbackRepair
	for _, repair := range p.Repairs {
		if !repair.Started() && repair.TakenAt == nil {
			owed = append(owed, repair)
		}
	}
	return owed
}

// RepairsTaken lists the decisions a pass took whose new run has not been
// recorded: one still running, or one that was interrupted.
func (p PersonMerge) RepairsTaken() []TeamFeedbackRepair {
	var taken []TeamFeedbackRepair
	for _, repair := range p.Repairs {
		if !repair.Started() && repair.TakenAt != nil {
			taken = append(taken, repair)
		}
	}
	return taken
}

// PersonMergeRepairDecided records a decision on the run that published the
// request. It is refused when the run is no longer waiting for a person to merge
// that request, which is how a decision about an item that has closed is
// refused, and when the entry already has a decision on this run.
type PersonMergeRepairDecided struct {
	Repair TeamFeedbackRepair
}

func (u PersonMergeRepairDecided) apply(state *State) (bool, error) {
	if !state.WaitingForPersonMerge() || state.PersonMerge.Number != u.Repair.PullRequest {
		return false, fmt.Errorf("run %s is not waiting for a person to merge pull request #%d, so a repair from feedback is not recorded on it", state.RunID, u.Repair.PullRequest)
	}
	if state.WorkItemID != u.Repair.WorkItemID {
		return false, fmt.Errorf("run %s carried %s, not %s", state.RunID, state.WorkItemID, u.Repair.WorkItemID)
	}
	if _, answered := state.PersonMerge.Repair(u.Repair.PullRequest, u.Repair.FeedbackID); answered {
		return false, fmt.Errorf("feedback %s on pull request #%d is already answered on run %s", u.Repair.FeedbackID, u.Repair.PullRequest, state.RunID)
	}
	wait := *state.PersonMerge
	wait.Repairs = append(append([]TeamFeedbackRepair(nil), wait.Repairs...), u.Repair)
	if err := wait.Validate(); err != nil {
		return false, fmt.Errorf("run %s cannot record the repair: %w", state.RunID, err)
	}
	state.PersonMerge = &wait
	state.UpdatedAt = u.Repair.DecidedAt
	return true, nil
}

// PersonMergeRepairTaken records that a pass took the decision to carry it out.
// It is refused when the decision is already taken or started, which is what
// keeps two passes from starting two runs: it is made under the run's write lock.
type PersonMergeRepairTaken struct {
	PullRequest int
	FeedbackID  string
	At          time.Time
}

func (u PersonMergeRepairTaken) apply(state *State) (bool, error) {
	return changePersonMergeRepair(state, u.PullRequest, u.FeedbackID, u.At, func(repair *TeamFeedbackRepair) error {
		if repair.Started() || repair.TakenAt != nil {
			return fmt.Errorf("the repair answering %s on pull request #%d is already taken", u.FeedbackID, u.PullRequest)
		}
		at := u.At
		repair.TakenAt = &at
		return nil
	})
}

// PersonMergeRepairWithdrawn gives a taken decision back where the new run was
// refused before it existed, so a later pass carries it out.
type PersonMergeRepairWithdrawn struct {
	PullRequest int
	FeedbackID  string
	At          time.Time
}

func (u PersonMergeRepairWithdrawn) apply(state *State) (bool, error) {
	return changePersonMergeRepair(state, u.PullRequest, u.FeedbackID, u.At, func(repair *TeamFeedbackRepair) error {
		if repair.Started() {
			return fmt.Errorf("the repair answering %s on pull request #%d already started run %s", u.FeedbackID, u.PullRequest, repair.StartedRunID)
		}
		repair.TakenAt = nil
		return nil
	})
}

// PersonMergeRepairStarted records the new run a decision started. Recording it
// again for the same run changes nothing.
type PersonMergeRepairStarted struct {
	PullRequest int
	FeedbackID  string
	RunID       string
	At          time.Time
}

func (u PersonMergeRepairStarted) apply(state *State) (bool, error) {
	return changePersonMergeRepair(state, u.PullRequest, u.FeedbackID, u.At, func(repair *TeamFeedbackRepair) error {
		if repair.StartedRunID == u.RunID {
			return errNoChange
		}
		if repair.Started() {
			return fmt.Errorf("the repair answering %s on pull request #%d already started run %s", u.FeedbackID, u.PullRequest, repair.StartedRunID)
		}
		at := u.At
		repair.StartedRunID, repair.StartedAt = u.RunID, &at
		return nil
	})
}

// PersonMergeContinued hands the wait to the run that carried a decided repair
// onto the request's own branch, once that run has published to the request. It
// changes nothing where the same run already holds it.
type PersonMergeContinued struct {
	PullRequest int
	RunID       string
	At          time.Time
}

func (u PersonMergeContinued) apply(state *State) (bool, error) {
	if state.PersonMerge == nil || state.PersonMerge.Number != u.PullRequest {
		return false, fmt.Errorf("run %s did not wait on pull request #%d, so it is not handed on from it", state.RunID, u.PullRequest)
	}
	if state.PersonMerge.ContinuedBy == u.RunID {
		return false, nil
	}
	if state.PersonMerge.Merged() || state.PersonMerge.Returned() || state.PersonMerge.ContinuedBy != "" {
		return false, fmt.Errorf("run %s's wait on pull request #%d has already ended", state.RunID, u.PullRequest)
	}
	wait := *state.PersonMerge
	at := u.At
	wait.ContinuedBy, wait.ContinuedAt = u.RunID, &at
	if err := wait.Validate(); err != nil {
		return false, err
	}
	state.PersonMerge = &wait
	state.UpdatedAt = u.At
	return true, nil
}

// errNoChange is what a change function returns to say the record already says
// what it was asked to say.
var errNoChange = fmt.Errorf("already recorded")

func changePersonMergeRepair(state *State, number int, feedbackID string, at time.Time, change func(*TeamFeedbackRepair) error) (bool, error) {
	if state.PersonMerge == nil || state.PersonMerge.Number != number {
		return false, fmt.Errorf("run %s did not wait on pull request #%d, so no repair of it is recorded on it", state.RunID, number)
	}
	wait := *state.PersonMerge
	wait.Repairs = append([]TeamFeedbackRepair(nil), wait.Repairs...)
	for index := range wait.Repairs {
		if wait.Repairs[index].PullRequest != number || wait.Repairs[index].FeedbackID != feedbackID {
			continue
		}
		if err := change(&wait.Repairs[index]); err != nil {
			if err == errNoChange {
				return false, nil
			}
			return false, err
		}
		if err := wait.Validate(); err != nil {
			return false, err
		}
		state.PersonMerge = &wait
		state.UpdatedAt = at
		return true, nil
	}
	return false, fmt.Errorf("run %s holds no repair answering %s on pull request #%d", state.RunID, feedbackID, number)
}
