package runstate

// The development manager's decision to repair an item whose run has ended, from
// feedback on a pull request into the team's branch that the harness tracks
// (docs/designs/integrating-to-a-local-branch.md, section 2, "Deciding a repair
// of an item whose run has ended").
//
// The decision cannot sit on a stopped run's triage record, because the run that
// landed the item ended as integrated and nothing stopped. It is bound to the
// list entry it answers instead: the pull request and the piece of feedback, as
// the report on her list names them. It is kept on the wait of the run that
// landed the item (TeamMerge.Repairs), beside the pull request the feedback is
// about, so that it goes where the run's record goes and is refused once the wait
// ends. A repair carried out starts a new run, and the decision records which.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The bounds one decision is held to, as a triage decision's are.
const (
	MaxTeamFeedbackRepairReasonBytes = MaxTriageDecisionReasonBytes
	// MaxTeamFeedbackRepairs bounds the decisions one landed run carries. An entry
	// is answered once, so reaching it takes that many pieces of feedback on one
	// change.
	MaxTeamFeedbackRepairs = 64
)

// TeamFeedbackRepair is one decision to repair one item from one piece of
// feedback on a tracked pull request.
type TeamFeedbackRepair struct {
	// PullRequest and FeedbackID name the list entry the decision answers.
	PullRequest int    `json:"pull_request"`
	FeedbackID  string `json:"feedback_id"`
	// Feedback is the feedback as it was read, which is what the new run's
	// developer is given as evidence.
	Feedback ForgeFeedbackItem `json:"feedback"`
	// WorkItemID is the one item she decided to repair.
	WorkItemID string `json:"work_item_id"`
	// Reason is her reasoning, verbatim. DecidedBy, Conversation and Turn say who
	// recorded it and where, so the new run's record can be traced back to it.
	Reason       string    `json:"reason"`
	DecidedBy    string    `json:"decided_by"`
	Conversation string    `json:"conversation"`
	Turn         int       `json:"turn"`
	DecidedAt    time.Time `json:"decided_at"`
	// StartedRunID is the new run the decision started, and StartedAt when. Both
	// are empty until the harness carries the decision out.
	StartedRunID string     `json:"started_run,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
}

// Started reports a decision the harness has carried out.
func (r TeamFeedbackRepair) Started() bool { return r.StartedRunID != "" }

// Validate reports every contract violation in the decision at once.
func (r TeamFeedbackRepair) Validate() error {
	var problems []error
	if r.PullRequest <= 0 {
		problems = append(problems, errors.New("a repair from feedback names the pull request the feedback is on"))
	}
	if !feedbackIDPattern.MatchString(r.FeedbackID) {
		problems = append(problems, fmt.Errorf("%q is not a forge review or comment identifier", r.FeedbackID))
	}
	if err := r.Feedback.Validate(); err != nil {
		problems = append(problems, fmt.Errorf("feedback: %w", err))
	} else if r.Feedback.ID != r.FeedbackID {
		problems = append(problems, fmt.Errorf("feedback %s is not the feedback %s the decision answers", r.Feedback.ID, r.FeedbackID))
	}
	if strings.TrimSpace(r.WorkItemID) == "" {
		problems = append(problems, errors.New("a repair from feedback names the one item it repairs"))
	}
	switch reason := strings.TrimSpace(r.Reason); {
	case reason == "":
		problems = append(problems, errors.New("a repair from feedback records the reasoning it was made on"))
	case len(reason) > MaxTeamFeedbackRepairReasonBytes:
		problems = append(problems, fmt.Errorf("the reason is %d bytes, limit is %d", len(reason), MaxTeamFeedbackRepairReasonBytes))
	}
	if strings.TrimSpace(r.DecidedBy) == "" || strings.TrimSpace(r.Conversation) == "" {
		problems = append(problems, errors.New("a repair from feedback names the role and the conversation that recorded it"))
	}
	if r.Turn < 0 {
		problems = append(problems, fmt.Errorf("turn %d is not a turn", r.Turn))
	}
	if r.DecidedAt.IsZero() {
		problems = append(problems, errors.New("a repair from feedback records when it was decided"))
	}
	if (r.StartedRunID == "") != (r.StartedAt == nil) {
		problems = append(problems, errors.New("a started repair records the run it started and when, together"))
	}
	if r.StartedRunID != "" && !ValidRunID(r.StartedRunID) {
		problems = append(problems, fmt.Errorf("%q is not a run identifier", r.StartedRunID))
	}
	return errors.Join(problems...)
}

// Cite names the record, for the reason a run starts from it and so that a
// reader can follow the chain from the team's feedback to the change it caused.
func (r TeamFeedbackRepair) Cite() string {
	return fmt.Sprintf("recorded by the %s in conversation %s after turn %d, at %s, answering %s on pull request #%d",
		strings.TrimSpace(r.DecidedBy), strings.TrimSpace(r.Conversation), r.Turn, r.DecidedAt.UTC().Format(time.RFC3339), r.FeedbackID, r.PullRequest)
}

// Repair is the decision answering one piece of feedback on one pull request,
// and whether this run's wait holds one.
func (t TeamMerge) Repair(number int, feedbackID string) (TeamFeedbackRepair, bool) {
	for _, repair := range t.Repairs {
		if repair.PullRequest == number && repair.FeedbackID == feedbackID {
			return repair, true
		}
	}
	return TeamFeedbackRepair{}, false
}

// RepairsOwed lists the decisions recorded and not yet carried out.
func (t TeamMerge) RepairsOwed() []TeamFeedbackRepair {
	var owed []TeamFeedbackRepair
	for _, repair := range t.Repairs {
		if !repair.Started() {
			owed = append(owed, repair)
		}
	}
	return owed
}

func validateTeamFeedbackRepairs(repairs []TeamFeedbackRepair) []error {
	var problems []error
	if len(repairs) > MaxTeamFeedbackRepairs {
		problems = append(problems, fmt.Errorf("team_merge carries %d repairs, which exceeds the bound of %d", len(repairs), MaxTeamFeedbackRepairs))
	}
	seen := map[string]bool{}
	for index, repair := range repairs {
		if err := repair.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("team_merge repairs[%d]: %w", index, err))
			continue
		}
		key := fmt.Sprintf("%d/%s", repair.PullRequest, repair.FeedbackID)
		if seen[key] {
			problems = append(problems, fmt.Errorf("team_merge repairs[%d]: feedback %s on pull request #%d is already answered", index, repair.FeedbackID, repair.PullRequest))
		}
		seen[key] = true
	}
	return problems
}

// TeamFeedbackRepairDecided records a decision on the run that landed the item.
// It is refused when the run is no longer waiting for the team's merge, which is
// how a decision about an item that has closed is refused, and when the entry
// already has a decision on this run.
type TeamFeedbackRepairDecided struct {
	LandingCommit string
	Repair        TeamFeedbackRepair
}

func (u TeamFeedbackRepairDecided) apply(state *State) (bool, error) {
	if !state.WaitingForTeamMerge() || state.TeamMerge.LandingCommit != u.LandingCommit {
		return false, fmt.Errorf("run %s is not waiting for the team to merge %s, so a repair from feedback is not recorded on it", state.RunID, u.LandingCommit)
	}
	if state.TeamMerge.PullRequest == nil || state.TeamMerge.PullRequest.Number != u.Repair.PullRequest {
		return false, fmt.Errorf("run %s is not tracked under pull request #%d, so feedback on it is not answered on this run", state.RunID, u.Repair.PullRequest)
	}
	if state.WorkItemID != u.Repair.WorkItemID {
		return false, fmt.Errorf("run %s landed %s, not %s", state.RunID, state.WorkItemID, u.Repair.WorkItemID)
	}
	if _, answered := state.TeamMerge.Repair(u.Repair.PullRequest, u.Repair.FeedbackID); answered {
		return false, fmt.Errorf("feedback %s on pull request #%d is already answered on run %s", u.Repair.FeedbackID, u.Repair.PullRequest, state.RunID)
	}
	wait := *state.TeamMerge
	wait.Repairs = append(append([]TeamFeedbackRepair(nil), wait.Repairs...), u.Repair)
	if err := wait.Validate(); err != nil {
		return false, fmt.Errorf("run %s cannot record the repair: %w", state.RunID, err)
	}
	state.TeamMerge = &wait
	state.UpdatedAt = u.Repair.DecidedAt
	return true, nil
}

// TeamFeedbackRepairStarted records the new run a decision started. Recording it
// again for the same run changes nothing.
type TeamFeedbackRepairStarted struct {
	LandingCommit string
	PullRequest   int
	FeedbackID    string
	RunID         string
	At            time.Time
}

func (u TeamFeedbackRepairStarted) apply(state *State) (bool, error) {
	if state.TeamMerge == nil || state.TeamMerge.LandingCommit != u.LandingCommit {
		return false, fmt.Errorf("run %s did not land %s, so no repair of it is recorded on it", state.RunID, u.LandingCommit)
	}
	wait := *state.TeamMerge
	wait.Repairs = append([]TeamFeedbackRepair(nil), wait.Repairs...)
	for index, repair := range wait.Repairs {
		if repair.PullRequest != u.PullRequest || repair.FeedbackID != u.FeedbackID {
			continue
		}
		if repair.StartedRunID == u.RunID {
			return false, nil
		}
		if repair.Started() {
			return false, fmt.Errorf("the repair answering %s on pull request #%d already started run %s", u.FeedbackID, u.PullRequest, repair.StartedRunID)
		}
		at := u.At
		repair.StartedRunID, repair.StartedAt = u.RunID, &at
		wait.Repairs[index] = repair
		if err := wait.Validate(); err != nil {
			return false, err
		}
		state.TeamMerge = &wait
		state.UpdatedAt = u.At
		return true, nil
	}
	return false, fmt.Errorf("run %s holds no repair answering %s on pull request #%d", state.RunID, u.FeedbackID, u.PullRequest)
}

// TriageFeedbackRepair is the action a cap refuses when it refuses a repair from
// feedback.
const TriageFeedbackRepair = "repair from feedback"

// RecordFeedbackRepair spends one of the item's attempts for a repair decided
// from feedback, in the same unit and against the same caps as a re-run: the new
// run it pays for is a whole run, repair budget and all. It refuses once the item
// has none left, as a re-run does, and writes the spend before the run it pays
// for is started, so a crash between the two costs an attempt nobody took rather
// than one nobody counted. runID is the run that landed the item, which the spend
// is recorded against.
//
// It records no decision on the item's triage record: that record holds one
// decision per stopped run, and nothing here stopped. The decision is kept on the
// landed run's wait (TeamFeedbackRepairDecided).
func (s *TriageStore) RecordFeedbackRepair(ctx context.Context, workItemID, runID string, at time.Time, caps TriageCaps) (TriageCounters, error) {
	if err := caps.Validate(); err != nil {
		return TriageCounters{}, err
	}
	if !ValidRunID(runID) {
		return TriageCounters{}, fmt.Errorf("%q is not a run identifier; a repair from feedback is spent against the run that landed the item", runID)
	}
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	return s.update(ctx, workItemID, when, func(counters *TriageCounters) error {
		permitted := caps.Overridden(counters.Overrides)
		check := func() error {
			var refusals []TriageCapRefusal
			if counters.Reruns >= permitted.Reruns {
				refusals = append(refusals, TriageCapRefusal{Budget: TriageRerunBudget, Spent: triageBudgetSpend(*counters, TriageRerunBudget), Cap: permitted.Reruns})
			}
			if counters.RoundsUncommitted(permitted.ReviewRounds) == 0 {
				refusals = append(refusals, TriageCapRefusal{Budget: TriageReviewRoundBudget, Spent: triageBudgetSpend(*counters, TriageReviewRoundBudget), Cap: permitted.ReviewRounds})
			}
			if len(refusals) > 0 {
				return TriageCapError{Action: TriageFeedbackRepair, WorkItemID: counters.WorkItemID, Refusals: refusals}
			}
			return nil
		}
		if err := s.spendShared(ctx, workItemID, counters, SharedSpend{Budget: TriageRerunBudget, Run: runID}, check); err != nil {
			return err
		}
		counters.Reruns++
		return nil
	})
}
